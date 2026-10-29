package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTailnet stands for tsnet: the test sets its status, and its
// listeners are plain loopback ones.
type fakeTailnet struct {
	mu        sync.Mutex
	opts      tailnetOptions
	st        tsStatus
	statusErr error
	startErr  error
	https     bool
	httpsURL  string
	queries   int
	logins    int
	started   bool
	closed    bool
	loggedOut bool
	cert      tls.Certificate
	lns       map[string]net.Listener
}

func (f *fakeTailnet) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return f.startErr
}

func (f *fakeTailnet) Status(context.Context) (tsStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, f.statusErr
}

func (f *fakeTailnet) QueryHTTPS(context.Context) (bool, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries++
	return f.https, f.httpsURL, "", nil
}

func (f *fakeTailnet) Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lns == nil {
		f.lns = map[string]net.Listener{}
	}
	f.lns[addr] = ln
	return ln, nil
}

func (f *fakeTailnet) addr(port string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ln := f.lns[port]; ln != nil {
		return ln.Addr().String()
	}
	return ""
}

func (f *fakeTailnet) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return &f.cert, nil
}

func (f *fakeTailnet) StartLogin(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	return nil
}

func (f *fakeTailnet) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loggedOut = true
	return nil
}

func (f *fakeTailnet) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeTailnet) set(fn func(*fakeTailnet)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

const testTSName = "eriphe.tail1234.ts.net"

// testClock is a clock the tests move.
type testClock struct{ ns atomic.Int64 }

func newTestClock() *testClock {
	c := &testClock{}
	c.ns.Store(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *testClock) now() time.Time      { return time.Unix(0, c.ns.Load()) }
func (c *testClock) add(d time.Duration) { c.ns.Add(int64(d)) }

type remoteHarness struct {
	t     *testing.T
	r     *remoteAccess
	store *Store
	db    string
	clock *testClock
	fakes []*fakeTailnet
	mu    sync.Mutex
	next  *fakeTailnet // the status the next tailnet starts with
}

func newRemoteHarness(t *testing.T, db string) *remoteHarness {
	t.Helper()
	if db == "" {
		db = filepath.Join(t.TempDir(), "core.db")
	}
	store, err := openStore(db)
	if err != nil {
		t.Fatal(err)
	}
	h := &remoteHarness{t: t, store: store, db: db, clock: newTestClock()}
	api := http.NewServeMux()
	api.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	api.HandleFunc("GET /peer", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
		}
	})
	h.r = h.newRemote(api)
	t.Cleanup(h.r.shutdown)
	return h
}

func (h *remoteHarness) newRemote(api http.Handler) *remoteAccess {
	r := newRemoteAccess(h.store, h.db, api)
	r.now = h.clock.now
	r.every = func(string) time.Duration { return 5 * time.Millisecond }
	r.logf = func(string, ...any) {}
	r.authKey = func() string { return "" }
	cert := testCert(h.t)
	r.newTailnet = func(o tailnetOptions) (tailnet, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		f := &fakeTailnet{opts: o, cert: cert, st: tsStatus{Backend: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/abc123"}}
		if h.next != nil {
			f.st, f.https, f.httpsURL = h.next.st, h.next.https, h.next.httpsURL
		}
		h.fakes = append(h.fakes, f)
		return f, nil
	}
	return r
}

func (h *remoteHarness) fake() *fakeTailnet {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.fakes) == 0 {
		return nil
	}
	return h.fakes[len(h.fakes)-1]
}

// wait waits until the view passes check.
func (h *remoteHarness) wait(what string, check func(remoteView) bool) remoteView {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v := h.r.current(); check(v) {
			return v
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("%s: never happened; view %+v", what, h.r.current())
	return remoteView{}
}

func (h *remoteHarness) phase(p string) remoteView {
	h.t.Helper()
	return h.wait("phase "+p, func(v remoteView) bool { return v.Phase == p })
}

var testCertOnce struct {
	sync.Once
	cert tls.Certificate
}

// testCert is a self-signed certificate for testTSName.
func testCert(t *testing.T) tls.Certificate {
	testCertOnce.Do(func() {
		dir, err := os.MkdirTemp("", "eriphe-cert")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
		c, err := selfSigned(filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem"), []string{testTSName}, time.Now())
		if err != nil {
			panic(err)
		}
		testCertOnce.cert = c
	})
	return testCertOnce.cert
}

func running(certs bool, expiry time.Time) tsStatus {
	st := tsStatus{Backend: "Running", DNSName: testTSName, Tailnet: "sam@example.com", MagicDNS: true, KeyExpiry: expiry}
	if certs {
		st.CertDomains = []string{testTSName}
	}
	return st
}

func TestRemoteStateMachine(t *testing.T) {
	h := newRemoteHarness(t, "")
	if v := h.r.current(); v.Phase != phaseOff || v.Enabled {
		t.Fatalf("remote access is on by default: %+v", v)
	}
	h.r.setWorkspace(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "workspace") }))
	machines, err := newMachineCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h.r.clientCAs = machines.pool
	if err := h.r.enable(); err != nil {
		t.Fatal(err)
	}
	v := h.phase(phaseLogin)
	if v.AuthURL != "https://login.tailscale.com/a/abc123" {
		t.Fatalf("login URL: %q", v.AuthURL)
	}
	f := h.fake()
	if f.opts.Hostname != "eriphe" || f.opts.Dir != filepath.Join(filepath.Dir(h.db), "tailscale") || f.opts.ControlURL != "" {
		t.Fatalf("tsnet options: %+v", f.opts)
	}
	if fi, err := os.Stat(f.opts.Dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state directory: %v %v", fi, err)
	}

	f.set(func(f *fakeTailnet) { f.st = tsStatus{Backend: "NeedsMachineAuth"} })
	h.phase(phaseApproval)

	f.set(func(f *fakeTailnet) {
		f.st = running(false, h.clock.now().Add(180*24*time.Hour))
		f.httpsURL = "https://login.tailscale.com/f/serve?node=abc"
	})
	v = h.phase(phaseHTTPS)
	v = h.wait("consent URL", func(v remoteView) bool { return v.HTTPSURL != "" })
	if v.HTTPSURL != "https://login.tailscale.com/f/serve?node=abc" || v.Name != testTSName || v.Listening {
		t.Fatalf("https phase: %+v", v)
	}

	f.set(func(f *fakeTailnet) { f.st = running(true, h.clock.now().Add(180*24*time.Hour)) })
	v = h.wait("ready and listening", func(v remoteView) bool { return v.Phase == phaseReady && v.Listening })
	if v.URL() != "https://"+testTSName || v.APIURL() != "https://"+testTSName+":8800" || v.Expiring {
		t.Fatalf("ready: %+v", v)
	}

	// The API on :8800 and the workspace on :443, in TLS with the tailnet's certificate.
	pool := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(f.cert.Certificate[0])
	pool.AddCert(leaf)
	client := func(port string) *http.Client {
		return &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: testTSName},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", f.addr(port))
			}}}
	}
	resp, err := client(":8800").Get("https://" + testTSName + ":8800/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok\n" || resp.Proto != "HTTP/1.1" {
		t.Fatalf("API on the tailnet: %s %q", resp.Proto, body)
	}
	resp, err = client(":443").Get("https://" + testTSName + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "workspace" {
		t.Fatalf("workspace on the tailnet: %q", body)
	}
	// A node presents its certificate there too, with the tailnet's one in front.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, nodeLeaf, _ := machines.issueNode("laptop", &key.PublicKey)
	nodeClient := client(":8800")
	nodeClient.Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{nodeLeaf.Raw}, PrivateKey: key}}
	resp, err = nodeClient.Get("https://" + testTSName + ":8800/peer")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "laptop" {
		t.Fatalf("node certificate on the tailnet: %q", body)
	}

	// Fourteen days before the key expires, the page warns.
	h.clock.add(170 * 24 * time.Hour)
	v = h.wait("expiry warning", func(v remoteView) bool { return v.Expiring })
	if v.Expired {
		t.Fatal("expired too early")
	}
	if err := h.r.relogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Fatalf("relogin: %d logins", f.logins)
	}

	if err := h.r.disable(); err != nil {
		t.Fatal(err)
	}
	if v := h.r.current(); v.Phase != phaseOff || v.Enabled || v.Listening {
		t.Fatalf("after disable: %+v", v)
	}
	if !f.closed || f.loggedOut {
		t.Fatalf("disable: closed %v, logged out %v", f.closed, f.loggedOut)
	}
	if _, err := (&net.Dialer{Timeout: time.Second}).Dial("tcp", f.addr(":8800")); err == nil {
		t.Fatal("the API listener is still open")
	}
	// The identity is kept: turning it on again needs no login.
	if _, err := os.Stat(f.opts.Dir); err != nil {
		t.Fatalf("state removed by disable: %v", err)
	}
}

func TestRemoteLoginAgain(t *testing.T) {
	h := newRemoteHarness(t, "")
	h.next = &fakeTailnet{st: tsStatus{Backend: "NeedsLogin"}}
	h.r.enable()
	h.phase(phaseLogin)
	f := h.fake()
	logins := func() (n int) { f.set(func(f *fakeTailnet) { n = f.logins }); return }
	time.Sleep(30 * time.Millisecond)
	if logins() != 0 {
		t.Fatal("asked for a login URL before tsnet could give one")
	}
	h.clock.add(4 * time.Second)
	h.wait("login asked", func(remoteView) bool { return logins() == 1 })
	time.Sleep(30 * time.Millisecond)
	if logins() != 1 {
		t.Fatalf("asked %d times within 30 s", logins())
	}
}

func TestRemoteError(t *testing.T) {
	h := newRemoteHarness(t, "")
	h.r.newTailnet = func(o tailnetOptions) (tailnet, error) {
		return &fakeTailnet{opts: o, startErr: errors.New("no network")}, nil
	}
	h.r.enable()
	v := h.phase(phaseError)
	if !strings.Contains(v.Error, "no network") {
		t.Fatalf("error: %q", v.Error)
	}
	h.r.newTailnet = h.newRemote(nil).newTailnet
	h.r.retry()
	h.phase(phaseLogin)
}

func TestRemoteSettingsPersist(t *testing.T) {
	h := newRemoteHarness(t, "")
	if err := h.r.configure("ma maison", ""); !errors.Is(err, errBadHostname) {
		t.Fatalf("bad hostname: %v", err)
	}
	if err := h.r.configure("-x", ""); !errors.Is(err, errBadHostname) {
		t.Fatalf("bad hostname: %v", err)
	}
	if err := h.r.configure("maison", "ftp://x"); !errors.Is(err, errBadControl) {
		t.Fatalf("bad control URL: %v", err)
	}
	if err := h.r.configure("maison", "https://hs.example.org/"); err != nil {
		t.Fatal(err)
	}
	if err := h.r.enable(); err != nil {
		t.Fatal(err)
	}
	h.phase(phaseLogin)
	if o := h.fake().opts; o.Hostname != "maison" || o.ControlURL != "https://hs.example.org" {
		t.Fatalf("options: %+v", o)
	}
	h.r.shutdown()

	// At the next start of eriphe-core, it starts again by itself.
	h2 := &remoteHarness{t: t, store: h.store, db: h.db, clock: h.clock}
	r2 := h2.newRemote(nil)
	h2.r = r2
	t.Cleanup(r2.shutdown)
	if v := r2.current(); !v.Enabled || v.Hostname != "maison" || v.ControlURL != "https://hs.example.org" {
		t.Fatalf("reloaded: %+v", v)
	}
	r2.boot()
	h2.phase(phaseLogin)

	// A new coordination server is a new identity: the old state goes.
	marker := filepath.Join(r2.dir, "tailscaled.state")
	os.WriteFile(marker, []byte("{}"), 0o600)
	if err := r2.configure("maison", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("state kept across coordination servers: %v", err)
	}
	h2.phase(phaseLogin)
	if o := h2.fake().opts; o.ControlURL != "" {
		t.Fatalf("restarted with %+v", o)
	}
	// Default name: stored empty.
	if raw := h.store.get(remoteKey); strings.Contains(raw, "hostname") && !strings.Contains(raw, "maison") {
		t.Fatalf("stored: %s", raw)
	}
}

func TestRemoteLogout(t *testing.T) {
	h := newRemoteHarness(t, "")
	h.r.enable()
	h.phase(phaseLogin)
	f := h.fake()
	if err := h.r.logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.loggedOut || !f.closed {
		t.Fatalf("logout: %+v", f)
	}
	if _, err := os.Stat(h.r.dir); !os.IsNotExist(err) {
		t.Fatalf("state kept: %v", err)
	}
	if v := h.r.current(); v.Enabled || v.Phase != phaseOff {
		t.Fatalf("after logout: %+v", v)
	}
	if raw := h.store.get(remoteKey); !strings.Contains(raw, `"enabled":false`) {
		t.Fatalf("stored: %s", raw)
	}
}

func TestTailnetHandler(t *testing.T) {
	h := tailnetHandler(func() string { return testTSName }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "in")
	}))
	for _, c := range []struct {
		method, host string
		code         int
		hsts         bool
	}{
		{"GET", testTSName, 200, true},
		{"GET", "ERIPHE.tail1234.ts.net:443", 200, true},
		{"GET", "eriphe", http.StatusMovedPermanently, false},
		{"POST", "evil.example", http.StatusMisdirectedRequest, false},
		{"GET", testTSName + ":8443", http.StatusMovedPermanently, false},
	} {
		req, _ := http.NewRequest(c.method, "https://"+c.host+"/x?y=1", nil)
		req.Host = c.host
		rec := newRecorder()
		h.ServeHTTP(rec, req)
		if rec.code != c.code || (rec.h.Get("Strict-Transport-Security") != "") != c.hsts {
			t.Errorf("%s %s: %d, HSTS %q", c.method, c.host, rec.code, rec.h.Get("Strict-Transport-Security"))
		}
		if c.code == http.StatusMovedPermanently && rec.h.Get("Location") != "https://"+testTSName+"/x?y=1" {
			t.Errorf("%s: redirected to %s", c.host, rec.h.Get("Location"))
		}
	}
}

func TestRedactKeys(t *testing.T) {
	in := "login: To authenticate, visit: https://login.tailscale.com/a/1a2b3c4d5e, key " + fakeTSKey
	out := redactKeys(in)
	if strings.Contains(out, "1a2b3c4d5e") || strings.Contains(out, "kAbc123") {
		t.Fatalf("not redacted: %s", out)
	}
}

// recorder is a minimal ResponseWriter.
type recorder struct {
	h    http.Header
	code int
	body strings.Builder
}

func newRecorder() *recorder                    { return &recorder{h: http.Header{}, code: 200} }
func (r *recorder) Header() http.Header         { return r.h }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

// fakeTSKey looks like a Tailscale auth key without being one: it is built
// at run time so that secret scanners do not mistake the fixture for a leak.
var fakeTSKey = "tskey" + "-auth-" + "kAbc123-XYZ"
