package thumb

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"testing"
	"time"
)

func TestKindOf(t *testing.T) {
	cases := map[string]Kind{
		"foto.JPG": KindImage, "clip.mp4": KindVideo, "lied.flac": KindAudio,
		"brief.pdf": KindPDF, "notiz.txt": KindText, "main.go": KindCode,
		"archiv.zip": KindArchive, "bericht.docx": KindDoc, "IMG_0001.HEIC": KindHEIC,
		"ohne-endung": KindOther, "seltsam.xyz": KindOther,
	}
	for name, want := range cases {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %q, erwartet %q", name, got, want)
		}
	}
}

func TestInlineSafe(t *testing.T) {
	// HTML und SVG duerfen nie inline ausgeliefert werden: sie koennten
	// Skripte im Ursprung der Anwendung ausfuehren.
	unsafe := []string{"seite.html", "bild.svg", "daten.xml", "programm.exe", "setup.msi"}
	for _, n := range unsafe {
		if InlineSafe(n) {
			t.Errorf("%q wird inline ausgeliefert", n)
		}
	}
	safe := []string{"foto.jpg", "clip.mp4", "brief.pdf", "notiz.txt", "main.go"}
	for _, n := range safe {
		if !InlineSafe(n) {
			t.Errorf("%q sollte inline gehen", n)
		}
	}
}

func TestMimeType(t *testing.T) {
	cases := map[string]string{
		"a.jpg": "image/jpeg", "a.mp4": "video/mp4", "a.pdf": "application/pdf",
		"a.unbekannt": "application/octet-stream", "a.go": "text/plain; charset=utf-8",
	}
	for n, want := range cases {
		if got := MimeType(n); got != want {
			t.Errorf("MimeType(%q) = %q, erwartet %q", n, got, want)
		}
	}
}

func TestCanThumb(t *testing.T) {
	if !CanThumb("foto.jpg", false) {
		t.Error("JPEG braucht kein ffmpeg")
	}
	if CanThumb("bild.svg", true) {
		t.Error("SVG soll der Browser selbst zeichnen")
	}
	if CanThumb("clip.mp4", false) {
		t.Error("Video ohne ffmpeg geht nicht")
	}
	if !CanThumb("clip.mp4", true) {
		t.Error("Video mit ffmpeg sollte gehen")
	}
}

func TestResizeBehaeltSeitenverhaeltnis(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 400))
	out := resize(src, 200)
	b := out.Bounds()
	if b.Dx() != 200 || b.Dy() != 100 {
		t.Errorf("Groesse %dx%d, erwartet 200x100", b.Dx(), b.Dy())
	}
	// Kleine Bilder werden nicht hochskaliert.
	small := image.NewRGBA(image.Rect(0, 0, 50, 30))
	if got := resize(small, 200); got.Bounds().Dx() != 50 {
		t.Errorf("kleines Bild wurde skaliert: %v", got.Bounds())
	}
}

func TestApplyOrientation(t *testing.T) {
	// 2x1-Bild: links rot, rechts blau.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	src.Set(1, 0, color.RGBA{0, 0, 255, 255})

	// Orientierung 2 spiegelt waagerecht.
	got := applyOrientation(src, 2)
	r, _, _, _ := got.At(0, 0).RGBA()
	if r > 0x8000 {
		t.Error("Orientierung 2 hat nicht gespiegelt")
	}
	// Orientierung 6 dreht um 90 Grad: aus 2x1 wird 1x2.
	got = applyOrientation(src, 6)
	if got.Bounds().Dx() != 1 || got.Bounds().Dy() != 2 {
		t.Errorf("Orientierung 6: %v, erwartet 1x2", got.Bounds())
	}
	// Ohne bzw. mit ungueltiger Angabe bleibt alles wie es ist.
	if applyOrientation(src, 0).Bounds() != src.Bounds() {
		t.Error("Orientierung 0 hat das Bild veraendert")
	}
}

func TestReadJPEGOrientation(t *testing.T) {
	// JPEG mit EXIF-Segment bauen, das Orientierung 6 enthaelt.
	var body bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := jpeg.Encode(&body, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := body.Bytes()

	exif := buildEXIF(6)
	withEXIF := append([]byte{}, raw[:2]...) // SOI
	seg := []byte{0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte((len(exif) + 2) & 0xFF)}
	withEXIF = append(withEXIF, seg...)
	withEXIF = append(withEXIF, exif...)
	withEXIF = append(withEXIF, raw[2:]...)

	if got := readJPEGOrientation(bytes.NewReader(withEXIF)); got != 6 {
		t.Errorf("Orientierung = %d, erwartet 6", got)
	}
	// Ohne EXIF muss 0 herauskommen, nicht ein Fehler.
	if got := readJPEGOrientation(bytes.NewReader(raw)); got != 0 {
		t.Errorf("ohne EXIF: %d, erwartet 0", got)
	}
	// Kein JPEG.
	if got := readJPEGOrientation(bytes.NewReader([]byte("kein bild"))); got != 0 {
		t.Errorf("kein JPEG: %d", got)
	}
}

// buildEXIF erzeugt ein minimales EXIF-Segment mit dem Orientierungs-Tag.
func buildEXIF(orientation uint16) []byte {
	b := []byte("Exif\x00\x00")
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0} // Little Endian, IFD bei Offset 8
	tiff = append(tiff, 1, 0)                   // ein Eintrag
	tiff = append(tiff, 0x12, 0x01)             // Tag 0x0112
	tiff = append(tiff, 3, 0)                   // Typ SHORT
	tiff = append(tiff, 1, 0, 0, 0)             // Anzahl 1
	tiff = append(tiff, byte(orientation), byte(orientation>>8), 0, 0)
	tiff = append(tiff, 0, 0, 0, 0) // kein weiterer IFD
	return append(b, tiff...)
}

func TestCacheBuildUndWiederverwendung(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, 8, 2)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 600, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 600; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()

	opens := 0
	open := func() (io.ReadCloser, error) {
		opens++
		return io.NopCloser(bytes.NewReader(raw)), nil
	}

	key := Key("loc", "bild.png", int64(len(raw)), time.Unix(1700000000, 0), 320)
	data, err := c.Build(context.Background(), key, "bild.png", open, Options{MaxDim: 320})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Vorschau ist kein JPEG: %v", err)
	}
	if dec.Bounds().Dx() != 320 {
		t.Errorf("Vorschaubreite %d, erwartet 320", dec.Bounds().Dx())
	}

	// Zweiter Aufruf muss aus dem Plattencache kommen.
	if _, err := c.Build(context.Background(), key, "bild.png", open, Options{MaxDim: 320}); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Errorf("Quelle %d-mal gelesen, erwartet 1 (Cache)", opens)
	}
	if _, ok := c.Get(key); !ok {
		t.Error("Cache-Treffer erwartet")
	}
	size, files := c.Size()
	if files == 0 || size == 0 {
		t.Error("Cache meldet keine Dateien")
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(key); ok {
		t.Error("Cache nach Clear nicht leer")
	}
}

func TestCacheUnbekanntesFormat(t *testing.T) {
	c, err := New(t.TempDir(), 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	open := func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("egal"))), nil }
	_, err = c.Build(context.Background(), "k", "datei.zip", open, Options{})
	if err != ErrUnsupported {
		t.Errorf("erwartet ErrUnsupported, war %v", err)
	}
}

func TestSafeKey(t *testing.T) {
	// Kurze oder bösartige Schlüssel dürfen nie einen Pfad erzeugen.
	for _, k := range []string{"", "k", "../../etc/passwd", "a/b", "ZZZZ"} {
		got := safeKey(k)
		if len(got) != 64 || !isHex(got) {
			t.Errorf("safeKey(%q) = %q", k, got)
		}
	}
	real := Key("loc", "a.jpg", 1, time.Unix(1, 0), 320)
	if safeKey(real) != real {
		t.Error("echter Schlüssel wurde unnötig umgeschrieben")
	}
}

func TestKeyAendertSichMitDatei(t *testing.T) {
	base := Key("loc", "a.jpg", 100, time.Unix(1, 0), 320)
	if base == Key("loc", "a.jpg", 101, time.Unix(1, 0), 320) {
		t.Error("Groesse geht nicht in den Schluessel ein")
	}
	if base == Key("loc", "a.jpg", 100, time.Unix(2, 0), 320) {
		t.Error("Zeitstempel geht nicht in den Schluessel ein")
	}
	if base == Key("loc", "a.jpg", 100, time.Unix(1, 0), 640) {
		t.Error("Groessenwunsch geht nicht in den Schluessel ein")
	}
	if base == Key("andere", "a.jpg", 100, time.Unix(1, 0), 320) {
		t.Error("Speicherort geht nicht in den Schluessel ein")
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
