// Package vfs stellt eine einheitliche Dateisystem-Abstraktion über
// verschiedene Netzwerkprotokolle bereit (SMB, FTP, SFTP, WebDAV, lokal).
//
// Alle Pfade sind intern kanonisch: mit "/" getrennt, ohne führenden oder
// abschließenden Slash, "" bezeichnet die Wurzel der Freigabe.
package vfs

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"
)

// Fehler, die von allen Backends einheitlich gemeldet werden.
var (
	ErrNotSupported = errors.New("vfs: operation nicht unterstützt")
	ErrNotFound     = errors.New("vfs: nicht gefunden")
	ErrExists       = errors.New("vfs: existiert bereits")
	ErrPermission   = errors.New("vfs: keine Berechtigung")
	ErrIsDir        = errors.New("vfs: ist ein Verzeichnis")
	ErrNotDir       = errors.New("vfs: ist kein Verzeichnis")
	ErrClosed       = errors.New("vfs: verbindung geschlossen")
)

// Entry beschreibt einen Verzeichniseintrag.
type Entry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"dir"`
	ModTime time.Time `json:"mtime"`
	Symlink bool      `json:"link,omitempty"`
}

// Caps beschreibt, was ein Backend kann. Die UI blendet nicht verfügbare
// Aktionen aus, statt sie mit einem Fehler quittieren zu lassen.
type Caps struct {
	RandomRead bool `json:"randomRead"` // ReaderAt / echtes Seeking
	Rename     bool `json:"rename"`     // serverseitiges Umbenennen/Verschieben
	ServerCopy bool `json:"serverCopy"` // serverseitiges Kopieren (WebDAV COPY)
	Recursive  bool `json:"recursive"`  // natives rekursives Löschen
	SetModTime bool `json:"setModTime"` // Zeitstempel setzbar
	SpaceInfo  bool `json:"spaceInfo"`  // freier Speicherplatz abfragbar
}

// Space beschreibt die Belegung eines Volumes.
type Space struct {
	Total int64 `json:"total"`
	Free  int64 `json:"free"`
}

// ReaderAtCloser buendelt wahlfreien Lesezugriff mit Freigabe der Ressource.
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}

// Conn ist eine einzelne physische Verbindung zu einem Speicherort.
//
// Implementierungen dürfen davon ausgehen, dass jeweils nur eine Goroutine
// gleichzeitig auf einer Conn arbeitet - der Pool stellt das sicher. Das ist
// nötig, weil z. B. FTP-Kontrollverbindungen nicht nebenläufig nutzbar sind.
type Conn interface {
	List(ctx context.Context, p string) ([]Entry, error)
	Stat(ctx context.Context, p string) (Entry, error)

	// Reader liefert einen Stream ab Byte-Offset off.
	Reader(ctx context.Context, p string, off int64) (io.ReadCloser, error)
	// ReaderAt liefert wahlfreien Lesezugriff; ErrNotSupported, wenn das
	// Protokoll das nicht effizient kann (FTP).
	ReaderAt(ctx context.Context, p string) (ReaderAtCloser, int64, error)

	// Write schreibt r vollständig nach p. size darf -1 sein (unbekannt).
	Write(ctx context.Context, p string, r io.Reader, size int64) (int64, error)

	Mkdir(ctx context.Context, p string) error
	Remove(ctx context.Context, p string, recursive bool) error
	Rename(ctx context.Context, oldp, newp string) error
	Copy(ctx context.Context, oldp, newp string) error
	SetModTime(ctx context.Context, p string, t time.Time) error
	Space(ctx context.Context, p string) (Space, error)

	// Ping prüft billig, ob die Verbindung noch lebt.
	Ping(ctx context.Context) error
	Close() error
	Caps() Caps
}

// Clean normalisiert einen von außen kommenden Pfad in die kanonische Form.
// Ausbrüche via ".." werden entfernt - der Aufrufer kann nie oberhalb der
// Freigabewurzel landen.
func Clean(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

// Join verbindet Pfadsegmente kanonisch.
func Join(elem ...string) string {
	return Clean(path.Join(elem...))
}

// Dir liefert das Elternverzeichnis in kanonischer Form.
func Dir(p string) string {
	p = Clean(p)
	if !strings.Contains(p, "/") {
		return ""
	}
	return path.Dir(p)
}

// Base liefert den Dateinamen.
func Base(p string) string {
	p = Clean(p)
	if p == "" {
		return ""
	}
	return path.Base(p)
}

// ValidName prüft einen einzelnen Namensbestandteil (kein Pfad).
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	return true
}
