package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// keepOffTheNetwork makes http.DefaultTransport, which the core's clients
// fall back to or clone (pool.http, OAuth, discovery, providers, a tool's
// test), dial this machine only: a test that would go out (AGENTS.md:
// tests need no network) fails at once instead of waiting on a real
// server.
func keepOffTheNetwork() {
	t := http.DefaultTransport.(*http.Transport)
	dial := t.DialContext
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || !isLoopbackHost(host) {
			return nil, fmt.Errorf("test transport: %s is not on this machine, tests stay off the network", addr)
		}
		return dial(ctx, network, addr)
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// The guard refuses a host elsewhere before any dial, and lets this
// machine's servers through.
func TestTestsStayOffTheNetwork(t *testing.T) {
	_, err := http.Get("https://example.org/")
	if err == nil || !strings.Contains(err.Error(), "tests stay off the network") {
		t.Fatalf("went out: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
