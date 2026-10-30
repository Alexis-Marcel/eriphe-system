package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Settings → Account → Sign-in (web_signin.go). The client secret is
// write-only: kept in the vault, never shown back. A change cannot shut
// the owner out: the provider's mode needs a test sign-in as an allowed
// account first, with the settings as they are saved; the proxy's, that
// this very page comes through the proxy (or that the password stays
// usable from here).

type signinView struct {
	Cfg       AuthConfig
	Mode      string
	Locked    bool // the configuration file's
	HasSecret bool
	Subs      string // the lists, one per line
	Emails    string
	Trusted   string
	Users     string
	Callback  string // where the provider sends the browser back
	Local     bool   // this page is opened from the local network
	Who       string // the account of the test sign-in, and its id
	Sub       string
	TrialOK   bool
	Result    string // the test sign-in just ended: ok, notallowed or failed
}

func (w *web) signinView(r *http.Request) signinView {
	si := w.signin()
	v := signinView{Cfg: si.cfg, Locked: si.locked, Local: fromLocalNetwork(r) && !si.fromProxy(r)}
	v.HasSecret = v.Cfg.OIDC.ClientSecretFile != ""
	if res := r.URL.Query().Get("sso"); res != "" {
		if t := w.trial(); t != nil && t.result != "pending" {
			// Back from the test sign-in: the form shows what was tried.
			v.Who, v.Sub, v.Result, v.TrialOK = t.who, t.sub, t.result, t.result == "ok"
			if !si.locked {
				v.Cfg.Mode, v.Cfg.ExternalURL, v.Cfg.OIDC = t.cfg.Mode, t.cfg.ExternalURL, t.cfg.OIDC
				v.Cfg.UnauthorizedURL, v.Cfg.NoLocalPassword, v.Cfg.Proxy = t.cfg.UnauthorizedURL, t.cfg.NoLocalPassword, t.cfg.Proxy
				v.HasSecret = v.HasSecret || t.secret != ""
			}
		}
	}
	v.Mode = v.Cfg.mode()
	v.Subs, v.Emails = strings.Join(v.Cfg.OIDC.AllowedSubs, "\n"), strings.Join(v.Cfg.OIDC.AllowedEmails, "\n")
	v.Trusted, v.Users = strings.Join(v.Cfg.Proxy.Trusted, "\n"), strings.Join(v.Cfg.Proxy.Users, "\n")
	if v.Cfg.ExternalURL != "" {
		v.Callback = strings.TrimRight(v.Cfg.ExternalURL, "/") + "/auth/callback"
	}
	return v
}

// lines reads a list typed one per line, or with commas.
func lines(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// signinForm reads the form; the secret typed is apart, and the one
// stored stays when none is.
func signinForm(r *http.Request, old AuthConfig) (AuthConfig, string) {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	a := AuthConfig{
		Mode:            v("mode"),
		ExternalURL:     old.ExternalURL, // the workspace's address, set apart (web_address.go)
		UnauthorizedURL: v("unauthorized_url"),
		NoLocalPassword: r.PostFormValue("local_password") != "1",
		OIDC: OIDCConfig{Issuer: v("oidc_issuer"), ClientID: v("oidc_client_id"), ClientSecretFile: old.OIDC.ClientSecretFile,
			AllowedSubs: lines(r.PostFormValue("oidc_subs")), AllowedEmails: lines(r.PostFormValue("oidc_emails")),
			Button: r.PostFormValue("oidc_button") == "1"},
		Proxy: ProxyConfig{Header: v("proxy_header"), Trusted: old.Proxy.Trusted, // the trusted proxies, with the address
			Users: lines(r.PostFormValue("proxy_users"))},
	}
	if a.Proxy.Header == defaultProxyHeader {
		a.Proxy.Header = ""
	}
	return a, v("oidc_secret")
}

type signinResult struct {
	Msg string
	OK  bool
}

func (w *web) signinMsg(rw http.ResponseWriter, r *http.Request, s *session, ok bool, msg string) {
	w.render(rw, r, s, "settings", "signin-result", page{Data: signinResult{Msg: msg, OK: ok}})
}

func (w *web) authErrText(r *http.Request, err error) string {
	var ae authErr
	if errors.As(err, &ae) {
		return tr(w.lang(r), "signin.err."+ae.key)
	}
	return tr(w.lang(r), "err.internal")
}

// typedSecret is the secret of the form, else the one of the test sign-in
// that just ended (the form does not show it back).
func (w *web) typedSecret(typed string) string {
	if typed != "" {
		return typed
	}
	if t := w.trial(); t != nil {
		return t.secret
	}
	return ""
}

func (w *web) handleSaveSignin(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	si := w.signin()
	if si.locked {
		w.signinMsg(rw, r, s, false, tr(lang, "signin.locked"))
		return
	}
	a, typed := signinForm(r, si.cfg)
	typed = w.typedSecret(typed)
	if err := a.check(); err != nil {
		w.signinMsg(rw, r, s, false, w.authErrText(r, err))
		return
	}
	secret := typed
	if secret == "" {
		secret, _ = clientSecret(a)
	}
	if a.mode() == modeOIDC {
		old, _ := clientSecret(si.cfg)
		same := si.cfg.mode() == modeOIDC && signinPrint(si.cfg, old) == signinPrint(a, secret)
		if !same && !w.tested(a, secret) {
			w.signinMsg(rw, r, s, false, tr(lang, "signin.testfirst"))
			return
		}
	}
	// Whoever changes the mode stays in: the password's mode always lets
	// the owner back in with the password; the others must take this page.
	if m := a.mode(); m == modeOIDC || m == modeProxy {
		next, _ := newSignin(a, false)
		if v, _ := w.decide(r, s, next); v != admit && v != admitNew {
			w.signinMsg(rw, r, s, false, tr(lang, "signin.lockout."+m))
			return
		}
	}
	if typed != "" {
		v := currentVault()
		if v == nil {
			w.signinMsg(rw, r, s, false, tr(lang, "signin.novault"))
			return
		}
		if err := v.set(oidcSecretName, typed); err != nil {
			w.signinMsg(rw, r, s, false, tr(lang, "err.internal"))
			return
		}
		a.OIDC.ClientSecretFile = vaultPrefix + oidcSecretName
	}
	if err := w.saveSignin(a); err != nil {
		w.signinMsg(rw, r, s, false, tr(lang, "err.internal"))
		return
	}
	if a.mode() != si.cfg.mode() {
		log.Printf("web: sign-in: %s, was %s", a.mode(), si.cfg.mode())
	}
	w.signinMsg(rw, r, s, true, tr(lang, "signin.saved"))
}

// handleTestIssuer reads the provider's discovery document ("Test").
func (w *web) handleTestIssuer(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	issuer := strings.TrimSpace(r.PostFormValue("oidc_issuer"))
	if si := w.signin(); si.locked {
		issuer = si.cfg.OIDC.Issuer
	}
	if u, err := url.Parse(issuer); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		w.signinMsg(rw, r, s, false, tr(lang, "signin.err.issuer"))
		return
	}
	if !w.testLimit.allow(s.ID, w.now()) {
		w.signinMsg(rw, r, s, false, tr(lang, "test.ratelimit"))
		return
	}
	p, err := w.sso.discover(r.Context(), issuer)
	if err != nil {
		w.signinMsg(rw, r, s, false, fmt.Sprintf(tr(lang, "signin.issuerfailed"), err))
		return
	}
	var meta struct {
		PKCE []string `json:"code_challenge_methods_supported"`
	}
	p.Claims(&meta)
	msg := fmt.Sprintf(tr(lang, "signin.issuerok"), p.Endpoint().AuthURL)
	if len(meta.PKCE) > 0 && !strings.Contains(strings.Join(meta.PKCE, " "), "S256") {
		msg += " " + tr(lang, "signin.nopkce")
	}
	w.signinMsg(rw, r, s, true, msg)
}

// handleTrySignin starts the test sign-in, with the form's settings.
func (w *web) handleTrySignin(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	si := w.signin()
	a, typed := si.cfg, ""
	if !si.locked {
		a, typed = signinForm(r, si.cfg)
		typed = w.typedSecret(typed)
	}
	try := a
	try.Mode = modeOIDC // what the provider's mode needs
	if err := try.check(); err != nil {
		w.signinMsg(rw, r, s, false, w.authErrText(r, err))
		return
	}
	if addr := w.elsewhere(r); addr != "" {
		// The provider sends the browser back to the address: this
		// session's cookie is not there.
		w.signinMsg(rw, r, s, false, fmt.Sprintf(tr(lang, "address.elsewhere"), addr))
		return
	}
	secret := typed
	if secret == "" {
		var err error
		if secret, err = clientSecret(a); err != nil {
			w.signinMsg(rw, r, s, false, tr(lang, "signin.nosecret"))
			return
		}
	}
	w.sso.mu.Lock()
	w.sso.trial = &ssoTrial{cfg: a, secret: typed, result: "pending", at: w.now()}
	w.sso.mu.Unlock()
	to, err := w.startSSO(rw, r, &ssoFlow{test: true, cfg: try, secret: secret, session: s.ID}, try, secret)
	if err != nil {
		log.Printf("web: test sign-in with %s: %v", a.OIDC.Issuer, err)
		w.signinMsg(rw, r, s, false, fmt.Sprintf(tr(lang, "signin.issuerfailed"), err))
		return
	}
	rw.Header().Set("HX-Redirect", to)
}
