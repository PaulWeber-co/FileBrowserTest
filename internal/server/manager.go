package server

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

// Manager hält für jeden konfigurierten Speicherort einen VFS-Client mit
// eigenem Verbindungspool. Clients werden erst beim ersten Zugriff gebaut -
// ein nicht erreichbarer Ort blockiert so nicht den Programmstart.
type Manager struct {
	cfg *config.Config

	mu      sync.Mutex
	clients map[string]*vfs.Client
}

// NewManager erzeugt einen Manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, clients: map[string]*vfs.Client{}}
}

// Location sucht eine Ortsdefinition.
func (m *Manager) Location(id string) (config.Location, bool) {
	for _, l := range m.cfg.Snapshot() {
		if l.ID == id {
			return l, true
		}
	}
	return config.Location{}, false
}

// Get liefert den Client zu einem Ort und baut ihn bei Bedarf auf.
func (m *Manager) Get(id string) (*vfs.Client, config.Location, error) {
	loc, ok := m.Location(id)
	if !ok {
		return nil, config.Location{}, fmt.Errorf("Speicherort %q ist nicht konfiguriert", id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[id]; ok {
		return c, loc, nil
	}
	c, err := m.build(loc)
	if err != nil {
		return nil, loc, err
	}
	m.clients[id] = c
	return c, loc, nil
}

// Build erzeugt einen Client, ohne ihn zu registrieren - für den Verbindungstest.
func (m *Manager) Build(l config.Location) (*vfs.Client, error) {
	return m.build(l)
}

func (m *Manager) build(l config.Location) (*vfs.Client, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	r := l.Resolve()
	perf := m.cfg.Perf()
	cacheTTL := time.Duration(perf.ListCacheSeconds) * time.Second

	switch r.Type {
	case "smb":
		// SMB1 ist ein eigener Client, kein Dialekt von SMB2.
		if vfs.IsSMB1Dialect(r.SMB.Dialect) {
			return vfs.NewSMB1(r.ID, r.Label, r.Root, vfs.SMB1Options{
				Host:     r.SMB.Host,
				Port:     r.SMB.Port,
				Share:    r.SMB.Share,
				User:     r.SMB.User,
				Password: r.SMB.Password,
				Domain:   r.SMB.Domain,
				PoolSize: r.PoolSize,
			}, cacheTTL), nil
		}
		return vfs.NewSMB(r.ID, r.Label, r.Root, vfs.SMBOptions{
			Host:        r.SMB.Host,
			Port:        r.SMB.Port,
			Share:       r.SMB.Share,
			User:        r.SMB.User,
			Password:    r.SMB.Password,
			Domain:      r.SMB.Domain,
			Dialect:     r.SMB.Dialect,
			RequireSign: r.SMB.RequireSign,
			MaxCredits:  uint16(r.SMB.MaxCredits),
			PoolSize:    r.PoolSize,
		}, cacheTTL), nil

	case "ftp":
		return vfs.NewFTP(r.ID, r.Label, r.Root, vfs.FTPOptions{
			Host:        r.FTP.Host,
			Port:        r.FTP.Port,
			User:        r.FTP.User,
			Password:    r.FTP.Password,
			TLS:         r.FTP.TLS,
			SkipVerify:  r.FTP.SkipVerify,
			DisableEPSV: r.FTP.DisableEPSV,
			PoolSize:    r.PoolSize,
		}, cacheTTL), nil

	case "sftp":
		var key []byte
		if r.SFTP.KeyFile != "" {
			b, err := os.ReadFile(r.SFTP.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("Schlüsseldatei nicht lesbar: %w", err)
			}
			key = b
		}
		id := r.ID
		return vfs.NewSFTP(r.ID, r.Label, r.Root, vfs.SFTPOptions{
			Host:       r.SFTP.Host,
			Port:       r.SFTP.Port,
			User:       r.SFTP.User,
			Password:   r.SFTP.Password,
			PrivateKey: key,
			Passphrase: r.SFTP.Passphrase,
			HostKey:    r.SFTP.HostKey,
			PoolSize:   r.PoolSize,
			OnHostKey:  func(fp string) { m.cfg.SetHostKey(id, fp) },
		}, cacheTTL), nil

	case "webdav":
		return vfs.NewWebDAV(r.ID, r.Label, r.Root, vfs.WebDAVOptions{
			URL:        r.WebDAV.URL,
			User:       r.WebDAV.User,
			Password:   r.WebDAV.Password,
			SkipVerify: r.WebDAV.SkipVerify,
			PoolSize:   r.PoolSize,
		}, cacheTTL), nil

	case "local":
		return vfs.NewLocal(r.ID, r.Label, r.Local.Path, cacheTTL)
	}
	return nil, fmt.Errorf("unbekannter Typ %q", r.Type)
}

// Drop schließt den Client eines Ortes - etwa nach einer Konfigurationsänderung.
func (m *Manager) Drop(id string) {
	m.mu.Lock()
	c, ok := m.clients[id]
	delete(m.clients, id)
	m.mu.Unlock()
	if ok {
		_ = c.Close()
	}
}

// Reload verwirft alle Clients; sie werden beim nächsten Zugriff neu gebaut.
func (m *Manager) Reload() {
	m.mu.Lock()
	old := m.clients
	m.clients = map[string]*vfs.Client{}
	m.mu.Unlock()
	for _, c := range old {
		_ = c.Close()
	}
}

// Close beendet alle Verbindungen.
func (m *Manager) Close() { m.Reload() }

// Active meldet, zu welchen Orten gerade Verbindungen offen sind.
func (m *Manager) Active() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for id, c := range m.clients {
		idle, _ := c.Pool().Stats()
		out[id] = idle
	}
	return out
}
