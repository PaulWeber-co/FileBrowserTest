package vfs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	smb2 "github.com/hirochachacha/go-smb2"
)

// SMBOptions beschreibt eine SMB-/CIFS-Freigabe.
type SMBOptions struct {
	Host        string // Hostname oder IP, z. B. 192.168.2.1
	Port        int    // 445 (Standard) oder 139
	Share       string // Freigabename, z. B. "USB-Speicher"
	User        string // leer = Gastzugang
	Password    string
	Domain      string
	Workstation string
	Dialect     string // "", "auto", "2.0.2", "2.1", "3.0", "3.0.2", "3.1.1"
	RequireSign bool   // Signierung erzwingen
	MaxCredits  uint16 // 0 = Bibliotheksvorgabe
	DialTimeout time.Duration
	PoolSize    int
	IdleTimeout time.Duration
}

func (o *SMBOptions) addr() string {
	port := o.Port
	if port == 0 {
		port = 445
	}
	return net.JoinHostPort(o.Host, fmt.Sprint(port))
}

// DialectCode übersetzt eine Dialektangabe in den SMB2-Zahlencode.
// 0 bedeutet: automatisch aushandeln.
func DialectCode(s string) uint16 {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "2.0.2", "2.0", "202":
		return 0x0202
	case "2.1", "210":
		return 0x0210
	case "3.0", "300":
		return 0x0300
	case "3.0.2", "302":
		return 0x0302
	case "3.1.1", "311":
		return 0x0311
	default:
		return 0
	}
}

// DialectName ist die Umkehrung von DialectCode.
func DialectName(code uint16) string {
	switch code {
	case 0x0202:
		return "SMB 2.0.2"
	case 0x0210:
		return "SMB 2.1"
	case 0x0300:
		return "SMB 3.0"
	case 0x0302:
		return "SMB 3.0.2"
	case 0x0311:
		return "SMB 3.1.1"
	case 0x02ff:
		return "SMB 2 (Wildcard)"
	case 0:
		return "automatisch"
	default:
		return fmt.Sprintf("0x%04x", code)
	}
}

func (o *SMBOptions) dialer() *smb2.Dialer {
	user := o.User
	pass := o.Password
	if user == "" {
		// Speedport-Freigaben laufen häufig ohne Benutzerverwaltung. Ein
		// leerer Benutzername wird von vielen Servern abgelehnt, "guest"
		// dagegen akzeptiert.
		user = "guest"
		pass = ""
	}
	return &smb2.Dialer{
		MaxCreditBalance: o.MaxCredits,
		Negotiator: smb2.Negotiator{
			RequireMessageSigning: o.RequireSign,
			SpecifiedDialect:      DialectCode(o.Dialect),
		},
		Initiator: &smb2.NTLMInitiator{
			User:        user,
			Password:    pass,
			Domain:      o.Domain,
			Workstation: o.Workstation,
		},
	}
}

func (o *SMBOptions) dialSession(ctx context.Context) (net.Conn, *smb2.Session, error) {
	timeout := o.DialTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", o.addr())
	if err != nil {
		return nil, nil, fmt.Errorf("TCP-Verbindung zu %s fehlgeschlagen: %w", o.addr(), err)
	}
	// TCP_NODELAY ist bei SMB wichtig: die Nagle-Verzögerung addiert sonst
	// bis zu 40 ms auf jede kleine Anfrage.
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	sess, err := o.dialer().DialContext(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("SMB-Anmeldung fehlgeschlagen: %w", err)
	}
	return conn, sess, nil
}

// SMBListShares meldet die Freigabenamen eines Servers - für den
// Einrichtungsassistenten, damit niemand den Namen raten muss.
func SMBListShares(ctx context.Context, o SMBOptions) ([]string, error) {
	conn, sess, err := o.dialSession(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	defer sess.Logoff()

	names, err := sess.WithContext(ctx).ListSharenames()
	if err != nil {
		return nil, fmt.Errorf("Freigaben konnten nicht gelistet werden: %w", err)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasSuffix(n, "$") { // IPC$, ADMIN$, C$ ... sind Verwaltungsfreigaben
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

type smbConn struct {
	conn  net.Conn
	sess  *smb2.Session
	share *smb2.Share
}

// NewSMB erzeugt einen Client für eine SMB-Freigabe.
func NewSMB(id, label, root string, o SMBOptions, cacheTTL time.Duration) *Client {
	poolSize := o.PoolSize
	if poolSize <= 0 {
		poolSize = 4
	}
	idle := o.IdleTimeout
	if idle <= 0 {
		// Router schließen inaktive SMB-Sitzungen gern nach kurzer Zeit.
		// 45 s liegt sicher darunter und hält trotzdem alles warm.
		idle = 45 * time.Second
	}
	opts := o
	dial := func(ctx context.Context) (Conn, error) {
		conn, sess, err := opts.dialSession(ctx)
		if err != nil {
			return nil, err
		}
		share, err := sess.WithContext(ctx).Mount(opts.Share)
		if err != nil {
			sess.Logoff()
			conn.Close()
			return nil, fmt.Errorf("Freigabe %q nicht erreichbar: %w", opts.Share, err)
		}
		return &smbConn{conn: conn, sess: sess, share: share}, nil
	}
	caps := Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
	return NewClient(id, label, root, NewPool(dial, poolSize, idle), caps, cacheTTL)
}

func (c *smbConn) fs(ctx context.Context) *smb2.Share { return c.share.WithContext(ctx) }

func (c *smbConn) List(ctx context.Context, p string) ([]Entry, error) {
	fis, err := c.fs(ctx).ReadDir(smbPath(p))
	if err != nil {
		return nil, mapOSErr(err)
	}
	out := make([]Entry, 0, len(fis))
	for _, fi := range fis {
		n := fi.Name()
		if n == "." || n == ".." {
			continue
		}
		out = append(out, Entry{
			Name:    n,
			Size:    fi.Size(),
			IsDir:   fi.IsDir(),
			ModTime: fi.ModTime(),
		})
	}
	return out, nil
}

func (c *smbConn) Stat(ctx context.Context, p string) (Entry, error) {
	fi, err := c.fs(ctx).Stat(smbPath(p))
	if err != nil {
		return Entry{}, mapOSErr(err)
	}
	name := Base(p)
	if name == "" {
		name = "/"
	}
	return Entry{Name: name, Size: fi.Size(), IsDir: fi.IsDir(), ModTime: fi.ModTime()}, nil
}

func (c *smbConn) Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	f, err := c.fs(ctx).Open(smbPath(p))
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

func (c *smbConn) ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error) {
	f, err := c.fs(ctx).Open(smbPath(p))
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

func (c *smbConn) Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error) {
	f, err := c.fs(ctx).OpenFile(smbPath(p), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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

func (c *smbConn) Mkdir(ctx context.Context, p string) error {
	return mapOSErr(c.fs(ctx).Mkdir(smbPath(p), 0o755))
}

func (c *smbConn) Remove(ctx context.Context, p string, recursive bool) error {
	if recursive {
		return mapOSErr(c.fs(ctx).RemoveAll(smbPath(p)))
	}
	return mapOSErr(c.fs(ctx).Remove(smbPath(p)))
}

func (c *smbConn) Rename(ctx context.Context, oldp, newp string) error {
	return mapOSErr(c.fs(ctx).Rename(smbPath(oldp), smbPath(newp)))
}

func (c *smbConn) Copy(ctx context.Context, oldp, newp string) error { return ErrNotSupported }

func (c *smbConn) SetModTime(ctx context.Context, p string, t time.Time) error {
	return mapOSErr(c.fs(ctx).Chtimes(smbPath(p), t, t))
}

func (c *smbConn) Space(ctx context.Context, p string) (Space, error) {
	info, err := c.fs(ctx).Statfs(smbPath(p))
	if err != nil {
		return Space{}, mapOSErr(err)
	}
	bs := int64(info.BlockSize())
	if bs == 0 {
		bs = int64(info.FragmentSize())
	}
	return Space{
		Total: int64(info.TotalBlockCount()) * bs,
		Free:  int64(info.AvailableBlockCount()) * bs,
	}, nil
}

func (c *smbConn) Ping(ctx context.Context) error {
	_, err := c.fs(ctx).Stat("")
	return mapOSErr(err)
}

func (c *smbConn) Close() error {
	if c.share != nil {
		_ = c.share.Umount()
	}
	if c.sess != nil {
		_ = c.sess.Logoff()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *smbConn) Caps() Caps {
	return Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
}

// smbPath übersetzt kanonische Pfade in die von go-smb2 erwartete Form.
// Die Bibliothek normalisiert "/" selbst zu "\", ein leerer Pfad ist die
// Freigabewurzel.
func smbPath(p string) string { return Clean(p) }
