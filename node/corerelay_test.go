//go:build !windows

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The relay's list of addresses: from the user's files only, read without
// following a symlink, strictly.
func TestCoreAddrs(t *testing.T) {
	dir := t.TempDir()
	me := os.Getuid()
	os.MkdirAll(filepath.Join(dir, "node"), 0o700)
	env := filepath.Join(dir, "node.env")
	os.WriteFile(env, []byte("ERIPHE_NODE_NAME='m'\nERIPHE_CORE='http://192.0.2.10:8800,https://eriphe.example.ts.net:8800'\n"+
		"ERIPHE_CORE=http://192.0.2.99:1\nERIPHE_CORE='http://user:pw@192.0.2.98:1,http://192.0.2.97:1/x?y,file:///etc/passwd,http://192.0.2.96:99999'\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "node", "node.json"), []byte(`{"core": "https://Eriphe.Example.ts.net:8800", "cores": ["http://192.0.2.11"], "secret": "s"}`), 0o600)
	got := coreAddrs(dir, me)
	want := []string{"192.0.2.10:8800", "eriphe.example.ts.net:8800", "192.0.2.11:80"}
	if len(got) != len(want) {
		t.Errorf("addresses %v, want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("no %s in %v", w, got)
		}
	}
	// Someone else's files, a symlink, too big: nothing from them.
	if got := coreAddrs(dir, me+1); len(got) != 0 {
		t.Errorf("another owner: %v", got)
	}
	os.Rename(env, env+".real")
	os.Symlink(env+".real", env)
	if got := coreAddrs(dir, me); got["192.0.2.10:8800"] {
		t.Errorf("a symlink was followed: %v", got)
	}
	os.Remove(env)
	os.WriteFile(env, []byte("ERIPHE_CORE='http://192.0.2.10:8800'\n"+strings.Repeat("#", relayFileMax)), 0o600)
	if got := coreAddrs(dir, me); got["192.0.2.10:8800"] {
		t.Errorf("a file too big was read: %v", got)
	}
	os.Rename(filepath.Join(dir, "node"), filepath.Join(dir, "node.real"))
	os.Symlink(filepath.Join(dir, "node.real"), filepath.Join(dir, "node"))
	if got := coreAddrs(dir, me); got["192.0.2.11:80"] {
		t.Errorf("a symlinked node/ was followed: %v", got)
	}
}

// The node reaches eriphe-core through the core relay, and only eriphe-core.
func TestCoreRelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "eriphe-core") }))
	defer srv.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "something else") }))
	defer other.Close()

	dir, _ := os.MkdirTemp("", "relay") // short: a Unix socket's path is limited
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "node.env"), []byte("ERIPHE_CORE='"+srv.URL+"'\n"), 0o600)
	sock := filepath.Join(dir, "run", "relay.sock")
	l, err := listenRelay(sock, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket %v %v", fi.Mode(), err)
	}
	go serveRelay(l, func() map[string]bool { return coreAddrs(dir, os.Getuid()) }, (&net.Dialer{}).DialContext)

	get := func(u string) (string, error) {
		c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialRelay(ctx, sock, addr)
		}}}
		resp, err := c.Get(u)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	for i := 0; i < 3; i++ {
		if got, err := get(srv.URL); err != nil || got != "eriphe-core" {
			t.Fatalf("eriphe-core: %q %v", got, err)
		}
	}
	if _, err := get(other.URL); err == nil || !strings.Contains(err.Error(), "refuses") {
		t.Fatalf("another address: %v", err)
	}
	// A line that is not host:port, or too long: refused, nothing dialed.
	for _, bad := range []string{"not an address\n", strings.Repeat("a", 400) + ":1\n"} {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(c, bad)
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		b, _ := io.ReadAll(c)
		c.Close()
		if strings.Contains(string(b), "ok") {
			t.Errorf("%q: %q", bad[:20], b)
		}
	}

	// dialCore: the relay, but the loopback directly, and directly when no
	// relay answers.
	old := coreRelaySocket
	defer func() { coreRelaySocket = old }()
	coreRelaySocket = filepath.Join(dir, "none.sock")
	c, err := dialCore(context.Background(), "tcp", other.Listener.Addr().String())
	if err != nil {
		t.Fatalf("no relay: %v", err)
	}
	c.Close()
	if !loopback("127.0.0.1:8800") || !loopback("localhost:8800") || loopback("192.0.2.10:8800") {
		t.Error("loopback")
	}
}
