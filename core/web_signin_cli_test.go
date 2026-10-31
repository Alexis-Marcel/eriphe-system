package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliSecret = "client-secret-from-stdin"

// cliRun runs `eriphe-core auth …` with stdin and returns what it printed.
func cliRun(t *testing.T, f *fakeIssuer, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cli := authCLI{in: strings.NewReader(stdin), out: &out}
	if f != nil {
		cli.client = f.srv.Client()
	}
	err := cli.run(args)
	return out.String(), err
}

func cliStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}

// `auth oidc` stores what the settings page stores, the secret in the
// vault, the password kept from the local network; the running workspace
// signs in with it at once.
func TestAuthOIDCCommand(t *testing.T) {
	t.Setenv(vaultKeyEnv, "")
	dir := t.TempDir()
	s := cliStore(t, dir)
	// The proxy's settings, from before, stay.
	s.set(authKey, `{"mode":"password","no_local_password":true,"proxy":{"trusted":["10.0.0.2"],"users":["owner"]}}`)
	f := newFakeIssuer(t)
	out, err := cliRun(t, f, cliSecret+"\n", "oidc", "-data", dir, "-issuer", f.srv.URL, "-client-id", "eriphe",
		"-client-secret-stdin", "-allow-sub", "owner-sub,other-sub", "-allow-email", "owner@example.org",
		"-external-url", "https://eriphe.example.org/", "-button")
	if err != nil {
		t.Fatalf("auth oidc: %v", err)
	}
	if strings.Contains(out, cliSecret) || !strings.Contains(out, f.srv.URL) || !strings.Contains(out, "https://eriphe.example.org/auth/callback") ||
		strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("summary: %q", out)
	}
	a := storedSignin(s)
	if a.mode() != modeOIDC || a.NoLocalPassword || a.ExternalURL != "https://eriphe.example.org" || !a.OIDC.Button ||
		a.OIDC.ClientSecretFile != vaultPrefix+oidcSecretName || len(a.OIDC.AllowedSubs) != 2 || len(a.OIDC.AllowedEmails) != 1 ||
		len(a.Proxy.Trusted) != 1 {
		t.Fatalf("stored: %+v", a)
	}
	if raw := s.get(authKey); strings.Contains(raw, cliSecret) {
		t.Fatal("the secret is in the settings")
	}
	v, err := configVault(&Config{DB: filepath.Join(dir, "core.db")}, s)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v.get(oidcSecretName); got != cliSecret {
		t.Fatalf("vault: %q", got)
	}
	// The workspace on the same database reads it on the next request.
	tw := newTestWebFor(t, newCore(&Config{User: "Sam", Listen: "127.0.0.1:8800"}, s))
	if si := tw.w.signin(); si.cfg.mode() != modeOIDC || si.cfg.OIDC.Issuer != f.srv.URL {
		t.Fatalf("the workspace: %+v", si.cfg)
	}

	// show: the mode and its settings, never the secret.
	out, err = cliRun(t, nil, "", "show", "-data", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode: oidc", f.srv.URL, "eriphe", "owner-sub, other-sub", "owner@example.org", "vault:workspace-oidc", "local password:    yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, cliSecret) || strings.Contains(out, "10.0.0.2") {
		t.Fatalf("show: %s", out)
	}
	// And back to the password: reset keeps the rest.
	if _, err := cliRun(t, nil, "", "reset", "-data", dir); err != nil {
		t.Fatal(err)
	}
	if out, _ := cliRun(t, nil, "", "show", "-data", dir); !strings.HasPrefix(out, "mode: password") || strings.Contains(out, "issuer") {
		t.Fatalf("show after reset: %s", out)
	}
}

// What `auth oidc` refuses changes nothing.
func TestAuthOIDCCommandRefuses(t *testing.T) {
	t.Setenv(vaultKeyEnv, "")
	dir := t.TempDir()
	s := cliStore(t, dir)
	s.set(authKey, `{"external_url":"https://eriphe.example.org"}`)
	f := newFakeIssuer(t)
	base := func(extra ...string) []string {
		return append([]string{"oidc", "-data", dir, "-issuer", f.srv.URL, "-client-id", "eriphe", "-allow-sub", "owner-sub"}, extra...)
	}
	for _, tc := range []struct {
		name, stdin, want string
		args              []string
		issuer            *fakeIssuer
	}{
		{"no stdin flag", cliSecret, "client-secret-stdin", base(), f},
		{"secret in argv", "", "unexpected", base("-client-secret-stdin", cliSecret), f},
		{"empty secret", " \n", "no client secret", base("-client-secret-stdin"), f},
		{"no allowlist", cliSecret, "allowed_subs", []string{"oidc", "-data", dir, "-issuer", f.srv.URL, "-client-id", "eriphe", "-client-secret-stdin"}, f},
		{"bad issuer", cliSecret, "oidc.issuer", base("-client-secret-stdin", "-issuer", "not a url"), f},
		{"issuer down", cliSecret, "does not answer", base("-client-secret-stdin", "-issuer", f.srv.URL+"/nothing-here"), f},
		{"untrusted issuer", cliSecret, "does not answer", base("-client-secret-stdin"), nil},
		{"http external url", cliSecret, "external_url", base("-client-secret-stdin", "-external-url", "http://eriphe.example.org"), f},
	} {
		out, err := cliRun(t, tc.issuer, tc.stdin, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
		if strings.Contains(out, cliSecret) || err != nil && strings.Contains(err.Error(), cliSecret) {
			t.Errorf("%s: the secret is printed", tc.name)
		}
		if a := storedSignin(s); a.mode() != modePassword || a.OIDC.Issuer != "" {
			t.Fatalf("%s: stored %+v", tc.name, a)
		}
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM vault WHERE name = ?`, oidcSecretName).Scan(&n)
		if n != 0 {
			t.Fatalf("%s: the secret was stored", tc.name)
		}
	}
}

// A configuration file that sets web.auth wins: the commands do not
// write over it, and show says where the settings come from.
func TestAuthCommandsWithFileAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.json")
	raw, _ := json.Marshal(map[string]any{"token_file": filepath.Join(dir, "token"), "db": filepath.Join(dir, "core.db"),
		"web": map[string]any{"auth": map[string]any{"mode": "proxy", "proxy": map[string]any{"trusted": []string{"10.0.0.2"}, "users": []string{"owner"}}}}})
	os.WriteFile(path, raw, 0o600)
	f := newFakeIssuer(t)
	_, err := cliRun(t, f, cliSecret, "oidc", "-config", path, "-issuer", f.srv.URL, "-client-id", "eriphe", "-client-secret-stdin",
		"-allow-sub", "owner-sub", "-external-url", "https://eriphe.example.org")
	if err == nil || !strings.Contains(err.Error(), "web.auth") {
		t.Fatalf("oidc over the file: %v", err)
	}
	if _, err := cliRun(t, nil, "", "reset", "-config", path); err == nil || !strings.Contains(err.Error(), "web.auth") {
		t.Fatalf("reset over the file: %v", err)
	}
	out, err := cliRun(t, nil, "", "show", "-config", path)
	if err != nil || !strings.Contains(out, "mode: proxy, set in "+path) || !strings.Contains(out, "10.0.0.2") {
		t.Fatalf("show: %v %s", err, out)
	}
	if s := cliStore(t, dir); s.get(authKey) != "" {
		t.Fatal("the workspace's settings were written")
	}
}

// `auth none` turns sign-in off from the machine, keeping the other
// settings; `auth reset` brings the password back.
func TestAuthNoneCommand(t *testing.T) {
	dir := t.TempDir()
	s := cliStore(t, dir)
	s.set(authKey, `{"mode":"password","proxy":{"trusted":["10.0.0.2"],"users":["owner"]}}`)
	out, err := cliRun(t, nil, "", "none", "-data", dir)
	if err != nil || !strings.Contains(out, "auth reset") {
		t.Fatalf("auth none: %v %s", err, out)
	}
	if a := storedSignin(s); a.mode() != modeNone || len(a.Proxy.Trusted) != 1 {
		t.Fatalf("stored: %+v", a)
	}
	if _, err := cliRun(t, nil, "", "reset", "-data", dir); err != nil {
		t.Fatalf("reset after none: %v", err)
	}
	if a := storedSignin(s); a.mode() != modePassword {
		t.Fatalf("after reset: %+v", a)
	}
}
