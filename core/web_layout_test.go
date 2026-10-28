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
		{"/settings/machines", "machines"},
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
