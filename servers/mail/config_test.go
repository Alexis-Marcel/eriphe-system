package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresets(t *testing.T) {
	cfg, err := LoadConfig(`{"accounts":[
	  {"name":"perso","address":"someone@gmail.com","auth":{"password_env":"GMAIL_APP_PASSWORD"}},
	  {"address":"someone@icloud.com","auth":{"password_file":"/run/secrets/icloud"}},
	  {"name":"work","address":"me@corp.example","provider":"office365","auth":{"oauth_token_file":"/data/ms-token"}},
	  {"name":"ovh","address":"me@my-domain.example","provider":"ovh","auth":{"password_env":"OVH"}},
	  {"name":"own","address":"me@self.example","imap":{"host":"mail.self.example","port":143},"smtp":{"host":"mail.self.example","port":587},
	   "auth":{"password_env":"OWN"},"folders":{"Archive":"Old"}}]}`, "")
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Accounts
	check := func(i int, imapAddr, imapSec, smtpAddr, smtpSec string, saveSent bool) {
		t.Helper()
		if got := a[i].IMAP.addr() + " " + a[i].IMAP.Security; got != imapAddr+" "+imapSec {
			t.Errorf("%s imap: %s", a[i].Name, got)
		}
		if got := a[i].SMTP.addr() + " " + a[i].SMTP.Security; got != smtpAddr+" "+smtpSec {
			t.Errorf("%s smtp: %s", a[i].Name, got)
		}
		if *a[i].SaveSent != saveSent {
			t.Errorf("%s save_sent: %v", a[i].Name, *a[i].SaveSent)
		}
	}
	check(0, "imap.gmail.com:993", "tls", "smtp.gmail.com:465", "tls", false)
	check(1, "imap.mail.me.com:993", "tls", "smtp.mail.me.com:587", "starttls", true)
	check(2, "outlook.office365.com:993", "tls", "smtp.office365.com:587", "starttls", false)
	check(3, "ssl0.ovh.net:993", "tls", "ssl0.ovh.net:465", "tls", true)
	check(4, "mail.self.example:143", "starttls", "mail.self.example:587", "starttls", true)
	if a[0].Provider != "gmail" || a[0].Folders["archive"] != "[Gmail]/All Mail" || a[0].Username != "someone@gmail.com" {
		t.Errorf("gmail: %+v", a[0])
	}
	if a[1].Name != "someone@icloud.com" {
		t.Errorf("default name: %q", a[1].Name)
	}
	if a[4].Folders["archive"] != "Old" {
		t.Errorf("folder override: %v", a[4].Folders)
	}
	if cfg.MaxResults != 50 || cfg.MaxBodyChars != 20000 {
		t.Errorf("limits: %+v", cfg)
	}
}

func TestConfigErrors(t *testing.T) {
	for want, js := range map[string]string{
		"no accounts":       `{"accounts":[]}`,
		"address":           `{"accounts":[{"name":"x","auth":{"password_env":"X"}}]}`,
		"exactly one":       `{"accounts":[{"address":"a@gmail.com","auth":{"password_env":"X","oauth_token_env":"Y"}}]}`,
		"host is required":  `{"accounts":[{"address":"a@unknown.example","auth":{"password_env":"X"}}]}`,
		"unknown provider":  `{"accounts":[{"address":"a@x.example","provider":"aol","auth":{"password_env":"X"}}]}`,
		"duplicate":         `{"accounts":[{"name":"a","address":"a@gmail.com","auth":{"password_env":"X"}},{"name":"A","address":"b@gmail.com","auth":{"password_env":"X"}}]}`,
		"unknown field":     `{"accounts":[{"address":"a@gmail.com","password":"oops","auth":{"password_env":"X"}}]}`,
		"no configuration:": ``,
	} {
		_, err := LoadConfig(js, "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "tok")
	os.WriteFile(f, []byte(`{"access_token":"abc","expires_in":3600}`), 0o600)
	a := AccountConfig{Auth: AuthConfig{OAuthTokenFile: f}}
	if s, err := a.secret(); err != nil || s != "abc" {
		t.Errorf("json token: %q %v", s, err)
	}
	os.WriteFile(f, []byte("raw-token\n"), 0o600)
	if s, _ := a.secret(); s != "raw-token" {
		t.Errorf("raw token: %q", s)
	}
	t.Setenv("EMPTY_PW", "")
	if _, err := (&AccountConfig{Auth: AuthConfig{PasswordEnv: "EMPTY_PW"}}).secret(); err == nil {
		t.Error("empty secret accepted")
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText(`<div>Line&nbsp;one<br>Line two</div><table><tr><td>A</td><td>B</td></tr></table><p>&eacute;t&eacute; <img alt="logo"></p>`)
	for _, want := range []string{"Line one\nLine two", "A B", "été [logo]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
