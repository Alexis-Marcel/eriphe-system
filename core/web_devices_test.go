package main

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var (
	waitURL     = regexp.MustCompile(`hx-get="(/settings/devices/[a-z0-9-]+/wait\?token=[0-9a-f]{16})"`)
	joinTokenRe = regexp.MustCompile(`--join-token (\S+)`)
)

// joinCommand asks the workspace for a device's commands, and returns the
// address the dialog then polls and the token the commands carry.
func joinCommand(t *testing.T, tw *testWeb, path string, form url.Values, csrf string) (wait, token string) {
	t.Helper()
	code, body, _ := tw.post(path, form, csrf)
	w, tok := waitURL.FindStringSubmatch(body), joinTokenRe.FindStringSubmatch(body)
	if code != http.StatusOK || w == nil || tok == nil {
		t.Fatalf("POST %s: %d\n%s", path, code, body)
	}
	if !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "pulse-dot") {
		t.Fatalf("the commands do not wait for the device:\n%s", body)
	}
	if strings.Contains(w[1], tok[1]) {
		t.Fatal("the polled address carries the whole token")
	}
	return w[1], tok[1]
}

// The connect dialog waits for the device under its commands, and says it
// is connected once it joined with its token and is online, in both
// languages; the polling then stops and the grid behind is shown again.
func TestWebJoinWait(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	if _, body, _ := tw.do("GET", "/settings/devices", nil, nil); !strings.Contains(body, `id="devices-grid"`) ||
		!strings.Contains(body, "data-reset-on-close") || !strings.Contains(body, `id="join-result" data-clear-on-close`) {
		t.Fatalf("devices page:\n%s", body)
	}
	wait, token := joinCommand(t, tw, "/settings/devices/join", url.Values{"name": {"desk"}}, csrf)

	waiting := func(lang string) string {
		t.Helper()
		tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: lang, Path: "/"}})
		code, body, _ := tw.do("GET", wait, nil, map[string]string{"HX-Request": "true"})
		if code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", wait, code, body)
		}
		return body
	}
	for lang, want := range map[string]string{"fr": "En attente de desk…", "en": "Waiting for desk…"} {
		if body := waiting(lang); !strings.Contains(body, want) || !strings.Contains(body, `hx-trigger="every 2s"`) {
			t.Fatalf("%s, before the device joins:\n%s", lang, body)
		}
	}

	// Joined with its token, but not connected yet: still waiting.
	if _, err := tw.c.store.joinNode(hashSecret(token), "desk", hashSecret("s")); err != nil {
		t.Fatal(err)
	}
	if body := waiting("fr"); !strings.Contains(body, "En attente de desk…") {
		t.Fatalf("joined but offline:\n%s", body)
	}

	tw.c.store.setNodeStatus("desk", true, []byte(`{}`))
	for lang, want := range map[string][]string{"fr": {"desk est connecté", "Terminer"}, "en": {"desk is connected", "Done"}} {
		body := waiting(lang)
		for _, w := range append(want, `hx-select="#devices-grid"`, `method="dialog"`) {
			if !strings.Contains(body, w) {
				t.Fatalf("%s, connected: no %q in\n%s", lang, w, body)
			}
		}
		if strings.Contains(body, "every 2s") {
			t.Fatalf("%s: still polling once connected:\n%s", lang, body)
		}
	}

	// Joining again: the dialog waits for the new token, even though the
	// device is online with its former secret.
	tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: "fr", Path: "/"}})
	wait, token = joinCommand(t, tw, "/settings/devices/desk/rejoin", nil, csrf)
	if body := waiting("fr"); !strings.Contains(body, "En attente de desk…") {
		t.Fatalf("rejoin, before the device joins again:\n%s", body)
	}
	if _, err := tw.c.store.joinNode(hashSecret(token), "desk", hashSecret("s2")); err != nil {
		t.Fatal(err)
	}
	if body := waiting("fr"); !strings.Contains(body, "desk est connecté") {
		t.Fatalf("rejoined:\n%s", body)
	}
}

// The waiting address takes only a device's name and a token's id, and
// like the rest of the workspace only with a session.
func TestWebJoinWaitRefused(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, path := range []string{
		"/settings/devices/" + url.PathEscape(`<b>x</b>`) + "/wait?token=0123456789abcdef",
		"/settings/devices/desk/wait?token=" + url.QueryEscape(`"><b>`),
		"/settings/devices/desk/wait",
	} {
		code, body, _ := tw.do("GET", path, nil, nil)
		if code != http.StatusNotFound || strings.Contains(body, "<b>") {
			t.Errorf("GET %s: %d %s", path, code, body)
		}
	}
	anon := &http.Client{Transport: tw.srv.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := anon.Get(tw.srv.URL + "/settings/devices/desk/wait?token=0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("without a session: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// A name is escaped in the waiting line, as everywhere in templates.
func TestJoinWaitEscapes(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, connected := range []bool{false, true} {
		got, err := tw.w.fragment("en", "settings", "join-wait", joinWait{Name: `<i>pc</i>`, ID: "0123456789abcdef", Connected: connected})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "<i>") || !strings.Contains(got, "&lt;i&gt;pc&lt;/i&gt;") {
			t.Fatalf("connected %t:\n%s", connected, got)
		}
	}
}
