package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// deadURL is an address nothing listens on.
func deadURL(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}

func TestParseCores(t *testing.T) {
	got := parseCores(" http://192.168.0.2:8800/, https://eriphe.tail1234.ts.net:8800 http://192.168.0.2:8800")
	want := []string{"http://192.168.0.2:8800", "https://eriphe.tail1234.ts.net:8800"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseCores: %q", got)
	}
	if parseCores("") != nil {
		t.Fatal("empty")
	}
}

// A node.json of an older version (one address) still loads, and keeps its
// format until a second address is added.
func TestStateCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.json")
	os.WriteFile(path, []byte(`{"core":"http://nas:8800","name":"pc","secret":"s"}`), 0o600)
	s, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.urls(), []string{"http://nas:8800"}) || s.base() != "http://nas:8800" {
		t.Fatalf("old state: %v %s", s.urls(), s.base())
	}
	if s.setURLs([]string{"http://nas:8800"}) {
		t.Fatal("same address: changed")
	}
	if !s.setURLs([]string{"http://nas:8800", "https://eriphe.tail1234.ts.net:8800"}) {
		t.Fatal("not changed")
	}
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	b, _ := os.ReadFile(path)
	json.Unmarshal(b, &raw)
	if raw["core"] != "http://nas:8800" || len(raw["cores"].([]any)) != 2 || raw["secret"] != "s" {
		t.Fatalf("saved: %s", b)
	}
	s.setURLs([]string{"https://eriphe.tail1234.ts.net:8800"})
	s.save(path)
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "cores") {
		t.Fatalf("one address, saved with cores: %s", b)
	}
}

func TestJoinFallback(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nodes/join" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"secret":"the-secret"}`)
	}))
	defer core.Close()
	path := filepath.Join(t.TempDir(), "node.json")
	lan := deadURL(t)
	s, err := join(path, []string{lan, core.URL}, "laptop", "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Secret != "the-secret" || s.Core != lan || !slices.Equal(s.Cores, []string{lan, core.URL}) {
		t.Fatalf("joined: %+v", s)
	}
	saved, _ := loadState(path)
	if !slices.Equal(saved.urls(), []string{lan, core.URL}) {
		t.Fatalf("saved: %v", saved.urls())
	}
}

// The tunnel goes to the first address that answers; one that refuses the
// node stops the search (it is eriphe-core, and it said no).
func TestConnectFallback(t *testing.T) {
	var connected atomic.Int32
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nodes/connect" || r.Header.Get("Authorization") != "Bearer s" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		connected.Add(1)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: eriphe-tunnel\r\n\r\n")
		buf.Flush()
		conn.Close() // the tunnel drops at once
	}))
	defer away.Close()
	lan := deadURL(t)
	s := &state{Core: lan, Cores: []string{lan, away.URL}, Name: "laptop", Secret: "s"}
	err := connect(s, http.NotFoundHandler())
	if err == nil || errors.Is(err, errRejected) || connected.Load() != 1 {
		t.Fatalf("connect: %v, %d", err, connected.Load())
	}
	if s.base() != away.URL {
		t.Fatalf("active: %s", s.base())
	}

	refuses := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer refuses.Close()
	s = &state{Core: refuses.URL, Cores: []string{refuses.URL, away.URL}, Name: "laptop", Secret: "s"}
	if err := connect(s, http.NotFoundHandler()); !errors.Is(err, errRejected) || connected.Load() != 1 {
		t.Fatalf("refused: %v, %d", err, connected.Load())
	}

	s = &state{Core: lan, Cores: []string{lan, deadURL(t)}, Name: "laptop", Secret: "s"}
	var unreachable *unreachableError
	if err := connect(s, http.NotFoundHandler()); !errors.As(err, &unreachable) {
		t.Fatalf("none reachable: %v", err)
	}
}

// On the remote address, the node moves back to the local one once it answers.
func TestPreferred(t *testing.T) {
	var up atomic.Bool
	lan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() || r.URL.Path != "/health" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	}))
	defer lan.Close()
	away := "https://eriphe.tail1234.ts.net:8800"
	s := &state{Core: lan.URL, Cores: []string{lan.URL, away}}
	moved := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go preferred(ctx, s, away, 10*time.Millisecond, func() { close(moved) })
	select {
	case <-moved:
		t.Fatal("moved while the local address is down")
	case <-time.After(60 * time.Millisecond):
	}
	up.Store(true)
	select {
	case <-moved:
	case <-time.After(3 * time.Second):
		t.Fatal("did not move back")
	}
	// Already on the preferred address: nothing to watch.
	done := make(chan struct{})
	go func() {
		preferred(ctx, s, lan.URL, time.Millisecond, func() { t.Error("moved from the preferred address") })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watched the preferred address")
	}
}
