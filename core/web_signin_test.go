package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIssuer is an OpenID Connect provider for the tests: discovery, an
// authorization endpoint that signs in at once, a token endpoint that
// checks PKCE, and RS256 ID tokens. What it puts in the token can be
// changed, to try the workspace's checks.
type fakeIssuer struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu       sync.Mutex
	codes    map[string]fakeCode
	sub      string
	email    string
	verified bool
	aud      string // instead of the client id
	nonce    string // instead of the flow's
}

type fakeCode struct{ nonce, challenge, client string }

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{t: t, key: key, codes: map[string]fakeCode{}, sub: "owner-sub", email: "owner@example.org", verified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(rw http.ResponseWriter, r *http.Request) {
		json.NewEncoder(rw).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(rw http.ResponseWriter, r *http.Request) {
		json.NewEncoder(rw).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("GET /authorize", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
			http.Error(rw, "PKCE, state and nonce are required", http.StatusBadRequest)
			return
		}
		code := randomToken(12)
		f.mu.Lock()
		f.codes[code] = fakeCode{q.Get("nonce"), q.Get("code_challenge"), q.Get("client_id")}
		f.mu.Unlock()
		http.Redirect(rw, r, q.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(rw http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		c, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || b64(sum[:]) != c.challenge {
			http.Error(rw, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		aud, nonce := c.client, c.nonce
		if f.aud != "" {
			aud = f.aud
		}
		if f.nonce != "" {
			nonce = f.nonce
		}
		claims := map[string]any{"iss": f.srv.URL, "sub": f.sub, "aud": aud, "nonce": nonce, "email": f.email,
			"email_verified": f.verified, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
		f.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300, "id_token": f.sign(claims)})
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *fakeIssuer) sign(claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return in + "." + b64(sig)
}

// signIn follows the provider's address and returns the workspace's
// callback, path and query, as the browser would be sent back to it.
func (f *fakeIssuer) signIn(authURL string) string {
	f.t.Helper()
	c := f.srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(authURL)
	if err != nil {
		f.t.Fatal(err)
	}
	resp.Body.Close()
	u, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || err != nil {
		f.t.Fatalf("authorize: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	return u.RequestURI()
}

// oidcWeb is a workspace with an owner and the fake provider's settings,
// in the given mode.
func oidcWeb(t *testing.T, mode string, edit func(*AuthConfig)) (*testWeb, *fakeIssuer) {
	t.Helper()
	tw := newTestWeb(t)
	tw.setup()
	f := newFakeIssuer(t)
	tw.w.sso.client = f.srv.Client()
	a := AuthConfig{Mode: mode, ExternalURL: tw.srv.URL,
		OIDC: OIDCConfig{Issuer: f.srv.URL, ClientID: "eriphe", AllowedSubs: []string{"owner-sub"}}}
	if edit != nil {
		edit(&a)
	}
	if err := tw.w.saveSignin(a); err != nil {
		t.Fatal(err)
	}
	return tw, f
}

func (tw *testWeb) signOut() {
	tw.hc.Jar, _ = cookiejar.New(nil)
}

func TestSigninCheck(t *testing.T) {
	for _, tc := range []struct {
		a   AuthConfig
		key string
	}{
		{AuthConfig{}, ""},
		{AuthConfig{Mode: "magic"}, "mode"},
		{AuthConfig{Mode: modeOIDC, ExternalURL: "https://eriphe.example.org", OIDC: OIDCConfig{Issuer: "https://auth.example.org/", ClientID: "c"}}, "allowlist"},
		{AuthConfig{Mode: modeOIDC, OIDC: OIDCConfig{Issuer: "https://auth.example.org/", ClientID: "c", AllowedSubs: []string{"x"}}}, "oidcurl"},
		{AuthConfig{Mode: modeOIDC, ExternalURL: "https://eriphe.example.org", OIDC: OIDCConfig{AllowedSubs: []string{"x"}}}, "oidcfields"},
		{AuthConfig{ExternalURL: "http://eriphe.example.org"}, "externalurl"},
		{AuthConfig{ExternalURL: "https://eriphe.example.org/eriphe"}, "externalurl"},
		{AuthConfig{UnauthorizedURL: "www.example.org"}, "unauthorizedurl"},
		{AuthConfig{Mode: modeProxy, Proxy: ProxyConfig{Users: []string{"me"}}}, "notrusted"},
		{AuthConfig{Mode: modeProxy, Proxy: ProxyConfig{Trusted: []string{"10.0.0.0/8"}}}, "nousers"},
		{AuthConfig{Proxy: ProxyConfig{Trusted: []string{"10.0.0.300"}}}, "trusted"},
		{AuthConfig{Proxy: ProxyConfig{Header: "Remote User"}}, "header"},
		{AuthConfig{Mode: modeProxy, Proxy: ProxyConfig{Trusted: []string{"10.0.0.2", "fd00::/8"}, Users: []string{"me"}}}, ""},
		{AuthConfig{Mode: modeNone}, ""},
	} {
		err := tc.a.check()
		var ae authErr
		if tc.key == "" && err != nil || tc.key != "" && (!errorsAs(err, &ae) || ae.key != tc.key) {
			t.Errorf("%+v: %v, want %q", tc.a, err, tc.key)
		}
	}
	// The configuration file's errors stop eriphe-core at start.
	tw := newTestWeb(t)
	if _, err := newWeb(tw.c, WebConfig{Auth: &AuthConfig{Mode: modeOIDC}}); err == nil {
		t.Fatal("an incomplete web.auth was accepted")
	}
}

func errorsAs(err error, ae *authErr) bool {
	e, ok := err.(authErr)
	if ok {
		*ae = e
	}
	return ok
}

// The provider's whole sign-in: to the provider at once, back with a
// code, the ID token checked, a session opened.
func TestOIDCSignIn(t *testing.T) {
	tw, f := oidcWeb(t, modeOIDC, func(a *AuthConfig) { a.NoLocalPassword = true })
	tw.signOut()
	if code, _, h := tw.do("GET", "/settings/account", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Fatalf("signed out: %d %s", code, h.Get("Location"))
	}
	if code, _, h := tw.do("GET", "/login", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/auth/login" {
		t.Fatalf("login page: %d %s, want the provider at once", code, h.Get("Location"))
	}
	code, _, h := tw.do("GET", "/auth/login", nil, nil)
	auth, _ := url.Parse(h.Get("Location"))
	q := auth.Query()
	if code != http.StatusFound || !strings.HasPrefix(h.Get("Location"), f.srv.URL+"/authorize") ||
		q.Get("redirect_uri") != tw.srv.URL+"/auth/callback" || q.Get("code_challenge_method") != "S256" ||
		q.Get("nonce") == "" || q.Get("state") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("to the provider: %d %s", code, h.Get("Location"))
	}
	back := f.signIn(h.Get("Location"))
	code, body, _ := tw.do("GET", back, nil, nil)
	if code != http.StatusOK || !strings.Contains(body, `url=/"`) {
		t.Fatalf("callback: %d %s", code, body)
	}
	if code, _, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusOK {
		t.Fatalf("after the sign-in: %d", code)
	}
	// A state is good once.
	if code, _, _ := tw.do("GET", back, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("the same callback again: %d", code)
	}
	// Signing out waits for a click, or the provider would sign in again.
	csrf := tw.sessionToken()
	if _, _, h := tw.post("/logout", nil, csrf); h.Get("HX-Redirect") != "/login?out=1" {
		t.Fatalf("logout: %v", h)
	}
	if code, body, _ := tw.do("GET", "/login?out=1", nil, nil); code != http.StatusOK || !strings.Contains(body, `href="/auth/login"`) || strings.Contains(body, `name="password"`) {
		t.Fatalf("after logout: %d", code)
	}
}

// What the callback refuses: a state of no flow, another browser's flow,
// an ID token for another client, or with another nonce.
func TestOIDCCallbackChecks(t *testing.T) {
	tw, f := oidcWeb(t, modeOIDC, func(a *AuthConfig) { a.NoLocalPassword = true })
	tw.signOut()
	start := func() string {
		_, _, h := tw.do("GET", "/auth/login", nil, nil)
		return f.signIn(h.Get("Location"))
	}
	inside := func() bool {
		code, _, _ := tw.do("GET", "/settings/account", nil, nil)
		return code == http.StatusOK
	}
	back := start()
	u, _ := url.Parse(back)
	q := u.Query()
	q.Set("state", "forged")
	if code, _, _ := tw.do("GET", "/auth/callback?"+q.Encode(), nil, nil); code != http.StatusBadRequest || inside() {
		t.Fatalf("bad state: %d", code)
	}
	// Another browser's flow (no binding cookie).
	back = start()
	other := &http.Client{Transport: tw.srv.Client().Transport, CheckRedirect: tw.hc.CheckRedirect}
	resp, _ := other.Get(tw.srv.URL + back)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("another browser's callback: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// The provider's error.
	u, _ = url.Parse(start())
	if code, _, _ := tw.do("GET", "/auth/callback?error=access_denied&state="+url.QueryEscape(u.Query().Get("state")), nil, nil); code != http.StatusBadRequest || inside() {
		t.Fatalf("error: %d", code)
	}
	f.aud = "another-app"
	if code, _, _ := tw.do("GET", start(), nil, nil); code != http.StatusBadRequest || inside() {
		t.Fatalf("token for another client: %d", code)
	}
	f.aud, f.nonce = "", "replayed"
	if code, _, _ := tw.do("GET", start(), nil, nil); code != http.StatusBadRequest || inside() {
		t.Fatalf("token with another nonce: %d", code)
	}
	f.nonce = ""
	if code, _, _ := tw.do("GET", start(), nil, nil); code != http.StatusOK || !inside() {
		t.Fatalf("the right one: %d", code)
	}
}

// An account the provider signs in, but not the owner: the unauthorised
// address, or the private page.
func TestOIDCNotAllowed(t *testing.T) {
	tw, f := oidcWeb(t, modeOIDC, func(a *AuthConfig) {
		a.NoLocalPassword = true
		a.OIDC.AllowedEmails = []string{"owner@example.org"}
	})
	tw.signOut()
	f.sub, f.email = "someone", "someone@example.org"
	_, _, h := tw.do("GET", "/auth/login", nil, nil)
	code, body, _ := tw.do("GET", f.signIn(h.Get("Location")), nil, nil)
	if code != http.StatusForbidden || !strings.Contains(body, "Cet espace est privé") {
		t.Fatalf("not allowed: %d", code)
	}
	// An allowed email counts only when verified.
	f.email, f.verified = "owner@example.org", false
	_, _, h = tw.do("GET", "/auth/login", nil, nil)
	if code, _, _ := tw.do("GET", f.signIn(h.Get("Location")), nil, nil); code != http.StatusForbidden {
		t.Fatalf("unverified email: %d", code)
	}
	a := tw.w.signin().cfg
	a.UnauthorizedURL = "https://www.example.org"
	tw.w.saveSignin(a)
	f.email = "someone@example.org"
	_, _, h = tw.do("GET", "/auth/login", nil, nil)
	if code, _, h := tw.do("GET", f.signIn(h.Get("Location")), nil, nil); code != http.StatusSeeOther || h.Get("Location") != "https://www.example.org" {
		t.Fatalf("not allowed, with an address: %d %s", code, h.Get("Location"))
	}
	if code, _, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusSeeOther {
		t.Fatalf("a refused account got in: %d", code)
	}
	f.verified = true
	f.email = "owner@example.org"
	_, _, h = tw.do("GET", "/auth/login", nil, nil)
	if code, _, _ := tw.do("GET", f.signIn(h.Get("Location")), nil, nil); code != http.StatusOK {
		t.Fatalf("verified email: %d", code)
	}
}

// from sends one request to the workspace from an address, with cookies.
func from(h http.Handler, method, path, addr string, header map[string]string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, "https://eriphe.example.org"+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, "https://eriphe.example.org"+path, nil)
	}
	req.RemoteAddr = addr
	for k, v := range header {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// The proxy's header is believed from the declared proxy only, and for
// the users listed; the password stays from the local network only.
func TestProxyMode(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.w.saveSignin(AuthConfig{Mode: modeProxy, Proxy: ProxyConfig{Trusted: []string{"10.0.0.2"}, Users: []string{"owner"}}})
	h := tw.w.routes()
	const proxy, lan, internet = "10.0.0.2:41000", "192.168.1.20:5000", "203.0.113.9:5000"
	user := map[string]string{"Remote-User": "owner", "X-Forwarded-For": "203.0.113.9"}

	rec := from(h, "GET", "/settings/account", proxy, user, nil)
	if rec.Code != http.StatusOK || cookie(rec, sessionCookie) == nil {
		t.Fatalf("through the proxy: %d", rec.Code)
	}
	if rec := from(h, "GET", "/settings/account", proxy, map[string]string{"Remote-User": "intruder"}, nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "privé") {
		t.Fatalf("another user through the proxy: %d", rec.Code)
	}
	if rec := from(h, "GET", "/settings/account", proxy, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("through the proxy without a user: %d", rec.Code)
	}
	if rec := from(h, "GET", "/settings/account", internet, user, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("the header from elsewhere: %d", rec.Code)
	}
	// A proxy session needs the header on every request.
	if rec := from(h, "GET", "/settings/account", internet, nil, nil, cookie(rec, sessionCookie)); rec.Code != http.StatusForbidden {
		t.Fatalf("a proxy session without the proxy: %d", rec.Code)
	}
	// From the local network, without a proxy: the password.
	if rec := from(h, "GET", "/settings/account", lan, nil, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("from the local network: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	login := func(addr string, header map[string]string) *httptest.ResponseRecorder {
		page := from(h, "GET", "/login", addr, header, nil)
		pre := cookie(page, preCookie)
		m := csrfField.FindStringSubmatch(page.Body.String())
		if pre == nil || m == nil {
			return page
		}
		return from(h, "POST", "/login", addr, header, url.Values{"csrf": {m[1]}, "password": {testPassword}}, pre)
	}
	rec = login(lan, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("password from the local network: %d", rec.Code)
	}
	if rec := from(h, "GET", "/settings/account", internet, nil, nil, cookie(rec, sessionCookie)); rec.Code != http.StatusOK {
		t.Fatalf("a password session: %d", rec.Code)
	}
	for name, addr := range map[string]string{"internet": internet, "proxy": proxy} {
		if rec := login(addr, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("password from the %s: %d", name, rec.Code)
		}
	}
	// A local address behind an undeclared proxy that saw the internet.
	if rec := login(lan, map[string]string{"X-Forwarded-For": "203.0.113.9"}); rec.Code != http.StatusForbidden {
		t.Fatalf("password forwarded from the internet: %d", rec.Code)
	}
	// Turned off: no password at all, and the old password sessions end.
	tw.w.saveSignin(AuthConfig{Mode: modeProxy, NoLocalPassword: true, UnauthorizedURL: "https://www.example.org",
		Proxy: ProxyConfig{Trusted: []string{"10.0.0.0/24"}, Users: []string{"owner"}}})
	if rec := from(h, "GET", "/settings/account", lan, nil, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "https://www.example.org" {
		t.Fatalf("local network, password off: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestFromLocalNetwork(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:1": true, "192.168.1.2:1": true, "100.92.1.2:1": true, "[fd7a:115c:a1e0::1]:1": true, "[::1]:1": true,
		"8.8.8.8:1": false, "[2001:db8::1]:1": false, "garbage": false,
	} {
		if got := fromLocalNetwork(&http.Request{RemoteAddr: addr, Header: http.Header{}}); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
	r := &http.Request{RemoteAddr: "10.0.0.2:1", Header: http.Header{"X-Forwarded-For": {"192.168.1.5, 203.0.113.9"}}}
	if fromLocalNetwork(r) {
		t.Error("a public hop counts as local")
	}
	r.Header = http.Header{"Forwarded": {"for=192.168.1.5"}}
	if fromLocalNetwork(r) {
		t.Error("Forwarded is not read, so not local")
	}
}

// With the provider, the password still works from the local network,
// unless turned off; from elsewhere it does not.
func TestOIDCPasswordFallback(t *testing.T) {
	tw, _ := oidcWeb(t, modeOIDC, nil)
	tw.signOut()
	code, body, _ := tw.do("GET", "/login", nil, nil)
	if code != http.StatusOK || !strings.Contains(body, `href="/auth/login"`) || !strings.Contains(body, `name="password"`) {
		t.Fatalf("login page from the local network: %d", code)
	}
	tok := csrfField.FindStringSubmatch(body)[1]
	if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}},
		map[string]string{"X-Forwarded-For": "203.0.113.9"}); code != http.StatusForbidden {
		t.Fatalf("password from the internet: %d", code)
	}
	if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, nil); code != http.StatusSeeOther {
		t.Fatalf("password from the local network: %d", code)
	}
	if code, _, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusOK {
		t.Fatalf("password session: %d", code)
	}
	// Turned off, the password session ends too.
	a := tw.w.signin().cfg
	a.NoLocalPassword = true
	tw.w.saveSignin(a)
	if code, _, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusSeeOther {
		t.Fatalf("password session with the password off: %d", code)
	}
	if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, nil); code != http.StatusForbidden {
		t.Fatalf("password turned off: %d", code)
	}
}

func TestNoSignIn(t *testing.T) {
	tw := newTestWeb(t)
	tw.w.saveSignin(AuthConfig{Mode: modeNone})
	csrf := tw.sessionToken() // a session, without a password
	if code, _, _ := tw.post("/lang", url.Values{"lang": {"en"}}, csrf); code >= 400 {
		t.Fatalf("a form without sign-in: %d", code)
	}
	if code, _, _ := tw.post("/lang", url.Values{"lang": {"en"}}, "forged"); code != http.StatusForbidden {
		t.Fatalf("CSRF without sign-in: %d", code)
	}
	if code, _, h := tw.do("GET", "/login", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("login page: %d", code)
	}
	// Back to the password: the sessions opened by nobody end.
	tw.w.saveSignin(AuthConfig{})
	if code, _, h := tw.do("GET", "/settings/account", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/setup" {
		t.Fatalf("after none: %d %s", code, h.Get("Location"))
	}
}

func TestAuthReset(t *testing.T) {
	tw, _ := oidcWeb(t, modeOIDC, func(a *AuthConfig) { a.NoLocalPassword = true })
	tw.signOut()
	if code, _, h := tw.do("GET", "/login", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/auth/login" {
		t.Fatalf("before the reset: %d", code)
	}
	was, err := resetSignin(tw.c.store)
	if err != nil || was != modeOIDC {
		t.Fatalf("reset: %q %v", was, err)
	}
	si := tw.w.signin()
	if si.cfg.mode() != modePassword || si.cfg.OIDC.Issuer == "" {
		t.Fatalf("after the reset: %+v (the provider's settings stay)", si.cfg)
	}
	tok := tw.formToken("/login")
	if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, map[string]string{"X-Forwarded-For": "203.0.113.9"}); code != http.StatusSeeOther {
		t.Fatalf("password after the reset: %d", code)
	}
}

// The settings: the secret kept in the vault and never shown back; the
// provider's mode only after a test sign-in; nothing that locks out.
func TestSigninSettings(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := vaultKey(filepath.Join(dir, "vault.key"))
	v, err := openVault(store, key)
	if err != nil {
		t.Fatal(err)
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	cfg := &Config{User: "Sam", Listen: "127.0.0.1:8800"}
	tw := newTestWebFor(t, newCore(cfg, store))
	csrf := tw.setup()
	f := newFakeIssuer(t)
	tw.w.sso.client = f.srv.Client()
	form := url.Values{"mode": {modeOIDC}, "external_url": {tw.srv.URL}, "local_password": {"1"},
		"oidc_issuer": {f.srv.URL}, "oidc_client_id": {"eriphe"}, "oidc_secret": {"client-secret-value"}, "oidc_subs": {"owner-sub"}}

	if _, body, _ := tw.post("/settings/signin/issuer", form, csrf); !strings.Contains(body, f.srv.URL+"/authorize") {
		t.Fatalf("issuer test: %s", body)
	}
	if _, body, _ := tw.post("/settings/signin", form, csrf); !strings.Contains(body, "abord la connexion") || tw.w.signin().cfg.mode() != modePassword {
		t.Fatalf("oidc without a test: %s", body)
	}
	// The test sign-in, as someone else first.
	f.sub = "someone"
	_, _, h := tw.post("/settings/signin/try", form, csrf)
	if code, body, _ := tw.do("GET", f.signIn(h.Get("HX-Redirect")), nil, nil); code != http.StatusOK || !strings.Contains(body, "sso=notallowed") {
		t.Fatalf("test sign-in, not allowed: %d %s", code, body)
	}
	if _, body, _ := tw.do("GET", "/settings/account?sso=notallowed", nil, nil); !strings.Contains(body, "someone") {
		t.Fatal("the test sign-in does not say who it was")
	}
	if _, body, _ := tw.post("/settings/signin", form, csrf); !strings.Contains(body, "abord la connexion") {
		t.Fatalf("oidc after a refused test: %s", body)
	}
	f.sub = "owner-sub"
	_, _, h = tw.post("/settings/signin/try", form, csrf)
	if code, body, _ := tw.do("GET", f.signIn(h.Get("HX-Redirect")), nil, nil); code != http.StatusOK || !strings.Contains(body, "sso=ok") {
		t.Fatalf("test sign-in: %d %s", code, body)
	}
	_, body, _ := tw.do("GET", "/settings/account?sso=ok", nil, nil)
	if strings.Contains(body, "client-secret-value") || !strings.Contains(body, "réussie") {
		t.Fatal("the account page shows the secret, or not the test")
	}
	// Saved without typing the secret again: the test's is kept.
	delete(form, "oidc_secret")
	if _, body, _ := tw.post("/settings/signin", form, csrf); !strings.Contains(body, "enregistrée") {
		t.Fatalf("save after the test: %s", body)
	}
	si := tw.w.signin()
	if si.cfg.mode() != modeOIDC || si.cfg.OIDC.ClientSecretFile != "vault:"+oidcSecretName {
		t.Fatalf("saved: %+v", si.cfg)
	}
	if got, _ := v.get(oidcSecretName); got != "client-secret-value" {
		t.Fatalf("vault: %q", got)
	}
	if raw := tw.c.store.get(authKey); strings.Contains(raw, "client-secret-value") {
		t.Fatal("the secret is in the settings")
	}
	// The session that changed the mode is still in.
	if code, body, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusOK || strings.Contains(body, "client-secret-value") {
		t.Fatalf("after the switch: %d", code)
	}
	// Another allowed list needs another test.
	form.Set("oidc_subs", "someone-else")
	if _, body, _ := tw.post("/settings/signin", form, csrf); !strings.Contains(body, "abord la connexion") {
		t.Fatalf("changed allowlist without a test: %s", body)
	}
	// The proxy, from here (not the proxy) without the password: refused.
	proxy := url.Values{"mode": {modeProxy}, "proxy_trusted": {"10.9.9.9"}, "proxy_users": {"owner"}}
	if _, body, _ := tw.post("/settings/signin", proxy, csrf); !strings.Contains(body, "mettrait dehors") || tw.w.signin().cfg.mode() != modeOIDC {
		t.Fatalf("proxy that locks out: %s", body)
	}
	proxy.Set("local_password", "1")
	if _, body, _ := tw.post("/settings/signin", proxy, csrf); !strings.Contains(body, "enregistrée") || tw.w.signin().cfg.mode() != modeProxy {
		t.Fatalf("proxy with the password kept: %s", body)
	}
	if code, _, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusOK {
		t.Fatalf("after the switch to the proxy: %d", code)
	}
	// Back to the password: always.
	if _, body, _ := tw.post("/settings/signin", url.Values{"mode": {modePassword}}, csrf); !strings.Contains(body, "enregistrée") {
		t.Fatalf("back to the password: %s", body)
	}
}

// Forms sent from the external address are the workspace's, behind a
// proxy that changes the host; other origins are not.
func TestSameOriginExternalURL(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	tw.w.saveSignin(AuthConfig{ExternalURL: "https://eriphe.example.org"})
	send := func(origin string) int {
		code, _, _ := tw.do("POST", "/lang", url.Values{"lang": {"en"}}, map[string]string{"X-CSRF-Token": csrf, "Origin": origin})
		return code
	}
	if code := send("https://eriphe.example.org"); code >= 400 {
		t.Fatalf("the external origin: %d", code)
	}
	if code := send("https://evil.example"); code != http.StatusForbidden {
		t.Fatalf("another origin: %d", code)
	}
}

// The configuration file's settings win, and the page cannot change them.
func TestSigninFromConfig(t *testing.T) {
	tw := newTestWeb(t)
	w, err := newWeb(tw.c, WebConfig{Auth: &AuthConfig{Mode: modeNone}})
	if err != nil {
		t.Fatal(err)
	}
	tw.w.fileAuth = w.fileAuth
	csrf := tw.sessionToken()
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); !strings.Contains(body, "tag-lock") {
		t.Fatal("no configuration tag")
	}
	if _, body, _ := tw.post("/settings/signin", url.Values{"mode": {modePassword}}, csrf); !strings.Contains(body, "fichier de configuration") {
		t.Fatalf("saved over the file: %s", body)
	}
	if tw.w.signin().cfg.mode() != modeNone {
		t.Fatal("the file's mode changed")
	}
}

// The command, as `docker exec eriphe-core eriphe-core auth reset` runs it.
func TestAuthResetCommand(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.set(authKey, `{"mode":"proxy","proxy":{"trusted":["10.0.0.2"],"users":["owner"]}}`)
	authCommand([]string{"reset", "-data", dir})
	var a AuthConfig
	json.Unmarshal([]byte(s.get(authKey)), &a)
	if a.mode() != modePassword || len(a.Proxy.Trusted) != 1 {
		t.Fatalf("after the command: %+v", a)
	}
}

// The page holds every mode's fields, so it saves without JavaScript; the
// CSS shows only the chosen mode's (app.css, .signin-form).
func TestSigninPageModes(t *testing.T) {
	tw, _ := oidcWeb(t, modeOIDC, nil)
	_, body, _ := tw.do("GET", "/settings/account", nil, nil)
	for _, want := range []string{`value="oidc" checked`, `class="choices if-oidc"`, `class="choices if-proxy"`,
		`class="choices if-delegated"`, `class="warn-note if-none"`, `name="oidc_issuer"`, `name="proxy_trusted"`,
		`name="unauthorized_url"`, `name="local_password"`, "Proxy qui authentifie", "(OpenID Connect)"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if n := strings.Count(body, `name="mode"`); n != 4 {
		t.Errorf("%d mode cards", n)
	}
}
