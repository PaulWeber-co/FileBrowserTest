// Package config lädt und schreibt die Konfiguration von SpeedNAS.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config ist der komplette Konfigurationsbaum.
type Config struct {
	Server      ServerConfig `json:"server"`
	Auth        AuthConfig   `json:"auth"`
	Performance PerfConfig   `json:"performance"`
	Locations   []Location   `json:"locations"`

	path string
	mu   sync.RWMutex
}

// ServerConfig steuert den HTTP-Server.
type ServerConfig struct {
	Listen    string    `json:"listen"`  // z. B. ":8088" oder "127.0.0.1:8088"
	DataDir   string    `json:"dataDir"` // Cache, Sitzungen, Freigabelinks
	TLS       TLSConfig `json:"tls"`
	PublicURL string    `json:"publicUrl"` // für Freigabelinks, optional
}

// TLSConfig steuert HTTPS.
type TLSConfig struct {
	Enabled  bool     `json:"enabled"`
	CertFile string   `json:"certFile"`
	KeyFile  string   `json:"keyFile"`
	SelfSign bool     `json:"selfSigned"` // Zertifikat automatisch erzeugen
	Hosts    []string `json:"hosts"`      // Namen/IPs im selbstsignierten Zertifikat
}

// AuthConfig steuert die Anmeldung an SpeedNAS selbst.
type AuthConfig struct {
	Enabled    bool   `json:"enabled"`
	Users      []User `json:"users"`
	SessionTTL int    `json:"sessionTtlHours"`
	// LocalOnlyNoAuth erlaubt Zugriffe aus dem eigenen Subnetz ohne Anmeldung.
	// Praktisch zu Hause, sollte bei Portfreigabe aus bleiben.
	LocalOnlyNoAuth bool `json:"localOnlyNoAuth"`
}

// User ist ein SpeedNAS-Benutzer (nicht der NAS-Benutzer).
type User struct {
	Name     string `json:"name"`
	Hash     string `json:"passwordHash"` // bcrypt
	Admin    bool   `json:"admin"`
	ReadOnly bool   `json:"readOnly"`
}

// PerfConfig buendelt die Stellschrauben für Durchsatz und Latenz.
type PerfConfig struct {
	PrefetchWorkers  int `json:"prefetchWorkers"`  // parallele Leseanfragen pro Download
	PrefetchChunkKB  int `json:"prefetchChunkKb"`  // Größe einer Leseanfrage
	ListCacheSeconds int `json:"listCacheSeconds"` // Cache für Verzeichnisse
	ThumbCacheMB     int `json:"thumbCacheMb"`     // Plattencache für Vorschaubilder
	ThumbWorkers     int `json:"thumbWorkers"`     // parallele Vorschau-Erzeugung
	SearchWorkers    int `json:"searchWorkers"`    // parallele Verzeichnisse bei der Suche
	UploadPartMB     int `json:"uploadPartMb"`     // Puffer beim Hochladen
}

// Location ist ein Speicherort in der Seitenleiste.
type Location struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Type     string `json:"type"` // smb | ftp | sftp | webdav | local
	Root     string `json:"root"` // Unterordner innerhalb der Freigabe
	ReadOnly bool   `json:"readOnly"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
	PoolSize int    `json:"poolSize,omitempty"`

	SMB    *SMBConf    `json:"smb,omitempty"`
	FTP    *FTPConf    `json:"ftp,omitempty"`
	SFTP   *SFTPConf   `json:"sftp,omitempty"`
	WebDAV *WebDAVConf `json:"webdav,omitempty"`
	Local  *LocalConf  `json:"local,omitempty"`
}

// SMBConf beschreibt eine SMB-Freigabe (Speedport, Fritzbox, NAS, Windows).
type SMBConf struct {
	Host        string `json:"host"`
	Port        int    `json:"port,omitempty"`
	Share       string `json:"share"`
	User        string `json:"user,omitempty"`
	Password    string `json:"password,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Dialect     string `json:"dialect,omitempty"` // "" = automatisch
	RequireSign bool   `json:"requireSigning,omitempty"`
	MaxCredits  int    `json:"maxCredits,omitempty"`
}

// FTPConf beschreibt einen FTP-/FTPS-Zugang.
type FTPConf struct {
	Host        string `json:"host"`
	Port        int    `json:"port,omitempty"`
	User        string `json:"user,omitempty"`
	Password    string `json:"password,omitempty"`
	TLS         string `json:"tls,omitempty"` // none | explicit | implicit
	SkipVerify  bool   `json:"skipVerify,omitempty"`
	DisableEPSV bool   `json:"disableEpsv,omitempty"`
}

// SFTPConf beschreibt einen SFTP-Zugang.
type SFTPConf struct {
	Host       string `json:"host"`
	Port       int    `json:"port,omitempty"`
	User       string `json:"user"`
	Password   string `json:"password,omitempty"`
	KeyFile    string `json:"keyFile,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	HostKey    string `json:"hostKey,omitempty"`
}

// WebDAVConf beschreibt einen WebDAV-Zugang.
type WebDAVConf struct {
	URL        string `json:"url"`
	User       string `json:"user,omitempty"`
	Password   string `json:"password,omitempty"`
	SkipVerify bool   `json:"skipVerify,omitempty"`
}

// LocalConf bindet ein Verzeichnis des Rechners ein.
type LocalConf struct {
	Path string `json:"path"`
}

// Default liefert eine sinnvolle Grundkonfiguration.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:  ":8088",
			DataDir: defaultDataDir(),
			TLS:     TLSConfig{Enabled: false, SelfSign: true},
		},
		Auth: AuthConfig{
			Enabled:    true,
			SessionTTL: 24 * 14,
		},
		Performance: PerfConfig{
			PrefetchWorkers:  4,
			PrefetchChunkKB:  1024,
			ListCacheSeconds: 5,
			ThumbCacheMB:     512,
			ThumbWorkers:     3,
			SearchWorkers:    6,
			UploadPartMB:     4,
		},
	}
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "speednas")
	}
	return "speednas-data"
}

// DefaultPath liefert den Standardort der Konfigurationsdatei.
func DefaultPath() string {
	return filepath.Join(defaultDataDir(), "config.json")
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv ersetzt ${NAME} durch die Umgebungsvariable. So können Passwörter
// aus der Umgebung kommen, statt im Klartext in der Datei zu stehen.
func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return m
	})
}

// Load liest die Konfiguration. Existiert sie nicht, wird eine Vorgabe
// geschrieben und zurückgegeben.
func Load(path string) (*Config, bool, error) {
	if path == "" {
		path = DefaultPath()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(abs)
	if os.IsNotExist(err) {
		c := Default()
		c.path = abs
		if err := c.Save(); err != nil {
			return nil, false, fmt.Errorf("Konfiguration konnte nicht angelegt werden: %w", err)
		}
		return c, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	c := Default()
	if err := json.Unmarshal(data, c); err != nil {
		return nil, false, fmt.Errorf("%s ist kein gültiges JSON: %w", abs, err)
	}
	c.path = abs
	c.normalize()
	return c, false, nil
}

func (c *Config) normalize() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8088"
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = defaultDataDir()
	}
	if c.Auth.SessionTTL <= 0 {
		c.Auth.SessionTTL = 24 * 14
	}
	p := &c.Performance
	if p.PrefetchWorkers <= 0 {
		p.PrefetchWorkers = 4
	}
	if p.PrefetchChunkKB <= 0 {
		p.PrefetchChunkKB = 1024
	}
	if p.ListCacheSeconds < 0 {
		p.ListCacheSeconds = 0
	}
	if p.ThumbCacheMB <= 0 {
		p.ThumbCacheMB = 512
	}
	if p.ThumbWorkers <= 0 {
		p.ThumbWorkers = 3
	}
	if p.SearchWorkers <= 0 {
		p.SearchWorkers = 6
	}
	if p.UploadPartMB <= 0 {
		p.UploadPartMB = 4
	}
	seen := map[string]bool{}
	for i := range c.Locations {
		l := &c.Locations[i]
		if l.ID == "" || seen[l.ID] {
			l.ID = NewID()
		}
		seen[l.ID] = true
		if l.Label == "" {
			l.Label = l.ID
		}
		l.Type = strings.ToLower(l.Type)
	}
}

// Path liefert den Ort der Konfigurationsdatei.
func (c *Config) Path() string { return c.path }

// SetPath setzt den Speicherort (für Tests und den --config-Schalter).
func (c *Config) SetPath(p string) { c.path = p }

// Save schreibt die Konfiguration atomar und nur für den Besitzer lesbar -
// sie enthält Zugangsdaten zum NAS.
func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.saveLocked()
}

func (c *Config) saveLocked() error {
	if c.path == "" {
		return fmt.Errorf("kein Konfigurationspfad gesetzt")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(c.path, 0o600)
}

// Snapshot liefert eine Kopie der Speicherorte (thread-sicher).
func (c *Config) Snapshot() []Location {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Location, len(c.Locations))
	copy(out, c.Locations)
	return out
}

// UpsertLocation legt einen Speicherort an oder aktualisiert ihn.
func (c *Config) UpsertLocation(l Location) (Location, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l.ID == "" {
		l.ID = NewID()
	}
	if l.Label == "" {
		l.Label = l.Type
	}
	found := false
	for i := range c.Locations {
		if c.Locations[i].ID == l.ID {
			// Leere Passwortfelder bedeuten "unverändert lassen".
			l = mergeSecrets(c.Locations[i], l)
			c.Locations[i] = l
			found = true
			break
		}
	}
	if !found {
		c.Locations = append(c.Locations, l)
	}
	return l, c.saveLocked()
}

// mergeSecrets übernimmt alte Passwörter, wenn das Formular sie leer lässt.
func mergeSecrets(old, upd Location) Location {
	if upd.SMB != nil && old.SMB != nil && upd.SMB.Password == "" {
		upd.SMB.Password = old.SMB.Password
	}
	if upd.FTP != nil && old.FTP != nil && upd.FTP.Password == "" {
		upd.FTP.Password = old.FTP.Password
	}
	if upd.SFTP != nil && old.SFTP != nil {
		if upd.SFTP.Password == "" {
			upd.SFTP.Password = old.SFTP.Password
		}
		if upd.SFTP.HostKey == "" {
			upd.SFTP.HostKey = old.SFTP.HostKey
		}
	}
	if upd.WebDAV != nil && old.WebDAV != nil && upd.WebDAV.Password == "" {
		upd.WebDAV.Password = old.WebDAV.Password
	}
	return upd
}

// DeleteLocation entfernt einen Speicherort.
func (c *Config) DeleteLocation(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.Locations[:0]
	for _, l := range c.Locations {
		if l.ID != id {
			out = append(out, l)
		}
	}
	c.Locations = out
	return c.saveLocked()
}

// SetHostKey merkt sich den SSH-Fingerabdruck nach dem ersten Verbinden.
func (c *Config) SetHostKey(id, fp string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Locations {
		if c.Locations[i].ID == id && c.Locations[i].SFTP != nil {
			if c.Locations[i].SFTP.HostKey == "" {
				c.Locations[i].SFTP.HostKey = fp
				_ = c.saveLocked()
			}
			return
		}
	}
}

// UpsertUser legt einen Benutzer an oder ändert sein Passwort.
func (c *Config) UpsertUser(u User) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Auth.Users {
		if strings.EqualFold(c.Auth.Users[i].Name, u.Name) {
			if u.Hash == "" {
				u.Hash = c.Auth.Users[i].Hash
			}
			c.Auth.Users[i] = u
			return c.saveLocked()
		}
	}
	c.Auth.Users = append(c.Auth.Users, u)
	return c.saveLocked()
}

// DeleteUser entfernt einen Benutzer.
func (c *Config) DeleteUser(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.Auth.Users[:0]
	for _, u := range c.Auth.Users {
		if !strings.EqualFold(u.Name, name) {
			out = append(out, u)
		}
	}
	c.Auth.Users = out
	return c.saveLocked()
}

// FindUser sucht einen Benutzer unabhängig von Groß-/Kleinschreibung.
func (c *Config) FindUser(name string) (User, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, u := range c.Auth.Users {
		if strings.EqualFold(u.Name, name) {
			return u, true
		}
	}
	return User{}, false
}

// Users liefert eine Kopie der Benutzerliste.
func (c *Config) Users() []User {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]User, len(c.Auth.Users))
	copy(out, c.Auth.Users)
	return out
}

// SetPerformance aktualisiert die Leistungsparameter.
func (c *Config) SetPerformance(p PerfConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Performance = p
	c.normalizeLocked()
	return c.saveLocked()
}

func (c *Config) normalizeLocked() {
	p := &c.Performance
	if p.PrefetchWorkers < 1 {
		p.PrefetchWorkers = 1
	}
	if p.PrefetchWorkers > 16 {
		p.PrefetchWorkers = 16
	}
	if p.PrefetchChunkKB < 64 {
		p.PrefetchChunkKB = 64
	}
	if p.PrefetchChunkKB > 8192 {
		p.PrefetchChunkKB = 8192
	}
	if p.ThumbWorkers < 1 {
		p.ThumbWorkers = 1
	}
	if p.SearchWorkers < 1 {
		p.SearchWorkers = 1
	}
	if p.UploadPartMB < 1 {
		p.UploadPartMB = 1
	}
}

// Perf liefert eine Kopie der Leistungsparameter.
func (c *Config) Perf() PerfConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Performance
}

// AuthSettings liefert eine Kopie der Anmeldeeinstellungen.
func (c *Config) AuthSettings() AuthConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	a := c.Auth
	a.Users = nil // Hashes gehören nicht in die API-Antwort
	return a
}

// SessionLifetime liefert die Sitzungsdauer.
func (c *Config) SessionLifetime() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Duration(c.Auth.SessionTTL) * time.Hour
}

// Resolve löst ${ENV}-Verweise in allen Zugangsdaten auf.
func (l Location) Resolve() Location {
	if l.SMB != nil {
		s := *l.SMB
		s.User, s.Password = expandEnv(s.User), expandEnv(s.Password)
		s.Host, s.Share = expandEnv(s.Host), expandEnv(s.Share)
		l.SMB = &s
	}
	if l.FTP != nil {
		f := *l.FTP
		f.User, f.Password, f.Host = expandEnv(f.User), expandEnv(f.Password), expandEnv(f.Host)
		l.FTP = &f
	}
	if l.SFTP != nil {
		s := *l.SFTP
		s.User, s.Password, s.Host = expandEnv(s.User), expandEnv(s.Password), expandEnv(s.Host)
		s.Passphrase = expandEnv(s.Passphrase)
		l.SFTP = &s
	}
	if l.WebDAV != nil {
		w := *l.WebDAV
		w.User, w.Password, w.URL = expandEnv(w.User), expandEnv(w.Password), expandEnv(w.URL)
		l.WebDAV = &w
	}
	if l.Local != nil {
		lo := *l.Local
		lo.Path = expandEnv(lo.Path)
		l.Local = &lo
	}
	return l
}

// Redacted entfernt Passwörter, bevor eine Konfiguration an den Browser geht.
func (l Location) Redacted() Location {
	if l.SMB != nil {
		s := *l.SMB
		s.Password = maskSecret(s.Password)
		l.SMB = &s
	}
	if l.FTP != nil {
		f := *l.FTP
		f.Password = maskSecret(f.Password)
		l.FTP = &f
	}
	if l.SFTP != nil {
		s := *l.SFTP
		s.Password = maskSecret(s.Password)
		s.Passphrase = maskSecret(s.Passphrase)
		l.SFTP = &s
	}
	if l.WebDAV != nil {
		w := *l.WebDAV
		w.Password = maskSecret(w.Password)
		l.WebDAV = &w
	}
	return l
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return "********"
}

// Validate prüft eine Ortsdefinition auf Vollständigkeit.
func (l Location) Validate() error {
	switch l.Type {
	case "smb":
		if l.SMB == nil || l.SMB.Host == "" || l.SMB.Share == "" {
			return fmt.Errorf("SMB braucht Host und Freigabenamen")
		}
	case "ftp":
		if l.FTP == nil || l.FTP.Host == "" {
			return fmt.Errorf("FTP braucht einen Host")
		}
	case "sftp":
		if l.SFTP == nil || l.SFTP.Host == "" || l.SFTP.User == "" {
			return fmt.Errorf("SFTP braucht Host und Benutzer")
		}
	case "webdav":
		if l.WebDAV == nil || l.WebDAV.URL == "" {
			return fmt.Errorf("WebDAV braucht eine URL")
		}
	case "local":
		if l.Local == nil || l.Local.Path == "" {
			return fmt.Errorf("Lokal braucht einen Pfad")
		}
	default:
		return fmt.Errorf("unbekannter Typ %q", l.Type)
	}
	return nil
}
