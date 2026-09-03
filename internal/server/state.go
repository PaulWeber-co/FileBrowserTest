package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Share ist ein öffentlicher Link auf eine Datei oder einen Ordner.
type Share struct {
	Token      string    `json:"token"`
	LocationID string    `json:"locationId"`
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	IsDir      bool      `json:"isDir"`
	Owner      string    `json:"owner"`
	Created    time.Time `json:"created"`
	Expires    time.Time `json:"expires,omitempty"`
	Hash       string    `json:"-"` // bcrypt, falls passwortgeschützt
	HasPass    bool      `json:"hasPassword"`
	AllowWrite bool      `json:"allowWrite"`
	Hits       int64     `json:"hits"`
}

// Expired meldet, ob der Link abgelaufen ist.
func (s *Share) Expired() bool {
	return !s.Expires.IsZero() && time.Now().After(s.Expires)
}

// Favorite ist ein Lesezeichen in der Seitenleiste.
type Favorite struct {
	LocationID string `json:"locationId"`
	Path       string `json:"path"`
	Name       string `json:"name"`
}

// Prefs sind die pro Benutzer gemerkten Oberflächeneinstellungen.
type Prefs struct {
	View    string `json:"view,omitempty"` // list | grid
	Sort    string `json:"sort,omitempty"` // name | size | mtime | type
	Desc    bool   `json:"desc,omitempty"`
	Theme   string `json:"theme,omitempty"` // auto | light | dark
	Hidden  bool   `json:"showHidden,omitempty"`
	Thumbs  *bool  `json:"thumbs,omitempty"`
	Density string `json:"density,omitempty"`
}

type userState struct {
	Favorites []Favorite `json:"favorites"`
	Prefs     Prefs      `json:"prefs"`
	Recent    []Favorite `json:"recent"`
}

type persisted struct {
	Secret   string                   `json:"secret"`
	Sessions map[string]sessionRecord `json:"sessions"`
	Shares   map[string]*Share        `json:"shares"`
	Users    map[string]*userState    `json:"users"`
	ShareKey map[string]string        `json:"shareKeys"` // Token -> bcrypt-Hash
}

// State hält alles, was zur Laufzeit entsteht und einen Neustart überleben
// soll: Sitzungen, Freigabelinks, Lesezeichen.
type State struct {
	path string

	mu   sync.RWMutex
	data persisted

	dirty   bool
	stopCh  chan struct{}
	stopped sync.Once
}

// NewState lädt den Zustand aus dem Datenverzeichnis.
func NewState(dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &State{
		path: filepath.Join(dir, "state.json"),
		data: persisted{
			Sessions: map[string]sessionRecord{},
			Shares:   map[string]*Share{},
			Users:    map[string]*userState{},
			ShareKey: map[string]string{},
		},
		stopCh: make(chan struct{}),
	}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.data)
		if s.data.Sessions == nil {
			s.data.Sessions = map[string]sessionRecord{}
		}
		if s.data.Shares == nil {
			s.data.Shares = map[string]*Share{}
		}
		if s.data.Users == nil {
			s.data.Users = map[string]*userState{}
		}
		if s.data.ShareKey == nil {
			s.data.ShareKey = map[string]string{}
		}
		for t, sh := range s.data.Shares {
			sh.Hash = s.data.ShareKey[t]
			sh.HasPass = sh.Hash != ""
		}
	}
	if s.data.Secret == "" {
		s.data.Secret = newToken()
		s.touch()
	}
	s.gc()
	go s.flusher()
	return s, nil
}

// flusher schreibt Aenderungen gebündelt - nicht bei jedem Klick.
func (s *State) flusher() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			d := s.dirty
			s.dirty = false
			s.mu.Unlock()
			if d {
				_ = s.Save()
			}
		case <-s.stopCh:
			return
		}
	}
}

// Close schreibt den Zustand ein letztes Mal.
func (s *State) Close() error {
	s.stopped.Do(func() { close(s.stopCh) })
	return s.Save()
}

func (s *State) touch() {
	s.dirty = true
}

// Save schreibt den Zustand atomar auf Platte.
func (s *State) Save() error {
	s.mu.RLock()
	keys := make(map[string]string, len(s.data.Shares))
	for t, sh := range s.data.Shares {
		if sh.Hash != "" {
			keys[t] = sh.Hash
		}
	}
	s.data.ShareKey = keys
	b, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Secret liefert den Serverschlüssel für signierte Cookies.
func (s *State) Secret() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Secret
}

// gc räumt abgelaufene Sitzungen und Links weg.
func (s *State) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.data.Sessions {
		if now.After(v.Expires) {
			delete(s.data.Sessions, k)
			s.touch()
		}
	}
	for k, v := range s.data.Shares {
		if v.Expired() {
			delete(s.data.Shares, k)
			s.touch()
		}
	}
}

// ------------------------------------------------------------- Shares -----

// PutShare speichert einen Freigabelink.
func (s *State) PutShare(sh *Share) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Shares[sh.Token] = sh
	s.touch()
}

// GetShare liefert einen Freigabelink, sofern er existiert und gültig ist.
func (s *State) GetShare(token string) (*Share, bool) {
	s.mu.RLock()
	sh, ok := s.data.Shares[token]
	s.mu.RUnlock()
	if !ok || sh.Expired() {
		return nil, false
	}
	return sh, true
}

// HitShare zählt einen Zugriff.
func (s *State) HitShare(token string) {
	s.mu.Lock()
	if sh, ok := s.data.Shares[token]; ok {
		sh.Hits++
		s.touch()
	}
	s.mu.Unlock()
}

// DeleteShare entfernt einen Link.
func (s *State) DeleteShare(token string) {
	s.mu.Lock()
	delete(s.data.Shares, token)
	s.touch()
	s.mu.Unlock()
}

// Shares listet die Links eines Benutzers (bzw. alle für Administratoren).
func (s *State) Shares(owner string, all bool) []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Share, 0, len(s.data.Shares))
	for _, sh := range s.data.Shares {
		if sh.Expired() {
			continue
		}
		if all || strings.EqualFold(sh.Owner, owner) {
			cp := *sh
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// DeleteSharesForLocation räumt Links auf, deren Speicherort verschwunden ist.
func (s *State) DeleteSharesForLocation(locID string) {
	s.mu.Lock()
	for t, sh := range s.data.Shares {
		if sh.LocationID == locID {
			delete(s.data.Shares, t)
			s.touch()
		}
	}
	s.mu.Unlock()
}

// ------------------------------------------------ Lesezeichen & Prefs -----

func (s *State) user(name string) *userState {
	u, ok := s.data.Users[name]
	if !ok {
		u = &userState{}
		s.data.Users[name] = u
	}
	return u
}

// Favorites liefert die Lesezeichen eines Benutzers.
func (s *State) Favorites(name string) []Favorite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.data.Users[name]
	if !ok {
		return nil
	}
	out := make([]Favorite, len(u.Favorites))
	copy(out, u.Favorites)
	return out
}

// AddFavorite legt ein Lesezeichen an (doppelte werden ignoriert).
func (s *State) AddFavorite(name string, f Favorite) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.user(name)
	for _, e := range u.Favorites {
		if e.LocationID == f.LocationID && e.Path == f.Path {
			return
		}
	}
	u.Favorites = append(u.Favorites, f)
	s.touch()
}

// RemoveFavorite entfernt ein Lesezeichen.
func (s *State) RemoveFavorite(name string, f Favorite) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.user(name)
	out := u.Favorites[:0]
	for _, e := range u.Favorites {
		if !(e.LocationID == f.LocationID && e.Path == f.Path) {
			out = append(out, e)
		}
	}
	u.Favorites = out
	s.touch()
}

// PushRecent merkt sich zuletzt besuchte Ordner (maximal zehn).
func (s *State) PushRecent(name string, f Favorite) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.user(name)
	out := make([]Favorite, 0, 10)
	out = append(out, f)
	for _, e := range u.Recent {
		if e.LocationID == f.LocationID && e.Path == f.Path {
			continue
		}
		out = append(out, e)
		if len(out) >= 10 {
			break
		}
	}
	u.Recent = out
	s.touch()
}

// Recent liefert die zuletzt besuchten Ordner.
func (s *State) Recent(name string) []Favorite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.data.Users[name]
	if !ok {
		return nil
	}
	out := make([]Favorite, len(u.Recent))
	copy(out, u.Recent)
	return out
}

// Prefs liefert die Oberflächeneinstellungen.
func (s *State) Prefs(name string) Prefs {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.data.Users[name]; ok {
		return u.Prefs
	}
	return Prefs{}
}

// SetPrefs speichert die Oberflächeneinstellungen.
func (s *State) SetPrefs(name string, p Prefs) {
	s.mu.Lock()
	s.user(name).Prefs = p
	s.touch()
	s.mu.Unlock()
}

// DropUser entfernt alle gespeicherten Daten eines Benutzers.
func (s *State) DropUser(name string) {
	s.mu.Lock()
	delete(s.data.Users, name)
	for k, v := range s.data.Sessions {
		if strings.EqualFold(v.User, name) {
			delete(s.data.Sessions, k)
		}
	}
	s.touch()
	s.mu.Unlock()
}
