package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHubAPI is GitHub's API for the app: it checks the app's JWT, knows
// one installation (7, on owner, with owner/repo) and records the tokens
// it mints.
type fakeGitHubAPI struct {
	t    *testing.T
	key  *rsa.PrivateKey
	pem  string
	srv  *httptest.Server
	mu   sync.Mutex
	mint []map[string]any // the bodies of the token requests
}

func newFakeGitHub(t *testing.T) *fakeGitHubAPI {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHubAPI{t: t, key: key,
		pem: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// checkJWT verifies the app's token: RS256 with the app's key, iss the
// app's id, at most ten minutes.
func (f *fakeGitHubAPI) checkJWT(r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(tok, ".")
	if !ok || len(parts) != 3 {
		return false
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&f.key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iat, Exp int64
		Iss      string
	}
	json.Unmarshal(raw, &c)
	now := time.Now().Unix()
	return c.Iss == "42" && c.Iat <= now && c.Exp > now && c.Exp-c.Iat <= 600
}

func (f *fakeGitHubAPI) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/app-manifests/good-code/conversions" && r.Method == http.MethodPost {
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "slug": "eriphe-sam", "name": "Eriphe – Sam",
			"html_url": "https://github.com/apps/eriphe-sam", "pem": f.pem, "webhook_secret": "whs", "client_id": "Iv1.x",
			"client_secret": "cs", "owner": map[string]any{"login": "owner"}})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/app-manifests/") {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
		return
	}
	if !f.checkJWT(r) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"A JSON web token could not be decoded"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/installation":
		io.WriteString(w, `{"id": 7}`)
	case r.Method == http.MethodGet && r.URL.Path == "/app/installations":
		io.WriteString(w, `[{"id": 7, "account": {"login": "owner", "type": "User"}, "repository_selection": "selected"}]`)
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/7/access_tokens":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if repos, _ := body["repositories"].([]any); len(repos) > 0 && repos[0] != "repo" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"message":"There is at least one repository that does not exist or is not accessible"}`)
			return
		}
		f.mu.Lock()
		f.mint = append(f.mint, body)
		n := len(f.mint)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"token": "ghs_" + strings.Repeat("x", n),
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	default:
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	}
}

func (f *fakeGitHubAPI) mints() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.mint...)
}

func withApp(t *testing.T, c *core, f *fakeGitHubAPI) {
	t.Helper()
	c.github.api, c.github.web = f.srv.URL, f.srv.URL
	if err := storeGitHubApp(githubAppSecret{ID: 42, Slug: "eriphe-sam", Name: "Eriphe – Sam", Owner: "owner", PEM: f.pem}); err != nil {
		t.Fatal(err)
	}
}

// Tokens are minted for the one repository and the rights asked, and last
// an hour; a second ask within that time reuses the token.
func TestMintInstallationToken(t *testing.T) {
	c, task := credCore(t, "github")
	f := newFakeGitHub(t)
	withApp(t, c, f)
	ctx := context.Background()

	a := c.credential(ctx, task, credRequest{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: "read", Tool: "git"})
	if a.Decision != decideAllow || a.Password != "ghs_x" || a.Username != "x-access-token" {
		t.Fatalf("read: %+v", a)
	}
	if d := time.Until(a.Expires); d < 50*time.Minute || d > time.Hour {
		t.Fatalf("expires in %s", d)
	}
	mints := f.mints()
	if len(mints) != 1 {
		t.Fatalf("mints %v", mints)
	}
	raw, _ := json.Marshal(mints[0])
	if string(raw) != `{"permissions":{"contents":"read"},"repositories":["repo"]}` {
		t.Fatalf("token asked with %s", raw)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) != 1 || log[0].Source != sourceApp || log[0].Fallback() {
		t.Fatalf("log %+v", log)
	}

	// Cached: git asks more than once in one operation.
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"}); a.Password != "ghs_x" || len(f.mints()) != 1 {
		t.Fatalf("not reused: %+v, %d mints", a, len(f.mints()))
	}

	// A write, once approved, gets contents:write; gh gets pull requests and issues.
	c.store.credentialAnswered(task.ID, map[string]any{"host": "github.com", "repo": "owner/repo", "access": "write"}, decideAllow, byUser, "")
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"}); a.Password != "ghs_xx" {
		t.Fatalf("write: %+v", a)
	}
	if a := c.credential(ctx, task, credRequest{Host: "api.github.com", Path: "owner/repo", Access: "write", Tool: "gh"}); a.Password != "ghs_xxx" {
		t.Fatalf("gh write: %+v", a)
	}
	mints = f.mints()
	w, _ := json.Marshal(mints[1]["permissions"])
	g, _ := json.Marshal(mints[2]["permissions"])
	if string(w) != `{"contents":"write"}` || string(g) != `{"checks":"read","contents":"read","issues":"write","pull_requests":"write"}` {
		t.Fatalf("permissions: git %s, gh %s", w, g)
	}
}

func TestMintNotInstalled(t *testing.T) {
	c, _ := credCore(t, "github")
	f := newFakeGitHub(t)
	withApp(t, c, f)
	_, err := c.mintGitHub(context.Background(), "t-1", "owner/elsewhere", accessRead, false)
	if err == nil || !strings.Contains(err.Error(), "not installed on owner/elsewhere") {
		t.Fatalf("err = %v", err)
	}
	// A broken key in the vault is an error, never the fallback.
	currentVault().set(githubAppVault, `{"id": 42, "pem": "nope"}`)
	if _, err := c.mintGitHub(context.Background(), "t-1", "owner/repo", accessRead, false); err == nil || err == errNoGitHubApp {
		t.Fatalf("broken app: %v", err)
	}
}

// The GitHub tool on the app gets an installation token for all its
// repositories, renewed before it ends.
func TestGitHubToolToken(t *testing.T) {
	c, _ := credCore(t, "")
	f := newFakeGitHub(t)
	if _, err := c.github.toolToken(context.Background()); err == nil {
		t.Fatal("a tool token without an app")
	}
	withApp(t, c, f)
	tok, err := c.github.toolToken(context.Background())
	if err != nil || tok != "ghs_x" {
		t.Fatalf("tool token %q %v", tok, err)
	}
	if raw, _ := json.Marshal(f.mints()[0]); string(raw) != `{}` {
		t.Fatalf("asked with %s", raw)
	}
	tok, _ = c.github.toolToken(context.Background())
	if tok != "ghs_x" || len(f.mints()) != 1 {
		t.Fatalf("not reused: %q", tok)
	}
	// Three minutes left: a new one.
	c.github.mu.Lock()
	c.github.toolExp = time.Now().Add(3 * time.Minute)
	c.github.mu.Unlock()
	if tok, err := c.github.toolToken(context.Background()); err != nil || tok != "ghs_xx" || len(f.mints()) != 2 {
		t.Fatalf("renewal: %q %v, %d mints", tok, err, len(f.mints()))
	}
}

func TestServerOnGitHubApp(t *testing.T) {
	ok := &Config{Servers: map[string]Server{"github": {URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{GitHubApp: true}, Use: "GitHub"}}}
	if err := ok.checkServers(); err != nil {
		t.Fatal(err)
	}
	if k := ok.Servers["github"].authKind(); k != "github-app" {
		t.Fatalf("kind %q", k)
	}
	both := &Config{Servers: map[string]Server{"github": {URL: "https://api.githubcopilot.com/mcp/",
		Auth: &ServerAuth{GitHubApp: true, BearerFile: "/x"}, Use: "GitHub"}}}
	if both.checkServers() == nil {
		t.Fatal("two authentications accepted")
	}
	if !isGitHubServer("gh", Server{Auth: &ServerAuth{GitHubApp: true}}) || isGitHubServer("notion", Server{URL: "https://mcp.notion.com/mcp"}) {
		t.Fatal("isGitHubServer")
	}
}

func TestGitHubAppManifest(t *testing.T) {
	m := githubAppManifest(githubAppName("  Sam's   house "), "https://192.0.2.10:8443")
	if m["name"] != "Eriphe – Sam's house" || m["redirect_url"] != "https://192.0.2.10:8443/github-app/callback" || m["public"] != false {
		t.Fatalf("manifest %v", m)
	}
	perms := m["default_permissions"].(map[string]string)
	if perms["contents"] != "write" || perms["pull_requests"] != "write" || perms["issues"] != "write" ||
		perms["metadata"] != "read" || perms["checks"] != "read" || len(perms) != 5 {
		t.Fatalf("permissions %v", perms)
	}
	if hook := m["hook_attributes"].(map[string]any); hook["active"] != false {
		t.Fatalf("webhook %v", hook)
	}
	if n := githubAppName(strings.Repeat("a", 60)); len([]rune(n)) > 34 {
		t.Fatalf("name too long: %q", n)
	}
	g := &githubClient{web: "https://github.com"}
	if u := g.newAppURL("my-org", "s1"); u != "https://github.com/organizations/my-org/settings/apps/new?state=s1" {
		t.Fatalf("org url %s", u)
	}
	if u := g.newAppURL("", "s1"); u != "https://github.com/settings/apps/new?state=s1" {
		t.Fatalf("user url %s", u)
	}
}

// The manifest flow, from the workspace to a fake GitHub and back.
func TestGitHubAppManifestFlow(t *testing.T) {
	c, _ := credCore(t, "")
	f := newFakeGitHub(t)
	c.github.api, c.github.web = f.srv.URL, f.srv.URL
	tw := newTestWebFor(t, c)
	csrf := tw.setup()

	// The GitHub card leads to the app; without it, the token is the fallback.
	if code, body, _ := tw.do("GET", "/settings/tools", nil, nil); code != 200 || !strings.Contains(body, `href="/settings/github"`) {
		t.Fatalf("tools: %d", code)
	}
	if code, body, _ := tw.do("GET", "/settings/github", nil, nil); code != 200 || !strings.Contains(body, "Créer l&#39;app GitHub d&#39;Eriphe") ||
		!strings.Contains(body, "Sans app") {
		t.Fatalf("page without app: %d %s", code, body)
	}

	code, body, h := tw.do("POST", "/settings/github/create", url.Values{"csrf": {csrf}, "suffix": {"Sam"}}, nil)
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' "+f.srv.URL) {
		t.Fatalf("CSP %s", csp)
	}
	action := regexp.MustCompile(`action="([^"]+)"`).FindAllStringSubmatch(body, -1)
	var post string
	for _, a := range action {
		if strings.HasPrefix(html.UnescapeString(a[1]), f.srv.URL+"/settings/apps/new?state=gha-") {
			post = html.UnescapeString(a[1])
		}
	}
	if post == "" {
		t.Fatalf("no form to GitHub in %s", body)
	}
	m := regexp.MustCompile(`name="manifest" value="([^"]+)"`).FindStringSubmatch(body)
	var manifest map[string]any
	if m == nil || json.Unmarshal([]byte(html.UnescapeString(m[1])), &manifest) != nil ||
		manifest["name"] != "Eriphe – Sam" || !strings.HasSuffix(manifest["redirect_url"].(string), "/github-app/callback") {
		t.Fatalf("manifest %v", m)
	}
	state := strings.TrimPrefix(post, f.srv.URL+"/settings/apps/new?state=")

	// From another browser (no flow cookie): refused.
	other := &http.Client{Transport: tw.hc.Transport}
	resp, err := other.Get(tw.srv.URL + "/github-app/callback?code=good-code&state=" + state)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("other browser: %d", resp.StatusCode)
	}
	if _, err := loadGitHubApp(); err != errNoGitHubApp {
		t.Fatalf("app stored from another browser: %v", err)
	}

	// The state was used up by that try: start again, and come back.
	_, body, _ = tw.do("POST", "/settings/github/create", url.Values{"csrf": {csrf}, "suffix": {"Sam"}}, nil)
	state = regexp.MustCompile(`state=(gha-[A-Za-z0-9_-]+)`).FindStringSubmatch(body)[1]
	code, body, _ = tw.do("GET", "/github-app/callback?code=good-code&state="+state, nil, nil)
	if code != 200 || !strings.Contains(body, "/settings/github?created=1") {
		t.Fatalf("callback: %d %s", code, body)
	}
	app, err := loadGitHubApp()
	if err != nil || app.ID != 42 || app.Slug != "eriphe-sam" || app.Owner != "owner" || app.key == nil {
		t.Fatalf("stored app %+v %v", app, err)
	}
	// After an installation, GitHub's redirect leads on to the page.
	if code, body, _ := tw.do("GET", "/github-app/installed?installation_id=7&setup_action=install", nil, nil); code != 200 ||
		!strings.Contains(body, "/settings/github?installed=1") {
		t.Fatalf("installed: %d %s", code, body)
	}
	// Used once.
	if code, _, _ := tw.do("GET", "/github-app/callback?code=good-code&state="+state, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("replay: %d", code)
	}

	// The page shows the app, where it is installed, and how to install it.
	code, body, _ = tw.do("GET", "/settings/github", nil, nil)
	if code != 200 || !strings.Contains(body, "eriphe-sam") || !strings.Contains(body, f.srv.URL+"/apps/eriphe-sam/installations/new") ||
		!strings.Contains(body, "<b>owner</b>") {
		t.Fatalf("page: %d %s", code, body)
	}

	// The tool of the configuration file is the file's to change.
	if code, _, h := tw.post("/settings/github/tool", url.Values{}, csrf); code != 200 || !strings.Contains(h.Get("HX-Trigger"), "configuration") {
		t.Fatalf("config tool: %d %v", code, h)
	}
}

// Without a GitHub tool, the app's page adds one on the app.
func TestGitHubToolOnApp(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	withApp(t, cc.core, f)
	tw := newTestWebFor(t, cc.core)
	csrf := tw.setup()
	if code, _, h := tw.post("/settings/github/tool", url.Values{}, csrf); code != 200 || !strings.Contains(h.Get("HX-Redirect"), "saved=github") {
		t.Fatalf("tool: %d %v", code, h)
	}
	s := cc.conf().Servers["github"]
	if s.Auth == nil || !s.Auth.GitHubApp || s.URL != "https://api.githubcopilot.com/mcp/" || !s.TrustAnnotations {
		t.Fatalf("tool %+v", s)
	}
}
