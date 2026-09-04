package smb1

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// NTLMSSP - die Anmeldung, die auch echte Windows-Clients über SMB1 verwenden.
//
// Der alte Weg (Antwort-Blobs direkt im Session-Setup) wird von heutigem Samba
// abgelehnt: "Rejecting raw NTLMv2 authentication". Stattdessen läuft die
// Anmeldung in zwei Runden:
//
//	1. Client schickt NTLMSSP_NEGOTIATE
//	2. Server antwortet mit NTLMSSP_CHALLENGE (Challenge + Zielinformationen)
//	3. Client schickt NTLMSSP_AUTHENTICATE mit der NTLMv2-Antwort
//
// Üblicherweise steckt das in einer SPNEGO-Hülle (ASN.1 DER). Samba und
// Windows akzeptieren aber auch das nackte NTLMSSP-Token, was uns einen
// kompletten ASN.1-Kodierer erspart.

const ntlmsspSignature = "NTLMSSP\x00"

const (
	msgNegotiate    = 1
	msgChallenge    = 2
	msgAuthenticate = 3
)

// Aushandlungsmerkmale (MS-NLMP 2.2.2.5).
const (
	negUnicode         = 0x00000001
	negOEM             = 0x00000002
	negRequestTarget   = 0x00000004
	negNTLM            = 0x00000200
	negAnonymous       = 0x00000800
	negAlwaysSign      = 0x00008000
	negExtendedSession = 0x00080000
	negTargetInfo      = 0x00800000
	negVersion         = 0x02000000
	neg128             = 0x20000000
	neg56              = 0x80000000
)

// ntlmNegotiate baut die erste Nachricht.
func ntlmNegotiate() []byte {
	b := make([]byte, 32)
	copy(b, ntlmsspSignature)
	binary.LittleEndian.PutUint32(b[8:], msgNegotiate)
	flags := uint32(negUnicode | negRequestTarget | negNTLM | negAlwaysSign |
		negExtendedSession | negTargetInfo | neg128 | neg56)
	binary.LittleEndian.PutUint32(b[12:], flags)
	// Domäne und Arbeitsstation bleiben leer: Längen und Offsets auf null.
	return b
}

// ntlmChallenge sind die Angaben, die der Server in Runde zwei liefert.
type ntlmChallenge struct {
	challenge  []byte
	targetInfo []byte
	flags      uint32
	targetName string
}

// parseChallenge zerlegt die Antwort des Servers.
func parseChallenge(b []byte) (*ntlmChallenge, error) {
	if len(b) < 48 {
		return nil, errors.New("smb1: NTLMSSP-Challenge zu kurz")
	}
	if string(b[:8]) != ntlmsspSignature {
		return nil, errors.New("smb1: keine NTLMSSP-Nachricht")
	}
	if t := binary.LittleEndian.Uint32(b[8:]); t != msgChallenge {
		return nil, fmt.Errorf("smb1: erwartete Challenge, erhielt Nachrichtentyp %d", t)
	}

	feld := func(off int) ([]byte, error) {
		length := int(binary.LittleEndian.Uint16(b[off:]))
		start := int(binary.LittleEndian.Uint32(b[off+4:]))
		if length == 0 {
			return nil, nil
		}
		if start < 0 || start+length > len(b) {
			return nil, fmt.Errorf("smb1: Feld bei Offset %d liegt außerhalb der Nachricht", off)
		}
		return b[start : start+length], nil
	}

	targetName, err := feld(12)
	if err != nil {
		return nil, err
	}
	targetInfo, err := feld(40)
	if err != nil {
		return nil, err
	}

	c := &ntlmChallenge{
		challenge:  append([]byte(nil), b[24:32]...),
		flags:      binary.LittleEndian.Uint32(b[20:]),
		targetInfo: append([]byte(nil), targetInfo...),
	}
	if targetName != nil {
		c.targetName = decodeUTF16(targetName)
	}
	return c, nil
}

// ntlmAuthenticate baut die dritte Nachricht mit der NTLMv2-Antwort.
func ntlmAuthenticate(ch *ntlmChallenge, user, password, domain, workstation string) ([]byte, error) {
	anonymous := user == ""

	var lm, nt []byte
	if anonymous {
		// Anonyme Anmeldung: ein einzelnes Nullbyte als NT-Antwort.
		lm = []byte{0}
		nt = nil
	} else {
		cc, err := newClientChallenge()
		if err != nil {
			return nil, err
		}
		key := ntowfv2(user, password, domain)
		nt = ntlmv2Response(key, ch.challenge, cc, ch.targetInfo, time.Now())
		lm = lmv2Response(key, ch.challenge, cc)
	}

	userB := utf16leNoNull(user)
	domainB := utf16leNoNull(domain)
	wsB := utf16leNoNull(workstation)

	// Kopf ist 64 Byte, danach folgen die veränderlichen Teile.
	const headLen = 64
	off := headLen
	type feld struct{ off, length int }
	place := func(data []byte) feld {
		f := feld{off: off, length: len(data)}
		off += len(data)
		return f
	}
	fLM := place(lm)
	fNT := place(nt)
	fDomain := place(domainB)
	fUser := place(userB)
	fWS := place(wsB)
	fKey := place(nil)

	b := make([]byte, off)
	copy(b, ntlmsspSignature)
	binary.LittleEndian.PutUint32(b[8:], msgAuthenticate)

	setFeld := func(at int, f feld) {
		binary.LittleEndian.PutUint16(b[at:], uint16(f.length))
		binary.LittleEndian.PutUint16(b[at+2:], uint16(f.length))
		binary.LittleEndian.PutUint32(b[at+4:], uint32(f.off))
	}
	setFeld(12, fLM)
	setFeld(20, fNT)
	setFeld(28, fDomain)
	setFeld(36, fUser)
	setFeld(44, fWS)
	setFeld(52, fKey)

	flags := uint32(negUnicode | negRequestTarget | negNTLM | negAlwaysSign |
		negExtendedSession | negTargetInfo | neg128 | neg56)
	if anonymous {
		flags |= negAnonymous
	}
	binary.LittleEndian.PutUint32(b[60:], flags)

	copy(b[fLM.off:], lm)
	copy(b[fNT.off:], nt)
	copy(b[fDomain.off:], domainB)
	copy(b[fUser.off:], userB)
	copy(b[fWS.off:], wsB)
	return b, nil
}
