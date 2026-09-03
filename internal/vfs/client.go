package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client ist die nach außen sichtbare Sicht auf einen Speicherort. Jede
// Operation leiht sich kurz eine Verbindung aus dem Pool; Streams halten ihre
// Verbindung bis zum Close.
type Client struct {
	id    string
	label string
	pool  *Pool
	caps  Caps
	root  string // fester Präfix innerhalb der Freigabe

	cache *listCache
}

// NewClient verbindet einen Pool mit Metadaten zu einem nutzbaren Client.
func NewClient(id, label, root string, pool *Pool, caps Caps, cacheTTL time.Duration) *Client {
	return &Client{
		id:    id,
		label: label,
		pool:  pool,
		caps:  caps,
		root:  Clean(root),
		cache: newListCache(cacheTTL),
	}
}

func (c *Client) ID() string    { return c.id }
func (c *Client) Label() string { return c.label }
func (c *Client) Caps() Caps    { return c.caps }
func (c *Client) Pool() *Pool   { return c.pool }

// abs bildet einen UI-Pfad auf den tatsächlichen Pfad in der Freigabe ab.
func (c *Client) abs(p string) string {
	if c.root == "" {
		return Clean(p)
	}
	return Join(c.root, p)
}

func (c *Client) Close() error {
	c.cache.purge()
	return c.pool.Close()
}

// List liefert den Inhalt eines Verzeichnisses, alphabetisch mit Ordnern
// zuerst. Ergebnisse werden kurz zwischengespeichert - bei einem USB-2.0-Stick
// am Router kostet ein Verzeichnis-Scan sonst spürbar Zeit, und die UI fragt
// dasselbe Verzeichnis beim Zurückspringen sofort erneut ab.
func (c *Client) List(ctx context.Context, p string) ([]Entry, error) {
	p = Clean(p)
	if e, ok := c.cache.get(p); ok {
		return e, nil
	}
	var entries []Entry
	err := c.withConn(ctx, func(l *Lease) error {
		var e error
		entries, e = l.List(ctx, c.abs(p))
		return e
	})
	if err != nil {
		return nil, err
	}
	SortEntries(entries, "name", false)
	c.cache.put(p, entries)
	return entries, nil
}

// Invalidate verwirft Cache-Einträge für ein Verzeichnis und dessen Eltern.
func (c *Client) Invalidate(paths ...string) {
	for _, p := range paths {
		c.cache.del(Clean(p))
	}
}

func (c *Client) InvalidateAll() { c.cache.purge() }

func (c *Client) Stat(ctx context.Context, p string) (Entry, error) {
	var e Entry
	err := c.withConn(ctx, func(l *Lease) error {
		var err error
		e, err = l.Stat(ctx, c.abs(p))
		return err
	})
	return e, err
}

type leaseReadCloser struct {
	rc   io.ReadCloser
	l    *Lease
	err  error
	once sync.Once
}

func (r *leaseReadCloser) Read(b []byte) (int, error) {
	n, err := r.rc.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

func (r *leaseReadCloser) Close() error {
	var err error
	r.once.Do(func() {
		err = r.rc.Close()
		if err != nil && r.err == nil {
			r.err = err
		}
		r.l.Release(r.err)
	})
	return err
}

// Reader liefert einen sequentiellen Stream ab off.
func (c *Client) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	var rc io.ReadCloser
	l, err := c.leaseFor(ctx, func(l *Lease) error {
		var e error
		rc, e = l.Reader(ctx, c.abs(p), off)
		return e
	})
	if err != nil {
		return nil, err
	}
	return &leaseReadCloser{rc: rc, l: l}, nil
}

type leaseReaderAt struct {
	ra   ReaderAtCloser
	l    *Lease
	mu   sync.Mutex
	err  error
	once sync.Once
}

func (r *leaseReaderAt) ReadAt(b []byte, off int64) (int, error) {
	n, err := r.ra.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
	}
	return n, err
}

func (r *leaseReaderAt) Close() error {
	var err error
	r.once.Do(func() {
		err = r.ra.Close()
		r.mu.Lock()
		e := r.err
		r.mu.Unlock()
		if err != nil && e == nil {
			e = err
		}
		r.l.Release(e)
	})
	return err
}

// ReaderAt liefert wahlfreien Lesezugriff samt Dateigröße.
func (c *Client) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	if !c.caps.RandomRead {
		return nil, 0, ErrNotSupported
	}
	var (
		ra   ReaderAtCloser
		size int64
	)
	l, err := c.leaseFor(ctx, func(l *Lease) error {
		var e error
		ra, size, e = l.ReaderAt(ctx, c.abs(p))
		return e
	})
	if err != nil {
		return nil, 0, err
	}
	return &leaseReaderAt{ra: ra, l: l}, size, nil
}

// StreamAt liefert den performantesten Lesestream, den das Backend hergibt:
// bei wahlfreiem Zugriff mit parallelem Vorauslesen, sonst sequentiell.
func (c *Client) StreamAt(ctx context.Context, p string, off int64, prefetch PrefetchOpts) (io.ReadCloser, int64, error) {
	if c.caps.RandomRead && prefetch.Workers > 1 {
		ra, size, err := c.ReaderAt(ctx, p)
		if err == nil {
			if off >= size {
				_ = ra.Close()
				return io.NopCloser(strings.NewReader("")), size, nil
			}
			return NewPrefetchReader(ctx, ra, size, off, prefetch), size, nil
		}
		if !errors.Is(err, ErrNotSupported) {
			return nil, 0, err
		}
	}
	rc, err := c.Reader(ctx, p, off)
	if err != nil {
		return nil, 0, err
	}
	return rc, -1, nil
}

// Write legt eine Datei an bzw. überschreibt sie.
func (c *Client) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	// Hier wird bewusst nicht wiederholt: der Datenstrom lässt sich nicht
	// zurückspulen. Deshalb eine frische Verbindung, wenn die Datei groß
	// genug ist, dass ein Fehlschlag richtig weh täte.
	get := c.pool.Get
	if size < 0 || size > 8<<20 {
		get = c.pool.GetFresh
	}
	l, err := get(ctx)
	if err != nil {
		return 0, err
	}
	n, err := l.Write(ctx, c.abs(p), r, size)
	l.Release(err)
	if err == nil {
		c.cache.del(Dir(p))
	}
	return n, err
}

func (c *Client) Mkdir(ctx context.Context, p string) error {
	err := c.withConn(ctx, func(l *Lease) error { return l.Mkdir(ctx, c.abs(p)) })
	if err == nil {
		c.cache.del(Dir(p))
	}
	return err
}

func (c *Client) Remove(ctx context.Context, p string, recursive bool) error {
	if Clean(p) == "" {
		return fmt.Errorf("die Wurzel kann nicht gelöscht werden")
	}
	err := c.withConn(ctx, func(l *Lease) error { return l.Remove(ctx, c.abs(p), recursive) })
	if err == nil {
		c.cache.del(Dir(p))
		c.cache.delPrefix(Clean(p))
	}
	return err
}

func (c *Client) Rename(ctx context.Context, oldp, newp string) error {
	if !c.caps.Rename {
		return ErrNotSupported
	}
	err := c.withConn(ctx, func(l *Lease) error { return l.Rename(ctx, c.abs(oldp), c.abs(newp)) })
	if err == nil {
		c.cache.del(Dir(oldp))
		c.cache.del(Dir(newp))
		c.cache.delPrefix(Clean(oldp))
	}
	return err
}

// ServerCopy kopiert innerhalb desselben Speicherorts serverseitig, falls das
// Protokoll das kann (WebDAV). Sonst ErrNotSupported.
func (c *Client) ServerCopy(ctx context.Context, oldp, newp string) error {
	if !c.caps.ServerCopy {
		return ErrNotSupported
	}
	err := c.withConn(ctx, func(l *Lease) error { return l.Copy(ctx, c.abs(oldp), c.abs(newp)) })
	if err == nil {
		c.cache.del(Dir(newp))
	}
	return err
}

func (c *Client) SetModTime(ctx context.Context, p string, t time.Time) error {
	if !c.caps.SetModTime {
		return ErrNotSupported
	}
	return c.withConn(ctx, func(l *Lease) error { return l.SetModTime(ctx, c.abs(p), t) })
}

func (c *Client) Space(ctx context.Context, p string) (Space, error) {
	if !c.caps.SpaceInfo {
		return Space{}, ErrNotSupported
	}
	var sp Space
	err := c.withConn(ctx, func(l *Lease) error {
		var e error
		sp, e = l.Space(ctx, c.abs(p))
		return e
	})
	return sp, err
}

// Ping prüft die Erreichbarkeit und misst die Antwortzeit.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	err := c.withConn(ctx, func(l *Lease) error { return l.Ping(ctx) })
	return time.Since(start), err
}

// withConn führt eine Operation auf einer Poolverbindung aus.
//
// Scheitert eine *wiederverwendete* Verbindung mit einem Transportfehler,
// wird genau einmal mit einer frisch aufgebauten wiederholt. Router - der
// Speedport eingeschlossen - werfen inaktive SMB-Sitzungen gern kommentarlos
// weg; ohne diesen Neuversuch scheitert dann die erste Aktion nach jeder
// Pause, und die Anwendung wirkt kaputt, obwohl alles in Ordnung ist.
func (c *Client) withConn(ctx context.Context, fn func(l *Lease) error) error {
	l, err := c.pool.Get(ctx)
	if err != nil {
		return err
	}
	reused := l.Reused
	err = fn(l)
	l.Release(err)
	if err == nil || !reused || !isFatal(err) {
		return err
	}
	l, dialErr := c.pool.GetFresh(ctx)
	if dialErr != nil {
		return err // der ursprüngliche Fehler ist aussagekräftiger
	}
	err = fn(l)
	l.Release(err)
	return err
}

// leaseFor besorgt eine Verbindung für einen Datenstrom, der die Verbindung
// bis zum Close behält - mit demselben Neuversuch wie withConn.
func (c *Client) leaseFor(ctx context.Context, open func(l *Lease) error) (*Lease, error) {
	l, err := c.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	reused := l.Reused
	if err := open(l); err != nil {
		l.Release(err)
		if !reused || !isFatal(err) {
			return nil, err
		}
		l, dialErr := c.pool.GetFresh(ctx)
		if dialErr != nil {
			return nil, err
		}
		if err := open(l); err != nil {
			l.Release(err)
			return nil, err
		}
		return l, nil
	}
	return l, nil
}

// SortEntries sortiert Einträge; Ordner stehen immer vorn.
func SortEntries(e []Entry, by string, desc bool) {
	less := func(i, j int) bool {
		a, b := e[i], e[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		var r bool
		switch by {
		case "size":
			if a.Size == b.Size {
				r = strings.ToLower(a.Name) < strings.ToLower(b.Name)
			} else {
				r = a.Size < b.Size
			}
		case "mtime":
			if a.ModTime.Equal(b.ModTime) {
				r = strings.ToLower(a.Name) < strings.ToLower(b.Name)
			} else {
				r = a.ModTime.Before(b.ModTime)
			}
		case "type":
			ea, eb := ext(a.Name), ext(b.Name)
			if ea == eb {
				r = strings.ToLower(a.Name) < strings.ToLower(b.Name)
			} else {
				r = ea < eb
			}
		default:
			r = naturalLess(a.Name, b.Name)
		}
		if desc && a.IsDir == b.IsDir {
			return !r
		}
		return r
	}
	sort.SliceStable(e, less)
}

func ext(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return strings.ToLower(name[i+1:])
}

// naturalLess vergleicht so, wie Menschen es erwarten: "Bild2" vor "Bild10".
func naturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(la) && j < len(lb) {
		ca, cb := la[i], lb[j]
		if isDigit(ca) && isDigit(cb) {
			si, sj := i, j
			for i < len(la) && isDigit(la[i]) {
				i++
			}
			for j < len(lb) && isDigit(lb[j]) {
				j++
			}
			na := strings.TrimLeft(la[si:i], "0")
			nb := strings.TrimLeft(lb[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(la)-i < len(lb)-j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
