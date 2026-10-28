package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newRemoteWeb is the workspace with remote access driven by a fake tailnet.
func newRemoteWeb(t *testing.T) (*testWeb, *remoteHarness, string) {
	t.Helper()
	tw := newTestWeb(t)
	h := newRemoteHarness(t, "")
	tw.c.remote = h.r
	h.r.setWorkspace(tailnetHandler(h.r.name, tw.w.routes()))
	return tw, h, tw.setup()
}

func TestWebRemotePages(t *testing.T) {
	tw, h, csrf := newRemoteWeb(t)
	code, body, _ := tw.do("GET", "/settings/remote", nil, nil)
	if code != 200 || !strings.Contains(body, "Activer l&#39;accès à distance") || !strings.Contains(body, `href="/settings/remote" aria-current="page"`) ||
		!strings.Contains(body, "/static/remote.js") {
		t.Fatalf("off page: %d\n%s", code, body)
	}
	if code, _, _ := tw.do("POST", "/settings/remote/enable", nil, nil); code != http.StatusForbidden {
		t.Fatalf("enable without CSRF: %d", code)
	}
	code, body, _ = tw.post("/settings/remote/enable", nil, csrf)
	if code != 200 || !strings.Contains(body, "remote-card") {
		t.Fatalf("enable: %d %s", code, body)
	}
	h.phase(phaseLogin)
	_, body, _ = tw.do("GET", "/settings/remote", nil, nil)
	if !strings.Contains(body, `href="https://login.tailscale.com/a/abc123"`) || !strings.Contains(body, "Se connecter à Tailscale") ||
		!strings.Contains(body, "Google, Apple ou Microsoft") {
		t.Fatalf("login page:\n%s", body)
	}

	f := h.fake()
	f.set(func(f *fakeTailnet) {
		f.st = running(false, h.clock.now().Add(180*24*time.Hour))
		f.httpsURL = "https://login.tailscale.com/f/serve?node=abc"
	})
	h.wait("consent", func(v remoteView) bool { return v.HTTPSURL != "" })
	_, body, _ = tw.do("GET", "/settings/remote", nil, nil)
	if !strings.Contains(body, `href="https://login.tailscale.com/f/serve?node=abc"`) || !strings.Contains(body, "journaux publics des certificats") {
		t.Fatalf("https page:\n%s", body)
	}

	f.set(func(f *fakeTailnet) { f.st = running(true, h.clock.now().Add(5*24*time.Hour)) })
	h.phase(phaseReady)
	_, body, _ = tw.do("GET", "/settings/remote", nil, map[string]string{"Cookie": "lang=en"})
	for _, want := range []string{"https://" + testTSName, `<svg class="qr"`, "apps.apple.com", "play.google.com",
		"Add to the home screen", "The Tailscale login expires on", "Log in again", "https://" + testTSName + ":8800"} {
		if !strings.Contains(body, want) {
			t.Errorf("ready page: no %q", want)
		}
	}

	// The join command offers the tailnet's address once ready.
	_, body, _ = tw.do("GET", "/settings/machines", nil, nil)
	if !strings.Contains(body, `name="via" value="both"`) {
		t.Fatal("no choice of address on the machines page")
	}
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"laptop"}, "via": {"both"}}, csrf)
	host := strings.TrimPrefix(tw.srv.URL, "https://")
	host = host[:strings.LastIndex(host, ":")]
	if !strings.Contains(body, "ERIPHE_CORE: http://"+host+":8800,https://"+testTSName+":8800") {
		t.Fatalf("join, both:\n%s", body)
	}
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"laptop"}, "via": {"remote"}}, csrf)
	if !strings.Contains(body, "-core https://"+testTSName+":8800 ") {
		t.Fatalf("join, remote:\n%s", body)
	}
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"laptop"}, "via": {"lan"}}, csrf)
	if strings.Contains(body, testTSName) {
		t.Fatalf("join, lan:\n%s", body)
	}

	if code, _, _ := tw.post("/settings/remote/disable", nil, csrf); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	if v := h.r.current(); v.Phase != phaseOff {
		t.Fatalf("after disable: %+v", v)
	}
	_, body, _ = tw.do("GET", "/settings/machines", nil, nil)
	if strings.Contains(body, `name="via"`) {
		t.Fatal("choice of address while remote access is off")
	}
}

func TestWebRemoteAdvanced(t *testing.T) {
	tw, h, csrf := newRemoteWeb(t)
	_, body, _ := tw.post("/settings/remote/advanced", url.Values{"hostname": {"-bad-"}}, csrf)
	if !strings.Contains(body, "1 à 63 caractères") {
		t.Fatalf("bad hostname: %s", body)
	}
	_, body, _ = tw.post("/settings/remote/advanced", url.Values{"hostname": {"maison"}, "authkey": {"not-a-key"}}, csrf)
	if !strings.Contains(body, "tskey-") {
		t.Fatalf("bad key: %s", body)
	}
	code, body, hd := tw.post("/settings/remote/advanced", url.Values{"hostname": {"maison"}, "control_url": {"https://hs.example.org"}}, csrf)
	if code != 200 || hd.Get("HX-Refresh") != "true" || !strings.Contains(body, "Enregistré") {
		t.Fatalf("save: %d %v %s", code, hd, body)
	}
	if v := h.r.current(); v.Hostname != "maison" || v.ControlURL != "https://hs.example.org" {
		t.Fatalf("saved: %+v", v)
	}
	if code, _, _ := tw.post("/settings/remote/logout", nil, csrf); code != 200 {
		t.Fatalf("logout: %d", code)
	}
}

// On the tailnet, the workspace answers to its ts.net name: same-origin
// requests from it pass the CSRF check, others do not, and the OAuth
// callback is on that origin.
func TestWebRemoteOrigin(t *testing.T) {
	tw, h, csrf := newRemoteWeb(t)
	h.r.enable()
	h.phase(phaseLogin)
	f := h.fake()
	f.set(func(f *fakeTailnet) { f.st = running(true, time.Time{}) })
	h.phase(phaseReady)
	handler := tailnetHandler(h.r.name, tw.w.routes())
	u, _ := url.Parse(tw.srv.URL)
	cookies := tw.hc.Jar.Cookies(u)
	post := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "https://"+testTSName+"/settings/remote/relogin", nil)
		req.Host = testTSName
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("HX-Request", "true")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("https://" + testTSName); rec.Code != 200 || rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("same origin on the tailnet: %d %v", rec.Code, rec.Header())
	}
	if rec := post("https://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("other origin: %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = testTSName
	if got := tw.w.callbackURL(req); got != "https://"+testTSName+"/oauth/callback" {
		t.Fatalf("callback: %s", got)
	}
	// The LAN listener sends no HSTS for a self-signed certificate.
	_, _, hd := tw.do("GET", "/login", nil, nil)
	if hd.Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS on the self-signed listener")
	}
}

func TestWebRemoteEvents(t *testing.T) {
	tw, h, _ := newRemoteWeb(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/settings/remote/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	next := func(want string) {
		t.Helper()
		var ev strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if line == "" && strings.Contains(ev.String(), "event: remote") {
				if !strings.Contains(ev.String(), want) {
					t.Fatalf("event without %q:\n%s", want, ev.String())
				}
				return
			}
			ev.WriteString(line + "\n")
		}
		t.Fatalf("stream ended: %v", sc.Err())
	}
	next("remote-hero")
	h.r.enable()
	h.phase(phaseLogin)
	// The stream sends the login step (maybe after the starting one).
	for i := 0; ; i++ {
		var ev strings.Builder
		for sc.Scan() && sc.Text() != "" {
			ev.WriteString(sc.Text() + "\n")
		}
		if strings.Contains(ev.String(), "login.tailscale.com/a/abc123") {
			break
		}
		if i > 5 {
			t.Fatalf("no login event: %s", ev.String())
		}
	}
}

func TestQRSVG(t *testing.T) {
	svg, err := qrSVG("https://"+testTSName, `a "label" <x>`)
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	// 34 bytes at level M: version 3, 29 modules, and 4 of quiet zone on each side.
	if !strings.HasPrefix(s, `<svg class="qr"`) || !strings.Contains(s, `viewBox="0 0 37 37"`) || !strings.Contains(s, `<path fill="#000" d="M`) {
		t.Fatalf("svg: %.200s", s)
	}
	if strings.Contains(s, `"label"`) || strings.Contains(s, "<x>") {
		t.Fatal("label not escaped")
	}
	// The three finder patterns: a 7-module dark run at the top-left corner, inside the quiet zone.
	if !strings.Contains(s, "M4 4h7v1h-7z") {
		t.Fatalf("no finder pattern: %.300s", s)
	}
	again, _ := qrSVG("https://"+testTSName, `a "label" <x>`)
	if again != svg {
		t.Fatal("not deterministic")
	}
}
