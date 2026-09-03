package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

// upload ist ein angefangener, in Teilen hochgeladener Transfer.
//
// Warum der Umweg über eine Datei auf dem Rechner: der Browser lädt mit
// voller LAN-Geschwindigkeit hoch, während der Speedport nur ein paar MB/s
// wegschreibt. Ohne Puffer bremst der Router jeden Upload-Tab aus; außerdem
// wird ein abgebrochener Upload sonst zur halben Datei auf dem NAS.
type upload struct {
	ID      string
	Loc     string
	Dir     string
	Name    string
	Size    int64
	Mode    string
	Owner   string
	Created time.Time

	mu       sync.Mutex
	file     *os.File
	received map[int64]int64
	got      int64
	closed   bool
}

func (u *upload) markReceived(off, n int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, dup := u.received[off]; !dup {
		u.got += n
	}
	u.received[off] = n
}

func (u *upload) progress() (got int64, parts int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.got, len(u.received)
}

// missing meldet die Bereiche, die noch fehlen - so kann ein abgerissener
// Upload genau dort weitermachen, wo er stehen geblieben ist.
func (u *upload) missing() [][2]int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	offs := make([]int64, 0, len(u.received))
	for o := range u.received {
		offs = append(offs, o)
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })

	var gaps [][2]int64
	var cursor int64
	for _, o := range offs {
		if o > cursor {
			gaps = append(gaps, [2]int64{cursor, o - cursor})
		}
		if end := o + u.received[o]; end > cursor {
			cursor = end
		}
	}
	if cursor < u.Size {
		gaps = append(gaps, [2]int64{cursor, u.Size - cursor})
	}
	return gaps
}

func (u *upload) close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.closed && u.file != nil {
		_ = u.file.Close()
		u.closed = true
	}
}

type uploadStore struct {
	dir string

	mu sync.Mutex
	m  map[string]*upload

	stop     chan struct{}
	stopOnce sync.Once
}

func newUploadStore(dir string) (*uploadStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Reste eines Absturzes wegräumen.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	s := &uploadStore{dir: dir, m: map[string]*upload{}, stop: make(chan struct{})}
	go s.janitor()
	return s, nil
}

func (s *uploadStore) janitor() {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			cutoff := time.Now().Add(-12 * time.Hour)
			s.mu.Lock()
			for id, u := range s.m {
				if u.Created.Before(cutoff) {
					u.close()
					_ = os.Remove(s.path(id))
					delete(s.m, id)
				}
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

func (s *uploadStore) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, u := range s.m {
		u.close()
		_ = os.Remove(s.path(id))
	}
	s.m = map[string]*upload{}
}

func (s *uploadStore) path(id string) string { return filepath.Join(s.dir, id+".part") }

func (s *uploadStore) create(u *upload) error {
	f, err := os.OpenFile(s.path(u.ID), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	u.file = f
	u.received = map[int64]int64{}
	u.Created = time.Now()
	s.mu.Lock()
	s.m[u.ID] = u
	s.mu.Unlock()
	return nil
}

func (s *uploadStore) get(id, owner string) (*upload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.m[id]
	if !ok || u.Owner != owner {
		return nil, false
	}
	return u, true
}

func (s *uploadStore) drop(id string) {
	s.mu.Lock()
	u, ok := s.m[id]
	delete(s.m, id)
	s.mu.Unlock()
	if ok {
		u.close()
	}
	_ = os.Remove(s.path(id))
}

// ------------------------------------------------------- Endpunkte -----

type uploadInitRequest struct {
	Loc  string `json:"loc"`
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mode string `json:"mode"` // overwrite | rename | skip
}

func (a *App) handleUploadInit(w http.ResponseWriter, r *http.Request, id Identity) {
	var req uploadInitRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Size < 0 {
		failWith(w, http.StatusBadRequest, "Ungültige Größe.")
		return
	}
	_, loc, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}
	dir, name, err := splitUploadTarget(req.Path, req.Name)
	if err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	u := &upload{
		ID: config.NewID(), Loc: req.Loc, Dir: dir, Name: name,
		Size: req.Size, Mode: req.Mode, Owner: id.Name,
	}
	if err := a.uploads.create(u); err != nil {
		failWith(w, http.StatusInternalServerError, "Zwischenspeicher nicht beschreibbar: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploadId": u.ID, "partSize": a.cfg.Perf().UploadPartMB << 20})
}

// splitUploadTarget trennt einen relativen Pfad (Ordner-Upload) vom Dateinamen.
func splitUploadTarget(base, name string) (dir, file string, err error) {
	clean := vfs.Clean(name)
	if clean == "" {
		return "", "", fmt.Errorf("Dateiname fehlt")
	}
	file = vfs.Base(clean)
	if !vfs.ValidName(file) {
		return "", "", fmt.Errorf("Ungültiger Dateiname")
	}
	dir = vfs.Join(base, vfs.Dir(clean))
	return dir, file, nil
}

func (a *App) handleUploadPart(w http.ResponseWriter, r *http.Request, id Identity) {
	uid := r.URL.Query().Get("id")
	off, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || off < 0 {
		failWith(w, http.StatusBadRequest, "Ungültiger Offset.")
		return
	}
	u, ok := a.uploads.get(uid, id.Name)
	if !ok {
		failWith(w, http.StatusNotFound, "Unbekannter Upload - bitte neu starten.")
		return
	}
	if u.Size > 0 && off >= u.Size {
		failWith(w, http.StatusBadRequest, "Offset liegt hinter dem Dateiende.")
		return
	}

	buf := make([]byte, 256<<10)
	var written int64
	pos := off
	for {
		n, rerr := r.Body.Read(buf)
		if n > 0 {
			if u.Size > 0 && pos+int64(n) > u.Size {
				failWith(w, http.StatusBadRequest, "Mehr Daten als angekündigt.")
				return
			}
			if _, werr := u.file.WriteAt(buf[:n], pos); werr != nil {
				failWith(w, http.StatusInsufficientStorage, "Zwischenspeicher voll: "+werr.Error())
				return
			}
			pos += int64(n)
			written += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			failWith(w, http.StatusBadRequest, "Übertragung abgebrochen.")
			return
		}
	}
	u.markReceived(off, written)
	a.bytesIn.Add(written)
	got, _ := u.progress()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "received": got})
}

func (a *App) handleUploadStatus(w http.ResponseWriter, r *http.Request, id Identity) {
	u, ok := a.uploads.get(r.URL.Query().Get("id"), id.Name)
	if !ok {
		failWith(w, http.StatusNotFound, "Unbekannter Upload.")
		return
	}
	got, parts := u.progress()
	gaps := u.missing()
	out := make([][2]int64, 0, len(gaps))
	out = append(out, gaps...)
	writeJSON(w, http.StatusOK, map[string]any{
		"received": got, "parts": parts, "size": u.Size, "missing": out,
	})
}

type uploadFinishRequest struct {
	ID string `json:"id"`
}

func (a *App) handleUploadFinish(w http.ResponseWriter, r *http.Request, id Identity) {
	var req uploadFinishRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	u, ok := a.uploads.get(req.ID, id.Name)
	if !ok {
		failWith(w, http.StatusNotFound, "Unbekannter Upload.")
		return
	}

	// Fehlende Teile melden, ohne den Zwischenstand wegzuwerfen - genau
	// dafür ist die Stückelung ja da: der Client schickt nur nach, was
	// fehlt, und ruft finish erneut auf.
	if gaps := u.missing(); len(gaps) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "Es fehlen noch Teile der Datei.", "missing": gaps,
		})
		return
	}
	c, loc, ok := a.clientFor(w, u.Loc)
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	target, skip, err := a.resolveTarget(ctx, c, u.Dir, u.Name, u.Mode)
	if err != nil {
		fail(w, err)
		return
	}
	if skip {
		a.uploads.drop(u.ID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "path": vfs.Join(u.Dir, u.Name)})
		return
	}
	if err := ensureDir(ctx, c, u.Dir); err != nil {
		fail(w, err)
		return
	}
	if _, err := u.file.Seek(0, io.SeekStart); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Schlägt das Schreiben auf den Netzwerkspeicher fehl, bleibt der
	// Zwischenstand liegen: dann genügt ein erneutes finish, statt die
	// ganze Datei noch einmal hochzuladen. Der Aufräumdienst entfernt
	// liegengebliebene Stände nach zwölf Stunden.
	if _, err := c.Write(ctx, target, u.file, u.Size); err != nil {
		fail(w, err)
		return
	}
	a.uploads.drop(u.ID)
	c.Invalidate(u.Dir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target, "name": vfs.Base(target)})
}

func (a *App) handleUploadAbort(w http.ResponseWriter, r *http.Request, id Identity) {
	var req uploadFinishRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := a.uploads.get(req.ID, id.Name); ok {
		a.uploads.drop(req.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUploadDirect nimmt kleine Dateien in einem Rutsch entgegen - für die
// lohnt der Umweg über Teile nicht.
func (a *App) handleUploadDirect(w http.ResponseWriter, r *http.Request, id Identity) {
	q := r.URL.Query()
	c, loc, ok := a.clientFor(w, q.Get("loc"))
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}
	dir, name, err := splitUploadTarget(q.Get("path"), q.Get("name"))
	if err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	target, skip, err := a.resolveTarget(ctx, c, dir, name, q.Get("mode"))
	if err != nil {
		fail(w, err)
		return
	}
	if skip {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true})
		return
	}
	if err := ensureDir(ctx, c, dir); err != nil {
		fail(w, err)
		return
	}
	size := int64(-1)
	if v := r.Header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			size = n
		}
	}
	n, err := c.Write(ctx, target, r.Body, size)
	if err != nil {
		fail(w, err)
		return
	}
	a.bytesIn.Add(n)
	c.Invalidate(dir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target, "name": vfs.Base(target), "size": n})
}

// resolveTarget entscheidet anhand des Modus, wohin geschrieben wird.
func (a *App) resolveTarget(ctx context.Context, c *vfs.Client, dir, name, mode string) (string, bool, error) {
	target := vfs.Join(dir, name)
	_, err := c.Stat(ctx, target)
	if errors.Is(err, vfs.ErrNotFound) {
		return target, false, nil
	}
	if err != nil {
		// Existenz unklar (z. B. FTP ohne MLST) - dann einfach schreiben.
		return target, false, nil
	}
	switch mode {
	case "skip":
		return "", true, nil
	case "overwrite":
		return target, false, nil
	default: // "rename"
		t, err := uniqueName(ctx, c, target)
		return t, false, err
	}
}

// ensureDir legt fehlende Zwischenverzeichnisse an.
func ensureDir(ctx context.Context, c *vfs.Client, dir string) error {
	dir = vfs.Clean(dir)
	if dir == "" {
		return nil
	}
	if _, err := c.Stat(ctx, dir); err == nil {
		return nil
	}
	if err := ensureDir(ctx, c, vfs.Dir(dir)); err != nil {
		return err
	}
	if err := c.Mkdir(ctx, dir); err != nil && !errors.Is(err, vfs.ErrExists) {
		return err
	}
	return nil
}

// ---------------------------------------------------- Auftragsstatus -----

func (a *App) handleJobs(w http.ResponseWriter, r *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": a.jobs.List(id.Name, id.Admin)})
}

type jobCancelRequest struct {
	ID string `json:"id"`
}

func (a *App) handleJobCancel(w http.ResponseWriter, r *http.Request, id Identity) {
	var req jobCancelRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.jobs.Cancel(req.ID, id.Name, id.Admin) {
		failWith(w, http.StatusNotFound, "Auftrag nicht gefunden.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleJobEvents liefert den Fortschritt als Server-Sent-Events, damit die
// Oberfläche nicht pollen muss.
func (a *App) handleJobEvents(w http.ResponseWriter, r *http.Request, id Identity) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		failWith(w, http.StatusInternalServerError, "Streaming nicht möglich.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			jobs := a.jobs.List(id.Name, id.Admin)
			b, err := json.Marshal(map[string]any{"jobs": jobs})
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
