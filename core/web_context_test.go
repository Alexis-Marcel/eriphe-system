package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The page: the live context, the level, a switch per source, a token made
// and shown once, revoked; the rooms; and what is kept, said plainly.
func TestWebContextPage(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	c.store.db.Exec(`INSERT INTO nodes (name, url, secret_hash) VALUES ('studio', '', 'x')`)
	c.store.setNodeStatus("studio", true, []byte(`{"os":"darwin"}`))
	c.takeIdle("studio", []byte(`{"idle":{"known":true,"seconds":3},"context":{"known":true,"dnd":true,"reason":"game"}}`), time.Now())

	_, body, _ := tw.do("GET", "/settings/context", nil, nil)
	for _, want := range []string{`href="/settings/context"`, "Eriphe sait de toi", "studio", "ne pas déranger", "(jeu)",
		"rien de branché", "30 jours", `hx-get="/context/live"`, "[contexte : studio : actif · ne pas déranger (jeu)]"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if _, body, _ := tw.do("GET", "/context/live", nil, nil); !strings.Contains(body, "ne pas déranger") {
		t.Fatal("no live fragment")
	}

	_, body, _ = tw.post("/context/sources", url.Values{"name": {"Raccourcis iPhone"}}, csrf)
	tok := regexp.MustCompile(`ecx_[A-Za-z0-9_-]{43}`).FindString(body)
	if tok == "" || !strings.Contains(body, "/v1/context") {
		t.Fatalf("no token shown: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/context", nil, nil); strings.Contains(body, tok) || !strings.Contains(body, "Raccourcis iPhone") {
		t.Fatal("the token is shown again, or the source is missing")
	}
	src := c.contextSources()[0]

	tw.post("/context/source", url.Values{"source": {src.key()}}, csrf) // the switch unchecked: off
	if c.contextPrefs().allows(src.key(), sigPlace) {
		t.Fatal("not turned off")
	}
	tw.post("/context/source", url.Values{"source": {src.key()}, "on": {"1"}}, csrf)
	if !c.contextPrefs().allows(src.key(), sigPlace) {
		t.Fatal("not turned on")
	}
	if code, _, _ := tw.post("/context/source", url.Values{"source": {"src:999"}}, csrf); code != 400 {
		t.Fatalf("an unknown source: %d", code)
	}

	tw.post("/context/level", url.Values{"level": {"2"}}, csrf)
	if c.contextPrefs().Level != 2 {
		t.Fatal("level not saved")
	}
	tw.post("/context/level", url.Values{"level": {"5"}}, csrf)
	if c.contextPrefs().Level != 2 {
		t.Fatal("level 5 saved")
	}

	tw.post("/context/rooms", url.Values{"rooms": {"Salon, Bureau] [x"}}, csrf)
	if got := c.contextPrefs().Rooms; strings.Join(got, "|") != "Salon|Bureau x" {
		t.Fatalf("rooms %q", got)
	}

	tw.post("/context/sources/"+itoa(int(src.ID))+"/revoke", nil, csrf)
	if len(c.contextSources()) != 0 {
		t.Fatal("not revoked")
	}
	tw.post("/context/level", url.Values{"level": {"0"}}, csrf)
	if _, body, _ := tw.do("GET", "/settings/context", nil, nil); !strings.Contains(body, "Niveau 0") {
		t.Fatal("level 0 not said")
	}
}
