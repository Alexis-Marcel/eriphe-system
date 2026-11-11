package main

import (
	"net/url"
	"strings"
	"testing"
)

// A tool's title is the user's name for it, shown on its card and as its
// page's heading; its technical name, in its tools' names, stays.
func TestToolTitle(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", oauthToolForm(f))
	code, state := f.authorize(authURL)
	callback(t, tw, "code="+code+"&state="+url.QueryEscape(state))
	waitStatus(t, c.pool.get("linear"), statusReady)

	_, body, _ := tw.do("GET", "/settings/tools/linear/access", nil, nil)
	if !strings.Contains(body, "<h2>linear</h2>") || !strings.Contains(body, `hx-post="/settings/tools/linear/title"`) || strings.Contains(body, "<h2>Accès de") {
		t.Fatalf("page before: %s", body)
	}
	if code, _, h := tw.post("/settings/tools/linear/title", url.Values{"title": {"  Tickets   du travail "}}, csrf); code != 200 || h.Get("HX-Refresh") != "true" {
		t.Fatalf("save: %d %v", code, h)
	}
	if s, ok := c.conf().Servers["linear"]; !ok || s.Title != "Tickets du travail" {
		t.Fatalf("saved %+v", s)
	}
	if c.pool.get("linear") == nil {
		t.Fatal("the tool's name changed")
	}
	if d, _ := c.decide("mcp__linear__search", nil); d == "" {
		t.Fatal("its tools are no longer named linear")
	}
	_, body, _ = tw.do("GET", "/settings/tools/linear/access", nil, nil)
	if !strings.Contains(body, "<h2>Tickets du travail</h2>") || !strings.Contains(body, "Nom technique : linear") {
		t.Fatalf("page after: %s", body)
	}
	if _, body, _ = tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "<h3>Tickets du travail</h3>") {
		t.Fatalf("card: %s", body)
	}
	if code, _, h := tw.post("/settings/tools/linear/title", url.Values{"title": {strings.Repeat("x", 65)}}, csrf); code != 200 || h.Get("HX-Refresh") != "" {
		t.Fatalf("too long saved: %d %v", code, h)
	}
	// Empty, or its name: back to its name.
	tw.post("/settings/tools/linear/title", url.Values{"title": {"linear"}}, csrf)
	if c.conf().Servers["linear"].Title != "" {
		t.Fatal("its name kept as a title")
	}
}
