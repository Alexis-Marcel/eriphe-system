package main

import (
	"net/url"
	"strings"
	"testing"
)

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
	if !strings.Contains(body, `level-read-only" href="/settings/tools/mail/access?account=pro%40example.org"`) ||
		!strings.Contains(body, `level-reads" href="/settings/tools/mail/access?account=perso%40example.org"`) {
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
	bob, _, err := c.keepConnection(providerGoogle.Name, "bob@example.org", []string{scopeMail}, `{"refresh_token":"bob"}`)
	if err != nil {
		t.Fatal(err)
	}
	c.markConnection(bob.ID, lapsedRevoked)
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: gmailAccount("bob@example.org"),
		Secret: &EnvValue{File: vaultPrefix + bob.Secret}}); err != nil {
		t.Fatal(err)
	}
	if why, _ := c.connectionHold("mail"); why == "" {
		t.Fatal("bob alone, refused: not held")
	}
	if err := c.putAccount(mailService, accountPut{Lang: "fr", Account: svcAccount{Name: "sam@example.org", Address: "sam@example.org", Provider: "ovh"},
		Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if why, _ := c.connectionHold("mail"); why != "" {
		t.Fatalf("held with another mailbox: %s", why)
	}
}

// An Apple calendar added beside a Google calendar of its address is
// named apart; a Google account connected after an Apple calendar of its
// address takes the address, the Apple one keeps its secret, its level,
// what was allowed and its watches under another name. The same calendar
// twice is still refused.
func TestCalendarsOfOneAddress(t *testing.T) {
	apple := svcAccount{Name: "sam@example.org", Provider: "icloud", Username: "sam@example.org", URL: "https://caldav.icloud.com"}
	google := func(c *core) error {
		ref := "connection.sam.token"
		currentVault().set(ref, `{"refresh_token":"sam"}`)
		return c.putAccount(calendarService, accountPut{Lang: "fr", Account: googleCalendarAccount("sam@example.org"),
			Secret: &EnvValue{File: vaultPrefix + ref}})
	}

	t.Run("apple after google", func(t *testing.T) {
		c := newDynamicCore(t, nil).core
		if err := google(c); err != nil {
			t.Fatal(err)
		}
		if err := c.putAccount(calendarService, accountPut{Lang: "fr", Account: apple, Password: "apple-pw"}); err != nil {
			t.Fatal(err)
		}
		if a, _ := serviceAccount(t, c, calendarService, "sam@example.org"); !a.signedIn() {
			t.Errorf("the address is not Google's: %+v", a)
		}
		if a, pw := serviceAccount(t, c, calendarService, "sam@example.org (iCloud)"); a.Provider != "icloud" || pw != "apple-pw" {
			t.Errorf("apple: %+v %q", a, pw)
		}
		if err := c.putAccount(calendarService, accountPut{Lang: "fr", Account: apple, Password: "again"}); err != errAccountExists {
			t.Errorf("the same Apple calendar twice: %v", err)
		}
		// Its own form (which names it by its username) changes it, under
		// its name.
		if err := c.putAccount(calendarService, accountPut{Lang: "fr", Replace: "sam@example.org (iCloud)", Account: apple, Level: accessReadOnly}); err != nil {
			t.Fatal(err)
		}
		if st, _ := c.serviceServer(calendarService); len(st.Config.Accounts) != 2 || st.Server.AccountAccess["sam@example.org (iCloud)"] != accessReadOnly {
			t.Errorf("after the edit: %+v %v", st.Config.Accounts, st.Server.AccountAccess)
		}
	})

	t.Run("google after apple", func(t *testing.T) {
		c := newDynamicCore(t, nil).core
		if err := c.putAccount(calendarService, accountPut{Lang: "fr", Account: apple, Password: "apple-pw", Level: accessFree}); err != nil {
			t.Fatal(err)
		}
		if err := c.putAccount(calendarService, accountPut{Lang: "fr", Account: apple, Password: "x"}); err != errAccountExists {
			t.Fatalf("the same calendar twice: %v", err)
		}
		st, _ := c.serviceServer(calendarService)
		c.store.allowAction(st.Name, "mcp__"+st.Name+"__create_event", "sam@example.org")

		if err := google(c); err != nil {
			t.Fatal(err)
		}
		st, _ = c.serviceServer(calendarService)
		if len(st.Config.Accounts) != 2 {
			t.Fatalf("accounts: %+v", st.Config.Accounts)
		}
		if a, _ := serviceAccount(t, c, calendarService, "sam@example.org"); !a.signedIn() {
			t.Errorf("Google did not take the address: %+v", a)
		}
		a, pw := serviceAccount(t, c, calendarService, "sam@example.org (iCloud)")
		if a.Provider != "icloud" || pw != "apple-pw" || st.Server.AccountAccess["sam@example.org (iCloud)"] != accessFree {
			t.Errorf("apple: %+v %q %v", a, pw, st.Server.AccountAccess)
		}
		if got := c.store.allowedActions(st.Name); len(got) != 1 || got[0].Account != "sam@example.org (iCloud)" {
			t.Errorf("allowed: %+v", got)
		}
		// Google connected again replaces its own account only.
		if err := google(c); err != nil {
			t.Fatal(err)
		}
		if st, _ := c.serviceServer(calendarService); len(st.Config.Accounts) != 2 {
			t.Errorf("after reconnecting: %+v", st.Config.Accounts)
		}
	})
}
