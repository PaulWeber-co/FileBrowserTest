package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
)

const sessionCookie = "speednas_session"

type sessionRecord struct {
	User    string    `json:"user"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	Agent   string    `json:"agent,omitempty"`
	IP      string    `json:"ip,omitempty"`
}

// Identity ist der angemeldete Benutzer im Kontext einer Anfrage.
type Identity struct {
	Name     string
	Admin    bool
	ReadOnly bool
	Guest    bool
}

// CanWrite meldet, ob dieser Benutzer Aenderungen vornehmen darf.
func (i Identity) CanWrite() bool { return !i.ReadOnly }

// HashPassword erzeugt einen bcrypt-Hash.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// createSession legt eine Sitzung an und setzt das Cookie.
func (a *App) createSession(w http.ResponseWriter, r *http.Request, user string) {
	token := newToken()
	ttl := a.cfg.SessionLifetime()
	if ttl <= 0 {
		ttl = 14 * 24 * time.Hour
	}
	rec := sessionRecord{
		User:    user,
		Created: time.Now(),
		Expires: time.Now().Add(ttl),
		Agent:   truncate(r.UserAgent(), 120),
		IP:      clientIP(r),
	}
	a.state.mu.Lock()
	a.state.data.Sessions[token] = rec
	a.state.touch()
	a.state.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.isTLS(),
		Expires:  rec.Expires,
		MaxAge:   int(ttl.Seconds()),
	})
}

func (a *App) destroySession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.state.mu.Lock()
		delete(a.state.data.Sessions, c.Value)
		a.state.touch()
		a.state.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.isTLS(),
		MaxAge:   -1,
	})
}

// identify ermittelt den Benutzer einer Anfrage.
func (a *App) identify(r *http.Request) (Identity, bool) {
	auth := a.cfg.AuthSettings()
	if !auth.Enabled {
		return Identity{Name: "lokal", Admin: true}, true
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.state.mu.RLock()
		rec, ok := a.state.data.Sessions[c.Value]
		a.state.mu.RUnlock()
		if ok && time.Now().Before(rec.Expires) {
			if u, found := a.cfg.FindUser(rec.User); found {
				return Identity{Name: u.Name, Admin: u.Admin, ReadOnly: u.ReadOnly}, true
			}
		}
	}
	// Vertrauensstellung für das eigene Netz, falls ausdrücklich erlaubt.
	if auth.LocalOnlyNoAuth && isPrivateAddr(clientIP(r)) {
		return Identity{Name: "lokal", Admin: false, ReadOnly: false}, true
	}
	return Identity{}, false
}

// checkLogin prüft Benutzername und Passwort.
func (a *App) checkLogin(name, pass string) (config.User, bool) {
	u, ok := a.cfg.FindUser(name)
	if !ok {
		// Konstante Laufzeit: sonst verrät die Antwortzeit, ob es den
		// Benutzernamen gibt.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidin"), []byte(pass))
		return config.User{}, false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(pass)) != nil {
		return config.User{}, false
	}
	return u, true
}

// isPrivateAddr erkennt Adressen aus dem lokalen Netz.
func isPrivateAddr(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// requireAuth schützt API-Endpunkte.
func (a *App) requireAuth(next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := a.identify(r)
		if !ok {
			failWith(w, http.StatusUnauthorized, "Nicht angemeldet.")
			return
		}
		next(w, r, id)
	}
}

// requireWrite schützt zusätzlich vor Schreibzugriffen im Nur-Lese-Modus.
func (a *App) requireWrite(next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request, id Identity) {
		if !a.checkCSRF(w, r) {
			return
		}
		if !id.CanWrite() {
			failWith(w, http.StatusForbidden, "Dieser Zugang darf nur lesen.")
			return
		}
		next(w, r, id)
	})
}

// requireAdmin schützt die Verwaltung.
func (a *App) requireAdmin(next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request, id Identity) {
		if r.Method != http.MethodGet && !a.checkCSRF(w, r) {
			return
		}
		if !id.Admin {
			failWith(w, http.StatusForbidden, "Nur für Administratoren.")
			return
		}
		next(w, r, id)
	})
}

// checkCSRF verlangt einen eigenen Header. Den kann ein fremdes Formular nicht
// setzen - zusammen mit SameSite=Lax reicht das gegen Cross-Site-Requests.
func (a *App) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-SpeedNAS") == "" {
		failWith(w, http.StatusForbidden, "Ungültige Anfrage (CSRF-Schutz).")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !a.originAllowed(origin, r) {
		failWith(w, http.StatusForbidden, "Herkunft der Anfrage nicht erlaubt.")
		return false
	}
	return true
}

func (a *App) originAllowed(origin string, r *http.Request) bool {
	host := r.Host
	for _, prefix := range []string{"http://", "https://"} {
		if strings.TrimPrefix(origin, prefix) == host {
			return true
		}
	}
	return false
}

// constantTimeEqual vergleicht Tokens ohne Laufzeitunterschied.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
