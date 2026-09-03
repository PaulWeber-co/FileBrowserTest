package vfs

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// Dialer baut eine neue physische Verbindung auf.
type Dialer func(ctx context.Context) (Conn, error)

type pooled struct {
	conn     Conn
	lastUsed time.Time
	broken   bool
}

// Pool hält eine begrenzte Zahl warmer Verbindungen offen.
//
// Genau das ist der größte Performance-Hebel bei einem Speedport: ein
// frischer SMB-Handshake (TCP + Negotiate + NTLM-Session-Setup + Tree-Connect)
// kostet 4-6 Round-Trips. Im LAN fällt das kaum auf, über VPN mit 30-60 ms
// RTT sind das schnell 300 ms - pro Verzeichniswechsel. Wiederverwendete
// Verbindungen sparen das komplett ein.
type Pool struct {
	dial    Dialer
	maxIdle time.Duration

	sem chan struct{} // begrenzt die Gesamtzahl gleichzeitiger Verbindungen

	mu     sync.Mutex
	idle   []*pooled
	closed bool

	stopReaper chan struct{}
	reaperOnce sync.Once
}

// NewPool erzeugt einen Pool mit maximal max gleichzeitigen Verbindungen.
func NewPool(dial Dialer, max int, maxIdle time.Duration) *Pool {
	if max < 1 {
		max = 1
	}
	if maxIdle <= 0 {
		maxIdle = 60 * time.Second
	}
	p := &Pool{
		dial:       dial,
		maxIdle:    maxIdle,
		sem:        make(chan struct{}, max),
		stopReaper: make(chan struct{}),
	}
	go p.reap()
	return p
}

// Get leiht eine Verbindung aus - bevorzugt eine bereits offene.
// Der Aufrufer muss zwingend Release aufrufen.
func (p *Pool) Get(ctx context.Context) (*Lease, error) { return p.get(ctx, false) }

// GetFresh erzwingt einen neuen Verbindungsaufbau. Wird gebraucht, wenn eine
// wiederverwendete Verbindung sich als tot herausgestellt hat.
func (p *Pool) GetFresh(ctx context.Context) (*Lease, error) { return p.get(ctx, true) }

func (p *Pool) get(ctx context.Context, fresh bool) (*Lease, error) {
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.sem
		return nil, ErrClosed
	}
	// Jüngste zuerst: die ist am wahrscheinlichsten noch am Leben.
	for !fresh && len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		p.mu.Unlock()
		if time.Since(c.lastUsed) < p.maxIdle {
			return &Lease{pool: p, p: c, Conn: c.conn, Reused: true}, nil
		}
		_ = c.conn.Close()
		p.mu.Lock()
	}
	p.mu.Unlock()

	conn, err := p.dial(ctx)
	if err != nil {
		<-p.sem
		return nil, err
	}
	c := &pooled{conn: conn, lastUsed: time.Now()}
	return &Lease{pool: p, p: c, Conn: conn}, nil
}

func (p *Pool) put(c *pooled) {
	p.mu.Lock()
	if p.closed || c.broken {
		p.mu.Unlock()
		_ = c.conn.Close()
		<-p.sem
		return
	}
	c.lastUsed = time.Now()
	p.idle = append(p.idle, c)
	p.mu.Unlock()
	<-p.sem
}

func (p *Pool) reap() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			cutoff := time.Now().Add(-p.maxIdle)
			p.mu.Lock()
			keep := p.idle[:0]
			var dead []*pooled
			for _, c := range p.idle {
				if c.lastUsed.Before(cutoff) {
					dead = append(dead, c)
				} else {
					keep = append(keep, c)
				}
			}
			p.idle = keep
			p.mu.Unlock()
			for _, c := range dead {
				_ = c.conn.Close()
			}
		case <-p.stopReaper:
			return
		}
	}
}

// Close schließt alle leerlaufenden Verbindungen. Bereits ausgeliehene
// Verbindungen werden bei ihrer Rückgabe geschlossen.
func (p *Pool) Close() error {
	p.reaperOnce.Do(func() { close(p.stopReaper) })
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, c := range idle {
		_ = c.conn.Close()
	}
	return nil
}

// Stats liefert Kennzahlen für die Diagnoseansicht.
func (p *Pool) Stats() (idle, max int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle), cap(p.sem)
}

// Lease ist eine ausgeliehene Verbindung.
type Lease struct {
	Conn
	// Reused meldet, ob die Verbindung aus dem Pool kam. Nur dann lohnt ein
	// Neuversuch: eine frisch aufgebaute Verbindung scheitert aus einem
	// echten Grund, nicht weil sie zwischenzeitlich gestorben ist.
	Reused bool

	pool *Pool
	p    *pooled
	once sync.Once
}

// Release gibt die Verbindung zurück. err entscheidet, ob sie wiederverwendet
// oder verworfen wird: Protokoll-/Transportfehler machen eine Verbindung
// unbrauchbar, ein schlichtes "Datei nicht gefunden" nicht.
func (l *Lease) Release(err error) {
	l.once.Do(func() {
		if isFatal(err) {
			l.p.broken = true
		}
		l.pool.put(l.p)
	})
}

// isFatal entscheidet, ob ein Fehler die Verbindung selbst betrifft.
func isFatal(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrNotFound),
		errors.Is(err, ErrExists),
		errors.Is(err, ErrPermission),
		errors.Is(err, ErrIsDir),
		errors.Is(err, ErrNotDir),
		errors.Is(err, ErrNotSupported),
		errors.Is(err, context.Canceled),
		errors.Is(err, io.EOF):
		return false
	}
	return true
}
