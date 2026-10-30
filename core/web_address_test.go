package main

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// forwardedReq is a request from addr, with X-Forwarded-* headers.
func forwardedReq(addr string, h map[string]string) *http.Request {
	r := httptest.NewRequest("GET", "https://192.0.2.2:8443/", nil)
	r.RemoteAddr = addr + ":40000"
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return r
}

// The forwarded headers are believed from a trusted proxy only.
func TestForwardedTrustedOnly(t *testing.T) {
	si, err := newSignin(AuthConfig{Proxy: ProxyConfig{Trusted: []string{"172.18.0.0/16", "192.0.2.9"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	fwd := map[string]string{"X-Forwarded-For": "203.0.113.7, 172.18.0.3", "X-Forwarded-Proto": "https",
		"X-Forwarded-Host": "eriphe.example.org"}
	for _, c := range []struct {
		from, ip, origin string
		h                map[string]string
	}{
		// Straight from a browser: the headers are its own, not believed.
		{"198.51.100.4", "198.51.100.4", "https://192.0.2.2:8443", fwd},
		// From the proxy: the last address that is not a trusted proxy.
		{"172.18.0.2", "203.0.113.7", "https://eriphe.example.org", fwd},
		{"192.0.2.9", "203.0.113.7", "https://eriphe.example.org", fwd},
		// Every hop a proxy: the first one.
		{"172.18.0.2", "172.18.0.4", "https://192.0.2.2:8443", map[string]string{"X-Forwarded-For": "172.18.0.4,172.18.0.3"}},
		// No header: the proxy itself.
		{"172.18.0.2", "172.18.0.2", "https://192.0.2.2:8443", nil},
		// What is not an address stops the walk; a bad host or scheme is ignored.
		{"172.18.0.2", "172.18.0.2", "https://192.0.2.2:8443", map[string]string{"X-Forwarded-For": "203.0.113.7, junk",
			"X-Forwarded-Host": "evil.example/path", "X-Forwarded-Proto": "gopher"}},
		// Plain HTTP at the proxy, a port, and the first of several values.
		{"172.18.0.2", "172.18.0.2", "http://eriphe.example.org:8080", map[string]string{"X-Forwarded-Proto": "http, https",
			"X-Forwarded-Host": "Eriphe.example.org:8080, other.example"}},
		{"172.18.0.2", "172.18.0.2", "https://eriphe.example.org", map[string]string{"X-Forwarded-Host": "eriphe.example.org:443"}},
	} {
		r := forwardedReq(c.from, c.h)
		if got := si.clientIP(r); got != c.ip {
			t.Errorf("from %s %v: client %s, want %s", c.from, c.h, got, c.ip)
		}
		if got := si.requestOrigin(r); got != c.origin {
			t.Errorf("from %s %v: origin %s, want %s", c.from, c.h, got, c.origin)
		}
	}
}

// The address: the configuration file's web.external_url, else its
// web.auth.external_url, else the workspace's; without one, the request's
// origin, or the tailnet's for the page that shows it.
func TestAddressResolution(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	r := forwardedReq("192.0.2.50", nil)
	if a := tw.w.address(); a != "" || tw.w.origin(r) != "https://192.0.2.2:8443" || tw.w.callbackURL(r) != "https://192.0.2.2:8443/oauth/callback" {
		t.Fatalf("unset: %q %q", a, tw.w.origin(r))
	}
	if tw.w.publicURL(r) != "https://192.0.2.2:8443" || tw.w.elsewhere(r) != "" {
		t.Fatal("unset: elsewhere")
	}
	tw.w.saveSignin(AuthConfig{ExternalURL: "https://eriphe.example.org"})
	if tw.w.origin(r) != "https://eriphe.example.org" || tw.w.callbackURL(r) != "https://eriphe.example.org/oauth/callback" ||
		tw.w.publicURL(r) != "https://eriphe.example.org" || tw.w.elsewhere(r) != "https://eriphe.example.org" {
		t.Fatalf("workspace's: %q %q", tw.w.origin(r), tw.w.elsewhere(r))
	}
	if v := tw.w.driveView(r); v.Callback != "https://eriphe.example.org/oauth/callback" {
		t.Fatalf("Drive's redirect URI: %s", v.Callback)
	}
	if tw.w.addressLocked() {
		t.Fatal("locked without a file")
	}

	// The configuration file's: web.auth.external_url, then web.external_url wins.
	w, err := newWeb(tw.c, WebConfig{Auth: &AuthConfig{ExternalURL: "https://auth.example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if w.address() != "https://auth.example.org" || !w.addressLocked() {
		t.Fatalf("web.auth: %q", w.address())
	}
	w, err = newWeb(tw.c, WebConfig{ExternalURL: "https://web.example.org/", Auth: &AuthConfig{ExternalURL: "https://auth.example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if w.address() != "https://web.example.org" {
		t.Fatalf("web.external_url and web.auth: %q", w.address())
	}
	w, err = newWeb(tw.c, WebConfig{ExternalURL: "https://web.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if w.address() != "https://web.example.org" || !w.addressLocked() || w.signin().locked {
		t.Fatalf("web.external_url over the workspace's: %q", w.address())
	}
	if _, err := newWeb(tw.c, WebConfig{ExternalURL: "http://web.example.org"}); err == nil {
		t.Fatal("an http address")
	}
}

// With remote access on and no address, the page shows the tailnet's, and
// forms sent from there are the workspace's.
func TestAddressTailnet(t *testing.T) {
	tw, h, _ := newRemoteWeb(t)
	h.r.enable()
	h.phase(phaseLogin)
	h.fake().set(func(f *fakeTailnet) { f.st = running(true, time.Time{}) })
	h.phase(phaseReady)
	r := forwardedReq("192.0.2.50", nil)
	if got := tw.w.publicURL(r); got != "https://"+testTSName {
		t.Fatalf("in use: %s", got)
	}
	if got := tw.w.origin(r); got != "https://192.0.2.2:8443" {
		t.Fatalf("callbacks stay on the request's origin: %s", got)
	}
	if !tw.w.ownOrigin(r, "https://"+testTSName) || tw.w.ownOrigin(r, "https://evil.example") {
		t.Fatal("tailnet origin")
	}
}

// Forms sent through a trusted proxy that rewrites the host are the
// workspace's, by its X-Forwarded-Host; the same headers from anywhere
// else do not make another site's form one.
func TestSameOriginForwarded(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	cookies := tw.hc.Jar.Cookies(mustURL(tw.srv.URL))
	handler := tw.w.routes()
	send := func(from, origin, host string) int {
		req := httptest.NewRequest("POST", "https://10.0.0.5:8443/lang", strings.NewReader("lang=en"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = from + ":50000"
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Forwarded-Host", host)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-CSRF-Token", csrf)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := send("172.18.0.2", "https://eriphe.example.org", "eriphe.example.org"); code != http.StatusForbidden {
		t.Fatalf("no trusted proxy yet: %d", code)
	}
	tw.w.saveSignin(AuthConfig{Proxy: ProxyConfig{Trusted: []string{"172.18.0.0/16"}}})
	if code := send("172.18.0.2", "https://eriphe.example.org", "eriphe.example.org"); code >= 400 {
		t.Fatalf("through the proxy: %d", code)
	}
	if code := send("192.0.2.77", "https://eriphe.example.org", "eriphe.example.org"); code != http.StatusForbidden {
		t.Fatalf("headers from a browser: %d", code)
	}
	if code := send("172.18.0.2", "https://evil.example", "eriphe.example.org"); code != http.StatusForbidden {
		t.Fatalf("another site through the proxy: %d", code)
	}
}

// Behind a trusted proxy, the login limiter counts the browser's address,
// not the proxy's: one guesser does not lock everyone out.
func TestLimiterForwarded(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.hc.Jar, _ = cookiejar.New(nil) // signed out
	tw.w.saveSignin(AuthConfig{Proxy: ProxyConfig{Trusted: []string{"127.0.0.1"}}})
	login := func(from string) int {
		tok := tw.formToken("/login")
		code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {"not the password"}},
			map[string]string{"X-Forwarded-For": from})
		return code
	}
	for range 5 {
		login("203.0.113.7")
	}
	if code := login("203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatalf("the guesser: %d", code)
	}
	if code := login("203.0.113.8"); code != http.StatusUnauthorized {
		t.Fatalf("another browser behind the proxy: %d", code)
	}
}

// The panel saves the address, the trusted proxies (the sign-in's own list)
// and the devices' second address, and checks them.
func TestAddressPanel(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/account", nil, nil)
	hasAll(t, "panel", body, "Adresse de l&#39;espace", `name="external_url"`, `name="trusted"`, `name="device_fallback"`,
		"docs/reverse-proxy.md", "En ce moment : https://127.0.0.1:")
	for _, bad := range []url.Values{{"external_url": {"http://eriphe.example.org"}}, {"external_url": {"https://eriphe.example.org/eriphe"}},
		{"trusted": {"not-an-address"}}, {"device_fallback": {"ftp://192.0.2.3"}}, {"device_fallback": {"http://127.0.0.1:8800"}}} {
		if _, body, _ := tw.post("/settings/address", bad, csrf); !strings.Contains(body, "alert") {
			t.Errorf("%v saved: %s", bad, body)
		}
	}
	form := url.Values{"external_url": {"https://eriphe.example.org/"}, "trusted": {"172.18.0.0/16\n192.0.2.9"},
		"device_fallback": {"http://100.64.0.2:8800/"}}
	if _, body, _ := tw.post("/settings/address", form, csrf); !strings.Contains(body, "Enregistré") {
		t.Fatalf("save: %s", body)
	}
	si := tw.w.signin()
	if si.cfg.ExternalURL != "https://eriphe.example.org" || len(si.trusted) != 2 || tw.w.deviceFallback() != "http://100.64.0.2:8800" {
		t.Fatalf("saved: %+v %q", si.cfg, tw.w.deviceFallback())
	}
	// The page is no longer at the address: connecting a tool says where to go.
	if _, _, h := tw.post("/settings/drive/connect", nil, csrf); !strings.Contains(h.Get("HX-Trigger"), "eriphe.example.org") {
		t.Fatalf("connect from elsewhere: %v", h)
	}
	// The provider's sign-in starts at the address.
	tw.w.saveSignin(AuthConfig{Mode: modeOIDC, ExternalURL: "https://eriphe.example.org", OIDC: OIDCConfig{Issuer: "https://auth.example.org",
		ClientID: "eriphe", AllowedSubs: []string{"x"}}})
	if code, _, h := tw.do("GET", "/auth/login", nil, nil); code != http.StatusFound || h.Get("Location") != "https://eriphe.example.org/auth/login" {
		t.Fatalf("sign-in from elsewhere: %d %v", code, h)
	}
}

// From the configuration file, the address and the proxies cannot be
// changed in the page; the devices' second address can.
func TestAddressLocked(t *testing.T) {
	tw := newTestWeb(t)
	w, err := newWeb(tw.c, WebConfig{ExternalURL: "https://eriphe.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	tw.w.cfg.ExternalURL = w.cfg.ExternalURL
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/account", nil, nil)
	hasAll(t, "locked", body, "tag-lock", "<fieldset class=\"choices\" disabled>")
	tw.post("/settings/address", url.Values{"external_url": {"https://other.example.org"}, "device_fallback": {"http://100.64.0.2:8800"}}, csrf)
	if tw.w.address() != "https://eriphe.example.org" || tw.w.deviceFallback() != "http://100.64.0.2:8800" {
		t.Fatalf("locked: %q %q", tw.w.address(), tw.w.deviceFallback())
	}
}

// The connect commands give the local address, then the second one, then
// the tailnet's: every installer takes the list (install.sh --core,
// install.ps1 -Core and ERIPHE_CORE, the launcher's and compose's
// ERIPHE_CORE, eriphe-node -core).
func TestJoinTwoAddresses(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	host := strings.TrimPrefix(tw.srv.URL, "https://")
	lan := "http://" + host[:strings.LastIndex(host, ":")] + ":8800"
	_, body, _ := tw.post("/settings/machines/join", url.Values{"name": {"laptop"}}, csrf)
	hasAll(t, "one address", body, "--core "+lan+" ")
	hasNone(t, "one address", body, ",http")

	tw.c.store.set(deviceFallbackKey, "http://100.64.0.2:8800")
	_, body, _ = tw.do("GET", "/settings/devices", nil, nil)
	hasAll(t, "choice", body, `name="via" value="both"`, "http://100.64.0.2:8800")
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"laptop"}, "via": {"both"}}, csrf)
	both := lan + ",http://100.64.0.2:8800"
	hasAll(t, "two addresses", body, "sh -s -- --core "+both+" ", "$env:ERIPHE_CORE='"+both+"';",
		"-Core '"+both+"'", "ERIPHE_CORE: "+both+"\n", "eriphe-node -core "+both+" ")
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"laptop"}, "via": {"remote"}}, csrf)
	hasAll(t, "away only", body, "--core http://100.64.0.2:8800 ")
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"laptop"}, "via": {"lan"}}, csrf)
	hasNone(t, "lan only", body, "100.64.0.2")
}
