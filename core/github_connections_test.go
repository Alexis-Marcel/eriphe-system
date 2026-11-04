package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A connection on the app names its account: its tokens come from that
// account's installation; without one, from the owner's.
func TestGitHubToolTokenAccount(t *testing.T) {
	c, _ := credCore(t, "")
	f := newFakeGitHub(t)
	f.org = true
	withApp(t, c, f)
	ctx := context.Background()

	tok, err := c.github.toolToken(ctx, "acme", false)
	if err != nil || tok != "ghs_acme_x" || len(f.orgMint) != 1 || len(f.mints()) != 0 {
		t.Fatalf("acme: %q %v (org mints %d, owner mints %d)", tok, err, len(f.orgMint), len(f.mints()))
	}
	// Cached per account, case aside; the owner's is another token.
	if tok, _ := c.github.toolToken(ctx, "ACME", false); tok != "ghs_acme_x" || len(f.orgMint) != 1 {
		t.Fatalf("acme again: %q", tok)
	}
	if tok, err := c.github.toolToken(ctx, "", false); err != nil || tok != "ghs_x" {
		t.Fatalf("owner: %q %v", tok, err)
	}
	if _, err := c.github.toolToken(ctx, "nobody", false); err == nil || !strings.Contains(err.Error(), "not installed on nobody") {
		t.Fatalf("an account without the app: %v", err)
	}

	// The pool asks with the server's account.
	var asked []string
	p := &pool{appToken: func(_ context.Context, account string, readOnly bool) (string, error) {
		asked = append(asked, account)
		return "tok", nil
	}}
	u := &upstream{p: p, name: "github-acme", srv: Server{URL: "https://api.githubcopilot.com/mcp/", Use: "GitHub",
		Auth: &ServerAuth{GitHubApp: true, Account: "acme"}}}
	if _, err := p.dialServer(ctx, u); err != nil || len(asked) != 1 || asked[0] != "acme" {
		t.Fatalf("dial: %v, asked %v", err, asked)
	}
}

func TestServerAccountNeedsTheApp(t *testing.T) {
	for _, tc := range []struct {
		auth ServerAuth
		ok   bool
	}{
		{ServerAuth{GitHubApp: true, Account: "acme"}, true},
		{ServerAuth{GitHubApp: true}, true},
		{ServerAuth{BearerFile: "/x", Account: "acme"}, false},
		{ServerAuth{GitHubApp: true, Account: "not an account"}, false},
	} {
		a := tc.auth
		c := &Config{Servers: map[string]Server{"github": {URL: "https://api.githubcopilot.com/mcp/", Auth: &a, Use: "GitHub"}}}
		if err := c.checkServers(); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc.auth, err)
		}
	}
}

// A token connection moved to the app keeps its name, its level and its
// actions allowed without asking; its token leaves the vault, and the page
// says to revoke it on GitHub.
func TestGitHubSwitchTokenToApp(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	f.org = true
	withApp(t, cc.core, f)
	srv := Server{URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{}, Use: "GitHub for acme"}.withLevel(accessFree)
	if err := cc.putServer(putCreate, "github-acme", srv, credentials{"token": "github_pat_x"}); err != nil {
		t.Fatal(err)
	}
	secret := entrySecret("server", "github-acme", "token")
	if v, err := currentVault().get(secret); err != nil || v != "github_pat_x" {
		t.Fatalf("token not stored: %q %v", v, err)
	}
	if err := cc.store.allowAction("github-acme", "create_issue"); err != nil {
		t.Fatal(err)
	}
	tw := newTestWebFor(t, cc.core)
	csrf := tw.setup()

	// The tools list offers the switch; the GitHub page shows it.
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "/settings/github?switch=github-acme") ||
		!strings.Contains(body, "Passer à l&#39;app") || !strings.Contains(body, "jeton personnel") {
		t.Fatalf("tools: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/github?switch=github-acme", nil, nil); !strings.Contains(body, `id="gh-conn-github-acme" class="lit"`) ||
		!strings.Contains(body, `<option value="acme">acme</option>`) {
		t.Fatalf("page: %s", body)
	}

	// Not on an account without the app.
	if _, _, h := tw.post("/settings/github/tool", url.Values{"name": {"github-acme"}, "account": {"nobody"}}, csrf); !strings.Contains(h.Get("HX-Trigger"), "nobody") {
		t.Fatalf("nobody: %v", h)
	}
	code, _, h := tw.post("/settings/github/tool", url.Values{"name": {"github-acme"}, "account": {"acme"}}, csrf)
	if code != 200 || h.Get("HX-Redirect") != "/settings/github?switched=github-acme&revoke=1" {
		t.Fatalf("switch: %d %v", code, h)
	}
	s := cc.conf().Servers["github-acme"]
	if s.Auth == nil || !s.Auth.GitHubApp || s.Auth.Account != "acme" || s.Auth.BearerFile != "" || s.level() != accessFree || s.Use != "GitHub for acme" {
		t.Fatalf("switched %+v %+v", s, s.Auth)
	}
	if a := cc.store.allowedActions("github-acme"); len(a) != 1 || a[0].Tool != "create_issue" {
		t.Fatalf("allowed actions lost: %+v", a)
	}
	if v, err := currentVault().get(secret); err == nil {
		t.Fatalf("token still in the vault: %q", v)
	}
	_, body, _ := tw.do("GET", "/settings/github?switched=github-acme&revoke=1", nil, nil)
	if !strings.Contains(body, "github.com/settings/personal-access-tokens") || !strings.Contains(body, "mêmes droits") {
		t.Fatalf("switched page: %s", body)
	}
	// The connection says its account, and the installation says who uses it.
	if !strings.Contains(body, "app GitHub · acme") || !strings.Contains(body, "Connexion « github-acme »") ||
		!strings.Contains(body, "organisation · 3 dépôts") || !strings.Contains(body, "Installer sur un autre compte ou une organisation") {
		t.Fatalf("connection on acme: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "GitHub · app GitHub · acme") {
		t.Fatalf("tools row: %s", body)
	}
}

// A second connection on the app, for an organization's installation.
func TestGitHubConnectionPerAccount(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	f.org = true
	withApp(t, cc.core, f)
	tw := newTestWebFor(t, cc.core)
	csrf := tw.setup()
	if _, _, h := tw.post("/settings/github/tool", url.Values{"name": {"github"}}, csrf); h.Get("HX-Redirect") != "/settings/tools?saved=github" {
		t.Fatalf("owner's: %v", h)
	}
	// The page suggests a name for acme's installation.
	if _, body, _ := tw.do("GET", "/settings/github", nil, nil); !strings.Contains(body, `value="github-acme"`) || !strings.Contains(body, "Connexion « github »") {
		t.Fatalf("page: %s", body)
	}
	if _, _, h := tw.post("/settings/github/tool", url.Values{"name": {"github-acme"}, "account": {"acme"}}, csrf); h.Get("HX-Redirect") != "/settings/tools?saved=github-acme" {
		t.Fatalf("acme's: %v", h)
	}
	conf := cc.conf()
	if a := conf.Servers["github"].Auth; a == nil || !a.GitHubApp || a.Account != "" {
		t.Fatalf("github %+v", a)
	}
	if s := conf.Servers["github-acme"]; s.Auth == nil || s.Auth.Account != "acme" || !strings.Contains(s.Use, "(acme)") {
		t.Fatalf("github-acme %+v", s)
	}
	// Not over a tool that is not GitHub's.
	if _, _, h := tw.post("/settings/github/tool", url.Values{"name": {"docs"}}, csrf); !strings.Contains(h.Get("HX-Trigger"), "docs") {
		t.Fatalf("docs: %v", h)
	}
}

// Adding GitHub starts with the choice: the app (recommended) or a token.
func TestGitHubAddChoice(t *testing.T) {
	cc := newDynamicCore(t, nil)
	tw := newTestWebFor(t, cc.core)
	tw.setup()
	code, body, _ := tw.do("GET", "/settings/tools/new?preset=github", nil, nil)
	if code != 200 {
		t.Fatalf("choice: %d", code)
	}
	for _, want := range []string{"Recommandé", "App GitHub d", "Jeton personnel", `href="/settings/github"`,
		"Rien à approuver", "accord chaque fois", "provider=token", `data-hash="add/github/token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("choice lacks %q", want)
		}
	}
	if strings.Contains(body, `name="token"`) {
		t.Error("the token field before the choice")
	}
	// The token: its form, which says what a token costs, and back to the choice.
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=github&provider=token", nil, nil)
	if !strings.Contains(body, `name="token"`) || !strings.Contains(body, "Choisir plutôt l&#39;app") ||
		!strings.Contains(body, `href="/settings/tools/new?preset=github"`) {
		t.Fatalf("token form: %s", body)
	}
	// In English too.
	if tr("en", "pick.github.app.noask") == "pick.github.app.noask" || tr("en", "github.switch") != "Switch to the app" {
		t.Fatal("English words missing")
	}
}

// Back from an installation, its connection comes by itself: a new one on
// an organization, the owner's token connection moved to the app. Once:
// not for an unknown, old or already used installation.
func TestGitHubConnectionOnInstall(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	f.org = true
	withApp(t, cc.core, f)
	tw := newTestWebFor(t, cc.core)
	tw.setup()

	// GitHub's return names the installation, on to the page.
	if _, body, _ := tw.do("GET", "/github-app/installed?installation_id=8&setup_action=install", nil, nil); !strings.Contains(body, "installed=1&amp;installation=8") ||
		!strings.Contains(body, "Retour sur Eriphe") || strings.Contains(body, "est connecté") {
		t.Fatalf("installed: %s", body)
	}
	if _, body, _ := tw.do("GET", "/github-app/installed?installation_id=8&setup_action=update", nil, nil); strings.Contains(body, "installation=8") {
		t.Fatalf("an update names it: %s", body)
	}
	// Unknown: nothing.
	if code, _, _ := tw.do("GET", "/settings/github?installed=1&installation=99", nil, nil); code != 200 || len(githubServers(cc.core)) != 0 {
		t.Fatalf("unknown: %d %v", code, cc.conf().Servers)
	}

	// acme's: a new connection, at the presets' level.
	code, _, h := tw.do("GET", "/settings/github?installed=1&installation=8", nil, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/settings/github?added=github-acme" {
		t.Fatalf("acme: %d %v", code, h)
	}
	s := cc.conf().Servers["github-acme"]
	if s.Auth == nil || !s.Auth.GitHubApp || s.Auth.Account != "acme" || s.level() != accessReads {
		t.Fatalf("github-acme %+v %+v", s, s.Auth)
	}
	if _, body, _ := tw.do("GET", "/settings/github?added=github-acme", nil, nil); !strings.Contains(body, "« github-acme » est dans tes outils") ||
		!strings.Contains(body, "Connexion « github-acme »") {
		t.Fatalf("page: %s", body)
	}
	// Again: it has its connection.
	if code, _, _ := tw.do("GET", "/settings/github?installed=1&installation=8", nil, nil); code != 200 || len(githubServers(cc.core)) != 1 {
		t.Fatalf("again: %d %v", code, cc.conf().Servers)
	}

	// The owner's, with a token connection named github: moved to the app.
	if err := cc.putServer(putCreate, "github", Server{URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{}, Use: "GitHub"}.withLevel(accessFree),
		credentials{"token": "github_pat_x"}); err != nil {
		t.Fatal(err)
	}
	code, _, h = tw.do("GET", "/settings/github?installed=1&installation=7", nil, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/settings/github?switched=github&revoke=1" {
		t.Fatalf("owner: %d %v", code, h)
	}
	if s := cc.conf().Servers["github"]; s.Auth == nil || !s.Auth.GitHubApp || s.Auth.Account != "" || s.level() != accessFree {
		t.Fatalf("github %+v", s)
	}
}

// An installation made long ago gets no connection by itself: one the user
// removed stays removed.
func TestGitHubOldInstallStaysUnconnected(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	f.org = true
	f.created = time.Now().Add(-time.Hour)
	withApp(t, cc.core, f)
	tw := newTestWebFor(t, cc.core)
	tw.setup()
	if code, body, _ := tw.do("GET", "/settings/github?installed=1&installation=8", nil, nil); code != 200 || len(githubServers(cc.core)) != 0 ||
		!strings.Contains(body, `value="github-acme"`) {
		t.Fatalf("old: %d %v", code, cc.conf().Servers)
	}
}

// githubServers are the catalog's GitHub connections.
func githubServers(c *core) []string {
	var out []string
	for name, s := range c.conf().Servers {
		if isGitHubServer(name, s) {
			out = append(out, name)
		}
	}
	return out
}

// A tool token GitHub refuses (its installation removed and made again,
// say) is dropped: the next call mints one from the installation there now,
// instead of the cached one for up to an hour.
func TestGitHubRefusedToolTokenDropped(t *testing.T) {
	c, _ := credCore(t, "")
	f := newFakeGitHub(t)
	withApp(t, c, f)
	ctx := context.Background()
	if tok, err := c.github.toolToken(ctx, "", false); err != nil || tok != "ghs_x" {
		t.Fatalf("first: %q %v", tok, err)
	}
	refused := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer ghs_x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()
	tt := tokenTransport{source: func(ctx context.Context) (string, error) { return c.github.toolToken(ctx, "", false) },
		refused: func() { refused++; c.github.dropToolToken("", false) }, base: http.DefaultTransport}
	client := &http.Client{Transport: tt}
	resp, err := client.Get(api.URL)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || refused != 1 {
		t.Fatalf("refused: %v %v, %d", resp, err, refused)
	}
	resp.Body.Close()
	resp, err = client.Get(api.URL)
	if err != nil || resp.StatusCode != http.StatusOK || len(f.mints()) != 2 {
		t.Fatalf("again: %v %v, %d mints", resp, err, len(f.mints()))
	}
	resp.Body.Close()
}
