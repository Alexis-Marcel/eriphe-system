package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The node talks to eriphe-core over TLS once it knows eriphe-core's
// certificate authority for machines, with a client certificate of its own
// (mTLS), like a kubelet:
//
//   - Joining with -ca-hash sha256:<hex>, the node finds in eriphe-core's
//     TLS handshake the CA whose public key has that hash, and pins it.
//     Without a hash, an https:// address must have a publicly trusted
//     certificate (eriphe-core's tailnet one); plain http:// still joins.
//   - A node without a certificate (one that joined over plain HTTP, or
//     before TLS) asks for one with its secret (POST /v1/node/certificate),
//     and pins the CA that comes with it. From then on it reaches every
//     http:// address of eriphe-core as https://, on the same port.
//   - It asks for a new one with its current one when a third of its life
//     is left.
//
// node.json keeps the CA, the certificate and its key; its addresses stay
// as given, so an older version still runs on plain HTTP with it.

// coreServerName is the name in eriphe-core's certificate for the nodes,
// checked whatever the address (an IP, a container's name).
const coreServerName = "eriphe-core"

var caHashRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// parseCAHash checks a -ca-hash value.
func parseCAHash(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s != "" && !caHashRe.MatchString(s) {
		return "", fmt.Errorf("-ca-hash %q: sha256:<64 hex digits> expected, as eriphe-core join-token prints it", s)
	}
	return s, nil
}

func caHash(cert *x509.Certificate) string {
	h := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(h[:])
}

// tlsState is what the node knows for TLS; state embeds it.
type tlsState struct {
	CA   string `json:"ca,omitempty"`   // eriphe-core's CA for machines, PEM
	Cert string `json:"cert,omitempty"` // this node's client certificate, PEM
	Key  string `json:"key,omitempty"`  // its private key, PEM

	path    string     // node.json, where a new certificate is saved; empty: none is asked for
	certify sync.Mutex // one request for a certificate at a time

	tlsMu   sync.Mutex
	pin     string // -ca-hash, until the CA is known
	pool    *x509.CertPool
	caCert  *x509.Certificate
	client  *tls.Certificate
	saidOld bool // the node said eriphe-core gives no certificates

	clients sync.Once
	dial    *http.Client // join, connect, reachable: gives up quickly
	long    *http.Client // assignments and reports: long polls
	relay   http.RoundTripper
}

// loadTLS parses what node.json holds; pin is -ca-hash, if given.
func (t *tlsState) loadTLS(pin string) error {
	t.tlsMu.Lock()
	defer t.tlsMu.Unlock()
	t.pin = pin
	if t.CA == "" {
		return nil
	}
	ca, err := parseCert(t.CA)
	if err != nil {
		return fmt.Errorf("node.json: ca: %w", err)
	}
	if pin != "" && caHash(ca) != pin {
		return fmt.Errorf("-ca-hash %s is not the certificate authority this node trusts (%s): delete node.json and join again to change it", pin, caHash(ca))
	}
	t.setCALocked(ca)
	if t.Cert != "" {
		pair, err := tls.X509KeyPair([]byte(t.Cert), []byte(t.Key))
		if err != nil {
			log.Printf("tls: node.json: certificate: %v; asking for a new one", err)
			t.Cert, t.Key = "", ""
			return nil
		}
		t.client = &pair
	}
	return nil
}

func (t *tlsState) setCALocked(ca *x509.Certificate) {
	t.caCert = ca
	t.pool = x509.NewCertPool()
	t.pool.AddCert(ca)
	t.CA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}))
}

// secure says whether the node reaches eriphe-core over TLS only: it knows
// its CA, or was given its hash.
func (t *tlsState) secure() bool {
	t.tlsMu.Lock()
	defer t.tlsMu.Unlock()
	return t.pool != nil || t.pin != ""
}

// certificate is the node's client certificate, nil without one.
func (t *tlsState) certificate() *tls.Certificate {
	t.tlsMu.Lock()
	defer t.tlsMu.Unlock()
	return t.client
}

// verifyCore checks eriphe-core's certificate: signed by the CA the node
// pinned for eriphe-core's name, or found in the chain by -ca-hash (then
// pinned), or else publicly trusted for the address dialed.
func (t *tlsState) verifyCore(cs tls.ConnectionState) error {
	certs := cs.PeerCertificates
	if len(certs) == 0 {
		return errors.New("eriphe-core sent no certificate")
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	t.tlsMu.Lock()
	pool, pin := t.pool, t.pin
	t.tlsMu.Unlock()
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if pool == nil && pin != "" {
		for _, c := range certs[1:] {
			if c.IsCA && caHash(c) == pin {
				p := x509.NewCertPool()
				p.AddCert(c)
				if _, err := certs[0].Verify(x509.VerifyOptions{Roots: p, DNSName: coreServerName, KeyUsages: usage}); err == nil {
					t.tlsMu.Lock()
					if t.pool == nil {
						t.setCALocked(c)
					}
					t.tlsMu.Unlock()
					return nil
				}
			}
		}
	}
	if pool != nil {
		if _, err := certs[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, DNSName: coreServerName, KeyUsages: usage}); err == nil {
			return nil
		}
	}
	// eriphe-core's tailnet address has a public certificate. An IP or an
	// address without a name cannot have one.
	if cs.ServerName == "" {
		return errUntrustedCore
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{Intermediates: inter, DNSName: cs.ServerName, KeyUsages: usage}); err != nil {
		return fmt.Errorf("%w: %v", errUntrustedCore, err)
	}
	return nil
}

var errUntrustedCore = errors.New("eriphe-core's certificate is not signed by the certificate authority this node trusts: join with -ca-hash, as eriphe-core join-token prints it")

func (t *tlsState) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Checked by verifyCore instead: the CA, then public roots.
		InsecureSkipVerify: true,
		VerifyConnection:   t.verifyCore,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			if c := t.certificate(); c != nil {
				return c, nil
			}
			return &tls.Certificate{}, nil
		},
	}
}

func (t *tlsState) newTransport(responseHeader time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeader,
		TLSClientConfig:       t.tlsConfig(),
		IdleConnTimeout:       90 * time.Second,
	}
}

func (t *tlsState) initClients() {
	t.clients.Do(func() {
		// The dial client gives up quickly on an address that does not
		// answer (the local one, away from home), so the next one is tried.
		t.dial = &http.Client{Transport: t.newTransport(20 * time.Second)}
		t.relay = t.newTransport(0)
		t.long = &http.Client{Timeout: 90 * time.Second, Transport: t.relay}
	})
}

func (t *tlsState) dialClient() *http.Client          { t.initClients(); return t.dial }
func (t *tlsState) longClient() *http.Client          { t.initClients(); return t.long }
func (t *tlsState) relayTransport() http.RoundTripper { t.initClients(); return t.relay }

// secureURL is the address to dial: https:// instead of http:// once the
// node reaches eriphe-core over TLS (eriphe-core serves both on its port).
func secureURL(u string) string {
	if rest, ok := strings.CutPrefix(u, "http://"); ok {
		return "https://" + rest
	}
	return u
}

// certifyCheck is how often the node checks whether its certificate needs
// renewing.
var certifyCheck = time.Hour

// renewLoop keeps the node's certificate current.
func (s *state) renewLoop() {
	for {
		time.Sleep(certifyCheck)
		s.certifyNow()
	}
}

// needsCert says whether to ask for a certificate: none yet, or a third of
// its life left, or ended (then dropped, the secret alone asks).
func (t *tlsState) needsCert(now time.Time) bool {
	t.tlsMu.Lock()
	defer t.tlsMu.Unlock()
	if t.client == nil || t.client.Leaf == nil {
		return true
	}
	leaf := t.client.Leaf
	if !now.Before(leaf.NotAfter) {
		log.Printf("tls: this node's certificate ended on %s; asking for a new one with its secret", leaf.NotAfter.Format(time.DateOnly))
		t.client, t.Cert, t.Key = nil, "", ""
		return true
	}
	return leaf.NotAfter.Sub(now) < leaf.NotAfter.Sub(leaf.NotBefore)/3
}

// errNoCertificates is an eriphe-core that predates TLS for machines.
var errNoCertificates = errors.New("eriphe-core gives no certificates (an older version): staying on plain HTTP")

// getCert gets the node its first certificate, or a new one, from the first
// of eriphe-core's addresses that answers, and saves it in node.json.
func (s *state) getCert() error {
	s.certify.Lock()
	defer s.certify.Unlock()
	if s.path == "" || !s.needsCert(time.Now()) {
		return nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: s.Name}}, key)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]string{"csr": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))})
	var out struct {
		CA          string `json:"ca"`
		Certificate string `json:"certificate"`
	}
	err = eachCore(s.urls(), func(u string) error {
		req, err := http.NewRequest(http.MethodPost, u+"/v1/node/certificate", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+s.Secret)
		req.Header.Set("Eriphe-Node", s.Name)
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.dialClient().Do(req)
		if err != nil {
			return &unreachableError{u, err}
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
			return errNoCertificates
		case resp.StatusCode != http.StatusOK:
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			return fmt.Errorf("certificate: %s: %s", resp.Status, bytes.TrimSpace(msg))
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out)
	})
	if err != nil {
		return err
	}
	return s.keepCert(out.CA, out.Certificate, key)
}

// keepCert checks what eriphe-core answered and keeps it: the CA must be
// the one the node pinned (or have the hash it was given), the certificate
// this node's, signed by it, for the key it holds.
func (s *state) keepCert(caPEM, certPEM string, key *ecdsa.PrivateKey) error {
	ca, err := parseCert(caPEM)
	if err != nil {
		return fmt.Errorf("certificate: the CA: %w", err)
	}
	leaf, err := parseCert(certPEM)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	s.tlsMu.Lock()
	known, pin := s.caCert, s.pin
	s.tlsMu.Unlock()
	switch {
	case known != nil && !known.Equal(ca):
		return errors.New("certificate: eriphe-core answered another certificate authority than the one this node trusts")
	case known == nil && pin != "" && caHash(ca) != pin:
		return fmt.Errorf("certificate: eriphe-core's certificate authority (%s) is not -ca-hash %s", caHash(ca), pin)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	if pub, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) || leaf.Subject.CommonName != s.Name {
		return errors.New("certificate: not this node's")
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair([]byte(certPEM), keyPEM)
	if err != nil {
		return err
	}
	s.tlsMu.Lock()
	first := s.caCert == nil
	if first {
		s.setCALocked(ca)
	}
	s.Cert, s.Key, s.client = certPEM, string(keyPEM), &pair
	s.tlsMu.Unlock()
	// Kept-alive connections were opened with the certificate before (or
	// none): new ones present this one.
	s.dial.CloseIdleConnections()
	s.long.CloseIdleConnections()
	if err := s.save(s.path); err != nil {
		return err
	}
	if first {
		log.Printf("tls: eriphe-core's certificate authority %s pinned; TLS to eriphe-core from now on", caHash(ca))
	}
	log.Printf("tls: certificate of this node until %s", leaf.NotAfter.Format(time.DateOnly))
	return nil
}

func parseCert(p string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// certifyNow is getCert, logged: an error never keeps the node from
// connecting as it can.
func (s *state) certifyNow() {
	err := s.getCert()
	switch {
	case err == nil:
	case errors.Is(err, errNoCertificates):
		s.tlsMu.Lock()
		said := s.saidOld
		s.saidOld = true
		s.tlsMu.Unlock()
		if !said {
			log.Printf("tls: %v", err)
		}
	default:
		log.Printf("tls: %v", err)
	}
}
