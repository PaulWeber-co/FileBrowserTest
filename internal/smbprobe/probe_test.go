package smbprobe

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// fakeServer beantwortet genau eine Aushandlung und liefert die empfangene
// Anfrage zur Prüfung zurück.
func fakeServer(t *testing.T, reply func(req []byte) []byte) (addr string, got chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan []byte, 4)
	go func() {
		defer ln.Close()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				var h [4]byte
				if _, err := io.ReadFull(c, h[:]); err != nil {
					return
				}
				n := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
				buf := make([]byte, n)
				if _, err := io.ReadFull(c, buf); err != nil {
					return
				}
				select {
				case got <- buf:
				default:
				}
				if r := reply(buf); r != nil {
					hdr := []byte{0, byte(len(r) >> 16), byte(len(r) >> 8), byte(len(r))}
					_, _ = c.Write(append(hdr, r...))
				}
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), got
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	for _, c := range p {
		port = port*10 + int(c-'0')
	}
	return h, port
}

// smb2Response baut eine gültige NEGOTIATE-Antwort nach MS-SMB2 2.2.4.
func smb2Response(dialect, secMode uint16, maxRead, maxWrite, maxTrans uint32) []byte {
	msg := make([]byte, 64+64)
	copy(msg, []byte{0xFE, 'S', 'M', 'B'})
	binary.LittleEndian.PutUint16(msg[4:], 64)
	binary.LittleEndian.PutUint16(msg[12:], 0) // NEGOTIATE
	binary.LittleEndian.PutUint32(msg[8:], 0)  // STATUS_SUCCESS

	b := msg[64:]
	binary.LittleEndian.PutUint16(b[0:], 65)
	binary.LittleEndian.PutUint16(b[2:], secMode)
	binary.LittleEndian.PutUint16(b[4:], dialect)
	binary.LittleEndian.PutUint32(b[28:], maxTrans)
	binary.LittleEndian.PutUint32(b[32:], maxRead)
	binary.LittleEndian.PutUint32(b[36:], maxWrite)
	return msg
}

func TestProbeErkenntSMB311(t *testing.T) {
	addr, reqs := fakeServer(t, func(req []byte) []byte {
		if len(req) > 4 && req[0] == 0xFE {
			// signingEnabled | signingRequired
			return smb2Response(0x0311, 0x0003, 8388608, 8388608, 8388608)
		}
		return nil // SMB1 nicht beantworten = deaktiviert
	})
	host, port := splitHostPort(t, addr)

	res := Probe(context.Background(), host, port, 3*time.Second)
	if !res.Reachable {
		t.Fatalf("nicht erreichbar: %s", res.DialError)
	}
	if !res.SMB2 || res.Dialect != 0x0311 {
		t.Fatalf("SMB2 nicht erkannt: %+v", res)
	}
	if res.DialectName != "SMB 3.1.1" {
		t.Errorf("Dialektname: %q", res.DialectName)
	}
	if !res.SigningOn || !res.SigningForce {
		t.Errorf("Signierung falsch gelesen: an=%v erzwungen=%v", res.SigningOn, res.SigningForce)
	}
	if res.MaxReadSize != 8388608 || res.MaxWriteSize != 8388608 {
		t.Errorf("Blockgrößen falsch: read=%d write=%d", res.MaxReadSize, res.MaxWriteSize)
	}
	if res.SMB1 {
		t.Error("SMB1 fälschlich gemeldet")
	}

	// Die gesendete Anfrage muss ein gültiges SMB2-NEGOTIATE sein.
	select {
	case req := <-reqs:
		if len(req) < 100 || req[0] != 0xFE || string(req[1:4]) != "SMB" {
			t.Fatalf("kein SMB2-Kopf gesendet: % x", req[:8])
		}
		if cmd := binary.LittleEndian.Uint16(req[12:]); cmd != 0 {
			t.Errorf("Kommando %d, erwartet 0 (NEGOTIATE)", cmd)
		}
		body := req[64:]
		if sz := binary.LittleEndian.Uint16(body[0:]); sz != 36 {
			t.Errorf("StructureSize %d, erwartet 36", sz)
		}
		count := binary.LittleEndian.Uint16(body[2:])
		if int(count) != len(smb2Dialects) {
			t.Errorf("Dialektanzahl %d, erwartet %d", count, len(smb2Dialects))
		}
		// Für 3.1.1 ist ein Preauth-Kontext Pflicht.
		ctxOff := binary.LittleEndian.Uint32(body[28:])
		ctxCount := binary.LittleEndian.Uint16(body[32:])
		if ctxCount != 1 || ctxOff%8 != 0 || int(ctxOff) >= len(req) {
			t.Errorf("Kontext fehlt oder ist falsch ausgerichtet: off=%d count=%d", ctxOff, ctxCount)
		}
		if ct := binary.LittleEndian.Uint16(req[ctxOff:]); ct != 0x0001 {
			t.Errorf("Kontexttyp 0x%04x, erwartet 0x0001", ct)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("keine Anfrage empfangen")
	}
}

func TestProbeErkenntNurSMB1(t *testing.T) {
	addr, _ := fakeServer(t, func(req []byte) []byte {
		if len(req) > 4 && req[0] == 0xFE {
			return nil // SMB2 abweisen
		}
		if len(req) > 4 && req[0] == 0xFF && string(req[1:4]) == "SMB" {
			// SMB1-Antwort mit DialectIndex 0 = "NT LM 0.12"
			msg := make([]byte, 32+1+2+35)
			copy(msg, []byte{0xFF, 'S', 'M', 'B'})
			msg[4] = 0x72
			msg[32] = 17 // WordCount
			binary.LittleEndian.PutUint16(msg[33:], 0)
			return msg
		}
		return nil
	})
	host, port := splitHostPort(t, addr)

	res := Probe(context.Background(), host, port, 3*time.Second)
	if !res.SMB1 {
		t.Fatalf("SMB1 nicht erkannt: %+v", res)
	}
	if res.SMB1Dialect != "NT LM 0.12" {
		t.Errorf("Dialekt: %q", res.SMB1Dialect)
	}
	if res.SMB2 {
		t.Error("SMB2 fälschlich gemeldet")
	}
}

func TestProbeUnerreichbar(t *testing.T) {
	// Port 1 ist praktisch garantiert zu.
	res := Probe(context.Background(), "127.0.0.1", 1, 500*time.Millisecond)
	if res.Reachable {
		t.Error("Port 1 als erreichbar gemeldet")
	}
	if res.DialError == "" {
		t.Error("keine Fehlermeldung")
	}
}

func TestProbeLehntMuellAb(t *testing.T) {
	addr, _ := fakeServer(t, func(req []byte) []byte {
		return []byte("das ist kein SMB")
	})
	host, port := splitHostPort(t, addr)
	res := Probe(context.Background(), host, port, 2*time.Second)
	if !res.Reachable {
		t.Error("TCP-Verbindung sollte stehen")
	}
	if res.SMB1 || res.SMB2 {
		t.Errorf("Müll als SMB erkannt: %+v", res)
	}
	if res.SMB2Error == "" {
		t.Error("keine Fehlerbeschreibung für SMB2")
	}
}

func TestDialectName(t *testing.T) {
	cases := map[uint16]string{
		0x0202: "SMB 2.0.2", 0x0210: "SMB 2.1", 0x0300: "SMB 3.0",
		0x0302: "SMB 3.0.2", 0x0311: "SMB 3.1.1",
	}
	for code, want := range cases {
		if got := dialectName(code); got != want {
			t.Errorf("dialectName(0x%04x) = %q, erwartet %q", code, got, want)
		}
	}
	if got := dialectName(0x1234); got == "" {
		t.Error("unbekannter Dialekt liefert keinen Text")
	}
}
