package main

import (
	"net/url"
	"strings"
	"testing"
)

// connectionSignIn connects through the tools list's generic route, as the
// browser would: the form, the provider's consent (the fake), the way back.
func connectionSignIn(t *testing.T, tw *testWeb, g *fakeGoogle, csrf, provider string, form url.Values) (url.Values, string) {
	t.Helper()
	code, body, h := tw.post("/settings/connections/"+provider+"/connect", form, csrf)
	authURL := h.Get("HX-Redirect")
	if code != 200 || authURL == "" {
		t.Fatalf("connect: %d %s", code, body)
	}
	state := g.authorize(t, authURL)
	u, _ := url.Parse(authURL)
	_, body, _ = tw.do("GET", "/oauth/callback?"+url.Values{"code": {"the-code"}, "state": {state}}.Encode(), nil, nil)
	_, after, ok := strings.Cut(body, `content="0;url=`)
	if !ok {
		t.Fatalf("callback: %s", body)
	}
	next, _, _ := strings.Cut(after, `"`)
	return u.Query(), next
}

// An account is connected, added to, reconnected and removed from the
// tools list, by the provider's name in the address: nothing there is
// one provider's.
func TestConnectionRoutes(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})

	if code, _, _ := tw.post("/settings/connections/nowhere/connect", url.Values{"tool": {"mail"}}, csrf); code != 404 {
		t.Fatalf("an unknown provider: %d", code)
	}
	if _, body, h := tw.post("/settings/connections/google/connect", nil, csrf); h.Get("HX-Redirect") != "" || !strings.Contains(body, `role="alert"`) {
		t.Fatalf("no tool ticked: %s", body)
	}

	g.scope = scopeMail
	q, next := connectionSignIn(t, tw, g, csrf, "google", url.Values{"tool": {"mail"}})
	if q.Get("scope") != "openid email "+scopeMail || next != "/settings/tools?connected=1" {
		t.Fatalf("connect: %v %q", q, next)
	}
	cn, ok := c.connectionOf("google", "sam@example.org")
	if !ok || len(c.connectionTools(cn)) != 1 {
		t.Fatalf("connection: %+v %v", cn, ok)
	}

	// The card: who gives it, the tool it could add, and its removal.
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	body = body[strings.Index(body, `id="tools-list"`):strings.Index(body, `id="add-dlg"`)]
	for _, want := range []string{"Google · Mail", `hx-post="/settings/connections/google/connect"`, `name="tool" value="drive"`,
		`hx-post="/settings/connections/google/remove"`, "Retirer ce compte"} {
		if !strings.Contains(body, want) {
			t.Errorf("tools list lacks %q", want)
		}
	}
	if strings.Contains(body, `href="/settings/google"`) {
		t.Error("the card leads to the Google page")
	}

	// Adding Drive asks for its scope only, for that account.
	g.scope = scopeMail + " " + scopeDrive
	q, _ = connectionSignIn(t, tw, g, csrf, "google", url.Values{"tool": {"drive"}, "account": {"sam@example.org"}, "write": {"1"}})
	if q.Get("scope") != "openid email "+scopeDrive || q.Get("login_hint") != "sam@example.org" {
		t.Fatalf("add drive: %v", q)
	}

	// Lapsed: Reconnect asks again for what it had.
	c.markConnection(cn.ID, lapsedRevoked)
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	if !strings.Contains(body, "Le compte Google sam@example.org est à reconnecter") || !strings.Contains(body, `name="reconnect" value="1"`) {
		t.Fatalf("to reconnect: %s", mainPart(t, body))
	}
	q, _ = connectionSignIn(t, tw, g, csrf, "google", url.Values{"account": {"sam@example.org"}, "reconnect": {"1"}})
	if q.Get("scope") != "openid email "+scopeMail+" "+scopeDrive || q.Get("login_hint") != "sam@example.org" {
		t.Fatalf("reconnect: %v", q)
	}
	if cn, _ := c.connectionOf("google", "sam@example.org"); cn.Lapsed != "" {
		t.Fatal("still to reconnect")
	}

	// Removed: its tools, its token, and its authorization.
	if code, _, h := tw.post("/settings/connections/google/remove", url.Values{"account": {"sam@example.org"}}, csrf); code != 200 || h.Get("HX-Refresh") != "true" {
		t.Fatalf("remove: %d", code)
	}
	if _, ok := c.connectionOf("google", "sam@example.org"); ok || len(g.revokes) != 1 {
		t.Fatalf("still there: %v, %d revokes", ok, len(g.revokes))
	}
	if _, ok := c.conf().Servers["mail"]; ok {
		t.Error("its mail stays")
	}
}

// A tool's places are picked on its page: browsed by account through its
// setup tool, given to read or to drop into, and taken back.
func TestPlacesPage(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	g.scope = scopeDriveReadonly
	connectionSignIn(t, tw, g, csrf, "google", url.Values{"tool": {"drive"}})
	waitStatus(t, c.pool.get(filesServer), statusReady)

	if code, _, _ := tw.do("GET", "/settings/tools/nothing/places", nil, nil); code != 404 {
		t.Fatalf("a tool of no places: %d", code)
	}
	base := "/settings/tools/" + filesServer + "/places"
	if code, body, _ := tw.do("GET", base, nil, nil); code != 200 || !strings.Contains(body, "Aucun lieu") || !strings.Contains(body, base+"/browse") {
		t.Fatalf("page: %d %s", code, mainPart(t, body))
	}
	hx := map[string]string{"HX-Request": "true"}
	_, body, _ := tw.do("GET", base+"/browse?parent=root-id&name=My+Drive", nil, hx)
	if !strings.Contains(body, "Compta") || !strings.Contains(body, `value="compta"`) || strings.Contains(body, `value="drop"`) {
		t.Fatalf("browse (read only): %s", body)
	}
	if _, body, _ := tw.post(base, url.Values{"id": {"compta"}, "name": {"Compta"}, "access": {"drop"}}, csrf); !strings.Contains(body, `role="alert"`) {
		t.Fatalf("drop on a read-only account: %s", body)
	}
	if _, body, _ := tw.post(base, url.Values{"id": {"compta"}, "name": {"Compta"}, "access": {"read"}}, csrf); !strings.Contains(body, "Compta") || strings.Contains(body, `role="alert"`) {
		t.Fatalf("give: %s", body)
	}
	if p := c.placeNamed("compta"); p == nil || p.Account != "sam@example.org" || p.Access != accessRead || p.Tool != filesServer {
		t.Fatalf("place: %+v", p)
	}
	if _, body, _ := tw.do("GET", base+"/browse?parent=root-id", nil, hx); !strings.Contains(body, "Lecture</span>") {
		t.Fatalf("given, in the browse: %s", body)
	}
	if _, body, _ := tw.post(base+"/remove", url.Values{"id": {"compta"}}, csrf); !strings.Contains(body, "Aucun lieu") {
		t.Fatalf("remove: %s", body)
	}
	if len(c.places()) != 0 {
		t.Fatal("still given")
	}
}

// The pages before connections send to the tools list.
func TestOldConnectionPages(t *testing.T) {
	tw, _, _ := filesCore(t, nil)
	for _, old := range []string{"/settings/google", "/settings/drive"} {
		if code, _, h := tw.do("GET", old, nil, nil); code != 301 || h.Get("Location") != "/settings/tools" {
			t.Errorf("%s: %d %s", old, code, h.Get("Location"))
		}
	}
}
