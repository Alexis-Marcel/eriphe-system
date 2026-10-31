package main

import (
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// newTestWebIn is a workspace whose database is in dir, where the auth
// commands find it with -data dir.
func newTestWebIn(t *testing.T, dir string) *testWeb {
	t.Helper()
	s, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return newTestWebFor(t, newCore(&Config{User: "Sam", Listen: "127.0.0.1:8800"}, s))
}

var printedCode = regexp.MustCompile(`[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}`)

// The setup code stays the same until it is used: across restarts, and
// for `auth setup-code`; once used, there is none.
func TestSetupCodeKept(t *testing.T) {
	dir := t.TempDir()
	tw := newTestWebIn(t, dir)
	code := tw.w.setupCode()
	if !printedCode.MatchString(code) {
		t.Fatalf("setup code %q", code)
	}
	// A restart: another workspace on the same database.
	again, err := newWeb(tw.c, WebConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := again.setupCode(); got != code {
		t.Fatalf("after a restart: %q, was %q", got, code)
	}
	out, err := cliRun(t, nil, "", "setup-code", "-data", dir)
	if err != nil || strings.TrimSpace(out) != code {
		t.Fatalf("auth setup-code: %v %q", err, out)
	}
	tw.setup()
	if tw.w.setupCode() != "" || tw.c.store.get(setupKey) != "" {
		t.Fatal("the setup code outlives the password")
	}
	if _, err := cliRun(t, nil, "", "setup-code", "-data", dir); err == nil || !strings.Contains(err.Error(), "password-reset") {
		t.Fatalf("auth setup-code with a password: %v", err)
	}
}

// `auth password-reset` forgets the password and signs every browser
// out, keeps the rest, and gives a new setup code the running workspace
// takes at once.
func TestAuthPasswordReset(t *testing.T) {
	dir := t.TempDir()
	tw := newTestWebIn(t, dir)
	first := tw.w.setupCode()
	tw.setup()
	tw.c.store.set("assistant.name", "Nova") // something else of the house
	tw.c.store.set(authKey, `{"mode":"password"}`)

	out, err := cliRun(t, nil, "", "password-reset", "-data", dir)
	if err != nil {
		t.Fatalf("auth password-reset: %v", err)
	}
	code := printedCode.FindString(out)
	if code == "" || code == first || !strings.Contains(out, "/setup") {
		t.Fatalf("printed: %q", out)
	}
	if tw.w.hasOwner() {
		t.Fatal("the password is still there")
	}
	if tw.c.store.get("assistant.name") != "Nova" || tw.c.store.get(authKey) == "" {
		t.Fatal("password-reset changed something else")
	}
	// The old session is gone: the workspace asks for the setup.
	if status, _, h := tw.do("GET", "/settings/account", nil, nil); status != http.StatusSeeOther || h.Get("Location") != "/setup" {
		t.Fatalf("after the reset: %d %s", status, h.Get("Location"))
	}
	if got := tw.w.setupCode(); got != code {
		t.Fatalf("the workspace's setup code is %q, the command printed %q", got, code)
	}
	tok := tw.formToken("/setup")
	if status, _, _ := tw.do("POST", "/setup", url.Values{"csrf": {tok}, "code": {first},
		"password": {"another password"}, "confirm": {"another password"}}, nil); status != http.StatusUnauthorized {
		t.Fatalf("the old setup code: %d", status)
	}
	if status, _, h := tw.do("POST", "/setup", url.Values{"csrf": {tok}, "code": {strings.ToLower(code)},
		"password": {"another password"}, "confirm": {"another password"}}, nil); status != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("setup with the new code: %d %s", status, h.Get("Location"))
	}
	if !checkPassword(tw.w.ownerHash(), "another password") {
		t.Fatal("the new password does not open")
	}
}

func TestAcceptLang(t *testing.T) {
	for h, want := range map[string]string{
		"":                        "fr",
		"*":                       "fr",
		"fr-FR,fr;q=0.9,en;q=0.8": "fr",
		"FR":                      "fr",
		"en-US,en;q=0.9,fr;q=0.8": "en",
		"de-DE,de;q=0.9":          "en",
		" fr-CA ; q=1":            "fr",
	} {
		if got := acceptLang(h, "fr"); got != want {
			t.Errorf("acceptLang(%q) = %s, want %s", h, got, want)
		}
	}
}

// Without web.lang, the browser's language; the browser's own choice
// wins; web.lang wins over the header.
func TestWorkspaceLanguage(t *testing.T) {
	tw := newTestWeb(t)
	page := func(header map[string]string) string {
		t.Helper()
		_, body, _ := tw.do("GET", "/setup", nil, header)
		m := regexp.MustCompile(`<html lang="(\w+)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no lang in %.200s", body)
		}
		return m[1]
	}
	if got := page(map[string]string{"Accept-Language": "en-GB,en;q=0.9"}); got != "en" {
		t.Fatalf("English browser: %s", got)
	}
	if got := page(map[string]string{"Accept-Language": "fr-FR"}); got != "fr" {
		t.Fatalf("French browser: %s", got)
	}
	if got := page(nil); got != "fr" {
		t.Fatalf("no header: %s", got)
	}
	if got := page(map[string]string{"Accept-Language": "en", "Cookie": "lang=fr"}); got != "fr" {
		t.Fatalf("the browser's choice: %s", got)
	}
	tw.w.langSet = true
	if got := page(map[string]string{"Accept-Language": "en"}); got != "fr" {
		t.Fatalf("web.lang fr: %s", got)
	}
}

// The setup page says where its code is: printed by the installer, and by
// auth setup-code, not only in a log the owner may never open.
func TestSetupPageSaysWhereTheCodeIs(t *testing.T) {
	tw := newTestWeb(t)
	for lang, want := range map[string]string{"fr": "la fin de l&#39;installation", "en": "the end of the install"} {
		_, body, _ := tw.do("GET", "/setup", nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(body, want) || !strings.Contains(body, "auth setup-code") {
			t.Errorf("%s: the setup page does not say where the code is", lang)
		}
	}
}
