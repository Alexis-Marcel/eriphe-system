package main

import (
	"context"
	"strings"
	"testing"
)

// An install with Google accounts keeps them as connections: their tokens
// keep their vault names (no tool's environment changes), a lapsed one
// stays lapsed, and the tokens learn where to refresh.
func TestMigrateConnections(t *testing.T) {
	c := newDynamicCore(t, nil).core
	samRef, bobRef := legacyGoogleSecret("sam@example.org"), legacyGoogleSecret("bob@example.org")
	currentVault().set(samRef, `{"client_id":"id","refresh_token":"sam"}`)
	currentVault().set(bobRef, `{"client_id":"id","refresh_token":"bob","token_url":"https://token.example.org"}`)
	c.store.set(legacyAccountsKey, `[{"email":"sam@example.org","scopes":["`+scopeMail+`"],"checked":5},`+
		`{"email":"bob@example.org","scopes":["`+scopeMail+`"],"reconnect":"the Google app it was connected with is refused"}]`)
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: googleAccountFor(mailService, "bob@example.org"),
		Secret: &EnvValue{File: vaultPrefix + bobRef}}); err != nil {
		t.Fatal(err)
	}

	c.migrateConnections()
	c.migrateConnections() // once

	list := c.connections()
	if len(list) != 2 || c.store.get(legacyAccountsKey) != "" {
		t.Fatalf("connections %+v", list)
	}
	sam, _ := c.connectionOf("google", "SAM@example.org")
	bob, _ := c.connectionOf("google", "bob@example.org")
	if sam.Secret != samRef || sam.Lapsed != "" || sam.Checked != 5 || sam.Scopes[0] != scopeMail || bob.Secret != bobRef || bob.Lapsed != lapsedBadClient {
		t.Fatalf("sam %+v, bob %+v", sam, bob)
	}
	if raw, _ := currentVault().get(samRef); !strings.Contains(raw, `"token_url":"`+providerGoogle.Endpoint.TokenURL+`"`) || !strings.Contains(raw, `"refresh_token":"sam"`) {
		t.Errorf("sam's token: %s", raw)
	}
	if raw, _ := currentVault().get(bobRef); !strings.Contains(raw, "https://token.example.org") {
		t.Errorf("bob's token_url was replaced: %s", raw)
	}
	if tools := c.connectionTools(bob); len(tools) != 1 || tools[0] != "mail" {
		t.Errorf("bob's tools %v", tools)
	}
	if why, where := c.connectionHold("mail"); !strings.Contains(why, "bob@example.org") || where != "Settings → Tools → Google → Reconnect bob@example.org" {
		t.Errorf("hold %q %q", why, where)
	}
}

// One connection per identity: signing in again keeps its secret and lifts
// its lapse; a new identity gets a token of its own.
func TestKeepConnection(t *testing.T) {
	c := newDynamicCore(t, nil).core
	cn, lapsed, err := c.keepConnection("google", "sam@example.org", []string{"a"}, `{"refresh_token":"1"}`)
	if err != nil || lapsed || cn.Secret != "connection."+cn.ID+".token" {
		t.Fatalf("%+v %v %v", cn, lapsed, err)
	}
	c.markConnection(cn.ID, lapsedRevoked)
	again, lapsed, err := c.keepConnection("google", "Sam@example.org", []string{"a", "b"}, `{"refresh_token":"2"}`)
	if err != nil || !lapsed || again.ID != cn.ID || again.Secret != cn.Secret {
		t.Fatalf("%+v %v %v", again, lapsed, err)
	}
	if got, _ := c.connectionOf("google", "sam@example.org"); got.Lapsed != "" || len(got.Scopes) != 2 {
		t.Fatalf("%+v", got)
	}
	if raw, _ := currentVault().get(cn.Secret); raw != `{"refresh_token":"2"}` {
		t.Fatalf("token %s", raw)
	}
	other, _, _ := c.keepConnection("google", "bob@example.org", nil, `{}`)
	if other.ID == cn.ID || len(c.connections()) != 2 {
		t.Fatal("bob")
	}
}

// The tokens no connection and no tool keeps are collected; the others,
// and any other secret, stay.
func TestCollectConnectionSecrets(t *testing.T) {
	c := newDynamicCore(t, nil).core
	v := currentVault()
	cn, _, _ := c.keepConnection("google", "sam@example.org", nil, `{}`)
	inUse := legacyGoogleSecret("bob@example.org")
	for _, name := range []string{inUse, "google.0123456789ab.token", "connection.feedfacecafe.token", "other.secret"} {
		v.set(name, "x")
	}
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: googleAccountFor(mailService, "bob@example.org"),
		Secret: &EnvValue{File: vaultPrefix + inUse}}); err != nil {
		t.Fatal(err)
	}
	c.collectConnectionSecrets()
	for name, want := range map[string]bool{cn.Secret: true, inUse: true, "other.secret": true,
		"google.0123456789ab.token": false, "connection.feedfacecafe.token": false} {
		if _, err := v.get(name); (err == nil) != want {
			t.Errorf("%s kept: %v, want %v", name, err == nil, want)
		}
	}
}

// A provider no code knows, given as data: its connection is checked,
// lapses, holds its tool and is told to the brain in its own name, then
// works again.
func TestConnectionOfAnyProvider(t *testing.T) {
	f := newFakeProvider(t)
	signInProviders[f.Name] = f.signInProvider
	t.Cleanup(func() { delete(signInProviders, f.Name) })
	c := newDynamicCore(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} }).core
	cn, _, err := c.keepConnection(f.Name, "sam@example.org", nil, `{"client_id":"app-id","refresh_token":"rt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: svcAccount{Name: "sam@example.org", Address: "sam@example.org", Provider: "other"},
		Secret: &EnvValue{File: vaultPrefix + cn.Secret}}); err != nil {
		t.Fatal(err)
	}
	c.checkConnection(context.Background(), cn)
	if got, _ := c.connectionOf(f.Name, "sam@example.org"); got.Lapsed != "" {
		t.Fatalf("a working token: %+v", got)
	}
	f.refreshes["rt"] = "invalid_grant"
	c.checkConnection(context.Background(), cn)
	if why, where := c.connectionHold("mail"); !strings.Contains(why, "Example refused") || where != "Settings → Tools → Example → Reconnect sam@example.org" {
		t.Fatalf("hold %q %q", why, where)
	}
	msgs, _ := c.store.messagesAfter(0, 100)
	if len(msgs) == 0 || !strings.HasPrefix(msgs[len(msgs)-1].Text, "Example a retiré l'autorisation du compte sam@example.org") {
		t.Fatalf("brain: %+v", msgs)
	}
	f.refreshes["rt"] = ""
	c.checkConnection(context.Background(), cn)
	if why, _ := c.connectionHold("mail"); why != "" {
		t.Fatalf("still held: %s", why)
	}
}
