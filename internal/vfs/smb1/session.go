package smb1

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Fähigkeiten, die wir vom Server erbitten (MS-CIFS 2.2.4.52.2).
const (
	capUnicode     = 0x00000004
	capLargeFiles  = 0x00000008
	capNTSMBs      = 0x00000010
	capStatus32    = 0x00000040
	capNTFind      = 0x00000200
	capLargeReadX  = 0x00004000
	capLargeWrite  = 0x00008000
	capExtSecurity = 0x80000000
)

const dialectNTLM = "NT LM 0.12"

// Dial baut die Verbindung auf, meldet sich an und verbindet die Freigabe.
func Dial(ctx context.Context, opts Options) (*Client, error) {
	timeout := opts.DialTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", opts.addr())
	if err != nil {
		return nil, fmt.Errorf("smb1: TCP-Verbindung zu %s fehlgeschlagen: %w", opts.addr(), err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		// Ohne NODELAY addiert die Nagle-Verzögerung bis zu 40 ms auf jede
		// kleine Anfrage - bei SMB1 mit seinen vielen Rundläufen fatal.
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}

	c := &Client{conn: conn, opts: opts, unicode: true}

	if err := c.negotiate(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.sessionSetup(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.treeConnect(ctx, opts.Share); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// negotiate handelt den Dialekt aus und holt die Challenge für die Anmeldung.
func (c *Client) negotiate(ctx context.Context) error {
	// Wir bieten ausschließlich "NT LM 0.12" an. Ältere Dialekte (LANMAN,
	// Core Protocol) können kein NTLMv2 und keine langen Dateinamen.
	payload := []byte{0x02}
	payload = append(payload, []byte(dialectNTLM)...)
	payload = append(payload, 0x00)

	// Beim Aushandeln steht die Kodierung noch nicht fest - der Dialektname
	// ist immer ASCII. Erweiterte Sicherheit fragen wir an; Geräte, die sie
	// nicht können, antworten schlicht auf die alte Art.
	c.unicode = false
	c.wantExtSec = true
	resp, err := c.send(ctx, request{command: cmdNegotiate, data: payload})
	c.unicode = true
	if err != nil {
		return err
	}
	if err := mapStatus("negotiate", resp.hdr.status); err != nil {
		return err
	}
	if len(resp.words) < 34 {
		return fmt.Errorf("smb1: der Server hat kein %q angeboten - vermutlich spricht er nur SMB2 oder neuer", dialectNTLM)
	}
	if idx := binary.LittleEndian.Uint16(resp.words[0:]); idx == 0xFFFF {
		return fmt.Errorf("smb1: der Server lehnt %q ab", dialectNTLM)
	}

	c.maxBuffer = binary.LittleEndian.Uint32(resp.words[7:])
	serverCaps := binary.LittleEndian.Uint32(resp.words[19:])
	keyLen := int(resp.words[33])

	// Kann der Server kein Unicode, fallen wir auf die alte Kodierung zurück.
	c.unicode = serverCaps&capUnicode != 0
	c.extSec = serverCaps&capExtSecurity != 0

	if keyLen > 0 {
		if len(resp.data) < keyLen {
			return errors.New("smb1: Challenge fehlt in der Antwort")
		}
		c.challenge = append([]byte(nil), resp.data[:keyLen]...)
	}
	if !c.extSec && len(c.challenge) != 8 && c.opts.User != "" {
		return errors.New("smb1: der Server bietet weder erweiterte Sicherheit noch eine Challenge an")
	}
	if c.maxBuffer == 0 || c.maxBuffer > 1<<20 {
		c.maxBuffer = 65535
	}
	return nil
}

// sessionSetup meldet sich an. Ohne Benutzernamen wird der Gastzugang
// versucht - genau das, was viele Router anbieten.
func (c *Client) sessionSetup(ctx context.Context) error {
	if c.extSec {
		return c.sessionSetupNTLMSSP(ctx)
	}
	// Sehr alte Geräte ohne erweiterte Sicherheit: Antwort-Blobs direkt im
	// Session-Setup. Heutiges Samba lehnt das ab, betagte Router brauchen es.
	return c.sessionSetupLegacy(ctx)
}

// sessionSetupNTLMSSP führt die zweistufige Anmeldung durch.
func (c *Client) sessionSetupNTLMSSP(ctx context.Context) error {
	// Runde 1: unsere Fähigkeiten anbieten.
	resp, err := c.sendSessionSetupBlob(ctx, ntlmNegotiate())
	if err != nil {
		return err
	}
	if resp.hdr.status != statusMoreProcessing && resp.hdr.status != statusSuccess {
		return mapStatus("anmeldung (runde 1)", resp.hdr.status)
	}
	// Die Sitzungskennung gilt ab jetzt für die zweite Runde.
	c.uid = resp.hdr.uid

	blob, err := extractSecurityBlob(resp)
	if err != nil {
		return err
	}
	token := findNTLMSSP(blob)
	if token == nil {
		return errors.New("smb1: der Server hat keine NTLMSSP-Challenge geschickt")
	}
	ch, err := parseChallenge(token)
	if err != nil {
		return err
	}

	domain := c.opts.Domain
	if domain == "" {
		// Ohne Vorgabe den Namen nehmen, den der Server selbst nennt.
		domain = ch.targetName
	}

	auth, err := ntlmAuthenticate(ch, c.opts.User, c.opts.Password, domain, "SpeedNAS")
	if err != nil {
		return err
	}

	// Runde 2: die eigentliche Anmeldung.
	resp2, err := c.sendSessionSetupBlob(ctx, auth)
	if err != nil {
		return err
	}
	if err := mapStatus("anmeldung", resp2.hdr.status); err != nil {
		if errors.Is(err, ErrAuth) {
			if c.opts.User == "" {
				return fmt.Errorf("smb1: Gastzugang abgelehnt - Benutzername und Passwort nötig")
			}
			return fmt.Errorf("smb1: Benutzername oder Passwort falsch")
		}
		return err
	}
	c.uid = resp2.hdr.uid
	return nil
}

// sendSessionSetupBlob verschickt eine Session-Setup-Nachricht mit
// Sicherheits-Token (Wortzahl 12, erweiterte Sicherheit).
func (c *Client) sendSessionSetupBlob(ctx context.Context, blob []byte) (*response, error) {
	words := make([]byte, 24)
	words[0] = 0xFF // AndXCommand: kein weiterer Befehl
	binary.LittleEndian.PutUint16(words[4:], uint16(min32(c.maxBuffer, 65535)))
	binary.LittleEndian.PutUint16(words[6:], 50) // MaxMpxCount
	binary.LittleEndian.PutUint16(words[8:], 0)  // VcNumber
	binary.LittleEndian.PutUint32(words[10:], 0) // SessionKey
	binary.LittleEndian.PutUint16(words[14:], uint16(len(blob)))
	caps := uint32(capNTSMBs | capStatus32 | capNTFind | capLargeFiles |
		capLargeReadX | capLargeWrite | capExtSecurity)
	if c.unicode {
		caps |= capUnicode
	}
	binary.LittleEndian.PutUint32(words[20:], caps)

	data := make([]byte, 0, len(blob)+64)
	data = append(data, blob...)
	if c.unicode && (32+1+len(words)+2+len(data))%2 != 0 {
		data = append(data, 0x00)
	}
	data = append(data, c.encodeString("SpeedNAS")...)
	data = append(data, c.encodeString("SpeedNAS")...)

	return c.send(ctx, request{command: cmdSessionSetupAndX, words: words, data: data})
}

// extractSecurityBlob liest das Token aus einer Session-Setup-Antwort
// (Wortzahl 4: AndX, Action, SecurityBlobLength).
func extractSecurityBlob(r *response) ([]byte, error) {
	if len(r.words) < 8 {
		return nil, errors.New("smb1: Antwort ohne Sicherheits-Token")
	}
	n := int(binary.LittleEndian.Uint16(r.words[6:]))
	if n == 0 {
		return nil, nil
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	return r.data[:n], nil
}

// findNTLMSSP sucht das NTLMSSP-Token - notfalls innerhalb einer
// SPNEGO-Hülle, ohne dafür ASN.1 zerlegen zu müssen.
func findNTLMSSP(blob []byte) []byte {
	sig := []byte(ntlmsspSignature)
	for i := 0; i+len(sig) <= len(blob); i++ {
		if string(blob[i:i+len(sig)]) == ntlmsspSignature {
			return blob[i:]
		}
	}
	return nil
}

// sessionSetupLegacy ist der alte Weg ohne erweiterte Sicherheit.
func (c *Client) sessionSetupLegacy(ctx context.Context) error {
	var lm, nt []byte
	user := c.opts.User
	domain := c.opts.Domain
	if domain == "" {
		domain = "WORKGROUP"
	}

	anonymous := user == ""
	if anonymous {
		// Anonyme Anmeldung: ein einzelnes Nullbyte als LM-Antwort, keine
		// NT-Antwort, leerer Benutzername.
		lm = []byte{0x00}
		nt = nil
		domain = ""
	} else {
		if len(c.challenge) != 8 {
			return errors.New("smb1: der Server hat keine Challenge geliefert - Anmeldung nicht möglich")
		}
		cc, err := newClientChallenge()
		if err != nil {
			return err
		}
		key := ntowfv2(user, c.opts.Password, strings.ToUpper(domain))
		// Ohne erweiterte Sicherheit liefert der Server keine AV-Paare; ein
		// leerer Zielinformationsblock ist hier korrekt.
		nt = ntlmv2Response(key, c.challenge, cc, nil, time.Now())
		lm = lmv2Response(key, c.challenge, cc)
	}

	words := make([]byte, 26)
	words[0] = 0xFF // AndXCommand: kein weiterer Befehl
	binary.LittleEndian.PutUint16(words[4:], uint16(min32(c.maxBuffer, 65535)))
	binary.LittleEndian.PutUint16(words[6:], 50) // MaxMpxCount
	binary.LittleEndian.PutUint16(words[8:], 0)  // VcNumber
	binary.LittleEndian.PutUint32(words[10:], 0) // SessionKey
	binary.LittleEndian.PutUint16(words[14:], uint16(len(lm)))
	binary.LittleEndian.PutUint16(words[16:], uint16(len(nt)))
	caps := uint32(capNTSMBs | capStatus32 | capNTFind | capLargeFiles | capLargeReadX | capLargeWrite)
	if c.unicode {
		caps |= capUnicode
	}
	binary.LittleEndian.PutUint32(words[22:], caps)

	data := make([]byte, 0, 128)
	data = append(data, lm...)
	data = append(data, nt...)
	// Zeichenketten müssen bei Unicode auf gerader Position beginnen,
	// gerechnet ab dem Anfang der SMB-Nachricht.
	if c.unicode && (32+1+len(words)+2+len(data))%2 != 0 {
		data = append(data, 0x00)
	}
	data = append(data, c.encodeString(user)...)
	data = append(data, c.encodeString(domain)...)
	data = append(data, c.encodeString("SpeedNAS")...)
	data = append(data, c.encodeString("SpeedNAS")...)

	resp, err := c.send(ctx, request{command: cmdSessionSetupAndX, words: words, data: data})
	if err != nil {
		return err
	}
	if err := mapStatus("anmeldung", resp.hdr.status); err != nil {
		if errors.Is(err, ErrAuth) && anonymous {
			return fmt.Errorf("smb1: Gastzugang abgelehnt - Benutzername und Passwort nötig")
		}
		return err
	}
	c.uid = resp.hdr.uid
	return nil
}

// treeConnect verbindet die Freigabe.
func (c *Client) treeConnect(ctx context.Context, share string) error {
	words := make([]byte, 8)
	words[0] = 0xFF                             // AndXCommand
	binary.LittleEndian.PutUint16(words[4:], 0) // Flags
	binary.LittleEndian.PutUint16(words[6:], 1) // Länge des Passworts

	path := fmt.Sprintf(`\\%s\%s`, c.opts.Host, share)

	data := make([]byte, 0, 64)
	data = append(data, 0x00) // leeres Passwort
	if c.unicode && (32+1+len(words)+2+len(data))%2 != 0 {
		data = append(data, 0x00)
	}
	data = append(data, c.encodeString(path)...)
	// Der Dienstname ist immer ASCII, auch wenn Unicode ausgehandelt wurde.
	data = append(data, []byte("?????")...)
	data = append(data, 0x00)

	resp, err := c.send(ctx, request{command: cmdTreeConnectAndX, words: words, data: data})
	if err != nil {
		return err
	}
	if err := mapStatus("freigabe verbinden", resp.hdr.status); err != nil {
		if errors.Is(err, ErrShareUnknown) {
			return fmt.Errorf("smb1: Freigabe %q unbekannt - Schreibweise prüfen", share)
		}
		return err
	}
	c.tid = resp.hdr.tid
	return nil
}

// Echo prüft billig, ob die Verbindung noch lebt.
func (c *Client) Echo(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	words := make([]byte, 2)
	binary.LittleEndian.PutUint16(words, 1) // eine Antwort erbeten
	resp, err := c.send(ctx, request{command: cmdEcho, words: words, data: []byte("ping")})
	if err != nil {
		return err
	}
	return mapStatus("echo", resp.hdr.status)
}

func min32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
