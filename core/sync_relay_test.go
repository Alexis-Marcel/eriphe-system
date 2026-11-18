package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// attachSyncDevice connects a fake device whose Syncthing echoes what it reads,
// as eriphe-device hands a peer's connection to its own (device/syncpeer.go).
// peers receives the peer each connection came for.
func attachSyncDevice(t *testing.T, c *core, name string, peers chan<- string) {
	t.Helper()
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/device", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{}`)) })
	api.HandleFunc("GET /v1/sync/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != syncProto {
			http.Error(w, "upgrade", http.StatusUpgradeRequired)
			return
		}
		peers <- r.Header.Get("Eriphe-Sync-Peer")
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: eriphe-sync\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		io.Copy(conn, rw)
		conn.Close()
	})
	coreEnd, deviceEnd := net.Pipe()
	deviceSession, _ := yamux.Server(deviceEnd, tunnelConfig())
	go http.Serve(deviceSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach(name, coreSession, false)
	t.Cleanup(func() { coreSession.Close() })
	waitUntil(t, func() bool { return c.device(name).connected() })
}

// syncDial asks eriphe-core for a way to peer, as device name.
func syncDial(t *testing.T, srv *httptest.Server, name, secret, peer string) (net.Conn, *bufio.Reader, int) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "GET /v1/device/sync/%s HTTP/1.1\r\nHost: core\r\nAuthorization: Bearer %s\r\nEriphe-Device: %s\r\nConnection: Upgrade\r\nUpgrade: eriphe-sync\r\n\r\n", peer, secret, name)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, nil, resp.StatusCode
	}
	t.Cleanup(func() { conn.Close() })
	return conn, br, resp.StatusCode
}

// eriphe-core joins two machines' Syncthing only when it set up a copy
// between them, whichever way; it refuses any other pair, a wrong secret,
// a peer away and too many connections, and cuts a pair's connections when
// its last copy goes.
func TestSyncRelay(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{}, store)
	for _, n := range []string{"mac", "nas", "pc"} {
		store.registerDevice(n, hashSecret(n+"-secret"))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/device/sync/{peer}", c.handleDeviceSync)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// No copy between them yet.
	if _, _, s := syncDial(t, srv, "mac", "mac-secret", "nas"); s != http.StatusForbidden {
		t.Fatalf("no copy: %d", s)
	}
	store.addCopy("mac", "f1", "nas")
	if _, _, s := syncDial(t, srv, "mac", "mac-secret", "nas"); s != http.StatusBadGateway {
		t.Fatalf("peer away: %d", s)
	}
	peers := make(chan string, 8)
	attachSyncDevice(t, c, "nas", peers)
	attachSyncDevice(t, c, "mac", peers)

	for name, tc := range map[string]struct {
		device, secret, peer string
		want                 int
	}{
		"wrong secret":   {"mac", "nope", "nas", http.StatusUnauthorized},
		"another pair":   {"pc", "pc-secret", "nas", http.StatusForbidden},
		"itself":         {"mac", "mac-secret", "mac", http.StatusForbidden},
		"not a name":     {"mac", "mac-secret", "NAS", http.StatusForbidden},
		"another way in": {"pc", "pc-secret", "mac", http.StatusForbidden},
	} {
		if _, _, s := syncDial(t, srv, tc.device, tc.secret, tc.peer); s != tc.want {
			t.Errorf("%s: %d, want %d", name, s, tc.want)
		}
	}

	// Both ways: the folder's machine to the copy's, and back.
	echo := func(conn net.Conn, br *bufio.Reader, msg string) {
		t.Helper()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(conn, msg)
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(br, got); err != nil || string(got) != msg {
			t.Fatalf("echo %q, %v", got, err)
		}
	}
	a, abr, _ := syncDial(t, srv, "mac", "mac-secret", "nas")
	echo(a, abr, "from mac")
	if p := <-peers; p != "mac" {
		t.Fatalf("nas was told the connection comes from %q", p)
	}
	b, bbr, _ := syncDial(t, srv, "nas", "nas-secret", "mac")
	echo(b, bbr, "from nas")
	if p := <-peers; p != "nas" {
		t.Fatalf("mac was told the connection comes from %q", p)
	}

	// Two connections held: two more fill the pair.
	syncDial(t, srv, "mac", "mac-secret", "nas")
	syncDial(t, srv, "mac", "mac-secret", "nas")
	<-peers
	<-peers
	if _, _, s := syncDial(t, srv, "mac", "mac-secret", "nas"); s != http.StatusTooManyRequests {
		t.Errorf("a fifth connection: %d", s)
	}

	// The last copy between them goes: the connections are cut.
	store.removeCopy("mac", "f1", "nas")
	c.cutUnpaired()
	a.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := abr.ReadByte(); err == nil {
		t.Fatal("a connection still open after the last copy went")
	}
	if _, _, s := syncDial(t, srv, "mac", "mac-secret", "nas"); s != http.StatusForbidden {
		t.Errorf("after the copy went: %d", s)
	}
}
