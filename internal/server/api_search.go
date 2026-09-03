package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/thumb"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

type searchHit struct {
	Path  string     `json:"path"`
	Dir   string     `json:"dir"`
	Name  string     `json:"name"`
	Size  int64      `json:"size"`
	IsDir bool       `json:"isDir"`
	Mtime time.Time  `json:"mtime"`
	Kind  thumb.Kind `json:"kind"`
	Thumb bool       `json:"thumb,omitempty"`
}

// handleSearch durchsucht rekursiv und liefert Treffer als Server-Sent-Events.
//
// Streaming statt "warten bis fertig": über SMB dauert ein tiefer Baum
// schnell eine Minute, und niemand starrt so lange auf einen Spinner.
func (a *App) handleSearch(w http.ResponseWriter, r *http.Request, id Identity) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		failWith(w, http.StatusBadRequest, "Suchbegriff fehlt.")
		return
	}
	c, _, ok := a.clientFor(w, r.URL.Query().Get("loc"))
	if !ok {
		return
	}
	base := vfs.Clean(r.URL.Query().Get("path"))
	limit := queryInt(r, "limit", 500)
	if limit < 1 || limit > 5000 {
		limit = 500
	}
	maxDepth := queryInt(r, "depth", 12)
	kindFilter := r.URL.Query().Get("kind")
	showHidden := queryBool(r, "hidden")

	flusher, isSSE := w.(http.Flusher)
	if !isSSE {
		failWith(w, http.StatusInternalServerError, "Streaming nicht möglich.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	needle := strings.ToLower(q)
	hasFF := a.thumbs.FFmpegAvailable()

	var (
		mu      sync.Mutex // schützt den Antwortstrom, nicht die Zähler
		found   atomic.Int64
		scanned atomic.Int64
	)

	send := func(event string, v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	type task struct {
		path  string
		depth int
	}

	workers := a.cfg.Perf().SearchWorkers
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	stopped := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopped) }) }

	var walk func(t task)
	walk = func(t task) {
		defer wg.Done()
		select {
		case <-stopped:
			return
		case <-ctx.Done():
			return
		default:
		}

		entries, err := c.List(ctx, t.path)
		if err != nil {
			return // unlesbare Ordner überspringen statt die Suche abzubrechen
		}
		scanned.Add(1)

		for _, e := range entries {
			if !showHidden && isHidden(e.Name) {
				continue
			}
			full := vfs.Join(t.path, e.Name)
			if strings.Contains(strings.ToLower(e.Name), needle) && matchKind(e, kindFilter) {
				if found.Add(1) > int64(limit) {
					stop()
					return
				}
				hit := searchHit{
					Path: full, Dir: t.path, Name: e.Name, Size: e.Size,
					IsDir: e.IsDir, Mtime: e.ModTime,
				}
				if e.IsDir {
					hit.Kind = "folder"
				} else {
					hit.Kind = thumb.KindOf(e.Name)
					hit.Thumb = thumb.CanThumb(e.Name, hasFF)
				}
				if !send("hit", hit) {
					stop()
					return
				}
			}
			if e.IsDir && t.depth < maxDepth {
				select {
				case sem <- struct{}{}:
					wg.Add(1)
					go func(next task) {
						defer func() { <-sem }()
						walk(next)
					}(task{full, t.depth + 1})
				default:
					// Alle Arbeiter belegt: im selben Zug weitermachen,
					// statt unbegrenzt Goroutinen anzulegen.
					wg.Add(1)
					walk(task{full, t.depth + 1})
				}
			}
		}
	}

	wg.Add(1)
	go walk(task{base, 0})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	// Fortschritt aus einer einzigen Goroutine melden: sonst schreiben
	// mehrere Arbeiter gleichzeitig in denselben Antwortstrom.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		t := time.NewTicker(800 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-stopped:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				send("progress", map[string]any{"scanned": scanned.Load(), "found": found.Load()})
			}
		}
	}()

	select {
	case <-done:
	case <-stopped:
	case <-ctx.Done():
	}
	<-progressDone

	send("done", map[string]any{
		"scanned":   scanned.Load(),
		"found":     min64(found.Load(), int64(limit)),
		"limited":   found.Load() > int64(limit),
		"cancelled": ctx.Err() != nil,
	})
}

func matchKind(e vfs.Entry, filter string) bool {
	if filter == "" || filter == "all" {
		return true
	}
	if filter == "folder" {
		return e.IsDir
	}
	if e.IsDir {
		return false
	}
	return string(thumb.KindOf(e.Name)) == filter
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
