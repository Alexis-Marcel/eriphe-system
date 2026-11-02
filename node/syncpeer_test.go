package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoServer stands for a Syncthing: it sends back what it reads.
func echoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return l.Addr().String()
}

// upgradeTo asks srv for an eriphe-sync connection as eriphe-core does, from peer.
func upgradeTo(srv *httptest.Server, peer string) (net.Conn, *bufio.Reader, int, error) {
	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		return nil, nil, 0, err
	}
	fmt.Fprintf(c, "GET /v1/sync/connect HTTP/1.1\r\nHost: node\r\nConnection: Upgrade\r\nUpgrade: eriphe-sync\r\nEriphe-Sync-Peer: %s\r\n\r\n", peer)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		c.Close()
		return nil, nil, 0, err
	}
	return c, br, resp.StatusCode, nil
}

// Syncthing on machine a reaches machine b's at a loopback port of a's
// node; b's node takes the connection (here straight from the test, as
// eriphe-core hands it over) only from a peer it was told about, and hands
// it to its own Syncthing.
func TestSyncPeerWay(t *testing.T) {
	b := newSyncNet(t.TempDir(), echoServer(t), 0, nil)
	b.setPeers([]string{"a"})
	defer b.close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sync/connect", b.handleSyncConnect)
	coreSide := httptest.NewServer(mux)
	defer coreSide.Close()

	asked := make(chan string, 4)
	a := newSyncNet(t.TempDir(), "127.0.0.1:1", 0, func(_ context.Context, peer string) (net.Conn, error) {
		asked <- peer
		c, br, status, err := upgradeTo(coreSide, "a")
		if err != nil || status != http.StatusSwitchingProtocols {
			return nil, fmt.Errorf("%d %v", status, err)
		}
		if br.Buffered() > 0 {
			t.Error("bytes before the switch")
		}
		return c, nil
	})
	ports, err := a.setPeers([]string{"b", "Not A Name"})
	if err != nil || len(ports) != 1 || ports["b"] == 0 {
		t.Fatalf("ports %v, %v", ports, err)
	}
	defer a.close()

	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", ports["b"]))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "hello b")
	got := make([]byte, 7)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello b" {
		t.Fatalf("echo %q, %v", got, err)
	}
	if p := <-asked; p != "b" {
		t.Fatalf("asked eriphe-core for %q", p)
	}

	// Refused: a peer b was not told about, a plain request.
	if _, _, status, _ := upgradeTo(coreSide, "c"); status != http.StatusForbidden {
		t.Errorf("unknown peer: %d", status)
	}
	resp, err := http.Get(coreSide.URL + "/v1/sync/connect")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUpgradeRequired {
			t.Errorf("no upgrade: %d", resp.StatusCode)
		}
	}
	// A machine without Syncthing takes nothing.
	none := newSyncNet(t.TempDir(), "", 0, nil)
	rec := httptest.NewRecorder()
	none.handleSyncConnect(rec, httptest.NewRequest("GET", "/v1/sync/connect", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no Syncthing: %d", rec.Code)
	}

	// A peer no longer named loses its port; a peer named again gets its
	// old one back.
	old := ports["b"]
	a.setPeers(nil)
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", old), time.Second); err == nil {
		t.Error("the port of a peer no longer named still answers")
	}
	again, _ := a.setPeers([]string{"b"})
	if again["b"] != old {
		t.Errorf("port %d, then %d", old, again["b"])
	}
}

// The rate holds what goes into the tunnel.
func TestByteRate(t *testing.T) {
	r := newByteRate(100 << 10) // 100 KiB/s, a second's worth at first
	start := time.Now()
	r.wait(100 << 10)
	r.wait(50 << 10)
	if d := time.Since(start); d < 400*time.Millisecond || d > 2*time.Second {
		t.Fatalf("150 KiB at 100 KiB/s took %s", d)
	}
	var none *byteRate
	none.wait(1 << 30) // no limit: no wait
}

func TestSyncthingListen(t *testing.T) {
	for addr, ok := range map[string]bool{
		"":                  true,
		"127.0.0.1:22000":   true,
		"[::1]:22000":       true,
		"0.0.0.0:22000":     false,
		"192.0.2.10:22000":  false,
		"localhost:22000":   false,
		"127.0.0.1":         false,
		"127.0.0.1:99999":   false,
		"syncthing:22000":   false,
		"127.0.0.1:22000/x": false,
	} {
		if err := checkSyncthingListen(addr); (err == nil) != ok {
			t.Errorf("%q: %v", addr, err)
		}
	}
}
