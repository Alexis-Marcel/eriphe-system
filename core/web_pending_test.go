package main

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pendingWeb is the workspace over a core that really dials its servers,
// with an open vault, and a fake OAuth service.
func pendingWeb(t *testing.T) (*testWeb, *core, *fakeAS, string) {
	t.Helper()
	f := newFakeAS(t) // first: closed after the core stops
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
	c := newCore(&Config{}, store)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		c.pool.mu.Lock()
		for _, ps := range c.pool.pending {
			ps.stop()
		}
		c.pool.mu.Unlock()
		cancel()
	})
	c.pool.run(ctx)
	tw := newTestWebFor(t, c)
	return tw, c, f, tw.setup()
}

func oauthToolForm(f *fakeAS) url.Values {
	return url.Values{"preset": {"remote"}, "name": {"linear"}, "use": {"issues"}, "kind": {"remote"},
		"url": {f.srv.URL + "/mcp"}, "auth": {"oauth"}, "connect": {"1"}}
}

// startConnect posts the form and returns the authorization URL.
func startConnectForm(t *testing.T, tw *testWeb, csrf, path string, form url.Values) string {
	t.Helper()
	code, body, h := tw.post(path, form, csrf)
	authURL := h.Get("HX-Redirect")
	if code != 200 || !strings.Contains(authURL, "/authorize") {
		t.Fatalf("connect: %d %v %s", code, h, body)
	}
	return authURL
}

func noPending(t *testing.T, c *core) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.pool.mu.Lock()
		n := len(c.pool.pending)
		c.pool.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a pending server is left")
}

func callback(t *testing.T, tw *testWeb, query string) (int, string) {
	t.Helper()
	code, body, _ := tw.do("GET", "/oauth/callback?"+query, nil, nil)
	return code, html.UnescapeString(body)
}

func addConnected(t *testing.T, tw *testWeb, c *core, f *fakeAS, csrf string) {
	t.Helper()
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", oauthToolForm(f))
	// Pending: not in the catalog, not listed, not lent.
	if _, ok := c.conf().Servers["linear"]; ok || c.pool.get("linear") != nil || len(c.listServers("", false)) != 0 {
		t.Fatal("a pending server is visible")
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); strings.Contains(body, "/settings/tools/linear/edit") {
		t.Fatal("the tools page shows a pending server")
	}
	code, state := f.authorize(authURL)
	status, body := callback(t, tw, "code="+code+"&state="+url.QueryEscape(state))
	if status != 200 || !strings.Contains(body, `content="0;url=/settings/tools?connected=linear#added/linear"`) {
		t.Fatalf("callback: %d %s", status, body)
	}
	if _, ok := c.conf().Servers["linear"]; !ok {
		t.Fatal("not saved after the authorization")
	}
	waitStatus(t, c.pool.get("linear"), statusReady)
	noPending(t, c)
	if cfg, tok, _ := c.store.oauthToken(pendingKey("linear")); cfg != nil || tok != nil {
		t.Fatal("tokens left under the pending name")
	}
}

func TestPendingOAuthConnect(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	// The form's primary action, for an OAuth preset.
	if _, body, _ := tw.do("GET", "/settings/tools/new?preset=linear", nil, nil); !strings.Contains(body, "Se connecter à Linear") ||
		!strings.Contains(body, `name="connect" value="1"`) || !strings.Contains(body, "Tu seras envoyé chez Linear") {
		t.Fatal("no connect button in the form")
	}
	addConnected(t, tw, c, f, csrf)
	if _, body, _ := tw.do("GET", "/settings/tools?connected=linear", nil, nil); !strings.Contains(html.UnescapeString(body), "« linear » est connecté") ||
		!strings.Contains(body, "srv-ready") || !strings.Contains(body, "/settings/servers/linear/connect") {
		t.Fatalf("tools page: %s", body)
	}
	// A plain save of the form, without connect, still goes through the
	// authorization for a new OAuth tool.
	form := oauthToolForm(f)
	form.Del("connect")
	form.Set("name", "linear2")
	startConnectForm(t, tw, csrf, "/settings/tools", form)
	if _, ok := c.conf().Servers["linear2"]; ok {
		t.Fatal("saved before its authorization")
	}
}

func TestPendingOAuthCancel(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	// A new tool: dropped.
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", oauthToolForm(f))
	u, _ := url.Parse(authURL)
	status, body := callback(t, tw, "error=access_denied&state="+url.QueryEscape(u.Query().Get("state")))
	if status != 200 || !strings.Contains(body, "notconnected=linear&why=cancelled") || strings.Contains(body, "kept=1") {
		t.Fatalf("cancel: %d %s", status, body)
	}
	noPending(t, c)
	if _, ok := c.conf().Servers["linear"]; ok {
		t.Fatal("a cancelled new tool was saved")
	}
	if _, body, _ := tw.do("GET", "/settings/tools?notconnected=linear&why=cancelled", nil, nil); !strings.Contains(html.UnescapeString(body), "Connexion à « linear » annulée. L'outil n'a pas été ajouté.") {
		t.Fatalf("tools page: %s", body)
	}

	// An edit: the previous version stays, still connected.
	addConnected(t, tw, c, f, csrf)
	form := oauthToolForm(f)
	form.Set("use", "changed")
	authURL = startConnectForm(t, tw, csrf, "/settings/tools/linear", form)
	u, _ = url.Parse(authURL)
	if _, body := callback(t, tw, "error=access_denied&state="+url.QueryEscape(u.Query().Get("state"))); !strings.Contains(body, "why=cancelled&kept=1") {
		t.Fatalf("cancel edit: %s", body)
	}
	noPending(t, c)
	if got := c.conf().Servers["linear"].Use; got != "issues" {
		t.Fatalf("use = %q: the edit was saved", got)
	}
	waitStatus(t, c.pool.get("linear"), statusReady)
	if _, body, _ := tw.do("GET", "/settings/tools?notconnected=linear&why=cancelled&kept=1", nil, nil); !strings.Contains(html.UnescapeString(body), "garde sa version précédente") {
		t.Fatal("no kept notice")
	}
	// The edit form of a server already on OAuth can save without signing in.
	if _, body, _ := tw.do("GET", "/settings/tools/linear/edit", nil, nil); !strings.Contains(body, "Enregistrer sans se reconnecter") {
		t.Fatal("no plain save on the edit form")
	}
	form.Del("connect")
	if code, _, h := tw.post("/settings/tools/linear", form, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=linear" {
		t.Fatalf("plain save: %d %v", code, h)
	}
	if c.conf().Servers["linear"].Use != "changed" {
		t.Fatal("plain save lost")
	}
}

func TestPendingOAuthExpiry(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	old := pendingTimeout
	pendingTimeout = 300 * time.Millisecond
	t.Cleanup(func() { pendingTimeout = old })
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", oauthToolForm(f))
	noPending(t, c)
	if _, ok := c.conf().Servers["linear"]; ok {
		t.Fatal("an expired pending tool was saved")
	}
	code, state := f.authorize(authURL)
	if status, body := callback(t, tw, "code="+code+"&state="+url.QueryEscape(state)); status != http.StatusBadRequest || !strings.Contains(body, "plus valable") {
		t.Fatalf("late callback: %d %s", status, body)
	}
	if _, ok := c.conf().Servers["linear"]; ok {
		t.Fatal("a late callback saved it")
	}
}

func TestPendingOAuthGuards(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	if code, _, _ := tw.post("/settings/tools", oauthToolForm(f), "forged"); code != http.StatusForbidden {
		t.Fatalf("forged CSRF: %d", code)
	}
	// A refused form starts nothing.
	form := oauthToolForm(f)
	form.Set("use", "")
	if code, body, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") != "" || !strings.Contains(body, "alert") {
		t.Fatalf("invalid form: %d %v", code, h)
	}
	c.pool.mu.Lock()
	n := len(c.pool.pending)
	c.pool.mu.Unlock()
	if n != 0 || f.registered() != nil {
		t.Fatal("a refused form started an authorization")
	}
}

func TestPendingOAuthUnreachable(t *testing.T) {
	tw, c, _, csrf := pendingWeb(t)
	form := url.Values{"preset": {"remote"}, "name": {"gone"}, "use": {"x"}, "kind": {"remote"},
		"url": {"http://127.0.0.1:1/mcp"}, "auth": {"oauth"}, "connect": {"1"}}
	code, body, h := tw.post("/settings/tools", form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "" || !strings.Contains(html.UnescapeString(body), "Adresse injoignable") {
		t.Fatalf("unreachable: %d %v %s", code, h, body)
	}
	noPending(t, c)
	if _, ok := c.conf().Servers["gone"]; ok {
		t.Fatal("saved")
	}
}
