package main

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"strings"
	"testing"
)

// googleSignIn connects through the Google page as the browser would: the
// form, Google's consent (the fake), and the way back. It returns the
// authorization URL's query and where the result page sends the browser.
func googleSignIn(t *testing.T, tw *testWeb, g *fakeGoogle, csrf string, form url.Values) (url.Values, string) {
	t.Helper()
	code, body, h := tw.post("/settings/google/connect", form, csrf)
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
	if _, body, _ := tw.do("GET", "/settings/tools/add", nil, nil); !strings.Contains(body, `href="/settings/google"`) {
		t.Fatal("no Google tile")
	}
	if _, body, _ := tw.do("GET", "/settings/tools/new?preset=mail", nil, nil); !strings.Contains(body, `href="/settings/google?add=mail"`) ||
		!strings.Contains(body, "Gmail avec ton compte Google") {
		t.Fatalf("mail picker: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/tools/new?preset=calendar", nil, nil); !strings.Contains(body, `href="/settings/google?add=calendar"`) {
		t.Fatalf("calendar picker: %s", body)
	}

	// The guide first: once for every service, with what to copy.
	_, body, _ := tw.do("GET", "/settings/google", nil, nil)
	for _, want := range []string{"Crée ton app Google", "pour tous les services et tous tes comptes", googleAPIs,
		`<pre id="google-scopes">` + scopeMail + "," + scopeCalendar, `data-copy="google-scopes"`,
		`<pre id="google-callback">` + tw.srv.URL + "/oauth/callback</pre>", `data-copy="google-callback"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("guide lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, `hx-post="/settings/google/connect"`) {
		t.Fatal("connect before the Google app")
	}
	if _, body, _ = tw.post("/settings/google/client", url.Values{"client_id": {testClientID}, "client_secret": {testClientSecret}}, csrf); !strings.Contains(body, "Se connecter avec Google") ||
		strings.Contains(body, testClientSecret) {
		t.Fatalf("client saved: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/google?add=calendar", nil, nil); strings.Contains(body, `value="mail" checked`) ||
		!strings.Contains(body, `value="calendar" checked`) {
		t.Fatalf("?add=calendar: %s", body)
	}
	if _, body, _ = tw.post("/settings/google/connect", url.Values{}, csrf); !strings.Contains(body, "Coche au moins un service") {
		t.Fatalf("no service: %s", body)
	}

	// Mail and calendar, in one sign-in.
	g.scope = scopeMail + " " + scopeCalendar
	q, next := googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail", "calendar"}})
	if q.Get("scope") != "openid email "+scopeMail+" "+scopeCalendar || q.Get("include_granted_scopes") != "true" || q.Get("login_hint") != "" {
		t.Fatalf("auth query %v", q)
	}
	if next != "/settings/google?connected=1" {
		t.Fatalf("next %q", next)
	}
	ref := googleSecret("sam@example.org")
	mail, cal := runOf(t, c.core, "mail"), runOf(t, c.core, "agenda")
	if mail.Env[mailGoogleEnv].File != vaultPrefix+ref || cal.Env[calGoogleEnv].File != vaultPrefix+ref ||
		!strings.Contains(mail.Env["MAIL_CONFIG"].Value, `"oauth_token_env":"MAIL_GOOGLE_TOKEN"`) ||
		!strings.Contains(cal.Env["CALENDAR_CONFIG"].Value, `"provider":"google"`) || !strings.Contains(cal.Env["CALENDAR_CONFIG"].Value, "Europe/Paris") {
		t.Fatalf("specs: %+v %+v", mail, cal)
	}
	for _, name := range []string{"mail", "agenda"} {
		if raw, _ := json.Marshal(c.conf().Servers[name]); strings.Contains(string(raw), "the-refresh") {
			t.Fatalf("a token in the spec of %s", name)
		}
	}
	env, err := lendService(mail)
	if err != nil || !strings.Contains(env[mailGoogleEnv], `"refresh_token":"the-refresh"`) {
		t.Fatalf("lent: %v %v", env, err)
	}
	if _, ok := c.conf().Servers[filesServer]; ok {
		t.Fatal("Drive connected without being asked")
	}
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	if !strings.Contains(body, "Gmail · sam@example.org") || !strings.Contains(body, "Google Agenda · sam@example.org") ||
		strings.Contains(body, `/settings/tools/mail/edit"`) || !strings.Contains(body, `href="/settings/google"`) {
		t.Fatalf("tools list: %s", body)
	}

	// The service it does not use follows the others, with its own button,
	// before the account's actions.
	_, body, _ = tw.do("GET", "/settings/google", nil, nil)
	drive, actions := strings.Index(body, `<input type="hidden" name="service" value="drive">`), strings.Index(body, `class="gaccount-actions"`)
	if drive < 0 || actions < drive || !strings.Contains(body, "Pas encore ajouté") || strings.Contains(body, `class="inline gadd"`) {
		t.Fatalf("drive to add: %s", body)
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
	if env, _ := lendService(runOf(t, c.core, "mail")); !strings.Contains(env[mailGoogleEnv], "the-refresh-2") {
		t.Fatal("the mail server does not read the new token")
	}
	if st := c.driveState(); len(st.Accounts) != 1 || st.Accounts[0].Name != "sam@example.org" || !st.CanWrite {
		t.Fatalf("drive: %+v", st)
	}
	_, body, _ = tw.do("GET", "/settings/google", nil, nil)
	if !strings.Contains(body, "sam@example.org") || !strings.Contains(body, "Choisir des dossiers") || strings.Contains(body, "the-refresh") ||
		strings.Contains(body, testClientSecret) {
		t.Fatalf("google page: %s", body)
	}

	// Its services just added are starting, not in error (their first
	// dial fails until they run): the accounts ask for themselves again.
	for _, name := range []string{"mail", "agenda", filesServer} {
		u := c.pool.get(name)
		u.mu.Lock()
		u.status, u.err = statusError, "service "+name+" has no node: not placed yet"
		u.mu.Unlock()
	}
	_, body, _ = tw.do("GET", "/settings/google", nil, nil)
	if !strings.Contains(body, "En attente d&#39;un appareil") || strings.Contains(body, ">Erreur<") {
		t.Fatalf("waiting for a machine: %s", body)
	}
	for _, name := range []string{"mail", "agenda", filesServer} {
		if _, err := c.store.assignService(name, "nasa"); err != nil {
			t.Fatal(err)
		}
	}
	_, body, _ = tw.do("GET", "/settings/google", nil, nil)
	if !strings.Contains(body, "Démarrage…") || strings.Contains(body, ">Erreur<") ||
		!strings.Contains(body, `id="g-accounts" aria-labelledby="g-accounts-t" hx-get="/settings/google"`) {
		t.Fatalf("starting: %s", body)
	}
	for _, name := range []string{"mail", "agenda", filesServer} {
		if err := c.store.updateService(name, map[string]any{"state": stateFailed, "error": "pull access denied for x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, body, _ = tw.do("GET", "/settings/google", nil, nil); !strings.Contains(body, "nasa") || strings.Contains(body, "Démarrage…") {
		t.Fatalf("failed: %s", body)
	}

	// A second account, for mail: a tool of its own, under another name.
	g.email, g.scope, g.refresh = "bob@example.org", scopeMail, "bob-refresh"
	googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail"}})
	if spec := runOf(t, c.core, "mail-bob"); spec.Env[mailGoogleEnv].File != vaultPrefix+googleSecret("bob@example.org") {
		t.Fatalf("second account: %+v", spec)
	}
	if !strings.Contains(c.conf().Servers["mail-bob"].Use, "bob@example.org") {
		t.Fatalf("use: %q", c.conf().Servers["mail-bob"].Use)
	}

	// A service Google did not grant is said.
	g.scope = scopeMail
	if _, next := googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail", "calendar"}, "account": {"bob@example.org"}}); next != "/settings/google?connected=1&missing=calendar" {
		t.Fatalf("missing: %q", next)
	}
	if _, body, _ := tw.do("GET", "/settings/google?connected=1&missing=calendar", nil, nil); !strings.Contains(body, "Google n&#39;a pas donné l&#39;accès à Google Agenda") {
		t.Fatalf("missing: %s", body)
	}

	// Removed: its tool, its token, and its authorization at Google.
	if _, body, _ := tw.post("/settings/google/remove", url.Values{"account": {"bob@example.org"}}, csrf); strings.Contains(body, "bob@example.org") {
		t.Fatalf("removed: %s", body)
	}
	if _, ok := c.conf().Servers["mail-bob"]; ok {
		t.Fatal("its tool stays")
	}
	if _, err := currentVault().get(googleSecret("bob@example.org")); err == nil {
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

	c.checkGoogle(context.Background(), "sam@example.org")
	if a, _ := c.googleAccount("sam@example.org"); a.Reconnect != "" || g.refreshes != 1 {
		t.Fatalf("a working account: %+v, %d refreshes", a, g.refreshes)
	}
	g.revoked = true
	c.checkGoogle(context.Background(), "sam@example.org")
	if a, _ := c.googleAccount("sam@example.org"); a.Reconnect == "" {
		t.Fatal("not marked")
	}
	c.checkGoogle(context.Background(), "sam@example.org") // told once
	told := 0
	msgs, _ := c.store.messagesAfter(0, 100)
	for _, m := range msgs {
		if m.Role == roleEvent && strings.Contains(m.Text, "Google a retiré l'autorisation du compte sam@example.org") {
			told++
			if !strings.Contains(m.Text, "mail, fichiers") || !strings.Contains(m.Text, "révoquée") || !strings.Contains(m.Text, "Reconnecter") {
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
	if _, body, _ := tw.do("GET", "/settings/google", nil, nil); !strings.Contains(body, "À reconnecter") {
		t.Fatalf("google page: %s", body)
	}

	// One click: the services it had, Drive with its rights.
	g.revoked = false
	q, next := googleSignIn(t, tw, g, csrf, url.Values{"account": {"sam@example.org"}, "reconnect": {"1"}, "back": {"/settings/tools"}})
	if q.Get("scope") != "openid email "+scopeMail+" "+scopeDrive || q.Get("login_hint") != "sam@example.org" || next != "/settings/tools?connected=1" {
		t.Fatalf("reconnect: %v %q", q, next)
	}
	if a, _ := c.googleAccount("sam@example.org"); a.Reconnect != "" {
		t.Fatal("still to reconnect")
	}
	waitStatus(t, c.pool.get("mail"), statusReady)
}

// A Drive connected before Google accounts is taken over, with its folders,
// by the first account that connects Drive.
func TestGoogleTakesOverLegacyDrive(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	putLegacyDrive(t, c.core, "fr", `{"client_id":"x","refresh_token":"old"}`)
	if err := c.addDriveFolder("", "compta", "Compta", "drop"); err != nil {
		t.Fatal(err)
	}
	googleSignIn(t, tw, g, csrf, url.Values{"service": {"drive"}, "write": {"1"}})
	fc, _ := filesConfigOf(c.conf().Servers[filesServer])
	if len(fc.Accounts) != 1 || fc.Accounts[0].Name != "sam@example.org" || fc.Folders[0].Account != "sam@example.org" || fc.Folders[0].Access != "drop" {
		t.Fatalf("taken over: %+v", fc)
	}
	if spec := runOf(t, c.core, filesServer); spec.Env[driveTokenEnv].File != "" {
		t.Fatal("the old token stays")
	}
	if _, err := currentVault().get("service.fichiers.files_google_token"); err == nil {
		t.Fatal("the old token stays in the vault")
	}
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
