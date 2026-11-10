package main

import (
	"context"
	"strings"
	"testing"
)

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
	inUse := "connection.bob.token"
	for _, name := range []string{inUse, "google.0123456789ab.token", "connection.feedfacecafe.token", "other.secret"} {
		v.set(name, "x")
	}
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: gmailAccount("bob@example.org"),
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
