package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeConnectCore answers the catalog and connect calls like eriphe-core.
func fakeConnectCore(t *testing.T, finished *map[string]string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/servers", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Server{
			{Name: "github", Auth: "bearer", Status: "ready", ToolCount: 40, Use: "issues and PRs"},
			{Name: "notion", Auth: "oauth", Status: "needs_auth", Error: "not authorized yet", Connect: "eriphe connect notion"},
		})
	})
	mux.HandleFunc("POST /v1/servers/notion/connect", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		auth := "https://auth.example/authorize?state=s1&redirect_uri=" + url.QueryEscape(in["redirect_uri"])
		json.NewEncoder(w).Encode(map[string]string{"flow": "f1", "auth_url": auth})
	})
	mux.HandleFunc("POST /v1/servers/notion/callback", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(finished)
		json.NewEncoder(w).Encode(map[string]any{"status": "ready", "tools": 12})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestConnectLoopback(t *testing.T) {
	var finished map[string]string
	srv := fakeConnectCore(t, &finished)
	c := NewClient(srv.URL, "tok")
	// The "browser" follows the authorization URL back to the redirect.
	browser := func(auth string) error {
		u, _ := url.Parse(auth)
		go http.Get(u.Query().Get("redirect_uri") + "?code=c1&state=s1")
		return nil
	}
	var out bytes.Buffer
	if err := connect(context.Background(), c, &out, "notion", browser, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if finished["flow"] != "f1" || finished["code"] != "c1" || finished["state"] != "s1" {
		t.Fatalf("handed over %v", finished)
	}
	if !strings.Contains(out.String(), "notion is connected: 12 tools") {
		t.Fatalf("output: %s", out.String())
	}
	if err := connect(context.Background(), c, &out, "github", browser, strings.NewReader("")); err == nil {
		t.Fatal("connected a bearer server")
	}
}

func TestConnectPaste(t *testing.T) {
	var finished map[string]string
	srv := fakeConnectCore(t, &finished)
	c := NewClient(srv.URL, "tok")
	pasted := strings.NewReader("nonsense\nhttp://127.0.0.1:9/callback?code=c2&state=s1&iss=https%3A%2F%2Fauth.example\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := connect(ctx, c, &out, "notion", func(string) error { return nil }, pasted); err != nil {
		t.Fatal(err)
	}
	if finished["code"] != "c2" || finished["iss"] != "https://auth.example" {
		t.Fatalf("handed over %v", finished)
	}
}

func TestPrintServers(t *testing.T) {
	var out bytes.Buffer
	printServers(&out, []Server{
		{Name: "github", Auth: "bearer", Status: "ready", ToolCount: 40, Use: "issues"},
		{Name: "notion", Auth: "oauth", Status: "needs_auth", Error: "not authorized yet", Connect: "eriphe connect notion", Source: "workspace"},
	})
	s := out.String()
	if !strings.Contains(s, "github") || !strings.Contains(s, "run `eriphe connect notion`") ||
		!strings.Contains(s, "SOURCE") || !strings.Contains(s, "workspace") || !strings.Contains(s, "config") {
		t.Fatalf("output:\n%s", s)
	}
}
