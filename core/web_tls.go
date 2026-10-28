package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The workspace is always HTTPS. With cert_file and key_file it serves
// those (an own CA's, or `tailscale cert`'s). Without them it makes a
// self-signed certificate at first start and keeps it next to the database,
// making a new one when it nears its end or misses a name.

func (w *web) certificate(cfg *Config) (tls.Certificate, error) {
	if w.cfg.CertFile != "" || w.cfg.KeyFile != "" {
		return tls.LoadX509KeyPair(w.cfg.CertFile, w.cfg.KeyFile)
	}
	dir := filepath.Dir(cfg.DB)
	certFile, keyFile := filepath.Join(dir, "web-cert.pem"), filepath.Join(dir, "web-key.pem")
	names := certNames(w.cfg.Hostnames, cfg.PublicURL)
	// Only the configured names decide whether to renew: the machine's own
	// name and addresses change each time a container is recreated, and a new
	// certificate would make every browser refuse the workspace again.
	if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil && certCovers(cert, requiredNames(w.cfg.Hostnames, cfg.PublicURL), w.now()) {
		logFingerprint(cert, false)
		return cert, nil
	}
	cert, err := selfSigned(certFile, keyFile, names, w.now())
	if err != nil {
		return tls.Certificate{}, err
	}
	logFingerprint(cert, true)
	return cert, nil
}

// requiredNames are the names a kept certificate must still cover.
func requiredNames(extra []string, publicURL string) []string {
	names := append([]string{}, extra...)
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		names = append(names, u.Hostname())
	}
	return append(names, "localhost")
}

// certNames are the names and addresses the certificate is for: the
// configured ones, public_url's host, this machine's name, localhost, and
// the addresses of its interfaces.
func certNames(extra []string, publicURL string) []string {
	names := append([]string{}, extra...)
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		names = append(names, u.Hostname())
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, h)
	}
	names = append(names, "localhost", "127.0.0.1", "::1")
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				names = append(names, ipn.IP.String())
			}
		}
	}
	var out []string
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func certCovers(cert tls.Certificate, names []string, now time.Time) bool {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || now.Add(30*24*time.Hour).After(leaf.NotAfter) {
		return false
	}
	for _, n := range names {
		if leaf.VerifyHostname(n) != nil {
			return false
		}
	}
	return true
}

func selfSigned(certFile, keyFile string, names []string, now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Eriphe", Organization: []string{"Eriphe"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(397 * 24 * time.Hour), // what browsers accept for a server certificate
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func fingerprint(cert tls.Certificate) string {
	if len(cert.Certificate) == 0 {
		return ""
	}
	sum := sha256.Sum256(cert.Certificate[0])
	hexs := make([]string, len(sum))
	for i, b := range sum {
		hexs[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(hexs, ":")
}

func logFingerprint(cert tls.Certificate, created bool) {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return
	}
	what := "self-signed certificate"
	if created {
		what = "new " + what
	}
	ips := make([]string, len(leaf.IPAddresses))
	for i, ip := range leaf.IPAddresses {
		ips[i] = ip.String()
	}
	log.Printf("web: %s for %s, until %s, SHA-256 fingerprint %s", what,
		strings.Join(append(leaf.DNSNames, ips...), ", "), leaf.NotAfter.Format("2006-01-02"), fingerprint(cert))
}
