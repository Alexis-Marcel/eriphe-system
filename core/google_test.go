package main

import (
	"context"
	"encoding/json"
	"html"
	"maps"
	"net/url"
	"strings"
	"testing"
)

// The Google app, for the tests (connection_apps.go).
var errNoGoogleClient = errNoAppClient

func googleClient() (oauthApp, error) { return appClient(providerGoogle) }

func storeGoogleClient(app oauthApp) error { return storeAppClient(providerGoogle, app) }

// The Google accounts connected, for the tests.
func (c *core) googleAccounts() []connection { return c.connectionsAt(providerGoogle.Name) }

func (c *core) googleAccount(email string) (connection, bool) {
	return c.connectionOf(providerGoogle.Name, email)
}

// googleTools are the tools of a Google account, by kind.
func (c *core) googleTools(email string) []connTool {
	cn, ok := c.googleAccount(email)
	if !ok {
		return nil
	}
	return c.connectionKinds(cn)
}

// googleSignIn connects through the connect route as the browser would: the
// form, Google's consent (the fake), and the way back. It returns the
// authorization URL's query and where the result page sends the browser.
func googleSignIn(t *testing.T, tw *testWeb, g *fakeGoogle, csrf string, form url.Values) (url.Values, string) {
	t.Helper()
	form = maps.Clone(form)
	if form["service"] != nil { // the tools ticked, as the Google page named them
		form["tool"], form["service"] = form["service"], nil
	}
	code, body, h := tw.post("/settings/connections/google/connect", form, csrf)
	authURL := h.Get("HX-Redirect")
	if code != 200 || authURL == "" {
		t.Fatalf("connect: %d %s", code, body)
	}
	state := g.authorize(t, authURL)
	u, _ := url.Parse(authURL)
	_, body, _ = tw.do("GET", "/oauth/callback?"+url.Values{"code": {"the-code"}, "state": {state}}.Encode(), nil, nil)
	next := ""
	if _, after, ok := strings.Cut(body, `content="0;url=`); ok {
		next, _, _ = strings.Cut(after, `"`)
	} else {
		t.Fatalf("callback: %s", body)
	}
	return u.Query(), html.UnescapeString(next)
}

func runOf(t *testing.T, c *core, name string) RunSpec {
	t.Helper()
	srv, ok := c.conf().Servers[name]
	if !ok {
		t.Fatalf("no server %s", name)
	}
	spec, err := decodeRun(srv.Run)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// One Google sign-in gives the services ticked; a service added later to
// the same account asks for its scope only, and the account keeps one
// token; a second account has tools of its own.
func TestGoogleAccounts(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, func(cfg *Config) { cfg.TimeZone = "Europe/Paris" })
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"

	// Found from "Add a tool", and from the kinds of mailbox and calendar.
	if _, body, _ := tw.do("GET", "/settings/tools/add", nil, nil); !strings.Contains(body, `href="/settings/connections/google"`) {
		t.Fatal("no Google tile")
	}
	if _, body, _ := tw.do("GET", "/settings/tools/new?preset=mail", nil, nil); !strings.Contains(body, `href="/settings/connections/google?tool=mail"`) ||
		!strings.Contains(body, "Gmail avec ton compte Google") {
		t.Fatalf("mail picker: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/tools/new?preset=calendar", nil, nil); !strings.Contains(body, `href="/settings/connections/google?tool=calendar"`) {
		t.Fatalf("calendar picker: %s", body)
	}

	// The app first, from the connect page.
	_, body, _ := tw.do("GET", "/settings/connections/google", nil, nil)
	if !strings.Contains(body, "Créer mon app") || !strings.Contains(body, `href="/settings/apps/google?back=`) ||
		strings.Contains(body, `hx-post="/settings/connections/google/connect"`) {
		t.Fatalf("connect before the app: %s", mainPart(t, body))
	}
	if _, body, _ = tw.post("/settings/apps/google", url.Values{"client_id": {testClientID}, "client_secret": {testClientSecret}}, csrf); strings.Contains(body, testClientSecret) ||
		!strings.Contains(body, testClientID) {
		t.Fatalf("client saved: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/connections/google?tool=calendar", nil, nil); strings.Contains(body, `value="mail" checked`) ||
		!strings.Contains(body, `value="calendar" checked`) || !strings.Contains(body, "Google Agenda") {
		t.Fatalf("?tool=calendar: %s", mainPart(t, body))
	}
	if _, body, _ = tw.post("/settings/connections/google/connect", url.Values{}, csrf); !strings.Contains(body, "Coche au moins un outil") {
		t.Fatalf("no tool: %s", body)
	}

	// Mail and calendar, in one sign-in.
	g.scope = scopeMail + " " + scopeCalendar
	q, next := googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail", "calendar"}})
	if q.Get("scope") != "openid email "+scopeMail+" "+scopeCalendar || q.Get("include_granted_scopes") != "true" || q.Get("login_hint") != "" {
		t.Fatalf("auth query %v", q)
	}
	if next != "/settings/tools?connected=1" {
		t.Fatalf("next %q", next)
	}
	sam, _ := c.googleAccount("sam@example.org")
	ref := sam.Secret
	mail, cal := runOf(t, c.core, "mail"), runOf(t, c.core, "agenda")
	samMail, samCal := accountEnv(mailTokenEnv, "sam@example.org"), accountEnv(calTokenEnv, "sam@example.org")
	if mail.Env[samMail].File != vaultPrefix+ref || cal.Env[samCal].File != vaultPrefix+ref ||
		!strings.Contains(mail.Env["MAIL_CONFIG"].Value, `"oauth_token_env":"`+samMail+`"`) ||
		!strings.Contains(cal.Env["CALENDAR_CONFIG"].Value, `"provider":"google"`) || !strings.Contains(cal.Env["CALENDAR_CONFIG"].Value, "Europe/Paris") {
		t.Fatalf("specs: %+v %+v", mail, cal)
	}
	for _, name := range []string{"mail", "agenda"} {
		if raw, _ := json.Marshal(c.conf().Servers[name]); strings.Contains(string(raw), "the-refresh") {
			t.Fatalf("a token in the spec of %s", name)
		}
	}
	env, err := lendService(mail)
	if err != nil || !strings.Contains(env[samMail], `"refresh_token":"the-refresh"`) {
		t.Fatalf("lent: %v %v", env, err)
	}
	if _, ok := c.conf().Servers[filesServer]; ok {
		t.Fatal("Drive connected without being asked")
	}
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	// One card for the account: its address, its services, its actions,
	// and Drive to add to it.
	card := rowKey("acct", "google:sam@example.org")
	body = body[strings.Index(body, `id="tools-list"`):]
	body = body[:strings.Index(body, `id="add-dlg"`)]
	if !strings.Contains(body, `<h3 id="`+card+`-t">sam@example.org</h3>`) || !strings.Contains(body, "Google · Mail, Agenda") ||
		!strings.Contains(body, "<h3>Mail</h3>") || !strings.Contains(body, "<h3>Agenda</h3>") || !strings.Contains(body, "Google Agenda") ||
		strings.Contains(body, `/settings/tools/mail/edit"`) || !strings.Contains(body, `hx-post="/settings/connections/google/remove"`) ||
		!strings.Contains(body, "Ajouter à ce compte") || !strings.Contains(body, `<input type="hidden" name="tool" value="drive">`) {
		t.Fatalf("tools list: %s", body)
	}

	// Drive added to the same account: its scope alone, the account named.
	g.scope, g.refresh = scopeMail+" "+scopeCalendar+" "+scopeDrive, "the-refresh-2"
	q, _ = googleSignIn(t, tw, g, csrf, url.Values{"service": {"drive"}, "account": {"sam@example.org"}, "write": {"1"}})
	if q.Get("scope") != "openid email "+scopeDrive || q.Get("login_hint") != "sam@example.org" {
		t.Fatalf("auth query %v", q)
	}
	if n := len(c.googleTools("sam@example.org")); n != 3 || len(c.googleAccounts()) != 1 {
		t.Fatalf("%d tools, %d accounts", n, len(c.googleAccounts()))
	}
	if env, _ := lendService(runOf(t, c.core, "mail")); !strings.Contains(env[samMail], "the-refresh-2") {
		t.Fatal("the mail server does not read the new token")
	}
	if acc := c.placeAccounts(filesServer); len(acc) != 1 || acc[0].Name != "sam@example.org" || !acc[0].CanWrite {
		t.Fatalf("drive: %+v", acc)
	}
	// In the tools list, Drive is a service of the account's card, with
	// that account's folders, and there is nothing left to add.
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	body = body[strings.Index(body, `id="tools-list"`):]
	body = body[:strings.Index(body, `id="add-dlg"`)]
	driveRow := rowKey(filesServer, "sam@example.org")
	if !strings.Contains(body, "Google · Mail, Agenda, Drive") || !strings.Contains(body, `id="tool-`+driveRow+`"`) || !strings.Contains(body, "<h3>Drive</h3>") ||
		!strings.Contains(body, "Google Drive · aucun lieu") || strings.Contains(body, "Ajouter à ce compte") || strings.Count(body, `class="tool-group`) != 1 ||
		strings.Contains(body, `hx-post="/settings/tools/`+filesServer+`/delete"`) || !strings.Contains(body, `href="/settings/tools/`+filesServer+`/places"`) {
		t.Fatalf("tools list with Drive: %s", body)
	}
	if _, s, _ := tw.do("GET", "/settings/tools/"+filesServer+"/row?account=sam@example.org", nil, map[string]string{"HX-Request": "true"}); !strings.Contains(s, "<h3>Drive</h3>") {
		t.Errorf("drive row: %s", s)
	}

	// Its services just added are starting, not in error (their first
	// dial fails until they run): the accounts ask for themselves again.
	for _, name := range []string{"mail", "agenda", filesServer} {
		u := c.pool.get(name)
		u.mu.Lock()
		u.status, u.err = statusError, "service "+name+" has no node: not placed yet"
		u.mu.Unlock()
	}
	for _, name := range []string{"mail", "agenda", filesServer} {
		if _, err := c.store.assignService(name, "nasa"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"mail", "agenda", filesServer} {
		if err := c.store.updateService(name, map[string]any{"state": stateFailed, "error": "pull access denied for x"}); err != nil {
			t.Fatal(err)
		}
	}
	// In the tools list, a row says the state of its service on its
	// machine, never which service of the account it is.
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	if strings.Contains(body, "service.mail") || strings.Contains(body, "service.calendar") || strings.Contains(body, "service.drive") ||
		strings.Count(body, " · "+tr("fr", "service."+stateFailed)) < 3 {
		t.Fatalf("service state: %s", body[strings.Index(body, `id="tools-list"`):strings.Index(body, `id="add-dlg"`)])
	}

	// A second account, for mail: one more account of the same server.
	g.email, g.scope, g.refresh = "bob@example.org", scopeMail, "bob-refresh"
	googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail"}})
	bobMail := accountEnv(mailTokenEnv, "bob@example.org")
	if spec := runOf(t, c.core, "mail"); spec.Env[bobMail].File != vaultPrefix+bobSecret(t, c.core) ||
		spec.Env[samMail].File != vaultPrefix+ref || !strings.Contains(spec.Env["MAIL_CONFIG"].Value, `"name":"bob@example.org"`) {
		t.Fatalf("second account: %+v", spec)
	}
	if _, ok := c.conf().Servers["mail-bob"]; ok || strings.Contains(c.conf().Servers["mail"].Use, "@") {
		t.Fatalf("a server for the second account: %+v", c.conf().Servers)
	}
	if tools := c.googleTools("bob@example.org"); len(tools) != 1 || tools[0].Name != "mail" {
		t.Fatalf("bob's tools: %+v", tools)
	}
	if lv := c.conf().Servers["mail"].AccountAccess; lv["bob@example.org"] != accessReads || lv["sam@example.org"] != accessReads {
		t.Fatalf("levels: %v", lv)
	}

	// A service Google did not grant is said.
	g.scope = scopeMail
	if _, next := googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail", "calendar"}, "account": {"bob@example.org"}}); next != "/settings/tools?connected=1&missing=calendar" {
		t.Fatalf("missing: %q", next)
	}
	if _, body, _ := tw.do("GET", "/settings/tools?connected=1&missing=calendar", nil, nil); !strings.Contains(body, "Le fournisseur n&#39;a pas donné l&#39;accès à : Agenda") {
		t.Fatalf("missing: %s", body)
	}

	// Removed: its tool, its token, and its authorization at Google.
	bobRef := bobSecret(t, c.core)
	if code, _, h := tw.post("/settings/connections/google/remove", url.Values{"account": {"bob@example.org"}}, csrf); code != 200 || h.Get("HX-Refresh") != "true" {
		t.Fatalf("removed: %d", code)
	}
	if spec := runOf(t, c.core, "mail"); spec.Env[bobMail].File != "" || strings.Contains(spec.Env["MAIL_CONFIG"].Value, "bob@") ||
		spec.Env[samMail].File != vaultPrefix+ref {
		t.Fatalf("its account stays, or sam's went: %+v", spec)
	}
	if _, err := currentVault().get(bobRef); err == nil {
		t.Fatal("its token stays")
	}
	if len(g.revokes) != 1 || g.revokes[0] != "bob-refresh" {
		t.Fatalf("revoked %v", g.revokes)
	}
	if _, ok := c.conf().Servers["mail"]; !ok {
		t.Fatal("the other account's tool went too")
	}
}

// An account Google refuses is "to reconnect": its tools wait, the brain's
// list says where to reconnect, a call says why, and one click reconnects
// it with the services it had.
func TestGoogleReconnect(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} })
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	g.scope = scopeMail + " " + scopeDrive
	googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail", "drive"}, "write": {"1"}})
	waitStatus(t, c.pool.get("mail"), statusReady)

	check := func() { a, _ := c.googleAccount("sam@example.org"); c.checkConnection(context.Background(), a) }
	check()
	if a, _ := c.googleAccount("sam@example.org"); a.Lapsed != "" || g.refreshes != 1 {
		t.Fatalf("a working account: %+v, %d refreshes", a, g.refreshes)
	}
	g.revoked = true
	check()
	if a, _ := c.googleAccount("sam@example.org"); a.Lapsed == "" {
		t.Fatal("not marked")
	}
	check() // told once
	told := 0
	msgs, _ := c.store.messagesAfter(0, 100)
	for _, m := range msgs {
		if m.Role == roleEvent && strings.Contains(m.Text, "Google a retiré l'autorisation du compte sam@example.org") {
			told++
			if !strings.Contains(m.Text, "fichiers, mail") || !strings.Contains(m.Text, "révoquée") || !strings.Contains(m.Text, "Reconnecter") {
				t.Fatalf("event: %s", m.Text)
			}
		}
	}
	if told != 1 {
		t.Fatalf("the brain was told %d times: %+v", told, msgs)
	}
	waitStatus(t, c.pool.get("mail"), statusNeedsAuth)
	waitStatus(t, c.pool.get(filesServer), statusNeedsAuth)
	if s := c.listServers("mail", false); s[0].Connect != "Settings → Tools → Google → Reconnect sam@example.org" {
		t.Fatalf("list_tools: %+v", s[0])
	}
	if _, err := c.pool.get("mail").call(context.Background(), "list_files", nil); err == nil || !strings.Contains(err.Error(), "reconnects it in the workspace") {
		t.Fatalf("call: %v", err)
	}
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	if !strings.Contains(body, "Le compte Google sam@example.org est à reconnecter") || !strings.Contains(body, `name="reconnect" value="1"`) {
		t.Fatalf("tools list: %s", body)
	}

	// One click: the services it had, Drive with its rights.
	g.revoked = false
	q, next := googleSignIn(t, tw, g, csrf, url.Values{"account": {"sam@example.org"}, "reconnect": {"1"}, "back": {"/settings/tools"}})
	if q.Get("scope") != "openid email "+scopeMail+" "+scopeDrive || q.Get("login_hint") != "sam@example.org" || next != "/settings/tools?connected=1" {
		t.Fatalf("reconnect: %v %q", q, next)
	}
	if a, _ := c.googleAccount("sam@example.org"); a.Lapsed != "" {
		t.Fatal("still to reconnect")
	}
	waitStatus(t, c.pool.get("mail"), statusReady)
}

func TestIDTokenEmail(t *testing.T) {
	if e := idTokenEmail(fakeIDToken("sam@example.org")); e != "sam@example.org" {
		t.Fatalf("email %q", e)
	}
	for _, bad := range []string{"", "a.b", "x." + "e30" + ".y", fakeIDToken("not an address")} {
		if e := idTokenEmail(bad); e != "" {
			t.Errorf("%q: %q", bad, e)
		}
	}
}

func bobSecret(t *testing.T, c *core) string {
	t.Helper()
	cn, ok := c.googleAccount("bob@example.org")
	if !ok {
		t.Fatal("no connection for bob")
	}
	return cn.Secret
}
