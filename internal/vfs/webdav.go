package vfs

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/studio-b12/gowebdav"
)

// WebDAVOptions beschreibt einen WebDAV-Zugang (auch Nextcloud/ownCloud).
type WebDAVOptions struct {
	URL        string // z. B. https://example.tld/remote.php/dav/files/paul
	User       string
	Password   string
	SkipVerify bool
	Timeout    time.Duration
	PoolSize   int
}

type webdavConn struct {
	c  *gowebdav.Client
	hc *http.Client
}

// NewWebDAV erzeugt einen Client für einen WebDAV-Server.
func NewWebDAV(id, label, root string, o WebDAVOptions, cacheTTL time.Duration) *Client {
	poolSize := o.PoolSize
	if poolSize <= 0 {
		poolSize = 4
	}
	opts := o
	// Ein gemeinsamer Transport hält HTTP-Verbindungen offen; das spart pro
	// Anfrage den TLS-Handshake.
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: opts.SkipVerify},
	}
	hc := &http.Client{Transport: tr}
	dial := func(ctx context.Context) (Conn, error) {
		c := gowebdav.NewClient(opts.URL, opts.User, opts.Password)
		c.SetTransport(tr)
		if opts.Timeout > 0 {
			c.SetTimeout(opts.Timeout)
		}
		if err := c.Connect(); err != nil {
			return nil, fmt.Errorf("WebDAV-Verbindung fehlgeschlagen: %w", err)
		}
		return &webdavConn{c: c, hc: hc}, nil
	}
	caps := Caps{RandomRead: true, Rename: true, ServerCopy: true, Recursive: true}
	return NewClient(id, label, root, NewPool(dial, poolSize, 5*time.Minute), caps, cacheTTL)
}

func davPath(p string) string {
	p = Clean(p)
	if p == "" {
		return "/"
	}
	return "/" + p
}

func mapDavErr(err error) error {
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"), errors.Is(err, os.ErrNotExist):
		return ErrNotFound
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return ErrPermission
	case strings.Contains(msg, "405"), strings.Contains(msg, "409"):
		return ErrExists
	}
	return err
}

func (c *webdavConn) List(ctx context.Context, p string) ([]Entry, error) {
	fis, err := c.c.ReadDir(davPath(p))
	if err != nil {
		return nil, mapDavErr(err)
	}
	out := make([]Entry, 0, len(fis))
	for _, fi := range fis {
		out = append(out, Entry{Name: fi.Name(), Size: fi.Size(), IsDir: fi.IsDir(), ModTime: fi.ModTime()})
	}
	return out, nil
}

func (c *webdavConn) Stat(ctx context.Context, p string) (Entry, error) {
	fi, err := c.c.Stat(davPath(p))
	if err != nil {
		return Entry{}, mapDavErr(err)
	}
	name := Base(p)
	if name == "" {
		name = "/"
	}
	return Entry{Name: name, Size: fi.Size(), IsDir: fi.IsDir(), ModTime: fi.ModTime()}, nil
}

func (c *webdavConn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	if off <= 0 {
		rc, err := c.c.ReadStream(davPath(p))
		return rc, mapDavErr(err)
	}
	e, err := c.Stat(ctx, p)
	if err != nil {
		return nil, err
	}
	rc, err := c.c.ReadStreamRange(davPath(p), off, e.Size-off)
	return rc, mapDavErr(err)
}

// webdavReaderAt bildet wahlfreien Zugriff auf HTTP-Range-Anfragen ab. In
// Verbindung mit dem Prefetch-Reader ergibt das mehrere parallele
// Range-Anfragen - genau das, was HTTP gut kann.
type webdavReaderAt struct {
	c    *gowebdav.Client
	path string
	size int64
}

func (r *webdavReaderAt) ReadAt(b []byte, off int64) (int, error) {
	if off >= r.size {
		return 0, io.EOF
	}
	n := int64(len(b))
	if rest := r.size - off; rest < n {
		n = rest
	}
	rc, err := r.c.ReadStreamRange(r.path, off, n)
	if err != nil {
		return 0, mapDavErr(err)
	}
	defer rc.Close()
	read, err := io.ReadFull(rc, b[:n])
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	if err == nil && int64(len(b)) > n {
		err = io.EOF
	}
	return read, err
}

func (r *webdavReaderAt) Close() error { return nil }

func (c *webdavConn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	e, err := c.Stat(ctx, p)
	if err != nil {
		return nil, 0, err
	}
	return &webdavReaderAt{c: c.c, path: davPath(p), size: e.Size}, e.Size, nil
}

func (c *webdavConn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	cr := &countingReader{r: r}
	if err := c.c.WriteStream(davPath(p), cr, 0o644); err != nil {
		return cr.n, mapDavErr(err)
	}
	return cr.n, nil
}

func (c *webdavConn) Mkdir(ctx context.Context, p string) error {
	return mapDavErr(c.c.Mkdir(davPath(p), 0o755))
}

func (c *webdavConn) Remove(ctx context.Context, p string, recursive bool) error {
	return mapDavErr(c.c.RemoveAll(davPath(p)))
}

func (c *webdavConn) Rename(ctx context.Context, oldp, newp string) error {
	return mapDavErr(c.c.Rename(davPath(oldp), davPath(newp), true))
}

func (c *webdavConn) Copy(ctx context.Context, oldp, newp string) error {
	return mapDavErr(c.c.Copy(davPath(oldp), davPath(newp), true))
}

func (c *webdavConn) SetModTime(ctx context.Context, p string, t time.Time) error {
	return ErrNotSupported
}

func (c *webdavConn) Space(ctx context.Context, p string) (Space, error) {
	return Space{}, ErrNotSupported
}

func (c *webdavConn) Ping(ctx context.Context) error { return mapDavErr(c.c.Connect()) }

func (c *webdavConn) Close() error { return nil }

func (c *webdavConn) Caps() Caps {
	return Caps{RandomRead: true, Rename: true, ServerCopy: true, Recursive: true}
}
