package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
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

// TestGoogleOrigin holds googleOrigin to Google's rules for an authorized
// redirect URI: https, no raw IP address (loopback ones aside), a host
// ending in a public top-level domain.
func TestGoogleOrigin(t *testing.T) {
	for origin, want := range map[string]string{
		"https://eriphe.example.org":         "",
		"https://eriphe.example.org:8443":    "",
		"https://eriphe.tail1234.ts.net":     "",
		"https://ia.example.fr":              "",
		"https://localhost:8443":             "",
		"https://127.0.0.1:8443":             "",
		"https://[::1]:8443":                 "",
		"http://localhost:8800":              "",
		"https://192.168.1.2:8443":           "ip",
		"https://100.101.102.103":            "ip",
		"https://[fd7a:115c:a1e0::1]":        "ip",
		"https://8.8.8.8":                    "ip",
		"https://eriphe.local:8443":          "local",
		"https://nas.lan":                    "local",
		"https://eriphe":                     "local",
		"https://eriphe.home.arpa":           "local",
		"https://eriphe.internal":            "local",
		"http://eriphe.example.org":          "http",
		"http://192.168.1.2:8800":            "ip",
		"http://eriphe.local":                "local",
		"https://Eriphe.Example.ORG.":        "",
		"https://eriphe.example.invalidtld0": "local",
	} {
		if got := googleOrigin(origin); got != want {
			t.Errorf("%s: %q, want %q", origin, got, want)
		}
	}
}

func TestParseGoogleClient(t *testing.T) {
	const id, secret = "123-abc.apps.googleusercontent.com", "GOCSPX-the-secret"
	app, uris, err := parseGoogleClient([]byte(`{"web":{"client_id":"` + id + `","project_id":"eriphe","auth_uri":"https://accounts.google.com/o/oauth2/auth",` +
		`"client_secret":"` + secret + `","redirect_uris":["https://eriphe.example.org/oauth/callback"]}}`))
	if err != nil || app != (oauthApp{ID: id, Secret: secret}) || len(uris) != 1 || uris[0] != "https://eriphe.example.org/oauth/callback" {
		t.Fatalf("web: %+v %v %v", app, uris, err)
	}
	if app, _, err := parseGoogleClient([]byte(` {"client_id":" ` + id + ` ","client_secret":"` + secret + `"}`)); err != nil || app.ID != id {
		t.Fatalf("flat: %+v %v", app, err)
	}
	_, _, err = parseGoogleClient([]byte(`{"installed":{"client_id":"` + id + `","client_secret":"` + secret + `","redirect_uris":["http://localhost"]}}`))
	wantKey(t, err, "drive.desktop")
	_, _, err = parseGoogleClient([]byte(`client_id=x`))
	wantKey(t, err, "drive.badjson")
	_, _, err = parseGoogleClient([]byte(`{"web":{"client_id":"` + id + `"}}`))
	wantKey(t, err, "drive.ownmissing")
	_, err = checkGoogleClient(secret, id) // swapped
	wantKey(t, err, "drive.badclient")
	_, err = checkGoogleClient(id, "two words")
	wantKey(t, err, "drive.badclient")
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
	cfg := &Config{DB: filepath.Join(dir, "core.db"),
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

const (
	testClientID     = "123-abc.apps.googleusercontent.com"
	testClientSecret = "GOCSPX-my-secret"
)

// The whole flow: the user's Google app saved, the workspace sends the
// browser to Google with PKCE and a signed state, Google back to the
// workspace, which exchanges the code and gives the token to eriphe-files.
func TestDriveRoundTrip(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"

	if _, body, _ := tw.do("GET", "/settings/tools/add", nil, nil); !strings.Contains(body, `href="/settings/drive"`) {
		t.Fatal("no Google Drive tile")
	}
	// No Google app yet: the guide, and no Connect.
	if code, body, _ := tw.do("GET", "/settings/drive", nil, nil); code != 200 || !strings.Contains(body, "Crée ton app Google") ||
		strings.Contains(body, `hx-post="/settings/drive/connect"`) {
		t.Fatalf("drive page: %d %s", code, body)
	}
	_, body, _ := tw.post("/settings/drive/connect", nil, csrf)
	if !strings.Contains(body, "Donne d&#39;abord") {
		t.Fatalf("connect without an app: %s", body)
	}
	if _, body, _ := tw.post("/settings/drive/client", url.Values{"client_id": {testClientID}}, csrf); !strings.Contains(body, "tous deux nécessaires") {
		t.Fatalf("no secret: %s", body)
	}
	code, body, _ := tw.post("/settings/drive/client", url.Values{"client_id": {testClientID}, "client_secret": {testClientSecret}}, csrf)
	if code != 200 || !strings.Contains(body, "Ton app Google est enregistrée") || !strings.Contains(body, testClientID) ||
		!strings.Contains(body, "Connecter Google Drive") || strings.Contains(body, testClientSecret) {
		t.Fatalf("save: %d %s", code, body)
	}
	if code, body, _ := tw.do("GET", "/settings/drive", nil, nil); code != 200 || strings.Contains(body, testClientSecret) ||
		!strings.Contains(body, `hx-post="/settings/drive/connect"`) {
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

	// Google → the workspace.
	back := tw.srv.URL + "/oauth/callback?" + url.Values{"code": {"the-code"}, "state": {state}, "scope": {"x"}, "authuser": {"0"}}.Encode()
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
	if tok["refresh_token"] != "the-refresh" || tok["client_id"] != testClientID || tok["client_secret"] != testClientSecret {
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

// The Google app as the console's JSON, uploaded: kept in the vault, its
// secret never shown back; a file that lacks this workspace's redirect URI
// is kept with a warning.
func TestDriveClientUpload(t *testing.T) {
	tw, _, csrf := filesCore(t, nil)
	callback := tw.srv.URL + "/oauth/callback"
	_, body, _ := tw.do("GET", "/settings/drive", nil, nil)
	if !strings.Contains(body, `<pre id="drive-callback">`+callback+`</pre>`) || !strings.Contains(body, `data-copy="drive-callback"`) ||
		!strings.Contains(body, "Web application") || !strings.Contains(body, "Authorized redirect URIs") || !strings.Contains(body, "7 jours") {
		t.Fatalf("guide: %s", body)
	}
	upload := func(file string) string {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		w, _ := mw.CreateFormFile("client_json", "client_secret_123.json")
		w.Write([]byte(file))
		mw.Close()
		req, _ := http.NewRequest("POST", tw.srv.URL+"/settings/drive/client", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("HX-Request", "true")
		resp, err := tw.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return string(raw)
	}
	if body := upload(`{"installed":{"client_id":"` + testClientID + `","client_secret":"` + testClientSecret + `"}}`); !strings.Contains(body, "Desktop app") {
		t.Fatalf("desktop: %s", body)
	}
	if _, err := googleClient(); err != errNoGoogleClient {
		t.Fatalf("a desktop client kept: %v", err)
	}
	body = upload(`{"web":{"client_id":"` + testClientID + `","client_secret":"` + testClientSecret + `","redirect_uris":["https://other.example.org/oauth/callback"]}}`)
	if !strings.Contains(body, "ne liste pas "+callback) || strings.Contains(body, testClientSecret) {
		t.Fatalf("upload: %s", body)
	}
	body = upload(`{"web":{"client_id":"` + testClientID + `","client_secret":"` + testClientSecret + `","redirect_uris":["` + callback + `"]}}`)
	if !strings.Contains(body, "Ton app Google est enregistrée") || strings.Contains(body, testClientSecret) {
		t.Fatalf("upload: %s", body)
	}
	if app, err := googleClient(); err != nil || app != (oauthApp{ID: testClientID, Secret: testClientSecret}) {
		t.Fatalf("vault: %+v %v", app, err)
	}
	if _, body, _ := tw.do("GET", "/settings/drive", nil, nil); strings.Contains(body, testClientSecret) || !strings.Contains(body, "jamais réaffiché") {
		t.Fatalf("page: %s", body)
	}
}

// A workspace Google cannot send back to says what to do instead.
func TestDriveUnusableOrigin(t *testing.T) {
	tw, c, _ := filesCore(t, nil)
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	for host, want := range map[string]string{
		"192.168.1.2:8443": "Google n&#39;accepte pas d&#39;adresse IP",
		"eriphe.local":     "un nom qui finit par un domaine public",
	} {
		req, _ := http.NewRequest("GET", tw.srv.URL+"/settings/drive", nil)
		req.Host = host
		for _, ck := range tw.hc.Jar.Cookies(req.URL) {
			req.AddCookie(ck)
		}
		resp, err := (&http.Client{Transport: tw.hc.Transport}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := string(raw)
		if !strings.Contains(body, want) || !strings.Contains(body, "https://"+host) || !strings.Contains(body, `href="/settings/remote"`) ||
			!strings.Contains(body, "ts.net") || !strings.Contains(body, "Google Drive pour ordinateur") ||
			!strings.Contains(body, `href="/settings/folders"`) || strings.Contains(body, `hx-post="/settings/drive/connect"`) {
			t.Fatalf("%s: %s", host, body)
		}
	}
	_, err := c.startDrive(driveConnect{Origin: "https://192.168.1.2:8443", Binding: "b"})
	wantKey(t, err, "drive.origin.ip")
	_, err = c.startDrive(driveConnect{Origin: "https://eriphe.local", Binding: "b"})
	wantKey(t, err, "drive.origin.local")
	_, err = c.startDrive(driveConnect{Origin: "http://eriphe.example.org", Binding: "b"})
	wantKey(t, err, "drive.origin.http")
}

// An older configuration that names the shared app still loads.
func TestLegacyCloudConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.json")
	os.WriteFile(path, []byte(`{"token_file": "/secrets/t", "db": "`+filepath.Join(dir, "core.db")+`",
		"cloud": {"google_client_file": "/data/google-oauth.json", "bounce_url": "https://relay.example.org/oauth/callback/"}}`), 0o600)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.legacyCloud(), "own Google app") {
		t.Fatalf("legacy: %q", cfg.legacyCloud())
	}
	if (&Config{}).legacyCloud() != "" {
		t.Fatal("a log line without the old fields")
	}
}

func TestDriveRefusals(t *testing.T) {
	newFakeGoogle(t)
	tw, c, _ := filesCore(t, nil)
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
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
	authURL, _ = c.startDrive(driveConnect{Origin: "https://eriphe.example.org", Binding: "b"})
	u, _ = url.Parse(authURL)
	if err := c.finishDrive(context.Background(), tw.srv.URL, u.Query().Get("state"), "the-code", "", "b"); err != errBadState {
		t.Fatalf("other origin: %v", err)
	}
	// A tool of the same name that is not eriphe-files is left alone.
	c.putServer(putCreate, filesServer, Server{URL: "https://x.example/mcp", Auth: &ServerAuth{None: true}, Use: "x"}, nil)
	wantKey(t, c.addDriveFolder("a", "A", "read"), "drive.taken")
}
