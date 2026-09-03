package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLegtVorgabeAn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	c, created, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("erste Ladung sollte die Datei anlegen")
	}
	if c.Server.Listen != ":8088" {
		t.Errorf("Standardport: %q", c.Server.Listen)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Die Datei enthält NAS-Passwörter - sie darf nicht für alle lesbar sein.
	if runtimeIsUnix() && st.Mode().Perm()&0o077 != 0 {
		t.Errorf("Rechte zu weit: %v", st.Mode().Perm())
	}

	c2, created2, err := Load(p)
	if err != nil || created2 {
		t.Fatalf("zweite Ladung: created=%v err=%v", created2, err)
	}
	if c2.Server.Listen != c.Server.Listen {
		t.Error("Konfiguration wurde nicht zurückgelesen")
	}
}

func runtimeIsUnix() bool { return os.PathSeparator == '/' }

func TestExpandEnv(t *testing.T) {
	t.Setenv("SPEEDNAS_TESTPASS", "geheim")
	if got := expandEnv("${SPEEDNAS_TESTPASS}"); got != "geheim" {
		t.Errorf("expandEnv = %q", got)
	}
	if got := expandEnv("vor ${SPEEDNAS_TESTPASS} nach"); got != "vor geheim nach" {
		t.Errorf("expandEnv = %q", got)
	}
	// Unbekannte Variablen bleiben stehen, statt still zu verschwinden.
	if got := expandEnv("${GIBTESNICHT_XYZ}"); got != "${GIBTESNICHT_XYZ}" {
		t.Errorf("expandEnv = %q", got)
	}
	if got := expandEnv("ohne Variablen"); got != "ohne Variablen" {
		t.Errorf("expandEnv = %q", got)
	}
}

func TestResolveNutztUmgebung(t *testing.T) {
	t.Setenv("NAS_PW", "s3cret")
	l := Location{Type: "smb", SMB: &SMBConf{Host: "192.168.2.1", Share: "USB", Password: "${NAS_PW}"}}
	r := l.Resolve()
	if r.SMB.Password != "s3cret" {
		t.Errorf("Passwort nicht aufgelöst: %q", r.SMB.Password)
	}
	// Das Original bleibt unverändert - sonst landet das Klartextpasswort
	// beim nächsten Speichern in der Datei.
	if l.SMB.Password != "${NAS_PW}" {
		t.Errorf("Original verändert: %q", l.SMB.Password)
	}
}

func TestRedactedEntferntPasswoerter(t *testing.T) {
	l := Location{
		Type:   "smb",
		SMB:    &SMBConf{Host: "h", Share: "s", Password: "geheim"},
		FTP:    &FTPConf{Password: "geheim"},
		SFTP:   &SFTPConf{Password: "geheim", Passphrase: "geheim"},
		WebDAV: &WebDAVConf{Password: "geheim"},
	}
	r := l.Redacted()
	for _, got := range []string{r.SMB.Password, r.FTP.Password, r.SFTP.Password, r.SFTP.Passphrase, r.WebDAV.Password} {
		if strings.Contains(got, "geheim") {
			t.Errorf("Passwort durchgereicht: %q", got)
		}
	}
	if l.SMB.Password != "geheim" {
		t.Error("Original wurde verändert")
	}
	// Ein leeres Passwort bleibt leer, damit die Oberfläche das erkennt.
	empty := Location{Type: "smb", SMB: &SMBConf{}}
	if empty.Redacted().SMB.Password != "" {
		t.Error("leeres Passwort wurde maskiert")
	}
}

func TestUpsertLocationBehaeltPasswort(t *testing.T) {
	dir := t.TempDir()
	c, _, err := Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.UpsertLocation(Location{
		Label: "NAS", Type: "smb",
		SMB: &SMBConf{Host: "192.168.2.1", Share: "USB", User: "paul", Password: "geheim"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bearbeiten ohne Passwortfeld darf das alte Passwort nicht löschen.
	upd := saved
	upd.SMB = &SMBConf{Host: "192.168.2.1", Share: "USB", User: "paula", Password: ""}
	again, err := c.UpsertLocation(upd)
	if err != nil {
		t.Fatal(err)
	}
	if again.SMB.Password != "geheim" {
		t.Errorf("Passwort ging verloren: %q", again.SMB.Password)
	}
	if again.SMB.User != "paula" {
		t.Errorf("Benutzer nicht übernommen: %q", again.SMB.User)
	}
	if len(c.Snapshot()) != 1 {
		t.Errorf("es sollte genau ein Ort sein, sind %d", len(c.Snapshot()))
	}
}

func TestValidate(t *testing.T) {
	bad := []Location{
		{Type: "smb", SMB: &SMBConf{Host: "h"}},    // Freigabe fehlt
		{Type: "smb"},                              // Block fehlt
		{Type: "ftp", FTP: &FTPConf{}},             // Host fehlt
		{Type: "sftp", SFTP: &SFTPConf{Host: "h"}}, // Benutzer fehlt
		{Type: "webdav", WebDAV: &WebDAVConf{}},    // URL fehlt
		{Type: "local", Local: &LocalConf{}},       // Pfad fehlt
		{Type: "unsinn"},                           // Typ unbekannt
	}
	for i, l := range bad {
		if err := l.Validate(); err == nil {
			t.Errorf("Fall %d hätte scheitern müssen", i)
		}
	}
	good := Location{Type: "smb", SMB: &SMBConf{Host: "h", Share: "s"}}
	if err := good.Validate(); err != nil {
		t.Errorf("gültige Definition abgelehnt: %v", err)
	}
}

func TestUserVerwaltung(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertUser(User{Name: "Paul", Hash: "h1", Admin: true}); err != nil {
		t.Fatal(err)
	}
	// Suche ist unabhängig von Groß-/Kleinschreibung.
	if u, ok := c.FindUser("paul"); !ok || !u.Admin {
		t.Errorf("FindUser: %+v %v", u, ok)
	}
	// Ohne neues Passwort bleibt der alte Hash stehen.
	if err := c.UpsertUser(User{Name: "paul", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	u, _ := c.FindUser("PAUL")
	if u.Hash != "h1" {
		t.Errorf("Hash überschrieben: %q", u.Hash)
	}
	if !u.ReadOnly {
		t.Error("ReadOnly nicht übernommen")
	}
	if err := c.DeleteUser("paul"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.FindUser("paul"); ok {
		t.Error("Benutzer nicht gelöscht")
	}
}

func TestPerformanceGrenzen(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetPerformance(PerfConfig{PrefetchWorkers: 999, PrefetchChunkKB: 1, UploadPartMB: 0}); err != nil {
		t.Fatal(err)
	}
	p := c.Perf()
	if p.PrefetchWorkers != 16 {
		t.Errorf("Arbeiter nicht begrenzt: %d", p.PrefetchWorkers)
	}
	if p.PrefetchChunkKB != 64 {
		t.Errorf("Blockgröße nicht angehoben: %d", p.PrefetchChunkKB)
	}
	if p.UploadPartMB < 1 {
		t.Errorf("Teilgröße nicht angehoben: %d", p.UploadPartMB)
	}
}

func TestAuthSettingsOhneHashes(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.UpsertUser(User{Name: "paul", Hash: "$2a$geheim"})
	if a := c.AuthSettings(); a.Users != nil {
		t.Error("Passwort-Hashes werden nach außen gereicht")
	}
}

func TestNewIDIstEindeutig(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := NewID()
		if seen[id] {
			t.Fatal("doppelte Kennung")
		}
		if strings.ContainsAny(id, "/\\. ") {
			t.Fatalf("Kennung enthält Sonderzeichen: %q", id)
		}
		seen[id] = true
	}
}
