package main

import (
	"strings"
	"testing"
)

// Every page of the settings renders inside the common layout: the top bar
// with its two buttons, Tâches and Réglages (current), the way back to the
// conversation, and the settings' own navigation in four groups with its
// entry marked. There is no tab bar and no main navigation any more.
func TestSettingsTabsHaveLayout(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, tc := range []struct{ path, tab string }{
		{"/memory", "memory"},
		{"/memory?view=inbox", "memory"},
		{"/settings/devices", "devices"},
		{"/settings/machines", "devices"},
		{"/settings/agents", "agents"},
		{"/settings/agents/new", "agents"},
		{"/settings/providers/new", "agents"},
		{"/settings/providers/new?kind=anthropic-api", "agents"},
		{"/settings/tools", "tools"},
		{"/settings/tools/add", "tools"},
		{"/settings/tools/new?preset=home-assistant", "tools"},
		{"/settings/home-assistant", "tools"},
		{"/settings/drive", "tools"},
		{"/settings/folders", "folders"},
		{"/settings/remote", "remote"},
		{"/settings/notifications", "notifications"},
		{"/settings/journal", "journal"},
		{"/settings/account", "account"},
	} {
		code, body, _ := tw.do("GET", tc.path, nil, nil)
		if code != 200 {
			t.Errorf("GET %s: %d", tc.path, code)
			continue
		}
		href := "/settings/" + tc.tab
		if tc.tab == "memory" {
			href = "/memory"
		}
		for _, want := range []string{`<header class="topbar">`, `<a class="back-home" href="/">`,
			`id="tasks-btn" href="/#tasks-panel"`, `id="tasks-badge"`, `<a class="head-btn" href="/settings" aria-label="Réglages" title="Réglages" aria-current="page">`,
			`<nav class="settings-nav"`, `<a href="` + href + `" aria-current="page">`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %q", tc.path, want)
			}
		}
		for _, gone := range []string{`class="tabbar"`, `class="topnav"`} {
			if strings.Contains(body, gone) {
				t.Errorf("GET %s still has %q", tc.path, gone)
			}
		}
		if n := strings.Count(body, `aria-current="page">`); n != 2 { // Réglages, and the entry
			t.Errorf("GET %s: %d current links, want 2", tc.path, n)
		}
	}
}

// The settings open on their first entry, the memory, whose address stays
// /memory: /settings/memory leads there, with its query.
func TestSettingsOpenOnMemory(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for path, want := range map[string]string{"/settings": "/memory", "/settings/memory?view=inbox": "/memory?view=inbox"} {
		if code, _, h := tw.do("GET", path, nil, nil); code != 303 || h.Get("Location") != want {
			t.Errorf("GET %s: %d to %q, want %q", path, code, h.Get("Location"), want)
		}
	}
}

// The settings' entries come in four groups, in this order, each under its
// title; the memory is the first entry of the first.
func TestSettingsNavGroups(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	nav := body[strings.Index(body, `<nav class="settings-nav"`):]
	nav = nav[:strings.Index(nav, "</nav>")]
	order := []string{"Assistant", `href="/memory"`, `href="/settings/agents"`,
		"Connexions", `href="/settings/tools"`, `href="/settings/folders"`,
		"Appareils &amp; accès", `href="/settings/devices"`, `href="/settings/remote"`, `href="/settings/notifications"`,
		"Mon espace", `href="/settings/account"`, `href="/settings/journal"`}
	at := 0
	for _, want := range order {
		i := strings.Index(nav[at:], want)
		if i < 0 {
			t.Fatalf("the nav lacks %q after its place, or has it out of order:\n%s", want, nav)
		}
		at += i + len(want)
	}
	if n := strings.Count(nav, `class="nav-group"`); n != 4 {
		t.Errorf("%d groups, want 4", n)
	}
	if strings.Contains(nav, "attn-dot") {
		t.Error("an entry is marked while nothing is wrong")
	}
}

// The conversation is the one main screen: Tâches opens its panel in place,
// and the panel can be closed (the phone's sheet).
func TestHomeHeader(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	_, body, _ := tw.do("GET", "/", nil, nil)
	for _, want := range []string{`<a class="brand" href="/"`, `id="tasks-btn" href="#tasks-panel" aria-controls="tasks-panel"`,
		`<b class="badge" id="tasks-badge" hidden></b>`, `<a class="head-btn" href="/settings" aria-label="Réglages"`,
		`<aside class="side" id="tasks-panel"`, `data-tasks-close`} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	for _, gone := range []string{`class="tabbar"`, `class="topnav"`, `class="back-home"`, `aria-current="page"`} {
		if strings.Contains(body, gone) {
			t.Errorf("home still has %q", gone)
		}
	}
	// Signing out is in My space now.
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); !strings.Contains(body, `hx-post="/logout"`) {
		t.Error("no way to sign out")
	}
}

// A tool in error and a refused key mark their entries.
func TestSettingsNavAttention(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.setProviderHealth("p", providerHealth{Cause: causeAuth})
	_, body, _ := tw.do("GET", "/settings/journal", nil, nil)
	nav := body[strings.Index(body, `<nav class="settings-nav"`):]
	agents := nav[strings.Index(nav, `href="/settings/agents"`):]
	if !strings.Contains(agents[:strings.Index(agents, "</a>")], "attn-dot") {
		t.Error("a refused key does not mark Agents")
	}
}

// Only main scrolls in the workspace: the document can never grow past the
// window, whatever overflows inside it, so nothing can scroll the top bar and
// the settings nav out of sight. The nav's pills are centred by scrolling
// their own row, not with scrollIntoView, which scrolls every ancestor that
// can scroll, the page included.
func TestShellNeverScrolls(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, path := range []string{"/settings/agents", "/settings/journal", "/memory"} {
		if _, body, _ := tw.do("GET", path, nil, nil); !strings.Contains(body, `<html lang="fr" class="shell">`) {
			t.Errorf("GET %s: the html element is not marked as the shell", path)
		}
	}
	if _, body, _ := tw.do("GET", "/login", nil, nil); strings.Contains(body, `class="shell"`) {
		t.Error("the login page is marked as the shell")
	}

	css, err := webFS.ReadFile("web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string][]string{}
	for _, block := range strings.Split(string(css), "}") {
		sel, decl, ok := strings.Cut(block, "{")
		if !ok {
			continue
		}
		if i := strings.LastIndex(sel, "*/"); i >= 0 {
			sel = sel[i+2:]
		}
		sel = strings.TrimSpace(sel)
		rules[sel] = append(rules[sel], decl)
	}
	for sel, want := range map[string][]string{
		"html.shell":        {"overflow: hidden"},
		"body.shell":        {"overflow: clip", "position: relative"},
		"body.shell > main": {"position: relative", "overflow: auto"},
	} {
		decl := strings.Join(rules[sel], ";")
		for _, w := range want {
			if !strings.Contains(decl, w) {
				t.Errorf("app.css: %s lacks %q", sel, w)
			}
		}
	}

	js, err := webFS.ReadFile("web/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "scrollIntoView") {
		t.Error("app.js uses scrollIntoView, which can scroll the whole page")
	}
}
