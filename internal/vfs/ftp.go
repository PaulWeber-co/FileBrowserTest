package vfs

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

// FTPOptions beschreibt einen FTP-Zugang.
type FTPOptions struct {
	Host        string
	Port        int
	User        string
	Password    string
	TLS         string // "", "none", "explicit" (FTPS/AUTH TLS), "implicit"
	SkipVerify  bool
	DisableEPSV bool
	DialTimeout time.Duration
	PoolSize    int
	IdleTimeout time.Duration
}

func (o *FTPOptions) addr() string {
	port := o.Port
	if port == 0 {
		if strings.EqualFold(o.TLS, "implicit") {
			port = 990
		} else {
			port = 21
		}
	}
	return net.JoinHostPort(o.Host, fmt.Sprint(port))
}

type ftpConn struct {
	c *ftp.ServerConn
}

// NewFTP erzeugt einen Client für einen FTP-/FTPS-Server.
func NewFTP(id, label, root string, o FTPOptions, cacheTTL time.Duration) *Client {
	poolSize := o.PoolSize
	if poolSize <= 0 {
		// Jede FTP-Verbindung ist eine eigene Kontrollverbindung; viele
		// Embedded-Server erlauben nur eine Handvoll gleichzeitig.
		poolSize = 2
	}
	idle := o.IdleTimeout
	if idle <= 0 {
		idle = 30 * time.Second
	}
	opts := o
	dial := func(ctx context.Context) (Conn, error) {
		timeout := opts.DialTimeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		dialOpts := []ftp.DialOption{
			ftp.DialWithContext(ctx),
			ftp.DialWithTimeout(timeout),
			ftp.DialWithDisabledEPSV(opts.DisableEPSV),
			// Viele Router melden im PASV-Antwortpaket eine falsche interne
			// IP. Der Kontrollverbindung zu vertrauen ist hier robuster.
			ftp.DialWithTrustPasvIP(true),
		}
		tlsCfg := &tls.Config{ServerName: opts.Host, InsecureSkipVerify: opts.SkipVerify}
		switch strings.ToLower(opts.TLS) {
		case "explicit", "ftps", "auth-tls":
			dialOpts = append(dialOpts, ftp.DialWithExplicitTLS(tlsCfg))
		case "implicit":
			dialOpts = append(dialOpts, ftp.DialWithTLS(tlsCfg))
		}
		c, err := ftp.Dial(opts.addr(), dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("FTP-Verbindung zu %s fehlgeschlagen: %w", opts.addr(), err)
		}
		user, pass := opts.User, opts.Password
		if user == "" {
			user, pass = "anonymous", "anonymous@"
		}
		if err := c.Login(user, pass); err != nil {
			_ = c.Quit()
			return nil, fmt.Errorf("FTP-Anmeldung fehlgeschlagen: %w", err)
		}
		// Binärmodus: sonst zerschießt der Server Zeilenenden in Dateien.
		_ = c.Type(ftp.TransferTypeBinary)
		return &ftpConn{c: c}, nil
	}
	caps := Caps{RandomRead: false, Rename: true, Recursive: true, SetModTime: true}
	return NewClient(id, label, root, NewPool(dial, poolSize, idle), caps, cacheTTL)
}

func ftpPath(p string) string {
	p = Clean(p)
	if p == "" {
		return "/"
	}
	return "/" + p
}

func mapFTPErr(err error) error {
	if err == nil {
		return nil
	}
	var te *textprotoErr
	if errors.As(err, &te) {
		switch te.code {
		case 550:
			return ErrNotFound
		case 530, 553:
			return ErrPermission
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "550"):
		return ErrNotFound
	case strings.Contains(msg, "530"):
		return ErrPermission
	}
	return err
}

// textprotoErr existiert nur, damit errors.As oben typsicher bleibt, falls die
// FTP-Bibliothek strukturierte Fehler liefert.
type textprotoErr struct {
	code int
	msg  string
}

func (e *textprotoErr) Error() string { return fmt.Sprintf("%d %s", e.code, e.msg) }

func (c *ftpConn) List(ctx context.Context, p string) ([]Entry, error) {
	entries, err := c.c.List(ftpPath(p))
	if err != nil {
		return nil, mapFTPErr(err)
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." || e.Name == "" {
			continue
		}
		out = append(out, Entry{
			Name:    e.Name,
			Size:    int64(e.Size),
			IsDir:   e.Type == ftp.EntryTypeFolder,
			ModTime: e.Time,
			Symlink: e.Type == ftp.EntryTypeLink,
		})
	}
	return out, nil
}

func (c *ftpConn) Stat(ctx context.Context, p string) (Entry, error) {
	cp := Clean(p)
	if cp == "" {
		return Entry{Name: "/", IsDir: true}, nil
	}
	if e, err := c.c.GetEntry(ftpPath(cp)); err == nil && e != nil {
		return Entry{
			Name:    Base(cp),
			Size:    int64(e.Size),
			IsDir:   e.Type == ftp.EntryTypeFolder,
			ModTime: e.Time,
		}, nil
	}
	// MLST wird nicht von jedem Server unterstützt - dann das Elternverzeichnis lesen.
	parent := Dir(cp)
	entries, err := c.List(ctx, parent)
	if err != nil {
		return Entry{}, err
	}
	name := Base(cp)
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
	}
	return Entry{}, ErrNotFound
}

// ftpReader schützt die Kontrollverbindung: wird ein Transfer vorzeitig
// abgebrochen, bleibt die Verbindung in unklarem Zustand und darf nicht
// zurück in den Pool.
type ftpReader struct {
	rc      io.ReadCloser
	reached bool
}

func (r *ftpReader) Read(b []byte) (int, error) {
	n, err := r.rc.Read(b)
	if errors.Is(err, io.EOF) {
		r.reached = true
	}
	return n, err
}

func (r *ftpReader) Close() error {
	err := r.rc.Close()
	if !r.reached {
		return fmt.Errorf("FTP-Transfer vorzeitig beendet: Verbindung wird erneuert")
	}
	return err
}

func (c *ftpConn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	var (
		resp *ftp.Response
		err  error
	)
	if off > 0 {
		resp, err = c.c.RetrFrom(ftpPath(p), uint64(off))
	} else {
		resp, err = c.c.Retr(ftpPath(p))
	}
	if err != nil {
		return nil, mapFTPErr(err)
	}
	return &ftpReader{rc: resp}, nil
}

func (c *ftpConn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	return nil, 0, ErrNotSupported
}

func (c *ftpConn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	cr := &countingReader{r: r}
	if err := c.c.Stor(ftpPath(p), cr); err != nil {
		return cr.n, mapFTPErr(err)
	}
	return cr.n, nil
}

func (c *ftpConn) Mkdir(ctx context.Context, p string) error {
	return mapFTPErr(c.c.MakeDir(ftpPath(p)))
}

func (c *ftpConn) Remove(ctx context.Context, p string, recursive bool) error {
	if recursive {
		if e, err := c.Stat(ctx, p); err == nil && e.IsDir {
			return mapFTPErr(c.c.RemoveDirRecur(ftpPath(p)))
		}
	}
	if err := c.c.Delete(ftpPath(p)); err != nil {
		// Verzeichnisse brauchen RMD statt DELE.
		if derr := c.c.RemoveDir(ftpPath(p)); derr == nil {
			return nil
		}
		return mapFTPErr(err)
	}
	return nil
}

func (c *ftpConn) Rename(ctx context.Context, oldp, newp string) error {
	return mapFTPErr(c.c.Rename(ftpPath(oldp), ftpPath(newp)))
}

func (c *ftpConn) Copy(ctx context.Context, oldp, newp string) error { return ErrNotSupported }

func (c *ftpConn) SetModTime(ctx context.Context, p string, t time.Time) error {
	if !c.c.IsSetTimeSupported() {
		return ErrNotSupported
	}
	return mapFTPErr(c.c.SetTime(ftpPath(p), t))
}

func (c *ftpConn) Space(ctx context.Context, p string) (Space, error) {
	return Space{}, ErrNotSupported
}

func (c *ftpConn) Ping(ctx context.Context) error { return mapFTPErr(c.c.NoOp()) }

func (c *ftpConn) Close() error { return c.c.Quit() }

func (c *ftpConn) Caps() Caps {
	return Caps{Rename: true, Recursive: true, SetModTime: true}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	return n, err
}

// ftpJoin verbindet FTP-Pfade (nur intern für Tests).
func ftpJoin(elem ...string) string { return path.Join(elem...) }
