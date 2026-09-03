package vfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// localConn bildet das VFS auf das lokale Dateisystem ab. Nützlich, um
// Downloads-Ordner, eine externe Platte am PC oder ein bereits vom
// Betriebssystem gemountetes Netzlaufwerk einzubinden.
type localConn struct {
	base string
}

// NewLocal erzeugt einen Client für ein lokales Verzeichnis.
func NewLocal(id, label, base string, cacheTTL time.Duration) (*Client, error) {
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDir
	}
	dial := func(ctx context.Context) (Conn, error) { return &localConn{base: abs}, nil }
	caps := Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
	return NewClient(id, label, "", NewPool(dial, 8, 5*time.Minute), caps, cacheTTL), nil
}

func (c *localConn) real(p string) string {
	return filepath.Join(c.base, filepath.FromSlash(Clean(p)))
}

func mapOSErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, fs.ErrPermission):
		return ErrPermission
	}
	return err
}

func (c *localConn) List(ctx context.Context, p string) ([]Entry, error) {
	des, err := os.ReadDir(c.real(p))
	if err != nil {
		return nil, mapOSErr(err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := de.Info()
		if err != nil {
			continue // Datei verschwand während des Scans
		}
		e := Entry{Name: de.Name(), IsDir: de.IsDir(), ModTime: info.ModTime()}
		if de.Type()&os.ModeSymlink != 0 {
			e.Symlink = true
			if t, err := os.Stat(filepath.Join(c.real(p), de.Name())); err == nil {
				e.IsDir = t.IsDir()
				e.Size = t.Size()
				e.ModTime = t.ModTime()
			}
		} else if !de.IsDir() {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	return out, nil
}

func (c *localConn) Stat(ctx context.Context, p string) (Entry, error) {
	st, err := os.Stat(c.real(p))
	if err != nil {
		return Entry{}, mapOSErr(err)
	}
	name := Base(p)
	if name == "" {
		name = "/"
	}
	return Entry{Name: name, Size: st.Size(), IsDir: st.IsDir(), ModTime: st.ModTime()}, nil
}

func (c *localConn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	f, err := os.Open(c.real(p))
	if err != nil {
		return nil, mapOSErr(err)
	}
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func (c *localConn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	f, err := os.Open(c.real(p))
	if err != nil {
		return nil, 0, mapOSErr(err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (c *localConn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	real := c.real(p)
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		return 0, mapOSErr(err)
	}
	f, err := os.Create(real)
	if err != nil {
		return 0, mapOSErr(err)
	}
	n, err := io.Copy(f, r)
	cerr := f.Close()
	if err != nil {
		os.Remove(real)
		return n, err
	}
	return n, cerr
}

func (c *localConn) Mkdir(ctx context.Context, p string) error {
	return mapOSErr(os.Mkdir(c.real(p), 0o755))
}

func (c *localConn) Remove(ctx context.Context, p string, recursive bool) error {
	if recursive {
		return mapOSErr(os.RemoveAll(c.real(p)))
	}
	return mapOSErr(os.Remove(c.real(p)))
}

func (c *localConn) Rename(ctx context.Context, oldp, newp string) error {
	return mapOSErr(os.Rename(c.real(oldp), c.real(newp)))
}

func (c *localConn) Copy(ctx context.Context, oldp, newp string) error { return ErrNotSupported }

func (c *localConn) SetModTime(ctx context.Context, p string, t time.Time) error {
	return mapOSErr(os.Chtimes(c.real(p), t, t))
}

func (c *localConn) Space(ctx context.Context, p string) (Space, error) {
	return diskSpace(c.real(p))
}

func (c *localConn) Ping(ctx context.Context) error {
	_, err := os.Stat(c.base)
	return mapOSErr(err)
}

func (c *localConn) Close() error { return nil }

func (c *localConn) Caps() Caps {
	return Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
}

// DefaultLocalRoots schlägt sinnvolle Startpunkte auf dem jeweiligen System vor.
func DefaultLocalRoots() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		for _, d := range []string{"Downloads", "Documents", "Dokumente", "Pictures", "Bilder"} {
			p := filepath.Join(home, d)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			out = append(out, home)
		}
	}
	return out
}

// SafeFileName entschärft einen Namen für die Ablage im Dateisystem.
func SafeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "datei"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}
