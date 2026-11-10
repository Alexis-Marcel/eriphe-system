package main

import (
	"slices"
	"strings"
)

// The tools list, grouped, as a phone's Settings → Accounts:
//
//   - each account is a card: a Google account, an Apple account, a
//     mailbox… with its address, who gives it, and a row for each of its
//     services (Mail, Agenda, Fichiers) — the same row as a lone tool: its
//     state, its access, edit, remove. A connected account's card has its
//     actions: reconnect, remove, add the tools it does not have yet.
//     The servers stay one per service (accounts.go): only the list is by
//     account. An Apple calendar and a Google calendar of one address are
//     two cards;
//   - the other tools of one kind (GitHub…) make one card when there are
//     several, by account; a kind with one tool stays a plain row, a tool
//     of no preset is its own.

// toolGroup is one entry of the list.
type toolGroup struct {
	Preset   toolPreset
	Single   *toolView // a kind with one tool: its row alone
	Accounts []toolAccount

	// An account's card: its address, who gives it (google, icloud…).
	Owner        string
	Provider     string
	ProviderName string // "Google", "iCloud"; "" for an IMAP or CalDAV of no name
	// Connection is the account signed in at ConnProvider, for a card of
	// a connected account (connections.go); Others, the kinds of tools it
	// could add.
	Connection   string
	ConnProvider string
	Reconnect    bool
	Others       []string
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

// Key names an account's card in its ids.
func (g toolGroup) Key() string { return rowKey("acct", g.Provider+":"+g.Owner) }

// Tools are an account's services, in their order: mail, calendar, files.
func (g toolGroup) Tools() []toolView {
	var out []toolView
	for _, a := range g.Accounts {
		out = append(out, a.Tools...)
	}
	slices.SortStableFunc(out, func(x, y toolView) int {
		return slices.Index(toolKinds, x.Part) - slices.Index(toolKinds, y.Part)
	})
	return out
}

// Services are the names of an account's services, for its card's line.
func (g toolGroup) Services() []string {
	var out []string
	for _, t := range g.Tools() {
		out = append(out, t.Label)
	}
	return out
}

// AddURL is where "Ajouter un compte" leads: the preset's page, or its
// form; AddStep tells a form that opens in the dialog.
func (g toolGroup) AddURL() string { return g.Preset.Page() }

func (g toolGroup) AddStep() bool { return strings.HasPrefix(g.AddURL(), "/settings/tools/new?") }

// accountLabel is the account a tool is for, from its line: what follows
// its last " · " ("GitHub · via l'app · acme").
func accountLabel(t toolView) string {
	if t.Connection != "" {
		return t.Connection
	}
	if i := strings.LastIndex(t.Detail, " · "); i >= 0 {
		return t.Detail[i+len(" · "):]
	}
	return t.Name
}

// ownerKey is the card of an account a row is a service of; "" for a tool.
func ownerKey(t toolView) string {
	if t.Part == "" || t.Owner == "" {
		return ""
	}
	return t.Provider + ":" + strings.ToLower(t.Owner)
}

// groupFamily is the kind a tool is grouped by: its preset; "" for a tool
// of no preset.
func groupFamily(t toolView) string {
	if t.Preset.ID == "remote" || t.Preset.ID == "local" {
		return ""
	}
	return t.Preset.ID
}

// groupTools groups the list, keeping the order in which accounts and
// kinds first come.
func groupTools(tools []toolView) []toolGroup {
	var out []toolGroup
	index := map[string]int{}
	for _, t := range tools {
		if key := ownerKey(t); key != "" {
			i, ok := index["acct "+key]
			if !ok {
				name := t.Detail // the provider, for a mailbox or a calendar on a password
				switch {
				case t.Connection != "":
					name = providerLabel(t.ConnProvider)
				case signInProviders[t.Provider] != nil: // a mailbox on a password at a provider one signs in at
					name = providerLabel(t.Provider)
				case t.Provider == "other" || t.Provider == "caldav":
					name = ""
				}
				out = append(out, toolGroup{Owner: t.Owner, Provider: t.Provider, ProviderName: name, Accounts: []toolAccount{{Label: t.Owner}}})
				i = len(out) - 1
				index["acct "+key] = i
			}
			g := &out[i]
			g.Accounts[0].Tools = append(g.Accounts[0].Tools, t)
			if t.Connection != "" {
				g.Connection, g.ConnProvider = t.Connection, t.ConnProvider
				g.Reconnect = g.Reconnect || t.Reconnect
			}
			continue
		}
		fam := groupFamily(t)
		i, ok := index[fam]
		if fam == "" || !ok {
			out = append(out, toolGroup{Preset: t.Preset})
			i = len(out) - 1
			if fam != "" {
				index[fam] = i
			}
		}
		g := &out[i]
		label := accountLabel(t)
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
		g := &out[i]
		if g.Owner != "" {
			if g.Connection != "" {
				for _, s := range toolsAt(g.ConnProvider) {
					if !slices.ContainsFunc(g.Accounts[0].Tools, func(t toolView) bool { return t.Part == s }) {
						g.Others = append(g.Others, s)
					}
				}
			}
			continue
		}
		if g.Count() == 1 {
			t := g.Accounts[0].Tools[0]
			g.Single = &t
		}
	}
	return out
}
