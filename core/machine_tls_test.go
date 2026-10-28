package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// The CA is made once and kept: its hash, which nodes pin, does not change
// across restarts; a CA that does not load is never replaced.
func TestMachineCAKept(t *testing.T) {
	dir := t.TempDir()
	ca, err := openMachineCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ca.hash, "sha256:") || len(ca.hash) != len("sha256:")+64 {
		t.Fatalf("hash %q", ca.hash)
	}
	if fi, err := os.Stat(filepath.Join(dir, machineCAKeyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	again, err := openMachineCA(dir)
	if err != nil || again.hash != ca.hash {
		t.Fatalf("reopened: %v %s != %s", err, again.hash, ca.hash)
	}
	os.WriteFile(filepath.Join(dir, machineCAFile), []byte("broken"), 0o644)
	if _, err := openMachineCA(dir); err == nil {
		t.Fatal("a broken CA was accepted")
	}
	os.Remove(filepath.Join(dir, machineCAFile))
	if _, err := openMachineCA(dir); err == nil {
		t.Fatal("a CA without its certificate was made again")
	}
}

// tlsCore is an eriphe-core with the machines' CA, serving the node routes
// on one port, TLS and plain HTTP side by side, as main does.
type tlsCore struct {
	c   *core
	srv *httptest.Server
}

func newTLSCore(t *testing.T) *tlsCore {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{}, store)
	c.startMachineTLS()
	if c.machines == nil {
		t.Fatal("no CA")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/join-tokens", c.handleJoinToken)
	mux.HandleFunc("POST /v1/nodes/join", c.handleJoin)
	mux.HandleFunc("GET /v1/nodes/connect", c.handleConnect)
	mux.HandleFunc("DELETE /v1/nodes/{name}", c.handleDeleteNode)
	mux.HandleFunc("PUT /v1/nodes/tls", c.handleNodesTLS)
	mux.HandleFunc("GET /v1/node/assignments", c.handleAssignments)
	mux.HandleFunc("POST /v1/node/certificate", c.handleNodeCertificate)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = c.apiListener(ln)
	srv.Start()
	t.Cleanup(srv.Close)
	return &tlsCore{c: c, srv: srv}
}

func (tc *tlsCore) plainURL() string { return tc.srv.URL }
func (tc *tlsCore) tlsURL() string   { return strings.Replace(tc.srv.URL, "http://", "https://", 1) }

// machineClient trusts roots for eriphe-core's name, and presents cert.
func machineClient(roots *x509.CertPool, cert *tls.Certificate) *http.Client {
	cfg := &tls.Config{RootCAs: roots, ServerName: machineServerName}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
}

// nodeDo makes a request as the node name with its secret.
func nodeDo(t *testing.T, client *http.Client, method, url, name, secret string, in, out any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, body)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Eriphe-Node", name)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

type issued struct {
	CA          string    `json:"ca"`
	Certificate string    `json:"certificate"`
	Expires     time.Time `json:"expires"`
}

// certify asks eriphe-core for a certificate for name, the way a node does.
func certify(t *testing.T, client *http.Client, url, name, secret string) (*tls.Certificate, issued, int) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}}, key)
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	var out issued
	code := nodeDo(t, client, http.MethodPost, url+"/v1/node/certificate", name, secret, map[string]string{"csr": string(csr)}, &out)
	if code != http.StatusOK {
		return nil, out, code
	}
	block, _ := pem.Decode([]byte(out.Certificate))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key, Leaf: leaf}, out, code
}

func joinNodeOver(t *testing.T, tc *tlsCore, client *http.Client, url, name string, rejoin bool) string {
	t.Helper()
	tok, _, err := tc.c.newJoinToken(name, rejoin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var joined struct{ Secret string }
	if code := nodeDo(t, client, http.MethodPost, url+"/v1/nodes/join", "", "", map[string]string{"name": name, "token": tok}, &joined); code != http.StatusOK {
		t.Fatalf("join %s: %d", name, code)
	}
	return joined.Secret
}

// connectTunnel opens the tunnel and reports the status.
func connectTunnel(t *testing.T, client *http.Client, url, name, secret string) (int, *yamux.Session) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/v1/nodes/connect", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Eriphe-Node", name)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", tunnelProto)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	session, err := yamux.Server(resp.Body.(io.ReadWriteCloser), tunnelConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	go http.Serve(session, http.NotFoundHandler())
	return resp.StatusCode, session
}

// A node joins over TLS, checking eriphe-core's certificate against the
// CA's hash, gets its certificate, and connects with it. The API's plain
// HTTP keeps working on the same port.
func TestNodeJoinsOverTLS(t *testing.T) {
	tc := newTLSCore(t)
	if code := nodeDo(t, http.DefaultClient, http.MethodGet, tc.plainURL()+"/health", "", "", nil, nil); code != http.StatusOK {
		t.Fatalf("plain HTTP on the API port: %d", code)
	}
	var tok struct {
		CAHash string `json:"ca_hash"`
	}
	if nodeDo(t, http.DefaultClient, http.MethodPost, tc.plainURL()+"/v1/join-tokens?name=pc", "", "", nil, &tok) != http.StatusCreated || tok.CAHash != tc.c.machines.hash {
		t.Fatalf("join token: ca_hash %q", tok.CAHash)
	}

	pinned := machineClient(tc.c.machines.pool, nil)
	secret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "pc", false)
	cert, out, code := certify(t, pinned, tc.tlsURL(), "pc", secret)
	if code != http.StatusOK {
		t.Fatalf("certificate: %d", code)
	}
	if out.CA != string(tc.c.machines.pem) || cert.Leaf.Subject.CommonName != "pc" || !out.Expires.Equal(cert.Leaf.NotAfter.Truncate(time.Second)) {
		t.Fatalf("issued: %+v, CN %s", out, cert.Leaf.Subject.CommonName)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: tc.c.machines.pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client certificate: %v", err)
	}
	if code, _ := connectTunnel(t, machineClient(tc.c.machines.pool, cert), tc.tlsURL(), "pc", secret); code != http.StatusSwitchingProtocols {
		t.Fatalf("connect with the certificate: %d", code)
	}
	n := tc.c.node("pc")
	n.mu.Lock()
	withCert := n.tls
	n.mu.Unlock()
	if !withCert {
		t.Fatal("the tunnel is not marked as TLS")
	}
	nodes, _ := tc.c.store.nodes()
	if len(nodes) != 1 || nodes[0].CertExpires.IsZero() {
		t.Fatalf("nodes: %+v", nodes)
	}
}

// A node that pins another CA's hash, or trusts no CA, cannot complete the
// handshake: it never sends its join token.
func TestNodeWrongCARefused(t *testing.T) {
	tc := newTLSCore(t)
	other, err := newMachineCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]*http.Client{
		"other CA":  machineClient(other.pool, nil),
		"system CA": {},
	} {
		if _, err := client.Get(tc.tlsURL() + "/health"); err == nil {
			t.Fatalf("%s: handshake accepted", name)
		}
	}
	if other.hash == tc.c.machines.hash {
		t.Fatal("two CAs with the same hash")
	}
}

// A request that presents a certificate must present the node's own: one
// of another CA fails the handshake, one for another node is refused.
func TestNodeCertificateRequired(t *testing.T) {
	tc := newTLSCore(t)
	pinned := machineClient(tc.c.machines.pool, nil)
	secret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "pc", false)
	otherSecret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "nas", false)
	otherCert, _, _ := certify(t, pinned, tc.tlsURL(), "nas", otherSecret)
	if code := nodeDo(t, machineClient(tc.c.machines.pool, otherCert), http.MethodGet, tc.tlsURL()+"/v1/node/assignments", "pc", secret, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("another node's certificate: %d", code)
	}

	foreign, _ := newMachineCA(time.Now())
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, leaf, _ := foreign.issueNode("pc", &key.PublicKey)
	forged := &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key}
	if _, err := machineClient(tc.c.machines.pool, forged).Get(tc.tlsURL() + "/health"); err == nil {
		t.Fatal("a certificate of another CA passed the handshake")
	}

	// Closed plain path: the secret alone no longer does, the certificate does.
	if code := nodeDo(t, http.DefaultClient, http.MethodPut, tc.plainURL()+"/v1/nodes/tls", "", "", map[string]bool{"tls_only": true}, nil); code != http.StatusOK {
		t.Fatalf("tls_only: %d", code)
	}
	if code := nodeDo(t, http.DefaultClient, http.MethodGet, tc.plainURL()+"/v1/node/assignments?wait=0", "pc", secret, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("plain HTTP with the secret: %d", code)
	}
	if code, _ := connectTunnel(t, http.DefaultClient, tc.plainURL(), "pc", secret); code != http.StatusForbidden {
		t.Fatalf("plain tunnel: %d", code)
	}
	if code, _ := connectTunnel(t, pinned, tc.tlsURL(), "pc", secret); code != http.StatusForbidden {
		t.Fatalf("TLS tunnel without a certificate: %d", code)
	}
	tok, _, _ := tc.c.newJoinToken("laptop", false, time.Hour)
	if code := nodeDo(t, http.DefaultClient, http.MethodPost, tc.plainURL()+"/v1/nodes/join", "", "", map[string]string{"name": "laptop", "token": tok}, nil); code != http.StatusForbidden {
		t.Fatalf("plain join: %d", code)
	}
	if code := nodeDo(t, machineClient(tc.c.machines.pool, otherCert), http.MethodGet, tc.tlsURL()+"/v1/node/assignments?wait=0", "nas", otherSecret, nil, nil); code != http.StatusOK {
		t.Fatalf("with its certificate: %d", code)
	}
}

// A node renews its certificate with its current one; the previous one
// still works while the node moves to the new one, the one before no more.
func TestNodeCertificateRenewal(t *testing.T) {
	tc := newTLSCore(t)
	pinned := machineClient(tc.c.machines.pool, nil)
	secret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "pc", false)
	first, _, _ := certify(t, pinned, tc.tlsURL(), "pc", secret)
	// Later: the new one ends later.
	tc.c.machines.now = func() time.Time { return time.Now().Add(time.Minute) }
	second, _, code := certify(t, machineClient(tc.c.machines.pool, first), tc.tlsURL(), "pc", secret)
	if code != http.StatusOK || !second.Leaf.NotAfter.After(first.Leaf.NotAfter) || serialHex(second.Leaf) == serialHex(first.Leaf) {
		t.Fatalf("renewal: %d", code)
	}
	tc.c.machines.now = time.Now
	assignments := tc.tlsURL() + "/v1/node/assignments?wait=0"
	for name, cert := range map[string]*tls.Certificate{"new": second, "previous": first} {
		if code := nodeDo(t, machineClient(tc.c.machines.pool, cert), http.MethodGet, assignments, "pc", secret, nil, nil); code != http.StatusOK {
			t.Fatalf("%s certificate: %d", name, code)
		}
	}
	third, _, _ := certify(t, machineClient(tc.c.machines.pool, second), tc.tlsURL(), "pc", secret)
	if code := nodeDo(t, machineClient(tc.c.machines.pool, first), http.MethodGet, assignments, "pc", secret, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("certificate before the previous: %d", code)
	}
	if code := nodeDo(t, machineClient(tc.c.machines.pool, third), http.MethodGet, assignments, "pc", secret, nil, nil); code != http.StatusOK {
		t.Fatalf("newest: %d", code)
	}
}

// Joining again or forgetting a node revokes its certificate at once.
func TestNodeCertificateRevoked(t *testing.T) {
	tc := newTLSCore(t)
	pinned := machineClient(tc.c.machines.pool, nil)
	secret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "pc", false)
	cert, _, _ := certify(t, pinned, tc.tlsURL(), "pc", secret)
	old := machineClient(tc.c.machines.pool, cert)
	assignments := tc.tlsURL() + "/v1/node/assignments?wait=0"
	if code := nodeDo(t, old, http.MethodGet, assignments, "pc", secret, nil, nil); code != http.StatusOK {
		t.Fatalf("before: %d", code)
	}

	newSecret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "pc", true)
	if code := nodeDo(t, old, http.MethodGet, assignments, "pc", newSecret, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("old certificate after rejoining: %d", code)
	}
	if code, _ := connectTunnel(t, old, tc.tlsURL(), "pc", newSecret); code != http.StatusUnauthorized {
		t.Fatalf("old certificate's tunnel after rejoining: %d", code)
	}
	cert, _, _ = certify(t, pinned, tc.tlsURL(), "pc", newSecret)
	current := machineClient(tc.c.machines.pool, cert)
	if code, _ := connectTunnel(t, current, tc.tlsURL(), "pc", newSecret); code != http.StatusSwitchingProtocols {
		t.Fatalf("new certificate: %d", code)
	}

	if code := nodeDo(t, http.DefaultClient, http.MethodDelete, tc.plainURL()+"/v1/nodes/pc", "", "", nil, nil); code != http.StatusNoContent {
		t.Fatalf("forget: %d", code)
	}
	if tc.c.store.nodeCertValid("pc", serialHex(cert.Leaf)) {
		t.Fatal("certificate still valid after forgetting the node")
	}
	if code := nodeDo(t, current, http.MethodGet, assignments, "pc", newSecret, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("after forgetting: %d", code)
	}
}

// A node that joined before TLS (its secret only, over plain HTTP) gets the
// CA and its certificate with its secret, on the address it knows, then
// connects over TLS on the same port. Closing the plain path drops a node
// still connected without its certificate.
func TestOldNodeMigrates(t *testing.T) {
	tc := newTLSCore(t)
	// An installation from before TLS: the plain path is still open.
	tc.c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, nodesTLSOnlyKey)
	secret := randomSecret()
	if err := tc.c.store.registerNode("nas", hashSecret(secret)); err != nil {
		t.Fatal(err)
	}
	if code, _ := connectTunnel(t, http.DefaultClient, tc.plainURL(), "nas", secret); code != http.StatusSwitchingProtocols {
		t.Fatalf("old node, plain: %d", code)
	}
	_, plainTunnel := connectTunnel(t, http.DefaultClient, tc.plainURL(), "nas", secret)

	cert, out, code := certify(t, http.DefaultClient, tc.plainURL(), "nas", secret)
	if code != http.StatusOK {
		t.Fatalf("upgrade: %d", code)
	}
	block, _ := pem.Decode([]byte(out.CA))
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || caHash(ca) != tc.c.machines.hash {
		t.Fatalf("CA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if code, _ := connectTunnel(t, machineClient(roots, cert), tc.tlsURL(), "nas", secret); code != http.StatusSwitchingProtocols {
		t.Fatalf("migrated node, TLS: %d", code)
	}

	// The plain tunnel was replaced; another old node is still plain.
	<-plainTunnel.CloseChan()
	other := randomSecret()
	tc.c.store.registerNode("pc", hashSecret(other))
	_, pcTunnel := connectTunnel(t, http.DefaultClient, tc.plainURL(), "pc", other)
	if err := tc.c.setNodesTLSOnly(true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pcTunnel.CloseChan():
	case <-time.After(5 * time.Second):
		t.Fatal("a node without its certificate stayed connected")
	}
	if !tc.c.node("nas").connected() {
		t.Fatal("the node over TLS was dropped")
	}
	if _, _, code := certify(t, http.DefaultClient, tc.plainURL(), "pc", other); code != http.StatusForbidden {
		t.Fatalf("upgrade over plain HTTP once closed: %d", code)
	}
}

// Once the plain path is closed, a node that has just joined over pinned TLS
// still gets its first certificate there, then connects with it; without one
// it gets nothing else.
func TestFirstCertificateWhenTLSOnly(t *testing.T) {
	tc := newTLSCore(t)
	if err := tc.c.setNodesTLSOnly(true); err != nil {
		t.Fatal(err)
	}
	pinned := machineClient(tc.c.machines.pool, nil)
	secret := joinNodeOver(t, tc, pinned, tc.tlsURL(), "desk", false)
	assignments := tc.tlsURL() + "/v1/node/assignments?wait=0"
	if code := nodeDo(t, pinned, http.MethodGet, assignments, "desk", secret, nil, nil); code == http.StatusOK {
		t.Fatalf("without a certificate: %d", code)
	}
	cert, _, code := certify(t, pinned, tc.tlsURL(), "desk", secret)
	if code != http.StatusOK {
		t.Fatalf("first certificate: %d", code)
	}
	if code, _ := connectTunnel(t, machineClient(tc.c.machines.pool, cert), tc.tlsURL(), "desk", secret); code != http.StatusSwitchingProtocols {
		t.Fatalf("tunnel with its certificate: %d", code)
	}
	if _, _, code := certify(t, pinned, tc.tlsURL(), "desk", "not-its-secret"); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", code)
	}
}

// A new installation accepts machines over TLS only from the start; an
// older one once every machine has its certificate, unless the API chose
// to keep the plain path open.
func TestAutoTLSOnly(t *testing.T) {
	tc := newTLSCore(t)
	if !tc.c.nodesTLSOnly() {
		t.Fatal("a new installation accepts machines without TLS")
	}

	// An older installation, with a machine from before TLS.
	tc.c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, nodesTLSOnlyKey)
	tc.c.store.registerNode("nas", hashSecret("s"))
	tc.c.store.registerNode("pc", hashSecret("p"))
	tc.c.store.setNodeCert("pc", hashSecret("p"), "2a", time.Now().Add(time.Hour))
	nodes, _ := tc.c.store.nodes()
	tc.c.autoTLSOnly(nodes)
	if tc.c.nodesTLSOnly() {
		t.Fatal("closed while a machine has no certificate")
	}
	tc.c.store.setNodeCert("nas", hashSecret("s"), "1f", time.Now().Add(time.Hour))
	nodes, _ = tc.c.store.nodes()
	tc.c.autoTLSOnly(nodes)
	if !tc.c.nodesTLSOnly() {
		t.Fatal("still open once every machine has its certificate")
	}

	// The API's choice to keep it open stays.
	if err := tc.c.setNodesTLSOnly(false); err != nil {
		t.Fatal(err)
	}
	tc.c.autoTLSOnly(nodes)
	if tc.c.nodesTLSOnly() {
		t.Fatal("the API's choice was overridden")
	}
}

// The workspace's commands carry the CA's hash; the machines page keeps it
// under "Advanced", says which machines have no certificate, and has no
// switch for the plain path any more.
func TestWebMachinesTLS(t *testing.T) {
	tw := newTestWeb(t)
	tw.c.startMachineTLS()
	csrf := tw.setup()
	hash := tw.c.machines.hash
	_, body, _ := tw.post("/settings/machines/join", url.Values{"name": {"desk"}}, csrf)
	for _, want := range []string{"-name desk -ca-hash " + hash, "ERIPHE_CA_HASH: " + hash, "-CaHash " + hash,
		"--name desk --ca-hash " + hash + "</pre>", "-Name desk -CaHash " + hash + "</pre>"} {
		if !strings.Contains(body, want) {
			t.Errorf("join commands lack %q", want)
		}
	}
	tw.c.store.registerNode("nas", hashSecret("s"))
	_, body, _ = tw.do("GET", "/settings/machines", nil, nil)
	i := strings.Index(body, `<details class="panel advanced" id="machines-advanced">`)
	if i < 0 || !strings.Contains(body[i:], hash) || strings.Count(body, hash) != 1 || !strings.Contains(body, "Connexion non chiffrée") {
		t.Fatalf("machines page:\n%s", body)
	}
	if strings.Contains(body, "N'accepter que les machines en TLS") || strings.Contains(body, "/settings/machines/tls") {
		t.Fatal("the TLS switch is still there")
	}
	tw.c.store.setNodeCert("nas", hashSecret("s"), "1f", time.Now().Add(time.Hour))
	if _, body, _ = tw.do("GET", "/settings/machines", nil, nil); strings.Contains(body, "Connexion non chiffrée") {
		t.Fatal("a machine with its certificate is shown without")
	}
	if code, _, _ := tw.post("/settings/machines/tls", url.Values{"tls_only": {"0"}}, csrf); code == http.StatusOK {
		t.Fatalf("the workspace still opens the plain path: %d", code)
	}
}
