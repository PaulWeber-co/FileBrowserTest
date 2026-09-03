package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// loadOrCreateCert nimmt das konfigurierte Zertifikat oder erzeugt ein
// selbstsigniertes.
//
// Selbstsigniert reicht im Heimnetz völlig: die Verbindung ist verschlüsselt,
// der Browser zeigt beim ersten Mal eine Warnung. Wer den Zugriff aus dem
// Internet öffnet, sollte ein echtes Zertifikat hinterlegen.
func (a *App) loadOrCreateCert() (tls.Certificate, error) {
	t := a.cfg.Server.TLS
	if t.CertFile != "" && t.KeyFile != "" {
		return tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
	}
	if !t.SelfSign {
		return tls.Certificate{}, fmt.Errorf("TLS ist aktiv, aber weder Zertifikat noch selfSigned gesetzt")
	}
	dir := filepath.Join(a.cfg.Server.DataDir, "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
			if time.Now().Before(leaf.NotAfter.Add(-24 * time.Hour)) {
				return c, nil
			}
		}
	}

	log.Printf("Erzeuge selbstsigniertes Zertifikat in %s", dir)
	certPEM, keyPEM, err := generateSelfSigned(t.Hosts)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func generateSelfSigned(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SpeedNAS", Organization: []string{"SpeedNAS"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	tmpl.DNSNames = append(tmpl.DNSNames, "localhost")
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"), net.ParseIP("::1"))
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	// Die eigenen Netzwerkadressen mit aufnehmen, damit der Zugriff per IP
	// vom Handy aus nicht sofort eine zusätzliche Warnung auslöst.
	for _, ip := range localIPs() {
		if p := net.ParseIP(ip); p != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, p)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
