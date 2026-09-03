// Kommando genicons erzeugt die App-Symbole von SpeedNAS.
//
// Die Symbole werden gerechnet statt gezeichnet: so liegen keine
// Binaerdateien unbekannter Herkunft im Repository, und jede Größe ist
// exakt gleich scharf. Aufruf: go run ./tools/genicons
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const outDir = "web/icons"

type rgb struct{ r, g, b float64 }

var (
	top    = rgb{0.24, 0.51, 0.98} // helles Blau
	bottom = rgb{0.36, 0.29, 0.90} // Indigo
	white  = rgb{1, 1, 1}
)

func main() {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	jobs := []struct {
		name     string
		size     int
		maskable bool
	}{
		{"icon-192.png", 192, false},
		{"icon-512.png", 512, false},
		{"icon-maskable-512.png", 512, true},
		{"apple-touch-icon.png", 180, true},
		{"favicon.png", 64, false},
	}
	for _, j := range jobs {
		img := render(j.size, j.maskable)
		f, err := os.Create(filepath.Join(outDir, j.name))
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, img); err != nil {
			panic(err)
		}
		f.Close()
		fmt.Printf("%s (%dx%d)\n", j.name, j.size, j.size)
	}
}

// render zeichnet das Symbol mit vierfacher Ueberabtastung, damit die Kanten
// sauber werden.
func render(size int, maskable bool) *image.RGBA {
	const ss = 4
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	n := float64(size)

	// Bei maskable-Symbolen schneidet Android bis zu 10 % am Rand ab,
	// deshalb sitzt der Inhalt dort kleiner in der Mitte.
	inset := 0.0
	if maskable {
		inset = 0.10
	}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var ar, ag, ab, aa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/ss) / n
					v := (float64(y) + (float64(sy)+0.5)/ss) / n
					c, alpha := shade(u, v, inset, maskable)
					ar += c.r * alpha
					ag += c.g * alpha
					ab += c.b * alpha
					aa += alpha
				}
			}
			total := float64(ss * ss)
			if aa > 0 {
				ar, ag, ab = ar/aa, ag/aa, ab/aa
			}
			a := aa / total
			img.Set(x, y, color.RGBA{
				R: clamp8(ar * a), G: clamp8(ag * a), B: clamp8(ab * a), A: clamp8(a),
			})
		}
	}
	return img
}

// shade liefert Farbe und Deckkraft an der Stelle (u,v) im Einheitsquadrat.
func shade(u, v, inset float64, maskable bool) (rgb, float64) {
	// Hintergrund: abgerundetes Quadrat mit Farbverlauf.
	bgAlpha := 1.0
	if !maskable {
		bgAlpha = coverage(roundedRectSDF(u, v, 0.02, 0.02, 0.96, 0.96, 0.22))
	}
	if bgAlpha <= 0 {
		return rgb{}, 0
	}
	bg := mix(top, bottom, v)

	// Inhaltsflaeche
	s := 1 - 2*inset
	cu := (u - inset) / s
	cv := (v - inset) / s

	// Ordner: Reiter oben links, Korpus darunter.
	body := roundedRectSDF(cu, cv, 0.18, 0.355, 0.64, 0.355, 0.065)
	tab := roundedRectSDF(cu, cv, 0.18, 0.285, 0.30, 0.10, 0.045)
	folder := math.Min(body, tab)

	// Blitz als Aussparung im Ordner - steht für Tempo.
	bolt := boltSDF(cu, cv)

	fa := coverage(folder)
	ba := coverage(bolt)
	// Der Blitz stanzt den Ordner aus und bekommt einen feinen Rand.
	fa = fa * (1 - ba)

	col := bg
	if fa > 0 {
		col = mix(bg, white, fa)
	}
	return col, bgAlpha
}

// coverage wandelt einen vorzeichenbehafteten Abstand in Deckung um.
func coverage(d float64) float64 {
	const e = 0.0035
	return clamp01(0.5 - d/(2*e))
}

// roundedRectSDF liefert den Abstand zu einem abgerundeten Rechteck.
func roundedRectSDF(px, py, x, y, w, h, r float64) float64 {
	cx, cy := x+w/2, y+h/2
	hx, hy := w/2-r, h/2-r
	dx := math.Abs(px-cx) - hx
	dy := math.Abs(py-cy) - hy
	ox, oy := math.Max(dx, 0), math.Max(dy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(dx, dy), 0) - r
}

// boltSDF beschreibt den Blitz als Polygon.
func boltSDF(px, py float64) float64 {
	pts := [][2]float64{
		{0.565, 0.395}, {0.405, 0.585}, {0.495, 0.585},
		{0.435, 0.775}, {0.605, 0.575}, {0.505, 0.575},
	}
	return polygonSDF(px, py, pts)
}

// polygonSDF ist der uebliche Abstand zu einem einfachen Polygon.
func polygonSDF(px, py float64, v [][2]float64) float64 {
	n := len(v)
	d := math.Pow(px-v[0][0], 2) + math.Pow(py-v[0][1], 2)
	s := 1.0
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		ex, ey := v[j][0]-v[i][0], v[j][1]-v[i][1]
		wx, wy := px-v[i][0], py-v[i][1]
		t := clamp01((wx*ex + wy*ey) / (ex*ex + ey*ey))
		bx, by := wx-ex*t, wy-ey*t
		if b := bx*bx + by*by; b < d {
			d = b
		}
		c1 := py >= v[i][1]
		c2 := py < v[j][1]
		c3 := ex*wy > ey*wx
		if (c1 && c2 && c3) || (!c1 && !c2 && !c3) {
			s = -s
		}
	}
	return s * math.Sqrt(d)
}

func mix(a, b rgb, t float64) rgb {
	t = clamp01(t)
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clamp8(v float64) uint8 {
	x := v * 255
	if x < 0 {
		x = 0
	}
	if x > 255 {
		x = 255
	}
	return uint8(x + 0.5)
}
