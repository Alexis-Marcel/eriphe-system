package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	for _, want := range []string{"Google · Mail", `hx-post="/settings/connections/google/connect"`, `name="tool" value="files"`,
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
	q, _ = connectionSignIn(t, tw, g, csrf, "google", url.Values{"tool": {"files"}, "account": {"sam@example.org"}, "write": {"1"}})
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

// A tool's folders are picked on its account's page: one list, the given
// ones first with their cases, then the account's own, browsed through its
// setup tool; a folder's case goes above or below its account's.
func TestPlacesPage(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	connectionSignIn(t, tw, g, csrf, "google", url.Values{"tool": {"files"}})
	waitStatus(t, c.pool.get(filesService.Base), statusReady)

	if code, _, _ := tw.do("GET", "/settings/tools/nothing/places", nil, nil); code != 404 {
		t.Fatalf("a tool of no places: %d", code)
	}
	base := "/settings/tools/" + filesService.Base + "/places"
	if code, body, _ := tw.do("GET", base, nil, nil); code != 200 || !strings.Contains(body, "Dossiers de sam@example.org") || !strings.Contains(body, "My Drive") {
		t.Fatalf("page: %d %s", code, mainPart(t, body))
	}
	hx := map[string]string{"HX-Request": "true", "HX-Target": "tool-folders"}
	_, body, _ := tw.do("GET", base+"?parent=root-id&name=My+Drive", nil, hx)
	if !strings.Contains(body, "Compta") || !strings.Contains(body, `value="compta"`) || strings.Contains(body, "<html") {
		t.Fatalf("browse: %s", body)
	}
	if code, body, _ := tw.post(base, url.Values{"id": {"compta"}, "name": {"Compta"}, "parent": {"root-id"}, "parent_name": {"My Drive"}}, csrf); !strings.Contains(body, "Compta") || strings.Contains(body, `role="alert"`) {
		t.Fatalf("give: %d %s", code, body)
	}
	p := c.placeNamed("compta")
	if p == nil || p.Account != "sam@example.org" || p.Tool != filesService.Base {
		t.Fatalf("place: %+v", p)
	}
	// Inside its parent, the given one comes first, with its cases.
	if _, body, _ := tw.do("GET", base+"?parent=root-id&name=My+Drive", nil, hx); !strings.Contains(body, `name="category" value="delete"`) {
		t.Fatalf("given, in the browse: %s", body)
	}
	// Supprimer free for this folder only, Modifier never: the folder's case
	// wins over its account's, an action's exception over both.
	for cat, v := range map[string]string{catDelete: gridFree, catChange: gridNever} {
		if _, body, _ := tw.post(base+"/case", url.Values{"id": {"compta"}, "category": {cat}, "value": {v}}, csrf); strings.Contains(body, `role="alert"`) {
			t.Fatalf("case %s: %s", cat, body)
		}
	}
	if _, body, _ := tw.post(base+"/case", url.Values{"id": {"compta"}, "category": {catSend}, "value": {gridFree}}, csrf); !strings.Contains(body, `role="alert"`) {
		t.Fatalf("a case the tool has not: %s", body)
	}
	srv := c.conf().Servers[filesService.Base]
	trash := &mcp.Tool{Name: "trash_file", Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)}}
	write := &mcp.Tool{Name: "write_file", Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}}
	if f := srv.toolFacts(trash, map[string]any{"folder": "Compta"}); f.value != gridFree {
		t.Fatalf("trash in Compta: %+v", f)
	}
	if f := srv.toolFacts(write, map[string]any{"folder": "compta"}); f.value != gridNever {
		t.Fatalf("write in Compta: %+v", f)
	}
	if err := c.setAccountGrid(filesService.Base, "sam@example.org", accountGrid{Grid: accessGrid{Read: gridFree, Change: gridApproval, Delete: gridApproval},
		Exceptions: map[string]string{"write_file": gridFree}}); err != nil {
		t.Fatal(err)
	}
	if f := c.conf().Servers[filesService.Base].toolFacts(write, map[string]any{"folder": "Compta"}); f.value != gridFree {
		t.Fatalf("an action's exception: %+v", f)
	}
	if _, body, _ := tw.post(base+"/case", url.Values{"id": {"compta"}, "category": {catDelete}, "value": {""}}, csrf); !strings.Contains(body, "Comme le compte (Sur accord)") {
		t.Fatalf("back to the account's: %s", body)
	}
	if _, body, _ := tw.post(base+"/remove", url.Values{"id": {"compta"}}, csrf); strings.Contains(body, `name="category"`) {
		t.Fatalf("remove: %s", body)
	}
	if len(c.places()) != 0 || c.conf().Servers[filesService.Base].PlaceGrids != nil {
		t.Fatal("still given")
	}
}
