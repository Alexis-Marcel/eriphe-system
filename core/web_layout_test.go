package main

import (
	"strings"
	"testing"
)

// Every page of the settings renders inside the common layout: the same top
// bar as the conversation (the name, Tâches, Réglages current) with no way
// back in it, and the settings' own navigation in four groups with its entry
// marked, under the way back to the conversation. The ways back follow the
// pages: a page goes up to the list (/settings, seen on a phone), a sub-page
// to its page, never to the browser's history.
func TestSettingsTabsHaveLayout(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, tc := range []struct{ path, tab, up string }{ // up: a sub-page's page
		{"/memory", "memory", ""},
		{"/memory?view=inbox", "memory", ""},
		{"/settings/devices", "devices", ""},
		{"/settings/machines", "devices", ""},
		{"/settings/agents", "agents", ""},
		{"/settings/agents/new", "agents", "/settings/agents"},
		{"/settings/providers/new", "agents", "/settings/agents"},
		{"/settings/providers/new?kind=anthropic-api", "agents", "/settings/agents"},
		{"/settings/tools", "tools", ""},
		{"/settings/tools/add", "tools", "/settings/tools"},
		{"/settings/tools/new?preset=home-assistant", "tools", "/settings/tools"},
		{"/settings/home-assistant", "tools", "/settings/tools"},
		{"/settings/drive", "tools", "/settings/tools"},
		{"/settings/folders", "folders", ""},
		{"/settings/skills", "skills", ""},
		{"/settings/remote", "remote", ""},
		{"/settings/notifications", "notifications", ""},
		{"/settings/channels", "channels", ""},
		{"/settings/journal", "journal", ""},
		{"/settings/happened", "happened", ""},
		{"/settings/happened?range=week&kind=task", "happened", ""},
		{"/settings/account", "account", ""},
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
		for _, want := range []string{`<header class="topbar">`, `<a class="brand" href="/"`,
			`id="tasks-btn" href="/#tasks-panel"`, `id="tasks-badge"`, `<a class="head-btn" href="/settings" aria-label="Réglages" title="Réglages" aria-current="page">`,
			`<nav class="settings-nav"`, `<a class="back to-chat" href="/">`, `<a href="` + href + `" aria-current="page">`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %q", tc.path, want)
			}
		}
		for _, gone := range []string{`class="tabbar"`, `class="topnav"`, `back-home`, `data-back`} {
			if strings.Contains(body, gone) {
				t.Errorf("GET %s still has %q", tc.path, gone)
			}
		}
		if n := strings.Count(body, `aria-current="page">`); n != 2 { // Réglages, and the entry
			t.Errorf("GET %s: %d current links, want 2", tc.path, n)
		}
		up := `<a class="back settings-up" href="/settings">`
		if tc.up == "" {
			if !strings.Contains(body, `<div class="settings is-page">`) || !strings.Contains(body, up) {
				t.Errorf("GET %s: a page without its way up to the list", tc.path)
			}
			continue
		}
		if !strings.Contains(body, `<div class="settings is-sub">`) || strings.Contains(body, up) {
			t.Errorf("GET %s: a sub-page shown as a page", tc.path)
		}
		if !strings.Contains(body, `<a class="back" href="`+tc.up) {
			t.Errorf("GET %s: no way up to %s", tc.path, tc.up)
		}
	}
}

// /settings is the list of the settings alone, the first level on a phone:
// its ✕ goes back to the conversation and no entry is current. A wide screen
// opens the first entry in its place (early.js), without a step in the
// history. /settings/memory leads to the memory, with its query.
func TestSettingsList(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	code, body, _ := tw.do("GET", "/settings", nil, nil)
	if code != 200 {
		t.Fatalf("GET /settings: %d", code)
	}
	for _, want := range []string{`<div class="settings is-index">`, `<nav class="settings-nav"`,
		`<a class="icon-btn settings-close" href="/" aria-label="Fermer les réglages"`, `<h1 class="settings-title">Réglages</h1>`,
		`<a href="/memory" >`, `<a href="/settings/journal" >`, `class="i chev"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the list lacks %q", want)
		}
	}
	if strings.Contains(body, `class="settings-body"`) {
		t.Error("the list has a page beside it")
	}
	if n := strings.Count(body, `aria-current="page">`); n != 1 { // Réglages only
		t.Errorf("%d current links, want 1", n)
	}
	early, err := webFS.ReadFile("web/static/early.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(early), `location.pathname === "/settings"`) || !strings.Contains(string(early), `location.replace("/memory")`) {
		t.Error("a wide screen does not open the first entry in place of the list")
	}
	if code, _, h := tw.do("GET", "/settings/memory?view=inbox", nil, nil); code != 303 || h.Get("Location") != "/memory?view=inbox" {
		t.Errorf("GET /settings/memory: %d to %q", code, h.Get("Location"))
	}
}

// No way back walks the browser's history: they are plain links to the page
// above. The sidebar switches pages in place, so that the browser's Back
// leaves the settings.
func TestSettingsBackIsHierarchical(t *testing.T) {
	js, err := webFS.ReadFile("web/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "data-back") {
		t.Error("app.js still sends a way back to the history")
	}
	if !strings.Contains(string(js), `".settings:not(.is-index) .nav-group > a[href]"`) || !strings.Contains(string(js), "location.replace(a.href)") {
		t.Error("the sidebar adds a step to the history for each page")
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
	order := []string{"Assistant", `href="/memory"`, `href="/settings/happened"`, `href="/settings/agents"`, `href="/settings/skills"`,
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
// the settings nav out of sight. Nothing uses scrollIntoView, which scrolls
// every ancestor that can scroll, the page included.
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
