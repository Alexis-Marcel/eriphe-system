package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// oldServer puts a server of one account, as they were before the
// accounts of a service were one server.
func oldServer(t *testing.T, c *core, name, image, configEnv, config, level, use string, env map[string]EnvValue, creds credentials) {
	t.Helper()
	if env == nil {
		env = map[string]EnvValue{}
	}
	env[configEnv] = EnvValue{Value: config}
	raw := marshalRun(RunSpec{Image: image, Transport: transportStdio, Env: env})
	if err := c.putServer(putCreate, name, Server{Use: use, Run: raw}.withLevel(level), creds); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// The servers of one account each are made one at start: every account,
// its secret, its level; what was allowed for good, the watches and the
// assistants follow. It runs once.
func TestMergeAccountServers(t *testing.T) {
	cc := newDynamicCore(t, nil)
	c := cc.core
	bobRef := googleSecret("bob@example.org")
	if err := currentVault().set(bobRef, `{"refresh_token":"bob"}`); err != nil {
		t.Fatal(err)
	}
	oldServer(t, c, "mail", mailImage, "MAIL_CONFIG",
		`{"accounts":[{"name":"mail","address":"sam@example.org","provider":"gmail","auth":{"password_env":"MAIL_PASSWORD"}}]}`,
		accessFree, "mes mails : lire, chercher", nil, credentials{"env:MAIL_PASSWORD": "sam-pw"})
	oldServer(t, c, "mail-bob", mailImage, "MAIL_CONFIG",
		`{"accounts":[{"name":"bob@example.org","address":"bob@example.org","provider":"gmail","auth":{"oauth_token_env":"MAIL_GOOGLE_TOKEN"}}]}`,
		accessReadOnly, "mes mails : lire, chercher (bob@example.org)", map[string]EnvValue{mailGoogleEnv: {File: vaultPrefix + bobRef}}, nil)
	oldServer(t, c, "agenda-icloud", calendarImage, "CALENDAR_CONFIG",
		`{"timezone":"Europe/Paris","accounts":[{"name":"icloud","provider":"icloud","username":"sam@example.net","auth":{"password_env":"CALENDAR_PASSWORD"}}]}`,
		accessReads, "mon agenda", nil, credentials{"env:CALENDAR_PASSWORD": "cal-pw"})
	c.store.allowAction("mail-bob", "mcp__mail-bob__mark", "")
	c.store.allowAction("mail", "mcp__mail__create_draft", "")
	spec, _ := json.Marshal(watchSpec{Server: "mail-bob", Tool: "search_messages", Args: map[string]any{"unread": true}, Every: 30})
	c.store.db.Exec(`INSERT INTO watches (name, source, spec, prompt, state, by, created, updated) VALUES ('bob', 'outil', ?, 'x', 'active', 'user', 0, 0)`, string(spec))
	if err := c.putAssistant("lecteur", Assistant{Role: roleTest, Disabled: true, Tools: []string{"mail-bob.search_messages", "mail", "agenda-icloud"}}); err != nil {
		t.Fatal(err)
	}

	c.mergeAccountServers()

	if _, ok := c.conf().Servers["mail-bob"]; ok {
		t.Fatal("mail-bob stays")
	}
	mail, ok := c.serviceServer(mailService)
	if !ok || mail.Name != "mail" || len(mail.Config.Accounts) != 2 {
		t.Fatalf("mail: %+v", mail)
	}
	if mail.Server.Use != "mes mails : lire, chercher" {
		t.Errorf("use: %q", mail.Server.Use)
	}
	sam, pw := serviceAccount(t, c, mailService, "sam@example.org")
	if pw != "sam-pw" || sam.Auth.PasswordEnv != accountEnv(mailSecretEnv, "sam@example.org") || sam.ReadOnly {
		t.Errorf("sam: %+v %q", sam, pw)
	}
	bob, _ := serviceAccount(t, c, mailService, "bob@example.org")
	if !bob.google() || mail.Spec.Env[bob.secretEnv()].File != vaultPrefix+bobRef || !bob.ReadOnly {
		t.Errorf("bob: %+v %+v", bob, mail.Spec.Env)
	}
	if lv := mail.Server.AccountAccess; lv["sam@example.org"] != accessFree || lv["bob@example.org"] != accessReadOnly {
		t.Errorf("levels: %v", lv)
	}
	if tools := c.googleTools("bob@example.org"); len(tools) != 1 || tools[0].Name != "mail" {
		t.Errorf("bob's tools: %+v", tools)
	}
	// The calendar alone is made one too: named after its username.
	cal, _ := c.serviceServer(calendarService)
	if a, pw := serviceAccount(t, c, calendarService, "sam@example.net"); cal.Name != "agenda-icloud" || pw != "cal-pw" || a.Provider != "icloud" ||
		cal.Config.Timezone != "Europe/Paris" || cal.Server.AccountAccess["sam@example.net"] != accessReads {
		t.Errorf("calendar: %s %+v %q", cal.Name, a, pw)
	}
	// What was allowed, for its account.
	got := map[string]string{}
	for _, a := range c.store.allowedActions("") {
		got[a.Tool] = a.Account
	}
	if len(got) != 2 || got["mcp__mail__mark"] != "bob@example.org" || got["mcp__mail__create_draft"] != "sam@example.org" {
		t.Errorf("allowed: %v", got)
	}
	// The watch calls the merged server, for bob's mailbox.
	var raw string
	c.store.db.QueryRow(`SELECT spec FROM watches WHERE name = 'bob'`).Scan(&raw)
	var w watchSpec
	json.Unmarshal([]byte(raw), &w)
	if w.Server != "mail" || w.Args["account"] != "bob@example.org" || w.Args["unread"] != true {
		t.Errorf("watch: %s", raw)
	}
	// The assistant is lent the merged server.
	if tools := c.conf().Assistants["lecteur"].Tools; strings.Join(tools, ",") != "mail.search_messages,mail,agenda-icloud" {
		t.Errorf("assistant: %v", tools)
	}
	// Once one, nothing changes.
	before := c.conf().Servers["mail"]
	c.mergeAccountServers()
	if after := c.conf().Servers["mail"]; string(after.Run) != string(before.Run) {
		t.Errorf("merged again: %s", after.Run)
	}
}

// The levels of the accounts reach the server: an account read only is
// read only in the server's configuration too, from its form or its
// access page; the other accounts are not touched.
func TestAccountLevelReachesTheServer(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	for _, addr := range []string{"pro@example.org", "perso@example.org"} {
		form := url.Values{"preset": {"mail"}, "access": {"reads"}, "mail_provider": {"ovh"}, "address": {addr}, "password": {addr + "-pw"}}
		if code, body, _ := tw.post("/settings/tools", form, csrf); code != 200 {
			t.Fatalf("add %s: %d %s", addr, code, body)
		}
	}
	form := url.Values{"preset": {"mail"}, "account": {"pro@example.org"}, "access": {"read-only"}, "mail_provider": {"ovh"}, "address": {"pro@example.org"}}
	if code, body, h := tw.post("/settings/tools/mail", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("pro read only: %d %s", code, body)
	}
	pro, pw := serviceAccount(t, c.core, mailService, "pro@example.org")
	perso, _ := serviceAccount(t, c.core, mailService, "perso@example.org")
	if !pro.ReadOnly || perso.ReadOnly || pw != "pro@example.org-pw" {
		t.Fatalf("pro %+v perso %+v %q", pro, perso, pw)
	}
	if lv := c.conf().Servers["mail"].AccountAccess; lv["pro@example.org"] != accessReadOnly || lv["perso@example.org"] != accessReads {
		t.Fatalf("levels: %v", lv)
	}
	// The list shows each account's level.
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	if !strings.Contains(body, `level-read-only" href="/settings/tools/mail/edit?account=pro%40example.org#form-access"`) ||
		!strings.Contains(body, `level-reads" href="/settings/tools/mail/edit?account=perso%40example.org#form-access"`) {
		t.Fatalf("list: %s", body)
	}
	// From the access page: perso free.
	if err := c.setAccountLevel("mail", "PERSO@example.org", accessFree); err != nil {
		t.Fatal(err)
	}
	if lv := c.conf().Servers["mail"].AccountAccess; lv["perso@example.org"] != accessFree || lv["pro@example.org"] != accessReadOnly || len(lv) != 2 {
		t.Fatalf("levels after: %v", lv)
	}
	if err := c.setAccountLevel("mail", "nobody@example.org", accessFree); err == nil {
		t.Fatal("an account it does not have")
	}
	// Removing an account forgets what was allowed for it.
	c.store.allowAction("mail", "mcp__mail__send_message", "perso@example.org")
	if err := c.dropAccount(mailService, "perso@example.org"); err != nil {
		t.Fatal(err)
	}
	if a := c.store.allowedActions("mail"); len(a) != 0 {
		t.Fatalf("allowed after removal: %+v", a)
	}
	if lv := c.conf().Servers["mail"].AccountAccess; len(lv) != 1 {
		t.Fatalf("levels after removal: %v", lv)
	}
}

// A Google account refused holds the server only when it holds nothing
// else: the other mailboxes are still read.
func TestGoogleHoldWithOtherAccounts(t *testing.T) {
	cc := newDynamicCore(t, nil)
	c := cc.core
	c.updateGoogleAccounts(func([]googleAccount) []googleAccount {
		return []googleAccount{{Email: "bob@example.org", Scopes: []string{scopeMail}, Reconnect: googleRevoked}}
	})
	currentVault().set(googleSecret("bob@example.org"), `{"refresh_token":"bob"}`)
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: googleAccountFor(mailService, "bob@example.org"),
		Secret: &EnvValue{File: vaultPrefix + googleSecret("bob@example.org")}}); err != nil {
		t.Fatal(err)
	}
	if why, _ := c.googleHold("mail"); why == "" {
		t.Fatal("bob alone, refused: not held")
	}
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: svcAccount{Name: "sam@example.org", Address: "sam@example.org", Provider: "ovh"},
		Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if why, _ := c.googleHold("mail"); why != "" {
		t.Fatalf("held with another mailbox: %s", why)
	}
}
