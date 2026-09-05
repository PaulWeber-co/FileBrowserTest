package smb1

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Dateiattribute und Zugriffsrechte (MS-CIFS / MS-SMB).
const (
	attrDirectory = 0x00000010

	accessRead  = 0x00120089 // FILE_READ_DATA|ATTRIBUTES|EA|READ_CONTROL|SYNCHRONIZE
	accessWrite = 0x00120116 // FILE_WRITE_DATA|APPEND|ATTRIBUTES|EA|SYNCHRONIZE

	shareRead   = 0x00000001
	shareWrite  = 0x00000002
	shareDelete = 0x00000004
	shareAll    = shareRead | shareWrite | shareDelete

	dispOpen      = 1 // nur öffnen, wenn vorhanden
	dispCreate    = 2 // nur anlegen, wenn nicht vorhanden
	dispOverwrite = 5 // anlegen oder überschreiben

	optDirectory    = 0x00000001
	optNonDirectory = 0x00000040

	impersonation = 2
)

// FileInfo beschreibt einen Eintrag.
type FileInfo struct {
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
}

// List liest ein Verzeichnis vollständig aus.
func (c *Client) List(ctx context.Context, path string) ([]FileInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pattern := smbPath(path)
	if pattern == "\\" {
		pattern = "\\*"
	} else {
		pattern += "\\*"
	}

	// FIND_FIRST2
	params := make([]byte, 12)
	binary.LittleEndian.PutUint16(params[0:], attrDirectory|0x06) // versteckt und System mitnehmen
	binary.LittleEndian.PutUint16(params[2:], 512)                // gewünschte Trefferzahl
	binary.LittleEndian.PutUint16(params[4:], 0x0006)             // Resume-Keys, bei Ende schließen
	binary.LittleEndian.PutUint16(params[6:], infoFileBothDirectory)
	params = append(params, c.encodeString(pattern)...)

	res, err := c.trans2(ctx, trans2FindFirst2, params, nil)
	if err != nil {
		return nil, err
	}
	if len(res.params) < 6 {
		return nil, errors.New("smb1: FIND_FIRST2 lieferte keine Kopfdaten")
	}
	sid := binary.LittleEndian.Uint16(res.params[0:])
	endOfSearch := binary.LittleEndian.Uint16(res.params[4:]) != 0

	out, last, err := parseDirEntries(res.data)
	if err != nil {
		return nil, err
	}

	// FIND_NEXT2, solange der Server noch etwas hat.
	for round := 0; !endOfSearch && round < 4096; round++ {
		np := make([]byte, 12)
		binary.LittleEndian.PutUint16(np[0:], sid)
		binary.LittleEndian.PutUint16(np[2:], 512)
		binary.LittleEndian.PutUint16(np[4:], infoFileBothDirectory)
		binary.LittleEndian.PutUint32(np[6:], 0)       // ResumeKey
		binary.LittleEndian.PutUint16(np[10:], 0x000E) // fortsetzen, Resume-Keys, bei Ende schließen
		np = append(np, c.encodeString(last)...)

		nres, err := c.trans2(ctx, trans2FindNext2, np, nil)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				break // manche Server melden so das Ende
			}
			return nil, err
		}
		if len(nres.params) < 4 {
			break
		}
		count := binary.LittleEndian.Uint16(nres.params[0:])
		endOfSearch = binary.LittleEndian.Uint16(nres.params[2:]) != 0
		if count == 0 {
			break
		}
		more, l, err := parseDirEntries(nres.data)
		if err != nil {
			return nil, err
		}
		if len(more) == 0 {
			break
		}
		out = append(out, more...)
		last = l
	}

	// "." und ".." gehören nicht in die Auflistung.
	filtered := out[:0]
	for _, e := range out {
		if e.Name == "." || e.Name == ".." || e.Name == "" {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

// parseDirEntries zerlegt SMB_FIND_FILE_BOTH_DIRECTORY_INFO-Einträge.
func parseDirEntries(b []byte) ([]FileInfo, string, error) {
	const headLen = 94 // feste Felder vor dem Dateinamen
	var out []FileInfo
	var last string

	for off := 0; off+headLen <= len(b); {
		next := int(binary.LittleEndian.Uint32(b[off:]))
		writeTime := binary.LittleEndian.Uint64(b[off+24:])
		size := int64(binary.LittleEndian.Uint64(b[off+40:]))
		attrs := binary.LittleEndian.Uint32(b[off+56:])
		nameLen := int(binary.LittleEndian.Uint32(b[off+60:]))

		nameStart := off + headLen
		nameEnd := nameStart + nameLen
		if nameLen < 0 || nameEnd > len(b) {
			break
		}
		name := decodeUTF16(b[nameStart:nameEnd])

		out = append(out, FileInfo{
			Name:    name,
			Size:    size,
			IsDir:   attrs&attrDirectory != 0,
			ModTime: filetimeToTime(writeTime),
		})
		last = name

		if next <= 0 {
			break
		}
		off += next
	}
	return out, last, nil
}

// Stat liefert Angaben zu einem einzelnen Pfad.
func (c *Client) Stat(ctx context.Context, path string) (FileInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name := smbPath(path)
	if name == "\\" {
		// Die Wurzel hat keine eigenen Angaben; sie ist immer ein Verzeichnis.
		return FileInfo{Name: "/", IsDir: true}, nil
	}

	params := make([]byte, 6)
	binary.LittleEndian.PutUint16(params[0:], infoQueryFileAllInfo)
	params = append(params, c.encodeString(name)...)

	res, err := c.trans2(ctx, trans2QueryPathInfo, params, nil)
	if err != nil {
		return FileInfo{}, err
	}
	d := res.data
	if len(d) < 40 {
		return FileInfo{}, errors.New("smb1: Antwort auf die Pfadabfrage zu kurz")
	}
	writeTime := binary.LittleEndian.Uint64(d[16:])
	attrs := binary.LittleEndian.Uint32(d[32:])
	var size int64
	if len(d) >= 56 {
		size = int64(binary.LittleEndian.Uint64(d[48:]))
	}
	return FileInfo{
		Name:    baseName(path),
		Size:    size,
		IsDir:   attrs&attrDirectory != 0,
		ModTime: filetimeToTime(writeTime),
	}, nil
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// File ist ein geöffnetes Handle.
type File struct {
	c    *Client
	fid  uint16
	size int64
}

// Size liefert die beim Öffnen gemeldete Dateigröße.
func (f *File) Size() int64 { return f.size }

// open öffnet oder erzeugt eine Datei bzw. ein Verzeichnis.
func (c *Client) open(ctx context.Context, path string, access, disposition, options uint32) (*File, error) {
	name := smbPath(path)
	// Der führende Backslash gehört bei NT_CREATE nicht dazu.
	rel := name
	if len(rel) > 0 && rel[0] == '\\' {
		rel = rel[1:]
	}
	enc := c.encodeString(rel)
	nameLen := len(enc)
	if c.unicode {
		nameLen -= 2 // die Länge zählt ohne das abschließende Nullzeichen
	} else {
		nameLen--
	}
	if nameLen < 0 {
		nameLen = 0
	}

	words := make([]byte, 48)
	words[0] = 0xFF // AndXCommand
	binary.LittleEndian.PutUint16(words[5:], uint16(nameLen))
	binary.LittleEndian.PutUint32(words[7:], 0)  // Flags
	binary.LittleEndian.PutUint32(words[11:], 0) // RootDirectoryFID
	binary.LittleEndian.PutUint32(words[15:], access)
	binary.LittleEndian.PutUint64(words[19:], 0)    // AllocationSize
	binary.LittleEndian.PutUint32(words[27:], 0x80) // FILE_ATTRIBUTE_NORMAL
	binary.LittleEndian.PutUint32(words[31:], shareAll)
	binary.LittleEndian.PutUint32(words[35:], disposition)
	binary.LittleEndian.PutUint32(words[39:], options)
	binary.LittleEndian.PutUint32(words[43:], impersonation)
	words[47] = 0 // SecurityFlags

	data := make([]byte, 0, len(enc)+1)
	if c.unicode && (32+1+len(words)+2)%2 != 0 {
		data = append(data, 0x00)
	}
	data = append(data, enc...)

	resp, err := c.send(ctx, request{command: cmdNTCreateAndX, words: words, data: data})
	if err != nil {
		return nil, err
	}
	if err := mapStatus("öffnen", resp.hdr.status); err != nil {
		return nil, err
	}
	if len(resp.words) < 63 {
		return nil, errors.New("smb1: Antwort auf das Öffnen zu kurz")
	}
	f := &File{
		c:    c,
		fid:  binary.LittleEndian.Uint16(resp.words[5:]),
		size: int64(binary.LittleEndian.Uint64(resp.words[55:])),
	}
	return f, nil
}

// Open öffnet eine vorhandene Datei zum Lesen.
func (c *Client) Open(ctx context.Context, path string) (*File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open(ctx, path, accessRead, dispOpen, optNonDirectory)
}

// Create legt eine Datei an bzw. überschreibt sie.
func (c *Client) Create(ctx context.Context, path string) (*File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open(ctx, path, accessWrite, dispOverwrite, optNonDirectory)
}

// Close gibt das Handle frei.
func (f *File) Close(ctx context.Context) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	return f.closeLocked(ctx)
}

// closeLocked ist Close ohne die Sperre. Wer die Sperre schon hält, muss
// diese Fassung nehmen - sync.Mutex ist nicht wiedereintrittsfähig, ein
// zweites Lock in derselben Goroutine würde den Aufruf für immer anhalten.
func (f *File) closeLocked(ctx context.Context) error {
	words := make([]byte, 6)
	binary.LittleEndian.PutUint16(words[0:], f.fid)
	binary.LittleEndian.PutUint32(words[2:], 0xFFFFFFFF) // Zeitstempel nicht ändern
	resp, err := f.c.send(ctx, request{command: cmdClose, words: words})
	if err != nil {
		return err
	}
	return mapStatus("schließen", resp.hdr.status)
}

// maxRead ist die größte Menge, die eine Leseanfrage liefern kann.
func (c *Client) maxRead() int {
	n := int(c.maxBuffer) - 512 // Platz für den Antwortkopf
	if n < 4096 {
		n = 4096
	}
	if n > 60000 {
		// SMB1 überträgt die Länge in 16 Bit; darüber wird es unzuverlässig.
		n = 60000
	}
	return n
}

// ReadAt liest ab einer festen Position.
func (f *File) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	gelesen := 0
	for gelesen < len(p) {
		chunk := len(p) - gelesen
		if m := f.c.maxRead(); chunk > m {
			chunk = m
		}
		n, err := f.readOnce(ctx, p[gelesen:gelesen+chunk], off+int64(gelesen))
		gelesen += n
		if err != nil {
			return gelesen, err
		}
		if n == 0 {
			return gelesen, io.EOF
		}
	}
	return gelesen, nil
}

func (f *File) readOnce(ctx context.Context, p []byte, off int64) (int, error) {
	words := make([]byte, 24)
	words[0] = 0xFF // AndXCommand
	binary.LittleEndian.PutUint16(words[4:], f.fid)
	binary.LittleEndian.PutUint32(words[6:], uint32(off))
	binary.LittleEndian.PutUint16(words[10:], uint16(len(p)))     // MaxCount
	binary.LittleEndian.PutUint16(words[12:], 0)                  // MinCount
	binary.LittleEndian.PutUint32(words[14:], uint32(len(p)>>16)) // MaxCountHigh
	binary.LittleEndian.PutUint16(words[18:], 0)                  // Remaining
	binary.LittleEndian.PutUint32(words[20:], uint32(off>>32))    // OffsetHigh

	resp, err := f.c.send(ctx, request{command: cmdReadAndX, words: words})
	if err != nil {
		return 0, err
	}
	if resp.hdr.status == statusSuccess && len(resp.words) < 24 {
		return 0, errors.New("smb1: Leseantwort zu kurz")
	}
	if err := mapStatus("lesen", resp.hdr.status); err != nil {
		return 0, err
	}
	length := int(binary.LittleEndian.Uint16(resp.words[10:]))
	length |= int(binary.LittleEndian.Uint16(resp.words[14:])) << 16
	dataOff := int(binary.LittleEndian.Uint16(resp.words[12:]))
	if length == 0 {
		return 0, io.EOF
	}
	if dataOff < 0 || dataOff+length > len(resp.raw) {
		return 0, fmt.Errorf("smb1: Lesebereich (%d+%d) liegt außerhalb der Antwort", dataOff, length)
	}
	if length > len(p) {
		length = len(p)
	}
	copy(p, resp.raw[dataOff:dataOff+length])
	return length, nil
}

// maxWrite ist die größte Menge pro Schreibanfrage.
func (c *Client) maxWrite() int {
	n := int(c.maxBuffer) - 512
	if n < 4096 {
		n = 4096
	}
	if n > 60000 {
		n = 60000
	}
	return n
}

// WriteAt schreibt ab einer festen Position.
func (f *File) WriteAt(ctx context.Context, p []byte, off int64) (int, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	geschrieben := 0
	for geschrieben < len(p) {
		chunk := len(p) - geschrieben
		if m := f.c.maxWrite(); chunk > m {
			chunk = m
		}
		n, err := f.writeOnce(ctx, p[geschrieben:geschrieben+chunk], off+int64(geschrieben))
		geschrieben += n
		if err != nil {
			return geschrieben, err
		}
		if n == 0 {
			return geschrieben, errors.New("smb1: der Server nahm keine Daten an")
		}
	}
	return geschrieben, nil
}

func (f *File) writeOnce(ctx context.Context, p []byte, off int64) (int, error) {
	words := make([]byte, 28)
	words[0] = 0xFF // AndXCommand
	binary.LittleEndian.PutUint16(words[4:], f.fid)
	binary.LittleEndian.PutUint32(words[6:], uint32(off))
	binary.LittleEndian.PutUint32(words[10:], 0) // Timeout
	binary.LittleEndian.PutUint16(words[14:], 0) // WriteMode
	binary.LittleEndian.PutUint16(words[16:], 0) // Remaining
	binary.LittleEndian.PutUint16(words[18:], uint16(len(p)>>16))
	binary.LittleEndian.PutUint16(words[20:], uint16(len(p)))
	binary.LittleEndian.PutUint32(words[24:], uint32(off>>32))

	// Der Datenoffset zeigt ab dem Anfang der Nachricht auf die Nutzdaten.
	byteStart := 32 + 1 + len(words) + 2
	binary.LittleEndian.PutUint16(words[22:], uint16(byteStart))

	resp, err := f.c.send(ctx, request{command: cmdWriteAndX, words: words, data: p})
	if err != nil {
		return 0, err
	}
	if err := mapStatus("schreiben", resp.hdr.status); err != nil {
		return 0, err
	}
	if len(resp.words) < 6 {
		return 0, errors.New("smb1: Schreibantwort zu kurz")
	}
	n := int(binary.LittleEndian.Uint16(resp.words[4:]))
	if len(resp.words) >= 10 {
		n |= int(binary.LittleEndian.Uint16(resp.words[8:])) << 16
	}
	if n > len(p) {
		n = len(p)
	}
	return n, nil
}

// Mkdir legt ein Verzeichnis an.
func (c *Client) Mkdir(ctx context.Context, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := c.open(ctx, path, accessWrite, dispCreate, optDirectory)
	if err != nil {
		return err
	}
	return f.closeLocked(ctx)
}

// legacyPathCommand baut die alten Befehle mit vorangestelltem Formatbyte.
func (c *Client) legacyPathCommand(ctx context.Context, cmd uint8, words []byte, paths ...string) error {
	data := make([]byte, 0, 128)
	byteStart := 32 + 1 + len(words) + 2
	for _, p := range paths {
		data = append(data, 0x04) // Formatbyte: Zeichenkette folgt
		if c.unicode && (byteStart+len(data))%2 != 0 {
			data = append(data, 0x00)
		}
		data = append(data, c.encodeString(smbPath(p))...)
	}
	resp, err := c.send(ctx, request{command: cmd, words: words, data: data})
	if err != nil {
		return err
	}
	return mapStatus("pfadbefehl", resp.hdr.status)
}

// Remove löscht eine Datei.
func (c *Client) Remove(ctx context.Context, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	words := make([]byte, 2)
	binary.LittleEndian.PutUint16(words, 0x06) // versteckte und System-Dateien einschließen
	return c.legacyPathCommand(ctx, cmdDelete, words, path)
}

// RemoveDir löscht ein leeres Verzeichnis.
func (c *Client) RemoveDir(ctx context.Context, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.legacyPathCommand(ctx, cmdDeleteDirectory, nil, path)
}

// Rename benennt um bzw. verschiebt innerhalb der Freigabe.
func (c *Client) Rename(ctx context.Context, from, to string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	words := make([]byte, 2)
	binary.LittleEndian.PutUint16(words, 0x16) // auch Verzeichnisse und versteckte Dateien
	return c.legacyPathCommand(ctx, cmdRename, words, from, to)
}

// Space liefert Gesamt- und freien Speicherplatz der Freigabe.
func (c *Client) Space(ctx context.Context) (total, free int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	params := make([]byte, 2)
	binary.LittleEndian.PutUint16(params, infoQueryFSSizeInfo)
	res, err := c.trans2(ctx, trans2QueryFSInfo, params, nil)
	if err != nil {
		return 0, 0, err
	}
	if len(res.data) < 24 {
		return 0, 0, errors.New("smb1: Antwort zur Speicherbelegung zu kurz")
	}
	totalUnits := int64(binary.LittleEndian.Uint64(res.data[0:]))
	freeUnits := int64(binary.LittleEndian.Uint64(res.data[8:]))
	sectorsPerUnit := int64(binary.LittleEndian.Uint32(res.data[16:]))
	bytesPerSector := int64(binary.LittleEndian.Uint32(res.data[20:]))
	unit := sectorsPerUnit * bytesPerSector
	return totalUnits * unit, freeUnits * unit, nil
}
