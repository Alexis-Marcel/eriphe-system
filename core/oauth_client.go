package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
	"golang.org/x/oauth2"
)

// Signing in to an account at a provider, for a local tool (a mailbox, a
// calendar, a drive): the OAuth 2.0 authorization code flow with PKCE, run
// by eriphe-core with the user's own OAuth client (their "app"). The
// refresh token it gets is kept in the vault and lent to the tool's server
// on its node, like a password: it never enters a task.
//
// Nothing here names a provider: a signInProvider says where to send the
// browser, where to exchange and refresh, how to revoke, what a client id
// looks like and which redirects it takes (signin_providers.go):
//
//	workspace → provider (PKCE, state = e1.<payload>.<mac>)
//	provider → <workspace>/oauth/callback?code&state   (in the browser)
//	workspace: checks the state's MAC, the flow and the browser's cookie,
//	           exchanges the code with the client secret and the PKCE verifier
//
// The state's payload is the workspace's origin, a random flow id and an
// expiry, signed with HMAC-SHA256 under a key only this eriphe-core has: a
// state it did not make, or one replayed, is refused here. oauth.go is the
// other OAuth of eriphe-core: remote MCP servers' own authorization, found
// by discovery.

const (
	stateVersion     = "e1"
	stateKeyName     = "cloud_state_key"
	signInFlowExpiry = 15 * time.Minute

	// redirectPublicHTTPS takes a redirect only at an https address whose
	// host is a name ending in a public top-level domain, or a loopback one.
	redirectPublicHTTPS = "public-https"
)

// signInProvider is a provider an account signs in at, as data.
type signInProvider struct {
	Name     string // its id: "google"
	Label    string // its name in errors: "Google"
	Endpoint oauth2.Endpoint
	// RevokeURL forgets a token (RFC 7009); "" when the provider has none.
	RevokeURL string
	// Scopes are asked at every sign-in, for the identity: "openid email".
	Scopes []string
	// AuthParams are added to the authorization URL, beside
	// access_type=offline and PKCE.
	AuthParams map[string]string
	// ClientIDSuffix ends every client id of the provider, or "".
	ClientIDSuffix string
	// Redirect is the rule its redirect URIs follow: redirectPublicHTTPS,
	// or "" for any.
	Redirect string
	// ForgetAt is where the user removes the app's access, when the
	// provider gives no new refresh token while it still has one.
	ForgetAt string
}

// signInHTTP talks to the providers; tests replace it.
var signInHTTP = &http.Client{Timeout: 30 * time.Second}

// oauthApp is an OAuth client: the user's own app at a provider.
type oauthApp struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret"`
}

// checkClient checks a client id and secret as typed.
func (p *signInProvider) checkClient(id, secret string) (oauthApp, error) {
	app := oauthApp{ID: strings.TrimSpace(id), Secret: strings.TrimSpace(secret)}
	if app.ID == "" || app.Secret == "" {
		return oauthApp{}, &catalogError{http.StatusBadRequest, "drive.ownmissing", "", errors.New("the client id and secret are required")}
	}
	if !strings.HasSuffix(app.ID, p.ClientIDSuffix) || strings.ContainsAny(app.ID+app.Secret, " \t\r\n\"'<>") || len(app.ID)+len(app.Secret) > 512 {
		return oauthApp{}, &catalogError{http.StatusBadRequest, "drive.badclient", "", errors.New("not a " + p.Label + " client id and secret")}
	}
	return app, nil
}

// parseClient reads a client JSON as a console downloads it:
// {"web": {"client_id", "client_secret", "redirect_uris", …}} (the flat
// {"client_id", "client_secret"} also works). A desktop client
// ({"installed": …}) is refused: it takes only loopback redirects, which a
// workspace cannot use. It also returns the redirect URIs the file lists,
// if any.
func (p *signInProvider) parseClient(raw []byte) (oauthApp, []string, error) {
	type client struct {
		oauthApp
		RedirectURIs []string `json:"redirect_uris"`
	}
	var doc struct {
		Web       *client `json:"web"`
		Installed *client `json:"installed"`
		client
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return oauthApp{}, nil, &catalogError{http.StatusBadRequest, "drive.badjson", "", errors.New("not a client JSON")}
	}
	if doc.Installed != nil {
		return oauthApp{}, nil, &catalogError{http.StatusBadRequest, "drive.desktop", "", errors.New("a Desktop client: create a Web application client")}
	}
	c := doc.client
	if doc.Web != nil {
		c = *doc.Web
	}
	app, err := p.checkClient(c.ID, c.Secret)
	return app, c.RedirectURIs, err
}

// refuses says why the provider would refuse <origin>/oauth/callback as a
// redirect URI, or "" if it takes it. For redirectPublicHTTPS
// (developers.google.com/identity/protocols/oauth2/web-server, "Redirect
// URI validation rules"): "ip" for a raw IP address other than a loopback
// one, "local" for a name whose top-level domain is not a public one
// (eriphe.local, nas.lan, a single label), "http" for plain http except to
// localhost.
func (p *signInProvider) refuses(origin string) string {
	if p.Redirect != redirectPublicHTTPS {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return "local"
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsLoopback() {
			return ""
		}
		return "ip"
	}
	if host == "localhost" {
		return ""
	}
	labels := strings.Split(host, ".")
	tld := labels[len(labels)-1]
	if suffix, icann := publicsuffix.PublicSuffix(tld); len(labels) < 2 || !icann || suffix != tld ||
		host == "home.arpa" || strings.HasSuffix(host, ".home.arpa") {
		return "local"
	}
	if u.Scheme != "https" {
		return "http"
	}
	return ""
}

// The state.

type statePayload struct {
	Origin  string `json:"w"` // the workspace, scheme://host[:port]
	Nonce   string `json:"n"` // the flow
	Expires int64  `json:"x"` // unix seconds
}

// stateKey is this installation's key for the states, made once.
func (c *core) stateKey() []byte {
	c.signIns.mu.Lock()
	defer c.signIns.mu.Unlock()
	if k, err := hex.DecodeString(c.store.get(stateKeyName)); err == nil && len(k) == 32 {
		return k
	}
	k := make([]byte, 32)
	rand.Read(k)
	c.store.set(stateKeyName, hex.EncodeToString(k))
	return k
}

func signState(key []byte, p statePayload) string {
	raw, _ := json.Marshal(p)
	body := stateVersion + "." + base64.RawURLEncoding.EncodeToString(raw)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

var errBadState = errors.New("this authorization is not valid any more: connect again")

// openState checks a state's MAC and expiry and reads it.
func openState(key []byte, s string, now time.Time) (statePayload, error) {
	parts := strings.Split(s, ".")
	if len(s) > 1024 || len(parts) != 3 || parts[0] != stateVersion {
		return statePayload{}, errBadState
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	m := hmac.New(sha256.New, key)
	m.Write([]byte(parts[0] + "." + parts[1]))
	if err != nil || !hmac.Equal(mac, m.Sum(nil)) {
		return statePayload{}, errBadState
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var p statePayload
	if json.Unmarshal(raw, &p) != nil || now.Unix() > p.Expires {
		return statePayload{}, errBadState
	}
	return p, nil
}

// isSignInState tells a state of ours from a state of the go-sdk's flows.
func isSignInState(s string) bool { return strings.HasPrefix(s, stateVersion+".") }

// The flows.

type signInFlow struct {
	provider *signInProvider
	origin   string
	verifier string
	binding  string // hash of the browser's flow cookie
	config   *oauth2.Config
	lang     string
	expires  time.Time
	asked    []string // what the caller asked for, handed back at the end
	back     string   // the page the browser goes back to
}

type signInFlows struct {
	mu    sync.Mutex
	flows map[string]*signInFlow
}

func (f *signInFlows) put(id string, fl *signInFlow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flows == nil {
		f.flows = map[string]*signInFlow{}
	}
	for k, old := range f.flows {
		if time.Now().After(old.expires) {
			delete(f.flows, k)
		}
	}
	f.flows[id] = fl
}

// take hands a flow out once.
func (f *signInFlows) take(id string) *signInFlow {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flows[id]
	delete(f.flows, id)
	if fl == nil || time.Now().After(fl.expires) {
		return nil
	}
	return fl
}

// signInStart is a sign-in asked for.
type signInStart struct {
	Origin  string   // the workspace, as the browser reaches it
	Binding string   // hash of the browser's flow cookie
	Scopes  []string // beside the provider's own
	Hint    string   // the account expected, if known
	Lang    string
	Back    string   // the page the browser goes back to
	Asked   []string // kept with the flow for the caller
}

// startSignIn starts an authorization with the user's app and returns
// where to send the browser.
func (c *core) startSignIn(p *signInProvider, app oauthApp, in signInStart) (string, error) {
	if why := p.refuses(in.Origin); why != "" {
		return "", &catalogError{http.StatusBadRequest, "drive.origin." + why, in.Origin, errors.New(p.Label + " takes no redirect to " + in.Origin)}
	}
	cfg := &oauth2.Config{ClientID: app.ID, ClientSecret: app.Secret, Endpoint: p.Endpoint,
		RedirectURL: in.Origin + "/oauth/callback", Scopes: append(append([]string{}, p.Scopes...), in.Scopes...)}
	nonce := randomToken(18)
	fl := &signInFlow{provider: p, origin: in.Origin, verifier: oauth2.GenerateVerifier(), binding: in.Binding, config: cfg,
		lang: in.Lang, expires: time.Now().Add(signInFlowExpiry), asked: in.Asked, back: in.Back}
	c.signIns.put(nonce, fl)
	state := signState(c.stateKey(), statePayload{Origin: in.Origin, Nonce: nonce, Expires: fl.expires.Unix()})
	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(fl.verifier)}
	for k, v := range p.AuthParams {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}
	if in.Hint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", in.Hint))
	}
	return cfg.AuthCodeURL(state, opts...), nil
}

// signedIn is what a sign-in gave.
type signedIn struct {
	Identity string   // the account's address, from the ID token
	Scopes   []string // all the account granted
	Refresh  string
}

// secret is the token as the vault keeps it and a tool's server reads it,
// with where to refresh it.
func (s signedIn) secret(fl *signInFlow) string {
	raw, _ := json.Marshal(map[string]string{"client_id": fl.config.ClientID, "client_secret": fl.config.ClientSecret,
		"refresh_token": s.Refresh, "token_url": fl.config.Endpoint.TokenURL})
	return string(raw)
}

// finishSignIn takes the provider's answer, as the workspace's callback
// received it: it checks the state, the flow and the browser, then
// exchanges the code. The flow is returned as soon as it is found, even
// with an error, for where to go back.
func (c *core) finishSignIn(ctx context.Context, origin, state, code, errCode, binding string) (*signInFlow, signedIn, error) {
	ps, err := openState(c.stateKey(), state, time.Now())
	if err != nil {
		return nil, signedIn{}, err
	}
	fl := c.signIns.take(ps.Nonce)
	if fl == nil || fl.origin != ps.Origin || ps.Origin != origin ||
		subtle.ConstantTimeCompare([]byte(fl.binding), []byte(binding)) != 1 {
		return nil, signedIn{}, errBadState
	}
	p := fl.provider
	if errCode != "" {
		if errCode == "access_denied" {
			return fl, signedIn{}, &catalogError{http.StatusBadRequest, "drive.denied", "", errors.New("access denied")}
		}
		return fl, signedIn{}, fmt.Errorf("%s refused: %s", p.Label, errCode)
	}
	if code == "" {
		return fl, signedIn{}, errors.New("no code in the redirect")
	}
	tok, err := fl.config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, signInHTTP), code, oauth2.VerifierOption(fl.verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return fl, signedIn{}, fmt.Errorf("%s refused the code: %s %s", p.Label, re.ErrorCode, re.ErrorDescription)
		}
		return fl, signedIn{}, fmt.Errorf("exchanging the code: %w", err)
	}
	if tok.RefreshToken == "" {
		return fl, signedIn{}, fmt.Errorf("%s gave no refresh token: remove Eriphe's access at %s, then connect again", p.Label, p.ForgetAt)
	}
	email := idTokenEmail(fmt.Sprint(tok.Extra("id_token")))
	if email == "" {
		return fl, signedIn{}, &catalogError{http.StatusBadRequest, "google.noemail", "", errors.New(p.Label + " did not say which account was connected")}
	}
	return fl, signedIn{Identity: email, Scopes: strings.Fields(fmt.Sprint(tok.Extra("scope"))), Refresh: tok.RefreshToken}, nil
}

// idTokenEmail reads the address in an ID token. It came straight from the
// provider's token endpoint over TLS, which OpenID Connect takes in place
// of checking its signature (Core 1.0, 3.1.3.7).
func idTokenEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email    string `json:"email"`
		Verified *bool  `json:"email_verified"`
	}
	if json.Unmarshal(raw, &claims) != nil || (claims.Verified != nil && !*claims.Verified) {
		return ""
	}
	e := strings.TrimSpace(claims.Email)
	if !strings.Contains(e, "@") || len(e) > 254 || strings.ContainsAny(e, " \t\r\n\"<>") {
		return ""
	}
	return e
}

// tokenCall posts to the provider's token endpoint: the error code and its
// description when it refuses, else the access token.
func (p *signInProvider) tokenCall(ctx context.Context, hc *http.Client, form url.Values) (code, desc, access string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
		Access      string `json:"access_token"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body) != nil && resp.StatusCode != http.StatusOK {
		return "http " + strconv.Itoa(resp.StatusCode), "", "", nil
	}
	if body.Error == "" && resp.StatusCode != http.StatusOK {
		body.Error = "http " + strconv.Itoa(resp.StatusCode)
	}
	return body.Error, body.Description, body.Access, nil
}

// lentToken is a token kept in the vault, as secret makes it.
type lentToken struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	TokenURL     string `json:"token_url,omitempty"`
}

// What a refresh says of a token.
const (
	refreshWorks      = iota
	refreshRevoked    // revoked, or expired
	refreshBadClient  // the app it was given to is refused
	refreshUnreadable // no token to try
)

// checkRefresh asks the provider for an access token with a kept token. An
// error means the provider could not be asked: nothing is known.
func (p *signInProvider) checkRefresh(ctx context.Context, raw string) (int, error) {
	var tok lentToken
	if json.Unmarshal([]byte(raw), &tok) != nil || tok.RefreshToken == "" {
		return refreshUnreadable, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	code, _, _, err := p.tokenCall(ctx, signInHTTP, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken},
		"client_id": {tok.ClientID}, "client_secret": {tok.ClientSecret}})
	switch {
	case err != nil:
		return 0, err
	case code == "invalid_grant":
		return refreshRevoked, nil
	case code == "invalid_client" || code == "unauthorized_client" || code == "deleted_client":
		return refreshBadClient, nil
	}
	return refreshWorks, nil
}

// revoke asks the provider to forget a kept token; a failure, or a
// provider with no revocation, leaves it to the user.
func (p *signInProvider) revoke(ctx context.Context, raw string) {
	var tok lentToken
	if p.RevokeURL == "" || json.Unmarshal([]byte(raw), &tok) != nil || tok.RefreshToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.RevokeURL, strings.NewReader(url.Values{"token": {tok.RefreshToken}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := signInHTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}
