package smb1

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// TRANS2-Unterbefehle (MS-CIFS 2.2.6).
const (
	trans2FindFirst2    = 0x0001
	trans2FindNext2     = 0x0002
	trans2QueryFSInfo   = 0x0003
	trans2QueryPathInfo = 0x0005
)

// Informationsstufen.
const (
	infoFileBothDirectory = 0x0104
	infoQueryFileAllInfo  = 0x0107
	infoQueryFSSizeInfo   = 0x0103
)

// trans2Result trägt Parameter- und Datenteil einer TRANS2-Antwort.
type trans2Result struct {
	params []byte
	data   []byte
}

// trans2 führt eine TRANS2-Transaktion aus und setzt mehrteilige Antworten
// wieder zusammen.
func (c *Client) trans2(ctx context.Context, setup uint16, params, data []byte) (*trans2Result, error) {
	words := make([]byte, 30)                                     // 14 feste Wörter + 1 Setup-Wort
	binary.LittleEndian.PutUint16(words[0:], uint16(len(params))) // TotalParameterCount
	binary.LittleEndian.PutUint16(words[2:], uint16(len(data)))   // TotalDataCount
	binary.LittleEndian.PutUint16(words[4:], 1024)                // MaxParameterCount
	maxData := min32(c.maxBuffer, 65535)
	if maxData > 1024 {
		maxData -= 1024 // Platz für Kopf und Parameter lassen
	}
	binary.LittleEndian.PutUint16(words[6:], uint16(maxData)) // MaxDataCount
	words[8] = 0                                              // MaxSetupCount
	binary.LittleEndian.PutUint16(words[18:], uint16(len(params)))
	binary.LittleEndian.PutUint16(words[22:], uint16(len(data)))
	words[26] = 1 // SetupCount
	binary.LittleEndian.PutUint16(words[28:], setup)

	// Der Datenteil beginnt mit dem (bei TRANS2 leeren) Namen. Danach werden
	// Parameter und Daten jeweils auf 4 Byte ausgerichtet - die Offsets zählen
	// ab dem Anfang der SMB-Nachricht.
	byteStart := 32 + 1 + len(words) + 2
	body := []byte{0x00} // Name: leere Zeichenkette

	padTo4 := func(cur int) int {
		if r := cur % 4; r != 0 {
			return 4 - r
		}
		return 0
	}

	pad := padTo4(byteStart + len(body))
	body = append(body, make([]byte, pad)...)
	paramOffset := byteStart + len(body)
	body = append(body, params...)

	pad = padTo4(byteStart + len(body))
	body = append(body, make([]byte, pad)...)
	dataOffset := byteStart + len(body)
	body = append(body, data...)

	binary.LittleEndian.PutUint16(words[20:], uint16(paramOffset))
	binary.LittleEndian.PutUint16(words[24:], uint16(dataOffset))

	resp, err := c.send(ctx, request{command: cmdTransaction2, words: words, data: body})
	if err != nil {
		return nil, err
	}
	if err := mapStatus("trans2", resp.hdr.status); err != nil {
		return nil, err
	}
	return c.collectTrans2(ctx, resp)
}

// collectTrans2 fügt eine gegebenenfalls über mehrere Nachrichten verteilte
// Antwort zusammen.
func (c *Client) collectTrans2(ctx context.Context, first *response) (*trans2Result, error) {
	out := &trans2Result{}
	totalParams, totalData := -1, -1

	resp := first
	for round := 0; round < 64; round++ {
		if len(resp.words) < 20 {
			return nil, errors.New("smb1: TRANS2-Antwort zu kurz")
		}
		tp := int(binary.LittleEndian.Uint16(resp.words[0:]))
		td := int(binary.LittleEndian.Uint16(resp.words[2:]))
		pc := int(binary.LittleEndian.Uint16(resp.words[6:]))
		po := int(binary.LittleEndian.Uint16(resp.words[8:]))
		dc := int(binary.LittleEndian.Uint16(resp.words[12:]))
		do := int(binary.LittleEndian.Uint16(resp.words[14:]))

		if totalParams < 0 {
			totalParams, totalData = tp, td
		}
		slice := func(off, n int) ([]byte, error) {
			if n == 0 {
				return nil, nil
			}
			if off < 0 || off+n > len(resp.raw) {
				return nil, fmt.Errorf("smb1: TRANS2-Bereich (%d+%d) liegt außerhalb der Nachricht", off, n)
			}
			return resp.raw[off : off+n], nil
		}
		p, err := slice(po, pc)
		if err != nil {
			return nil, err
		}
		d, err := slice(do, dc)
		if err != nil {
			return nil, err
		}
		out.params = append(out.params, p...)
		out.data = append(out.data, d...)

		if len(out.params) >= totalParams && len(out.data) >= totalData {
			return out, nil
		}
		// Es folgen weitere Teilantworten, ohne dass wir etwas senden müssten.
		next, err := c.readMore(ctx)
		if err != nil {
			return nil, err
		}
		resp = next
	}
	return nil, errors.New("smb1: TRANS2-Antwort endet nicht")
}

// readMore liest eine weitere Nachricht ohne vorheriges Senden.
func (c *Client) readMore(ctx context.Context) (*response, error) {
	if err := c.setDeadline(ctx); err != nil {
		return nil, err
	}
	raw, err := readNBT(c.conn)
	if err != nil {
		return nil, fmt.Errorf("smb1: Folgeantwort nicht lesbar: %w", err)
	}
	resp, err := parseResponse(raw)
	if err != nil {
		return nil, err
	}
	if err := mapStatus("trans2", resp.hdr.status); err != nil {
		return nil, err
	}
	return resp, nil
}
