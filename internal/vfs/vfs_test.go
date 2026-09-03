package vfs

import (
	"testing"
	"time"
)

func TestClean(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"/", ""},
		{".", ""},
		{"a/b", "a/b"},
		{"/a/b/", "a/b"},
		{"a//b", "a/b"},
		{"a\\b", "a/b"},
		{"./a", "a"},
		// Ausbruchversuche muessen an der Wurzel enden, nicht darueber.
		{"../a", "a"},
		{"../../etc/passwd", "etc/passwd"},
		{"a/../../b", "b"},
		{"/../..", ""},
		{"a/./b/../c", "a/c"},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, erwartet %q", c.in, got, c.want)
		}
	}
}

func TestJoinDirBase(t *testing.T) {
	if got := Join("a", "b", "c"); got != "a/b/c" {
		t.Errorf("Join = %q", got)
	}
	if got := Join("a", "../../b"); got != "b" {
		t.Errorf("Join mit Ausbruch = %q, erwartet b", got)
	}
	if got := Dir("a/b/c"); got != "a/b" {
		t.Errorf("Dir = %q", got)
	}
	if got := Dir("a"); got != "" {
		t.Errorf("Dir(a) = %q, erwartet leer", got)
	}
	if got := Base("a/b/c.txt"); got != "c.txt" {
		t.Errorf("Base = %q", got)
	}
	if got := Base(""); got != "" {
		t.Errorf("Base(leer) = %q", got)
	}
}

func TestValidName(t *testing.T) {
	ok := []string{"datei.txt", "Ordner mit Leerzeichen", "ümläute.jpg", "a"}
	bad := []string{"", ".", "..", "a/b", "a\\b", "a\x00b", "zeilen\numbruch"}
	for _, n := range ok {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, erwartet true", n)
		}
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, erwartet false", n)
		}
	}
}

func TestSortEntriesFoldersFirst(t *testing.T) {
	e := []Entry{
		{Name: "zeta.txt"},
		{Name: "Bilder", IsDir: true},
		{Name: "alpha.txt"},
		{Name: "Archiv", IsDir: true},
	}
	SortEntries(e, "name", false)
	if !e[0].IsDir || !e[1].IsDir {
		t.Fatalf("Ordner stehen nicht vorn: %v", names(e))
	}
	if e[0].Name != "Archiv" || e[1].Name != "Bilder" {
		t.Errorf("Ordner falsch sortiert: %v", names(e))
	}
	if e[2].Name != "alpha.txt" || e[3].Name != "zeta.txt" {
		t.Errorf("Dateien falsch sortiert: %v", names(e))
	}
}

func TestSortNaturalNumbers(t *testing.T) {
	e := []Entry{{Name: "Bild10.jpg"}, {Name: "Bild2.jpg"}, {Name: "Bild1.jpg"}, {Name: "Bild20.jpg"}}
	SortEntries(e, "name", false)
	want := []string{"Bild1.jpg", "Bild2.jpg", "Bild10.jpg", "Bild20.jpg"}
	for i, w := range want {
		if e[i].Name != w {
			t.Fatalf("natuerliche Sortierung: %v, erwartet %v", names(e), want)
		}
	}
}

func TestSortBySizeAndTimeDescending(t *testing.T) {
	now := time.Now()
	e := []Entry{
		{Name: "klein", Size: 10, ModTime: now.Add(-time.Hour)},
		{Name: "gross", Size: 1000, ModTime: now.Add(-time.Minute)},
		{Name: "mittel", Size: 500, ModTime: now},
	}
	SortEntries(e, "size", true)
	if e[0].Name != "gross" || e[2].Name != "klein" {
		t.Errorf("nach Groesse absteigend: %v", names(e))
	}
	SortEntries(e, "mtime", false)
	if e[0].Name != "klein" || e[2].Name != "mittel" {
		t.Errorf("nach Datum aufsteigend: %v", names(e))
	}
}

func names(e []Entry) []string {
	out := make([]string, len(e))
	for i := range e {
		out[i] = e[i].Name
	}
	return out
}

func TestDialectCodeRoundTrip(t *testing.T) {
	cases := map[string]uint16{
		"": 0, "auto": 0, "2.0.2": 0x0202, "2.1": 0x0210,
		"3.0": 0x0300, "3.0.2": 0x0302, "3.1.1": 0x0311,
	}
	for in, want := range cases {
		if got := DialectCode(in); got != want {
			t.Errorf("DialectCode(%q) = 0x%04x, erwartet 0x%04x", in, got, want)
		}
	}
	if DialectName(0x0311) != "SMB 3.1.1" {
		t.Errorf("DialectName(0x0311) = %q", DialectName(0x0311))
	}
}

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"normal.txt":     "normal.txt",
		"a/b\\c:d*e":     "a_b_c_d_e",
		"":               "datei",
		"..":             "datei",
		"  Leerzeichen ": "Leerzeichen",
	}
	for in, want := range cases {
		if got := SafeFileName(in); got != want {
			t.Errorf("SafeFileName(%q) = %q, erwartet %q", in, got, want)
		}
	}
}
