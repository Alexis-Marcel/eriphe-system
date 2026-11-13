package main

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// publicRoutes are the workspace's only mutating routes that do not go
// through w.auth's session CSRF check, each with what protects it instead.
// A new route that skips w.auth fails TestWebCSRFEveryRoute until it is
// added here, with its reason.
var publicRoutes = map[string]string{
	// No session exists yet: the forms carry their own double-submit token
	// (preToken) and check the Origin; TestWebCSRFPublicForms checks both.
	"POST /setup": "pre-session form, double-submit token",
	"POST /login": "pre-session form, double-submit token",
	// An incoming address: the caller is another service, never a browser
	// with the owner's cookies; the token in the path is the authorisation
	// (TestAlertHookHTTP).
	"POST " + hookPath + "{token}": "the path's token is the authorisation",
	// A push notification's button, answered by the service worker with no
	// session: the notification's own token (k) is the authorisation, and a
	// foreign Origin is refused (checked below).
	"POST /notify/react": "the notification's token, same origin only",
}

var routeWildcard = regexp.MustCompile(`\{[^}]*\}`)

// routePath is a request path for a mux pattern: each wildcard becomes a
// plain segment, {$} nothing.
func routePath(p string) string {
	return routeWildcard.ReplaceAllStringFunc(p, func(w string) string {
		if w == "{$}" {
			return ""
		}
		return "x"
	})
}

// Every route that changes something refuses a request without the
// session's CSRF token, and one from another site even with it.
func TestWebCSRFEveryRoute(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	if len(tw.w.patterns) < 100 {
		t.Fatalf("only %d patterns recorded", len(tw.w.patterns))
	}
	seen := map[string]bool{}
	checked := 0
	for _, p := range tw.w.patterns {
		method, path, ok := strings.Cut(p, " ")
		if !ok {
			// A pattern without a method answers every method.
			method, path = "POST", p
		}
		switch method {
		case "POST", "PUT", "PATCH", "DELETE":
		default:
			continue
		}
		if _, public := publicRoutes[p]; public {
			seen[p] = true
			continue
		}
		target := routePath(path)
		for _, c := range []struct {
			name   string
			header map[string]string
		}{
			{"no token", map[string]string{"HX-Request": "true"}},
			{"foreign origin", map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true", "Origin": "https://evil.example"}},
			{"cross-site fetch", map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true", "Sec-Fetch-Site": "cross-site"}},
		} {
			code, body, _ := tw.do(method, target, url.Values{}, c.header)
			if code != http.StatusForbidden || !strings.Contains(body, "invalid CSRF token") {
				t.Errorf("%s (%s %s), %s: %d %.80q", p, method, target, c.name, code, body)
			}
		}
		checked++
	}
	for p := range publicRoutes {
		if !seen[p] {
			t.Errorf("public route %q is not registered any more: remove it from publicRoutes", p)
		}
	}
	t.Logf("%d mutating routes checked", checked)
	if checked < 100 {
		t.Fatalf("only %d mutating routes checked", checked)
	}
}

// The setup and login forms, before any session, check their own token
// and the Origin.
func TestWebCSRFPublicForms(t *testing.T) {
	tw := newTestWeb(t)
	tok := tw.formToken("/setup")
	form := url.Values{"code": {tw.w.setupCode()}, "password": {testPassword}, "confirm": {testPassword}}
	if code, _, _ := tw.do("POST", "/setup", form, nil); code != http.StatusForbidden {
		t.Fatalf("setup without a token: %d", code)
	}
	form.Set("csrf", tok)
	if code, _, _ := tw.do("POST", "/setup", form, map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("setup from another origin: %d", code)
	}
	if tw.w.hasOwner() {
		t.Fatal("a refused setup made the owner")
	}
	tw.setup()
	tw.do("POST", "/logout", nil, map[string]string{"X-CSRF-Token": tw.sessionToken()})
	tok = tw.formToken("/login")
	login := url.Values{"password": {testPassword}}
	if code, _, _ := tw.do("POST", "/login", login, nil); code != http.StatusForbidden {
		t.Fatalf("login without a token: %d", code)
	}
	login.Set("csrf", tok)
	if code, _, _ := tw.do("POST", "/login", login, map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("login from another origin: %d", code)
	}
	// A push button's answer from another site is refused before its token
	// is even read.
	if code, _, _ := tw.do("POST", "/notify/react?id=1&k=x", url.Values{"r": {"ok"}},
		map[string]string{"Origin": "https://evil.example"}); code != http.StatusBadRequest {
		t.Fatalf("notify/react from another origin: %d", code)
	}
}
