package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// fakeAS is an MCP server protected by its own OAuth authorization server:
// protected resource metadata, authorization server metadata, dynamic
// client registration, and a token endpoint that rotates refresh tokens.
type fakeAS struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	registrations int
	redirects     []string // of the last registration
	challenge     string   // PKCE challenge of the pending authorization
	code          string
	access        map[string]bool
	refresh       string // the only valid refresh token
	n             int
	firstExpiry   int // expires_in of the token the code gives
	onAccess      func(token string)
	scopes        []string // its protected resource metadata's scopes_supported (read)
	lastScope     string   // the scope of the last authorization URL
	// Confidential clients only (Miro, Supabase): methods it advertises
	// (nil: none, "": the field left out), and the registration refuses
	// "none" and gives a secret the token endpoint wants.
	secretOnly bool
	methods    []string
	omitMethod bool
	regMethods []string // token_endpoint_auth_method of each registration
	tokenAuth  string   // how the last token request authenticated: post, basic or none
}

func (f *fakeAS) resourceScopes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scopes == nil {
		return []string{"read"}
	}
	return f.scopes
}

func newFakeAS(t *testing.T) *fakeAS {
	f := &fakeAS{t: t, access: map[string]bool{}, firstExpiry: 3600}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "notion"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "search", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"pages": 3}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		ok := f.access[tok]
		hook := f.onAccess
		f.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if hook != nil {
			hook(tok)
		}
		handler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"resource": f.srv.URL + "/mcp", "authorization_servers": []string{f.srv.URL},
			"scopes_supported": f.resourceScopes()})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "registration_endpoint": f.srv.URL + "/register",
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": f.authMethods(),
			"scopes_supported": []string{"read", "offline_access"}})
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		method, _ := in["token_endpoint_auth_method"].(string)
		f.regMethods = append(f.regMethods, method)
		if f.secretOnly && method == "none" {
			f.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata", "error_description": "public clients are not allowed"})
			return
		}
		if f.secretOnly {
			in["client_secret"] = "secret-1"
		}
		f.registrations++
		f.redirects = nil
		if list, ok := in["redirect_uris"].([]any); ok {
			for _, r := range list {
				f.redirects = append(f.redirects, fmt.Sprint(r))
			}
		}
		f.mu.Unlock()
		in["client_id"] = "client-1"
		writeJSON(w, http.StatusCreated, in)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		id, secret, basic := r.BasicAuth()
		switch {
		case basic:
			f.tokenAuth = "basic"
			r.Form.Set("client_id", id)
		case r.Form.Get("client_secret") != "":
			f.tokenAuth, secret = "post", r.Form.Get("client_secret")
		default:
			f.tokenAuth = "none"
		}
		if f.secretOnly && secret != "secret-1" {
			writeJSON(w, 401, map[string]string{"error": "invalid_client"})
			return
		}
		expiry := 3600
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != f.code || f.code == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge ||
				r.Form.Get("resource") != f.srv.URL+"/mcp" || r.Form.Get("client_id") != "client-1" {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			f.code = ""
			expiry = f.firstExpiry
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.refresh || f.refresh == "" {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
		default:
			writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
			return
		}
		f.n++
		access, refresh := fmt.Sprintf("a%d", f.n), fmt.Sprintf("r%d", f.n)
		f.access[access], f.refresh = true, refresh
		writeJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": expiry, "refresh_token": refresh})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// authorize plays the user approving in the browser: it reads the
// authorization URL and returns the redirect's code and state.
func (f *fakeAS) authorize(authURL string) (code, state string) {
	u, err := url.Parse(authURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("resource") != f.srv.URL+"/mcp" || q.Get("client_id") != "client-1" ||
		!strings.Contains(q.Get("scope"), "read") {
		f.t.Fatalf("authorization URL = %s", authURL)
	}
	f.mu.Lock()
	f.challenge, f.code, f.lastScope = q.Get("code_challenge"), "code-1", q.Get("scope")
	f.mu.Unlock()
	return "code-1", q.Get("state")
}

func (f *fakeAS) authMethods() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.omitMethod:
		return nil
	case f.methods != nil:
		return f.methods
	}
	return []string{"none"}
}

func (f *fakeAS) registered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.redirects
}

func (f *fakeAS) revokeAll() {
	f.mu.Lock()
	f.access, f.refresh = map[string]bool{}, ""
	f.mu.Unlock()
}

func oauthCore(t *testing.T, store *Store, f *fakeAS) (*core, context.CancelFunc) {
	cfg := &Config{Servers: map[string]Server{"notion": {URL: f.srv.URL + "/mcp", Auth: &ServerAuth{OAuth: &OAuthConfig{}},
		Use: "notes", TrustAnnotations: true}}}
	c := newCore(cfg, store)
	ctx, cancel := context.WithCancel(context.Background())
	c.pool.run(ctx)
	return c, cancel
}

// connectFlow runs `eriphe connect` against the core's API.
func connectFlow(t *testing.T, c *core, f *fakeAS, redirect string) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	c.serverRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var started map[string]string
	if code := call(t, srv, "POST", "/v1/servers/notion/connect", `{"redirect_uri":"`+redirect+`"}`, &started); code != 200 || started["auth_url"] == "" {
		t.Fatalf("connect: %d %v", code, started)
	}
	code, state := f.authorize(started["auth_url"])
	var done map[string]any
	body, _ := json.Marshal(map[string]string{"flow": started["flow"], "code": code, "state": state})
	if status := call(t, srv, "POST", "/v1/servers/notion/callback", string(body), &done); status != 200 {
		t.Fatalf("callback: %d", status)
	}
	return done
}

func TestOAuthConnectRefreshRestart(t *testing.T) {
	f := newFakeAS(t)
	f.firstExpiry = 1 // already expired: the first use refreshes it
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	// A rotated refresh token is on disk before its access token is used.
	f.onAccess = func(tok string) {
		if tok == "a1" {
			return
		}
		_, saved, _ := store.oauthToken("notion")
		if saved == nil || saved.AccessToken != tok || saved.RefreshToken != "r"+tok[1:] {
			t.Errorf("access token %s used before it was saved (%+v)", tok, saved)
		}
	}
	c, stop := oauthCore(t, store, f)
	u := c.pool.get("notion")
	waitStatus(t, u, statusNeedsAuth)
	if s := c.listServers("notion", false); s[0].Connect != "eriphe connect notion" {
		t.Fatalf("needs auth = %+v", s)
	}

	done := connectFlow(t, c, f, "http://127.0.0.1:8976/callback")
	if done["status"] != statusReady || done["tools"] != float64(1) {
		t.Fatalf("after connect: %v", done)
	}
	_, tok, _ := store.oauthToken("notion")
	if tok == nil || tok.RefreshToken != "r2" || tok.AccessToken != "a2" {
		t.Fatalf("stored token = %+v (want the rotated r2)", tok)
	}
	if reg := store.oauthClient("notion"); reg == nil || reg.ClientID != "client-1" || reg.RedirectURI != "http://127.0.0.1:8976/callback" {
		t.Fatalf("stored client = %+v", reg)
	}
	if s := c.listServers("notion", false); s[0].RedirectURI != "http://127.0.0.1:8976/callback" {
		t.Fatalf("redirect hint = %+v", s[0])
	}
	res, err := u.call(context.Background(), "search", nil)
	if err != nil || res.IsError {
		t.Fatalf("call = %+v, %v", res, err)
	}
	stop()
	time.Sleep(100 * time.Millisecond)

	// Restart: the stored token is used, nothing registers again.
	c2, stop2 := oauthCore(t, store, f)
	defer stop2()
	u2 := c2.pool.get("notion")
	waitStatus(t, u2, statusReady)
	// A second connect reuses the registered client for the same redirect.
	if done := connectFlow(t, c2, f, "http://127.0.0.1:8976/callback"); done["status"] != statusReady {
		t.Fatalf("second connect: %v", done)
	}
	if f.registrations != 1 {
		t.Fatalf("registered %d times", f.registrations)
	}

	// Revoked: needs an authorization again.
	f.revokeAll()
	u2.reconnect()
	waitStatus(t, u2, statusNeedsAuth)
	if _, errText, _ := u2.snapshot(); !strings.Contains(errText, "expired or was revoked") {
		t.Fatalf("reason = %q", errText)
	}
	if _, err := u2.call(context.Background(), "search", nil); err == nil || !strings.Contains(err.Error(), "eriphe connect notion") {
		t.Fatalf("call while needs_auth: %v", err)
	}
}

func TestConnectRejects(t *testing.T) {
	f := newFakeAS(t)
	store, _ := openStore(filepath.Join(t.TempDir(), "core.db"))
	c, stop := oauthCore(t, store, f)
	defer stop()
	mux := http.NewServeMux()
	c.serverRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, bad := range []string{`{"redirect_uri":"https://evil.example/callback"}`, `{"redirect_uri":"http://localhost:1/callback"}`, `{}`} {
		if code := call(t, srv, "POST", "/v1/servers/notion/connect", bad, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if code := call(t, srv, "POST", "/v1/servers/nope/connect", `{}`, nil); code != http.StatusNotFound {
		t.Errorf("unknown server: %d", code)
	}
	if code := call(t, srv, "POST", "/v1/servers/notion/callback", `{"flow":"x","code":"y"}`, nil); code != http.StatusNotFound {
		t.Errorf("unknown flow: %d", code)
	}
	var list []serverInfo
	if code := call(t, srv, "GET", "/v1/servers", "", &list); code != 200 || len(list) != 1 || list[0].Auth != "oauth" {
		t.Fatalf("list: %d %+v", code, list)
	}
}

// oauthHandler: the stored token outside a connect; during one, a handler
// for the client of the configuration, of the form, the one registered for
// this redirect, or a new registration. A client file that cannot be read
// fails the attempt.
func TestOAuthHandler(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, secret := filepath.Join(dir, "id"), filepath.Join(dir, "secret")
	os.WriteFile(id, []byte("client-1"), 0o600)
	os.WriteFile(secret, []byte("secret-1"), 0o600)
	p := newPool(store, nil)
	const redirect = "http://127.0.0.1:8976/callback"
	handler := func(o OAuthConfig, flow bool, clientID string) (*oauthGate, error) {
		u := &upstream{p: p, name: "docs", srv: Server{URL: "https://docs.example.org/mcp", Auth: &ServerAuth{OAuth: &o}}, clientID: clientID}
		if flow {
			u.flow = &authFlow{redirect: redirect}
		}
		return p.oauthHandler(u)
	}

	if g, err := handler(OAuthConfig{}, false, ""); err != nil || g.ts != nil || g.inner != nil {
		t.Fatalf("no token, no connect: %+v %v", g, err)
	}
	store.saveOAuthToken("docs", &oauth2.Config{ClientID: "c"}, &oauth2.Token{AccessToken: "a", RefreshToken: "r"})
	if g, err := handler(OAuthConfig{}, false, ""); err != nil || g.ts == nil || g.inner != nil {
		t.Fatalf("stored token: %+v %v", g, err)
	}
	store.saveOAuthClient("docs", oauthClient{ClientID: "registered", RedirectURI: redirect})
	for name, c := range map[string]struct {
		o        OAuthConfig
		clientID string
	}{
		"dynamic":           {OAuthConfig{}, ""},
		"metadata":          {OAuthConfig{ClientMetadata: "https://eriphe.example.org/client.json"}, ""},
		"client file":       {OAuthConfig{ClientIDFile: id}, ""},
		"client and secret": {OAuthConfig{ClientIDFile: id, ClientSecretFile: secret}, ""},
		"typed in the form": {OAuthConfig{ClientIDFile: filepath.Join(dir, "missing")}, "typed"},
	} {
		if g, err := handler(c.o, true, c.clientID); err != nil || g.ts == nil || g.inner == nil {
			t.Errorf("%s: %+v %v", name, g, err)
		}
	}
	for name, o := range map[string]OAuthConfig{
		"client file missing": {ClientIDFile: filepath.Join(dir, "missing")},
		"secret file missing": {ClientIDFile: id, ClientSecretFile: filepath.Join(dir, "missing")},
	} {
		if _, err := handler(o, true, ""); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
