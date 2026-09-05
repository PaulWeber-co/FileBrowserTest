package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs/smb1"
)

// SMB1Options beschreibt eine Freigabe, die nur SMB1 spricht.
//
// SMB1 ist veraltet: keine Verschlüsselung, angreifbare Aushandlung, und die
// Signierung wird praktisch nie erzwungen. SpeedNAS verwendet es deshalb nur,
// wenn es ausdrücklich eingeschaltet wird. Manche Router - der Speedport
// gehört dazu - bieten ihre USB-Freigabe aber ausschließlich so an; ohne
// diesen Weg wäre der Speicher schlicht unerreichbar.
//
// Ausführlich: docs/smb1.md
type SMB1Options struct {
	Host        string
	Port        int
	Share       string
	User        string
	Password    string
	Domain      string
	DialTimeout time.Duration
	PoolSize    int
	IdleTimeout time.Duration
}

type smb1Conn struct {
	c *smb1.Client
}

// NewSMB1 erzeugt einen Client für eine SMB1-Freigabe.
func NewSMB1(id, label, root string, o SMB1Options, cacheTTL time.Duration) *Client {
	poolSize := o.PoolSize
	if poolSize <= 0 {
		// Betagte Geräte vertragen wenig Parallelität. Zwei Verbindungen
		// reichen, damit ein Download das Blättern nicht blockiert.
		poolSize = 2
	}
	idle := o.IdleTimeout
	if idle <= 0 {
		idle = 45 * time.Second
	}
	opts := o
	dial := func(ctx context.Context) (Conn, error) {
		c, err := smb1.Dial(ctx, smb1.Options{
			Host:        opts.Host,
			Port:        opts.Port,
			Share:       opts.Share,
			User:        opts.User,
			Password:    opts.Password,
			Domain:      opts.Domain,
			DialTimeout: opts.DialTimeout,
			IOTimeout:   90 * time.Second,
		})
		if err != nil {
			return nil, err
		}
		return &smb1Conn{c: c}, nil
	}
	caps := Caps{RandomRead: true, Rename: true, SpaceInfo: true}
	return NewClient(id, label, root, NewPool(dial, poolSize, idle), caps, cacheTTL)
}

// mapSMB1Err übersetzt die Fehler des Protokolls in die des VFS.
func mapSMB1Err(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, smb1.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, smb1.ErrExists):
		return ErrExists
	case errors.Is(err, smb1.ErrPermission), errors.Is(err, smb1.ErrAuth):
		return ErrPermission
	case errors.Is(err, smb1.ErrNotSupported):
		return ErrNotSupported
	}
	return err
}

func (s *smb1Conn) List(ctx context.Context, p string) ([]Entry, error) {
	items, err := s.c.List(ctx, Clean(p))
	if err != nil {
		return nil, mapSMB1Err(err)
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		out = append(out, Entry{
			Name:    it.Name,
			Size:    it.Size,
			IsDir:   it.IsDir,
			ModTime: it.ModTime,
		})
	}
	return out, nil
}

func (s *smb1Conn) Stat(ctx context.Context, p string) (Entry, error) {
	fi, err := s.c.Stat(ctx, Clean(p))
	if err != nil {
		return Entry{}, mapSMB1Err(err)
	}
	name := Base(p)
	if name == "" {
		name = "/"
	}
	return Entry{Name: name, Size: fi.Size, IsDir: fi.IsDir, ModTime: fi.ModTime}, nil
}

// smb1Reader liest eine Datei fortlaufend ab einer Position.
type smb1Reader struct {
	ctx  context.Context
	f    *smb1.File
	off  int64
	size int64
}

func (r *smb1Reader) Read(p []byte) (int, error) {
	if r.off >= r.size {
		return 0, io.EOF
	}
	if rest := r.size - r.off; int64(len(p)) > rest {
		p = p[:rest]
	}
	n, err := r.f.ReadAt(r.ctx, p, r.off)
	r.off += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, mapSMB1Err(err)
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (r *smb1Reader) Close() error {
	return mapSMB1Err(r.f.Close(r.ctx))
}

func (s *smb1Conn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	f, err := s.c.Open(ctx, Clean(p))
	if err != nil {
		return nil, mapSMB1Err(err)
	}
	return &smb1Reader{ctx: ctx, f: f, off: off, size: f.Size()}, nil
}

// smb1ReaderAt bietet wahlfreien Zugriff. Die Aufrufe werden vom Client
// serialisiert - eine SMB1-Verbindung hat immer nur eine Anfrage unterwegs.
type smb1ReaderAt struct {
	ctx context.Context
	f   *smb1.File
}

func (r *smb1ReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.f.ReadAt(r.ctx, p, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, mapSMB1Err(err)
	}
	return n, err
}

func (r *smb1ReaderAt) Close() error { return mapSMB1Err(r.f.Close(r.ctx)) }

func (s *smb1Conn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	f, err := s.c.Open(ctx, Clean(p))
	if err != nil {
		return nil, 0, mapSMB1Err(err)
	}
	return &smb1ReaderAt{ctx: ctx, f: f}, f.Size(), nil
}

func (s *smb1Conn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	f, err := s.c.Create(ctx, Clean(p))
	if err != nil {
		return 0, mapSMB1Err(err)
	}
	defer f.Close(ctx)

	buf := make([]byte, 64<<10)
	var off int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := f.WriteAt(ctx, buf[:n], off); werr != nil {
				return off, mapSMB1Err(werr)
			}
			off += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return off, rerr
		}
	}
	return off, nil
}

func (s *smb1Conn) Mkdir(ctx context.Context, p string) error {
	return mapSMB1Err(s.c.Mkdir(ctx, Clean(p)))
}

func (s *smb1Conn) Remove(ctx context.Context, p string, recursive bool) error {
	p = Clean(p)
	fi, err := s.c.Stat(ctx, p)
	if err != nil {
		return mapSMB1Err(err)
	}
	if !fi.IsDir {
		return mapSMB1Err(s.c.Remove(ctx, p))
	}
	if !recursive {
		return mapSMB1Err(s.c.RemoveDir(ctx, p))
	}
	// SMB1 kennt kein rekursives Löschen - also selbst absteigen.
	return s.removeAll(ctx, p, 0)
}

func (s *smb1Conn) removeAll(ctx context.Context, p string, tiefe int) error {
	if tiefe > 64 {
		return fmt.Errorf("smb1: Verzeichnisbaum zu tief verschachtelt")
	}
	items, err := s.c.List(ctx, p)
	if err != nil {
		return mapSMB1Err(err)
	}
	for _, it := range items {
		child := Join(p, it.Name)
		if it.IsDir {
			if err := s.removeAll(ctx, child, tiefe+1); err != nil {
				return err
			}
			continue
		}
		if err := s.c.Remove(ctx, child); err != nil {
			return mapSMB1Err(err)
		}
	}
	return mapSMB1Err(s.c.RemoveDir(ctx, p))
}

func (s *smb1Conn) Rename(ctx context.Context, oldp, newp string) error {
	return mapSMB1Err(s.c.Rename(ctx, Clean(oldp), Clean(newp)))
}

func (s *smb1Conn) Copy(ctx context.Context, oldp, newp string) error { return ErrNotSupported }

func (s *smb1Conn) SetModTime(ctx context.Context, p string, t time.Time) error {
	return ErrNotSupported
}

func (s *smb1Conn) Space(ctx context.Context, p string) (Space, error) {
	total, free, err := s.c.Space(ctx)
	if err != nil {
		return Space{}, mapSMB1Err(err)
	}
	return Space{Total: total, Free: free}, nil
}

func (s *smb1Conn) Ping(ctx context.Context) error { return mapSMB1Err(s.c.Echo(ctx)) }

func (s *smb1Conn) Close() error { return s.c.Close() }

func (s *smb1Conn) Caps() Caps {
	return Caps{RandomRead: true, Rename: true, SpaceInfo: true}
}
