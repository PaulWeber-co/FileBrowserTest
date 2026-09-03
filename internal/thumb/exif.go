package thumb

import (
	"encoding/binary"
	"errors"
	"io"
)

// Orientation ist der EXIF-Ausrichtungswert 1..8. 0 bedeutet "unbekannt".
type Orientation int

// readJPEGOrientation liest das EXIF-Feld 0x0112 aus einem JPEG-Kopf.
//
// Ohne das stehen Handyfotos in der Galerie quer: Kameras speichern das Bild
// unrotiert und notieren die Drehung nur als Metadatum.
func readJPEGOrientation(r io.Reader) Orientation {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi[0] != 0xFF || soi[1] != 0xD8 {
		return 0
	}
	// Nur den Anfang der Datei durchsuchen; EXIF steht immer weit vorn.
	limited := io.LimitReader(r, 512*1024)
	for {
		marker, size, err := nextSegment(limited)
		if err != nil {
			return 0
		}
		if marker == 0xE1 && size > 6 {
			buf := make([]byte, size)
			if _, err := io.ReadFull(limited, buf); err != nil {
				return 0
			}
			if o, err := parseEXIF(buf); err == nil {
				return o
			}
			continue
		}
		if marker == 0xDA { // Beginn der Bilddaten - danach kommt kein EXIF mehr
			return 0
		}
		if _, err := io.CopyN(io.Discard, limited, int64(size)); err != nil {
			return 0
		}
	}
}

func nextSegment(r io.Reader) (marker byte, size int, err error) {
	var b [1]byte
	// Marker beginnen mit 0xFF; Füllbytes überspringen.
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, 0, err
		}
		if b[0] == 0xFF {
			break
		}
	}
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, 0, err
		}
		if b[0] != 0xFF {
			break
		}
	}
	marker = b[0]
	if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) {
		return marker, 0, nil
	}
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return 0, 0, err
	}
	size = int(binary.BigEndian.Uint16(l[:])) - 2
	if size < 0 {
		return 0, 0, errors.New("ungültige Segmentlänge")
	}
	return marker, size, nil
}

func parseEXIF(b []byte) (Orientation, error) {
	if len(b) < 14 || string(b[:6]) != "Exif\x00\x00" {
		return 0, errors.New("kein EXIF-Segment")
	}
	tiff := b[6:]
	if len(tiff) < 8 {
		return 0, errors.New("TIFF-Kopf zu kurz")
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0, errors.New("unbekannte Byte-Reihenfolge")
	}
	off := bo.Uint32(tiff[4:8])
	if off+2 > uint32(len(tiff)) {
		return 0, errors.New("IFD-Offset außerhalb")
	}
	n := int(bo.Uint16(tiff[off : off+2]))
	base := off + 2
	for i := 0; i < n; i++ {
		e := base + uint32(i*12)
		if e+12 > uint32(len(tiff)) {
			break
		}
		tag := bo.Uint16(tiff[e : e+2])
		if tag == 0x0112 {
			v := bo.Uint16(tiff[e+8 : e+10])
			if v >= 1 && v <= 8 {
				return Orientation(v), nil
			}
			return 0, nil
		}
	}
	return 0, errors.New("kein Orientierungs-Tag")
}
