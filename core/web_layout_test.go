package main

import (
	"strings"
	"testing"
)

// Every page of the settings renders inside the common layout: the top bar
// with the main navigation, the settings' own navigation with its tab
// marked, and the tab bar of the phone.
func TestSettingsTabsHaveLayout(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, tc := range []struct{ path, tab string }{
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
		for _, want := range []string{`<header class="topbar">`, `<nav class="topnav"`, `<a href="/settings" aria-current="page">`,
			`<nav class="settings-nav"`, `<a href="/settings/` + tc.tab + `" aria-current="page">`, `<nav class="tabbar"`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %q", tc.path, want)
			}
		}
		if n := strings.Count(body, `aria-current="page">`); n != 3 { // the top bar, the tab bar, the settings
			t.Errorf("GET %s: %d current links, want 3", tc.path, n)
		}
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
