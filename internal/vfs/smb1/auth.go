package smb1

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"

	"golang.org/x/crypto/md4"
)

// NTLMv2 für die klassische Anmeldung ohne erweiterte Sicherheit.
//
// SMB1 kennt zwei Wege, sich anzumelden: den alten, bei dem Antwort-Blobs
// direkt im Session-Setup stehen, und den neueren über SPNEGO/NTLMSSP. Wir
// nehmen den alten - er kommt ohne ASN.1 aus und ist genau der, den betagte
// Geräte beherrschen.
//
// Bewusst NICHT umgesetzt sind LM und NTLMv1: beide gelten als gebrochen.
// Wenn eine Gegenstelle nur die akzeptiert, soll die Anmeldung scheitern und
// nicht stillschweigend auf etwas Unsicheres ausweichen.

// ntowfv2 bildet den NTLMv2-Schlüssel aus Passwort, Benutzer und Domäne.
func ntowfv2(user, password, domain string) []byte {
	// MD4 über das Passwort in UTF-16LE ist der klassische NT-Hash.
	nt := md4.New()
	nt.Write(utf16leNoNull(password))
	ntHash := nt.Sum(nil)

	mac := hmac.New(md5.New, ntHash)
	mac.Write(utf16leNoNull(strings.ToUpper(user) + domain))
	return mac.Sum(nil)
}

// ntlmv2Response baut die NT-Antwort samt Blob.
func ntlmv2Response(key, serverChallenge, clientChallenge, targetInfo []byte, now time.Time) []byte {
	blob := make([]byte, 0, 32+len(targetInfo))
	blob = append(blob, 0x01, 0x01)       // Version der Antwortstruktur
	blob = append(blob, 0, 0, 0, 0, 0, 0) // reserviert
	ts := make([]byte, 8)                 // Zeitstempel im Windows-Format
	binary.LittleEndian.PutUint64(ts, uint64(now.UnixNano()/100)+116444736000000000)
	blob = append(blob, ts...)
	blob = append(blob, clientChallenge...)
	blob = append(blob, 0, 0, 0, 0) // reserviert
	blob = append(blob, targetInfo...)
	blob = append(blob, 0, 0, 0, 0) // reserviert

	mac := hmac.New(md5.New, key)
	mac.Write(serverChallenge)
	mac.Write(blob)
	proof := mac.Sum(nil)

	return append(proof, blob...)
}

// lmv2Response ist die LM-Antwort im NTLMv2-Verfahren (24 Byte).
func lmv2Response(key, serverChallenge, clientChallenge []byte) []byte {
	mac := hmac.New(md5.New, key)
	mac.Write(serverChallenge)
	mac.Write(clientChallenge)
	return append(mac.Sum(nil), clientChallenge...)
}

// newClientChallenge erzeugt die 8 Zufallsbytes des Clients.
func newClientChallenge() ([]byte, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// utf16leNoNull kodiert nach UTF-16LE ohne abschließendes Nullzeichen.
func utf16leNoNull(s string) []byte {
	b := utf16le(s)
	if len(b) >= 2 {
		return b[:len(b)-2]
	}
	return nil
}
