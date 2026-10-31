package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// The workspace's sign-in from the machine (web_signin.go), for its owner:
// root on the host is the owner.
//
//	eriphe-core auth show                     # the mode and its settings, never a secret
//	eriphe-core auth reset                    # back to the password
//	eriphe-core auth none                     # no sign-in (a machine nobody else reaches)
//	eriphe-core auth password-reset           # forgotten password: a new setup code
//	eriphe-core auth setup-code               # the setup code, while there is no password
//	eriphe-core auth oidc -issuer … -client-id … -client-secret-stdin -allow-sub …
//
// They write what Settings → Account writes, in the same database: the
// running eriphe-core reads it again on the next request.

const authUsage = `usage:
  eriphe-core auth show  [-config file | -data dir]
  eriphe-core auth reset [-config file | -data dir]
  eriphe-core auth none  [-config file | -data dir]
  eriphe-core auth password-reset [-config file | -data dir]
  eriphe-core auth setup-code [-config file | -data dir]
  eriphe-core auth oidc  -issuer URL -client-id ID -client-secret-stdin -allow-sub S[,S] [-allow-email E[,E]]
                         [-external-url URL] [-unauthorized-url URL] [-button] [-config file | -data dir]

show prints how one signs in to the workspace; reset sets it back to the password;
none turns sign-in off (only for a machine nobody else can reach);
password-reset forgets the password (a forgotten one) and its sessions, and prints
a new setup code to choose another at /setup; setup-code prints the setup code
while there is no password; oidc sets up an identity provider (the client secret
is read from stdin).`

// authCLI is what the commands read and write; the tests set them.
type authCLI struct {
	in     io.Reader
	out    io.Writer
	client *http.Client // to reach the provider
}

// authCommand is `eriphe-core auth …`.
func authCommand(args []string) {
	cli := authCLI{in: os.Stdin, out: os.Stdout}
	if err := cli.run(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		log.Fatal(err)
	}
}

func (cli authCLI) run(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, authUsage)
		return flag.ErrHelp
	}
	switch args[0] {
	case "show":
		return cli.show(args[1:])
	case "reset":
		return cli.reset(args[1:])
	case "none":
		return cli.none(args[1:])
	case "password-reset":
		return cli.passwordReset(args[1:])
	case "setup-code":
		return cli.setupCode(args[1:])
	case "oidc":
		return cli.oidc(args[1:])
	}
	fmt.Fprintln(os.Stderr, authUsage)
	return flag.ErrHelp
}

// authFlags are the flags every command has: where eriphe-core's files are.
func authFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("auth "+name, flag.ContinueOnError)
	path := fs.String("config", "", "configuration file (without one, defaults in -data)")
	data := fs.String("data", envOr("ERIPHE_DATA", defaultData), "eriphe-core's data directory, without a configuration file (env ERIPHE_DATA)")
	return fs, path, data
}

// authStore opens eriphe-core's database; the configuration is returned
// for its web.auth, which wins over the database.
func authStore(path, data string) (*Config, *Store, error) {
	cfg, err := loadOrDefault(path, data)
	if err != nil {
		return nil, nil, err
	}
	store, err := openStore(cfg.DB)
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	return cfg, store, nil
}

func fileAuth(cfg *Config) *AuthConfig {
	if cfg.Web != nil {
		return cfg.Web.Auth
	}
	return nil
}

// storedSignin is the workspace's own settings, empty when there are none
// or they cannot be read.
func storedSignin(s *Store) AuthConfig {
	var a AuthConfig
	if raw := s.get(authKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			a = AuthConfig{}
		}
	}
	return a
}

func (cli authCLI) reset(args []string) error {
	fs, path, data := authFlags("reset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	if fileAuth(cfg) != nil {
		return fmt.Errorf("%s sets web.auth: change its mode there, then restart eriphe-core", *path)
	}
	was, err := resetSignin(store)
	if err != nil {
		return err
	}
	fmt.Fprintf(cli.out, "The workspace's sign-in is the password again (it was %s). Open /login.\n", was)
	if code := pendingSetupCode(store); code != "" {
		fmt.Fprintf(cli.out, "There is no password yet: open /setup with the setup code %s\n", code)
	}
	return nil
}

// passwordReset is the way back in for an owner who forgot the password:
// root on the host is the owner. It forgets the password and every session
// of the workspace, keeps everything else (the assistant, its memory, the
// devices, the accounts), and makes a new setup code; the running
// eriphe-core asks for it at /setup from the next request.
func (cli authCLI) passwordReset(args []string) error {
	fs, path, data := authFlags("password-reset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	code := newSetupCode()
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM web_owner`, `DELETE FROM web_sessions`} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("cannot reset the password: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, setupKey, code); err != nil {
		return fmt.Errorf("cannot reset the password: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cannot reset the password: %w", err)
	}
	fmt.Fprintln(cli.out, "The workspace's password is forgotten, and every browser signed out; nothing else changed.")
	fmt.Fprintf(cli.out, "Open the workspace at /setup (https://<its address>/setup) and choose a new password with the setup code %s\n", code)
	return nil
}

// setupCode prints the setup code, while the owner has no password.
func (cli authCLI) setupCode(args []string) error {
	fs, path, data := authFlags("setup-code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	code := pendingSetupCode(store)
	if code == "" {
		return errors.New("the workspace has a password already: sign in at /login (forgotten: eriphe-core auth password-reset)")
	}
	fmt.Fprintln(cli.out, code)
	return nil
}

// resetSignin sets the workspace's mode back to the password, keeping its
// other settings, and returns the mode it had.
// none turns the workspace's sign-in off. The page allows it only from the
// local network; on the machine, whoever is root owns the workspace anyway.
func (cli authCLI) none(args []string) error {
	fs, path, data := authFlags("none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	if fileAuth(cfg) != nil {
		return fmt.Errorf("%s sets web.auth: change its mode there, then restart eriphe-core", *path)
	}
	a := storedSignin(store)
	was := a.mode()
	a.Mode = modeNone
	if err := saveStoredSignin(store, a); err != nil {
		return err
	}
	fmt.Fprintf(cli.out, "The workspace has no sign-in any more (it was %s): whoever reaches its address gets in.\n", was)
	fmt.Fprintln(cli.out, "Back to the password: eriphe-core auth reset")
	return nil
}

func resetSignin(s *Store) (string, error) {
	a := storedSignin(s)
	was := a.mode()
	a.Mode = modePassword
	if err := saveStoredSignin(s, a); err != nil {
		return "", err
	}
	return was, nil
}

func saveStoredSignin(s *Store, a AuthConfig) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if err := s.set(authKey, string(raw)); err != nil {
		return errors.Join(errors.New("cannot save the sign-in settings"), err)
	}
	return nil
}

// show prints the settings in effect: the mode, then what it uses. A
// secret is named by where it is kept, never shown.
func (cli authCLI) show(args []string) error {
	fs, path, data := authFlags("show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	a, from := storedSignin(store), "the workspace (Settings → Account)"
	if f := fileAuth(cfg); f != nil {
		a, from = *f, *path+" (web.auth)"
	}
	out := cli.out
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(out, "  %-18s %s\n", k+":", v)
		}
	}
	list := func(l []string) string { return strings.Join(l, ", ") }
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	fmt.Fprintf(out, "mode: %s, set in %s\n", a.mode(), from)
	switch a.mode() {
	case modeOIDC:
		line("issuer", a.OIDC.Issuer)
		line("client id", a.OIDC.ClientID)
		secret := "none"
		if a.OIDC.ClientSecretFile != "" {
			secret = "kept in " + a.OIDC.ClientSecretFile
		}
		line("client secret", secret)
		line("allowed subs", list(a.OIDC.AllowedSubs))
		line("allowed emails", list(a.OIDC.AllowedEmails))
		line("sign-in button", yes(a.OIDC.Button))
		line("external url", a.ExternalURL)
		if a.ExternalURL != "" {
			line("redirect uri", strings.TrimRight(a.ExternalURL, "/")+"/auth/callback")
		}
	case modeProxy:
		line("header", a.header())
		line("trusted", list(a.Proxy.Trusted))
		line("users", list(a.Proxy.Users))
		line("external url", a.ExternalURL)
	case modeNone:
		fmt.Fprintln(out, "  anyone who reaches the workspace gets in")
	}
	if m := a.mode(); m == modeOIDC || m == modeProxy {
		line("unauthorized url", a.UnauthorizedURL)
		line("local password", yes(!a.NoLocalPassword))
	}
	return nil
}

// commaList reads a flag's list: commas, and the flag given again.
type commaList []string

func (l *commaList) String() string { return strings.Join(*l, ",") }
func (l *commaList) Set(s string) error {
	*l = append(*l, lines(s)...)
	return nil
}

// oidc sets the workspace's sign-in to an identity provider, as the
// settings page saves it, with the password kept from the local network.
// The provider must answer its discovery document; the secret comes from
// stdin, never from the command line, and goes to the vault.
func (cli authCLI) oidc(args []string) error {
	fs, path, data := authFlags("oidc")
	issuer := fs.String("issuer", "", "the provider's issuer, e.g. https://auth.example.org/application/o/eriphe/")
	clientID := fs.String("client-id", "", "the client ID the provider gave")
	stdin := fs.Bool("client-secret-stdin", false, "read the client secret from stdin (required)")
	var subs, emails commaList
	fs.Var(&subs, "allow-sub", "who may enter: the provider's account ids (sub), comma-separated")
	fs.Var(&emails, "allow-email", "who may enter: emails the provider says are verified, comma-separated")
	external := fs.String("external-url", "", "where the workspace is reached, e.g. https://eriphe.example.org (kept when not given)")
	unauthorized := fs.String("unauthorized-url", "", "where visitors who may not enter are sent (kept when not given)")
	button := fs.Bool("button", false, "show a sign-in button rather than going to the provider at once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		// Not echoed: it may be the secret, typed where it should not be.
		return errors.New("unexpected argument: the client secret is read from stdin, with -client-secret-stdin")
	}
	if !*stdin {
		return errors.New("give -client-secret-stdin and the client secret on stdin, e.g. printf %s \"$SECRET\" | eriphe-core auth oidc …")
	}
	cfg, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	if fileAuth(cfg) != nil {
		return fmt.Errorf("%s sets web.auth: change it there, then restart eriphe-core", *path)
	}
	a := storedSignin(store)
	a.Mode, a.NoLocalPassword = modeOIDC, false
	if *external != "" {
		a.ExternalURL = strings.TrimRight(strings.TrimSpace(*external), "/")
	}
	if *unauthorized != "" {
		a.UnauthorizedURL = strings.TrimSpace(*unauthorized)
	}
	a.OIDC = OIDCConfig{Issuer: strings.TrimSpace(*issuer), ClientID: strings.TrimSpace(*clientID),
		AllowedSubs: subs, AllowedEmails: emails, Button: *button}
	if err := a.check(); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(cli.in, 8<<10))
	if err != nil {
		return fmt.Errorf("reading the client secret: %w", err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return errors.New("no client secret on stdin")
	}
	sso := newSSOState()
	sso.client = cli.client
	p, err := sso.discover(context.Background(), a.OIDC.Issuer)
	if err != nil {
		return fmt.Errorf("the provider does not answer as expected, nothing changed: %w", err)
	}
	var meta struct {
		PKCE []string `json:"code_challenge_methods_supported"`
	}
	p.Claims(&meta)
	v, err := configVault(cfg, store)
	if err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	if err := v.set(oidcSecretName, secret); err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	a.OIDC.ClientSecretFile = vaultPrefix + oidcSecretName
	if err := saveStoredSignin(store, a); err != nil {
		return err
	}
	who := []string{}
	if len(subs) > 0 {
		who = append(who, "subs "+subs.String())
	}
	if len(emails) > 0 {
		who = append(who, "emails "+emails.String())
	}
	fmt.Fprintf(cli.out, "The workspace's sign-in is the identity provider %s (client %s, for %s); redirect URI %s/auth/callback; the password still opens it from the local network.\n",
		a.OIDC.Issuer, a.OIDC.ClientID, strings.Join(who, ", "), a.ExternalURL)
	if len(meta.PKCE) > 0 && !strings.Contains(strings.Join(meta.PKCE, " "), "S256") {
		fmt.Fprintln(os.Stderr, "warning: the provider does not announce PKCE (S256): check its settings")
	}
	return nil
}
