// Package thumb erzeugt Vorschaubilder und legt sie auf Platte ab.
package thumb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// Cache erzeugt Vorschaubilder und hält sie auf Platte vor.
//
// Der Plattencache ist hier kein Luxus: das Bild muss sonst bei jedem
// Scrollen erneut über SMB gelesen werden, und ein 4-MB-Foto vom
// USB-2.0-Stick am Router dauert spürbar.
type Cache struct {
	dir      string
	maxBytes int64
	sem      chan struct{}

	// single-flight: gleiche Vorschau nicht mehrfach parallel bauen
	mu      sync.Mutex
	inWork  map[string]*flight
	ffmpeg  string
	hasFF   bool
	ffOnce  sync.Once
	evictMu sync.Mutex
}

type flight struct {
	done chan struct{}
	data []byte
	err  error
}

// Options steuert eine einzelne Vorschau.
type Options struct {
	MaxDim  int // längste Kante in Pixeln
	Quality int // JPEG-Qualität
}

// New erzeugt einen Cache im angegebenen Verzeichnis.
func New(dir string, maxMB, workers int) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if workers < 1 {
		workers = 1
	}
	c := &Cache{
		dir:      dir,
		maxBytes: int64(maxMB) << 20,
		sem:      make(chan struct{}, workers),
		inWork:   make(map[string]*flight),
	}
	return c, nil
}

// Key bildet einen stabilen Cache-Schlüssel. mtime und Größe gehen ein,
// damit eine geänderte Datei automatisch eine neue Vorschau bekommt.
func Key(locationID, path string, size int64, mod time.Time, maxDim int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\x00%d", locationID, path, size, mod.UnixNano(), maxDim)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cache) file(key string) string {
	key = safeKey(key)
	// Zwei Ebenen Unterordner: sonst liegen zehntausende Dateien flach in
	// einem Verzeichnis, was auf NTFS spürbar langsam wird.
	return filepath.Join(c.dir, key[:2], key[2:4], key+".jpg")
}

// safeKey erzwingt einen 64 Zeichen langen Hex-Schlüssel. Aus Key() kommt das
// ohnehin so; alles andere wird gehasht, damit weder zu kurze Schlüssel noch
// Pfadtrenner darin je einen Dateipfad erzeugen können.
func safeKey(key string) string {
	if len(key) == 64 && isHex(key) {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Get liefert eine Vorschau aus dem Cache, falls vorhanden.
func (c *Cache) Get(key string) ([]byte, bool) {
	p := c.file(key)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // Zugriffszeit für die LRU-Verdrängung
	return data, true
}

// ErrUnsupported meldet ein Format, für das es keine Vorschau gibt.
var ErrUnsupported = errors.New("kein Vorschauformat")

// Build erzeugt die Vorschau aus dem gelieferten Datenstrom und legt sie ab.
// Mehrfache Anfragen für denselben Schlüssel werden zusammengefasst.
func (c *Cache) Build(ctx context.Context, key, name string, open func() (io.ReadCloser, error), opt Options) ([]byte, error) {
	if data, ok := c.Get(key); ok {
		return data, nil
	}

	c.mu.Lock()
	if f, ok := c.inWork[key]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.data, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	c.inWork[key] = f
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.inWork, key)
		c.mu.Unlock()
		close(f.done)
	}()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		f.err = ctx.Err()
		return nil, f.err
	}

	data, err := c.build(ctx, name, open, opt)
	f.data, f.err = data, err
	if err == nil {
		c.store(key, data)
	}
	return data, err
}

func (c *Cache) build(ctx context.Context, name string, open func() (io.ReadCloser, error), opt Options) ([]byte, error) {
	if opt.MaxDim <= 0 {
		opt.MaxDim = 320
	}
	if opt.Quality <= 0 {
		opt.Quality = 78
	}
	kind := KindOf(name)
	switch kind {
	case KindImage:
		return c.fromImage(ctx, name, open, opt)
	case KindVideo, KindHEIC:
		return c.fromFFmpeg(ctx, name, open, opt)
	}
	return nil, ErrUnsupported
}

func (c *Cache) fromImage(ctx context.Context, name string, open func() (io.ReadCloser, error), opt Options) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// Die Datei wird nur einmal gelesen; EXIF und Decoder arbeiten auf
	// demselben Puffer.
	raw, err := io.ReadAll(io.LimitReader(rc, 64<<20))
	if err != nil {
		return nil, err
	}
	orient := Orientation(0)
	if isJPEG(name) {
		orient = readJPEGOrientation(bytes.NewReader(raw))
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("Bild nicht lesbar: %w", err)
	}
	img = resize(img, opt.MaxDim)
	img = applyOrientation(img, orient)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opt.Quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fromFFmpeg zieht ein Einzelbild aus Videos - und dekodiert nebenbei HEIC,
// das iPhones standardmäßig aufnehmen und wofür es in Go keinen Decoder gibt.
func (c *Cache) fromFFmpeg(ctx context.Context, name string, open func() (io.ReadCloser, error), opt Options) ([]byte, error) {
	if !c.FFmpegAvailable() {
		return nil, ErrUnsupported
	}
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", opt.MaxDim),
		"-f", "image2", "-vcodec", "mjpeg",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, c.ffmpeg, args...)
	cmd.Stdin = rc
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	if out.Len() == 0 {
		return nil, ErrUnsupported
	}
	return out.Bytes(), nil
}

// FFmpegAvailable meldet, ob ffmpeg im Suchpfad liegt. Ist es da, gibt es
// zusätzlich Videovorschauen und HEIC-Unterstützung.
func (c *Cache) FFmpegAvailable() bool {
	c.ffOnce.Do(func() {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			c.ffmpeg, c.hasFF = p, true
		}
	})
	return c.hasFF
}

func (c *Cache) store(key string, data []byte) {
	p := c.file(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return
	}
	go c.evict()
}

// evict hält den Cache unter der eingestellten Größe; am längsten nicht
// genutzte Vorschauen fliegen zuerst raus.
func (c *Cache) evict() {
	if !c.evictMu.TryLock() {
		return
	}
	defer c.evictMu.Unlock()

	type item struct {
		path string
		size int64
		mod  time.Time
	}
	var items []item
	var total int64
	_ = filepath.Walk(c.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".jpg") {
			return nil
		}
		items = append(items, item{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total <= c.maxBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	target := c.maxBytes * 8 / 10 // auf 80 % herunter, damit nicht ständig geräumt wird
	for _, it := range items {
		if total <= target {
			break
		}
		if os.Remove(it.path) == nil {
			total -= it.size
		}
	}
}

// Clear leert den Vorschaucache vollständig.
func (c *Cache) Clear() error {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()
	if err := os.RemoveAll(c.dir); err != nil {
		return err
	}
	return os.MkdirAll(c.dir, 0o700)
}

// Size meldet die aktuelle Belegung des Caches.
func (c *Cache) Size() (bytes int64, files int) {
	_ = filepath.Walk(c.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		bytes += info.Size()
		files++
		return nil
	})
	return
}

func resize(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return src
	}
	if w >= h {
		h = h * maxDim / w
		w = maxDim
	} else {
		w = w * maxDim / h
		h = maxDim
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// CatmullRom kostet etwas mehr Rechenzeit, sieht bei Fotos aber deutlich
	// besser aus als eine einfache Mittelung.
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

func applyOrientation(img image.Image, o Orientation) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	swap := o >= 5
	if swap {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, c)
		}
	}
	return dst
}
