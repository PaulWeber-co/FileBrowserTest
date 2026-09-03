// Package smbprobe spricht die SMB-Aushandlung roh auf dem Draht, um
// herauszufinden, was ein Server tatsächlich kann.
//
// Hintergrund: Wenn der Windows-Explorer oder die iOS-Dateien-App eine
// Freigabe nicht öffnen, liegt das fast immer an der Protokollversion.
// Windows 11 und iOS haben SMB1 komplett entfernt; viele Router liefern ab
// Werk nur SMB1 oder ein sehr altes SMB2. Statt zu raten, fragen wir den
// Server direkt - und können dem Nutzer dann sagen, was Sache ist.
package smbprobe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Result fasst zusammen, was der Server auf der Leitung preisgibt.
type Result struct {
	Host string `json:"host"`
	Port int    `json:"port"`

	Reachable bool   `json:"reachable"`
	DialError string `json:"dialError,omitempty"`

	SMB1        bool   `json:"smb1"`
	SMB1Dialect string `json:"smb1Dialect,omitempty"`
	SMB1Error   string `json:"smb1Error,omitempty"`

	SMB2         bool   `json:"smb2"`
	Dialect      uint16 `json:"dialect,omitempty"`
	DialectName  string `json:"dialectName,omitempty"`
	SMB2Error    string `json:"smb2Error,omitempty"`
	SigningOn    bool   `json:"signingEnabled"`
	SigningForce bool   `json:"signingRequired"`

	MaxReadSize     uint32 `json:"maxReadSize,omitempty"`
	MaxWriteSize    uint32 `json:"maxWriteSize,omitempty"`
	MaxTransactSize uint32 `json:"maxTransactSize,omitempty"`

	RTT time.Duration `json:"-"`
	// RTTms ist die TCP-Antwortzeit in Millisekunden.
	RTTms float64 `json:"rttMs"`
}

func dialectName(code uint16) string {
	switch code {
	case 0x0202:
		return "SMB 2.0.2"
	case 0x0210:
		return "SMB 2.1"
	case 0x0300:
		return "SMB 3.0"
	case 0x0302:
		return "SMB 3.0.2"
	case 0x0311:
		return "SMB 3.1.1"
	case 0x02ff:
		return "SMB 2 Wildcard"
	}
	return fmt.Sprintf("unbekannt (0x%04x)", code)
}

// Probe fragt Port 445 (bzw. den angegebenen Port) nach SMB1 und SMB2/3.
func Probe(ctx context.Context, host string, port int, timeout time.Duration) Result {
	if port == 0 {
		port = 445
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	res := Result{Host: host, Port: port}
	addr := net.JoinHostPort(host, fmt.Sprint(port))

	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		res.DialError = err.Error()
		return res
	}
	res.RTT = time.Since(start)
	res.RTTms = float64(res.RTT.Microseconds()) / 1000
	res.Reachable = true
	c.Close()

	// SMB2/3: eigene Verbindung, weil ein fehlgeschlagener Negotiate die
	// Verbindung unbrauchbar macht.
	if conn, err := d.DialContext(ctx, "tcp", addr); err == nil {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if r, err := negotiateSMB2(conn); err != nil {
			res.SMB2Error = err.Error()
		} else {
			res.SMB2 = true
			res.Dialect = r.dialect
			res.DialectName = dialectName(r.dialect)
			res.SigningOn = r.securityMode&0x0001 != 0
			res.SigningForce = r.securityMode&0x0002 != 0
			res.MaxReadSize = r.maxRead
			res.MaxWriteSize = r.maxWrite
			res.MaxTransactSize = r.maxTransact
		}
		conn.Close()
	}

	if conn, err := d.DialContext(ctx, "tcp", addr); err == nil {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if name, err := negotiateSMB1(conn); err != nil {
			res.SMB1Error = err.Error()
		} else {
			res.SMB1 = true
			res.SMB1Dialect = name
		}
		conn.Close()
	}

	return res
}

// ---------------------------------------------------------------- SMB2 -----

type smb2Result struct {
	dialect      uint16
	securityMode uint16
	maxTransact  uint32
	maxRead      uint32
	maxWrite     uint32
}

var smb2Dialects = []uint16{0x0202, 0x0210, 0x0300, 0x0302, 0x0311}

func negotiateSMB2(conn net.Conn) (smb2Result, error) {
	var out smb2Result

	hdr := make([]byte, 64)
	copy(hdr, []byte{0xFE, 'S', 'M', 'B'})
	binary.LittleEndian.PutUint16(hdr[4:], 64) // StructureSize
	binary.LittleEndian.PutUint16(hdr[6:], 0)  // CreditCharge
	binary.LittleEndian.PutUint16(hdr[12:], 0) // Command NEGOTIATE = 0
	binary.LittleEndian.PutUint16(hdr[14:], 1) // CreditRequest

	body := make([]byte, 36+len(smb2Dialects)*2)
	binary.LittleEndian.PutUint16(body[0:], 36)                        // StructureSize
	binary.LittleEndian.PutUint16(body[2:], uint16(len(smb2Dialects))) // DialectCount
	binary.LittleEndian.PutUint16(body[4:], 0x0001)                    // SigningEnabled
	binary.LittleEndian.PutUint32(body[8:], 0x0000007F)                // Capabilities
	if _, err := rand.Read(body[12:28]); err != nil {                  // ClientGuid
		return out, err
	}
	for i, d := range smb2Dialects {
		binary.LittleEndian.PutUint16(body[36+i*2:], d)
	}

	// SMB 3.1.1 verlangt zwingend einen Preauth-Integrity-Kontext.
	pad := (8 - (64+len(body))%8) % 8
	ctxOff := uint32(64 + len(body) + pad)
	preauth := make([]byte, 8+38)
	binary.LittleEndian.PutUint16(preauth[0:], 0x0001) // SMB2_PREAUTH_INTEGRITY_CAPABILITIES
	binary.LittleEndian.PutUint16(preauth[2:], 38)     // DataLength
	binary.LittleEndian.PutUint16(preauth[8:], 1)      // HashAlgorithmCount
	binary.LittleEndian.PutUint16(preauth[10:], 32)    // SaltLength
	binary.LittleEndian.PutUint16(preauth[12:], 0x0001)
	if _, err := rand.Read(preauth[14:46]); err != nil {
		return out, err
	}
	binary.LittleEndian.PutUint32(body[28:], ctxOff) // NegotiateContextOffset
	binary.LittleEndian.PutUint16(body[32:], 1)      // NegotiateContextCount

	msg := append(append(append(hdr, body...), make([]byte, pad)...), preauth...)
	if err := writeNBT(conn, msg); err != nil {
		return out, fmt.Errorf("senden fehlgeschlagen: %w", err)
	}
	resp, err := readNBT(conn)
	if err != nil {
		return out, fmt.Errorf("keine Antwort: %w", err)
	}
	if len(resp) < 64+64 {
		return out, errors.New("Antwort zu kurz")
	}
	if resp[0] != 0xFE || resp[1] != 'S' || resp[2] != 'M' || resp[3] != 'B' {
		return out, errors.New("keine SMB2-Antwort")
	}
	if status := binary.LittleEndian.Uint32(resp[8:]); status != 0 {
		return out, fmt.Errorf("Server lehnt Aushandlung ab (NTSTATUS 0x%08x)", status)
	}
	b := resp[64:]
	out.securityMode = binary.LittleEndian.Uint16(b[2:])
	out.dialect = binary.LittleEndian.Uint16(b[4:])
	// Feste Feldpositionen der SMB2-NEGOTIATE-Antwort (MS-SMB2 2.2.4).
	out.maxTransact = binary.LittleEndian.Uint32(b[28:])
	out.maxRead = binary.LittleEndian.Uint32(b[32:])
	out.maxWrite = binary.LittleEndian.Uint32(b[36:])
	return out, nil
}

// ---------------------------------------------------------------- SMB1 -----

var smb1Dialects = []string{"NT LM 0.12", "SMB 2.002", "SMB 2.???"}

func negotiateSMB1(conn net.Conn) (string, error) {
	var payload []byte
	for _, d := range smb1Dialects {
		payload = append(payload, 0x02)
		payload = append(payload, []byte(d)...)
		payload = append(payload, 0x00)
	}

	msg := make([]byte, 0, 32+3+len(payload))
	hdr := make([]byte, 32)
	copy(hdr, []byte{0xFF, 'S', 'M', 'B'})
	hdr[4] = 0x72                                   // SMB_COM_NEGOTIATE
	hdr[9] = 0x18                                   // Flags: CASE_INSENSITIVE | CANONICALIZED_PATHS
	binary.LittleEndian.PutUint16(hdr[10:], 0xC853) // Flags2: Unicode, NT-Status, erweiterte Sicherheit
	binary.LittleEndian.PutUint16(hdr[26:], 0xFEFF) // PIDLow
	msg = append(msg, hdr...)
	msg = append(msg, 0x00) // WordCount
	bc := make([]byte, 2)
	binary.LittleEndian.PutUint16(bc, uint16(len(payload)))
	msg = append(msg, bc...)
	msg = append(msg, payload...)

	if err := writeNBT(conn, msg); err != nil {
		return "", fmt.Errorf("senden fehlgeschlagen: %w", err)
	}
	resp, err := readNBT(conn)
	if err != nil {
		return "", fmt.Errorf("keine Antwort (SMB1 vermutlich deaktiviert): %w", err)
	}
	if len(resp) < 37 {
		return "", errors.New("Antwort zu kurz")
	}
	if resp[0] == 0xFE {
		// Server antwortet direkt in SMB2 - SMB1 ist aus.
		return "", errors.New("Server antwortet mit SMB2, SMB1 ist deaktiviert")
	}
	if resp[0] != 0xFF || resp[1] != 'S' || resp[2] != 'M' || resp[3] != 'B' {
		return "", errors.New("keine SMB1-Antwort")
	}
	wordCount := resp[32]
	if wordCount == 0 {
		return "", errors.New("Server lehnt alle angebotenen Dialekte ab")
	}
	idx := binary.LittleEndian.Uint16(resp[33:])
	if idx == 0xFFFF {
		return "", errors.New("Server lehnt alle angebotenen Dialekte ab")
	}
	if int(idx) >= len(smb1Dialects) {
		return "", fmt.Errorf("unerwarteter Dialektindex %d", idx)
	}
	return smb1Dialects[idx], nil
}

// ------------------------------------------------------------- NetBIOS -----

// writeNBT verpackt eine Nachricht in den NetBIOS-Session-Service-Rahmen,
// den SMB auf Port 445 verwendet.
func writeNBT(conn net.Conn, msg []byte) error {
	h := []byte{0x00, byte(len(msg) >> 16), byte(len(msg) >> 8), byte(len(msg))}
	if _, err := conn.Write(append(h, msg...)); err != nil {
		return err
	}
	return nil
}

func readNBT(conn net.Conn) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(conn, h[:]); err != nil {
		return nil, err
	}
	n := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
	if n <= 0 || n > 1<<20 {
		return nil, fmt.Errorf("unplausible Länge %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
