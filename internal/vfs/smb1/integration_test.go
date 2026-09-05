package smb1

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Diese Tests sprechen mit einem echten SMB1-Server. Ohne konfigurierten
// Server werden sie übersprungen, damit "go test ./..." überall durchläuft.
//
// Testserver aufsetzen (Samba, nur SMB1) und dann:
//
//	SMB1_TEST_HOST=127.0.0.1 SMB1_TEST_PORT=4451 \
//	SMB1_TEST_SHARE=USB-Speicher SMB1_TEST_USER=nasuser \
//	SMB1_TEST_PASS=nasPass123 go test ./internal/vfs/smb1/ -v

func testOptions(t *testing.T) Options {
	t.Helper()
	host := os.Getenv("SMB1_TEST_HOST")
	if host == "" {
		t.Skip("SMB1_TEST_HOST nicht gesetzt - Integrationstest übersprungen")
	}
	port := 445
	if p := os.Getenv("SMB1_TEST_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("SMB1_TEST_PORT: %v", err)
		}
		port = n
	}
	return Options{
		Host:        host,
		Port:        port,
		Share:       os.Getenv("SMB1_TEST_SHARE"),
		User:        os.Getenv("SMB1_TEST_USER"),
		Password:    os.Getenv("SMB1_TEST_PASS"),
		DialTimeout: 10 * time.Second,
		IOTimeout:   20 * time.Second,
	}
}

func dial(t *testing.T) (*Client, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	c, err := Dial(ctx, testOptions(t))
	if err != nil {
		t.Fatalf("Verbindung fehlgeschlagen: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, ctx
}

func TestVerbindungMitBenutzer(t *testing.T) {
	c, ctx := dial(t)
	if c.uid == 0 {
		t.Error("keine Sitzungskennung erhalten")
	}
	if err := c.Echo(ctx); err != nil {
		t.Errorf("Echo: %v", err)
	}
	t.Logf("angemeldet (uid=%d, tid=%d, maxBuffer=%d, unicode=%v)", c.uid, c.tid, c.maxBuffer, c.unicode)
}

func TestVerbindungAlsGast(t *testing.T) {
	o := testOptions(t)
	o.User = ""
	o.Password = ""
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, o)
	if err != nil {
		t.Skipf("Gastzugang nicht möglich (das ist je nach Server normal): %v", err)
	}
	defer c.Close()
	if err := c.Echo(ctx); err != nil {
		t.Errorf("Echo als Gast: %v", err)
	}
}

func TestFalschesPasswortWirdAbgelehnt(t *testing.T) {
	o := testOptions(t)
	if o.User == "" {
		t.Skip("kein Benutzer konfiguriert")
	}
	o.Password = "ganz-sicher-falsch"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, o)
	if err == nil {
		c.Close()
		t.Fatal("falsches Passwort wurde akzeptiert")
	}
	if !errors.Is(err, ErrAuth) && !strings.Contains(err.Error(), "Anmeldung") {
		t.Logf("Fehler (akzeptabel, aber unspezifisch): %v", err)
	}
}

func TestUnbekannteFreigabe(t *testing.T) {
	o := testOptions(t)
	o.Share = "GibtEsGarantiertNicht"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, o)
	if err == nil {
		c.Close()
		t.Fatal("unbekannte Freigabe wurde akzeptiert")
	}
	t.Logf("erwarteter Fehler: %v", err)
}

func TestVerzeichnisListen(t *testing.T) {
	c, ctx := dial(t)
	entries, err := c.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	gefunden := map[string]FileInfo{}
	for _, e := range entries {
		gefunden[e.Name] = e
	}
	t.Logf("%d Einträge: %v", len(entries), namen(entries))

	for _, muss := range []string{"liesmich.txt", "video.bin", "Fotos", "Leer"} {
		if _, ok := gefunden[muss]; !ok {
			t.Errorf("%q fehlt in der Auflistung", muss)
		}
	}
	// Namen mit Leerzeichen und Umlauten müssen unversehrt ankommen.
	if _, ok := gefunden["Datei mit Leerzeichen.txt"]; !ok {
		t.Error("Name mit Leerzeichen fehlt")
	}
	if _, ok := gefunden["Grüße Übung.txt"]; !ok {
		t.Errorf("Name mit Umlauten fehlt - Unicode falsch dekodiert? Gefunden: %v", namen(entries))
	}
	if e := gefunden["Fotos"]; !e.IsDir {
		t.Error("Fotos wird nicht als Verzeichnis gemeldet")
	}
	if e := gefunden["video.bin"]; e.IsDir {
		t.Error("video.bin wird als Verzeichnis gemeldet")
	}
	if e := gefunden["video.bin"]; e.Size != 3000000 {
		t.Errorf("Größe von video.bin: %d, erwartet 3000000", e.Size)
	}
	if e := gefunden["video.bin"]; e.ModTime.IsZero() {
		t.Error("kein Änderungszeitpunkt geliefert")
	}
	// "." und ".." dürfen nicht auftauchen.
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			t.Errorf("%q sollte herausgefiltert sein", e.Name)
		}
	}
}

func TestUnterverzeichnisListen(t *testing.T) {
	c, ctx := dial(t)
	entries, err := c.List(ctx, "Fotos")
	if err != nil {
		t.Fatalf("List(Fotos): %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "klein.bin" {
		t.Errorf("Fotos enthält %v, erwartet [klein.bin]", namen(entries))
	}
	leer, err := c.List(ctx, "Leer")
	if err != nil {
		t.Fatalf("List(Leer): %v", err)
	}
	if len(leer) != 0 {
		t.Errorf("leeres Verzeichnis liefert %v", namen(leer))
	}
}

func TestStat(t *testing.T) {
	c, ctx := dial(t)

	fi, err := c.Stat(ctx, "video.bin")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size != 3000000 || fi.IsDir {
		t.Errorf("Stat(video.bin) = %+v", fi)
	}

	d, err := c.Stat(ctx, "Fotos")
	if err != nil {
		t.Fatalf("Stat(Fotos): %v", err)
	}
	if !d.IsDir {
		t.Error("Fotos nicht als Verzeichnis erkannt")
	}

	if _, err := c.Stat(ctx, "gibt-es-nicht.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat auf Fehlendes: %v, erwartet ErrNotFound", err)
	}

	// Auch die Wurzel muss beantwortbar sein.
	r, err := c.Stat(ctx, "")
	if err != nil || !r.IsDir {
		t.Errorf("Stat(Wurzel) = %+v, %v", r, err)
	}
}

func TestLesen(t *testing.T) {
	c, ctx := dial(t)

	f, err := c.Open(ctx, "liesmich.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close(ctx)

	if f.Size() != 28 {
		t.Errorf("gemeldete Größe %d, erwartet 28", f.Size())
	}
	buf := make([]byte, f.Size())
	n, err := f.ReadAt(ctx, buf, 0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if got := string(buf[:n]); got != "Datei auf der SMB1-Freigabe\n" {
		t.Errorf("Inhalt = %q", got)
	}
}

func TestGrosseDateiLesenUndTeilbereiche(t *testing.T) {
	c, ctx := dial(t)

	f, err := c.Open(ctx, "video.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close(ctx)

	if f.Size() != 3000000 {
		t.Fatalf("Größe %d, erwartet 3000000", f.Size())
	}

	// Vollständig lesen und mit dem Original vergleichen.
	buf := make([]byte, f.Size())
	if _, err := f.ReadAt(ctx, buf, 0); err != nil && err != io.EOF {
		t.Fatalf("vollständiges Lesen: %v", err)
	}
	orig, err := os.ReadFile(os.Getenv("SMB1_TEST_LOCALDIR") + "/video.bin")
	if err != nil {
		t.Skipf("Vergleichsdatei nicht lesbar: %v", err)
	}
	if !bytes.Equal(buf, orig) {
		t.Fatalf("Inhalt weicht ab (%d gelesen, %d im Original)", len(buf), len(orig))
	}

	// Ein Bereich mitten in der Datei - das braucht die Videowiedergabe.
	teil := make([]byte, 1000)
	if _, err := f.ReadAt(ctx, teil, 1_500_000); err != nil && err != io.EOF {
		t.Fatalf("Teilbereich: %v", err)
	}
	if !bytes.Equal(teil, orig[1_500_000:1_501_000]) {
		t.Error("Teilbereich stimmt nicht")
	}

	// Bereich über mehrere Anfragen hinweg (größer als maxRead).
	gross := make([]byte, 200_000)
	if _, err := f.ReadAt(ctx, gross, 100_000); err != nil && err != io.EOF {
		t.Fatalf("großer Bereich: %v", err)
	}
	if !bytes.Equal(gross, orig[100_000:300_000]) {
		t.Error("mehrteiliger Bereich stimmt nicht")
	}
}

// TestGleichzeitigeZugriffe prüft, dass ein Client mehrere Goroutinen
// gleichzeitig verträgt. SMB1 hat pro Verbindung immer nur eine Anfrage
// unterwegs; die Sperre im Client serialisiert sie. Ohne sie würden sich
// zwei Goroutinen ihre Antworten gegenseitig wegschnappen - genau das macht
// der Vorauslese-Leser, der eine Datei mit mehreren ReadAt parallel holt.
// Mit "go test -race" fällt hier jede fehlende Sperre auf.
func TestGleichzeitigeZugriffe(t *testing.T) {
	c, ctx := dial(t)

	orig, err := os.ReadFile(os.Getenv("SMB1_TEST_LOCALDIR") + "/video.bin")
	if err != nil {
		t.Skipf("Vergleichsdatei nicht lesbar: %v", err)
	}

	f, err := c.Open(ctx, "video.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close(ctx)

	const teile = 8
	const laenge = 50_000

	var wg sync.WaitGroup
	fehler := make(chan error, teile+2)

	// Acht Leser holen sich verschiedene Bereiche derselben Datei.
	for i := 0; i < teile; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			off := int64(i) * laenge
			buf := make([]byte, laenge)
			if _, err := f.ReadAt(ctx, buf, off); err != nil && err != io.EOF {
				fehler <- err
				return
			}
			if !bytes.Equal(buf, orig[off:off+laenge]) {
				fehler <- errors.New("Bereich " + strconv.Itoa(i) + " weicht ab")
			}
		}(i)
	}

	// Parallel dazu Verzeichnis und Metadaten - andere Befehlstypen auf
	// derselben Verbindung.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if _, err := c.List(ctx, ""); err != nil {
				fehler <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if _, err := c.Stat(ctx, "video.bin"); err != nil {
				fehler <- err
				return
			}
		}
	}()

	wg.Wait()
	close(fehler)
	for err := range fehler {
		t.Error(err)
	}
}

func TestSchreibenUndLoeschen(t *testing.T) {
	c, ctx := dial(t)
	name := "speednas-test-schreiben.bin"

	nutzdaten := make([]byte, 250_000)
	if _, err := rand.Read(nutzdaten); err != nil {
		t.Fatal(err)
	}

	f, err := c.Create(ctx, name)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	n, err := f.WriteAt(ctx, nutzdaten, 0)
	if err != nil {
		f.Close(ctx)
		t.Fatalf("WriteAt: %v (nach %d Bytes)", err, n)
	}
	if n != len(nutzdaten) {
		f.Close(ctx)
		t.Fatalf("%d Bytes geschrieben, erwartet %d", n, len(nutzdaten))
	}
	if err := f.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Zurücklesen und vergleichen.
	rf, err := c.Open(ctx, name)
	if err != nil {
		t.Fatalf("erneutes Öffnen: %v", err)
	}
	if rf.Size() != int64(len(nutzdaten)) {
		rf.Close(ctx)
		t.Fatalf("Größe nach dem Schreiben: %d", rf.Size())
	}
	zurueck := make([]byte, len(nutzdaten))
	if _, err := rf.ReadAt(ctx, zurueck, 0); err != nil && err != io.EOF {
		rf.Close(ctx)
		t.Fatalf("Zurücklesen: %v", err)
	}
	rf.Close(ctx)
	if !bytes.Equal(zurueck, nutzdaten) {
		t.Error("zurückgelesener Inhalt weicht ab")
	}

	if err := c.Remove(ctx, name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := c.Stat(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Errorf("Datei nach dem Löschen noch da (%v)", err)
	}
}

func TestVerzeichnisAnlegenUmbenennenLoeschen(t *testing.T) {
	c, ctx := dial(t)
	dir := "speednas-test-ordner"
	neu := "speednas-test-ordner-umbenannt"

	_ = c.RemoveDir(ctx, dir)
	_ = c.RemoveDir(ctx, neu)

	if err := c.Mkdir(ctx, dir); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	fi, err := c.Stat(ctx, dir)
	if err != nil || !fi.IsDir {
		t.Fatalf("angelegtes Verzeichnis: %+v, %v", fi, err)
	}

	// Ein zweites Mal muss scheitern.
	if err := c.Mkdir(ctx, dir); !errors.Is(err, ErrExists) {
		t.Errorf("doppeltes Mkdir: %v, erwartet ErrExists", err)
	}

	if err := c.Rename(ctx, dir, neu); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := c.Stat(ctx, dir); !errors.Is(err, ErrNotFound) {
		t.Error("alter Name existiert noch")
	}
	if _, err := c.Stat(ctx, neu); err != nil {
		t.Errorf("neuer Name fehlt: %v", err)
	}

	if err := c.RemoveDir(ctx, neu); err != nil {
		t.Fatalf("RemoveDir: %v", err)
	}
	if _, err := c.Stat(ctx, neu); !errors.Is(err, ErrNotFound) {
		t.Error("Verzeichnis nach dem Löschen noch da")
	}
}

func TestUmlauteImDateinamen(t *testing.T) {
	c, ctx := dial(t)
	name := "Größenprüfung Ärger.txt"

	_ = c.Remove(ctx, name)
	f, err := c.Create(ctx, name)
	if err != nil {
		t.Fatalf("Create mit Umlauten: %v", err)
	}
	if _, err := f.WriteAt(ctx, []byte("Öl"), 0); err != nil {
		f.Close(ctx)
		t.Fatalf("WriteAt: %v", err)
	}
	f.Close(ctx)

	entries, err := c.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, e := range entries {
		if e.Name == name {
			gefunden = true
		}
	}
	if !gefunden {
		t.Errorf("Datei mit Umlauten nicht wiedergefunden. Vorhanden: %v", namen(entries))
	}
	if err := c.Remove(ctx, name); err != nil {
		t.Errorf("Remove mit Umlauten: %v", err)
	}
}

func TestSpeicherplatz(t *testing.T) {
	c, ctx := dial(t)
	total, free, err := c.Space(ctx)
	if err != nil {
		t.Fatalf("Space: %v", err)
	}
	if total <= 0 || free < 0 || free > total {
		t.Errorf("unplausible Werte: total=%d free=%d", total, free)
	}
	t.Logf("Freigabe: %d MB frei von %d MB", free>>20, total>>20)
}

func namen(e []FileInfo) []string {
	out := make([]string, len(e))
	for i := range e {
		out[i] = e[i].Name
	}
	return out
}
