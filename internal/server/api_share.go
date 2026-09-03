package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PaulWeber-co/FileBrowserTest/internal/thumb"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

type shareCreateRequest struct {
	Loc      string `json:"loc"`
	Path     string `json:"path"`
	Days     int    `json:"days"`
	Password string `json:"password,omitempty"`
}

func (a *App) handleShareCreate(w http.ResponseWriter, r *http.Request, id Identity) {
	var req shareCreateRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	c, _, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	p := vfs.Clean(req.Path)
	e, err := c.Stat(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	sh := &Share{
		Token:      newToken()[:22],
		LocationID: req.Loc,
		Path:       p,
		Name:       e.Name,
		IsDir:      e.IsDir,
		Owner:      id.Name,
		Created:    time.Now(),
	}
	if sh.Name == "" {
		sh.Name = "Freigabe"
	}
	if req.Days > 0 {
		sh.Expires = time.Now().AddDate(0, 0, req.Days)
	}
	if req.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			failWith(w, http.StatusInternalServerError, "Passwort konnte nicht gesichert werden.")
			return
		}
		sh.Hash = string(h)
		sh.HasPass = true
	}
	a.state.PutShare(sh)
	writeJSON(w, http.StatusOK, map[string]any{"share": sh, "url": a.shareURL(r, sh.Token)})
}

func (a *App) shareURL(r *http.Request, token string) string {
	if base := strings.TrimRight(a.cfg.Server.PublicURL, "/"); base != "" {
		return base + "/s/" + token
	}
	scheme := "http"
	if a.isTLS() || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/s/%s", scheme, r.Host, token)
}

func (a *App) handleSharesList(w http.ResponseWriter, r *http.Request, id Identity) {
	shares := a.state.Shares(id.Name, id.Admin)
	out := make([]map[string]any, 0, len(shares))
	for _, s := range shares {
		out = append(out, map[string]any{"share": s, "url": a.shareURL(r, s.Token)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}

type shareDeleteRequest struct {
	Token string `json:"token"`
}

func (a *App) handleShareDelete(w http.ResponseWriter, r *http.Request, id Identity) {
	var req shareDeleteRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	sh, ok := a.state.GetShare(req.Token)
	if !ok {
		failWith(w, http.StatusNotFound, "Link existiert nicht mehr.")
		return
	}
	if !id.Admin && !strings.EqualFold(sh.Owner, id.Name) {
		failWith(w, http.StatusForbidden, "Dieser Link gehört jemand anderem.")
		return
	}
	a.state.DeleteShare(req.Token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------- öffentlicher Teil -----

func (a *App) shareCookieName(token string) string { return "snas_s_" + token }

func (a *App) shareUnlockValue(token string) string {
	mac := hmac.New(sha256.New, []byte(a.state.Secret()))
	mac.Write([]byte("share-unlock:" + token))
	return hex.EncodeToString(mac.Sum(nil))
}

// shareAccess prüft Existenz, Ablauf und - falls gesetzt - das Passwort.
func (a *App) shareAccess(w http.ResponseWriter, r *http.Request) (*Share, bool) {
	token := r.PathValue("token")
	sh, ok := a.state.GetShare(token)
	if !ok {
		http.Error(w, "Dieser Link ist abgelaufen oder existiert nicht.", http.StatusNotFound)
		return nil, false
	}
	if !sh.HasPass {
		return sh, true
	}
	c, err := r.Cookie(a.shareCookieName(token))
	if err != nil || !constantTimeEqual(c.Value, a.shareUnlockValue(token)) {
		return sh, false
	}
	return sh, true
}

func (a *App) handleSharePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if _, ok := a.state.GetShare(token); !ok {
		http.Error(w, "Dieser Link ist abgelaufen oder existiert nicht.", http.StatusNotFound)
		return
	}
	a.servePage(w, r, "share.html")
}

func (a *App) handleShareUnlock(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	sh, ok := a.state.GetShare(token)
	if !ok {
		failWith(w, http.StatusNotFound, "Link existiert nicht mehr.")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if !sh.HasPass {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(sh.Hash), []byte(req.Password)) != nil {
		// Kleine Verzögerung gegen automatisiertes Durchprobieren.
		time.Sleep(400 * time.Millisecond)
		failWith(w, http.StatusUnauthorized, "Falsches Passwort.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     a.shareCookieName(token),
		Value:    a.shareUnlockValue(token),
		Path:     "/s/" + token,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.isTLS(),
		MaxAge:   12 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleShareList liefert den Inhalt einer freigegebenen Datei oder eines
// freigegebenen Ordners - immer nur unterhalb des freigegebenen Pfades.
func (a *App) handleShareList(w http.ResponseWriter, r *http.Request) {
	sh, unlocked := a.shareAccess(w, r)
	if sh == nil {
		return
	}
	if !unlocked {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "Passwort erforderlich.", "needPassword": true, "name": sh.Name,
		})
		return
	}
	c, _, err := a.mgr.Get(sh.LocationID)
	if err != nil {
		failWith(w, http.StatusBadGateway, friendly(err))
		return
	}
	sub := vfs.Clean(r.URL.Query().Get("path"))
	full, ok := shareResolve(sh, sub)
	if !ok {
		failWith(w, http.StatusForbidden, "Pfad außerhalb der Freigabe.")
		return
	}
	if !sh.IsDir {
		e, err := c.Stat(r.Context(), sh.Path)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name": sh.Name, "isDir": false, "path": "",
			"entries": a.toDTO([]vfs.Entry{e}, true),
		})
		return
	}
	entries, err := c.List(r.Context(), full)
	if err != nil {
		fail(w, err)
		return
	}
	vfs.SortEntries(entries, "name", false)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    sh.Name,
		"isDir":   true,
		"path":    sub,
		"crumbs":  crumbsFor(sub),
		"entries": a.toDTO(entries, false),
	})
}

// shareResolve verhindert das Ausbrechen aus dem freigegebenen Unterbaum.
func shareResolve(sh *Share, sub string) (string, bool) {
	sub = vfs.Clean(sub)
	if sub == "" {
		return sh.Path, true
	}
	if !sh.IsDir {
		return "", false
	}
	full := vfs.Join(sh.Path, sub)
	if full != sh.Path && !strings.HasPrefix(full, sh.Path+"/") && sh.Path != "" {
		return "", false
	}
	return full, true
}

func (a *App) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	sh, unlocked := a.shareAccess(w, r)
	if sh == nil {
		return
	}
	if !unlocked {
		http.Error(w, "Passwort erforderlich.", http.StatusUnauthorized)
		return
	}
	c, _, err := a.mgr.Get(sh.LocationID)
	if err != nil {
		http.Error(w, friendly(err), http.StatusBadGateway)
		return
	}
	full, ok := shareResolve(sh, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "Pfad außerhalb der Freigabe.", http.StatusForbidden)
		return
	}
	e, err := c.Stat(r.Context(), full)
	if err != nil {
		fail(w, err)
		return
	}
	if e.IsDir {
		http.Error(w, "Ordner bitte als ZIP laden.", http.StatusBadRequest)
		return
	}
	a.state.HitShare(sh.Token)
	a.streamEntry(w, r, c, full, e, queryBool(r, "dl"))
}

func (a *App) handleShareThumb(w http.ResponseWriter, r *http.Request) {
	sh, unlocked := a.shareAccess(w, r)
	if sh == nil {
		return
	}
	if !unlocked {
		http.Error(w, "Passwort erforderlich.", http.StatusUnauthorized)
		return
	}
	c, _, err := a.mgr.Get(sh.LocationID)
	if err != nil {
		http.Error(w, friendly(err), http.StatusBadGateway)
		return
	}
	full, ok := shareResolve(sh, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "Pfad außerhalb der Freigabe.", http.StatusForbidden)
		return
	}
	e, err := c.Stat(r.Context(), full)
	if err != nil {
		fail(w, err)
		return
	}
	key := thumb.Key(sh.LocationID, full, e.Size, e.ModTime, 320)
	data, err := a.thumbs.Build(r.Context(), key, e.Name, func() (io.ReadCloser, error) {
		rc, _, err := c.StreamAt(r.Context(), full, 0, a.prefetchOpts())
		return rc, err
	}, thumb.Options{MaxDim: 320})
	if err != nil {
		http.Error(w, "Keine Vorschau.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func (a *App) handleShareZip(w http.ResponseWriter, r *http.Request) {
	sh, unlocked := a.shareAccess(w, r)
	if sh == nil {
		return
	}
	if !unlocked {
		http.Error(w, "Passwort erforderlich.", http.StatusUnauthorized)
		return
	}
	if !sh.IsDir {
		http.Error(w, "Das ist keine Ordnerfreigabe.", http.StatusBadRequest)
		return
	}
	a.state.HitShare(sh.Token)
	// Der ZIP-Handler liest Ort und Pfad aus der Anfrage - hier setzen wir
	// beides auf die Freigabe, damit nichts anderes erreichbar ist.
	q := r.URL.Query()
	q.Set("loc", sh.LocationID)
	full, ok := shareResolve(sh, q.Get("path"))
	if !ok {
		http.Error(w, "Pfad außerhalb der Freigabe.", http.StatusForbidden)
		return
	}
	q.Set("path", full)
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q.Encode()
	a.handleZip(w, r2, Identity{Name: "share", ReadOnly: true, Guest: true})
}
