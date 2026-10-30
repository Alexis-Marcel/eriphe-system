package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
)

func TestStateSignature(t *testing.T) {
	key, other := []byte(strings.Repeat("k", 32)), []byte(strings.Repeat("o", 32))
	now := time.Unix(1_800_000_000, 0)
	p := statePayload{Origin: "https://192.168.0.2:8443", Nonce: "abc", Expires: now.Add(time.Minute).Unix()}
	s := signState(key, p)
	if got, err := openState(key, s, now); err != nil || got != p {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	parts := strings.Split(s, ".")
	forged := p
	forged.Origin = "https://evil.example"
	raw, _ := json.Marshal(forged)
	for name, bad := range map[string]string{
		"other key":      signState(other, p),
		"other origin":   parts[0] + "." + base64.RawURLEncoding.EncodeToString(raw) + "." + parts[2],
		"no mac":         parts[0] + "." + parts[1],
		"bad mac":        parts[0] + "." + parts[1] + ".AAAA",
		"other version":  "e2." + parts[1] + "." + parts[2],
		"go-sdk's state": "Zm9vYmFy",
		"too long":       s + strings.Repeat("A", 1100),
	} {
		if _, err := openState(key, bad, now); err != errBadState {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := openState(key, s, now.Add(2*time.Minute)); err != errBadState {
		t.Errorf("expired: %v", err)
	}
	if !isCloudState(s) || isCloudState("Zm9vYmFy") {
		t.Error("isCloudState")
	}
}

// TestBounceOrigin holds bounceOrigin to the relay's own cases
// (pages/origins.json, which pages/relay.test.js reads too).
func TestBounceOrigin(t *testing.T) {
	raw, err := os.ReadFile("../pages/origins.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct{ Allowed, Refused []string }
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases.Allowed) == 0 || len(cases.Refused) == 0 {
		t.Fatalf("origins.json: %v", err)
	}
	for _, s := range cases.Allowed {
		if !bounceOrigin(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range cases.Refused {
		if bounceOrigin(s) {
			t.Errorf("%q allowed", s)
		}
	}
}

// fakeGoogle is Google's authorization server for the code exchange.
type fakeGoogle struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	challenge string // from the authorization URL
	redirect  string
	clientID  string
	secret    string
	scope     string
	exchanges int
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{t: t, scope: scopeDrive}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		r.ParseForm()
		f := r.Form
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/token" || f.Get("grant_type") != "authorization_code" || f.Get("code") != "the-code" ||
			f.Get("client_id") != g.clientID || f.Get("client_secret") != g.secret || f.Get("redirect_uri") != g.redirect ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		g.exchanges++
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "the-refresh", "expires_in": 3600,
			"token_type": "Bearer", "scope": g.scope})
	}))
	t.Cleanup(g.srv.Close)
	old, oldHTTP := googleOAuth, cloudHTTP
	googleOAuth.AuthURL, googleOAuth.TokenURL, cloudHTTP = g.srv.URL+"/auth", g.srv.URL+"/token", g.srv.Client()
	t.Cleanup(func() { googleOAuth, cloudHTTP = old, oldHTTP })
	return g
}

// authorize reads what the workspace sent to Google, as Google would.
func (g *fakeGoogle) authorize(t *testing.T, authURL string) (state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, g.srv.URL+"/auth?") {
		t.Fatalf("auth URL %q", authURL)
	}
	q := u.Query()
	if q.Get("client_id") != g.clientID || q.Get("redirect_uri") != g.redirect || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
		t.Fatalf("auth URL %q", authURL)
	}
	g.mu.Lock()
	g.challenge = q.Get("code_challenge")
	g.mu.Unlock()
	return q.Get("state")
}

// fakePortal is the relay's bounce as the page does it
// (pages/site/oauth/callback/relay.js): only to the origin in the state,
// and only code and state.
func fakePortal(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		parts := strings.Split(q.Get("state"), ".")
		var p statePayload
		if len(parts) == 3 && parts[0] == stateVersion {
			raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			json.Unmarshal(raw, &p)
		}
		if r.URL.Path != "/oauth/callback" || !bounceOrigin(p.Origin) || (q.Get("code") == "") == (q.Get("error") == "") {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		out := url.Values{"state": {q.Get("state")}}
		if q.Get("code") != "" {
			out.Set("code", q.Get("code"))
		} else {
			out.Set("error", q.Get("error"))
		}
		http.Redirect(w, r, p.Origin+"/oauth/callback?"+out.Encode(), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// filesCore is a workspace whose fichiers server is a fake eriphe-files.
func filesCore(t *testing.T, mutate func(*Config)) (*testWeb, *catalogCore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := vaultKey(filepath.Join(dir, "vault.key"))
	v, err := openVault(store, key)
	if err != nil {
		t.Fatal(err)
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	app := filepath.Join(dir, "google-oauth.json")
	os.WriteFile(app, []byte(`{"web":{"client_id":"eriphe-app","client_secret":"app-secret","redirect_uris":["https://alexis-marcel.github.io/eriphe-system/oauth/callback/"]}}`), 0o600)
	cfg := &Config{DB: filepath.Join(dir, "core.db"), Cloud: &CloudConfig{},
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: "/secrets/claude"}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Use: "default"}},
		DefaultAgent: "sonnet"}
	if mutate != nil {
		mutate(cfg)
	}
	cc := &catalogCore{core: newCore(cfg, store), dials: map[string]int{}}
	cc.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: u.name}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "list_files"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{}, nil
		})
		mcp.AddTool(s, &mcp.Tool{Name: "browse_folders", Meta: mcp.Meta{"eriphe/setup": true}},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				Parent string `json:"parent,omitempty"`
				Page   string `json:"page_token,omitempty"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				if in.Parent == "" {
					return nil, map[string]any{"folders": []map[string]string{{"id": "root-id", "name": "My Drive"}}}, nil
				}
				return nil, map[string]any{"folders": []map[string]string{{"id": "compta", "name": "Compta"}}}, nil
			})
		ct, st := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cc.pool.run(ctx)
	tw := newTestWebFor(t, cc.core)
	return tw, cc, tw.setup()
}

// The whole bounce: the workspace sends the browser to Google with PKCE and
// a signed state, Google to the relay, the relay back to the workspace,
// which exchanges the code and gives the token to eriphe-files.
func TestDriveBounceRoundTrip(t *testing.T) {
	g := newFakeGoogle(t)
	portal := fakePortal(t)
	tw, c, csrf := filesCore(t, func(cfg *Config) { cfg.Cloud.BounceURL = portal.URL + "/oauth/callback" })
	g.clientID, g.secret, g.redirect = "eriphe-app", "app-secret", portal.URL+"/oauth/callback"

	if _, body, _ := tw.do("GET", "/settings/tools/add", nil, nil); !strings.Contains(body, `href="/settings/drive"`) {
		t.Fatal("no Google Drive tile")
	}
	if code, body, _ := tw.do("GET", "/settings/drive", nil, nil); code != 200 || !strings.Contains(body, "Se connecter à Google Drive") ||
		strings.Contains(body, "google-oauth.json") {
		t.Fatalf("drive page: %d %s", code, body)
	}
	code, body, h := tw.post("/settings/drive/connect", url.Values{"write": {"1"}}, csrf)
	authURL := h.Get("HX-Redirect")
	if code != 200 || authURL == "" {
		t.Fatalf("connect: %d %s", code, body)
	}
	if u, _ := url.Parse(authURL); u.Query().Get("scope") != scopeDrive {
		t.Fatalf("scope %q", u.Query().Get("scope"))
	}
	state := g.authorize(t, authURL)
	p, err := openState(c.stateKey(), state, time.Now())
	if err != nil || p.Origin != tw.srv.URL {
		t.Fatalf("state %+v %v, want origin %s", p, err, tw.srv.URL)
	}

	// Google → the relay → the workspace, with nothing but code and state.
	resp, err := tw.hc.Get(portal.URL + "/oauth/callback?code=the-code&state=" + url.QueryEscape(state) + "&scope=x&authuser=0")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back := resp.Header.Get("Location")
	bu, _ := url.Parse(back)
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(back, tw.srv.URL+"/oauth/callback?") ||
		len(bu.Query()) != 2 || bu.Query().Get("state") != state {
		t.Fatalf("bounce: %d %s", resp.StatusCode, back)
	}
	// The cookie of the flow is this browser's: another one is refused.
	other := &testWeb{t: t, srv: tw.srv, hc: &http.Client{Transport: tw.hc.Transport}}
	if code, _, _ := other.do("GET", strings.TrimPrefix(back, tw.srv.URL), nil, nil); code != http.StatusBadRequest {
		t.Fatalf("another browser: %d", code)
	}
	// That refusal used the flow up: connect again.
	_, _, h = tw.post("/settings/drive/connect", url.Values{"write": {"1"}}, csrf)
	state = g.authorize(t, h.Get("HX-Redirect"))
	back = tw.srv.URL + "/oauth/callback?" + url.Values{"code": {"the-code"}, "state": {state}}.Encode()
	if code, _, _ := tw.do("GET", "/oauth/callback?code=the-code&state="+url.QueryEscape(state[:len(state)-2]+"AA"), nil, nil); code != http.StatusBadRequest {
		t.Fatalf("forged state: %d", code)
	}
	code, body, _ = tw.do("GET", strings.TrimPrefix(back, tw.srv.URL), nil, nil)
	if code != 200 || !strings.Contains(body, `url=/settings/drive?connected=1`) || strings.Contains(body, "the-code") {
		t.Fatalf("callback: %d %s", code, body)
	}
	if g.exchanges != 1 {
		t.Fatalf("%d exchanges", g.exchanges)
	}
	if code, _, _ := tw.do("GET", strings.TrimPrefix(back, tw.srv.URL), nil, nil); code != http.StatusBadRequest {
		t.Fatalf("replayed: %d", code)
	}

	// eriphe-files runs with the token from the vault, never in its spec.
	srv := c.conf().Servers[filesServer]
	spec, _ := decodeRun(srv.Run)
	if !strings.HasPrefix(spec.Image, filesImage) || spec.Env[driveTokenEnv].File != "vault:service.fichiers.files_google_token" ||
		strings.Contains(string(srv.Run), "the-refresh") || !srv.TrustAnnotations {
		t.Fatalf("server: %s", srv.Run)
	}
	env, err := lendService(spec)
	if err != nil {
		t.Fatal(err)
	}
	var tok map[string]string
	json.Unmarshal([]byte(env[driveTokenEnv]), &tok)
	if tok["refresh_token"] != "the-refresh" || tok["client_id"] != "eriphe-app" || tok["client_secret"] != "app-secret" {
		t.Fatalf("token lent: %v", tok)
	}
	st := c.driveState()
	if !st.Mine || !st.CanWrite || len(st.Folders) != 0 {
		t.Fatalf("state: %+v", st)
	}

	// The folders, through the files server's setup tool, which tasks never get.
	waitStatus(t, c.pool.get(filesServer), statusReady)
	_, _, tools := c.pool.get(filesServer).snapshot()
	if len(tools) != 1 || tools[0].Name != "list_files" {
		t.Fatalf("tools lent: %v", tools)
	}
	if _, body, _ := tw.do("GET", "/settings/drive/browse?parent=root-id&name=My+Drive", nil, nil); !strings.Contains(body, "Compta") ||
		!strings.Contains(body, `value="drop"`) {
		t.Fatalf("browse: %s", body)
	}
	if code, body, _ := tw.post("/settings/drive/folders", url.Values{"id": {"compta"}, "name": {"Compta"}, "access": {"drop"}}, csrf); code != 200 ||
		!strings.Contains(body, "dépôt dans Eriphe/") {
		t.Fatalf("add: %d %s", code, body)
	}
	tw.post("/settings/drive/folders", url.Values{"id": {"other"}, "name": {"Compta"}, "access": {"read"}}, csrf)
	fc, _ := filesConfigOf(c.conf().Servers[filesServer])
	if len(fc.Folders) != 2 || fc.Folders[0] != (DriveFolder{Name: "Compta", Account: "google", ID: "compta", Access: "drop"}) ||
		fc.Folders[1].Name != "Compta (2)" || fc.Accounts[0].Auth.TokenEnv != driveTokenEnv {
		t.Fatalf("folders: %+v", fc)
	}
	if code, body, _ := tw.do("GET", "/settings/drive", nil, nil); code != 200 || !strings.Contains(body, "Compta (2)") ||
		!strings.Contains(body, `hx-get="/settings/drive/browse"`) || !strings.Contains(body, "Reconnecter") {
		t.Fatalf("drive page: %d %s", code, body)
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "Google Drive · 2 dossiers") ||
		!strings.Contains(body, `href="/settings/drive"`) {
		t.Fatalf("tools list: %s", body)
	}
	// One list of folders with the machines' (folders.go): in Settings →
	// Folders and for the brain, lent through the files tool, not mounted.
	if _, body, _ := tw.do("GET", "/settings/folders", nil, nil); !strings.Contains(body, "Compta (2)") || !strings.Contains(body, "Dans ton Google Drive") {
		t.Fatalf("folders page: %s", body)
	}
	if fs, _ := c.folderViews(); len(fs) != 2 || fs[0].Source != sourceDrive || fs[0].Drive != "compta" || !strings.Contains(fs[0].Lend, filesServer) {
		t.Fatalf("views: %+v", fs)
	}
	if _, _, err := c.folderGrant([]string{"compta"}); err == nil || !strings.Contains(err.Error(), "Google Drive") {
		t.Fatalf("a Drive folder lent as a machine's: %v", err)
	}
	tw.post("/settings/folders/delete", url.Values{"source": {sourceDrive}, "name": {"Compta (2)"}}, csrf)
	if f := c.cloudFolders(); len(f) != 1 || f[0].ID != "compta" {
		t.Fatalf("after remove: %+v", f)
	}
	if spec, _ := decodeRun(c.conf().Servers[filesServer].Run); spec.Env[driveTokenEnv].File == "" {
		t.Fatal("a folder change lost the token")
	}

	// Reconnected read only: the write rights go.
	g.scope = scopeDriveReadonly
	_, _, h = tw.post("/settings/drive/connect", nil, csrf)
	state = g.authorize(t, h.Get("HX-Redirect"))
	tw.do("GET", "/oauth/callback?code=the-code&state="+url.QueryEscape(state), nil, nil)
	if st := c.driveState(); st.CanWrite || st.Folders[0].Access != "read" {
		t.Fatalf("read only: %+v", st)
	}
	if err := c.addDriveFolder("compta", "Compta", "drop"); err == nil {
		t.Fatal("write right without the scope")
	}
}

func TestDriveOwnClient(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, func(cfg *Config) { cfg.Cloud.GoogleClientFile = "/nonexistent" })
	if _, body, _ := tw.do("GET", "/settings/drive", nil, nil); !strings.Contains(body, "google-oauth.json") {
		t.Fatal("no notice without the Eriphe app")
	}
	code, body, _ := tw.post("/settings/drive/connect", nil, csrf)
	if code != 200 || !strings.Contains(body, "google-oauth.json") {
		t.Fatalf("without the app: %s", body)
	}
	if _, body, _ := tw.post("/settings/drive/connect", url.Values{"own": {"1"}, "client_id": {"mine"}}, csrf); !strings.Contains(body, "tous deux nécessaires") {
		t.Fatalf("no secret: %s", body)
	}
	g.clientID, g.secret, g.redirect, g.scope = "mine", "my-secret", tw.srv.URL+"/oauth/callback", scopeDriveReadonly
	_, _, h := tw.post("/settings/drive/connect", url.Values{"own": {"1"}, "client_id": {"mine"}, "client_secret": {"my-secret"}}, csrf)
	state := g.authorize(t, h.Get("HX-Redirect"))
	if code, body, _ := tw.do("GET", "/oauth/callback?code=the-code&state="+url.QueryEscape(state), nil, nil); code != 200 ||
		!strings.Contains(body, "connected=1") {
		t.Fatalf("callback: %d %s", code, body)
	}
	spec, _ := decodeRun(c.conf().Servers[filesServer].Run)
	env, _ := lendService(spec)
	if !strings.Contains(env[driveTokenEnv], `"client_id":"mine"`) || c.driveState().CanWrite {
		t.Fatalf("own client: %s", env[driveTokenEnv])
	}
}

func TestDriveRefusals(t *testing.T) {
	newFakeGoogle(t)
	tw, c, _ := filesCore(t, nil)
	// The relay does not bounce to an origin a browser would not write.
	_, err := c.startDrive(driveConnect{Origin: "https://eriphe.example.com:443", Binding: "b"})
	wantKey(t, err, "drive.badorigin")
	// Denied at Google: nothing saved.
	authURL, err := c.startDrive(driveConnect{Origin: tw.srv.URL, Binding: "b"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	err = c.finishDrive(context.Background(), tw.srv.URL, u.Query().Get("state"), "", "access_denied", "b")
	wantKey(t, err, "drive.denied")
	if _, ok := c.conf().Servers[filesServer]; ok {
		t.Fatal("saved after a refusal")
	}
	// A state for another workspace is refused here.
	authURL, _ = c.startDrive(driveConnect{Origin: "https://192.168.0.9", Binding: "b"})
	u, _ = url.Parse(authURL)
	if err := c.finishDrive(context.Background(), tw.srv.URL, u.Query().Get("state"), "the-code", "", "b"); err != errBadState {
		t.Fatalf("other origin: %v", err)
	}
	// A tool of the same name that is not eriphe-files is left alone.
	c.putServer(putCreate, filesServer, Server{URL: "https://x.example/mcp", Auth: &ServerAuth{None: true}, Use: "x"}, nil)
	wantKey(t, c.addDriveFolder("a", "A", "read"), "drive.taken")
}
