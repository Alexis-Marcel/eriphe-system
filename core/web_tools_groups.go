package main

import (
	"strings"
)

// The tools list, grouped: the tools of one kind (Mail, Agenda, GitHub,
// Google…, by the preset they come from) make one card when there are
// several, listing each account with its tools (the same row as a lone
// tool: its state, its access, reconnect, edit, remove) and "Ajouter un
// compte". A kind with one tool stays a plain row; a tool of no preset is
// its own. Several tools of one account (a Google account's Gmail and
// Agenda) are under that account.

// toolGroup is one entry of the list.
type toolGroup struct {
	Preset   toolPreset
	Single   *toolView // a kind with one tool: its row alone
	Accounts []toolAccount
}

// toolAccount is one account of a group, and its tools.
type toolAccount struct {
	Label     string // the address, the GitHub account…; the tool's name when nothing says
	Tools     []toolView
	Reconnect bool // an account to connect again (a Google account)
}

// Count is how many tools the group holds.
func (g toolGroup) Count() int {
	n := 0
	for _, a := range g.Accounts {
		n += len(a.Tools)
	}
	return n
}

// AddURL is where "Ajouter un compte" leads: the preset's page, or its
// form; AddStep tells a form that opens in the dialog.
func (g toolGroup) AddURL() string {
	if g.Preset.ID == "gdrive" {
		return "/settings/google?add=drive"
	}
	return g.Preset.Page()
}

func (g toolGroup) AddStep() bool { return strings.HasPrefix(g.AddURL(), "/settings/tools/new?") }

// accountLabel is the account a tool is for, from its line: what follows
// its last " · " ("Gmail · sam@example.org", "GitHub · via l'app · acme").
func accountLabel(t toolView) string {
	if t.Google != "" {
		return t.Google
	}
	if i := strings.LastIndex(t.Detail, " · "); i >= 0 {
		return t.Detail[i+len(" · "):]
	}
	return t.Name
}

// groupFamily is the kind a tool is grouped by: its preset, Drive with the
// Google accounts once there are some; "" for a tool of no preset.
func groupFamily(t toolView, google bool) string {
	switch {
	case t.Preset.ID == "gdrive" && google:
		return "google"
	case t.Preset.ID == "remote" || t.Preset.ID == "local":
		return ""
	}
	return t.Preset.ID
}

// groupTools groups the list, keeping the order in which kinds first come.
func groupTools(lang string, tools []toolView) []toolGroup {
	google := false
	for _, t := range tools {
		google = google || t.Preset.ID == "google"
	}
	var out []toolGroup
	index := map[string]int{}
	for _, t := range tools {
		fam := groupFamily(t, google)
		i, ok := index[fam]
		if fam == "" || !ok {
			p := t.Preset
			if fam == "google" {
				p, _ = presetByID("google")
			}
			out = append(out, toolGroup{Preset: p})
			i = len(out) - 1
			if fam != "" {
				index[fam] = i
			}
		}
		g := &out[i]
		label := accountLabel(t)
		if t.Preset.ID == "gdrive" {
			label = tr(lang, "preset.gdrive.title") // its folders, of every account
		}
		j := -1
		for k, a := range g.Accounts {
			if strings.EqualFold(a.Label, label) {
				j = k
			}
		}
		if j < 0 {
			g.Accounts = append(g.Accounts, toolAccount{Label: label})
			j = len(g.Accounts) - 1
		}
		g.Accounts[j].Tools = append(g.Accounts[j].Tools, t)
		g.Accounts[j].Reconnect = g.Accounts[j].Reconnect || t.Reconnect
	}
	for i := range out {
		if out[i].Count() == 1 {
			t := out[i].Accounts[0].Tools[0]
			out[i].Single = &t
		}
	}
	return out
}
