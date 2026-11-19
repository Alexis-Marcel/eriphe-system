package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// homeWeb is a workspace whose brain has an AI: its home page is the
// graph workspace.
func homeWeb(t *testing.T) (*testWeb, string) {
	t.Helper()
	tw, _, csrf := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} })
	return tw, csrf
}

const espaceShell = `data-boot="/espace/boot"`

// / and /espace open the graph workspace, the one surface: the
// conversation page, the home choice and the top bar are gone.
func TestHomeOpensTheWorkspace(t *testing.T) {
	tw, csrf := homeWeb(t)

	code, body, h := tw.do("GET", "/", nil, nil)
	if code != http.StatusOK || !strings.Contains(body, espaceShell) {
		t.Fatalf("GET /: %d, not the workspace:\n%s", code, body)
	}
	// The shell: its error state offers to try again, nothing inline, the
	// CSP strict, and no way to a conversation page.
	for _, want := range []string{`class="espace-fallback"`, `<a href="/espace">Réessayer</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %s", want)
		}
	}
	for _, gone := range []string{"/conversation", "/settings/home", "noscript"} {
		if strings.Contains(body, gone) {
			t.Errorf("home still has %s", gone)
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
	if code, body, _ = tw.do("GET", "/espace", nil, nil); code != http.StatusOK || !strings.Contains(body, espaceShell) {
		t.Fatalf("GET /espace: %d", code)
	}
	// A link to a node opens the workspace, its address kept for the page.
	if code, body, _ := tw.do("GET", "/espace?open=node-k3m9", nil, nil); code != http.StatusOK || !strings.Contains(body, espaceShell) {
		t.Errorf("a link to a node: %d", code)
	}

	// The conversation page and the home choice are no more.
	if code, _, _ := tw.do("GET", "/conversation", nil, nil); code != http.StatusNotFound {
		t.Errorf("GET /conversation: %d", code)
	}
	if code, _, _ := tw.post("/settings/home", url.Values{"home": {"conversation"}}, csrf); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("POST /settings/home: %d", code)
	}
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); strings.Contains(body, "/settings/home") {
		t.Error("the settings still offer a home page")
	}

	// Without a session: the sign-in.
	tw.hc.Jar = nil
	for _, path := range []string{"/", "/espace"} {
		if code, _, h := tw.do("GET", path, nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
			t.Errorf("GET %s signed out: %d %s", path, code, h.Get("Location"))
		}
	}
}

// Before the wizard, / and /espace lead to it; once it is left for later,
// the workspace opens and its boot says Eriphe has no AI yet, for the
// console to say it with the way to the wizard. With no brain at all, the
// boot says that.
func TestHomeWithoutAI(t *testing.T) {
	boot := func(tw *testWeb) espaceBoot {
		t.Helper()
		_, body, _ := tw.do("GET", "/espace/boot", nil, nil)
		var b espaceBoot
		if err := json.Unmarshal([]byte(body), &b); err != nil {
			t.Fatalf("boot: %v\n%s", err, body)
		}
		return b
	}

	tw := newTestWeb(t) // no brain
	tw.setup()
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, espaceShell) {
		t.Error("no brain: / is not the workspace")
	}
	if b := boot(tw); b.Setup != "nobrain" {
		t.Errorf("no brain: the boot says %q", b.Setup)
	}

	tw, _, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	for _, path := range []string{"/", "/espace"} {
		if code, _, h := tw.do("GET", path, nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
			t.Fatalf("GET %s before the wizard: %d %s", path, code, h.Get("Location"))
		}
	}
	tw.do("POST", "/welcome/skip", url.Values{"csrf": {csrf}}, nil)
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, espaceShell) {
		t.Error("wizard left for later: / is not the workspace")
	}
	if b := boot(tw); b.Setup != "noai" || b.Strings["espace.noai.link"] == "" {
		t.Errorf("wizard left for later: the boot says %q", b.Setup)
	}

	tw, _ = homeWeb(t)
	if b := boot(tw); b.Setup != "" {
		t.Errorf("an AI: the boot says %q", b.Setup)
	}
}

// What pointed into the conversation points to its place in the graph (a
// message's node, the graph for the home's), to « À valider » for a
// permission, and to a task's own page.
func TestLinksIntoTheGraph(t *testing.T) {
	tw, _ := homeWeb(t)
	c := tw.c
	home, err := c.store.addMessage(roleBrain, "à la maison", true)
	if err != nil {
		t.Fatal(err)
	}
	onNode, err := c.store.addNodeMessage(roleBrain, "sur un nœud", "", "k3m9", true)
	if err != nil {
		t.Fatal(err)
	}

	if got := nodeAt(""); got != "/espace" {
		t.Errorf("nodeAt home: %s", got)
	}
	if got := c.messageAt(int(onNode)); got != "/espace?open=node-k3m9" {
		t.Errorf("a node's message: %s", got)
	}
	if got := c.messageAt(int(home)); got != "/espace" {
		t.Errorf("the home's message: %s", got)
	}
	if got := c.messageAt(9999); got != "/espace" {
		t.Errorf("a message not there: %s", got)
	}
	if got := permissionAt("t-1", "p-2"); got != "/settings/approve#a-perm-t-1-p-2" {
		t.Errorf("permission: %s", got)
	}
	if n := taskEndedNote(&TaskRow{ID: "t-1", Kind: "worker", Prompt: "x"}, stateIdle); n.URL != "/tasks/t-1" {
		t.Errorf("task note: %s", n.URL)
	}
	if n := c.channelCardNote(convoCard{ID: 1, Seq: int(onNode)}); n.URL != "/espace?open=node-k3m9" {
		t.Errorf("card note: %s", n.URL)
	}
	if n := c.channelCardNote(convoCard{ID: 1}); n.URL != "/espace" {
		t.Errorf("card note without message: %s", n.URL)
	}
	if got := c.learnedHref("msg:" + itoa(int(onNode))); got != "/espace?open=node-k3m9" {
		t.Errorf("evidence: %s", got)
	}
	if n := (notifCard{notification: &notification{Msg: 3, About: "node:k3m9"}}); n.MsgAt() != "/espace?open=node-k3m9" {
		t.Errorf("a notification's message: %s", n.MsgAt())
	}
	if n := (notifCard{notification: &notification{Msg: 3, About: "skill:x"}}); n.MsgAt() != "/espace" {
		t.Errorf("a notification about no node: %s", n.MsgAt())
	}
	if got := link("/conversation#m3", "là"); strings.Contains(got, "href") {
		t.Errorf("a message's link to the old conversation: %s", got)
	}

	// The settings lead back to the workspace, closing them too; no top bar.
	_, body, _ := tw.do("GET", "/settings/account", nil, nil)
	for _, want := range []string{`<a class="back to-espace" href="/espace">`, `class="icon-btn settings-close" href="/espace"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %s", want)
		}
	}
	for _, gone := range []string{`class="topbar"`, "tasks-btn", "/conversation"} {
		if strings.Contains(body, gone) {
			t.Errorf("settings still have %s", gone)
		}
	}
}

// The workspace's stream does not carry the conversation's past messages:
// it starts after the last one, with the last exchange as JSON, never HTML.
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
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
		if strings.Contains(sc.Text(), `"text":"le dernier"`) {
			time.AfterFunc(200*time.Millisecond, cancel)
		}
	}
	got := b.String()
	for _, gone := range []string{"un message passé", "event: message", "event: tasks", "event: pending", "event: draft", "event: cards"} {
		if strings.Contains(got, gone) {
			t.Errorf("the stream carries %q:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "event: node") || !strings.Contains(got, `"text":"le dernier"`) {
		t.Errorf("the workspace's stream:\n%s", got)
	}
}
