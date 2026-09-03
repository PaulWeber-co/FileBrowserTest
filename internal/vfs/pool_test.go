package vfs

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubConn ist eine Verbindung, deren Verhalten der Test steuert.
type stubConn struct {
	id     int
	dead   atomic.Bool
	closed atomic.Bool
	listed atomic.Int64
}

var errTransport = errors.New("connection error: EOF")

func (c *stubConn) check() error {
	if c.dead.Load() {
		return errTransport
	}
	return nil
}

func (c *stubConn) List(ctx context.Context, p string) ([]Entry, error) {
	c.listed.Add(1)
	if err := c.check(); err != nil {
		return nil, err
	}
	return []Entry{{Name: "datei.txt"}}, nil
}
func (c *stubConn) Stat(ctx context.Context, p string) (Entry, error) {
	if err := c.check(); err != nil {
		return Entry{}, err
	}
	if p == "fehlt" {
		return Entry{}, ErrNotFound
	}
	return Entry{Name: p}, nil
}
func (c *stubConn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	return io.NopCloser(io.LimitReader(nil, 0)), nil
}
func (c *stubConn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	return nil, 0, ErrNotSupported
}
func (c *stubConn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	return 0, c.check()
}
func (c *stubConn) Mkdir(ctx context.Context, p string) error                   { return c.check() }
func (c *stubConn) Remove(ctx context.Context, p string, rec bool) error        { return c.check() }
func (c *stubConn) Rename(ctx context.Context, a, b string) error               { return c.check() }
func (c *stubConn) Copy(ctx context.Context, a, b string) error                 { return ErrNotSupported }
func (c *stubConn) SetModTime(ctx context.Context, p string, t time.Time) error { return c.check() }
func (c *stubConn) Space(ctx context.Context, p string) (Space, error)          { return Space{}, c.check() }
func (c *stubConn) Ping(ctx context.Context) error                              { return c.check() }
func (c *stubConn) Close() error                                                { c.closed.Store(true); return nil }
func (c *stubConn) Caps() Caps                                                  { return Caps{Rename: true} }

func TestPoolWiederverwendetVerbindungen(t *testing.T) {
	var dials atomic.Int64
	p := NewPool(func(ctx context.Context) (Conn, error) {
		return &stubConn{id: int(dials.Add(1))}, nil
	}, 4, time.Minute)
	defer p.Close()

	for i := 0; i < 5; i++ {
		l, err := p.Get(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		l.Release(nil)
	}
	if dials.Load() != 1 {
		t.Errorf("%d Verbindungsaufbauten, erwartet 1 (Wiederverwendung)", dials.Load())
	}
}

func TestPoolVerwirftKaputteVerbindung(t *testing.T) {
	var dials atomic.Int64
	p := NewPool(func(ctx context.Context) (Conn, error) {
		return &stubConn{id: int(dials.Add(1))}, nil
	}, 4, time.Minute)
	defer p.Close()

	l, _ := p.Get(context.Background())
	l.Release(errTransport)

	l2, _ := p.Get(context.Background())
	l2.Release(nil)
	if dials.Load() != 2 {
		t.Errorf("kaputte Verbindung wurde wiederverwendet (dials=%d)", dials.Load())
	}
}

func TestPoolBehaeltVerbindungBeiFachlichemFehler(t *testing.T) {
	var dials atomic.Int64
	p := NewPool(func(ctx context.Context) (Conn, error) {
		return &stubConn{id: int(dials.Add(1))}, nil
	}, 4, time.Minute)
	defer p.Close()

	l, _ := p.Get(context.Background())
	l.Release(ErrNotFound) // "Datei nicht gefunden" macht die Verbindung nicht kaputt
	l2, _ := p.Get(context.Background())
	l2.Release(nil)
	if dials.Load() != 1 {
		t.Errorf("Verbindung wurde unnoetig verworfen (dials=%d)", dials.Load())
	}
}

// Kernfall: der Router hat die Sitzung im Leerlauf weggeworfen. Die erste
// Aktion danach muss trotzdem klappen.
func TestClientWiederholtMitFrischerVerbindung(t *testing.T) {
	var dials atomic.Int64
	var mu sync.Mutex
	var conns []*stubConn

	p := NewPool(func(ctx context.Context) (Conn, error) {
		c := &stubConn{id: int(dials.Add(1))}
		mu.Lock()
		conns = append(conns, c)
		mu.Unlock()
		return c, nil
	}, 4, time.Minute)
	c := NewClient("t", "Test", "", p, Caps{Rename: true}, 0)
	defer c.Close()

	// Erste Nutzung baut die Verbindung auf und legt sie in den Pool zurueck.
	if _, err := c.List(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	conns[0].dead.Store(true) // Gegenstelle hat die Sitzung beendet
	mu.Unlock()

	entries, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatalf("kein Neuversuch: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("unerwartete Antwort: %v", entries)
	}
	if dials.Load() != 2 {
		t.Errorf("erwartet 2 Verbindungsaufbauten, waren %d", dials.Load())
	}
}

func TestClientWiederholtNichtBeiFrischerVerbindung(t *testing.T) {
	var dials atomic.Int64
	p := NewPool(func(ctx context.Context) (Conn, error) {
		c := &stubConn{id: int(dials.Add(1))}
		c.dead.Store(true) // jede neue Verbindung ist sofort kaputt
		return c, nil
	}, 4, time.Minute)
	c := NewClient("t", "Test", "", p, Caps{}, 0)
	defer c.Close()

	if _, err := c.List(context.Background(), ""); err == nil {
		t.Fatal("Fehler erwartet")
	}
	// Eine frisch aufgebaute Verbindung wird nicht noch einmal versucht -
	// sonst haette jeder echte Fehler die doppelte Wartezeit.
	if dials.Load() != 1 {
		t.Errorf("erwartet 1 Verbindungsaufbau, waren %d", dials.Load())
	}
}

func TestClientRootPraefix(t *testing.T) {
	p := NewPool(func(ctx context.Context) (Conn, error) { return &stubConn{}, nil }, 2, time.Minute)
	c := NewClient("t", "Test", "unterordner", p, Caps{}, 0)
	defer c.Close()
	if got := c.abs("datei.txt"); got != "unterordner/datei.txt" {
		t.Errorf("abs = %q", got)
	}
	// Ausbruch aus dem festgelegten Unterordner darf nicht moeglich sein.
	if got := c.abs("../../etc/passwd"); got != "etc/passwd" && got != "unterordner/etc/passwd" {
		t.Errorf("abs mit Ausbruch = %q", got)
	}
}

func TestListCache(t *testing.T) {
	var dials atomic.Int64
	var conn *stubConn
	p := NewPool(func(ctx context.Context) (Conn, error) {
		conn = &stubConn{id: int(dials.Add(1))}
		return conn, nil
	}, 2, time.Minute)
	c := NewClient("t", "Test", "", p, Caps{}, 5*time.Second)
	defer c.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.List(ctx, "ordner"); err != nil {
			t.Fatal(err)
		}
	}
	if n := conn.listed.Load(); n != 1 {
		t.Errorf("%d Abfragen an die Gegenstelle, erwartet 1 (Cache)", n)
	}
	c.Invalidate("ordner")
	if _, err := c.List(ctx, "ordner"); err != nil {
		t.Fatal(err)
	}
	if n := conn.listed.Load(); n != 2 {
		t.Errorf("Cache wurde nicht invalidiert (%d Abfragen)", n)
	}
}
