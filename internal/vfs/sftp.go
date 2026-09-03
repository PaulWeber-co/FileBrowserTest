package vfs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTPOptions beschreibt einen SFTP-Zugang (SSH-Dateiübertragung).
type SFTPOptions struct {
	Host        string
	Port        int
	User        string
	Password    string
	PrivateKey  []byte // optionaler privater Schlüssel (PEM)
	Passphrase  string
	HostKey     string // gepinnter Fingerabdruck "SHA256:..."; leer = beim ersten Mal lernen
	DialTimeout time.Duration
	PoolSize    int
	IdleTimeout time.Duration

	// OnHostKey wird beim ersten Verbindungsaufbau mit dem gesehenen
	// Fingerabdruck aufgerufen, damit die Konfiguration ihn speichern kann.
	OnHostKey func(fingerprint string)
}

func (o *SFTPOptions) addr() string {
	port := o.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(o.Host, fmt.Sprint(port))
}

// Fingerprint bildet den in OpenSSH üblichen SHA256-Fingerabdruck.
func Fingerprint(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

type sftpConn struct {
	ssh  *ssh.Client
	c    *sftp.Client
	root string
}

// NewSFTP erzeugt einen Client für einen SFTP-Server.
func NewSFTP(id, label, root string, o SFTPOptions, cacheTTL time.Duration) *Client {
	poolSize := o.PoolSize
	if poolSize <= 0 {
		poolSize = 2
	}
	idle := o.IdleTimeout
	if idle <= 0 {
		idle = 2 * time.Minute
	}
	opts := o
	dial := func(ctx context.Context) (Conn, error) {
		var auths []ssh.AuthMethod
		if len(opts.PrivateKey) > 0 {
			var signer ssh.Signer
			var err error
			if opts.Passphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(opts.PrivateKey, []byte(opts.Passphrase))
			} else {
				signer, err = ssh.ParsePrivateKey(opts.PrivateKey)
			}
			if err != nil {
				return nil, fmt.Errorf("privater Schlüssel unbrauchbar: %w", err)
			}
			auths = append(auths, ssh.PublicKeys(signer))
		}
		if opts.Password != "" {
			auths = append(auths, ssh.Password(opts.Password),
				ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
					ans := make([]string, len(questions))
					for i := range ans {
						ans[i] = opts.Password
					}
					return ans, nil
				}))
		}
		timeout := opts.DialTimeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		cfg := &ssh.ClientConfig{
			User:            opts.User,
			Auth:            auths,
			Timeout:         timeout,
			HostKeyCallback: hostKeyChecker(opts),
		}
		d := net.Dialer{Timeout: timeout}
		nc, err := d.DialContext(ctx, "tcp", opts.addr())
		if err != nil {
			return nil, fmt.Errorf("SSH-Verbindung zu %s fehlgeschlagen: %w", opts.addr(), err)
		}
		if tc, ok := nc.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
		}
		sc, chans, reqs, err := ssh.NewClientConn(nc, opts.addr(), cfg)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("SSH-Anmeldung fehlgeschlagen: %w", err)
		}
		client := ssh.NewClient(sc, chans, reqs)
		// Größere Pakete und viele parallele Anfragen pro Datei: das ist bei
		// SFTP der entscheidende Hebel gegen Latenz.
		sf, err := sftp.NewClient(client,
			sftp.MaxPacket(32768),
			sftp.MaxConcurrentRequestsPerFile(64),
			sftp.UseConcurrentReads(true),
			sftp.UseConcurrentWrites(true),
		)
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("SFTP-Subsystem nicht verfügbar: %w", err)
		}
		return &sftpConn{ssh: client, c: sf}, nil
	}
	caps := Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
	return NewClient(id, label, root, NewPool(dial, poolSize, idle), caps, cacheTTL)
}

func hostKeyChecker(o SFTPOptions) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := Fingerprint(key)
		if o.HostKey == "" {
			// Erstkontakt: Fingerabdruck merken (Trust On First Use).
			if o.OnHostKey != nil {
				o.OnHostKey(fp)
			}
			return nil
		}
		if o.HostKey != fp {
			return fmt.Errorf("Hostschlüssel weicht ab: erwartet %s, erhalten %s", o.HostKey, fp)
		}
		return nil
	}
}

func sftpPath(p string) string {
	p = Clean(p)
	if p == "" {
		return "/"
	}
	return "/" + p
}

func (c *sftpConn) List(ctx context.Context, p string) ([]Entry, error) {
	fis, err := c.c.ReadDirContext(ctx, sftpPath(p))
	if err != nil {
		return nil, mapOSErr(err)
	}
	out := make([]Entry, 0, len(fis))
	for _, fi := range fis {
		e := Entry{Name: fi.Name(), Size: fi.Size(), IsDir: fi.IsDir(), ModTime: fi.ModTime()}
		if fi.Mode()&os.ModeSymlink != 0 {
			e.Symlink = true
			if t, err := c.c.Stat(sftpPath(Join(p, fi.Name()))); err == nil {
				e.IsDir = t.IsDir()
				e.Size = t.Size()
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (c *sftpConn) Stat(ctx context.Context, p string) (Entry, error) {
	fi, err := c.c.Stat(sftpPath(p))
	if err != nil {
		return Entry{}, mapOSErr(err)
	}
	name := Base(p)
	if name == "" {
		name = "/"
	}
	return Entry{Name: name, Size: fi.Size(), IsDir: fi.IsDir(), ModTime: fi.ModTime()}, nil
}

func (c *sftpConn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	f, err := c.c.Open(sftpPath(p))
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

func (c *sftpConn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	f, err := c.c.Open(sftpPath(p))
	if err != nil {
		return nil, 0, mapOSErr(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, mapOSErr(err)
	}
	return f, fi.Size(), nil
}

func (c *sftpConn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	f, err := c.c.Create(sftpPath(p))
	if err != nil {
		return 0, mapOSErr(err)
	}
	n, err := f.ReadFrom(r)
	cerr := f.Close()
	if err != nil {
		return n, err
	}
	return n, cerr
}

func (c *sftpConn) Mkdir(ctx context.Context, p string) error {
	return mapOSErr(c.c.Mkdir(sftpPath(p)))
}

func (c *sftpConn) Remove(ctx context.Context, p string, recursive bool) error {
	if recursive {
		return mapOSErr(c.c.RemoveAll(sftpPath(p)))
	}
	return mapOSErr(c.c.Remove(sftpPath(p)))
}

func (c *sftpConn) Rename(ctx context.Context, oldp, newp string) error {
	if err := c.c.PosixRename(sftpPath(oldp), sftpPath(newp)); err == nil {
		return nil
	}
	return mapOSErr(c.c.Rename(sftpPath(oldp), sftpPath(newp)))
}

func (c *sftpConn) Copy(ctx context.Context, oldp, newp string) error { return ErrNotSupported }

func (c *sftpConn) SetModTime(ctx context.Context, p string, t time.Time) error {
	return mapOSErr(c.c.Chtimes(sftpPath(p), t, t))
}

func (c *sftpConn) Space(ctx context.Context, p string) (Space, error) {
	st, err := c.c.StatVFS(sftpPath(p))
	if err != nil {
		return Space{}, ErrNotSupported
	}
	return Space{
		Total: int64(st.Blocks * st.Bsize),
		Free:  int64(st.Bavail * st.Bsize),
	}, nil
}

func (c *sftpConn) Ping(ctx context.Context) error {
	_, err := c.c.Getwd()
	return mapOSErr(err)
}

func (c *sftpConn) Close() error {
	if c.c != nil {
		_ = c.c.Close()
	}
	if c.ssh != nil {
		return c.ssh.Close()
	}
	return nil
}

func (c *sftpConn) Caps() Caps {
	return Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
}
