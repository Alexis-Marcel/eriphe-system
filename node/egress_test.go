package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1": true, "140.82.121.4": true, "2606:4700:4700::1111": true,
		"192.168.0.2": false, "192.168.0.1": false, "10.0.0.1": false, "172.16.5.4": false,
		"127.0.0.1": false, "::1": false, "169.254.169.254": false, "fe80::1": false,
		"100.64.0.1": false, "100.100.100.100": false, "fd00::1": false, "fc00::1": false,
		"0.0.0.0": false, "::": false, "224.0.0.1": false, "ff02::1": false, "255.255.255.255": false,
		"::ffff:192.168.0.1": false, "::ffff:1.1.1.1": true, "64:ff9b::c0a8:1": false, "2002:c0a8:1::1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddr(%s) = %v", addr, got)
		}
	}
}

// The socket-level guard refuses a private address whatever resolved it.
func TestGuardControl(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	d := &net.Dialer{Control: guardControl}
	if c, err := d.Dial("tcp", l.Addr().String()); err == nil {
		c.Close()
		t.Fatal("dialed the loopback")
	}
}

func TestHostAllowed(t *testing.T) {
	allow := []string{"github.com", "*.npmjs.org"}
	for host, want := range map[string]bool{
		"github.com": true, "GitHub.com.": true, "api.github.com": false, "evilgithub.com": false,
		"registry.npmjs.org": true, "a.b.npmjs.org": true, "npmjs.org": false, "npmjs.org.evil.com": false,
	} {
		if got := hostAllowed(allow, host); got != want {
			t.Errorf("hostAllowed(%s) = %v", host, got)
		}
	}
}

// testRelay is a relay with a fake resolver, whose public addresses all
// lead to upstream, and one task, t-1, with secret s.
type testRelay struct {
	p       *egressProxy
	srv     *httptest.Server
	mu      sync.Mutex
	dialed  []string
	logs    []string
	rule    EgressRule
	lookups int
}

func newTestRelay(t *testing.T, upstream string, resolve func(n int, host string) []string) *testRelay {
	r := &testRelay{rule: EgressRule{Mode: egressInternet}}
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "node listener") }))
	t.Cleanup(listener.Close)
	seen := map[string]bool{}
	r.p = newEgressProxy(func(id, secret string) (*egressTask, bool) {
		if id != "t-1" || secret != "s" {
			return nil, false
		}
		return &egressTask{id: id, rule: r.rule, seen: func(h string) bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			if seen[h] {
				return false
			}
			seen[h] = true
			return true
		}}, true
	}, []string{"eriphe-node:8790"}, listener.Listener.Addr().String())
	r.p.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		r.mu.Lock()
		r.lookups++
		n := r.lookups
		r.mu.Unlock()
		var out []netip.Addr
		for _, a := range resolve(n, host) {
			out = append(out, netip.MustParseAddr(a))
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no such host %s", host)
		}
		return out, nil
	}
	r.p.dialPublic = func(ctx context.Context, network, addr string) (net.Conn, error) {
		r.mu.Lock()
		r.dialed = append(r.dialed, addr)
		r.mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, network, upstream)
	}
	r.p.logf = func(format string, args ...any) {
		r.mu.Lock()
		r.logs = append(r.logs, fmt.Sprintf(format, args...))
		r.mu.Unlock()
	}
	r.srv = httptest.NewServer(r.p)
	t.Cleanup(r.srv.Close)
	return r
}

func basic(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// connect sends a CONNECT through the relay and returns the status, and the
// tunnel when it is open.
func (r *testRelay) connect(t *testing.T, target, auth string) (int, net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", r.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if auth != "" {
		fmt.Fprintf(c, "Proxy-Authorization: %s\r\n", auth)
	}
	io.WriteString(c, "\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		c.Close()
		return resp.StatusCode, nil, nil
	}
	t.Cleanup(func() { c.Close() })
	return resp.StatusCode, c, br
}

func upstreamServer(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "upstream host=%s proxy-auth=%q", r.Host, r.Header.Get("Proxy-Authorization"))
	}))
	t.Cleanup(s.Close)
	return s
}

func public(int, string) []string { return []string{"140.82.121.4"} }

func TestEgressAuth(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), public)
	for _, auth := range []string{"", basic("t-1", "wrong"), basic("t-2", "s"), "Bearer s"} {
		if code, _, _ := r.connect(t, "github.com:443", auth); code != http.StatusProxyAuthRequired {
			t.Errorf("auth %q: %d", auth, code)
		}
	}
	// A plain request too, and the challenge clients like git wait for.
	resp := proxyGet(t, r, "http://example.com/", "")
	if resp.StatusCode != http.StatusProxyAuthRequired || !strings.HasPrefix(resp.Header.Get("Proxy-Authenticate"), "Basic") {
		t.Errorf("plain without auth: %d %v", resp.StatusCode, resp.Header)
	}
}

func proxyGet(t *testing.T, r *testRelay, target, auth string) *http.Response {
	t.Helper()
	c, err := net.Dial("tcp", r.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	u, _ := url.Parse(target)
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", target, u.Host)
	if auth != "" {
		fmt.Fprintf(c, "Proxy-Authorization: %s\r\n", auth)
	}
	io.WriteString(c, "\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func body(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestEgressConnect(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), public)
	code, c, br := r.connect(t, "github.com:443", basic("t-1", "s"))
	if code != http.StatusOK {
		t.Fatalf("CONNECT: %d", code)
	}
	// Through the tunnel, any protocol: here HTTP to the fake upstream.
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: github.com\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := body(resp); !strings.Contains(b, "upstream host=github.com") {
		t.Errorf("through the tunnel: %s", b)
	}
	if len(r.dialed) != 1 || r.dialed[0] != "140.82.121.4:443" {
		t.Errorf("dialed %v", r.dialed)
	}
	// One log line per host, however many connections.
	r.connect(t, "github.com:443", basic("t-1", "s"))
	if len(r.logs) != 1 || r.logs[0] != "task t-1: egress to github.com" {
		t.Errorf("logs = %q", r.logs)
	}
}

func TestEgressPlainHTTP(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), public)
	resp := proxyGet(t, r, "http://example.com/x", basic("t-1", "s"))
	b := body(resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(b, "upstream host=example.com") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if !strings.Contains(b, `proxy-auth=""`) {
		t.Errorf("the task's credentials went upstream: %s", b)
	}
	if len(r.dialed) != 1 || r.dialed[0] != "140.82.121.4:80" {
		t.Errorf("dialed %v", r.dialed)
	}
}

func TestEgressRefusesPrivate(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), func(_ int, host string) []string {
		switch host {
		case "nas.lan":
			return []string{"192.168.0.2"}
		case "192.168.0.1.nip.io": // a name made to resolve to the router
			return []string{"192.168.0.1"}
		case "metadata.example":
			return []string{"169.254.169.254"}
		case "mixed.example":
			return []string{"10.0.0.7", "140.82.121.4"}
		}
		return nil
	})
	auth := basic("t-1", "s")
	for _, target := range []string{"192.168.0.2:443", "192.168.0.1:80", "169.254.169.254:80", "[::1]:443", "127.0.0.1:443",
		"[fd00::1]:443", "100.100.100.100:443", "nas.lan:443", "192.168.0.1.nip.io:443", "metadata.example:80"} {
		if code, _, _ := r.connect(t, target, auth); code != http.StatusForbidden {
			t.Errorf("CONNECT %s: %d", target, code)
		}
	}
	if resp := proxyGet(t, r, "http://169.254.169.254/latest/meta-data/", auth); resp.StatusCode != http.StatusForbidden {
		t.Errorf("plain metadata: %d", resp.StatusCode)
	}
	if len(r.dialed) != 0 {
		t.Errorf("dialed %v", r.dialed)
	}
	// Only the public address of a name that has both.
	if code, _, _ := r.connect(t, "mixed.example:443", auth); code != http.StatusOK || len(r.dialed) != 1 || r.dialed[0] != "140.82.121.4:443" {
		t.Errorf("mixed: %d, dialed %v", code, r.dialed)
	}
}

// DNS rebinding: a name public when checked, private right after. The relay
// resolves once per connection and dials the address it checked.
func TestEgressRebinding(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), func(n int, _ string) []string {
		if n%2 == 1 {
			return []string{"140.82.121.4"}
		}
		return []string{"192.168.0.1"}
	})
	auth := basic("t-1", "s")
	if code, _, _ := r.connect(t, "rebind.example:443", auth); code != http.StatusOK {
		t.Fatalf("first: %d", code)
	}
	if code, _, _ := r.connect(t, "rebind.example:443", auth); code != http.StatusForbidden {
		t.Errorf("rebound: %d", code)
	}
	if r.lookups != 2 || len(r.dialed) != 1 || r.dialed[0] != "140.82.121.4:443" {
		t.Errorf("lookups %d, dialed %v", r.lookups, r.dialed)
	}
}

func TestEgressRules(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), public)
	auth := basic("t-1", "s")
	if code, _, _ := r.connect(t, "github.com:22", auth); code != http.StatusForbidden {
		t.Errorf("port 22: %d", code)
	}
	r.rule = EgressRule{Mode: egressInternet, Allow: []string{"github.com", "*.npmjs.org"}}
	for target, want := range map[string]int{"github.com:443": 200, "registry.npmjs.org:443": 200, "example.com:443": 403} {
		if code, _, _ := r.connect(t, target, auth); code != want {
			t.Errorf("allowlist %s: %d", target, code)
		}
	}
	r.rule = EgressRule{Mode: egressNone}
	if code, _, _ := r.connect(t, "github.com:443", auth); code != http.StatusForbidden {
		t.Errorf("mode none: %d", code)
	}
}

// The node's own listener, named as in the callback URL, is reachable
// through the relay: for a client that ignores NO_PROXY.
func TestEgressSelf(t *testing.T) {
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), func(int, string) []string { return []string{"172.18.0.2"} })
	resp := proxyGet(t, r, "http://eriphe-node:8790/perm/t-1/mcp", basic("t-1", "s"))
	if b := body(resp); resp.StatusCode != 200 || b != "node listener" {
		t.Errorf("%d %s", resp.StatusCode, b)
	}
	if code, _, _ := r.connect(t, "eriphe-node:8791", basic("t-1", "s")); code != http.StatusForbidden {
		t.Errorf("another port of the node: %d", code)
	}
}

func TestEgressAuthorizer(t *testing.T) {
	n := newNode(options{})
	task := &Task{ID: "t-1", secret: "s", State: stateRunning}
	n.tasks["t-1"] = task
	if et, ok := n.egressAuthorizer("t-1", "s"); !ok || et.rule.Mode != egressInternet {
		t.Errorf("default rule: %v %+v", ok, et)
	}
	task.Spec.Egress = &EgressRule{Mode: egressNone}
	if et, ok := n.egressAuthorizer("t-1", "s"); !ok || et.rule.Mode != egressNone {
		t.Errorf("spec rule: %+v", et)
	}
	if _, ok := n.egressAuthorizer("t-1", "x"); ok {
		t.Error("wrong secret accepted")
	}
	task.State = stateStopped
	if _, ok := n.egressAuthorizer("t-1", "s"); ok {
		t.Error("a finished task still has egress")
	}
	if !task.egressSeen("a") || task.egressSeen("a") {
		t.Error("egressSeen")
	}
}

func TestEgressEnv(t *testing.T) {
	n := newNode(options{callbackURL: "http://host.docker.internal:8790", listenAddr: "172.17.0.1:8790", egressPort: "8791"})
	task := &Task{ID: "t-1", secret: "s"}
	env := n.egressEnv(task)
	if env["https_proxy"] != "http://t-1:s@host.docker.internal:8791" || env["no_proxy"] != "host.docker.internal,localhost,127.0.0.1,172.17.0.1" {
		t.Errorf("env = %v", env)
	}
	// The brain goes out through the relay too, its MCP servers direct.
	task.Spec.SystemNetwork = true
	task.Spec.MCPServers = map[string]any{"eriphe-core": map[string]any{"type": "http", "url": "http://eriphe-core:8800/mcp"}}
	if env := n.egressEnv(task); env["https_proxy"] == "" || !strings.HasSuffix(env["no_proxy"], ",eriphe-core") {
		t.Errorf("the brain: %v", env)
	}
	n.opt.egressPort = ""
	if env := n.egressEnv(&Task{ID: "t-2"}); env != nil {
		t.Errorf("no relay: %v", env)
	}
}

func TestEgressListenAddr(t *testing.T) {
	if got, _ := egressListenAddr(":8791", "172.18.0.2:8790"); got != "172.18.0.2:8791" {
		t.Errorf("got %s", got)
	}
	if got, _ := egressListenAddr("0.0.0.0:9000", "172.18.0.2:8790"); got != "0.0.0.0:9000" {
		t.Errorf("got %s", got)
	}
}

// The docker CLI gets the task's env to pass it on: a tcp daemon must be in
// NO_PROXY, or the CLI would dial it through the relay.
func TestDockerDaemonHost(t *testing.T) {
	for in, want := range map[string]string{
		"tcp://eriphe-docker-proxy:2375": "eriphe-docker-proxy",
		"unix:///var/run/docker.sock":    "",
		"":                               "",
	} {
		if got := dockerDaemonHost(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
