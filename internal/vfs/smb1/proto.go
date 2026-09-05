// Package smb1 ist ein schlanker Client für SMB1 (Dialekt "NT LM 0.12").
//
// Warum es das überhaupt gibt: Manche Router - der Speedport gehört dazu -
// bieten ihre USB-Freigabe ausschließlich über SMB1 an. Windows und iOS haben
// dieses Protokoll entfernt, weshalb die Freigabe dort schlicht nicht mehr
// erscheint. Ohne diesen Code wäre der Speicher für SpeedNAS ebenso
// unerreichbar.
//
// SMB1 ist veraltet und hat bekannte Schwächen; siehe docs/smb1.md. Der
// Client wird deshalb nur benutzt, wenn er ausdrücklich eingeschaltet wird.
//
// Umgesetzt ist genau so viel des Protokolls, wie ein Dateibrowser braucht -
// kein vollständiges SMB1.
package smb1

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	"unicode/utf16"
)

// SMB1-Kommandos (MS-CIFS 2.2.2.1).
const (
	cmdCreateDirectory  = 0x00
	cmdDeleteDirectory  = 0x01
	cmdClose            = 0x04
	cmdDelete           = 0x06
	cmdRename           = 0x07
	cmdEcho             = 0x2B
	cmdReadAndX         = 0x2E
	cmdWriteAndX        = 0x2F
	cmdTransaction2     = 0x32
	cmdTreeDisconnect   = 0x71
	cmdNegotiate        = 0x72
	cmdSessionSetupAndX = 0x73
	cmdLogoffAndX       = 0x74
	cmdTreeConnectAndX  = 0x75
	cmdNTCreateAndX     = 0xA2
)

// Kopf-Flags (MS-CIFS 2.2.3.1).
const (
	flagCaseInsensitive = 0x08
	flagCanonicalPaths  = 0x10

	flags2LongNames   = 0x0001
	flags2EAS         = 0x0002
	flags2IsLongName  = 0x0010
	flags2ExtSecurity = 0x0800
	flags2NTStatus    = 0x4000
	flags2Unicode     = 0x8000
)

// Fehlercodes, die wir unterscheiden müssen.
const (
	statusSuccess              = 0x00000000
	statusMoreProcessing       = 0xC0000016
	statusNoSuchFile           = 0xC000000F
	statusObjectNameNotFound   = 0xC0000034
	statusObjectPathNotFound   = 0xC000003A
	statusObjectNameCollision  = 0xC0000035
	statusAccessDenied         = 0xC0000022
	statusNoMoreFiles          = 0x80000006
	statusLogonFailure         = 0xC000006D
	statusBadNetworkName       = 0xC00000CC
	statusNotADirectory        = 0xC0000103
	statusFileIsADirectory     = 0xC00000BA
	statusDirectoryNotEmpty    = 0xC0000101
	statusSharingViolation     = 0xC0000043
	statusInsufficientResource = 0xC000009A
)

// Fehler, die der Aufrufer unterscheiden können muss.
var (
	ErrNotFound     = errors.New("smb1: nicht gefunden")
	ErrExists       = errors.New("smb1: existiert bereits")
	ErrPermission   = errors.New("smb1: zugriff verweigert")
	ErrNotSupported = errors.New("smb1: nicht unterstützt")
	ErrShareUnknown = errors.New("smb1: freigabename unbekannt")
	ErrAuth         = errors.New("smb1: anmeldung fehlgeschlagen")
)

// StatusError trägt den rohen NT-Status, falls jemand genauer hinsehen will.
type StatusError struct {
	Status uint32
	Op     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("smb1: %s fehlgeschlagen (NTSTATUS 0x%08x)", e.Op, e.Status)
}

// mapStatus übersetzt NT-Status in die Fehler oben.
func mapStatus(op string, status uint32) error {
	switch status {
	case statusSuccess:
		return nil
	case statusNoSuchFile, statusObjectNameNotFound, statusObjectPathNotFound:
		return ErrNotFound
	case statusObjectNameCollision:
		return ErrExists
	case statusAccessDenied, statusSharingViolation:
		return ErrPermission
	case statusLogonFailure:
		return ErrAuth
	case statusBadNetworkName:
		return ErrShareUnknown
	}
	return &StatusError{Status: status, Op: op}
}

// header ist der 32 Byte große SMB1-Kopf.
type header struct {
	command uint8
	status  uint32
	flags   uint8
	flags2  uint16
	tid     uint16
	pid     uint16
	uid     uint16
	mid     uint16
}

func (h *header) encode() []byte {
	b := make([]byte, 32)
	copy(b, []byte{0xFF, 'S', 'M', 'B'})
	b[4] = h.command
	binary.LittleEndian.PutUint32(b[5:], h.status)
	b[9] = h.flags
	binary.LittleEndian.PutUint16(b[10:], h.flags2)
	binary.LittleEndian.PutUint16(b[24:], h.tid)
	binary.LittleEndian.PutUint16(b[26:], h.pid)
	binary.LittleEndian.PutUint16(b[28:], h.uid)
	binary.LittleEndian.PutUint16(b[30:], h.mid)
	return b
}

func parseHeader(b []byte) (header, error) {
	var h header
	if len(b) < 32 {
		return h, errors.New("smb1: antwort zu kurz für den kopf")
	}
	if b[0] != 0xFF || b[1] != 'S' || b[2] != 'M' || b[3] != 'B' {
		return h, errors.New("smb1: keine SMB1-antwort")
	}
	h.command = b[4]
	h.status = binary.LittleEndian.Uint32(b[5:])
	h.flags = b[9]
	h.flags2 = binary.LittleEndian.Uint16(b[10:])
	h.tid = binary.LittleEndian.Uint16(b[24:])
	h.pid = binary.LittleEndian.Uint16(b[26:])
	h.uid = binary.LittleEndian.Uint16(b[28:])
	h.mid = binary.LittleEndian.Uint16(b[30:])
	return h, nil
}

// request ist eine Nachricht: Parameterwörter plus Datenteil.
type request struct {
	command uint8
	words   []byte // muss eine gerade Länge haben
	data    []byte
}

// response ist die zerlegte Antwort. raw bleibt erhalten, weil TRANS2
// seine Offsets ab dem Anfang der Nachricht zählt.
type response struct {
	hdr   header
	words []byte
	data  []byte
	raw   []byte
}

// Options steuert den Verbindungsaufbau.
type Options struct {
	Host        string
	Port        int
	Share       string
	User        string
	Password    string
	Domain      string
	DialTimeout time.Duration
	IOTimeout   time.Duration
}

func (o *Options) addr() string {
	port := o.Port
	if port == 0 {
		port = 445
	}
	return net.JoinHostPort(o.Host, fmt.Sprint(port))
}

// Client ist eine SMB1-Verbindung mit angemeldeter Sitzung und verbundener
// Freigabe. Er ist nicht nebenläufig nutzbar - der Verbindungspool von
// SpeedNAS sorgt dafür, dass immer nur eine Goroutine darauf arbeitet.
type Client struct {
	conn net.Conn
	opts Options

	// mu macht den Client für nebenläufige Aufrufe sicher. SMB1 hat pro
	// Verbindung immer nur eine Anfrage unterwegs; ohne diese Sperre würden
	// sich zwei Goroutinen ihre Antworten gegenseitig wegschnappen.
	mu  sync.Mutex
	mid uint16

	uid uint16
	tid uint16

	maxBuffer  uint32
	challenge  []byte
	unicode    bool
	extSec     bool
	wantExtSec bool
	closed     bool
}

// nextMID vergibt die Nachrichtenkennung.
func (c *Client) nextMID() uint16 {
	c.mid++
	if c.mid == 0 {
		c.mid = 1
	}
	return c.mid
}

func (c *Client) flags2() uint16 {
	f := uint16(flags2NTStatus | flags2LongNames | flags2IsLongName | flags2EAS)
	if c.unicode {
		f |= flags2Unicode
	}
	if c.wantExtSec {
		f |= flags2ExtSecurity
	}
	return f
}

// send verschickt eine Nachricht und liest die Antwort.
func (c *Client) send(ctx context.Context, r request) (*response, error) {
	if c.closed {
		return nil, errors.New("smb1: verbindung geschlossen")
	}
	if len(r.words)%2 != 0 {
		return nil, fmt.Errorf("smb1: parameterwörter müssen gerade Länge haben (%d)", len(r.words))
	}

	h := header{
		command: r.command,
		flags:   flagCaseInsensitive | flagCanonicalPaths,
		flags2:  c.flags2(),
		tid:     c.tid,
		pid:     0xFEFF,
		uid:     c.uid,
		mid:     c.nextMID(),
	}

	msg := make([]byte, 0, 32+1+len(r.words)+2+len(r.data))
	msg = append(msg, h.encode()...)
	msg = append(msg, byte(len(r.words)/2))
	msg = append(msg, r.words...)
	var bc [2]byte
	binary.LittleEndian.PutUint16(bc[:], uint16(len(r.data)))
	msg = append(msg, bc[:]...)
	msg = append(msg, r.data...)

	if err := c.setDeadline(ctx); err != nil {
		return nil, err
	}
	if err := writeNBT(c.conn, msg); err != nil {
		return nil, fmt.Errorf("smb1: senden fehlgeschlagen: %w", err)
	}
	raw, err := readNBT(c.conn)
	if err != nil {
		return nil, fmt.Errorf("smb1: antwort nicht lesbar: %w", err)
	}
	return parseResponse(raw)
}

func parseResponse(raw []byte) (*response, error) {
	hdr, err := parseHeader(raw)
	if err != nil {
		return nil, err
	}
	if len(raw) < 33 {
		return nil, errors.New("smb1: antwort ohne wortzähler")
	}
	wordCount := int(raw[32])
	wordsEnd := 33 + wordCount*2
	if len(raw) < wordsEnd+2 {
		// Fehlerantworten kommen häufig ganz ohne Datenteil.
		if hdr.status != statusSuccess {
			return &response{hdr: hdr, raw: raw}, nil
		}
		return nil, errors.New("smb1: antwort zu kurz für die parameterwörter")
	}
	byteCount := int(binary.LittleEndian.Uint16(raw[wordsEnd:]))
	dataStart := wordsEnd + 2
	dataEnd := dataStart + byteCount
	if dataEnd > len(raw) {
		dataEnd = len(raw) // manche Server geben einen zu großen Zähler an
	}
	return &response{
		hdr:   hdr,
		words: raw[33:wordsEnd],
		data:  raw[dataStart:dataEnd],
		raw:   raw,
	}, nil
}

func (c *Client) setDeadline(ctx context.Context) error {
	timeout := c.opts.IOTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return c.conn.SetDeadline(deadline)
}

// Close beendet Sitzung und Verbindung. Die Sperre gehört auch hierhin:
// sonst könnte das Schließen mitten in eine laufende Anfrage einer anderen
// Goroutine fallen und ihr den Socket unter den Füßen wegziehen.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	// Der Abschied vom Share geht noch über die offene Verbindung, deshalb
	// erst danach die Markierung setzen.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c.tid != 0 {
		_, _ = c.send(ctx, request{command: cmdTreeDisconnect})
	}
	c.closed = true
	return c.conn.Close()
}

// ---------------------------------------------------------- NetBIOS -------

// writeNBT verpackt die Nachricht in den NetBIOS-Session-Rahmen, den SMB auf
// Port 445 verwendet.
func writeNBT(conn net.Conn, msg []byte) error {
	if len(msg) > 0xFFFFFF {
		return errors.New("smb1: nachricht zu groß")
	}
	h := []byte{0x00, byte(len(msg) >> 16), byte(len(msg) >> 8), byte(len(msg))}
	if _, err := conn.Write(h); err != nil {
		return err
	}
	_, err := conn.Write(msg)
	return err
}

func readNBT(conn net.Conn) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(conn, h[:]); err != nil {
		return nil, err
	}
	n := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
	if n <= 0 || n > 16<<20 {
		return nil, fmt.Errorf("smb1: unplausible länge %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// ------------------------------------------------------ Zeichenketten -----

// encodeString liefert einen Namen in der ausgehandelten Kodierung,
// abgeschlossen mit einem Nullzeichen.
func (c *Client) encodeString(s string) []byte {
	if !c.unicode {
		return append([]byte(s), 0)
	}
	return utf16le(s)
}

// utf16le kodiert nach UTF-16LE mit abschließendem Nullzeichen.
func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2+2)
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[i*2:], r)
	}
	return b
}

// decodeUTF16 liest UTF-16LE fester Länge.
func decodeUTF16(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

// smbPath wandelt einen kanonischen Pfad ("a/b") in die SMB-Form ("\a\b").
func smbPath(p string) string {
	out := make([]rune, 0, len(p)+1)
	out = append(out, '\\')
	for _, r := range p {
		if r == '/' {
			r = '\\'
		}
		out = append(out, r)
	}
	// Ein einzelner Backslash ist die Wurzel; doppelte vermeiden.
	s := string(out)
	for len(s) > 1 && s[len(s)-1] == '\\' {
		s = s[:len(s)-1]
	}
	return s
}

// filetimeToTime wandelt einen Windows-Zeitstempel (100-ns-Schritte seit 1601)
// in Go-Zeit.
func filetimeToTime(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	const offset = 116444736000000000 // 1601-01-01 bis 1970-01-01
	if ft < offset {
		return time.Time{}
	}
	ns := (int64(ft) - offset) * 100
	return time.Unix(0, ns)
}
