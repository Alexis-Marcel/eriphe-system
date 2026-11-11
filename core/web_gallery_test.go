package main

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// mainPart is a tool form above its advanced options.
func mainPart(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "advanced-opts")
	if i < 0 {
		t.Fatal("no advanced options")
	}
	return body[:i]
}

// advancedPart is a tool form's advanced options.
func advancedPart(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "advanced-opts")
	if i < 0 {
		t.Fatal("no advanced options")
	}
	return body[i:]
}

// toolsList is the tools page above its "Add a tool" dialog.
func toolsList(t *testing.T, body string) (list, dialog string) {
	t.Helper()
	i := strings.Index(body, `<dialog class="modal finder-dlg" id="add-dlg"`)
	if i < 0 {
		t.Fatal("no Add a tool dialog")
	}
	return body[:i], body[i:]
}

// The tools page lists the tools of the workspace only; "Add a tool" opens
// a dialog with a search over every preset, and its own page without
// JavaScript.
func TestToolsPageAndAddDialog(t *testing.T) {
	tw, _, _ := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	list, dlg := toolsList(t, body)
	if !strings.Contains(list, `href="/settings/tools/add" data-dialog="add-dlg"`) {
		t.Error("Add a tool neither opens the dialog nor falls back to its page")
	}
	if !strings.Contains(list, `<h3 class="mono">docs</h3>`) {
		t.Error("the tool of the workspace is not listed")
	}
	for _, not := range []string{"ftile", "preset-todoist", "presets"} {
		if strings.Contains(list, not) {
			t.Errorf("the tools list shows the presets (%s)", not)
		}
	}
	for _, not := range []string{"secrets-panel", "Secrets avancés", "Clés et mots de passe enregistrés", `id="secrets"`} {
		if strings.Contains(body, not) {
			t.Errorf("the tools page shows the saved keys (%s)", not)
		}
	}
	if !strings.Contains(dlg, `aria-labelledby="add-dlg-title"`) || !strings.Contains(dlg, `<h3 id="add-dlg-title">Ajouter un outil</h3>`) ||
		!strings.Contains(dlg, `data-focus-on-open="finder-q"`) || !strings.Contains(dlg, `<span class="sr-only">Chercher un outil</span>`) {
		t.Error("the dialog is not labelled, or its search has no label")
	}
	// Every preset, and a chip for every category.
	for _, p := range toolPresets {
		if !strings.Contains(dlg, `class="ftile preset-`+p.ID+`" href="`+p.Page()+`"`) {
			t.Errorf("dialog: no tile for %s", p.ID)
		}
	}
	for _, cat := range toolCategories {
		if !strings.Contains(dlg, `data-chip="`+cat+`" aria-pressed="false"`) || !strings.Contains(dlg, `data-group="`+cat+`"`) {
			t.Errorf("dialog: no category %s", cat)
		}
	}
	if !strings.Contains(dlg, `data-chip="" aria-pressed="true">Tout</button>`) {
		t.Error("no Tout chip")
	}
	// No tool yet: the empty state has the same button.
	tw, _, _ = catalogWeb(t, func(c *Config) { c.Servers = nil })
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	list, _ = toolsList(t, body)
	if !strings.Contains(list, "tools-empty") || strings.Count(list, `data-dialog="add-dlg"`) != 2 {
		t.Error("the empty tools page does not offer Add a tool")
	}
}

func TestGallery(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	// The old address of the gallery still works: the dialog's content on a page.
	code, body, _ := tw.do("GET", "/settings/tools/add", nil, nil)
	if code != 200 || !strings.Contains(body, "data-finder") || strings.Contains(body, `id="add-dlg"`) {
		t.Fatalf("gallery page: %d", code)
	}
	if !strings.Contains(body, `data-finder-bar hidden`) || !strings.Contains(body, `type="search"`) {
		t.Error("no search field, hidden until the script shows it")
	}
	at := -1
	// The popular ones, then the categories (headings) and their tiles, in
	// order, and "Autre service" last.
	var want []string
	for _, id := range popularPresets {
		want = append(want, `class="ftile preset-`+id+`"`)
	}
	want = append(want, "Tâches &amp; notes</h4>", "<b>Linear</b>", "<b>Notion</b>", "<b>Todoist</b>",
		"Mail &amp; agenda</h4>", "<b>Mails</b>", "<b>Agenda</b>", "<b>Fastmail</b>", "Fichiers</h4>", "<b>Google Drive</b>", "<b>Dropbox</b>",
		"Développement</h4>", "<b>GitHub</b>", "Réseaux &amp; newsletters</h4>", "<b>Buffer</b>", "<b>Kit</b>",
		"Automatisation</h4>", "<b>Zapier</b>", "<b>Make</b>", "Maison</h4>", "<b>Home Assistant</b>",
		`Pas trouvé ? <a href="/settings/tools/add/other" data-finder-paste>Colle son adresse</a>`, "Pas dans la liste ?</h4>",
		"<b>Autre programme local</b>", "<b>Autre service en ligne</b>", "<b>Machine SSH</b>", "<b>Clé ou jeton HTTP</b>", "Pas encore possible</summary>")
	for _, w := range want {
		i := strings.Index(body[at+1:], w)
		if i < 0 {
			t.Fatalf("%s missing or out of order", w)
		}
		at += 1 + i
	}
	// Every tile opens a page first: none signs in at once.
	for _, p := range toolPresets {
		if !strings.Contains(body, `href="`+p.Page()+`"`) {
			t.Errorf("gallery: no page for %s", p.ID)
		}
	}
	// An access is found here too, and leaves the dialog for its own page.
	for _, p := range accessTiles {
		if !strings.Contains(html.UnescapeString(body), `class="ftile preset-`+p.ID+`" href="`+p.Link+`" title=`) {
			t.Errorf("gallery: the tile of %s", p.ID)
		}
	}
	if strings.Contains(body, "tool-add") {
		t.Error("a tile still adds and connects in one click")
	}
	if code, _, _ := tw.post("/settings/tool-add/linear", nil, csrf); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("the one-click route still answers: %d", code)
	}
}

// The saved keys and passwords are on "Mon espace", folded, with a word on
// what they are; replaced or deleted there.
func TestAccountSecrets(t *testing.T) {
	tw, _, _ := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/account", nil, nil)
	i := strings.Index(body, `<details class="panel advanced" id="secrets-panel">`)
	if i < 0 {
		t.Fatal("no saved keys on the account page")
	}
	if !strings.Contains(body[i:], "Clés et mots de passe enregistrés") || !strings.Contains(body[i:], `<div id="secrets" class="secrets">`) ||
		!strings.Contains(body[i:], "gardé chiffré") {
		t.Error("the saved keys' section")
	}
	// The sign-in and the address moved to Access → Web workspace.
	if !strings.Contains(body, `name="lang" value="fr"`) || strings.Contains(body, "signin-form") || strings.Contains(body, `name="external_url"`) {
		t.Error("the account page: the language, without the sign-in nor the address")
	}
}

// withPresetURL points a preset at a test server.
func withPresetURL(t *testing.T, id, u string) {
	t.Helper()
	for i := range toolPresets {
		if toolPresets[i].ID == id {
			old := toolPresets[i].URL
			toolPresets[i].URL = u
			t.Cleanup(func() { toolPresets[i].URL = old })
			return
		}
	}
	t.Fatalf("no preset %s", id)
}

// A vendor's OAuth tile (Linear, Notion) opens a short page: the name, the
// use and what Eriphe may do alone; "Connect to Linear" saves and signs in.
func TestVendorPageThenConnect(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	withPresetURL(t, "linear", f.srv.URL+"/mcp")
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=linear", nil, nil)
	main := html.UnescapeString(mainPart(t, body))
	for _, want := range []string{`name="name" value="linear"`, `name="use" value="Linear : tickets, projets, cycles"`,
		`name="access" value="reads" checked`, `name="access" value="read-only"`, `name="access" value="free"`, `name="access" value="ask"`, "Lecture libre, écriture sur accord", "Tout libre", "Se connecter à Linear", "Tu seras envoyé chez Linear"} {
		if !strings.Contains(main, want) && !strings.Contains(body, want) {
			t.Errorf("linear page lacks %q", want)
		}
	}
	// The token field is there only for a switch to a token in the
	// advanced options: hidden while the authorization is OAuth.
	if !strings.Contains(main, `class="if-bearer stack"`) {
		t.Error("the token field is not tied to the token authentication")
	}
	for _, not := range []string{`name="url"`, `name="auth"`} {
		if strings.Contains(main, not) {
			t.Errorf("linear page asks for %s outside the advanced options", not)
		}
	}
	if strings.Contains(body, "test-btn") {
		t.Error("a vendor's sign-in page offers a connection test")
	}
	if !strings.Contains(advancedPart(t, body), `value="`+f.srv.URL+`/mcp"`) {
		t.Error("the vendor's address is not under the advanced options")
	}
	// What the browser posts: the page's fields and the advanced ones as prefilled.
	form := url.Values{"preset": {"linear"}, "name": {"Linear Pro"}, "use": {"tickets du travail"}, "access": {"ask"},
		"kind": {"remote"}, "url": {f.srv.URL + "/mcp"}, "auth": {"oauth"}, "connect": {"1"}}
	if code, _, _ := tw.post("/settings/tools", form, "forged"); code != http.StatusForbidden {
		t.Fatalf("forged CSRF: %d", code)
	}
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", form)
	if _, ok := c.conf().Servers["linear-pro"]; ok {
		t.Fatal("saved before its authorization")
	}
	code, state := f.authorize(authURL)
	if _, body := callback(t, tw, "code="+code+"&state="+url.QueryEscape(state)); !strings.Contains(body, "connected=linear-pro") {
		t.Fatalf("callback: %s", body)
	}
	srv := c.conf().Servers["linear-pro"]
	if srv.URL != f.srv.URL+"/mcp" || srv.authKind() != "oauth" || srv.Use != "tickets du travail" || srv.freeReads() {
		t.Fatalf("server = %+v", srv)
	}
	// Cancelled at the vendor: nothing added, and the list says so.
	form.Set("name", "linear-perso")
	authURL = startConnectForm(t, tw, csrf, "/settings/tools", form)
	_, state = f.authorize(authURL)
	if _, body := callback(t, tw, "error=access_denied&state="+url.QueryEscape(state)); !strings.Contains(body, "notconnected=linear-perso&why=cancelled") {
		t.Fatalf("cancel: %s", body)
	}
	if _, ok := c.conf().Servers["linear-perso"]; ok {
		t.Fatal("a cancelled tool was added")
	}
	if _, body, _ := tw.do("GET", "/settings/tools?notconnected=linear-perso&why=cancelled", nil, nil); !strings.Contains(html.UnescapeString(body), "Connexion à « linear-perso » annulée. L'outil n'a pas été ajouté.") {
		t.Fatal("no word of the cancellation")
	}
	// A name taken: the page comes back with why, its fields kept.
	form.Set("name", "linear-pro")
	_, body, _ = tw.post("/settings/tools", form, csrf)
	if b := html.UnescapeString(body); !strings.Contains(b, "« linear-pro » existe déjà") || !strings.Contains(b, `value="tickets du travail"`) ||
		!strings.Contains(b, `name="access" value="ask" checked`) {
		t.Fatalf("taken: %s", b)
	}
	// The edit: the name is fixed, it saves without signing in again.
	_, body, _ = tw.do("GET", "/settings/tools/linear-pro/edit", nil, nil)
	if !strings.Contains(body, `name="name" value="linear-pro" disabled`) || !strings.Contains(body, "save-btn keep if-noreauth") {
		t.Fatalf("edit: %s", mainPart(t, body))
	}
}

// "Lecture libre" is trust_annotations: the read-only tools of the server
// are the brain's to judge; "Tout me demander" leaves every tool to the user.
func TestAccessChoiceMapsToPolicy(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	form := oauthToolForm(f) // its tool "search" is read-only
	form.Set("access", accessReads)
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", form)
	code, state := f.authorize(authURL)
	callback(t, tw, "code="+code+"&state="+url.QueryEscape(state))
	waitStatus(t, c.pool.get("linear"), statusReady)
	if !c.conf().Servers["linear"].freeReads() {
		t.Fatal("reads free not kept")
	}
	if d, _ := c.decide("mcp__linear__search", nil); d != decideAllow {
		t.Fatalf("a read with reads free: %q", d)
	}
	if d, _ := c.decide("mcp__linear__other", nil); d != decideAsk {
		t.Fatalf("an unknown tool with reads free: %q", d)
	}
	// Ask me everything: saved without signing in again.
	form.Set("access", "ask")
	form.Del("connect")
	if code, _, h := tw.post("/settings/tools/linear", form, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=linear&tool=linear" {
		t.Fatalf("edit: %d %v", code, h)
	}
	waitStatus(t, c.pool.get("linear"), statusReady)
	if c.conf().Servers["linear"].freeReads() {
		t.Fatal("ask everything still reads free")
	}
	if d, _ := c.decide("mcp__linear__search", nil); d != decideAsk {
		t.Fatalf("a read with ask everything: %q", d)
	}
	// The built-in denials come first whatever the choice.
	if d, _ := c.decide("Bash", map[string]any{"command": "git push origin main"}); d != decideDeny {
		t.Fatalf("built-in denial: %q", d)
	}
	// The list says it.
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "Tout sur accord") {
		t.Fatal("the list does not show the access")
	}
}

// serviceAccount is an account of the mail or of the calendar, by its
// name, and its secret as the server reads it.
func serviceAccount(t *testing.T, c *core, svc toolCard, name string) (svcAccount, string) {
	t.Helper()
	st, ok := c.serviceServer(svc)
	if !ok {
		t.Fatalf("no %s server", svc.ID)
	}
	a, ok := st.account(name)
	if !ok {
		t.Fatalf("%s: no account %s in %s", svc.ID, name, st.Spec.Env[toolConfigEnv].Value)
	}
	secret, _ := readSecret(c.conf().runSpecs[st.Name].Env[a.secretEnv()].File)
	return a, secret
}

func mailConfig(t *testing.T, c *catalogCore, address string) svcAccount {
	t.Helper()
	a, _ := serviceAccount(t, c.core, mailService, address)
	return a
}

// Mail: the kind of mailbox first, then its own form; each gives a config
// eriphe-mail takes.
func TestMailProviderForms(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=mail", nil, nil)
	for _, id := range []string{"gmail", "icloud", "orange", "free", "ovh", "fastmail", "other"} {
		if !strings.Contains(body, `href="/settings/tools/new?preset=mail&amp;provider=`+id+`"`) {
			t.Errorf("picker: no %s", id)
		}
	}
	if strings.Contains(body, "provider=outlook") || !strings.Contains(body, "Outlook, Hotmail") || !strings.Contains(body, "Bientôt") {
		t.Error("Outlook is not shown as coming soon")
	}
	if strings.Contains(body, `name="address"`) {
		t.Error("the picker already shows the form")
	}
	pages := map[string][]string{
		"gmail":    {"myaccount.google.com/apppasswords", "validation en deux étapes", "Mot de passe d'application"},
		"icloud":   {"account.apple.com", "Mots de passe pour apps"},
		"orange":   {"espace-client.orange.fr", "logiciels et applications de messagerie tiers"},
		"free":     {"Mot de passe de la boîte mail", "imap.free.fr"},
		"ovh":      {"MX Plan", "Mot de passe de la boîte mail"},
		"fastmail": {"app.fastmail.com/settings/security", "Nouveau mot de passe d'application"},
		"other":    {`name="imap_host"`, `name="smtp_host"`},
	}
	for id, wants := range pages {
		_, body, _ := tw.do("GET", "/settings/tools/new?preset=mail&provider="+id, nil, nil)
		b := html.UnescapeString(body)
		for _, want := range append(wants, `name="mail_provider" value="`+id+`"`, `name="address"`, `name="password"`) {
			if !strings.Contains(b, want) {
				t.Errorf("%s form lacks %q", id, want)
			}
		}
		if id != "other" && strings.Contains(b, `name="imap_host"`) {
			t.Errorf("%s form asks for the servers", id)
		}
	}
	post := func(name, provider, address string, extra url.Values) (int, string, http.Header) {
		form := url.Values{"preset": {"mail"}, "name": {name}, "use": {"mails"}, "access": {accessReads}, "mail_provider": {provider},
			"address": {address}, "password": {"abcd efgh ijkl mnop"}}
		for k, v := range extra {
			form[k] = v
		}
		return tw.post("/settings/tools", form, csrf)
	}
	for _, tc := range []struct{ name, provider, address, wantProvider, wantIMAP string }{
		{"m-gmail", "gmail", "sam@gmail.com", "gmail", ""},
		{"m-icloud", "icloud", "sam@icloud.com", "icloud", ""},
		{"m-orange", "orange", "sam@orange.fr", "orange", ""},
		{"m-free", "free", "sam@free.fr", "", "imap.free.fr"},
		{"m-ovh", "ovh", "sam@example.fr", "ovh", ""},
		{"m-fastmail", "fastmail", "sam@fastmail.com", "fastmail", ""},
	} {
		if code, body, h := post(tc.name, tc.provider, tc.address, nil); code != 200 || h.Get("HX-Redirect") == "" {
			t.Errorf("%s: %d %s", tc.provider, code, body)
			continue
		}
		a := mailConfig(t, c, tc.address)
		if a.Provider != tc.wantProvider || a.get("address") != tc.address || a.Name != tc.address || a.Auth.PasswordEnv != accountEnv(toolPasswordEnv, tc.address) {
			t.Errorf("%s: %+v", tc.provider, a)
		}
		if tc.wantIMAP != "" && (a.get("imap.host") != tc.wantIMAP || a.get("imap.port") != "993" || a.get("smtp.host") != "smtp.free.fr" || a.get("smtp.port") != "465") {
			t.Errorf("%s: servers %+v", tc.provider, a.Fields)
		}
		// It edits back in its own form.
		_, body, _ := tw.do("GET", "/settings/tools/mail/edit?account="+tc.address, nil, nil)
		if !strings.Contains(body, `name="mail_provider" value="`+tc.provider+`"`) || !strings.Contains(body, `value="`+tc.address+`"`) ||
			!strings.Contains(body, `name="account" value="`+tc.address+`"`) {
			t.Errorf("%s: edit form %s", tc.provider, mainPart(t, body))
		}
	}
	// One server for every mailbox, each with its password.
	if st, _ := c.serviceServer(mailService); st.Name != "mail" || len(st.Config.Accounts) != 6 || len(c.serviceServers(mailService)) != 1 {
		t.Fatalf("mail server: %s %+v", st.Name, st.Config.Accounts)
	}
	// Gmail's app password is shown in groups: the spaces are not kept.
	if _, pw := serviceAccount(t, c.core, mailService, "sam@gmail.com"); pw != "abcdefghijklmnop" {
		t.Errorf("gmail password = %q", pw)
	}
	if _, pw := serviceAccount(t, c.core, mailService, "sam@orange.fr"); pw != "abcd efgh ijkl mnop" {
		t.Errorf("orange password = %q", pw)
	}
	// The same mailbox twice is refused.
	if _, body, _ := post("m-again", "gmail", "sam@gmail.com", nil); !strings.Contains(html.UnescapeString(body), "déjà connecté") {
		t.Errorf("twice: %s", body)
	}
	// Other: its servers, and the advanced ports and login.
	if code, body, _ := post("m-other", "other", "sam@example.org", url.Values{"imap_host": {"mail.example.org"},
		"smtp_host": {"mail.example.org"}, "smtp_port": {"587"}, "mail_user": {"sam"}}); code != 200 || strings.Contains(body, "alert") {
		t.Fatalf("other: %d %s", code, body)
	}
	a := mailConfig(t, c, "sam@example.org")
	if a.get("imap.host") != "mail.example.org" || a.get("imap.port") != "993" || a.get("smtp.port") != "587" || a.get("username") != "sam" || a.Provider != "" {
		t.Fatalf("other: %+v", a)
	}
	_, body, _ = tw.do("GET", "/settings/tools/mail/edit?account=sam@example.org", nil, nil)
	if adv := advancedPart(t, body); !strings.Contains(adv, `name="smtp_port" value="587"`) || !strings.Contains(adv, `name="mail_user" value="sam"`) {
		t.Fatalf("other edit: %s", adv)
	}
	// Refusals, in words.
	for _, tc := range []struct {
		provider, address string
		extra             url.Values
		want              string
	}{
		{"outlook", "sam@outlook.com", nil, "pas encore pris en charge"},
		{"gmail", "not an address", nil, "ne semble pas valable"},
		{"other", "sam@example.net", url.Values{"imap_host": {"https://x"}, "smtp_host": {"y"}}, "Indique les serveurs IMAP et SMTP"},
		{"other", "sam@example.net", url.Values{"imap_host": {"x"}, "smtp_host": {"y"}, "imap_port": {"99999"}}, "Le port est un nombre"},
		{"nope", "sam@example.org", nil, "Messagerie non reconnue"},
	} {
		if _, body, _ := post("m-bad", tc.provider, tc.address, tc.extra); !strings.Contains(html.UnescapeString(body), tc.want) {
			t.Errorf("%s %s: no %q in %s", tc.provider, tc.address, tc.want, body)
		}
	}
	if st, _ := c.serviceServer(mailService); len(st.Config.Accounts) != 7 {
		t.Fatalf("a refused mailbox was added: %+v", st.Config.Accounts)
	}
}

// Another mailbox on an edit: the old password is not reused.
func TestMailSwitchNeedsPassword(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"mail"}, "name": {"mail"}, "use": {"mails"}, "access": {accessReads}, "mail_provider": {"gmail"},
		"address": {"sam@gmail.com"}, "password": {"gmail-pw"}}
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatal("add")
	}
	// The same mailbox: the password is kept, the access changes.
	form.Del("password")
	form.Set("access", "ask")
	form.Set("account", "sam@gmail.com")
	if code, body, h := tw.post("/settings/tools/mail", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("same mailbox: %d %s", code, body)
	}
	a, _ := serviceAccount(t, c.core, mailService, "sam@gmail.com")
	file := c.conf().runSpecs["mail"].Env[a.secretEnv()].File
	if pw, _ := readSecret(file); pw != "gmail-pw" || c.conf().Servers["mail"].freeReads() || c.conf().Servers["mail"].AccountAccess["sam@gmail.com"] != accessAsk {
		t.Fatalf("same mailbox: %q %+v", pw, c.conf().Servers["mail"])
	}
	// The picker on an edit, then Free.
	_, body, _ := tw.do("GET", "/settings/tools/mail/edit?account=sam@gmail.com&provider=choose", nil, nil)
	if !strings.Contains(body, `href="/settings/tools/mail/edit?account=sam%40gmail.com&amp;provider=free"`) || !strings.Contains(body, "provider current") {
		t.Fatalf("edit picker: %s", body)
	}
	_, body, _ = tw.do("GET", "/settings/tools/mail/edit?account=sam@gmail.com&provider=free", nil, nil)
	if b := html.UnescapeString(body); !strings.Contains(b, `name="mail_provider" value="free"`) || strings.Contains(b, "défini — remplacer") ||
		!strings.Contains(b, "Nouveau compte") {
		t.Fatalf("switched form: %s", mainPart(t, b))
	}
	form.Set("mail_provider", "free")
	form.Set("address", "sam@free.fr")
	if _, body, _ := tw.post("/settings/tools/mail", form, csrf); !strings.Contains(html.UnescapeString(body), "Ce n'est plus le même compte") {
		t.Fatalf("switch without password: %s", body)
	}
	if a := mailConfig(t, c, "sam@gmail.com"); a.get("address") != "sam@gmail.com" {
		t.Fatal("the refused switch was saved")
	}
	form.Set("password", "free-pw")
	if code, _, h := tw.post("/settings/tools/mail", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatal("switch")
	}
	a, pw := serviceAccount(t, c.core, mailService, "sam@free.fr")
	if a.get("imap.host") != "imap.free.fr" || pw != "free-pw" || c.conf().Servers["mail"].AccountAccess["sam@free.fr"] != accessAsk {
		t.Fatalf("switched: %+v %q %v", a, pw, c.conf().Servers["mail"].AccountAccess)
	}
	if st, _ := c.serviceServer(mailService); len(st.Config.Accounts) != 1 {
		t.Fatalf("the switch kept the old account: %+v", st.Config.Accounts)
	}
	newFile := c.conf().runSpecs["mail"].Env[a.secretEnv()].File
	// Removed: the server goes with its last account, its password too.
	tw.post("/settings/tools/mail/delete", url.Values{"account": {"sam@free.fr"}}, csrf)
	if _, ok := c.conf().Servers["mail"]; ok {
		t.Fatal("the mail server stays without accounts")
	}
	if _, err := readSecret(newFile); err == nil {
		t.Fatal("password kept after removal")
	}
	if _, err := readSecret(file); err == nil {
		t.Fatal("password kept after removal")
	}
}

func TestCalendarProviderForms(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=calendar", nil, nil)
	for _, id := range []string{"icloud", "fastmail", "nextcloud", "caldav"} {
		if !strings.Contains(body, "provider="+id+`"`) {
			t.Errorf("picker: no %s", id)
		}
	}
	if strings.Contains(body, "provider=google") || strings.Contains(body, "provider=microsoft") || !strings.Contains(body, "Google Agenda") {
		t.Error("Google and Microsoft are not shown as coming soon")
	}
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=calendar&provider=nextcloud", nil, nil)
	if main := mainPart(t, body); !strings.Contains(main, `name="cal_url"`) || !strings.Contains(main, "remote.php/dav") ||
		strings.Contains(main, `name="timezone"`) || !strings.Contains(advancedPart(t, body), `name="timezone" value="Europe/Paris"`) {
		t.Fatalf("nextcloud form: %s", main)
	}
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=calendar&provider=icloud", nil, nil)
	if strings.Contains(mainPart(t, body), `name="cal_url"`) {
		t.Fatal("iCloud asks for an address")
	}
	post := func(name, provider string, extra url.Values) string {
		form := url.Values{"preset": {"calendar"}, "name": {name}, "use": {"agenda"}, "access": {accessReads}, "cal_provider": {provider},
			"cal_user": {"sam@icloud.com"}, "password": {"pw"}, "timezone": {"Europe/Paris"}}
		for k, v := range extra {
			form[k] = v
		}
		_, body, _ := tw.post("/settings/tools", form, csrf)
		return html.UnescapeString(body)
	}
	post("c-icloud", "icloud", nil)
	post("c-next", "nextcloud", url.Values{"cal_url": {"https://cloud.example.org/remote.php/dav"}, "cal_user": {"sam"}, "timezone": {"America/New_York"}})
	// One calendar server for both, in the zone given last.
	st, _ := c.serviceServer(calendarService)
	if tz := st.Config.option("timezone"); tz != "America/New_York" {
		t.Errorf("zone %q", tz)
	}
	for name, want := range map[string]accountForm{
		"sam@icloud.com":        {Choice: "icloud", Values: map[string]string{"cal_user": "sam@icloud.com"}},
		"sam@cloud.example.org": {Choice: "nextcloud", Values: map[string]string{"cal_url": "https://cloud.example.org/remote.php/dav", "cal_user": "sam"}},
	} {
		a, pw := serviceAccount(t, c.core, calendarService, name)
		got, ok := calendarShape.formOf(a)
		if !ok || got.Choice != want.Choice || pw != "pw" {
			t.Errorf("%s = %+v %q", name, got, pw)
		}
		for k, v := range want.Values {
			if got.Values[k] != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got.Values[k], v)
			}
		}
	}
	if st.Name != "agenda" || len(st.Config.Accounts) != 2 {
		t.Fatalf("calendar server: %s %+v", st.Name, st.Config.Accounts)
	}
	// A new account's form starts in the calendar's zone.
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=calendar&provider=fastmail", nil, nil)
	if !strings.Contains(advancedPart(t, body), `name="timezone" value="America/New_York"`) {
		t.Errorf("zone of a new account: %s", advancedPart(t, body))
	}
	if b := post("c-bad", "nextcloud", url.Values{"cal_url": {"http://cloud.example.org"}}); !strings.Contains(b, "https://") {
		t.Errorf("http CalDAV: %s", b)
	}
	if b := post("c-bad", "google", nil); !strings.Contains(b, "avec ton compte Google") {
		t.Errorf("google: %s", b)
	}
	if b := post("c-bad", "icloud", url.Values{"timezone": {"Mars/Base"}}); !strings.Contains(b, "Fuseau horaire inconnu") {
		t.Errorf("timezone: %s", b)
	}
}

// The advanced options still work: a vendor's address and a timeout.
func TestAdvancedOptionsStillWork(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=github&provider=token", nil, nil)
	main, adv := mainPart(t, body), advancedPart(t, body)
	if !strings.Contains(main, `name="token"`) || strings.Contains(main, `name="url"`) || strings.Contains(main, `name="auth"`) ||
		!strings.Contains(main, "github.com/settings/personal-access-tokens/new") || !strings.Contains(adv, `name="timeout"`) ||
		!strings.Contains(adv, `value="https://api.githubcopilot.com/mcp/"`) || strings.Contains(main, `name="trust"`) {
		t.Fatalf("github form: %s", main)
	}
	gh := url.Values{"preset": {"github"}, "name": {"github"}, "use": {"GitHub"}, "access": {accessReads}, "kind": {"remote"},
		"url": {"https://ghe.example.com/mcp/"}, "auth": {"bearer"}, "token": {"github_pat_x"}, "timeout": {"30"}}
	if code, body, h := tw.post("/settings/tools", gh, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=github&tool=github&new=1" {
		t.Fatalf("github save: %d %s", code, body)
	}
	if s := c.conf().Servers["github"]; s.URL != "https://ghe.example.com/mcp/" || s.Timeout != 30 || !s.freeReads() {
		t.Fatalf("github = %+v", s)
	}
	// Home Assistant: the address is the user's own, asked with an example.
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=home-assistant", nil, nil)
	if main := mainPart(t, body); !strings.Contains(main, `name="url" value=""`) || !strings.Contains(main, `placeholder="http://192.168.0.10:8123/api/mcp"`) {
		t.Fatalf("home assistant form: %s", main)
	}
	ha := url.Values{"preset": {"home-assistant"}, "name": {"maison"}, "use": {"maison"}, "kind": {"remote"}, "auth": {"bearer"}, "token": {"t"}}
	for _, bad := range []string{"", "192.168.0.10:8123/api/mcp", "http://<ip>/api/mcp"} {
		ha.Set("url", bad)
		if _, body, _ := tw.post("/settings/tools", ha, csrf); !strings.Contains(html.UnescapeString(body), "adresse") {
			t.Errorf("url %q accepted: %s", bad, body)
		}
	}
	// A name typed with capitals and spaces becomes a catalog name.
	ha.Set("url", "http://192.168.0.10:8123/api/mcp")
	ha.Set("name", "Ma Maison")
	if code, _, h := tw.post("/settings/tools", ha, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=ma-maison&tool=ma-maison&new=1" {
		t.Fatalf("name: %d %v", code, h)
	}
}

func TestSlugName(t *testing.T) {
	for in, want := range map[string]string{"Mail Pro": "mail-pro", "  agenda_été ": "agenda-ete", "linear": "linear",
		"Œuvre!!": "oeuvre", "a--b": "a-b", "": "", "!!!": ""} {
		if got := slugName(in); got != want {
			t.Errorf("slugName(%q) = %q, want %q", in, got, want)
		}
	}
}

// After a save, the row that follows a ready tool asks its server to sign
// the account in (test_account): a refused one says so, with the server's
// reason, and the way out is to change it.
func TestRowTestsTheAccount(t *testing.T) {
	tw, c, _ := filesCore(t, nil)
	for _, name := range []string{"sam@example.org", "bob@example.org"} {
		a := svcAccount{Name: name, Provider: "google"}
		if err := c.putAccount(filesService, accountPut{Lang: "fr", Account: a, Secret: &EnvValue{File: "vault:t." + name}}); err != nil {
			t.Fatal(err)
		}
	}
	waitStatus(t, c.pool.get(filesService.Base), statusReady)
	row := func(account string) string {
		_, body, _ := tw.do("GET", "/settings/tools/"+filesService.Base+"/row?account="+url.QueryEscape(account), nil, nil)
		return html.UnescapeString(body)
	}
	if b := row("sam@example.org"); strings.Contains(b, "ne se connecte pas") {
		t.Errorf("sam refused: %s", b)
	}
	if b := row("bob@example.org"); !strings.Contains(b, "ce compte ne se connecte pas") || !strings.Contains(b, "token revoked: reconnect") {
		t.Errorf("bob: %s", b)
	}
}

// A local tool a machine is starting says so, and the list refreshes itself
// until it is up; one no machine has taken still waits.
func TestToolStartState(t *testing.T) {
	local := func(node, service, status string) serverView {
		return serverView{serverInfo: serverInfo{Kind: "local", Status: status}, Node: node, Service: service}
	}
	for _, tc := range []struct {
		name            string
		sv              serverView
		pending, placed bool
	}{
		{"no machine", local("", statePending, statusConnecting), true, false},
		{"starting", local("nas", "starting", statusConnecting), true, true},
		{"failed", local("nas", stateFailed, statusError), true, false},
		{"running", local("nas", stateRunning, statusConnecting), false, false},
		{"ready", local("nas", "starting", statusReady), false, false},
		{"remote", serverView{serverInfo: serverInfo{Kind: "remote", Status: statusError}}, false, false},
	} {
		if p, pl := startState(tc.sv); p != tc.pending || pl != tc.placed {
			t.Errorf("%s: pending %v placed %v", tc.name, p, pl)
		}
	}
}

// Why a local tool does not start, in words.
func TestServiceProblem(t *testing.T) {
	failed := func(msg string) serverView {
		return serverView{serverInfo: serverInfo{Kind: "local", Error: msg}, Node: "pc", Service: stateFailed}
	}
	pull := `service agenda is failed on pc: exit status 125: Unable to find image 'x' locally
docker: Error response from daemon: Head "https://ghcr.io/v2/x/manifests/latest": unauthorized`
	if got := serviceProblem("fr", failed(pull)); got != "L'appareil pc n'a pas pu télécharger le programme de cet outil." {
		t.Errorf("pull: %q", got)
	}
	if got := serviceProblem("fr", failed("exit status 1: panic")); !strings.Contains(got, "s'arrête sans cesse sur l'appareil pc") {
		t.Errorf("stopped: %q", got)
	}
	if got := serviceProblem("fr", serverView{serverInfo: serverInfo{Kind: "local"}, Node: "pc", Service: "starting"}); got != "" {
		t.Errorf("starting: %q", got)
	}
}
