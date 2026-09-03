package server

import (
	"strings"
	"testing"
)

func TestParseRange(t *testing.T) {
	const size = 1000
	cases := []struct {
		header             string
		start, end         int64
		partial, wantError bool
	}{
		{"", 0, 999, false, false},
		{"bytes=0-99", 0, 99, true, false},
		{"bytes=100-", 100, 999, true, false},
		{"bytes=-100", 900, 999, true, false},
		{"bytes=0-", 0, 999, true, false},
		{"bytes=999-999", 999, 999, true, false},
		// Ueber das Ende hinaus wird gekappt, nicht abgelehnt.
		{"bytes=500-99999", 500, 999, true, false},
		// Mehrfachbereiche liefern die ganze Datei - das ist erlaubt.
		{"bytes=0-10,20-30", 0, 999, false, false},
		{"quatsch", 0, 999, false, false},
		{"bytes=1000-", 0, 0, false, true},
		{"bytes=5000-6000", 0, 0, false, true},
		{"bytes=-", 0, 0, false, true},
		{"bytes=50-10", 0, 0, false, true},
	}
	for _, c := range cases {
		start, end, partial, err := parseRange(c.header, size)
		if c.wantError {
			if err == nil {
				t.Errorf("%q: Fehler erwartet", c.header)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unerwarteter Fehler %v", c.header, err)
			continue
		}
		if start != c.start || end != c.end || partial != c.partial {
			t.Errorf("%q: (%d,%d,%v), erwartet (%d,%d,%v)",
				c.header, start, end, partial, c.start, c.end, c.partial)
		}
	}
}

func TestParseRangeLeereDatei(t *testing.T) {
	start, end, partial, err := parseRange("bytes=0-10", 0)
	if err != nil || partial || start != 0 || end != -1 {
		t.Errorf("leere Datei: (%d,%d,%v,%v)", start, end, partial, err)
	}
}

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("attachment", "Urlaub 2024.jpg")
	if !strings.Contains(got, `filename="Urlaub 2024.jpg"`) {
		t.Errorf("ASCII-Name fehlt: %s", got)
	}
	// Umlaute und Emojis muessen prozentkodiert mitgeliefert werden.
	got = contentDisposition("attachment", "Grüße 😀.txt")
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Errorf("RFC-5987-Teil fehlt: %s", got)
	}
	if strings.Contains(got, "ü") {
		t.Errorf("Rohe Umlaute im ASCII-Teil: %s", got)
	}
	if !strings.Contains(got, "%C3%BC") {
		t.Errorf("Umlaut nicht kodiert: %s", got)
	}
	// Anfuehrungszeichen duerfen den Header nicht sprengen.
	got = contentDisposition("attachment", `bo"se\.txt`)
	if strings.Count(got, `"`) != 2 {
		t.Errorf("Anfuehrungszeichen nicht entschaerft: %s", got)
	}
}

func TestShareResolveVerhindertAusbruch(t *testing.T) {
	dir := &Share{Path: "Bilder", IsDir: true}
	cases := []struct {
		sub  string
		want string
		ok   bool
	}{
		{"", "Bilder", true},
		{"Urlaub", "Bilder/Urlaub", true},
		{"Urlaub/2024", "Bilder/Urlaub/2024", true},
		// Alles, was hinausfuehrt, wird vorher weggekuerzt und landet wieder
		// innerhalb der Freigabe.
		{"../Docs", "Bilder/Docs", true},
		{"../../etc/passwd", "Bilder/etc/passwd", true},
		{"/etc/passwd", "Bilder/etc/passwd", true},
	}
	for _, c := range cases {
		got, ok := shareResolve(dir, c.sub)
		if ok != c.ok || got != c.want {
			t.Errorf("shareResolve(%q) = (%q,%v), erwartet (%q,%v)", c.sub, got, ok, c.want, c.ok)
		}
		if ok && got != "Bilder" && !strings.HasPrefix(got, "Bilder/") {
			t.Errorf("shareResolve(%q) fuehrt aus der Freigabe: %q", c.sub, got)
		}
	}

	// Eine Dateifreigabe darf ueberhaupt keine Unterpfade zulassen.
	file := &Share{Path: "Bilder/urlaub.jpg", IsDir: false}
	if got, ok := shareResolve(file, ""); !ok || got != "Bilder/urlaub.jpg" {
		t.Errorf("Datei-Freigabe Wurzel: (%q,%v)", got, ok)
	}
	if _, ok := shareResolve(file, "andere.jpg"); ok {
		t.Error("Datei-Freigabe erlaubt Unterpfad")
	}
}

func TestSplitUploadTarget(t *testing.T) {
	cases := []struct {
		base, name string
		wantDir    string
		wantFile   string
		wantErr    bool
	}{
		{"", "datei.txt", "", "datei.txt", false},
		{"Ziel", "datei.txt", "Ziel", "datei.txt", false},
		{"Ziel", "Ordner/datei.txt", "Ziel/Ordner", "datei.txt", false},
		{"Ziel", "a/b/c/datei.txt", "Ziel/a/b/c", "datei.txt", false},
		// Ausbruchversuch: der Pfad wird gekuerzt, die Datei landet im Ziel.
		{"Ziel", "../../datei.txt", "Ziel", "datei.txt", false},
		{"Ziel", "../Nachbar/datei.txt", "Ziel/Nachbar", "datei.txt", false},
		{"", "", "", "", true},
		{"", "/", "", "", true},
	}
	for _, c := range cases {
		dir, file, err := splitUploadTarget(c.base, c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("splitUploadTarget(%q,%q): Fehler erwartet", c.base, c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitUploadTarget(%q,%q): %v", c.base, c.name, err)
			continue
		}
		if dir != c.wantDir || file != c.wantFile {
			t.Errorf("splitUploadTarget(%q,%q) = (%q,%q), erwartet (%q,%q)",
				c.base, c.name, dir, file, c.wantDir, c.wantFile)
		}
	}
}

func TestUploadMissingRanges(t *testing.T) {
	u := &upload{Size: 1000, received: map[int64]int64{}}
	if got := u.missing(); len(got) != 1 || got[0] != [2]int64{0, 1000} {
		t.Fatalf("leerer Upload: %v", got)
	}
	u.markReceived(0, 300)
	u.markReceived(600, 400)
	got := u.missing()
	if len(got) != 1 || got[0] != [2]int64{300, 300} {
		t.Fatalf("Luecke falsch berechnet: %v", got)
	}
	u.markReceived(300, 300)
	if got := u.missing(); len(got) != 0 {
		t.Fatalf("vollstaendig, aber Luecken gemeldet: %v", got)
	}
	// Doppelt gesendete Teile duerfen den Zaehler nicht verfaelschen.
	u.markReceived(0, 300)
	if got, _ := u.progress(); got != 1000 {
		t.Errorf("Fortschritt nach Doppelsendung: %d", got)
	}
}

func TestUploadMissingMitLueckeAmEnde(t *testing.T) {
	u := &upload{Size: 1000, received: map[int64]int64{}}
	u.markReceived(0, 500)
	got := u.missing()
	if len(got) != 1 || got[0] != [2]int64{500, 500} {
		t.Errorf("Rest am Ende: %v", got)
	}
}

func TestIsHidden(t *testing.T) {
	hidden := []string{".ssh", ".DS_Store", "System Volume Information", "$RECYCLE.BIN", "Thumbs.db"}
	shown := []string{"Bilder", "urlaub.jpg", "Meine Datei.txt"}
	for _, n := range hidden {
		if !isHidden(n) {
			t.Errorf("%q sollte versteckt sein", n)
		}
	}
	for _, n := range shown {
		if isHidden(n) {
			t.Errorf("%q sollte sichtbar sein", n)
		}
	}
}

func TestCrumbsFor(t *testing.T) {
	c := crumbsFor("a/b/c")
	if len(c) != 3 || c[0].Path != "a" || c[1].Path != "a/b" || c[2].Path != "a/b/c" {
		t.Errorf("Brotkrumen falsch: %+v", c)
	}
	if len(crumbsFor("")) != 0 {
		t.Error("Wurzel sollte keine Brotkrumen liefern")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1024: "1.0 KB", 1536: "1.5 KB", 1 << 20: "1.0 MB", 1 << 30: "1.0 GB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, erwartet %q", in, got, want)
		}
	}
}
