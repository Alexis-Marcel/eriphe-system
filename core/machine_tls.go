package main

import (
	"bufio"
	"crypto"
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
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Machines reach eriphe-core over TLS, with a certificate authority of
// their own, like kubeadm's cluster CA. It is not the workspace's
// certificate: it only signs eriphe-core's certificate for the nodes and
// one client certificate per node.
//
//   - eriphe-core makes the CA at first start, next to the database
//     (machine-ca.pem, machine-ca-key.pem), and keeps it: nodes pin it.
//   - The join command carries the hash of the CA's public key (-ca-hash
//     sha256:<hex>, as kubeadm's --discovery-token-ca-cert-hash): the node
//     checks it in the TLS handshake of its join.
//   - A node asks for its client certificate (POST /v1/node/certificate)
//     with its secret, then with its certificate to renew it. A request that
//     presents one must present the node's current (or previous) one:
//     forgetting a node or letting it join again revokes it at once.
//   - The API port serves TLS and plain HTTP side by side (sniffListener):
//     the CLI, Home Assistant and the brain keep their plain HTTP, and a
//     node that joined over plain HTTP moves to TLS by itself on the same
//     address. nodesTLSOnlyKey then closes the plain path for nodes: from
//     the start on a new installation, and on an older one as soon as
//     every machine has its certificate.

const (
	machineCAFile    = "machine-ca.pem"
	machineCAKeyFile = "machine-ca-key.pem"
	// machineServerName is the name in eriphe-core's certificate for the
	// nodes: they check it whatever address they dial (an IP, a container's
	// name), since only this CA can sign it.
	machineServerName = "eriphe-core"
	nodeCertLifetime  = 365 * 24 * time.Hour
	// nodesTLSOnlyKey, when "1", refuses nodes over plain HTTP and nodes
	// without their certificate. Unset, eriphe-core sets it by itself once
	// every machine has its certificate (autoTLSOnly): at once on a new
	// installation. "0" is the API's explicit choice to keep the plain path
	// open (PUT /v1/nodes/tls), never changed by itself.
	nodesTLSOnlyKey = "nodes_tls_only"
)

type machineCA struct {
	cert *x509.Certificate
	key  crypto.Signer
	pem  []byte // cert, PEM
	pool *x509.CertPool
	hash string // sha256:<hex> of the CA's public key, for -ca-hash
	now  func() time.Time

	mu     sync.Mutex
	server *tls.Certificate // eriphe-core's own, made and renewed as needed
}

// openMachineCA loads the machines' CA from dir, or makes it if there is
// none yet. A CA that is there but does not load is an error, never
// replaced: every node pins it.
func openMachineCA(dir string) (*machineCA, error) {
	certFile, keyFile := filepath.Join(dir, machineCAFile), filepath.Join(dir, machineCAKeyFile)
	certPEM, err := os.ReadFile(certFile)
	if errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Stat(keyFile); err == nil {
			return nil, fmt.Errorf("%s is missing next to %s", machineCAFile, machineCAKeyFile)
		}
		ca, err := newMachineCA(time.Now())
		if err != nil {
			return nil, err
		}
		key, err := x509.MarshalPKCS8PrivateKey(ca.key)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(certFile, ca.pem, 0o644); err != nil {
			return nil, err
		}
		log.Printf("machines: certificate authority created, %s", ca.hash)
		return ca, nil
	}
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", machineCAFile, err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok || !cert.IsCA {
		return nil, fmt.Errorf("%s is not a certificate authority", machineCAFile)
	}
	return wrapMachineCA(cert, signer), nil
}

func newMachineCA(now time.Time) (*machineCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "Eriphe machines CA", Organization: []string{"Eriphe"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return wrapMachineCA(cert, key), nil
}

func wrapMachineCA(cert *x509.Certificate, key crypto.Signer) *machineCA {
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &machineCA{cert: cert, key: key, pool: pool, hash: caHash(cert), now: time.Now,
		pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})}
}

// caHash is the hash a join command pins: SHA-256 of the CA's public key
// (its SubjectPublicKeyInfo), as kubeadm's.
func caHash(cert *x509.Certificate) string {
	h := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(h[:])
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func serialHex(cert *x509.Certificate) string { return cert.SerialNumber.Text(16) }

// serverCertificate is eriphe-core's certificate for the nodes, signed by
// the CA and sent with it (a joining node finds it by its hash), made again
// a month before its end.
func (ca *machineCA) serverCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	now := ca.now()
	if ca.server != nil && now.Add(30*24*time.Hour).Before(ca.server.Leaf.NotAfter) {
		return ca.server, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: machineServerName},
		DNSNames:     []string{machineServerName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	leaf, _ := x509.ParseCertificate(der)
	ca.server = &tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key, Leaf: leaf}
	return ca.server, nil
}

// issueNode signs a client certificate for the node name, for pub.
func (ca *machineCA) issueNode(name string, pub crypto.PublicKey) (certPEM []byte, cert *x509.Certificate, err error) {
	now := ca.now()
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: name, Organization: []string{"Eriphe nodes"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(nodeCertLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, nil, err
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert, nil
}

// clientAuth asks clients for a certificate of this CA, without requiring
// one: the CLI and the brain have none, and a node is checked per request.
func (ca *machineCA) clientAuth(cfg *tls.Config) *tls.Config {
	cfg.ClientAuth = tls.VerifyClientCertIfGiven
	cfg.ClientCAs = ca.pool
	return cfg
}

// listener serves TLS, with eriphe-core's certificate for the nodes, and
// plain HTTP on the same port.
func (ca *machineCA) listener(ln net.Listener) net.Listener {
	cfg := ca.clientAuth(&tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: ca.serverCertificate,
		NextProtos: []string{"http/1.1"}}) // the tunnel is an HTTP/1.1 upgrade
	return newSniffListener(ln, cfg)
}

// sniffListener tells TLS from plain HTTP by the first byte a client sends:
// a TLS handshake record starts with 0x16, an HTTP request with a method's
// letter. A TLS connection is handed out as a *tls.Conn, so http.Server
// fills r.TLS for it.
type sniffListener struct {
	net.Listener
	tls   *tls.Config
	conns chan net.Conn
	err   chan error
	done  chan struct{}
	close sync.Once
}

func newSniffListener(ln net.Listener, cfg *tls.Config) *sniffListener {
	l := &sniffListener{Listener: ln, tls: cfg, conns: make(chan net.Conn), err: make(chan error, 1), done: make(chan struct{})}
	go l.run()
	return l
}

func (l *sniffListener) run() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			l.err <- err
			return
		}
		go l.sniff(conn)
	}
}

func (l *sniffListener) sniff(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	first, err := r.Peek(1)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	var c net.Conn = &peekedConn{Conn: conn, r: r}
	if first[0] == 0x16 {
		c = tls.Server(c, l.tls)
	}
	select {
	case l.conns <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.err:
		l.err <- err
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) Close() error {
	l.close.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// peekedConn reads the bytes sniffed first.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

var (
	errNodeTLSRequired = errors.New("eriphe-core accepts machines over TLS only, with their certificate: update eriphe-node, or join again with -ca-hash")
	errNodeCert        = errors.New("this certificate is not the node's: it was revoked, or is for another node")
)

// authNode authenticates a node by its name and secret, and by its
// certificate when it presents one; without one, only while the plain path
// is open. The status is what to answer when it fails.
func (c *core) authNode(r *http.Request) (string, int, error) {
	name := r.Header.Get("Eriphe-Node")
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if name == "" || !c.store.checkNode(name, hashSecret(secret)) {
		return name, http.StatusUnauthorized, errors.New("unauthorized")
	}
	if c.store.nodeSuspended(name) {
		return name, http.StatusForbidden, errNodeSuspended // node_access.go
	}
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		leaf := r.TLS.PeerCertificates[0]
		// Both listeners verify a given certificate against the CA; a chain
		// is there only if it was.
		if len(r.TLS.VerifiedChains) == 0 || leaf.Subject.CommonName != name || !c.store.nodeCertValid(name, serialHex(leaf)) {
			log.Printf("node %s: refused a certificate that is not its own (serial %s)", name, serialHex(leaf))
			return name, http.StatusUnauthorized, errNodeCert
		}
		return name, 0, nil
	}
	if c.nodesTLSOnly() {
		return name, http.StatusForbidden, errNodeTLSRequired
	}
	return name, 0, nil
}

func (c *core) nodesTLSOnly() bool { return c.store.get(nodesTLSOnlyKey) == "1" }

// handleNodeCertificate signs a client certificate for the calling node,
// for the public key of its request ({"csr": PEM}): its first one, with its
// secret, or a new one before its current one ends. It answers the CA too,
// which a node that joined over plain HTTP pins from then on. Once the
// plain path is closed, a node with no certificate yet (it has just joined
// over pinned TLS) still gets its first one here, over TLS, with its secret.
func (c *core) handleNodeCertificate(w http.ResponseWriter, r *http.Request) {
	name, status, err := c.authNode(r)
	if errors.Is(err, errNodeTLSRequired) && r.TLS != nil {
		err = nil
	}
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if c.machines == nil {
		http.Error(w, "eriphe-core has no certificate authority for machines", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		CSR string `json:"csr"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		http.Error(w, "a certificate request is required", http.StatusBadRequest)
		return
	}
	block, _ := pem.Decode([]byte(in.CSR))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		http.Error(w, "a PEM certificate request is required", http.StatusBadRequest)
		return
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err == nil {
		err = csr.CheckSignature() // the node holds the key
	}
	if err != nil {
		http.Error(w, "certificate request: "+err.Error(), http.StatusBadRequest)
		return
	}
	certPEM, cert, err := c.machines.issueNode(name, csr.PublicKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := c.store.setNodeCert(name, hashSecret(secret), serialHex(cert), cert.NotAfter); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		log.Printf("node %s: certificate renewed, until %s", name, cert.NotAfter.Format(time.DateOnly))
	} else {
		log.Printf("node %s: first certificate, until %s", name, cert.NotAfter.Format(time.DateOnly))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ca": string(c.machines.pem), "certificate": string(certPEM), "expires": cert.NotAfter})
}

// handleNodesTLS shows the CA's hash and whether nodes must use TLS
// (GET), or opens and closes the plain path for nodes (PUT
// {"tls_only": true}, admin only). It is an advanced setting: eriphe-core
// closes the plain path by itself (autoTLSOnly), and the PUT only matters
// to keep it open for a machine that cannot have its certificate yet.
// Closing it drops the tunnels of nodes connected without their
// certificate.
func (c *core) handleNodesTLS(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var in struct {
			TLSOnly bool `json:"tls_only"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
			http.Error(w, "{\"tls_only\": true|false} is required", http.StatusBadRequest)
			return
		}
		if err := c.setNodesTLSOnly(in.TLSOnly); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}
	nodes, err := c.store.nodes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plain := []string{}
	for _, n := range nodes {
		if n.CertExpires.IsZero() {
			plain = append(plain, n.Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ca_hash": c.caHash(), "tls_only": c.nodesTLSOnly(), "without_certificate": plain})
}

func (c *core) setNodesTLSOnly(on bool) error {
	if on && c.machines == nil {
		return errors.New("eriphe-core has no certificate authority for machines")
	}
	v := "0"
	if on {
		v = "1"
	}
	if err := c.store.set(nodesTLSOnlyKey, v); err != nil {
		return err
	}
	log.Printf("machines: plain path for nodes %s", map[bool]string{true: "closed", false: "open"}[on])
	if on {
		c.mu.Lock()
		var plain []string
		for name, n := range c.nodes {
			n.mu.Lock()
			if n.session != nil && !n.tls {
				plain = append(plain, name)
			}
			n.mu.Unlock()
		}
		c.mu.Unlock()
		for _, name := range plain {
			log.Printf("node %s: connected without its certificate, disconnected", name)
			c.disconnect(name)
		}
	}
	return nil
}

// autoTLSOnly closes the plain path for nodes once every registered machine
// has its certificate, unless the API chose to keep it open: at once on a
// new installation, which has none; after the last one moves to TLS on an
// older one.
func (c *core) autoTLSOnly(nodes []NodeRow) {
	if c.machines == nil || c.store.get(nodesTLSOnlyKey) != "" {
		return
	}
	for _, n := range nodes {
		if n.CertExpires.IsZero() {
			return
		}
	}
	if err := c.setNodesTLSOnly(true); err != nil {
		log.Printf("machines: %v", err)
		return
	}
	if len(nodes) == 0 {
		log.Printf("machines: TLS only, from the start")
	} else {
		log.Printf("machines: every machine has its certificate: TLS only from now on")
	}
}

// caHash is the -ca-hash of join commands, empty without a CA.
func (c *core) caHash() string {
	if c.machines == nil {
		return ""
	}
	return c.machines.hash
}

// startMachineTLS opens the machines' CA in the data folder; without it
// (it could not be read), nodes stay on plain HTTP.
func (c *core) startMachineTLS() {
	ca, err := openMachineCA(c.store.dir)
	if err != nil {
		log.Printf("machines: no TLS for nodes: %v", err)
		return
	}
	c.machines = ca
	log.Printf("machines: TLS for nodes on the API port, CA %s", ca.hash)
	if nodes, err := c.store.nodes(); err == nil {
		c.autoTLSOnly(nodes)
	}
}

// apiListener is the API's listener: TLS for nodes and plain HTTP side by
// side when the machines' CA is there.
func (c *core) apiListener(ln net.Listener) net.Listener {
	if c.machines == nil {
		return ln
	}
	return c.machines.listener(ln)
}
