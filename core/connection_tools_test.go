package main

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// A tool's needs are data: a provider no code knows serves the mail once
// it has a row; signing in creates the mailbox on the connection's token,
// and removing the account takes it away and revokes the token.
func TestToolOnAnyProvider(t *testing.T) {
	f := newFakeProvider(t)
	signInProviders[f.Name] = f.signInProvider
	old := toolCards
	cards := slices.Clone(toolCards)
	cards[0].SignIns = append(slices.Clone(cards[0].SignIns), toolSignIn{Provider: f.Name, Read: []string{"mail.read"},
		AccountProvider: "example", AccountAddress: true})
	toolCards = cards
	t.Cleanup(func() { delete(signInProviders, f.Name); toolCards = old })

	c := newDynamicCore(t, nil).core
	if got := toolsAt(f.Name); len(got) != 1 || got[0] != toolMail {
		t.Fatalf("tools at %s: %v", f.Name, got)
	}
	if _, err := c.startAccountSignIn(connectAsk{Provider: f.Name, Tools: []string{toolCalendar}, Origin: "http://eriphe.lan"}); err == nil {
		t.Fatal("a tool the provider does not serve")
	}
	if _, err := c.startAccountSignIn(connectAsk{Provider: f.Name, Tools: []string{toolMail}, Origin: "http://eriphe.lan"}); err == nil {
		t.Fatal("no app yet")
	}
	storeAppClient(f.signInProvider, oauthApp{ID: "app-id", Secret: "app-secret"})
	authURL, err := c.startAccountSignIn(connectAsk{Provider: f.Name, Tools: []string{toolMail}, Origin: "http://eriphe.lan", Binding: "b", Lang: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if u.Query().Get("scope") != "openid email mail.read" {
		t.Fatalf("scope %q", u.Query().Get("scope"))
	}
	f.challenge = u.Query().Get("code_challenge")
	res, err := c.finishAccountSignIn(context.Background(), "http://eriphe.lan", u.Query().Get("state"), "the-code", "", "b")
	if err != nil || res.Provider != f.Name || res.Identity != "sam@example.org" || len(res.Added) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	cn, ok := c.connectionOf(f.Name, "sam@example.org")
	mail, _ := c.serviceServer(mailService)
	if !ok || len(mail.Config.Accounts) != 1 {
		t.Fatalf("connection %v, mail %+v", ok, mail.Config.Accounts)
	}
	a := mail.Config.Accounts[0]
	if a.Provider != "example" || a.Address != "sam@example.org" || mail.Spec.Env[a.secretEnv()].File != vaultPrefix+cn.Secret {
		t.Fatalf("account %+v, env %+v", a, mail.Spec.Env)
	}
	if got, ok := c.connectionBySecret(mail, a); !ok || got.ID != cn.ID {
		t.Fatal("connectionBySecret")
	}
	if kinds := c.connectionKinds(cn); len(kinds) != 1 || kinds[0].Kind != toolMail {
		t.Fatalf("kinds %+v", kinds)
	}

	if err := c.removeConnection(context.Background(), f.Name, "sam@example.org"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.connectionOf(f.Name, "sam@example.org"); ok || len(f.revoked) != 1 {
		t.Fatalf("still there, or not revoked: %v", f.revoked)
	}
	if _, ok := c.serviceServer(mailService); ok {
		t.Fatal("the mail of its only account stays")
	}
}

// What a tool is granted: the drive reads with its read scope, writes with
// its write scope; the mail needs its one scope.
func TestToolGranted(t *testing.T) {
	drive, _ := signInOf(toolDrive, "google")
	for _, x := range []struct {
		scopes    string
		ok, write bool
	}{
		{scopeDriveReadonly, true, false},
		{scopeDrive, true, true},
		{scopeMail, false, false},
	} {
		if ok, w := drive.granted(strings.Fields(x.scopes)); ok != x.ok || w != x.write {
			t.Errorf("drive with %s: %v %v", x.scopes, ok, w)
		}
	}
	mail, _ := signInOf(toolMail, "google")
	if ok, w := mail.granted([]string{scopeMail}); !ok || !w {
		t.Error("mail")
	}
	if got := drive.scopes(true); got[0] != scopeDrive {
		t.Error("drive write scopes")
	}
	if got := drive.scopes(false); got[0] != scopeDriveReadonly {
		t.Error("drive read scopes")
	}
}
