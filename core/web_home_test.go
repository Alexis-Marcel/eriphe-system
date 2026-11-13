package main

import (
	"bufio"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// homeWeb is a workspace whose brain has an AI: its home page is the
// composed workspace.
func homeWeb(t *testing.T) (*testWeb, string) {
	t.Helper()
	tw, _, csrf := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} })
	return tw, csrf
}

const (
	espaceShell = `data-boot="/espace/boot"`
	convoPage   = `id="tasks-panel"`
)

// / opens the workspace; the conversation stays at /conversation; the
// workspace is still at /espace.
func TestHomeOpensTheWorkspace(t *testing.T) {
	tw, csrf := homeWeb(t)

	code, body, h := tw.do("GET", "/", nil, nil)
	if code != http.StatusOK || !strings.Contains(body, espaceShell) || strings.Contains(body, convoPage) {
		t.Fatalf("GET /: %d, not the workspace:\n%s", code, body)
	}
	// The shell, as the home page: its fallback leads to the conversation,
	// nothing inline, the CSP strict.
	for _, want := range []string{`<a href="/conversation">Ouvrir la conversation</a>`, `<form method="post" action="/settings/home">`,
		`name="csrf" value="` + csrf + `"`, `name="home" value="conversation"`, `<noscript><meta http-equiv="refresh" content="0; url=/conversation"></noscript>`,
		`class="espace-fallback"`} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %s", want)
		}
	}
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(m, ` src="/static/`) {
			t.Errorf("inline script: %s", m)
		}
	}
	if strings.Contains(body, "<style") || strings.Contains(body, "style=") {
		t.Error("inline style in the home page")
	}
	if csp := h.Get("Content-Security-Policy"); strings.Contains(csp, "unsafe") || !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP: %q", csp)
	}

	code, body, _ = tw.do("GET", "/conversation", nil, nil)
	if code != http.StatusOK || !strings.Contains(body, convoPage) || !strings.Contains(body, `hx-post="/messages"`) {
		t.Fatalf("GET /conversation: %d, not the conversation", code)
	}
	// The conversation leads home, and to the workspace.
	if !strings.Contains(body, `<a class="brand" href="/"`) || !strings.Contains(body, `href="/espace"`) {
		t.Error("the conversation has no way back to the workspace")
	}
	if code, body, _ = tw.do("GET", "/espace", nil, nil); code != http.StatusOK || !strings.Contains(body, espaceShell) {
		t.Fatalf("GET /espace: %d", code)
	}

	// Without a session: the sign-in, for both.
	tw.hc.Jar = nil
	for _, path := range []string{"/", "/conversation"} {
		if code, _, h := tw.do("GET", path, nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
			t.Errorf("GET %s signed out: %d %s", path, code, h.Get("Location"))
		}
	}
}

// A browser can keep the conversation as its home page, from the settings
// or from the workspace's fallback, and come back to the workspace.
func TestHomeChoice(t *testing.T) {
	tw, csrf := homeWeb(t)

	if code, body, _ := tw.do("GET", "/settings/account", nil, nil); code != http.StatusOK ||
		!strings.Contains(body, `hx-post="/settings/home"`) || !strings.Contains(body, `value="espace" aria-pressed="true"`) {
		t.Fatalf("the settings do not offer the home page: %d", code)
	}

	// The fallback's plain form: back home, which is now the conversation.
	code, _, h := tw.do("POST", "/settings/home", url.Values{"csrf": {csrf}, "home": {homeConvo}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/" || !strings.Contains(h.Get("Set-Cookie"), "home=conversation") ||
		!strings.Contains(h.Get("Set-Cookie"), "HttpOnly") {
		t.Fatalf("choose the conversation: %d %s %s", code, h.Get("Location"), h.Get("Set-Cookie"))
	}
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, convoPage) || strings.Contains(body, espaceShell) {
		t.Fatal("/ is not the conversation once chosen")
	}
	// /espace still opens the workspace; the settings say what is chosen.
	if _, body, _ := tw.do("GET", "/espace", nil, nil); !strings.Contains(body, espaceShell) {
		t.Error("/espace does not open the workspace")
	}
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); !strings.Contains(body, `value="conversation" aria-pressed="true"`) {
		t.Error("the settings do not show the conversation chosen")
	}

	// From the settings (htmx): the page reloads; the workspace is home again.
	code, _, h = tw.post("/settings/home", url.Values{"home": {homeEspace}}, csrf)
	if code != http.StatusOK || h.Get("HX-Refresh") != "true" {
		t.Fatalf("choose the workspace: %d", code)
	}
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, espaceShell) {
		t.Fatal("/ is not the workspace again")
	}

	// Anything else, or without the token: refused.
	if code, _, _ := tw.post("/settings/home", url.Values{"home": {"memory"}}, csrf); code != http.StatusBadRequest {
		t.Errorf("unknown home: %d", code)
	}
	if code, _, _ := tw.do("POST", "/settings/home", url.Values{"home": {homeConvo}}, nil); code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d", code)
	}
}

// While no AI composes the workspace, / opens the conversation, which says
// so; before the wizard, / leads to it.
func TestHomeWithoutAI(t *testing.T) {
	tw := newTestWeb(t) // no brain
	tw.setup()
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, convoPage) {
		t.Error("no brain: / is not the conversation")
	}

	tw, _, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	if code, _, h := tw.do("GET", "/", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
		t.Fatalf("before the wizard: %d %s", code, h.Get("Location"))
	}
	if code, _, h := tw.do("GET", "/conversation", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
		t.Fatalf("the conversation before the wizard: %d %s", code, h.Get("Location"))
	}
	tw.do("POST", "/welcome/skip", url.Values{"csrf": {csrf}}, nil)
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, "pas encore d&#39;IA") || strings.Contains(body, espaceShell) {
		t.Error("wizard left for later: / does not say there is no AI yet")
	}
}

// What points into the conversation (a message, a permission, a task)
// names /conversation; what means home stays /.
func TestLinksIntoTheConversation(t *testing.T) {
	if got := convoAt("m12"); got != "/conversation#m12" {
		t.Errorf("convoAt: %s", got)
	}
	if n := taskEndedNote(&TaskRow{ID: "t-1", Kind: "worker", Prompt: "x"}, stateIdle); n.URL != "/conversation?task=t-1" {
		t.Errorf("task note: %s", n.URL)
	}
	if n := channelCardNote(convoCard{ID: 1, Seq: 9}); n.URL != "/conversation#m9" {
		t.Errorf("card note: %s", n.URL)
	}
	if n := channelCardNote(convoCard{ID: 1}); n.URL != "/conversation" {
		t.Errorf("card note without message: %s", n.URL)
	}
	if learnedHref("msg:4") != "/conversation#m4" {
		t.Errorf("evidence: %s", learnedHref("msg:4"))
	}
	if got := link("/conversation#m3", "là"); !strings.Contains(got, `href="/conversation#m3"`) {
		t.Errorf("a message's link to the conversation: %s", got)
	}

	tw, _ := homeWeb(t)
	// A task's address opened as a page: the conversation with its drawer.
	code, _, h := tw.do("GET", "/tasks/t-9", nil, map[string]string{"Sec-Fetch-Mode": "navigate"})
	if code != http.StatusSeeOther || h.Get("Location") != "/conversation?task=t-9" {
		t.Errorf("task page: %d %s", code, h.Get("Location"))
	}
	// The settings lead back to the conversation; closing them goes home.
	_, body, _ := tw.do("GET", "/settings/account", nil, nil)
	for _, want := range []string{`<a class="back to-chat" href="/conversation">`, `class="icon-btn settings-close" href="/"`,
		`id="tasks-btn" href="/conversation#tasks-panel"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %s", want)
		}
	}
}

// The workspace's stream does not carry the conversation's past messages:
// it starts after the last one, with the last exchange as JSON.
func TestWorkspaceStreamSkipsThePast(t *testing.T) {
	tw, _ := homeWeb(t)
	for range 3 {
		if _, err := tw.c.store.addMessage(roleUser, "un message passé", true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tw.c.store.addMessage(roleUser, "le dernier", true); err != nil {
		t.Fatal(err)
	}
	// events reads the stream until it carries until, and a moment more for
	// what should not follow.
	events := func(query, until string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events"+query, nil)
		resp, err := tw.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			b.WriteString(sc.Text() + "\n")
			if strings.Contains(sc.Text(), until) {
				time.AfterFunc(200*time.Millisecond, cancel)
			}
		}
		return b.String()
	}
	if got := events("?a2ui=1", `"text":"le dernier"`); strings.Contains(got, "un message passé") || !strings.Contains(got, "event: a2ui") ||
		strings.Contains(got, "event: message") || !strings.Contains(got, `"text":"le dernier"`) {
		t.Errorf("the workspace's stream:\n%s", got)
	}
	if got := events("", "le dernier"); !strings.Contains(got, "un message passé") {
		t.Error("the conversation's stream lost its messages")
	}
}
