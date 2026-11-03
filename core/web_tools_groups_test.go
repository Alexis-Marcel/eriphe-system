package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestGroupTools(t *testing.T) {
	p := func(id string) toolPreset { x, _ := presetByID(id); return x }
	row := func(name, preset, detail, google string) toolView {
		return toolView{serverView: serverView{serverInfo: serverInfo{Name: name}}, Preset: p(preset), Detail: detail, Google: google}
	}
	tools := []toolView{
		row("mail", "mail", "Gmail · sam@example.org", ""),
		row("agenda", "calendar", "iCloud · sam@icloud.example", ""),
		row("mail-pro", "mail", "Fastmail · sam@pro.example", ""),
		row("gmail", "google", "Gmail · bob@example.org", "bob@example.org"),
		row("notion", "notion", "Notion", ""),
		row("agenda-bob", "google", "Google Agenda · bob@example.org", "bob@example.org"),
		row(filesServer, "gdrive", "Google Drive · 2 dossiers", ""),
		row("x", "remote", "", ""),
		row("y", "remote", "", ""),
	}
	tools[5].Reconnect = true
	groups := groupTools("fr", tools)
	var got []string
	for _, g := range groups {
		if g.Single != nil {
			got = append(got, g.Single.Name)
			continue
		}
		var accounts []string
		for _, a := range g.Accounts {
			var names []string
			for _, t := range a.Tools {
				names = append(names, t.Name)
			}
			accounts = append(accounts, a.Label+"="+strings.Join(names, "+"))
		}
		got = append(got, g.Preset.ID+"["+strings.Join(accounts, ",")+"]")
	}
	want := "mail[sam@example.org=mail,sam@pro.example=mail-pro] agenda google[bob@example.org=gmail+agenda-bob,Google Drive=fichiers] notion x y"
	if strings.Join(got, " ") != want {
		t.Fatalf("groups:\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	if !groups[2].Accounts[0].Reconnect || groups[2].Accounts[1].Reconnect {
		t.Fatal("reconnect")
	}
	if groups[0].AddURL() != "/settings/tools/new?preset=mail" || !groups[0].AddStep() || groups[2].AddURL() != "/settings/google" || groups[2].AddStep() {
		t.Fatalf("add: %s %s", groups[0].AddURL(), groups[2].AddURL())
	}
	// Drive alone, without Google accounts, is its own kind.
	if g := groupTools("fr", []toolView{tools[6]}); len(g) != 1 || g[0].Single == nil || g[0].Preset.ID != "gdrive" {
		t.Fatalf("drive alone: %+v", g)
	}
}

// Two mailboxes are one card, each with its row and its actions, and a way
// to add another.
func TestToolsListGroupsAccounts(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	for name, addr := range map[string]string{"mail": "sam@gmail.com", "mail-pro": "sam@fastmail.com"} {
		form := url.Values{"preset": {"mail"}, "name": {name}, "use": {"mails"}, "access": {"reads"}, "address": {addr}, "password": {"app-pw"}}
		if code, body, _ := tw.post("/settings/tools", form, csrf); code != 200 {
			t.Fatalf("add %s: %d %s", name, code, body)
		}
	}
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	i := strings.Index(body, `id="group-mail"`)
	if i < 0 {
		t.Fatalf("no group: %s", body)
	}
	group := body[i:]
	for _, want := range []string{"2 comptes · 2 outils", "Ajouter un compte", `href="/settings/tools/new?preset=mail" data-step-open="add-dlg"`,
		`id="tool-mail"`, `id="tool-mail-pro"`, "Gmail · sam@gmail.com", "Fastmail · sam@fastmail.com",
		`/settings/tools/mail/edit`, `/settings/tools/mail-pro/delete`} {
		if !strings.Contains(group, want) {
			t.Errorf("the group lacks %q", want)
		}
	}
	if strings.Count(body, `id="tool-mail"`) != 1 {
		t.Fatal("a row twice")
	}
}
