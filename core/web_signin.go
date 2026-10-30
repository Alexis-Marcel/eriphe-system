package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// How one enters the workspace, like Fundarr (and the *arr apps):
//   - password (default): the owner's password, made with the setup code;
//   - oidc: any OpenID Connect provider (Authentik, Authelia, Keycloak,
//     PocketID…), for the accounts listed: Eriphe has one owner;
//   - proxy: a reverse proxy signs in and passes the user in a header,
//     believed only from the proxy's own addresses;
//   - none: no sign-in, for a machine only the owner can reach.
//
// In oidc and proxy modes the password still opens the workspace from the
// local network (private, loopback and Tailscale addresses, with no proxy
// on the way), unless that is turned off; and `eriphe-core auth reset` goes
// back to the password. The settings are the configuration file's web.auth,
// which wins, or else the workspace's own (Settings → Account). Devices
// (mutual TLS) and API clients (tokens) do not go through any of this.

const (
	modePassword = "password"
	modeOIDC     = "oidc"
	modeProxy    = "proxy"
	modeNone     = "none"

	defaultProxyHeader = "Remote-User"
	authKey            = "web_auth"       // kv: the workspace's own settings, as JSON
	oidcSecretName     = "workspace-oidc" // the vault's: the client secret typed in the workspace
)

// AuthConfig is how one signs in to the workspace (web.auth).
type AuthConfig struct {
	Mode string `json:"mode,omitempty"` // password (default), oidc, proxy or none
	// Where the workspace is reached, e.g. https://eriphe.example.org: the
	// provider sends the browser back to <external_url>/auth/callback, and
	// forms sent from that address are the workspace's own.
	ExternalURL string      `json:"external_url,omitempty"`
	OIDC        OIDCConfig  `json:"oidc,omitzero"`
	Proxy       ProxyConfig `json:"proxy,omitzero"`
	// Where the visitors who may not enter are sent; without it, they see
	// a page that says the workspace is private.
	UnauthorizedURL string `json:"unauthorized_url,omitempty"`
	// Turns off the password from the local network in oidc and proxy modes.
	NoLocalPassword bool `json:"no_local_password,omitempty"`
}

type OIDCConfig struct {
	Issuer           string `json:"issuer,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	ClientSecretFile string `json:"client_secret_file,omitempty"` // a file, or vault:<name>
	// Who may enter: the provider's account ids (sub), and emails the
	// provider says are verified.
	AllowedSubs   []string `json:"allowed_subs,omitempty"`
	AllowedEmails []string `json:"allowed_emails,omitempty"`
	// A sign-in button, rather than going to the provider at once.
	Button bool `json:"button,omitempty"`
}

type ProxyConfig struct {
	Header  string   `json:"header,omitempty"`  // Remote-User by default
	Trusted []string `json:"trusted,omitempty"` // the proxy's addresses or CIDRs
	Users   []string `json:"users,omitempty"`   // who may enter
}

// authErr is a settings error: its key words it in the workspace, its
// text in the log and on the command line.
type authErr struct{ key, text string }

func (e authErr) Error() string { return "web.auth: " + e.text }

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (a *AuthConfig) mode() string {
	if a.Mode == "" {
		return modePassword
	}
	return a.Mode
}

func (a *AuthConfig) header() string {
	if a.Proxy.Header == "" {
		return defaultProxyHeader
	}
	return a.Proxy.Header
}

// check tells whether the settings hold: their fields' form always, what
// the mode needs only in that mode.
func (a *AuthConfig) check() error {
	if a.ExternalURL != "" {
		u, err := url.Parse(a.ExternalURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
			return authErr{"externalurl", "external_url must be an https address without a path, e.g. https://eriphe.example.org"}
		}
	}
	if a.UnauthorizedURL != "" {
		if u, err := url.Parse(a.UnauthorizedURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return authErr{"unauthorizedurl", "unauthorized_url must be a full address, e.g. https://www.example.org"}
		}
	}
	if a.OIDC.Issuer != "" {
		if u, err := url.Parse(a.OIDC.Issuer); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return authErr{"issuer", "oidc.issuer must be the provider's address, e.g. https://auth.example.org/application/o/eriphe/"}
		}
	}
	if a.Proxy.Header != "" && !headerName.MatchString(a.Proxy.Header) {
		return authErr{"header", "proxy.header must be a header name, e.g. Remote-User"}
	}
	if _, err := parsePrefixes(a.Proxy.Trusted); err != nil {
		return authErr{"trusted", "proxy.trusted: " + err.Error()}
	}
	switch a.mode() {
	case modePassword, modeNone:
	case modeOIDC:
		if a.OIDC.Issuer == "" || a.OIDC.ClientID == "" {
			return authErr{"oidcfields", "oidc needs oidc.issuer and oidc.client_id"}
		}
		if a.ExternalURL == "" {
			return authErr{"oidcurl", "oidc needs external_url: the provider sends the browser back to <external_url>/auth/callback"}
		}
		if len(a.OIDC.AllowedSubs) == 0 && len(a.OIDC.AllowedEmails) == 0 {
			return authErr{"allowlist", "oidc needs oidc.allowed_subs or oidc.allowed_emails: not every account of the provider may enter"}
		}
	case modeProxy:
		if len(a.Proxy.Trusted) == 0 {
			return authErr{"notrusted", "proxy needs proxy.trusted, the proxy's addresses: the header is believed from them only"}
		}
		if len(a.Proxy.Users) == 0 {
			return authErr{"nousers", "proxy needs proxy.users, who may enter"}
		}
	default:
		return authErr{"mode", fmt.Sprintf("unknown mode %q (password, oidc, proxy or none)", a.Mode)}
	}
	return nil
}

// parsePrefixes reads addresses and CIDRs.
func parsePrefixes(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or a CIDR", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// signin is the settings in effect, read.
type signin struct {
	cfg     AuthConfig
	locked  bool // from the configuration file
	trusted []netip.Prefix
	origin  string // the external URL's scheme and host
}

func newSignin(a AuthConfig, locked bool) (*signin, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	si := &signin{cfg: a, locked: locked}
	si.trusted, _ = parsePrefixes(a.Proxy.Trusted)
	if u, err := url.Parse(a.ExternalURL); err == nil && u.Host != "" {
		si.origin = strings.ToLower(u.Scheme + "://" + u.Host)
	}
	return si, nil
}

// signin returns the settings in effect: the configuration file's, else
// the workspace's, read again when they change (also by `auth reset`).
func (w *web) signin() *signin {
	if w.fileAuth != nil {
		return w.fileAuth
	}
	raw := w.c.store.get(authKey)
	w.authMu.Lock()
	defer w.authMu.Unlock()
	if w.authCur != nil && raw == w.authRaw {
		return w.authCur
	}
	var (
		a   AuthConfig
		si  *signin
		err error
	)
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &a)
	}
	if err == nil {
		si, err = newSignin(a, false)
	}
	if err != nil {
		// Never a door left open by a bad row: back to the password.
		log.Printf("web: sign-in settings: %v: using the password", err)
		si = &signin{cfg: AuthConfig{Mode: modePassword}}
	}
	w.authRaw, w.authCur = raw, si
	return si
}

func (w *web) saveSignin(a AuthConfig) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return w.c.store.set(authKey, string(raw))
}

// localAddr: private, loopback, link-local, and Tailscale's shared space (cgnat).
func localAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || cgnat.Contains(a)
}

func hostAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	return a.Unmap(), err == nil
}

// fromLocalNetwork is true when the browser and every proxy on the way
// (X-Forwarded-For, X-Real-Ip) have local addresses. A proxy that speaks
// RFC 7239's Forwarded is not read: such a request is not local.
func fromLocalNetwork(r *http.Request) bool {
	if r.Header.Get("Forwarded") != "" {
		return false
	}
	addrs := []string{r.RemoteAddr}
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip"} {
		for _, v := range r.Header.Values(h) {
			for _, s := range strings.Split(v, ",") {
				if s = strings.TrimSpace(s); s != "" {
					addrs = append(addrs, s)
				}
			}
		}
	}
	for _, s := range addrs {
		a, ok := hostAddr(s)
		if !ok || !localAddr(a) {
			return false
		}
	}
	return true
}

// fromProxy is true when the request comes straight from a declared proxy.
func (si *signin) fromProxy(r *http.Request) bool {
	a, ok := hostAddr(r.RemoteAddr)
	if !ok {
		return false
	}
	for _, p := range si.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// proxyUser is the user a declared proxy passes, when they may enter.
func (si *signin) proxyUser(r *http.Request) (string, bool) {
	if !si.fromProxy(r) {
		return "", false
	}
	user := strings.TrimSpace(r.Header.Get(si.cfg.header()))
	if user == "" {
		return "", false
	}
	for _, u := range si.cfg.Proxy.Users {
		if strings.EqualFold(strings.TrimSpace(u), user) {
			return user, true
		}
	}
	return user, false
}

// passwordHere tells whether the password may open the workspace for r:
// always in password mode; from the local network, not through a declared
// proxy, in oidc and proxy modes unless that is off; never in none mode
// (there is nothing to open).
func (si *signin) passwordHere(r *http.Request) bool {
	switch si.cfg.mode() {
	case modePassword:
		return true
	case modeOIDC, modeProxy:
		return !si.cfg.NoLocalPassword && fromLocalNetwork(r) && !si.fromProxy(r)
	}
	return false
}

// keepsPassword tells whether a session opened with the password is still
// good in this mode.
func (si *signin) keepsPassword() bool {
	switch si.cfg.mode() {
	case modePassword, modeNone:
		return true
	}
	return !si.cfg.NoLocalPassword
}

const (
	viaPassword = "password"
	viaOIDC     = "oidc"
	viaProxy    = "proxy"
	viaNone     = "none"
)

type verdict int

const (
	admit     verdict = iota // the session is good
	admitNew                 // good, with a new session (via)
	toLogin                  // to the sign-in page
	toSetup                  // to the first password
	unwelcome                // may not enter
)

// decide is what the request, with its session (or none), meets under
// these settings; via is the new session's, for admitNew.
func (w *web) decide(r *http.Request, s *session, si *signin) (v verdict, via string) {
	switch si.cfg.mode() {
	case modeNone:
		if s != nil {
			return admit, ""
		}
		return admitNew, viaNone
	case modeProxy:
		// The sessions eriphe-core opened itself (the password, the
		// provider) stay while the password does.
		if s != nil && (s.Via == viaPassword || s.Via == viaOIDC) && si.keepsPassword() {
			return admit, ""
		}
		if _, ok := si.proxyUser(r); ok {
			if s != nil && s.Via == viaProxy {
				return admit, ""
			}
			return admitNew, viaProxy
		}
		if si.passwordHere(r) {
			if !w.hasOwner() {
				return toSetup, ""
			}
			return toLogin, ""
		}
		return unwelcome, ""
	case modeOIDC:
		if s != nil && (s.Via == viaOIDC || s.Via == viaPassword && si.keepsPassword()) {
			return admit, ""
		}
		return toLogin, ""
	}
	if !w.hasOwner() {
		return toSetup, ""
	}
	if s != nil && (s.Via == viaPassword || s.Via == viaOIDC) {
		return admit, ""
	}
	return toLogin, ""
}

// refuse answers a visitor who may not enter: the address of the settings,
// or a page that says the workspace is private.
func (w *web) refuse(rw http.ResponseWriter, r *http.Request, si *signin) {
	if r.URL.Path == "/events" {
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}
	if u := si.cfg.UnauthorizedURL; u != "" {
		redirect(rw, r, u)
		return
	}
	rw.WriteHeader(http.StatusForbidden)
	w.render(rw, r, nil, "login", "", page{Title: "private.title", Data: authForm{Private: true}})
}

// authCommand is `eriphe-core auth reset`: back to the password, for when
// the provider or the proxy keeps the owner out.
func authCommand(args []string) {
	if len(args) == 0 || args[0] != "reset" {
		fmt.Fprintln(os.Stderr, "usage: eriphe-core auth reset [-config file | -data dir]\n\nSets the workspace's sign-in back to the password.")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("auth reset", flag.ExitOnError)
	path := fs.String("config", "", "configuration file (without one, defaults in -data)")
	data := fs.String("data", envOr("ERIPHE_DATA", defaultData), "eriphe-core's data directory, without a configuration file (env ERIPHE_DATA)")
	fs.Parse(args[1:])
	cfg, err := loadOrDefault(*path, *data)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.Web != nil && cfg.Web.Auth != nil {
		log.Fatalf("%s sets web.auth: change its mode there, then restart eriphe-core", *path)
	}
	store, err := openStore(cfg.DB)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	was, err := resetSignin(store)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("The workspace's sign-in is the password again (it was %s). Open /login.\n", was)
	var n int
	store.db.QueryRow(`SELECT COUNT(*) FROM web_owner`).Scan(&n)
	if n == 0 {
		fmt.Println("There is no password yet: the setup code is in eriphe-core's log.")
	}
}

// resetSignin sets the workspace's mode back to the password, keeping its
// other settings, and returns the mode it had.
func resetSignin(s *Store) (string, error) {
	var a AuthConfig
	if raw := s.get(authKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			a = AuthConfig{}
		}
	}
	was := a.mode()
	a.Mode = modePassword
	raw, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	if err := s.set(authKey, string(raw)); err != nil {
		return "", errors.Join(errors.New("cannot save the sign-in settings"), err)
	}
	return was, nil
}
