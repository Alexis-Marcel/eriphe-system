package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Presets of the workspace: common tools a user picks from a list, with
// everything filled in but the credential and, where it differs per
// person, the address. The URLs were checked against each vendor's own
// documentation (October 2026); the user can still edit them.

type toolPreset struct {
	ID    string
	Link  string // a tile that is no tool's form: the page it opens
	Form  string // generic, account, or connection
	Card  string // Form account: the kind of tool whose card makes the account (tool_cards.go)
	Name  string // the catalog name it suggests
	Mark  string // the two letters of its badge
	Kind  string // remote or local
	URL   string
	Auth  string // none, bearer or oauth
	Trust bool   // the default of "what Eriphe may do without asking": reads free
	// The address differs per person (a host of their own): the form asks
	// for it, and a tool whose address ends with URL's path is this preset's.
	AskURL bool
	// The gallery: its category (presets_online.go) and its tags.
	Category  string
	Paid      bool // a paid plan of the service is needed
	Beta      bool // the service's server is in beta
	ReadsOnly bool // the service only reads
	// A preset of Form "connection" is an account signed in at Provider
	// (a signInProviders name) for its Tools (toolKinds; none: all the
	// provider serves): its tile leads to the connect page.
	Provider string
	Tools    []string
}

var toolPresets = []toolPreset{
	// https://linear.app/docs/mcp : streamable HTTP, OAuth 2.1 with dynamic
	// client registration.
	{ID: "linear", Form: "generic", Name: "linear", Mark: "Li", Kind: "remote",
		URL: "https://mcp.linear.app/mcp", Auth: "oauth", Trust: true},
	// https://developers.notion.com/docs/get-started-with-mcp : streamable
	// HTTP, OAuth.
	{ID: "notion", Form: "generic", Name: "notion", Mark: "No", Kind: "remote",
		URL: "https://mcp.notion.com/mcp", Auth: "oauth", Trust: true},
	// https://github.com/github/github-mcp-server : the remote server takes a
	// personal access token as a bearer token; it has no dynamic client
	// registration.
	{ID: "github", Form: "generic", Name: "github", Mark: "GH", Kind: "remote",
		URL: "https://api.githubcopilot.com/mcp/", Auth: "bearer", Trust: true},
	// https://www.home-assistant.io/integrations/mcp_server/ : the MCP Server
	// integration serves /api/mcp; a long-lived access token (profile →
	// security) is a bearer token like for the rest of Home Assistant's API.
	// Its tools carry readOnlyHint (GetLiveContext reads, the intents act).
	{ID: "home-assistant", Form: "generic", Name: "maison", Mark: "HA", Kind: "remote",
		URL: "http://<ip>/api/mcp", Auth: "bearer", Trust: true, AskURL: true},
	// servers/mail and servers/calendar of this repository: their reading
	// tools are marked read-only, the others are not.
	{ID: "mail", Form: "account", Card: toolMail, Name: "mail", Mark: "@", Kind: "local", Trust: true},
	{ID: "calendar", Form: "account", Card: toolCalendar, Name: "agenda", Mark: "31", Kind: "local", Trust: true},
	// Accounts signed in at a provider (connection_tools.go): one sign-in
	// for the tools ticked; a tool with places has them picked on its page.
	{ID: "gdrive", Form: "connection", Provider: "google", Tools: []string{toolFiles}, Name: "fichiers", Mark: "GD", Kind: "local", Trust: true},
	{ID: "google", Form: "connection", Provider: "google", Name: "google", Mark: "G", Kind: "local", Trust: true},
	{ID: "remote", Form: "generic", Mark: "↗", Kind: "remote", Auth: "none"},
	{ID: "local", Form: "generic", Mark: "▣", Kind: "local"},
}

// connectionPreset is the preset of an account signed in at a provider
// for a kind of tool: the one for that kind alone, else the provider's.
func connectionPreset(provider, kind string) (toolPreset, bool) {
	var out toolPreset
	found := false
	for _, p := range toolPresets {
		if p.Form != "connection" || p.Provider != provider {
			continue
		}
		if len(p.Tools) == 1 && p.Tools[0] == kind {
			return p, true
		}
		if len(p.Tools) == 0 && !found {
			out, found = p, true
		}
	}
	return out, found
}

// Page is where the preset's tile leads.
func (p toolPreset) Page() string {
	switch {
	case p.Link != "":
		return p.Link
	case p.Form == "connection":
		return connectPage(p.Provider, p.Tools...)
	case p.ID == "remote":
		return "/settings/tools/add/other" // its address first: Eriphe finds how to sign in (discover.go)
	}
	return "/settings/tools/new?preset=" + p.ID
}

// Scoped tells a service whose OAuth scopes follow the access level: a
// read-only token for "read only" (access.go).
func (p toolPreset) Scoped() bool { _, ok := tokenScopes[p.ID]; return ok }

// Open tells a vendor's server that answers without an account.
func (p toolPreset) Open() bool { return p.FixedURL() && p.Auth == "none" }

// FixedURL tells a vendor-hosted service, whose address is never asked.
func (p toolPreset) FixedURL() bool { return p.Kind == "remote" && p.URL != "" && !p.AskURL }

// Compact tells a preset whose form shows only what the user must give;
// the rest is under the advanced options. "Other…" keeps the full form.
func (p toolPreset) Compact() bool { return p.ID != "" && p.ID != "remote" && p.ID != "local" }

// Vendor tells a vendor's own service signed in with OAuth (Linear,
// Notion): its page asks only for the name, the use and the access, and
// its primary action connects.
func (p toolPreset) Vendor() bool { return p.FixedURL() && p.Auth == "oauth" }

// presetByURL finds the vendor-hosted preset of an address, for an edit.
func presetByURL(u string) (toolPreset, bool) {
	for _, p := range toolPresets {
		if p.FixedURL() && p.URL == u {
			return p, true
		}
	}
	for _, p := range toolPresets {
		if p.asked(u) {
			return p, true
		}
	}
	return toolPreset{}, false
}

// asked tells an address of a preset whose host is the user's own: any
// host, with the path of the preset's URL ("http://<ip>/api/mcp").
func (p toolPreset) asked(u string) bool {
	if !p.AskURL || p.Kind != "remote" {
		return false
	}
	_, rest, ok := strings.Cut(p.URL, "://")
	i := strings.Index(rest, "/")
	if !ok || i < 0 {
		return false
	}
	pu, err := url.Parse(u)
	return err == nil && pu.Host != "" && strings.TrimSuffix(pu.Path, "/") == strings.TrimSuffix(rest[i:], "/")
}

func presetByID(id string) (toolPreset, bool) {
	for _, p := range toolPresets {
		if p.ID == id {
			return p, true
		}
	}
	return toolPreset{}, false
}

const ()

func marshalRun(spec RunSpec) json.RawMessage {
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("run spec: %v", err))
	}
	return raw
}

// slugName turns what the user typed into a catalog name: lowercase,
// accents dropped, spaces and other separators as dashes ("Mail Pro" →
// "mail-pro"). What is left is checked by the catalog.
func slugName(s string) string {
	repl := strings.NewReplacer("à", "a", "â", "a", "ä", "a", "á", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
		"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u", "ÿ", "y", "ñ", "n",
		"œ", "oe", "æ", "ae")
	s = repl.Replace(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
