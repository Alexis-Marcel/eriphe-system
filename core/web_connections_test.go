package main

import (
	"net/url"
	"slices"
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

// A tool's folders are a tree on its account's page: the root, the
// account's grid, Never until set; a folder follows its parent, or has
// cases of its own, set in its drawer, which keep it in the tool's
// configuration; back to following, it is no longer kept.
func TestPlaceTree(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	connectionSignIn(t, tw, g, csrf, "google", url.Values{"tool": {"files"}})
	waitStatus(t, c.pool.get(filesService.Base), statusReady)

	if code, _, _ := tw.do("GET", "/settings/tools/nothing/tree", nil, nil); code != 404 {
		t.Fatalf("a tool of no places: %d", code)
	}
	base := "/settings/tools/" + filesService.Base + "/tree"
	if code, body, _ := tw.do("GET", base+"?account=sam%40example.org", nil, nil); code != 200 || !strings.Contains(body, "My Drive") ||
		!strings.Contains(body, "parents=root-id") || !strings.Contains(body, "Comme le compte") || strings.Contains(body, "<html") {
		t.Fatalf("top: %d %s", code, body)
	}
	_, body, _ := tw.do("GET", base+"?account=sam%40example.org&parents=root-id&pname=My+Drive", nil, nil)
	if !strings.Contains(body, "Compta") || !strings.Contains(body, "Comme My Drive") || !strings.Contains(body, "pname=Compta") {
		t.Fatalf("browse: %s", body)
	}
	// The root: the account's grid, Never until set, no column to follow.
	if _, body, _ := tw.do("GET", base+"/grid?account=sam%40example.org", nil, nil); !strings.Contains(body, "Tout le Drive") ||
		!strings.Contains(body, `name="case-read" value="never" aria-label="Lire : Jamais" checked`) || strings.Contains(body, `name="case-read" value=""`) {
		t.Fatalf("root: %s", body)
	}
	if _, body, _ := tw.post(base+"/case", url.Values{"account": {"sam@example.org"}, "case-read": {gridFree}}, csrf); strings.Contains(body, `role="alert"`) ||
		c.conf().Servers[filesService.Base].rootAccess("sam@example.org").grid != (accessGrid{gridFree, gridNever, gridNever, gridNever}) {
		t.Fatalf("root read: %s", body)
	}
	if p := c.places(); len(p) != 1 || !p[0].Root || p[0].Name != "sam@example.org" {
		t.Fatalf("the whole account: %+v", p)
	}
	// A folder's drawer: its parent's column, and only the tool's cases.
	ref := url.Values{"account": {"sam@example.org"}, "id": {"compta"}, "name": {"Compta"}, "parents": {"root-id"}, "pname": {"My Drive"}}
	if code, body, _ := tw.do("GET", base+"/grid?"+ref.Encode(), nil, nil); code != 200 || !strings.Contains(body, `name="case-delete" value=""`) ||
		strings.Contains(body, `name="case-send"`) || !strings.Contains(body, "Comme My Drive") || !strings.Contains(body, `aria-label="Lire : Comme My Drive (Libre)" checked`) {
		t.Fatalf("drawer: %d %s", code, body)
	}
	// Supprimer free for this folder only, Modifier never: the folder's case
	// wins over what it follows, an action's exception over both. The
	// answer is the grid, and the folder's summary in the list.
	post := func(cases url.Values) string {
		t.Helper()
		v := url.Values{}
		for k, x := range ref {
			v[k] = x
		}
		for k, x := range cases {
			v[k] = x
		}
		_, body, _ := tw.post(base+"/case", v, csrf)
		return body
	}
	body = post(url.Values{"case-read": {""}, "case-change": {gridNever}, "case-delete": {gridFree}, "case-send": {gridFree}})
	if strings.Contains(body, `role="alert"`) || !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, "Modifier : Jamais · Supprimer : Libre") {
		t.Fatalf("cases: %s", body)
	}
	if g := c.conf().Servers[filesService.Base].PlaceGrids["compta"]; g != (accessGrid{Change: gridNever, Delete: gridFree}) {
		t.Fatalf("kept: %+v", g)
	}
	p := c.placeNamed("compta")
	if p == nil || p.Account != "sam@example.org" || p.Tool != filesService.Base || !slices.Equal(p.Parents, []string{"root-id"}) {
		t.Fatalf("place: %+v", p)
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
	// The root changed in its drawer keeps the account's exceptions.
	tw.post(base+"/case", url.Values{"account": {"sam@example.org"}, "case-delete": {gridNever}}, csrf)
	if a := c.conf().Servers[filesService.Base].AccountGrids["sam@example.org"]; a.Grid.Delete != gridNever || a.Exceptions["write_file"] != gridFree {
		t.Fatalf("root again: %+v", a)
	}
	if body := post(url.Values{"case-change": {gridNever}, "case-delete": {""}}); !strings.Contains(body, `hx-swap-oob="true">Modifier : Jamais</span>`) ||
		!strings.Contains(body, `name="case-delete" value="" aria-label="Supprimer : Comme My Drive (Jamais)" checked`) {
		t.Fatalf("back to its parent's: %s", body)
	}
	post(url.Values{"case-change": {""}})
	if len(c.places()) != 1 || c.conf().Servers[filesService.Base].PlaceGrids != nil {
		t.Fatalf("still kept: %+v", c.places())
	}
}
