package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/smbprobe"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

func (a *App) handleAdminLocations(w http.ResponseWriter, r *http.Request, id Identity) {
	locs := a.cfg.Snapshot()
	out := make([]config.Location, 0, len(locs))
	for _, l := range locs {
		out = append(out, l.Redacted())
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": out, "active": a.mgr.Active()})
}

func (a *App) handleAdminLocationSave(w http.ResponseWriter, r *http.Request, id Identity) {
	var l config.Location
	if err := decodeBody(w, r, &l, 1<<20); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	// Maskierte Passwörter aus der Anzeige nie zurückschreiben.
	unmaskLocation(&l)
	if err := l.Validate(); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := a.cfg.UpsertLocation(l)
	if err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mgr.Drop(saved.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "location": saved.Redacted()})
}

// unmaskLocation setzt Platzhalter zurück auf "leer", damit UpsertLocation das
// alte Passwort behält.
func unmaskLocation(l *config.Location) {
	const mask = "********"
	if l.SMB != nil && l.SMB.Password == mask {
		l.SMB.Password = ""
	}
	if l.FTP != nil && l.FTP.Password == mask {
		l.FTP.Password = ""
	}
	if l.SFTP != nil {
		if l.SFTP.Password == mask {
			l.SFTP.Password = ""
		}
		if l.SFTP.Passphrase == mask {
			l.SFTP.Passphrase = ""
		}
	}
	if l.WebDAV != nil && l.WebDAV.Password == mask {
		l.WebDAV.Password = ""
	}
}

func (a *App) handleAdminLocationDelete(w http.ResponseWriter, r *http.Request, id Identity) {
	locID := r.URL.Query().Get("id")
	if locID == "" {
		failWith(w, http.StatusBadRequest, "id fehlt.")
		return
	}
	a.mgr.Drop(locID)
	a.state.DeleteSharesForLocation(locID)
	if err := a.cfg.DeleteLocation(locID); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminTest baut testweise eine Verbindung auf und listet die Wurzel.
func (a *App) handleAdminTest(w http.ResponseWriter, r *http.Request, id Identity) {
	var l config.Location
	if err := decodeBody(w, r, &l, 1<<20); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	unmaskLocation(&l)
	if l.ID != "" {
		// Beim Bearbeiten kommen leere Passwortfelder an; die alten nehmen.
		if old, ok := a.mgr.Location(l.ID); ok {
			l = mergeForTest(old, l)
		}
	}
	if err := l.Validate(); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := a.mgr.Build(l)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": friendly(err)})
		return
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	start := time.Now()
	entries, err := c.List(ctx, "")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": friendly(err), "detail": err.Error()})
		return
	}
	res := map[string]any{
		"ok":      true,
		"entries": len(entries),
		"tookMs":  time.Since(start).Milliseconds(),
		"sample":  sampleNames(entries, 8),
	}
	if sp, err := c.Space(ctx, ""); err == nil && sp.Total > 0 {
		res["space"] = sp
		res["spaceText"] = fmt.Sprintf("%s frei von %s", humanBytes(sp.Free), humanBytes(sp.Total))
	}
	writeJSON(w, http.StatusOK, res)
}

func mergeForTest(old, upd config.Location) config.Location {
	if upd.SMB != nil && old.SMB != nil && upd.SMB.Password == "" {
		upd.SMB.Password = old.SMB.Password
	}
	if upd.FTP != nil && old.FTP != nil && upd.FTP.Password == "" {
		upd.FTP.Password = old.FTP.Password
	}
	if upd.SFTP != nil && old.SFTP != nil && upd.SFTP.Password == "" {
		upd.SFTP.Password = old.SFTP.Password
	}
	if upd.WebDAV != nil && old.WebDAV != nil && upd.WebDAV.Password == "" {
		upd.WebDAV.Password = old.WebDAV.Password
	}
	return upd
}

func sampleNames(entries []vfs.Entry, n int) []string {
	out := make([]string, 0, n)
	for _, e := range entries {
		if len(out) >= n {
			break
		}
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		out = append(out, name)
	}
	return out
}

type discoverRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Domain   string `json:"domain"`
}

// handleAdminDiscoverShares listet die Freigaben eines SMB-Servers, damit
// niemand den Namen raten muss.
func (a *App) handleAdminDiscoverShares(w http.ResponseWriter, r *http.Request, id Identity) {
	var req discoverRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Host == "" {
		failWith(w, http.StatusBadRequest, "Host fehlt.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	names, err := vfs.SMBListShares(ctx, vfs.SMBOptions{
		Host: req.Host, Port: req.Port, User: req.User,
		Password: req.Password, Domain: req.Domain,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": friendly(err)})
		return
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "shares": names})
}

type probeRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// handleAdminProbe fragt den Server direkt nach seinen Protokollversionen und
// leitet daraus Klartext-Hinweise ab.
func (a *App) handleAdminProbe(w http.ResponseWriter, r *http.Request, id Identity) {
	var req probeRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Host == "" {
		failWith(w, http.StatusBadRequest, "Host fehlt.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	res := smbprobe.Probe(ctx, req.Host, req.Port, 6*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"probe": res, "hints": probeHints(res)})
}

// probeHints übersetzt das Messergebnis in Handlungsempfehlungen.
func probeHints(p smbprobe.Result) []string {
	var out []string
	if !p.Reachable {
		return []string{
			"Der Server ist auf Port " + fmt.Sprint(p.Port) + " nicht erreichbar.",
			"Prüfe: Ist der Netzwerkspeicher im Router aktiviert? Stimmt die IP? Blockiert eine Firewall Port 445?",
		}
	}
	switch {
	case p.SMB2 && p.SMB1:
		out = append(out, "Der Server spricht "+p.DialectName+" und zusätzlich noch SMB1.",
			"SMB1 gilt als unsicher. Wenn der Router es erlaubt, schalte SMB1 ab - moderne Geräte brauchen es nicht mehr.")
	case p.SMB2:
		out = append(out, "Der Server spricht "+p.DialectName+". Das ist die gute Nachricht: iPhone und Windows kommen damit grundsätzlich klar.")
	case p.SMB1:
		out = append(out, "Der Server spricht ausschließlich SMB1 ("+p.SMB1Dialect+").",
			"Genau das ist der Grund, warum die iOS-Dateien-App und der Windows-Explorer nicht mehr verbinden: beide haben SMB1 entfernt.",
			"SpeedNAS kann SMB1 - stelle die Protokollversion des Speicherorts dafür ausdrücklich auf \"SMB 1\".",
			"Beachte dabei: SMB1 überträgt unverschlüsselt. Vertretbar ist das, weil SMB1 nur zwischen SpeedNAS und dem Router läuft und dein Netz nie verlässt - auch beim Zugriff von unterwegs. Hintergründe: docs/smb1.md.",
			"Besser wäre, im Router SMB2 zu aktivieren oder die Firmware zu aktualisieren - falls das Gerät das hergibt.")
	default:
		out = append(out, "Port ist offen, aber es kam keine gültige SMB-Antwort. Läuft dort wirklich eine Dateifreigabe?")
	}
	if p.SigningForce {
		out = append(out, "Der Server verlangt Signierung jedes Pakets. Das kostet auf schwacher Router-Hardware spürbar Durchsatz - falls abschaltbar, gern testen.")
	}
	if p.MaxReadSize > 0 {
		kb := p.MaxReadSize / 1024
		out = append(out, fmt.Sprintf("Größte Leseanfrage: %d KiB. Stelle die Blockgröße in den Leistungseinstellungen höchstens so groß ein.", kb))
	}
	if p.RTTms > 20 {
		out = append(out, fmt.Sprintf("Antwortzeit %.0f ms - das riecht nach VPN oder WLAN. Mehr parallele Leseanfragen bringen hier am meisten.", p.RTTms))
	}
	return out
}

type speedtestRequest struct {
	Loc   string `json:"loc"`
	Path  string `json:"path,omitempty"`
	MB    int    `json:"mb"`
	Write bool   `json:"write"`
}

// handleAdminSpeedtest misst den tatsächlichen Durchsatz - einmal seriell,
// einmal mit den eingestellten parallelen Leseanfragen.
func (a *App) handleAdminSpeedtest(w http.ResponseWriter, r *http.Request, id Identity) {
	var req speedtestRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	c, loc, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	if req.MB <= 0 {
		req.MB = 32
	}
	if req.MB > 512 {
		req.MB = 512
	}
	limit := int64(req.MB) << 20

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result := map[string]any{"location": loc.Label}

	path := vfs.Clean(req.Path)
	if path == "" {
		p, size, err := largestFile(ctx, c, "", 2)
		if err != nil || p == "" {
			result["readError"] = "Keine Testdatei gefunden. Bitte eine Datei auswählen oder den Schreibtest nutzen."
		} else {
			path = p
			if size < limit {
				limit = size
			}
		}
	}

	if path != "" {
		if e, err := c.Stat(ctx, path); err == nil && e.Size < limit {
			limit = e.Size
		}
		result["file"] = path
		result["bytes"] = limit

		if d, err := measureRead(ctx, c, path, limit, vfs.PrefetchOpts{Workers: 1, ChunkSize: 256 << 10}); err == nil {
			result["serialMBs"] = mbPerSec(limit, d)
			result["serialMs"] = d.Milliseconds()
		} else {
			result["readError"] = friendly(err)
		}
		opts := a.prefetchOpts()
		if d, err := measureRead(ctx, c, path, limit, opts); err == nil {
			result["parallelMBs"] = mbPerSec(limit, d)
			result["parallelMs"] = d.Milliseconds()
			result["workers"] = opts.Workers
			result["chunkKB"] = opts.ChunkSize >> 10
		}
	}

	if req.Write && !loc.ReadOnly {
		name := fmt.Sprintf(".speednas-speedtest-%d.tmp", time.Now().UnixNano())
		wl := int64(req.MB) << 20
		if wl > 128<<20 {
			wl = 128 << 20
		}
		start := time.Now()
		n, err := c.Write(ctx, name, io.LimitReader(rand.Reader, wl), wl)
		if err != nil {
			result["writeError"] = friendly(err)
		} else {
			result["writeMBs"] = mbPerSec(n, time.Since(start))
			result["writeBytes"] = n
		}
		_ = c.Remove(context.Background(), name, false)
	}

	ping, err := c.Ping(ctx)
	if err == nil {
		result["pingMs"] = float64(ping.Microseconds()) / 1000
	}
	result["hints"] = speedHints(result)
	writeJSON(w, http.StatusOK, result)
}

func speedHints(res map[string]any) []string {
	var out []string
	serial, okS := res["serialMBs"].(float64)
	par, okP := res["parallelMBs"].(float64)
	if okS && okP {
		switch {
		case par > serial*1.5:
			out = append(out, fmt.Sprintf("Paralleles Lesen bringt hier Faktor %.1f. Mehr Arbeiter können noch mehr bringen - probier 6 oder 8.", par/serial))
		case par < serial*0.9:
			out = append(out, "Parallel ist langsamer als seriell. Das spricht für eine überlastete Gegenstelle - reduziere die Arbeiter auf 2.")
		default:
			out = append(out, "Parallel und seriell liegen gleichauf: die Leitung, nicht die Latenz, ist hier der Engpass.")
		}
	}
	if okP && par < 3 {
		out = append(out, "Unter 3 MB/s ist typisch für USB 2.0 am Router mit langsamem Stick. Ein schnellerer USB-Stick oder eine SSD hilft mehr als jede Einstellung.")
	}
	if okP && par > 25 {
		out = append(out, "Über 25 MB/s - das ist für einen Router-USB-Speicher sehr ordentlich.")
	}
	return out
}

func mbPerSec(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return (float64(n) / (1 << 20)) / d.Seconds()
}

func measureRead(ctx context.Context, c *vfs.Client, path string, limit int64, opts vfs.PrefetchOpts) (time.Duration, error) {
	rc, _, err := c.StreamAt(ctx, path, 0, opts)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	start := time.Now()
	n, err := io.Copy(io.Discard, io.LimitReader(rc, limit))
	if err != nil && n == 0 {
		return 0, err
	}
	return time.Since(start), nil
}

// largestFile sucht eine möglichst große Datei für den Lesetest.
func largestFile(ctx context.Context, c *vfs.Client, base string, depth int) (string, int64, error) {
	var bestPath string
	var bestSize int64
	var walk func(p string, d int) error
	walk = func(p string, d int) error {
		entries, err := c.List(ctx, p)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir {
				if d > 0 && !isHidden(e.Name) {
					_ = walk(vfs.Join(p, e.Name), d-1)
				}
				continue
			}
			if e.Size > bestSize {
				bestSize, bestPath = e.Size, vfs.Join(p, e.Name)
			}
		}
		return nil
	}
	err := walk(base, depth)
	return bestPath, bestSize, err
}

func (a *App) handleAdminStatus(w http.ResponseWriter, r *http.Request, id Identity) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	thumbBytes, thumbFiles := a.thumbs.Size()

	pools := map[string]any{}
	for lid, idle := range a.mgr.Active() {
		pools[lid] = map[string]any{"idle": idle}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    Version,
		"go":         runtime.Version(),
		"os":         runtime.GOOS + "/" + runtime.GOARCH,
		"uptimeSec":  int64(time.Since(a.startedAt).Seconds()),
		"requests":   a.reqCount.Load(),
		"bytesOut":   a.bytesOut.Load(),
		"bytesIn":    a.bytesIn.Load(),
		"memMB":      mem.Alloc >> 20,
		"goroutines": runtime.NumGoroutine(),
		"thumbBytes": thumbBytes,
		"thumbFiles": thumbFiles,
		"ffmpeg":     a.thumbs.FFmpegAvailable(),
		"pools":      pools,
		"configPath": a.cfg.Path(),
		"dataDir":    a.cfg.Server.DataDir,
	})
}

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request, id Identity) {
	users := a.cfg.Users()
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{"name": u.Name, "admin": u.Admin, "readOnly": u.ReadOnly})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

type userSaveRequest struct {
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
	Admin    bool   `json:"admin"`
	ReadOnly bool   `json:"readOnly"`
}

func (a *App) handleAdminUserSave(w http.ResponseWriter, r *http.Request, id Identity) {
	var req userSaveRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		failWith(w, http.StatusBadRequest, "Benutzername fehlt.")
		return
	}
	_, exists := a.cfg.FindUser(name)
	if !exists && len(req.Password) < 8 {
		failWith(w, http.StatusBadRequest, "Passwort mindestens 8 Zeichen.")
		return
	}
	u := config.User{Name: name, Admin: req.Admin, ReadOnly: req.ReadOnly}
	if req.Password != "" {
		if len(req.Password) < 8 {
			failWith(w, http.StatusBadRequest, "Passwort mindestens 8 Zeichen.")
			return
		}
		h, err := HashPassword(req.Password)
		if err != nil {
			failWith(w, http.StatusInternalServerError, err.Error())
			return
		}
		u.Hash = h
	}
	if err := a.cfg.UpsertUser(u); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleAdminUserDelete(w http.ResponseWriter, r *http.Request, id Identity) {
	name := r.URL.Query().Get("name")
	if strings.EqualFold(name, id.Name) {
		failWith(w, http.StatusBadRequest, "Der eigene Zugang kann nicht gelöscht werden.")
		return
	}
	users := a.cfg.Users()
	admins := 0
	for _, u := range users {
		if u.Admin && !strings.EqualFold(u.Name, name) {
			admins++
		}
	}
	if admins == 0 {
		failWith(w, http.StatusBadRequest, "Es muss mindestens ein Administrator übrig bleiben.")
		return
	}
	if err := a.cfg.DeleteUser(name); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.state.DropUser(name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleAdminPerfGet(w http.ResponseWriter, r *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, a.cfg.Perf())
}

func (a *App) handleAdminPerfSet(w http.ResponseWriter, r *http.Request, id Identity) {
	var p config.PerfConfig
	if err := decodeBody(w, r, &p); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.cfg.SetPerformance(p); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Verbindungen neu aufbauen, damit geänderte Cache-Zeiten sofort greifen.
	a.mgr.Reload()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "perf": a.cfg.Perf()})
}

func (a *App) handleAdminCacheClear(w http.ResponseWriter, r *http.Request, id Identity) {
	if err := a.thumbs.Clear(); err != nil {
		failWith(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mgr.Reload()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
