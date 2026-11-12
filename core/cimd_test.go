package main

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The document published on GitHub Pages is the client eriphe-core names:
// same client_id, the one redirect, a public client, client_uri on the
// client_id's origin (strict servers check it).
func TestBuiltinClientDocument(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "site", "oauth", "client.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ClientID   string   `json:"client_id"`
		ClientURI  string   `json:"client_uri"`
		Redirects  []string `json:"redirect_uris"`
		AuthMethod string   `json:"token_endpoint_auth_method"`
		Grants     []string `json:"grant_types"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ClientID != builtinClientID || len(doc.Redirects) != 1 || doc.Redirects[0] != builtinRedirect || doc.AuthMethod != "none" {
		t.Fatalf("document = %+v", doc)
	}
	id, _ := url.Parse(doc.ClientID)
	home, _ := url.Parse(doc.ClientURI)
	if id.Scheme != "https" || home.Scheme != id.Scheme || home.Host != id.Host {
		t.Fatalf("client_uri %s is not on the origin of %s", doc.ClientURI, doc.ClientID)
	}
	if !loopbackRedirect(builtinRedirect) {
		t.Fatalf("%s is not a loopback redirect", builtinRedirect)
	}
}

// A server that takes Client ID Metadata Documents only is connected with
// Eriphe's published client, on its loopback redirect; nothing registers.
func TestConnectWithBuiltinClient(t *testing.T) {
	f := newFakeAS(t)
	f.cimdOnly = true
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c, stop := oauthCore(t, store, f)
	defer stop()
	waitStatus(t, c.pool.get("notion"), statusNeedsAuth)
	if !c.usesBuiltinClient(context.Background(), "notion", c.conf().Servers["notion"], nil) {
		t.Fatal("a CIMD-only server does not use the published client")
	}

	// Another loopback port is refused: the document names one redirect.
	mux := http.NewServeMux()
	c.serverRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var refused string
	if code := call(t, srv, "POST", "/v1/servers/notion/connect", `{"redirect_uri":"http://127.0.0.1:9123/callback"}`, &refused); code != http.StatusBadRequest {
		t.Fatalf("another port: %d", code)
	}

	done := connectFlow(t, c, f, builtinRedirect)
	if done["status"] != statusReady || done["tools"] != float64(1) {
		t.Fatalf("after connect: %v", done)
	}
	if f.registrations != 0 {
		t.Fatalf("registered %d times", f.registrations)
	}
	cfg, tok, _ := store.oauthToken("notion")
	if cfg == nil || cfg.ClientID != builtinClientID || tok == nil || tok.RefreshToken == "" {
		t.Fatalf("stored = %+v %+v", cfg, tok)
	}
	if reg := store.oauthClient("notion"); reg != nil {
		t.Fatalf("a client registration was kept: %+v", reg)
	}
	// The CLI's hint: the published client's redirect.
	if s := c.listServers("notion", false); s[0].RedirectURI != builtinRedirect {
		t.Fatalf("hint = %q", s[0].RedirectURI)
	}
	if res, err := c.pool.get("notion").call(context.Background(), "search", nil); err != nil || res.IsError {
		t.Fatalf("call = %+v, %v", res, err)
	}
}

// Dynamic registration stays first: a server that offers both, or a server
// with a client of its own, does not use the published client.
func TestBuiltinClientOnlyWhenNothingElse(t *testing.T) {
	ctx := context.Background()
	both := newFakeAS(t)
	both.cimd = true
	store, _ := openStore(filepath.Join(t.TempDir(), "core.db"))
	c, stop := oauthCore(t, store, both)
	defer stop()
	if c.usesBuiltinClient(ctx, "notion", c.conf().Servers["notion"], nil) {
		t.Fatal("a server with dynamic registration uses the published client")
	}
	if done := connectFlow(t, c, both, builtinRedirect); done["status"] != statusReady || both.registrations != 1 {
		t.Fatalf("DCR + CIMD: %v, %d registrations", done, both.registrations)
	}

	only := newFakeAS(t)
	only.cimdOnly = true
	s := Server{URL: only.srv.URL + "/mcp", Auth: &ServerAuth{OAuth: &OAuthConfig{}}}
	if !c.usesBuiltinClient(ctx, "x", s, nil) {
		t.Fatal("CIMD only: not the published client")
	}
	if c.usesBuiltinClient(ctx, "x", s, credentials{"client-id": "typed"}) {
		t.Fatal("a client typed in the form is not used")
	}
	for _, o := range []OAuthConfig{{ClientIDFile: "/run/id"}, {ClientMetadata: "https://example.org/client.json"}, {RedirectURI: "http://127.0.0.1:1/callback"}} {
		s := Server{URL: only.srv.URL + "/mcp", Auth: &ServerAuth{OAuth: &o}}
		if c.usesBuiltinClient(ctx, "x", s, nil) {
			t.Errorf("own client %+v is not used", o)
		}
	}
	if c.usesBuiltinClient(ctx, "x", Server{URL: only.srv.URL + "/mcp", Auth: &ServerAuth{BearerFile: "/run/token"}}, nil) {
		t.Error("a token server uses the published client")
	}
	if d, _ := discover(ctx, http.DefaultClient, only.srv.URL+"/mcp"); d.Reach != reachOAuth || d.DCR || !d.CIMD {
		t.Fatalf("discover, CIMD only: %+v", d)
	}
}

// From the workspace, a CIMD-only server is saved, then connected by
// pasting the address of the loopback page: the browser never comes back
// to the workspace.
func TestWebConnectWithBuiltinClient(t *testing.T) {
	f := newFakeAS(t)
	f.cimdOnly = true
	store, _ := openStore(filepath.Join(t.TempDir(), "core.db"))
	c, cancel := oauthCore(t, store, f)
	defer cancel()
	waitStatus(t, c.pool.get("notion"), statusNeedsAuth)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, body, h := tw.post("/settings/servers/notion/connect", nil, csrf)
	if code != 200 || h.Get("HX-Redirect") != "" {
		t.Fatalf("connect: %d %v", code, h)
	}
	m := regexp.MustCompile(`href="([^"]+)" target="_blank"`).FindStringSubmatch(body)
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil || flow == nil {
		t.Fatalf("no paste step: %s", body)
	}
	authURL := html.UnescapeString(m[1])
	if u, _ := url.Parse(authURL); u.Query().Get("redirect_uri") != builtinRedirect || u.Query().Get("client_id") != builtinClientID {
		t.Fatalf("authorization URL = %s", authURL)
	}
	authCode, state := f.authorize(authURL)
	_, body, _ = tw.post("/settings/servers/notion/callback", url.Values{"flow": {flow[1]},
		"url": {builtinRedirect + "?code=" + authCode + "&state=" + url.QueryEscape(state) + "&iss=" + url.QueryEscape(f.srv.URL)}}, csrf)
	if !strings.Contains(body, "Connecté : 1 outils") {
		t.Fatalf("paste: %s", body)
	}
	waitStatus(t, c.pool.get("notion"), statusReady)
}
