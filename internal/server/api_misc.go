package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

type loginRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.checkCSRF(w, r) {
		return
	}
	var req loginRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	u, ok := a.checkLogin(strings.TrimSpace(req.User), req.Password)
	if !ok {
		// Bremse gegen Durchprobieren; im LAN fällt das nicht auf.
		time.Sleep(600 * time.Millisecond)
		failWith(w, http.StatusUnauthorized, "Benutzername oder Passwort stimmt nicht.")
		return
	}
	a.createSession(w, r, u.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"user": map[string]any{"name": u.Name, "admin": u.Admin, "readOnly": u.ReadOnly},
	})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.destroySession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	auth := a.cfg.AuthSettings()
	needSetup := auth.Enabled && len(a.cfg.Users()) == 0
	id, ok := a.identify(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": false, "needSetup": needSetup, "version": Version,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"needSetup":     needSetup,
		"version":       Version,
		"user": map[string]any{
			"name": id.Name, "admin": id.Admin, "readOnly": id.ReadOnly,
		},
		"prefs":   a.state.Prefs(id.Name),
		"hasFF":   a.thumbs.FFmpegAvailable(),
		"locales": "de",
	})
}

type setupRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// handleSetup legt beim allerersten Start den Administrator an. Danach ist der
// Endpunkt gesperrt.
func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !a.checkCSRF(w, r) {
		return
	}
	if len(a.cfg.Users()) > 0 {
		failWith(w, http.StatusConflict, "Es gibt bereits Benutzer.")
		return
	}
	var req setupRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.User)
	if name == "" || len(req.Password) < 8 {
		failWith(w, http.StatusBadRequest, "Benutzername nötig, Passwort mindestens 8 Zeichen.")
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.cfg.UpsertUser(config.User{Name: name, Hash: hash, Admin: true}); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.createSession(w, r, name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------ Lesezeichen -----

func (a *App) handleFavorites(w http.ResponseWriter, r *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, map[string]any{
		"favorites": a.state.Favorites(id.Name),
		"recent":    a.state.Recent(id.Name),
	})
}

func (a *App) handleFavoriteAdd(w http.ResponseWriter, r *http.Request, id Identity) {
	var f Favorite
	if err := decodeBody(w, r, &f); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Path = vfs.Clean(f.Path)
	if f.Name == "" {
		f.Name = vfs.Base(f.Path)
	}
	if f.Name == "" {
		f.Name = "Wurzel"
	}
	a.state.AddFavorite(id.Name, f)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "favorites": a.state.Favorites(id.Name)})
}

func (a *App) handleFavoriteDel(w http.ResponseWriter, r *http.Request, id Identity) {
	var f Favorite
	if err := decodeBody(w, r, &f); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Path = vfs.Clean(f.Path)
	a.state.RemoveFavorite(id.Name, f)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "favorites": a.state.Favorites(id.Name)})
}

func (a *App) handlePrefsGet(w http.ResponseWriter, r *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, a.state.Prefs(id.Name))
}

func (a *App) handlePrefsSet(w http.ResponseWriter, r *http.Request, id Identity) {
	if !a.checkCSRF(w, r) {
		return
	}
	var p Prefs
	if err := decodeBody(w, r, &p); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	a.state.SetPrefs(id.Name, p)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
