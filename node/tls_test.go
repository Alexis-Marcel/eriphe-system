package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// fakeCore answers like eriphe-core with its CA for machines: TLS and plain
// HTTP on one port, joins, certificates and the tunnel. It records how
// each request came.
type fakeCore struct {
	t        *testing.T
	ca       *x509.Certificate
	caKey    *ecdsa.PrivateKey
	server   tls.Certificate
	url      string // http://127.0.0.1:port
	lifetime time.Duration
	noCerts  bool // an eriphe-core before TLS for machines

	mu   sync.Mutex
	seen []seenRequest
}

type seenRequest struct {
	path string
	tls  bool
	cn   string // the client certificate's
}

func newCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return ca, key
}

var serial int64 = 1

func sign(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, tmpl *x509.Certificate, pub any) *x509.Certificate {
	serial++
	tmpl.SerialNumber = big.NewInt(serial)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func startFakeCore(t *testing.T) *fakeCore {
	f := &fakeCore{t: t, lifetime: 24 * time.Hour}
	f.ca, f.caKey = newCA(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := sign(t, f.ca, f.caKey, &x509.Certificate{Subject: pkix.Name{CommonName: coreServerName}, DNSNames: []string{coreServerName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, &key.PublicKey)
	f.server = tls.Certificate{Certificate: [][]byte{leaf.Raw, f.ca.Raw}, PrivateKey: key}
	pool := x509.NewCertPool()
	pool.AddCert(f.ca)
	cfg := &tls.Config{Certificates: []tls.Certificate{f.server}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.url = "http://" + ln.Addr().String()
	conns := make(chan net.Conn)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(conns)
				return
			}
			go func() {
				r := bufio.NewReader(c)
				b, err := r.Peek(1)
				if err != nil {
					c.Close()
					return
				}
				var conn net.Conn = &peeked{Conn: c, r: r}
				if b[0] == 0x16 {
					conn = tls.Server(conn, cfg)
				}
				conns <- conn
			}()
		}
	}()
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go srv.Serve(&chanListener{Listener: ln, conns: conns})
	t.Cleanup(func() { srv.Close(); ln.Close() })
	return f
}

type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }

type chanListener struct {
	net.Listener
	conns chan net.Conn
}

func (l *chanListener) Accept() (net.Conn, error) {
	c, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (f *fakeCore) serve(w http.ResponseWriter, r *http.Request) {
	seen := seenRequest{path: r.URL.Path, tls: r.TLS != nil}
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		seen.cn = r.TLS.PeerCertificates[0].Subject.CommonName
	}
	f.mu.Lock()
	f.seen = append(f.seen, seen)
	f.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/nodes/join":
		json.NewEncoder(w).Encode(map[string]string{"secret": "s3cret"})
	case r.URL.Path == "/v1/node/certificate" && !f.noCerts:
		var in struct{ CSR string }
		json.NewDecoder(r.Body).Decode(&in)
		block, _ := pem.Decode([]byte(in.CSR))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			http.Error(w, "bad csr", http.StatusBadRequest)
			return
		}
		cert := sign(f.t, f.ca, f.caKey, &x509.Certificate{Subject: pkix.Name{CommonName: r.Header.Get("Eriphe-Node")},
			NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(f.lifetime),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, csr.PublicKey)
		json.NewEncoder(w).Encode(map[string]string{
			"ca":          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.ca.Raw})),
			"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		})
	case r.URL.Path == "/v1/nodes/connect":
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + tunnelProto + "\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		session, err := yamux.Client(conn, nil)
		if err != nil {
			conn.Close()
			return
		}
		// The test is done with the tunnel once it is up.
		session.Close()
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCore) requests(path string) []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seenRequest
	for _, s := range f.seen {
		if s.path == path {
			out = append(out, s)
		}
	}
	return out
}

// A node given -ca-hash joins over TLS on its http:// address, checks
// eriphe-core's CA by its hash, then gets its certificate and connects with
// it. node.json keeps the address as given, the CA and the certificate.
func TestJoinWithCAHash(t *testing.T) {
	f := startFakeCore(t)
	path := filepath.Join(t.TempDir(), "node.json")
	s, err := join(path, []string{f.url}, "pc", "tok", caHash(f.ca))
	if err != nil {
		t.Fatal(err)
	}
	if j := f.requests("/v1/nodes/join"); len(j) != 1 || !j[0].tls {
		t.Fatalf("join: %+v", j)
	}
	if err := connect(s, http.NotFoundHandler()); err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("connect: %v", err)
	}
	if c := f.requests("/v1/node/certificate"); len(c) != 1 || !c[0].tls {
		t.Fatalf("certificate: %+v", c)
	}
	if c := f.requests("/v1/nodes/connect"); len(c) != 1 || !c[0].tls || c[0].cn != "pc" {
		t.Fatalf("connect: %+v", c)
	}

	raw, _ := os.ReadFile(path)
	var saved map[string]any
	json.Unmarshal(raw, &saved)
	if saved["core"] != f.url || saved["secret"] != "s3cret" || saved["ca"] == nil || saved["cert"] == nil || saved["key"] == nil {
		t.Fatalf("node.json: %s", raw)
	}
	again, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.loadTLS(""); err != nil || again.certificate() == nil || again.urls()[0] != secureURL(f.url) {
		t.Fatalf("reloaded: %v, %v", err, again.urls())
	}
	other, _ := newCA(t)
	if err := again.loadTLS(caHash(other)); err == nil {
		t.Fatal("another -ca-hash than the pinned CA was accepted")
	}
}

// A wrong hash fails the handshake: the join token never leaves the node.
// An https:// address with eriphe-core's own certificate and no hash is
// refused too.
func TestJoinWrongCAHash(t *testing.T) {
	f := startFakeCore(t)
	other, _ := newCA(t)
	if _, err := join(filepath.Join(t.TempDir(), "node.json"), []string{f.url}, "pc", "tok", caHash(other)); err == nil {
		t.Fatal("joined with a wrong hash")
	}
	if _, err := join(filepath.Join(t.TempDir(), "node.json"), []string{secureURL(f.url)}, "pc", "tok", ""); !errors.Is(err, errUntrustedCore) {
		t.Fatalf("https without a hash: %v", err)
	}
	if j := f.requests("/v1/nodes/join"); len(j) != 0 {
		t.Fatalf("the token was sent: %+v", j)
	}
	if _, err := parseCAHash("sha256:abc"); err == nil {
		t.Fatal("short hash accepted")
	}
}

// A node that joined over plain HTTP before TLS (its node.json has only
// its secret) gets the CA and its certificate with its secret, then
// connects over TLS on the same port.
func TestOldNodeMigrates(t *testing.T) {
	f := startFakeCore(t)
	path := filepath.Join(t.TempDir(), "node.json")
	os.WriteFile(path, []byte(`{"core":"`+f.url+`","name":"nas","secret":"s3cret"}`), 0o600)
	s, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	s.path = path
	if err := s.loadTLS(""); err != nil {
		t.Fatal(err)
	}
	connect(s, http.NotFoundHandler())
	if c := f.requests("/v1/node/certificate"); len(c) != 1 || c[0].tls {
		t.Fatalf("certificate over plain HTTP with the secret: %+v", c)
	}
	if c := f.requests("/v1/nodes/connect"); len(c) != 1 || !c[0].tls || c[0].cn != "nas" {
		t.Fatalf("tunnel: %+v", c)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"ca"`) || !strings.Contains(string(raw), `"core": "`+f.url+`"`) {
		t.Fatalf("node.json: %s", raw)
	}
	// The relay and the assignments go over TLS with the certificate too.
	resp, err := s.longClient().Get(s.urls()[0] + "/v1/node/assignments")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if c := f.requests("/v1/node/assignments"); len(c) != 1 || c[0].cn != "nas" {
		t.Fatalf("assignments: %+v", c)
	}
}

// An eriphe-core that gives no certificates leaves the node on plain HTTP.
func TestOldCoreStaysPlain(t *testing.T) {
	f := startFakeCore(t)
	f.noCerts = true
	path := filepath.Join(t.TempDir(), "node.json")
	s, err := join(path, []string{f.url}, "pc", "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	connect(s, http.NotFoundHandler())
	if c := f.requests("/v1/nodes/connect"); len(c) != 1 || c[0].tls {
		t.Fatalf("tunnel: %+v", c)
	}
	if s.secure() || s.urls()[0] != f.url {
		t.Fatalf("addresses: %v", s.urls())
	}
}

// With a third of its life left, the node asks for a new certificate with
// its current one.
func TestCertificateRenewal(t *testing.T) {
	f := startFakeCore(t)
	f.lifetime = 20 * time.Second // less than a third of its life is left right away (it started a minute ago)
	path := filepath.Join(t.TempDir(), "node.json")
	s, err := join(path, []string{f.url}, "pc", "tok", caHash(f.ca))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.getCert(); err != nil {
		t.Fatal(err)
	}
	first := s.certificate().Leaf
	f.lifetime = 24 * time.Hour
	if err := s.getCert(); err != nil {
		t.Fatal(err)
	}
	second := s.certificate().Leaf
	if second.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Fatal("not renewed")
	}
	c := f.requests("/v1/node/certificate")
	if len(c) != 2 || c[0].cn != "" || c[1].cn != "pc" {
		t.Fatalf("certificate requests: %+v", c)
	}
	if err := s.getCert(); err != nil || len(f.requests("/v1/node/certificate")) != 2 {
		t.Fatalf("renewed a fresh certificate: %v", err)
	}
	// Ended: dropped, asked for with the secret alone.
	f.lifetime = 24 * time.Hour
	s.tlsMu.Lock()
	s.client.Leaf.NotAfter = time.Now().Add(-time.Minute)
	s.tlsMu.Unlock()
	if !s.needsCert(time.Now()) || s.certificate() != nil {
		t.Fatal("an ended certificate was kept")
	}
}
