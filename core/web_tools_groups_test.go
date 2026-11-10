package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestGroupTools(t *testing.T) {
	p := func(id string) toolPreset { x, _ := presetByID(id); return x }
	row := func(name, preset, detail string) toolView {
		return toolView{serverView: serverView{serverInfo: serverInfo{Name: name}}, Preset: p(preset), Detail: detail}
	}
	svc := func(name, service, owner, provider, google string) toolView {
		t := row(name, service, "")
		t.Part, t.Label, t.Owner, t.Provider, t.Connection = service, service, owner, provider, google
		if google != "" {
			t.Preset, t.ConnProvider = p("google"), providerGoogle.Name
		}
		return t
	}
	tools := []toolView{
		svc("agenda", "calendar", "sam@example.org", "google", "sam@example.org"),
		svc("agenda", "calendar", "sam@example.org", "icloud", ""), // an Apple calendar of the same address
		svc(filesServer, "drive", "sam@example.org", "google", "sam@example.org"),
		svc(filesServer, "drive", "bob@example.org", "google", "bob@example.org"),
		row("gh-1", "github", "GitHub · acme"),
		svc("mail", "mail", "SAM@example.org", "google", "sam@example.org"),
		svc("mail", "mail", "sam@example.org", "icloud", ""),
		svc("mail", "mail", "pro@example.net", "ovh", ""),
		row("notion", "notion", "Notion"),
		row("gh-2", "github", "GitHub · sam"),
		row("x", "remote", ""),
	}
	tools[3].Reconnect = true
	groups := groupTools(tools)
	var got []string
	for _, g := range groups {
		switch {
		case g.Owner != "":
			got = append(got, g.Provider+":"+g.Owner+"["+strings.Join(g.Services(), "+")+"]")
		case g.Single != nil:
			got = append(got, g.Single.Name)
		default:
			var accounts []string
			for _, a := range g.Accounts {
				accounts = append(accounts, a.Label)
			}
			got = append(got, g.Preset.ID+"["+strings.Join(accounts, ",")+"]")
		}
	}
	want := "google:sam@example.org[mail+calendar+drive] icloud:sam@example.org[mail+calendar] google:bob@example.org[drive] github[acme,sam] ovh:pro@example.net[mail] notion x"
	if strings.Join(got, " ") != want {
		t.Fatalf("groups:\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	sam, apple, bob, ovh := groups[0], groups[1], groups[2], groups[4]
	if sam.Connection != "sam@example.org" || sam.Reconnect || len(sam.Others) != 0 {
		t.Errorf("sam: %+v", sam)
	}
	if bob.Connection != "bob@example.org" || !bob.Reconnect || strings.Join(bob.Others, ",") != "mail,calendar" {
		t.Errorf("bob: reconnect %v, others %v", bob.Reconnect, bob.Others)
	}
	if apple.Connection != "" || apple.Others != nil || ovh.Connection != "" {
		t.Error("an account not signed in has a connection's actions")
	}
	if apple.Key() == sam.Key() {
		t.Error("two cards, one id")
	}
}

// Two mailboxes are one card, each with its row and its actions, and a way
// to add another: two accounts of the one mail server.
func TestToolsListGroupsAccounts(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	for _, addr := range []string{"sam@gmail.com", "sam@fastmail.com"} {
		form := url.Values{"preset": {"mail"}, "access": {"reads"}, "address": {addr}, "password": {"app-pw"}}
		if code, body, _ := tw.post("/settings/tools", form, csrf); code != 200 {
			t.Fatalf("add %s: %d %s", addr, code, body)
		}
	}
	if len(c.serviceServers(mailService)) != 1 {
		t.Fatal("a server per mailbox")
	}
	gmail, fastmail := rowKey("mail", "sam@gmail.com"), rowKey("mail", "sam@fastmail.com")
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	// A card for each account: a Gmail on a password is the Google
	// account's mailbox, the Fastmail one Fastmail's.
	for _, c := range []struct{ card, row, head, product, edit string }{
		{rowKey("acct", "google:sam@gmail.com"), gmail, "sam@gmail.com", "Google · Mail", "sam%40gmail.com"},
		{rowKey("acct", "fastmail:sam@fastmail.com"), fastmail, "sam@fastmail.com", "Fastmail · Mail", "sam%40fastmail.com"},
	} {
		i := strings.Index(body, `id="`+c.card+`"`)
		if i < 0 {
			t.Fatalf("no card for %s: %s", c.head, mainPart(t, body))
		}
		card := body[i:]
		card = card[:strings.Index(card, "</ul>")]
		for _, want := range []string{`<h3 id="` + c.card + `-t">` + c.head + `</h3>`, c.product, `id="tool-` + c.row + `"`, "<h3>Mail</h3>",
			`/settings/tools/mail/access?account=` + c.edit, `pmark-`} {
			if !strings.Contains(card, want) {
				t.Errorf("the card of %s lacks %q", c.head, want)
			}
		}
		if strings.Contains(card, "Ajouter à ce compte") || strings.Contains(card, `href="/settings/google"`) {
			t.Errorf("the card of %s has a Google account's actions", c.head)
		}
	}
	if strings.Count(body, `id="tool-`+gmail+`"`) != 1 {
		t.Fatal("a row twice")
	}
	// One account removed: the other stays, with its password.
	if code, _, h := tw.post("/settings/tools/mail/delete", url.Values{"account": {"sam@gmail.com"}}, csrf); code != 200 ||
		h.Get("HX-Redirect") != "/settings/tools?removedaccount=sam%40gmail.com" {
		t.Fatalf("remove: %d %v", code, h)
	}
	if st, _ := c.serviceServer(mailService); len(st.Config.Accounts) != 1 || st.Config.Accounts[0].Name != "sam@fastmail.com" {
		t.Fatalf("after removal: %+v", st.Config.Accounts)
	}
	if _, pw := serviceAccount(t, c.core, mailService, "sam@fastmail.com"); pw != "app-pw" {
		t.Fatal("the other account lost its password")
	}
	if _, body, _ := tw.do("GET", "/settings/tools?removedaccount=sam%40gmail.com", nil, nil); !strings.Contains(body, "Compte « sam@gmail.com » retiré.") {
		t.Error("no flash")
	}
}
