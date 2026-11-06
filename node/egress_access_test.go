package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const accessHostPort = "registry.example.org:443"

// accessCore stands for eriphe-core: it records what the relay hands it.
type accessCore struct {
	mu   sync.Mutex
	got  []*http.Request
	size int64
}

func (f *accessCore) last(t *testing.T) *http.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("nothing reached eriphe-core")
	}
	return f.got[len(f.got)-1]
}

// accessRelayed is a relay with task t-1 lent one access, in front of a
// fake eriphe-core, and the task's authority.
func accessRelayed(t *testing.T, upstream string) (*testRelay, *accessCore, *taskCA) {
	t.Helper()
	ca, err := newTaskCA("t-1")
	if err != nil {
		t.Fatal(err)
	}
	f := &accessCore{}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.got = append(f.got, r.Clone(r.Context()))
		f.size = n
		f.mu.Unlock()
		w.Header().Set("X-From", "core")
		io.WriteString(w, "from core")
	}))
	t.Cleanup(core.Close)
	r := newTestRelay(t, upstream, public)
	r.accessHosts, r.ca = []string{accessHostPort}, ca
	r.p.access = accessRelayTo(func() string { return core.URL }, func() (string, string) { return "nas", "node-secret" },
		http.DefaultTransport, func(id string) bool { return id == "t-1" })
	return r, f, ca
}

// tlsClient speaks HTTPS through a tunnel the relay opened, trusting roots.
func tlsClient(t *testing.T, r *testRelay, target string, roots *x509.CertPool) *http.Client {
	t.Helper()
	host, _, _ := net.SplitHostPort(target)
	return &http.Client{Transport: &http.Transport{
		DialTLS: func(string, string) (net.Conn, error) {
			code, c, _ := r.connect(t, target, basic("t-1", "s"))
			if code != http.StatusOK {
				return nil, fmt.Errorf("CONNECT: %d", code)
			}
			s := tls.Client(c, &tls.Config{ServerName: host, RootCAs: roots})
			return s, s.Handshake()
		},
	}}
}

func rootsOf(ca *taskCA) *x509.CertPool {
	p := x509.NewCertPool()
	p.AppendCertsFromPEM(ca.pem)
	return p
}

func TestAccessRelayHandsTheRequestToCore(t *testing.T) {
	r, f, ca := accessRelayed(t, upstreamServer(t).Listener.Addr().String())
	client := tlsClient(t, r, accessHostPort, rootsOf(ca))
	req, _ := http.NewRequest("PUT", "https://registry.example.org/pkg%2Fname?tag=latest", strings.NewReader("a body"))
	req.Header.Set("Authorization", "Bearer placeholder")
	// A task naming another host, or writing what only the relay writes.
	req.Host = "evil.example.org"
	req.Header.Set(accessHostHeader, "evil.example.org:443")
	req.Header.Set(accessTargetHeader, "/forged")
	req.Header.Set("Eriphe-Node", "forged")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "from core" || res.Header.Get("X-From") != "core" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	got := f.last(t)
	if got.Method != "PUT" || got.URL.Path != "/v1/node/tasks/t-1/access" || got.URL.RawQuery != "" {
		t.Errorf("request = %s %s", got.Method, got.URL)
	}
	for k, want := range map[string]string{
		accessHostHeader: accessHostPort, accessTargetHeader: "/pkg%2Fname?tag=latest", accessAuthHeader: "Bearer placeholder",
		"Authorization": "Bearer node-secret", "Eriphe-Node": "nas",
	} {
		if v := got.Header.Get(k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if f.size != int64(len("a body")) {
		t.Errorf("body = %d bytes", f.size)
	}
}

// An access's request leaves from eriphe-core: the node dials nothing.
func TestAccessRelayDialsNothing(t *testing.T) {
	r, _, ca := accessRelayed(t, upstreamServer(t).Listener.Addr().String())
	res, err := tlsClient(t, r, accessHostPort, rootsOf(ca)).Get("https://registry.example.org/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(r.dialed) != 0 {
		t.Errorf("dialed %v", r.dialed)
	}
}

// A large upload goes through.
func TestAccessRelayStreamsBodies(t *testing.T) {
	r, f, ca := accessRelayed(t, upstreamServer(t).Listener.Addr().String())
	const size = 8 << 20
	res, err := tlsClient(t, r, accessHostPort, rootsOf(ca)).Post("https://registry.example.org/upload", "application/octet-stream", io.LimitReader(zeros{}, size))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	f.last(t)
	if f.size != size {
		t.Errorf("body = %d bytes", f.size)
	}
}

type zeros struct{}

func (zeros) Read(b []byte) (int, error) { clear(b); return len(b), nil }

// A client that does not trust the task's authority fails closed.
func TestAccessRelayFailsClosed(t *testing.T) {
	r, f, _ := accessRelayed(t, upstreamServer(t).Listener.Addr().String())
	other, _ := newTaskCA("t-2")
	if _, err := tlsClient(t, r, accessHostPort, rootsOf(other)).Get("https://registry.example.org/"); err == nil {
		t.Fatal("trusted another task's authority")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 0 {
		t.Error("the request went on")
	}
}

// Every other host stays the blind tunnel it is, its port rule included.
func TestAccessRelayLeavesOtherHostsAlone(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "the real one") }))
	t.Cleanup(up.Close)
	r, f, _ := accessRelayed(t, up.Listener.Addr().String())
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	code, c, _ := r.connect(t, "example.com:443", basic("t-1", "s"))
	if code != http.StatusOK {
		t.Fatal(code)
	}
	s := tls.Client(c, &tls.Config{ServerName: "example.com", RootCAs: roots})
	fmt.Fprint(s, "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
	res, err := http.ReadResponse(bufio.NewReader(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "the real one" {
		t.Errorf("body = %q", body)
	}
	if len(f.got) != 0 {
		t.Error("it went through eriphe-core")
	}
	// A declared host on a port it does not declare is not its access.
	if code, _, _ := r.connect(t, "registry.example.org:8443", basic("t-1", "s")); code != http.StatusForbidden {
		t.Errorf("another port: %d", code)
	}
}

// A task over reaches nothing, even on a session still open.
func TestAccessRelayStopsWithItsTask(t *testing.T) {
	h := accessRelayTo(func() string { return "http://192.0.2.1" }, func() (string, string) { return "nas", "x" }, http.DefaultTransport,
		func(string) bool { return false })
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), accessTaskKey{}, accessCall{"t-1", accessHostPort}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Errorf("%d", rec.Code)
	}
}

func TestTaskCALeaves(t *testing.T) {
	ca, err := newTaskCA("t-1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ca.pem, []byte("PRIVATE")) {
		t.Fatal("the key is in what the task gets")
	}
	for _, host := range []string{"registry.example.org", "203.0.113.7"} {
		c, err := ca.leaf(host)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: rootsOf(ca)}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
		if err := leaf.VerifyHostname("other.example.org"); err == nil {
			t.Errorf("%s holds for any host", host)
		}
		if again, _ := ca.leaf(host); again != c {
			t.Error("made twice")
		}
	}
	// It issues for its hosts, and nothing under it.
	block, _ := pem.Decode(ca.pem)
	root, _ := x509.ParseCertificate(block.Bytes)
	if !root.IsCA || !root.MaxPathLenZero {
		t.Errorf("authority = %+v", root)
	}
}

func accessTask() *Task {
	return &Task{ID: "t-1", Spec: TaskSpec{Accesses: []TaskAccess{{Name: "npm", Hosts: []string{accessHostPort}, Env: "NPM_TOKEN", Placeholder: "eriphe-access-npm-t-1"}}}}
}

func TestAccessEnv(t *testing.T) {
	if env := accessEnv(&Task{}); env != nil {
		t.Fatalf("no access: %v", env)
	}
	env := accessEnv(accessTask())
	if env["NPM_TOKEN"] != "eriphe-access-npm-t-1" || env["ERIPHE_ACCESS_CA"] != runPath(accessCAFile) {
		t.Fatalf("env = %v", env)
	}
	for _, v := range accessCAVars {
		if env[v] != accessBundle {
			t.Errorf("%s = %q", v, env[v])
		}
	}
}

// The task's certificate is a run file; nothing without accesses.
func TestAccessCAIsARunFile(t *testing.T) {
	files, err := withAccessCA(&Task{}, nil)
	if err != nil || files != nil {
		t.Fatalf("no access: %v, %v", files, err)
	}
	task := accessTask()
	files, err = withAccessCA(task, map[string][]byte{"other": []byte("x")})
	if err != nil || len(files) != 2 {
		t.Fatalf("files = %v, %v", files, err)
	}
	ca, _ := task.authority()
	if !bytes.Equal(files[accessCAFile], ca.pem) {
		t.Error("not its authority")
	}
	if !strings.Contains(entrypoint, accessBundleScript) {
		t.Error("the entrypoint does not write the bundle")
	}
	if strings.Index(entrypoint, accessBundleScript) > strings.Index(entrypoint, "git clone") {
		t.Error("the bundle comes after the clone")
	}
}

// The bundle holds the image's certificates and the task's: a program
// pointed at it still trusts the rest of the Internet.
func TestAccessBundleScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	system := filepath.Join(dir, "system.pem")
	task := filepath.Join(dir, "task.pem")
	bundle := filepath.Join(dir, "bundle.pem")
	os.WriteFile(system, []byte("SYSTEM\n"), 0o600)
	os.WriteFile(task, []byte("TASK\n"), 0o600)
	script := strings.ReplaceAll(accessBundleScript, accessBundle, bundle)
	script = strings.ReplaceAll(script, "/etc/ssl/certs/ca-certificates.crt", system)
	cmd := exec.Command("sh", "-ec", script)
	cmd.Env = []string{"ERIPHE_ACCESS_CA=" + task, "PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got, _ := os.ReadFile(bundle)
	if string(got) != "SYSTEM\nTASK\n" {
		t.Errorf("bundle = %q", got)
	}
}

func TestAccessTarget(t *testing.T) {
	for in, want := range map[string]string{
		"Registry.Example.org.:443": accessHostPort, accessHostPort: accessHostPort, "[2001:db8::1]:443": "[2001:db8::1]:443", "no-port": "",
	} {
		if got := accessTarget(in); got != want {
			t.Errorf("accessTarget(%q) = %q", in, got)
		}
	}
}

// A real client, told only what a task's environment tells it: the relay,
// and the bundle.
func TestAccessWithARealClient(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("no curl")
	}
	r, f, ca := accessRelayed(t, upstreamServer(t).Listener.Addr().String())
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	os.WriteFile(bundle, ca.pem, 0o600)
	cmd := exec.Command(curl, "-sS", "--fail", "-H", "Authorization: Bearer placeholder", "--data-binary", "a body", "https://registry.example.org/pkg")
	cmd.Env = []string{"HTTPS_PROXY=" + egressURL("127.0.0.1", r.srv.Listener.Addr().(*net.TCPAddr).AddrPort().String()[len("127.0.0.1:"):], "t-1", "s"),
		"CURL_CA_BUNDLE=" + bundle, "SSL_CERT_FILE=" + bundle}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if string(out) != "from core" {
		t.Fatalf("curl = %q", out)
	}
	if got := f.last(t); got.Header.Get(accessAuthHeader) != "Bearer placeholder" || got.Header.Get(accessHostHeader) != accessHostPort {
		t.Errorf("headers = %v", got.Header)
	}
}
