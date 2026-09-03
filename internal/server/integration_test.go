package server

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
)

// testServer baut eine vollständige Anwendung auf einem Wegwerf-Verzeichnis.
type testServer struct {
	*httptest.Server
	app    *App
	files  string
	cookie *http.Cookie
	t      *testing.T
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()
	files := filepath.Join(dir, "files")
	if err := os.MkdirAll(filepath.Join(files, "Unterordner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "notiz.txt"), []byte("Hallo Welt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "Unterordner", "tief.txt"), []byte("tief"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.SetPath(filepath.Join(dir, "config.json"))
	cfg.Server.DataDir = filepath.Join(dir, "data")
	cfg.Performance.ListCacheSeconds = 0
	if _, err := cfg.UpsertLocation(config.Location{
		ID: "test", Label: "Testordner", Type: "local", Local: &config.LocalConf{Path: files},
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("passwort123")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.UpsertUser(config.User{Name: "paul", Hash: hash, Admin: true}); err != nil {
		t.Fatal(err)
	}

	app, err := New(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })

	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &testServer{Server: srv, app: app, files: files, t: t}
}

func (s *testServer) do(method, path string, body io.Reader, hdr map[string]string) *http.Response {
	s.t.Helper()
	req, err := http.NewRequest(method, s.URL+path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("X-SpeedNAS", "1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if s.cookie != nil {
		req.AddCookie(s.cookie)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return res
}

func (s *testServer) json(method, path string, body any) (*http.Response, map[string]any) {
	s.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	res := s.do(method, path, r, map[string]string{"Content-Type": "application/json"})
	var out map[string]any
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	_ = json.Unmarshal(data, &out)
	return res, out
}

func (s *testServer) login() {
	s.t.Helper()
	res, _ := s.json(http.MethodPost, "/api/login", map[string]string{"user": "paul", "password": "passwort123"})
	if res.StatusCode != http.StatusOK {
		s.t.Fatalf("Anmeldung fehlgeschlagen: %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			s.cookie = c
		}
	}
	if s.cookie == nil {
		s.t.Fatal("kein Sitzungscookie erhalten")
	}
}

func TestIntegrationAnmeldungNoetig(t *testing.T) {
	s := newTestServer(t)
	res := s.do(http.MethodGet, "/api/list?loc=test", nil, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("ohne Anmeldung: %d, erwartet 401", res.StatusCode)
	}

	// Falsches Passwort darf keine Sitzung geben.
	res2, _ := s.json(http.MethodPost, "/api/login", map[string]string{"user": "paul", "password": "falsch"})
	if res2.StatusCode != http.StatusUnauthorized {
		t.Errorf("falsches Passwort: %d", res2.StatusCode)
	}
}

func TestIntegrationCSRFHeaderPflicht(t *testing.T) {
	s := newTestServer(t)
	s.login()
	body, _ := json.Marshal(map[string]string{"loc": "test", "path": "", "name": "neu"})
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/api/mkdir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(s.cookie) // aber kein X-SpeedNAS-Header
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("ohne CSRF-Header: %d, erwartet 403", res.StatusCode)
	}
}

func TestIntegrationFremdeHerkunftAbgelehnt(t *testing.T) {
	s := newTestServer(t)
	s.login()
	b, _ := json.Marshal(map[string]string{"loc": "test", "path": "", "name": "boese"})
	res := s.do(http.MethodPost, "/api/mkdir", bytes.NewReader(b),
		map[string]string{"Content-Type": "application/json", "Origin": "https://angreifer.example"})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("fremde Herkunft: %d, erwartet 403", res.StatusCode)
	}
}

func TestIntegrationDateiRundlauf(t *testing.T) {
	s := newTestServer(t)
	s.login()

	// Auflisten
	res, out := s.json(http.MethodGet, "/api/list?loc=test&path=", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", res.StatusCode)
	}
	entries, _ := out["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("%d Einträge, erwartet 2: %v", len(entries), out)
	}

	// Ordner anlegen
	res, _ = s.json(http.MethodPost, "/api/mkdir", map[string]string{"loc": "test", "path": "", "name": "Neuer Ordner"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mkdir: %d", res.StatusCode)
	}

	// Hochladen
	payload := make([]byte, 300_000)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	res = s.do(http.MethodPost, "/api/upload?loc=test&path=Neuer%20Ordner&name=daten.bin&mode=rename",
		bytes.NewReader(payload), nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d", res.StatusCode)
	}
	onDisk, err := os.ReadFile(filepath.Join(s.files, "Neuer Ordner", "daten.bin"))
	if err != nil || !bytes.Equal(onDisk, payload) {
		t.Fatalf("hochgeladene Datei stimmt nicht: %v", err)
	}

	// Herunterladen
	res = s.do(http.MethodGet, "/api/download?loc=test&path=Neuer%20Ordner/daten.bin", nil, nil)
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(got, payload) {
		t.Errorf("Download weicht ab (%d statt %d Bytes)", len(got), len(payload))
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "daten.bin") {
		t.Errorf("Content-Disposition: %q", cd)
	}

	// Teilbereich
	res = s.do(http.MethodGet, "/api/raw?loc=test&path=Neuer%20Ordner/daten.bin", nil,
		map[string]string{"Range": "bytes=1000-1099"})
	part, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent {
		t.Errorf("Range: %d, erwartet 206", res.StatusCode)
	}
	if !bytes.Equal(part, payload[1000:1100]) {
		t.Error("Teilbereich stimmt nicht")
	}
	if cr := res.Header.Get("Content-Range"); cr != fmt.Sprintf("bytes 1000-1099/%d", len(payload)) {
		t.Errorf("Content-Range: %q", cr)
	}

	// Umbenennen
	res, _ = s.json(http.MethodPost, "/api/rename", map[string]string{
		"loc": "test", "path": "Neuer Ordner/daten.bin", "name": "umbenannt.bin"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(s.files, "Neuer Ordner", "umbenannt.bin")); err != nil {
		t.Errorf("nach dem Umbenennen nicht gefunden: %v", err)
	}

	// Löschen
	res, _ = s.json(http.MethodPost, "/api/delete", map[string]any{
		"loc": "test", "items": []string{"Neuer Ordner"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(s.files, "Neuer Ordner")); !os.IsNotExist(err) {
		t.Error("Ordner wurde nicht gelöscht")
	}
}

func TestIntegrationZipStream(t *testing.T) {
	s := newTestServer(t)
	s.login()
	res := s.do(http.MethodGet, "/api/zip?loc=test&path=", nil, nil)
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("zip: %d", res.StatusCode)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("kein gültiges ZIP: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["notiz.txt"] || !names["Unterordner/tief.txt"] {
		t.Errorf("Inhalt unvollständig: %v", names)
	}
	// Inhalt stichprobenartig prüfen
	for _, f := range zr.File {
		if f.Name != "notiz.txt" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != "Hallo Welt" {
			t.Errorf("Dateiinhalt im ZIP: %q", b)
		}
	}
}

func TestIntegrationChunkUploadMitWiederaufnahme(t *testing.T) {
	s := newTestServer(t)
	s.login()

	payload := make([]byte, 5_000_000)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	res, out := s.json(http.MethodPost, "/api/upload/init", map[string]any{
		"loc": "test", "path": "", "name": "gross.bin", "size": len(payload), "mode": "overwrite"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("init: %d", res.StatusCode)
	}
	id, _ := out["uploadId"].(string)
	if id == "" {
		t.Fatal("keine Upload-Kennung")
	}

	const part = 2_000_000
	send := func(off int) {
		end := off + part
		if end > len(payload) {
			end = len(payload)
		}
		r := s.do(http.MethodPost, fmt.Sprintf("/api/upload/part?id=%s&offset=%d", id, off),
			bytes.NewReader(payload[off:end]), nil)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("part %d: %d", off, r.StatusCode)
		}
	}
	// Absichtlich in falscher Reihenfolge und mit Lücke.
	send(4_000_000)
	send(0)

	res, out = s.json(http.MethodPost, "/api/upload/finish", map[string]string{"id": id})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unvollständiger Abschluss: %d, erwartet 409", res.StatusCode)
	}
	missing, _ := out["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("Lücke nicht gemeldet: %v", out)
	}

	// Nach dem 409 muss der Zwischenstand erhalten bleiben.
	send(2_000_000)
	res, _ = s.json(http.MethodPost, "/api/upload/finish", map[string]string{"id": id})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("finish: %d", res.StatusCode)
	}
	onDisk, err := os.ReadFile(filepath.Join(s.files, "gross.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Errorf("zusammengesetzte Datei weicht ab (%d statt %d Bytes)", len(onDisk), len(payload))
	}
}

func TestIntegrationFreigabelink(t *testing.T) {
	s := newTestServer(t)
	s.login()

	res, out := s.json(http.MethodPost, "/api/shares", map[string]any{
		"loc": "test", "path": "Unterordner", "days": 1, "password": "linkpass"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("share: %d", res.StatusCode)
	}
	share, _ := out["share"].(map[string]any)
	token, _ := share["token"].(string)
	if token == "" {
		t.Fatal("kein Token")
	}

	// Ohne Passwort gesperrt - und zwar ohne Sitzungscookie.
	req, _ := http.NewRequest(http.MethodGet, s.URL+"/s/"+token+"/list", nil)
	req.Header.Set("X-SpeedNAS", "1")
	anon, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Errorf("ohne Passwort: %d, erwartet 401", anon.StatusCode)
	}

	// Entsperren
	body, _ := json.Marshal(map[string]string{"password": "linkpass"})
	req, _ = http.NewRequest(http.MethodPost, s.URL+"/s/"+token+"/unlock", bytes.NewReader(body))
	req.Header.Set("X-SpeedNAS", "1")
	req.Header.Set("Content-Type", "application/json")
	unlock, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	unlock.Body.Close()
	if unlock.StatusCode != http.StatusOK {
		t.Fatalf("unlock: %d", unlock.StatusCode)
	}
	var shareCookie *http.Cookie
	for _, c := range unlock.Cookies() {
		if strings.HasPrefix(c.Name, "snas_s_") {
			shareCookie = c
		}
	}
	if shareCookie == nil {
		t.Fatal("kein Freigabe-Cookie")
	}

	// Jetzt lesbar
	req, _ = http.NewRequest(http.MethodGet, s.URL+"/s/"+token+"/list", nil)
	req.Header.Set("X-SpeedNAS", "1")
	req.AddCookie(shareCookie)
	listed, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(listed.Body)
	listed.Body.Close()
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list nach unlock: %d (%s)", listed.StatusCode, data)
	}
	if !strings.Contains(string(data), "tief.txt") {
		t.Errorf("Inhalt fehlt: %s", data)
	}

	// Falsches Passwort bleibt gesperrt.
	body, _ = json.Marshal(map[string]string{"password": "falsch"})
	req, _ = http.NewRequest(http.MethodPost, s.URL+"/s/"+token+"/unlock", bytes.NewReader(body))
	req.Header.Set("X-SpeedNAS", "1")
	req.Header.Set("Content-Type", "application/json")
	bad, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Errorf("falsches Link-Passwort: %d", bad.StatusCode)
	}
}

func TestIntegrationNurLesenZugang(t *testing.T) {
	s := newTestServer(t)
	hash, _ := HashPassword("nurlesen123")
	if err := s.app.cfg.UpsertUser(config.User{Name: "gast", Hash: hash, ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	res, _ := s.json(http.MethodPost, "/api/login", map[string]string{"user": "gast", "password": "nurlesen123"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Anmeldung: %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			s.cookie = c
		}
	}
	// Lesen geht
	r, _ := s.json(http.MethodGet, "/api/list?loc=test&path=", nil)
	if r.StatusCode != http.StatusOK {
		t.Errorf("Lesen verweigert: %d", r.StatusCode)
	}
	// Schreiben nicht
	r, _ = s.json(http.MethodPost, "/api/mkdir", map[string]string{"loc": "test", "path": "", "name": "x"})
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("Schreiben erlaubt: %d, erwartet 403", r.StatusCode)
	}
	// Verwaltung erst recht nicht
	r, _ = s.json(http.MethodGet, "/api/admin/users", nil)
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("Verwaltung erlaubt: %d, erwartet 403", r.StatusCode)
	}
}

func TestIntegrationPfadAusbruch(t *testing.T) {
	s := newTestServer(t)
	s.login()
	// Alles muss innerhalb des konfigurierten Ordners bleiben.
	for _, p := range []string{"../", "../../etc", "..%2f..%2fetc", "/etc/passwd"} {
		res := s.do(http.MethodGet, "/api/list?loc=test&path="+p, nil, nil)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(body), "passwd") || strings.Contains(string(body), "\"hosts\"") {
			t.Errorf("Ausbruch mit %q möglich: %s", p, body)
		}
	}
}

func TestIntegrationStatischeDateienUndKopfzeilen(t *testing.T) {
	s := newTestServer(t)
	res := s.do(http.MethodGet, "/static/css/app.css", nil, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("CSS: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type: %q", ct)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP fehlt oder zu locker: %q", csp)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff fehlt")
	}
	// Nicht angemeldet: die Startseite leitet zur Anmeldung.
	res2 := s.do(http.MethodGet, "/", nil, nil)
	res2.Body.Close()
	if res2.StatusCode != http.StatusFound {
		t.Errorf("Startseite ohne Anmeldung: %d, erwartet 302", res2.StatusCode)
	}
}
