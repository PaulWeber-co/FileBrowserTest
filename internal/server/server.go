// Package server enthält den HTTP-Server und die gesamte API von SpeedNAS.
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/thumb"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
	"github.com/PaulWeber-co/FileBrowserTest/web"
)

// Version wird beim Bauen gesetzt.
var Version = "dev"

// App buendelt alle Bausteine des Servers.
type App struct {
	cfg    *config.Config
	state  *State
	mgr    *Manager
	thumbs *thumb.Cache
	web    fs.FS

	uploads *uploadStore
	jobs    *JobManager

	startedAt time.Time
	reqCount  atomic.Int64
	bytesOut  atomic.Int64
	bytesIn   atomic.Int64

	tls bool
}

// New baut die Anwendung auf.
func New(cfg *config.Config, devWebDir string) (*App, error) {
	dataDir := cfg.Server.DataDir
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("Datenverzeichnis %s: %w", dataDir, err)
	}
	st, err := NewState(dataDir)
	if err != nil {
		return nil, err
	}
	perf := cfg.Perf()
	tc, err := thumb.New(filepath.Join(dataDir, "thumbs"), perf.ThumbCacheMB, perf.ThumbWorkers)
	if err != nil {
		return nil, err
	}
	up, err := newUploadStore(filepath.Join(dataDir, "uploads"))
	if err != nil {
		return nil, err
	}

	var webFS fs.FS = web.Assets()
	if devWebDir != "" {
		webFS = os.DirFS(devWebDir)
		log.Printf("Weboberfläche wird aus %s geladen (Entwicklungsmodus)", devWebDir)
	}

	a := &App{
		cfg:       cfg,
		state:     st,
		mgr:       NewManager(cfg),
		thumbs:    tc,
		web:       webFS,
		uploads:   up,
		jobs:      NewJobManager(),
		startedAt: time.Now(),
		tls:       cfg.Server.TLS.Enabled,
	}
	return a, nil
}

func (a *App) isTLS() bool { return a.tls }

// Close räumt auf.
func (a *App) Close() error {
	a.jobs.CancelAll()
	a.mgr.Close()
	a.uploads.Close()
	return a.state.Close()
}

// Handler baut den kompletten Routenbaum.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- Seiten -------------------------------------------------------
	mux.HandleFunc("GET /", a.handleIndex)
	mux.HandleFunc("GET /login", a.handleLoginPage)
	mux.HandleFunc("GET /s/{token}", a.handleSharePage)
	mux.HandleFunc("GET /s/{token}/dl", a.handleShareDownload)
	mux.HandleFunc("GET /s/{token}/zip", a.handleShareZip)
	mux.HandleFunc("GET /s/{token}/list", a.handleShareList)
	mux.HandleFunc("GET /s/{token}/thumb", a.handleShareThumb)
	mux.HandleFunc("POST /s/{token}/unlock", a.handleShareUnlock)

	// --- Anmeldung ----------------------------------------------------
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/me", a.handleMe)
	mux.HandleFunc("POST /api/setup", a.handleSetup)

	// --- Dateien ------------------------------------------------------
	mux.HandleFunc("GET /api/locations", a.requireAuth(a.handleLocations))
	mux.HandleFunc("GET /api/list", a.requireAuth(a.handleList))
	mux.HandleFunc("GET /api/stat", a.requireAuth(a.handleStat))
	mux.HandleFunc("GET /api/space", a.requireAuth(a.handleSpace))
	mux.HandleFunc("GET /api/raw", a.requireAuth(a.handleRaw))
	mux.HandleFunc("GET /api/download", a.requireAuth(a.handleDownload))
	mux.HandleFunc("GET /api/zip", a.requireAuth(a.handleZip))
	mux.HandleFunc("GET /api/thumb", a.requireAuth(a.handleThumb))
	mux.HandleFunc("GET /api/search", a.requireAuth(a.handleSearch))
	mux.HandleFunc("GET /api/text", a.requireAuth(a.handleTextRead))

	mux.HandleFunc("POST /api/mkdir", a.requireWrite(a.handleMkdir))
	mux.HandleFunc("POST /api/rename", a.requireWrite(a.handleRename))
	mux.HandleFunc("POST /api/delete", a.requireWrite(a.handleDelete))
	mux.HandleFunc("POST /api/transfer", a.requireWrite(a.handleTransfer))
	mux.HandleFunc("POST /api/text", a.requireWrite(a.handleTextWrite))

	// --- Hintergrundaufträge -----------------------------------------
	mux.HandleFunc("GET /api/jobs", a.requireAuth(a.handleJobs))
	mux.HandleFunc("GET /api/jobs/events", a.requireAuth(a.handleJobEvents))
	mux.HandleFunc("POST /api/jobs/cancel", a.requireWrite(a.handleJobCancel))

	// --- Hochladen ----------------------------------------------------
	mux.HandleFunc("POST /api/upload", a.requireWrite(a.handleUploadDirect))
	mux.HandleFunc("POST /api/upload/init", a.requireWrite(a.handleUploadInit))
	mux.HandleFunc("POST /api/upload/part", a.requireWrite(a.handleUploadPart))
	mux.HandleFunc("POST /api/upload/finish", a.requireWrite(a.handleUploadFinish))
	mux.HandleFunc("POST /api/upload/abort", a.requireWrite(a.handleUploadAbort))
	mux.HandleFunc("GET /api/upload/status", a.requireAuth(a.handleUploadStatus))

	// --- Persönliches ------------------------------------------------
	mux.HandleFunc("GET /api/favorites", a.requireAuth(a.handleFavorites))
	mux.HandleFunc("POST /api/favorites", a.requireWrite(a.handleFavoriteAdd))
	mux.HandleFunc("DELETE /api/favorites", a.requireWrite(a.handleFavoriteDel))
	mux.HandleFunc("GET /api/prefs", a.requireAuth(a.handlePrefsGet))
	mux.HandleFunc("POST /api/prefs", a.requireAuth(a.handlePrefsSet))

	// --- Freigabelinks ------------------------------------------------
	mux.HandleFunc("GET /api/shares", a.requireAuth(a.handleSharesList))
	mux.HandleFunc("POST /api/shares", a.requireWrite(a.handleShareCreate))
	mux.HandleFunc("DELETE /api/shares", a.requireWrite(a.handleShareDelete))

	// --- Verwaltung ---------------------------------------------------
	mux.HandleFunc("GET /api/admin/locations", a.requireAdmin(a.handleAdminLocations))
	mux.HandleFunc("POST /api/admin/locations", a.requireAdmin(a.handleAdminLocationSave))
	mux.HandleFunc("DELETE /api/admin/locations", a.requireAdmin(a.handleAdminLocationDelete))
	mux.HandleFunc("POST /api/admin/test", a.requireAdmin(a.handleAdminTest))
	mux.HandleFunc("POST /api/admin/shares/discover", a.requireAdmin(a.handleAdminDiscoverShares))
	mux.HandleFunc("POST /api/admin/probe", a.requireAdmin(a.handleAdminProbe))
	mux.HandleFunc("POST /api/admin/speedtest", a.requireAdmin(a.handleAdminSpeedtest))
	mux.HandleFunc("GET /api/admin/status", a.requireAdmin(a.handleAdminStatus))
	mux.HandleFunc("GET /api/admin/users", a.requireAdmin(a.handleAdminUsers))
	mux.HandleFunc("POST /api/admin/users", a.requireAdmin(a.handleAdminUserSave))
	mux.HandleFunc("DELETE /api/admin/users", a.requireAdmin(a.handleAdminUserDelete))
	mux.HandleFunc("GET /api/admin/perf", a.requireAdmin(a.handleAdminPerfGet))
	mux.HandleFunc("POST /api/admin/perf", a.requireAdmin(a.handleAdminPerfSet))
	mux.HandleFunc("POST /api/admin/cache/clear", a.requireAdmin(a.handleAdminCacheClear))

	// --- Statische Dateien --------------------------------------------
	mux.HandleFunc("GET /static/", a.handleStatic)
	mux.HandleFunc("GET /manifest.webmanifest", a.handleRootAsset)
	mux.HandleFunc("GET /sw.js", a.handleRootAsset)
	mux.HandleFunc("GET /favicon.ico", a.handleRootAsset)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": Version})
	})

	return a.middleware(mux)
}

// middleware zählt Anfragen, setzt Sicherheitskopfzeilen und protokolliert.
func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.reqCount.Add(1)
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		// Strikte CSP: keine fremden Skripte, keine Einbettung durch Dritte.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; "+
				"style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; "+
				"object-src 'self'; frame-src 'self'; base-uri 'none'; form-action 'self'")
		if a.isTLS() {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------- Seiten -----

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		a.notFound(w, r)
		return
	}
	if _, ok := a.identify(r); !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	a.servePage(w, r, "index.html")
}

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.identify(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	a.servePage(w, r, "login.html")
}

func (a *App) servePage(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(a.web, name)
	if err != nil {
		http.Error(w, "Seite nicht gefunden", http.StatusNotFound)
		return
	}
	body := strings.ReplaceAll(string(data), "{{VERSION}}", Version)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(body))
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		failWith(w, http.StatusNotFound, "Unbekannter Endpunkt.")
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	a.serveAsset(w, r, name)
}

func (a *App) handleRootAsset(w http.ResponseWriter, r *http.Request) {
	a.serveAsset(w, r, strings.TrimPrefix(r.URL.Path, "/"))
}

func (a *App) serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(a.web, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := thumb.MimeType(name)
	switch {
	case strings.HasSuffix(name, ".webmanifest"):
		ct = "application/manifest+json"
	case strings.HasSuffix(name, ".js"):
		ct = "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		ct = "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		ct = "image/svg+xml"
	}
	w.Header().Set("Content-Type", ct)
	// Die Dateinamen tragen keinen Inhaltshash, deshalb kurze Gültigkeit mit
	// Revalidierung statt langem Caching.
	w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	etag := fmt.Sprintf("W/\"%x-%d\"", len(data), len(Version))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// ---------------------------------------------------------- Start -----

// Serve startet den HTTP-Server und wartet auf ctx.
func (a *App) Serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.cfg.Server.Listen,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
		// Kein WriteTimeout: große Downloads über eine langsame Leitung
		// würden sonst mittendrin abgeschnitten.
		IdleTimeout: 120 * time.Second,
	}

	ln, err := net.Listen("tcp", a.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("Port %s belegt: %w", a.cfg.Server.Listen, err)
	}

	if a.cfg.Server.TLS.Enabled {
		cert, err := a.loadOrCreateCert()
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln = tls.NewListener(ln, srv.TLSConfig)
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	a.printBanner()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *App) printBanner() {
	scheme := "http"
	if a.cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	addr := a.cfg.Server.Listen
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = "8088"
	}
	log.Printf("SpeedNAS %s läuft", Version)
	if host == "" || host == "0.0.0.0" || host == "::" {
		log.Printf("  lokal:      %s://localhost:%s", scheme, port)
		for _, ip := range localIPs() {
			log.Printf("  im Netzwerk: %s://%s:%s", scheme, ip, port)
		}
	} else {
		log.Printf("  Adresse:    %s://%s", scheme, addr)
	}
	log.Printf("  Konfiguration: %s", a.cfg.Path())
}

func localIPs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			out = append(out, ipnet.IP.String())
		}
	}
	return out
}

// clientFor liefert Client und Ortsdefinition oder schreibt eine Fehlermeldung.
func (a *App) clientFor(w http.ResponseWriter, id string) (*vfs.Client, config.Location, bool) {
	c, loc, err := a.mgr.Get(id)
	if err != nil {
		failWith(w, http.StatusBadRequest, friendly(err))
		return nil, config.Location{}, false
	}
	return c, loc, true
}

// prefetchOpts liest die aktuellen Leistungsparameter.
func (a *App) prefetchOpts() vfs.PrefetchOpts {
	p := a.cfg.Perf()
	return vfs.PrefetchOpts{
		Workers:   p.PrefetchWorkers,
		ChunkSize: int64(p.PrefetchChunkKB) << 10,
	}
}
