package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Signing in with an OpenID Connect provider: the authorization code flow
// with PKCE, a state bound to the browser by a cookie, and a nonce; the ID
// token's signature, issuer, audience and expiry are checked by go-oidc.
// A flow is either a sign-in, or the test sign-in of the settings, which
// proves that the provider lets the owner in before the mode changes.

const ssoCookie = "__Host-eriphe-sso" // binds a flow to the browser that started it

const (
	ssoFlowTTL  = 10 * time.Minute
	ssoMaxFlows = 1000
	ssoTrialTTL = time.Hour // how long a test sign-in counts
	providerTTL = time.Hour // how long a provider's discovery is kept
)

type ssoFlow struct {
	binding  string // the hash of the cookie's value
	nonce    string
	verifier string // PKCE
	expires  time.Time
	// The test sign-in: the settings tried, their secret, and the session
	// it proves.
	test    bool
	cfg     AuthConfig
	secret  string
	session string
}

// ssoTrial is the last test sign-in of the settings.
type ssoTrial struct {
	cfg    AuthConfig
	secret string // as typed in the form; "" for the one stored
	print  string // what was tried (fingerprint), when it passed
	result string // pending, ok, notallowed or failed
	who    string
	sub    string
	at     time.Time
}

type ssoState struct {
	mu        sync.Mutex
	flows     map[string]*ssoFlow // by state
	providers map[string]cachedProvider
	trial     *ssoTrial
	client    *http.Client // to reach the provider; tests set it
}

type cachedProvider struct {
	p  *oidc.Provider
	at time.Time
}

func newSSOState() *ssoState {
	return &ssoState{flows: map[string]*ssoFlow{}, providers: map[string]cachedProvider{}}
}

func (st *ssoState) ctx(ctx context.Context) context.Context {
	c := st.client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	return oidc.ClientContext(ctx, c)
}

// discover reads the provider's discovery document.
func (st *ssoState) discover(ctx context.Context, issuer string) (*oidc.Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return oidc.NewProvider(st.ctx(ctx), issuer)
}

// provider is the issuer's provider, discovered again after providerTTL.
func (st *ssoState) provider(ctx context.Context, issuer string, now time.Time) (*oidc.Provider, error) {
	st.mu.Lock()
	cp, ok := st.providers[issuer]
	st.mu.Unlock()
	if ok && now.Sub(cp.at) < providerTTL {
		return cp.p, nil
	}
	p, err := st.discover(ctx, issuer)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.providers[issuer] = cachedProvider{p, now}
	st.mu.Unlock()
	return p, nil
}

func oauthConfig(a AuthConfig, secret string, p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.OIDC.ClientID,
		ClientSecret: secret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  strings.TrimRight(a.ExternalURL, "/") + "/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

// signinPrint is what a test sign-in vouches for: changing any of it
// needs another one.
func signinPrint(a AuthConfig, secret string) string {
	h := sha256.New()
	for _, s := range []string{a.OIDC.Issuer, a.OIDC.ClientID, secret, strings.TrimRight(a.ExternalURL, "/"),
		strings.Join(a.OIDC.AllowedSubs, "\n"), strings.ToLower(strings.Join(a.OIDC.AllowedEmails, "\n"))} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// clientSecret reads the secret the settings point at, if any.
func clientSecret(a AuthConfig) (string, error) {
	if a.OIDC.ClientSecretFile == "" {
		return "", nil
	}
	return readSecret(a.OIDC.ClientSecretFile)
}

// startSSO begins a flow and returns the provider's address to go to.
func (w *web) startSSO(rw http.ResponseWriter, r *http.Request, f *ssoFlow, a AuthConfig, secret string) (string, error) {
	p, err := w.sso.provider(r.Context(), a.OIDC.Issuer, w.now())
	if err != nil {
		return "", err
	}
	state, bind := randomToken(24), randomToken(24)
	f.binding, f.nonce, f.verifier = hashToken(bind), randomToken(24), oauth2.GenerateVerifier()
	f.expires = w.now().Add(ssoFlowTTL)
	now := w.now()
	w.sso.mu.Lock()
	// Past the cap, the oldest flow goes: anyone may start one, and
	// refusing new ones would keep the owner out.
	oldest := ""
	for k, old := range w.sso.flows {
		if now.After(old.expires) {
			delete(w.sso.flows, k)
		} else if oldest == "" || old.expires.Before(w.sso.flows[oldest].expires) {
			oldest = k
		}
	}
	if len(w.sso.flows) >= ssoMaxFlows {
		delete(w.sso.flows, oldest)
	}
	w.sso.flows[state] = f
	w.sso.mu.Unlock()
	// Lax: the provider sends the browser back from its own site.
	http.SetCookie(rw, &http.Cookie{Name: ssoCookie, Value: bind, Path: "/", MaxAge: int(ssoFlowTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	return oauthConfig(a, secret, p).AuthCodeURL(state, oidc.Nonce(f.nonce), oauth2.S256ChallengeOption(f.verifier)), nil
}

// takeFlow returns the flow of a state once, and forgets it.
func (w *web) takeFlow(state string) *ssoFlow {
	w.sso.mu.Lock()
	defer w.sso.mu.Unlock()
	f := w.sso.flows[state]
	delete(w.sso.flows, state)
	if f == nil || w.now().After(f.expires) {
		return nil
	}
	return f
}

// handleSSOStart sends a visitor to the provider (GET /auth/login).
func (w *web) handleSSOStart(rw http.ResponseWriter, r *http.Request) {
	si := w.signin()
	if si.cfg.mode() != modeOIDC {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	}
	secret, err := clientSecret(si.cfg)
	var to string
	if err == nil {
		to, err = w.startSSO(rw, r, &ssoFlow{}, si.cfg, secret)
	}
	if err != nil {
		log.Printf("web: sign-in with %s: %v", si.cfg.OIDC.Issuer, err)
		w.loginFailed(rw, r, si, http.StatusBadGateway, "sso.unreachable")
		return
	}
	http.Redirect(rw, r, to, http.StatusFound)
}

// loginFailed shows the sign-in page again, with what went wrong.
func (w *web) loginFailed(rw http.ResponseWriter, r *http.Request, si *signin, status int, msg string) {
	f := w.loginForm(r, si)
	f.Error = msg
	rw.WriteHeader(status)
	w.render(rw, r, &session{CSRF: w.preToken(rw, r)}, "login", "", page{Title: "login.title", Data: f})
}

type ssoClaims struct {
	Sub      string   `json:"sub"`
	Email    string   `json:"email"`
	Verified flexBool `json:"email_verified"`
	Username string   `json:"preferred_username"`
}

// flexBool is a claim some providers send as a string ("true").
type flexBool bool

func (b *flexBool) UnmarshalJSON(raw []byte) error {
	s := strings.Trim(string(raw), `"`)
	*b = flexBool(strings.EqualFold(s, "true"))
	return nil
}

func (c ssoClaims) who() string {
	if c.Email != "" {
		return c.Email
	}
	if c.Username != "" {
		return c.Username
	}
	return c.Sub
}

func allowed(a AuthConfig, c ssoClaims) bool {
	for _, s := range a.OIDC.AllowedSubs {
		if s = strings.TrimSpace(s); s != "" && s == c.Sub {
			return true
		}
	}
	if c.Verified {
		for _, m := range a.OIDC.AllowedEmails {
			if m = strings.TrimSpace(m); m != "" && strings.EqualFold(m, c.Email) {
				return true
			}
		}
	}
	return false
}

// finishSSO trades the code for the tokens and checks the ID token.
func (w *web) finishSSO(ctx context.Context, f *ssoFlow, a AuthConfig, secret, code string) (ssoClaims, error) {
	var c ssoClaims
	p, err := w.sso.provider(ctx, a.OIDC.Issuer, w.now())
	if err != nil {
		return c, err
	}
	tok, err := oauthConfig(a, secret, p).Exchange(w.sso.ctx(ctx), code, oauth2.VerifierOption(f.verifier))
	if err != nil {
		return c, fmt.Errorf("code exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return c, errors.New("no ID token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: a.OIDC.ClientID, Now: w.now}).Verify(w.sso.ctx(ctx), raw)
	if err != nil {
		return c, err
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.nonce)) != 1 {
		return c, errors.New("the ID token's nonce is not the flow's")
	}
	if err := idt.Claims(&c); err != nil {
		return c, err
	}
	c.Sub = idt.Subject
	return c, nil
}

// handleSSOCallback ends a flow (GET /auth/callback). The browser comes
// from the provider's site, so the session cookie (SameSite=Strict) only
// comes back with the next page: the answer moves on with a refresh.
func (w *web) handleSSOCallback(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := w.takeFlow(q.Get("state"))
	c, err := r.Cookie(ssoCookie)
	http.SetCookie(rw, &http.Cookie{Name: ssoCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	si := w.signin()
	if f == nil || err != nil || subtle.ConstantTimeCompare([]byte(hashToken(c.Value)), []byte(f.binding)) != 1 {
		if f != nil && f.test {
			w.endTrial("failed", ssoClaims{}, "")
			w.moveOn(rw, r, "/settings/account?sso=failed")
			return
		}
		w.loginFailed(rw, r, si, http.StatusBadRequest, "sso.expired")
		return
	}
	a, secret := si.cfg, ""
	if f.test {
		a, secret = f.cfg, f.secret
	} else if a.mode() != modeOIDC {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	} else if secret, err = clientSecret(a); err != nil {
		log.Printf("web: sign-in: client secret: %v", err)
		w.loginFailed(rw, r, si, http.StatusInternalServerError, "sso.failed")
		return
	}
	var claims ssoClaims
	if e := q.Get("error"); e != "" {
		err = fmt.Errorf("the provider answered %s", e)
	} else {
		claims, err = w.finishSSO(r.Context(), f, a, secret, q.Get("code"))
	}
	if err != nil {
		log.Printf("web: sign-in with %s failed: %v", a.OIDC.Issuer, err)
		if f.test {
			w.endTrial("failed", claims, "")
			w.moveOn(rw, r, "/settings/account?sso=failed")
			return
		}
		w.loginFailed(rw, r, si, http.StatusBadRequest, "sso.failed")
		return
	}
	if !allowed(a, claims) {
		log.Printf("web: sign-in by %s (sub %s) refused: not in the allowed list", claims.who(), claims.Sub)
		if f.test {
			w.endTrial("notallowed", claims, "")
			w.moveOn(rw, r, "/settings/account?sso=notallowed")
			return
		}
		w.refuse(rw, r, si)
		return
	}
	if f.test {
		// The owner is who they say: their session counts as signed in by
		// the provider, so that the change of mode keeps them in.
		w.c.store.db.Exec(`UPDATE web_sessions SET via = ? WHERE id = ?`, viaOIDC, f.session)
		w.endTrial("ok", claims, signinPrint(a, secret))
		w.moveOn(rw, r, "/settings/account?sso=ok")
		return
	}
	if _, err := w.newSession(rw, viaOIDC); err != nil {
		w.loginFailed(rw, r, si, http.StatusInternalServerError, "err.internal")
		return
	}
	log.Printf("web: signed in with %s as %s", a.OIDC.Issuer, claims.who())
	w.moveOn(rw, r, "/")
}

// moveOn leaves the provider's redirect for a page of the workspace, with
// a refresh: then the browser sends the session cookie.
func (w *web) moveOn(rw http.ResponseWriter, r *http.Request, to string) {
	w.render(rw, r, nil, "oauth", "", page{Title: "login.title", Data: oauthDone{Next: to, Moving: true}})
}

func (w *web) endTrial(result string, c ssoClaims, print string) {
	w.sso.mu.Lock()
	defer w.sso.mu.Unlock()
	if w.sso.trial == nil {
		return
	}
	w.sso.trial.result, w.sso.trial.who, w.sso.trial.sub, w.sso.trial.print = result, c.who(), c.Sub, print
	w.sso.trial.at = w.now()
}

// trial returns the last test sign-in, while it counts.
func (w *web) trial() *ssoTrial {
	w.sso.mu.Lock()
	defer w.sso.mu.Unlock()
	t := w.sso.trial
	if t == nil || w.now().Sub(t.at) > ssoTrialTTL {
		return nil
	}
	cp := *t
	return &cp
}

// tested tells whether a test sign-in let the owner in with these settings.
func (w *web) tested(a AuthConfig, secret string) bool {
	t := w.trial()
	return t != nil && t.result == "ok" && t.print == signinPrint(a, secret)
}
