package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newUpdatesWeb is a workspace at 0.1.0, the latest eriphe-core and
// eriphe-node, with eriphe-core's own updater; it returns the headers of an
// htmx request.
func newUpdatesWeb(t *testing.T) (*testWeb, map[string]string) {
	t.Helper()
	old := version
	t.Cleanup(func() { version = old })
	version = "0.1.0"
	tw := newTestWeb(t)
	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.1.0", "eriphe-node": "0.1.0"})
	tw.c.updates = newUpdates(f.srv.URL)
	tw.c.updates.check(context.Background())
	os.WriteFile(filepath.Join(tw.c.store.dir, "updater"), []byte("version=1\npin=\n"), 0o600)
	csrf := tw.setup()
	return tw, map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true"}
}

func hasAll(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("%s: no %q in\n%s", what, w, body)
		}
	}
}

func hasNone(t *testing.T, what, body string, gone ...string) {
	t.Helper()
	for _, g := range gone {
		if strings.Contains(body, g) {
			t.Fatalf("%s: still %q in\n%s", what, g, body)
		}
	}
}

// A device's card follows its update: asked, under way, done, each time
// from its foot, which asks again only while the request is pending.
func TestWebUpdateLive(t *testing.T) {
	tw, h := newUpdatesWeb(t)
	setNode(t, tw.c, "pc", `{"os":"linux","install":"linux","version":"0.0.1","update":{"updater":true}}`)

	_, body, _ := tw.do("GET", "/settings/devices", nil, nil)
	hasAll(t, "before", body, `id="device-foot-pc"`, `hx-target="closest .device-foot"`)
	hasNone(t, "before", body, "data-update-poll")

	// The button answers with the card's foot, which follows the request.
	code, body, hdr := tw.do("POST", "/settings/updates/request", url.Values{"machine": {"node:pc"}}, h)
	r := tw.c.pendingUpdate("node:pc")
	if code != http.StatusOK || r == nil || hdr.Get("HX-Refresh") != "" {
		t.Fatalf("update now: %d %+v %v", code, r, hdr)
	}
	poll := `hx-get="/settings/devices/pc/update?id=` + r.ID + `"`
	hasAll(t, "asked", body, `id="device-foot-pc"`, poll, `hx-trigger="every 3s"`, `hx-swap="outerHTML"`,
		"data-update-poll", "Mise à jour demandée…", "pulse-dot", "eriphe-node 0.0.1")
	hasNone(t, "asked", body, "Mise à jour disponible", "→ 0.1.0")
	state := "/settings/devices/pc/update?id=" + r.ID
	if _, body, _ = tw.do("GET", state, nil, h); !strings.Contains(body, poll) || !strings.Contains(body, "Mise à jour demandée…") {
		t.Fatalf("pending:\n%s", body)
	}
	// The page opened while it is pending follows it too.
	_, body, _ = tw.do("GET", "/settings/devices", nil, nil)
	hasAll(t, "page while pending", body, poll)

	// It waits for the tasks: slower.
	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true,"waiting":"`+r.ID+`"}}`)
	_, body, _ = tw.do("GET", state, nil, h)
	hasAll(t, "waiting", body, poll, `hx-trigger="every 10s"`, "elle attend la fin des tâches")

	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"running"}}}`)
	_, body, _ = tw.do("GET", state, nil, h)
	hasAll(t, "running", body, poll, "Mise à jour en cours…", "pulse-dot")

	// Done: the new version, once, and no more asking, even before
	// resolveUpdates drops the request.
	setNode(t, tw.c, "pc", `{"version":"0.1.0","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"ok","to":"0.1.0"}}}`)
	_, body, _ = tw.do("GET", state, nil, h)
	hasAll(t, "done", body, `id="device-foot-pc"`, "eriphe-node 0.1.0", "update-done", "À jour")
	hasNone(t, "done", body, "data-update-poll", "hx-get", "Mise à jour disponible", "pulse-dot")
	_, body, _ = tw.do("GET", "/settings/devices", nil, nil)
	hasNone(t, "page once done", body, "data-update-poll", "update-done")
	tw.c.resolveUpdates(mustNodes(t, tw.c))
	if tw.c.pendingUpdate("node:pc") != nil {
		t.Fatal("the request is not dropped")
	}

	// English.
	tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: "en", Path: "/"}})
	_, body, _ = tw.do("GET", state, nil, h)
	hasAll(t, "done, in English", body, "Updated")
}

// A failed update shows how it went, and the card stops asking.
func TestWebUpdateLiveFailed(t *testing.T) {
	tw, h := newUpdatesWeb(t)
	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	tw.do("POST", "/settings/updates/request", url.Values{"machine": {"node:pc"}}, h)
	r := tw.c.pendingUpdate("node:pc")
	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"rolled_back","message":"eriphe-node did not connect"}}}`)
	_, body, _ := tw.do("GET", "/settings/devices/pc/update?id="+r.ID, nil, h)
	hasAll(t, "failed", body, "La mise à jour a échoué, version précédente remise", "eriphe-node did not connect",
		"Mise à jour disponible", "→ 0.1.0", `value="node:pc"`)
	hasNone(t, "failed", body, "data-update-poll", "update-done")
}

// eriphe-core's row follows its own update, through its restart: once
// done, the cards are shown again, as the nodes can follow it.
func TestWebUpdateLiveCore(t *testing.T) {
	tw, h := newUpdatesWeb(t)
	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.2.0"})
	tw.c.updates = newUpdates(f.srv.URL)
	tw.c.updates.check(context.Background())

	_, body, _ := tw.do("POST", "/settings/updates/request", url.Values{"machine": {"core"}}, h)
	r := tw.c.pendingUpdate(coreMachine)
	if r == nil {
		t.Fatalf("not asked:\n%s", body)
	}
	poll := `hx-get="/settings/updates/core?id=` + r.ID + `"`
	hasAll(t, "asked", body, `id="update-core"`, poll, "data-update-poll", "Mise à jour demandée…")
	state := "/settings/updates/core?id=" + r.ID

	status := func(outcome string) {
		os.WriteFile(filepath.Join(tw.c.store.dir, "update-status"), []byte("id="+r.ID+"\naction=update\noutcome="+outcome+"\n"), 0o600)
	}
	status("running")
	_, body, _ = tw.do("GET", state, nil, h)
	hasAll(t, "running", body, poll, "Mise à jour en cours…")

	// eriphe-core restarted on its new version.
	status("ok")
	version = "0.2.0"
	_, body, _ = tw.do("GET", state, nil, h)
	hasAll(t, "done", body, `id="update-core"`, "0.2.0", "update-done", `hx-select="#devices-grid"`)
	hasNone(t, "done", body, "data-update-poll", "Mise à jour disponible")
}

// Only a known device, and only with a session.
func TestWebUpdateStateRefused(t *testing.T) {
	tw, h := newUpdatesWeb(t)
	if code, _, _ := tw.do("GET", "/settings/devices/ghost/update", nil, h); code != http.StatusNotFound {
		t.Fatalf("unknown device: %d", code)
	}
	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	anon := &http.Client{Transport: tw.srv.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/settings/devices/pc/update", "/settings/updates/core"} {
		resp, err := anon.Get(tw.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Fatalf("%s without a session: %d %s", path, resp.StatusCode, resp.Header.Get("Location"))
		}
		req, _ := http.NewRequest("GET", tw.srv.URL+path, nil)
		req.Header.Set("HX-Request", "true")
		resp, err = anon.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("HX-Redirect") != "/login" {
			t.Fatalf("%s polled without a session: %d %v", path, resp.StatusCode, resp.Header)
		}
	}
}

func mustNodes(t *testing.T, c *core) []NodeRow {
	t.Helper()
	nodes, err := c.store.nodes()
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}
