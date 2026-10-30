package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestGallery(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, `href="/settings/tools/add"`) {
		t.Fatal("Add a tool does not open the gallery")
	}
	_, body, _ := tw.do("GET", "/settings/tools/add", nil, nil)
	at := -1
	for _, title := range []string{"Linear", "Notion", "GitHub", "Home Assistant", "Mails", "Agenda", "Autre service en ligne", "Autre programme local"} {
		i := strings.Index(body[at+1:], "<b>"+title+"</b>")
		if i < 0 {
			t.Fatalf("tile %s missing or out of order", title)
		}
		at += 1 + i
	}
	// Every tile opens a page first: none signs in at once.
	for _, p := range toolPresets {
		if !strings.Contains(body, `href="`+p.Page()+`"`) {
			t.Errorf("gallery: no page for %s", p.ID)
		}
	}
	if strings.Contains(body, "tool-add") {
		t.Error("a tile still adds and connects in one click")
	}
	if code, _, _ := tw.post("/settings/tool-add/linear", nil, csrf); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("the one-click route still answers: %d", code)
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
	if srv.URL != f.srv.URL+"/mcp" || srv.authKind() != "oauth" || srv.Use != "tickets du travail" || srv.TrustAnnotations {
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
	// The edit: the name is fixed, the access can change without signing in again.
	_, body, _ = tw.do("GET", "/settings/tools/linear-pro/edit", nil, nil)
	if !strings.Contains(body, `name="name" value="linear-pro" disabled`) || !strings.Contains(body, "Enregistrer sans se reconnecter") {
		t.Fatalf("edit: %s", mainPart(t, body))
	}
}

// "Lecture libre" is trust_annotations: the read-only tools of the server
// are the brain's to judge; "Tout me demander" leaves every tool to the user.
func TestAccessChoiceMapsToPolicy(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	form := oauthToolForm(f) // its tool "search" is read-only
	form.Set("access", "read")
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", form)
	code, state := f.authorize(authURL)
	callback(t, tw, "code="+code+"&state="+url.QueryEscape(state))
	waitStatus(t, c.pool.get("linear"), statusReady)
	if !c.conf().Servers["linear"].TrustAnnotations {
		t.Fatal("reads free did not trust the annotations")
	}
	if d, _ := c.decide("mcp__linear__search", nil); d != decideBrain {
		t.Fatalf("a read with reads free: %q", d)
	}
	if d, _ := c.decide("mcp__linear__other", nil); d != decideAsk {
		t.Fatalf("an unknown tool with reads free: %q", d)
	}
	// Ask me everything: saved without signing in again.
	form.Set("access", "ask")
	form.Del("connect")
	if code, _, h := tw.post("/settings/tools/linear", form, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=linear" {
		t.Fatalf("edit: %d %v", code, h)
	}
	waitStatus(t, c.pool.get("linear"), statusReady)
	if c.conf().Servers["linear"].TrustAnnotations {
		t.Fatal("ask everything still trusts the annotations")
	}
	if d, _ := c.decide("mcp__linear__search", nil); d != decideAsk {
		t.Fatalf("a read with ask everything: %q", d)
	}
	// The built-in denials come first whatever the choice.
	if d, _ := c.decide("Bash", map[string]any{"command": "rm -rf /work"}); d != decideDeny {
		t.Fatalf("built-in denial: %q", d)
	}
	// The list says it.
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "Tout sur accord") {
		t.Fatal("the list does not show the access")
	}
}

func mailConfig(t *testing.T, c *catalogCore, name string) mailAccount {
	t.Helper()
	spec := c.conf().runSpecs[name]
	var cfg struct{ Accounts []mailAccount }
	if err := json.Unmarshal([]byte(spec.Env["MAIL_CONFIG"].Value), &cfg); err != nil || len(cfg.Accounts) != 1 {
		t.Fatalf("%s: MAIL_CONFIG = %q", name, spec.Env["MAIL_CONFIG"].Value)
	}
	return cfg.Accounts[0]
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
		form := url.Values{"preset": {"mail"}, "name": {name}, "use": {"mails"}, "access": {"read"}, "mail_provider": {provider},
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
		a := mailConfig(t, c, tc.name)
		if a.Provider != tc.wantProvider || a.Address != tc.address || a.Auth.PasswordEnv != mailSecretEnv {
			t.Errorf("%s: %+v", tc.provider, a)
		}
		if tc.wantIMAP != "" && (a.IMAP == nil || a.IMAP.Host != tc.wantIMAP || a.IMAP.Port != 993 || a.SMTP.Host != "smtp.free.fr" || a.SMTP.Port != 465) {
			t.Errorf("%s: servers %+v %+v", tc.provider, a.IMAP, a.SMTP)
		}
		// It edits back in its own form.
		_, body, _ := tw.do("GET", "/settings/tools/"+tc.name+"/edit", nil, nil)
		if !strings.Contains(body, `name="mail_provider" value="`+tc.provider+`"`) || !strings.Contains(body, `value="`+tc.address+`"`) {
			t.Errorf("%s: edit form %s", tc.provider, mainPart(t, body))
		}
	}
	// Gmail's app password is shown in groups: the spaces are not kept.
	if pw, _ := readSecret(c.conf().runSpecs["m-gmail"].Env[mailSecretEnv].File); pw != "abcdefghijklmnop" {
		t.Errorf("gmail password = %q", pw)
	}
	if pw, _ := readSecret(c.conf().runSpecs["m-orange"].Env[mailSecretEnv].File); pw != "abcd efgh ijkl mnop" {
		t.Errorf("orange password = %q", pw)
	}
	// Other: its servers, and the advanced ports and login.
	if code, body, _ := post("m-other", "other", "sam@example.org", url.Values{"imap_host": {"mail.example.org"},
		"smtp_host": {"mail.example.org"}, "smtp_port": {"587"}, "mail_user": {"sam"}}); code != 200 || strings.Contains(body, "alert") {
		t.Fatalf("other: %d %s", code, body)
	}
	a := mailConfig(t, c, "m-other")
	if a.IMAP.Host != "mail.example.org" || a.IMAP.Port != 993 || a.SMTP.Port != 587 || a.Username != "sam" || a.Provider != "" {
		t.Fatalf("other: %+v %+v %+v", a, a.IMAP, a.SMTP)
	}
	_, body, _ = tw.do("GET", "/settings/tools/m-other/edit", nil, nil)
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
		{"other", "sam@example.org", url.Values{"imap_host": {"https://x"}, "smtp_host": {"y"}}, "Indique les serveurs IMAP et SMTP"},
		{"other", "sam@example.org", url.Values{"imap_host": {"x"}, "smtp_host": {"y"}, "imap_port": {"99999"}}, "Le port est un nombre"},
		{"nope", "sam@example.org", nil, "Messagerie non reconnue"},
	} {
		if _, body, _ := post("m-bad", tc.provider, tc.address, tc.extra); !strings.Contains(html.UnescapeString(body), tc.want) {
			t.Errorf("%s %s: no %q in %s", tc.provider, tc.address, tc.want, body)
		}
	}
	if _, ok := c.conf().Servers["m-bad"]; ok {
		t.Fatal("a refused mailbox was added")
	}
}

// Another mailbox on an edit: the old password is not reused.
func TestMailSwitchNeedsPassword(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"mail"}, "name": {"mail"}, "use": {"mails"}, "access": {"read"}, "mail_provider": {"gmail"},
		"address": {"sam@gmail.com"}, "password": {"gmail-pw"}}
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatal("add")
	}
	// The same mailbox: the password is kept, the access changes.
	form.Del("password")
	form.Set("access", "ask")
	if code, body, h := tw.post("/settings/tools/mail", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("same mailbox: %d %s", code, body)
	}
	file := c.conf().runSpecs["mail"].Env[mailSecretEnv].File
	if pw, _ := readSecret(file); pw != "gmail-pw" || c.conf().Servers["mail"].TrustAnnotations {
		t.Fatalf("same mailbox: %q %+v", pw, c.conf().Servers["mail"])
	}
	// The picker on an edit, then Free.
	_, body, _ := tw.do("GET", "/settings/tools/mail/edit?provider=choose", nil, nil)
	if !strings.Contains(body, `href="/settings/tools/mail/edit?provider=free"`) || !strings.Contains(body, "provider current") {
		t.Fatalf("edit picker: %s", body)
	}
	_, body, _ = tw.do("GET", "/settings/tools/mail/edit?provider=free", nil, nil)
	if b := html.UnescapeString(body); !strings.Contains(b, `name="mail_provider" value="free"`) || strings.Contains(b, "défini — remplacer") ||
		!strings.Contains(b, "Nouveau compte") {
		t.Fatalf("switched form: %s", mainPart(t, b))
	}
	form.Set("mail_provider", "free")
	form.Set("address", "sam@free.fr")
	if _, body, _ := tw.post("/settings/tools/mail", form, csrf); !strings.Contains(html.UnescapeString(body), "Ce n'est plus le même compte") {
		t.Fatalf("switch without password: %s", body)
	}
	if a := mailConfig(t, c, "mail"); a.Address != "sam@gmail.com" {
		t.Fatal("the refused switch was saved")
	}
	form.Set("password", "free-pw")
	if code, _, h := tw.post("/settings/tools/mail", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatal("switch")
	}
	if a := mailConfig(t, c, "mail"); a.Address != "sam@free.fr" || a.IMAP.Host != "imap.free.fr" {
		t.Fatalf("switched: %+v", a)
	}
	if pw, _ := readSecret(c.conf().runSpecs["mail"].Env[mailSecretEnv].File); pw != "free-pw" {
		t.Fatalf("password after the switch = %q", pw)
	}
	// Removed: its password goes with it.
	tw.post("/settings/tools/mail/delete", nil, csrf)
	if _, err := readSecret(c.conf().runSpecs["mail"].Env[mailSecretEnv].File); err == nil {
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
		form := url.Values{"preset": {"calendar"}, "name": {name}, "use": {"agenda"}, "access": {"read"}, "cal_provider": {provider},
			"cal_user": {"sam@icloud.com"}, "password": {"pw"}, "timezone": {"Europe/Paris"}}
		for k, v := range extra {
			form[k] = v
		}
		_, body, _ := tw.post("/settings/tools", form, csrf)
		return html.UnescapeString(body)
	}
	post("c-icloud", "icloud", nil)
	post("c-next", "nextcloud", url.Values{"cal_url": {"https://cloud.example.org/remote.php/dav"}, "cal_user": {"sam"}, "timezone": {"America/New_York"}})
	for name, want := range map[string]calendarForm{
		"c-icloud": {Provider: "icloud", Username: "sam@icloud.com", Timezone: "Europe/Paris"},
		"c-next":   {Provider: "nextcloud", URL: "https://cloud.example.org/remote.php/dav", Username: "sam", Timezone: "America/New_York"},
	} {
		if got, ok := calendarFormOf(c.conf().runSpecs[name]); !ok || got != want {
			t.Errorf("%s = %+v", name, got)
		}
	}
	if b := post("c-bad", "nextcloud", url.Values{"cal_url": {"http://cloud.example.org"}}); !strings.Contains(b, "https://") {
		t.Errorf("http CalDAV: %s", b)
	}
	if b := post("c-bad", "google", nil); !strings.Contains(b, "pas encore pris en charge") {
		t.Errorf("google: %s", b)
	}
	if b := post("c-bad", "icloud", url.Values{"timezone": {"Mars/Base"}}); !strings.Contains(b, "Fuseau horaire inconnu") {
		t.Errorf("timezone: %s", b)
	}
}

// The advanced options still work: a vendor's address and a timeout.
func TestAdvancedOptionsStillWork(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=github", nil, nil)
	main, adv := mainPart(t, body), advancedPart(t, body)
	if !strings.Contains(main, `name="token"`) || strings.Contains(main, `name="url"`) || strings.Contains(main, `name="auth"`) ||
		!strings.Contains(main, "github.com/settings/personal-access-tokens/new") || !strings.Contains(adv, `name="timeout"`) ||
		!strings.Contains(adv, `value="https://api.githubcopilot.com/mcp/"`) || strings.Contains(main, `name="trust"`) {
		t.Fatalf("github form: %s", main)
	}
	gh := url.Values{"preset": {"github"}, "name": {"github"}, "use": {"GitHub"}, "access": {"read"}, "kind": {"remote"},
		"url": {"https://ghe.example.com/mcp/"}, "auth": {"bearer"}, "token": {"github_pat_x"}, "timeout": {"30"}}
	if code, body, h := tw.post("/settings/tools", gh, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=github" {
		t.Fatalf("github save: %d %s", code, body)
	}
	if s := c.conf().Servers["github"]; s.URL != "https://ghe.example.com/mcp/" || s.Timeout != 30 || !s.TrustAnnotations {
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
	if code, _, h := tw.post("/settings/tools", ha, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=ma-maison" {
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

// fakeIMAP accepts one login over TLS.
func fakeIMAP(t *testing.T, user, pass string) (port int) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	srv.StartTLS() // only for its certificate
	cert := srv.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	srv.Close()
	testRoots = pool
	t.Cleanup(func() { testRoots = nil })
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				fmt.Fprintf(conn, "* OK fake ready\r\n")
				line, _ := bufio.NewReader(conn).ReadString('\n')
				if line == fmt.Sprintf("a1 LOGIN %q %q\r\n", user, pass) {
					fmt.Fprintf(conn, "a1 OK logged in\r\n")
				} else {
					fmt.Fprintf(conn, "a1 NO [AUTHENTICATIONFAILED] Invalid credentials\r\n")
				}
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// "Test the connection" tries a mailbox's password over IMAP, and a
// calendar's over CalDAV.
func TestAccountConnectionTest(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	port := fakeIMAP(t, "sam@example.org", "good-pw")
	form := url.Values{"preset": {"mail"}, "name": {"mail"}, "use": {"mails"}, "mail_provider": {"other"}, "address": {"sam@example.org"},
		"imap_host": {"127.0.0.1"}, "smtp_host": {"127.0.0.1"}, "imap_port": {strconv.Itoa(port)}, "password": {"good-pw"}}
	test := func(path string, form url.Values) string {
		_, body, _ := tw.post(path, form, csrf)
		return html.UnescapeString(body)
	}
	if b := test("/settings/tool-test", form); !strings.Contains(b, "test-ok") || !strings.Contains(b, "sam@example.org accepte") {
		t.Fatalf("good password: %s", b)
	}
	form.Set("password", "bad-pw")
	if b := test("/settings/tool-test", form); !strings.Contains(b, "Mot de passe refusé") || strings.Contains(b, "bad-pw") {
		t.Fatalf("bad password: %s", b)
	}
	// An edit of the same mailbox tries the stored password.
	form.Set("password", "good-pw")
	tw.post("/settings/tools", form, csrf)
	form.Del("password")
	if b := test("/settings/tool-test/mail", form); !strings.Contains(b, "test-ok") {
		t.Fatalf("stored password: %s", b)
	}
	form.Set("mail_provider", "outlook")
	if b := test("/settings/tool-test", form); !strings.Contains(b, "bientôt") {
		t.Fatalf("outlook: %s", b)
	}
	// CalDAV.
	dav := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); r.Method != "PROPFIND" || !ok || u != "sam" || p != "cal-pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
	}))
	defer dav.Close()
	pool := x509.NewCertPool()
	pool.AddCert(dav.Certificate())
	testRoots = pool
	cal := url.Values{"preset": {"calendar"}, "name": {"agenda"}, "use": {"agenda"}, "cal_provider": {"caldav"}, "cal_url": {dav.URL},
		"cal_user": {"sam"}, "password": {"cal-pw"}}
	if b := test("/settings/tool-test", cal); !strings.Contains(b, "test-ok") {
		t.Fatalf("calendar: %s", b)
	}
	cal.Set("password", "nope")
	if b := test("/settings/tool-test", cal); !strings.Contains(b, "refusé par l'agenda") {
		t.Fatalf("calendar refused: %s", b)
	}
	// The test needs the session's CSRF token like any form.
	if code, _, _ := tw.post("/settings/tool-test", cal, "forged"); code != http.StatusForbidden {
		t.Fatalf("forged test: %d", code)
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
