package server

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/thumb"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

// handleRaw liefert eine Datei zur Anzeige im Browser (Bild, Video, PDF).
func (a *App) handleRaw(w http.ResponseWriter, r *http.Request, id Identity) {
	a.serveFile(w, r, r.URL.Query().Get("loc"), vfs.Clean(r.URL.Query().Get("path")), false)
}

// handleDownload liefert eine Datei als Download.
func (a *App) handleDownload(w http.ResponseWriter, r *http.Request, id Identity) {
	a.serveFile(w, r, r.URL.Query().Get("loc"), vfs.Clean(r.URL.Query().Get("path")), true)
}

func (a *App) serveFile(w http.ResponseWriter, r *http.Request, locID, p string, attachment bool) {
	c, _, ok := a.clientFor(w, locID)
	if !ok {
		return
	}
	e, err := c.Stat(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	if e.IsDir {
		failWith(w, http.StatusBadRequest, "Das ist ein Ordner - bitte als ZIP herunterladen.")
		return
	}
	a.streamEntry(w, r, c, p, e, attachment)
}

// streamEntry beantwortet die Anfrage inklusive Range-Unterstützung.
//
// Range ist hier nicht optional: ohne sie kann Safari auf dem iPhone kein
// Video abspielen und nicht springen.
func (a *App) streamEntry(w http.ResponseWriter, r *http.Request, c *vfs.Client, p string, e vfs.Entry, attachment bool) {
	name := e.Name
	if name == "" {
		name = vfs.Base(p)
	}
	ct := thumb.MimeType(name)
	disp := "inline"
	if attachment || !thumb.InlineSafe(name) {
		disp = "attachment"
	}

	etag := fmt.Sprintf("W/\"%x-%x\"", e.Size, e.ModTime.UnixNano())
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", contentDisposition(disp, name))
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", etag)
	h.Set("Last-Modified", e.ModTime.UTC().Format(http.TimeFormat))
	// Privat, aber im Browser zwischenspeicherbar: spart beim Blättern durch
	// eine Bildergalerie jede Menge Netzverkehr.
	h.Set("Cache-Control", "private, max-age=600")

	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" && !e.ModTime.IsZero() {
		if t, err := http.ParseTime(ims); err == nil && !e.ModTime.Truncate(time.Second).After(t) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	start, end, partial, err := parseRange(r.Header.Get("Range"), e.Size)
	if err != nil {
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", e.Size))
		failWith(w, http.StatusRequestedRangeNotSatisfiable, "Ungültiger Bereich.")
		return
	}
	length := end - start + 1

	if r.Method == http.MethodHead {
		h.Set("Content-Length", strconv.FormatInt(e.Size, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	rc, _, err := c.StreamAt(r.Context(), p, start, a.prefetchOpts())
	if err != nil {
		fail(w, err)
		return
	}
	defer rc.Close()

	h.Set("Content-Length", strconv.FormatInt(length, 10))
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, e.Size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	n, _ := io.Copy(w, io.LimitReader(rc, length))
	a.bytesOut.Add(n)
}

// parseRange versteht eine einzelne Bereichsangabe. Mehrfachbereiche sind bei
// Medienwiedergabe unüblich; dafür liefern wir die ganze Datei.
func parseRange(header string, size int64) (start, end int64, partial bool, err error) {
	if header == "" || size <= 0 {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, size - 1, false, nil
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, size - 1, false, nil
	}
	i := strings.IndexByte(spec, '-')
	if i < 0 {
		return 0, 0, false, fmt.Errorf("ungültig")
	}
	first, last := strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])

	switch {
	case first == "" && last == "":
		return 0, 0, false, fmt.Errorf("ungültig")
	case first == "": // Suffix: die letzten N Bytes
		n, e := strconv.ParseInt(last, 10, 64)
		if e != nil || n <= 0 {
			return 0, 0, false, fmt.Errorf("ungültig")
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, nil
	default:
		s, e := strconv.ParseInt(first, 10, 64)
		if e != nil || s < 0 || s >= size {
			return 0, 0, false, fmt.Errorf("außerhalb")
		}
		en := size - 1
		if last != "" {
			v, e := strconv.ParseInt(last, 10, 64)
			if e != nil || v < s {
				return 0, 0, false, fmt.Errorf("ungültig")
			}
			if v < en {
				en = v
			}
		}
		return s, en, true, nil
	}
}

// ------------------------------------------------------------ ZIP -----

type zipItem struct {
	path string
	name string
}

// handleZip packt einen Ordner oder eine Auswahl im Vorbeigehen in ein ZIP.
// Nichts wird zwischengespeichert: der Download startet sofort.
func (a *App) handleZip(w http.ResponseWriter, r *http.Request, id Identity) {
	locID := r.URL.Query().Get("loc")
	base := vfs.Clean(r.URL.Query().Get("path"))
	c, loc, ok := a.clientFor(w, locID)
	if !ok {
		return
	}

	var items []string
	if raw := r.URL.Query().Get("items"); raw != "" {
		for _, n := range strings.Split(raw, "\n") {
			if n = strings.TrimSpace(n); n != "" {
				items = append(items, n)
			}
		}
	}

	archiveName := "download"
	if len(items) == 1 {
		archiveName = vfs.SafeFileName(items[0])
	} else if base != "" {
		archiveName = vfs.SafeFileName(vfs.Base(base))
	} else if loc.Label != "" {
		archiveName = vfs.SafeFileName(loc.Label)
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", archiveName+".zip"))
	w.Header().Set("Cache-Control", "no-store")
	// Die Größe steht vorher nicht fest; der Browser zeigt dann eben keinen
	// Fortschrittsbalken, dafür startet der Download sofort.
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	zw := zip.NewWriter(w)
	defer zw.Close()

	var addFile func(path, name string) error
	addFile = func(path, name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := c.Stat(ctx, path)
		if err != nil {
			return err
		}
		if e.IsDir {
			entries, err := c.List(ctx, path)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				// Leere Ordner als eigenen Eintrag anlegen, damit sie im
				// Archiv nicht verschwinden - mit Zeitstempel, sonst zeigen
				// Entpacker "1980".
				_, _ = zw.CreateHeader(&zip.FileHeader{Name: name + "/", Modified: e.ModTime})
				return nil
			}
			for _, child := range entries {
				if err := addFile(vfs.Join(path, child.Name), name+"/"+child.Name); err != nil {
					return err
				}
			}
			return nil
		}
		hdr := &zip.FileHeader{Name: name, Modified: e.ModTime}
		// Bereits komprimierte Formate nicht noch einmal durch Deflate
		// schicken: das kostet nur Rechenzeit und bringt nichts.
		if isPrecompressed(name) {
			hdr.Method = zip.Store
		} else {
			hdr.Method = zip.Deflate
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		rc, _, err := c.StreamAt(ctx, path, 0, a.prefetchOpts())
		if err != nil {
			return err
		}
		defer rc.Close()
		n, err := io.Copy(fw, rc)
		a.bytesOut.Add(n)
		return err
	}

	if len(items) == 0 {
		entries, err := c.List(ctx, base)
		if err != nil {
			return
		}
		for _, e := range entries {
			items = append(items, e.Name)
		}
	}
	for _, it := range items {
		name := vfs.Base(it)
		if err := addFile(vfs.Join(base, it), name); err != nil {
			// Der Kopf ist längst raus - mehr als abbrechen geht nicht.
			return
		}
	}
}

func isPrecompressed(name string) bool {
	switch thumb.Ext(name) {
	case "jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "avif",
		"mp4", "m4v", "mov", "mkv", "webm", "avi",
		"mp3", "m4a", "aac", "flac", "ogg", "opus",
		"zip", "7z", "rar", "gz", "bz2", "xz", "tgz", "pdf", "docx", "xlsx", "pptx":
		return true
	}
	return false
}

// ----------------------------------------------------- Vorschaubild -----

func (a *App) handleThumb(w http.ResponseWriter, r *http.Request, id Identity) {
	locID := r.URL.Query().Get("loc")
	p := vfs.Clean(r.URL.Query().Get("path"))
	c, _, ok := a.clientFor(w, locID)
	if !ok {
		return
	}
	size := queryInt(r, "w", 320)
	switch {
	case size <= 160:
		size = 160
	case size <= 320:
		size = 320
	case size <= 640:
		size = 640
	default:
		size = 1024
	}

	e, err := c.Stat(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	key := thumb.Key(locID, p, e.Size, e.ModTime, size)

	etag := "\"" + key[:16] + "\""
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	data, err := a.thumbs.Build(ctx, key, e.Name, func() (io.ReadCloser, error) {
		rc, _, err := c.StreamAt(ctx, p, 0, a.prefetchOpts())
		return rc, err
	}, thumb.Options{MaxDim: size})
	if err != nil {
		// Kein Drama: die Oberfläche zeigt dann das Dateisymbol.
		failWith(w, http.StatusNotFound, "Keine Vorschau verfügbar.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// -------------------------------------------------------- Texteditor -----

const maxTextEdit = 4 << 20

func (a *App) handleTextRead(w http.ResponseWriter, r *http.Request, id Identity) {
	c, _, ok := a.clientFor(w, r.URL.Query().Get("loc"))
	if !ok {
		return
	}
	p := vfs.Clean(r.URL.Query().Get("path"))
	e, err := c.Stat(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	if e.Size > maxTextEdit {
		failWith(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("Datei ist zu groß für den Editor (%s).", humanBytes(e.Size)))
		return
	}
	rc, err := c.Reader(r.Context(), p, 0)
	if err != nil {
		fail(w, err)
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxTextEdit+1))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    p,
		"content": string(data),
		"size":    e.Size,
		"mtime":   e.ModTime,
	})
}

type textWriteRequest struct {
	Loc     string `json:"loc"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (a *App) handleTextWrite(w http.ResponseWriter, r *http.Request, id Identity) {
	var req textWriteRequest
	if err := decodeBody(w, r, &req, maxTextEdit+(1<<16)); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	c, loc, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}
	p := vfs.Clean(req.Path)
	n, err := c.Write(r.Context(), p, strings.NewReader(req.Content), int64(len(req.Content)))
	if err != nil {
		fail(w, err)
		return
	}
	a.bytesIn.Add(n)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "size": n})
}
