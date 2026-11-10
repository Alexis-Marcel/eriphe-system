package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Presets of the workspace: common tools a user picks from a list, with
// everything filled in but the credential and, where it differs per
// person, the address. The URLs were checked against each vendor's own
// documentation (October 2026); the user can still edit them.

type toolPreset struct {
	ID    string
	Link  string // a tile that is no tool's form: the page it opens
	Form  string // generic, mail or calendar
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
	{ID: "mail", Form: "mail", Name: "mail", Mark: "@", Kind: "local", Trust: true},
	{ID: "calendar", Form: "calendar", Name: "agenda", Mark: "31", Kind: "local", Trust: true},
	// Accounts signed in at a provider (connection_tools.go): one sign-in
	// for the tools ticked; a tool with places has them picked on its page.
	{ID: "gdrive", Form: "connection", Provider: "google", Tools: []string{toolDrive}, Name: filesServer, Mark: "GD", Kind: "local", Trust: true},
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

const (
	mailImage     = "ghcr.io/alexis-marcel/eriphe-mail"
	calendarImage = "ghcr.io/alexis-marcel/eriphe-calendar"
	mailSecretEnv = "MAIL_PASSWORD"
	calSecretEnv  = "CALENDAR_PASSWORD"
)

// Mail: an account of eriphe-mail with a password (accounts.go). The user picks the
// kind of mailbox first; each one has its own form. Checked against each
// provider's help (October 2026):
//   - Gmail: an app password needs 2-Step Verification
//     (support.google.com/accounts/answer/185833).
//   - iCloud: an app-specific password needs two-factor authentication
//     (support.apple.com/102654).
//   - Outlook.com, Hotmail and Microsoft 365 refuse passwords since
//     September 2024 (support.microsoft.com, "Modern Authentication Methods
//     now needed"): they wait for OAuth tokens lent by eriphe-core.
//   - Orange: a "mot de passe pour logiciels et applications de messagerie
//     tiers", made in the Espace client.
//   - Free and OVH: the mailbox's own password; Free is not a preset of
//     eriphe-mail, so its servers are written out.
//   - Fastmail: an app password with mail access (not on the Basic plan).
type mailChoice struct {
	ID       string // gmail … other
	Provider string // eriphe-mail's preset, "" when the servers are written out
	IMAP     string // the servers written out (Free)
	SMTP     string
	AppPass  bool   // the provider makes app passwords
	Link     string // where the password is made
	Soon     bool   // not supported yet
	Hidden   bool   // not offered, only read back
	Page     string // connected on a page of its own (an account signed in), not with the form
	// SpacedPassword: the provider shows its app passwords in groups of
	// four letters; the spaces copied with them are not part of it.
	SpacedPassword bool
}

var mailChoices = []mailChoice{
	{ID: "google", Page: connectPage("google", toolMail)},
	{ID: "gmail", Provider: "gmail", AppPass: true, SpacedPassword: true, Link: "https://myaccount.google.com/apppasswords"},
	{ID: "icloud", Provider: "icloud", AppPass: true, Link: "https://account.apple.com/account/manage"},
	{ID: "outlook", Provider: "outlook", Soon: true},
	{ID: "orange", Provider: "orange", AppPass: true, Link: "https://espace-client.orange.fr/"},
	{ID: "free", IMAP: "imap.free.fr", SMTP: "smtp.free.fr"},
	{ID: "ovh", Provider: "ovh", Link: "https://www.ovh.com/manager/"},
	{ID: "fastmail", Provider: "fastmail", AppPass: true, Link: "https://app.fastmail.com/settings/security"},
	{ID: "other"},
	{ID: "office365", Provider: "office365", Soon: true, Hidden: true},
}

func mailChoiceByID(id string) (mailChoice, bool) {
	for _, c := range mailChoices {
		if c.ID == id {
			return c, true
		}
	}
	return mailChoice{}, false
}

// imapHost is where a mailbox's IMAP server is, for the connection test:
// eriphe-mail's presets (servers/mail/config.go), or the servers given.
func (f mailForm) imapEndpoint() (string, int) {
	hosts := map[string]string{"gmail": "imap.gmail.com", "icloud": "imap.mail.me.com", "outlook": "outlook.office365.com",
		"office365": "outlook.office365.com", "orange": "imap.orange.fr", "ovh": "ssl0.ovh.net", "fastmail": "imap.fastmail.com"}
	c, _ := mailChoiceByID(f.Provider)
	switch {
	case c.IMAP != "":
		return c.IMAP, 993
	case f.Provider == "other":
		p, _ := strconv.Atoi(f.IMAPPort)
		if p == 0 {
			p = 993
		}
		return f.IMAPHost, p
	}
	return hosts[c.Provider], 993
}

func guessMailProvider(address string) string {
	_, domain, _ := strings.Cut(strings.ToLower(address), "@")
	switch {
	case domain == "gmail.com" || domain == "googlemail.com":
		return "gmail"
	case domain == "icloud.com" || domain == "me.com" || domain == "mac.com":
		return "icloud"
	case strings.HasPrefix(domain, "outlook.") || strings.HasPrefix(domain, "hotmail.") ||
		strings.HasPrefix(domain, "live.") || domain == "msn.com":
		return "outlook"
	case domain == "orange.fr" || domain == "wanadoo.fr":
		return "orange"
	case domain == "free.fr":
		return "free"
	case domain == "fastmail.com" || domain == "fastmail.fm":
		return "fastmail"
	}
	return ""
}

type mailEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
}

type mailForm struct {
	Address  string
	Provider string // a mailChoice ID; "" to guess it from the address
	IMAPHost string // "other" only
	SMTPHost string
	IMAPPort string // "other" only, advanced: 993 by default
	SMTPPort string // 465 by default
	Username string // "other" only, advanced: the address by default
}

// Choice is the form's kind of mailbox.
func (f mailForm) Choice() mailChoice { c, _ := mailChoiceByID(f.Provider); return c }

// same tells whether two forms name the same mailbox: if not, a new
// password is needed.
func (f mailForm) same(o mailForm) bool {
	return f.Provider == o.Provider && strings.EqualFold(f.Address, o.Address) && f.IMAPHost == o.IMAPHost &&
		f.SMTPHost == o.SMTPHost && f.Username == o.Username
}

func port(s string, def int) (int, bool) {
	if s = strings.TrimSpace(s); s == "" {
		return def, true
	}
	p, err := strconv.Atoi(s)
	return p, err == nil && p > 0 && p < 65536
}

// normalize fills the provider guessed from the address and checks what
// was typed; old is the mailbox edited, if any.
func (f *mailForm) normalize(old *svcAccount) error {
	addr, err := mail.ParseAddress(strings.TrimSpace(f.Address))
	if err != nil || !strings.Contains(addr.Address, "@") {
		return errors.New("presets.badaddress")
	}
	f.Address = addr.Address
	if f.Provider == "" {
		if f.Provider = guessMailProvider(f.Address); f.Provider == "" {
			return errors.New("presets.noprovider")
		}
	}
	c, ok := mailChoiceByID(f.Provider)
	if !ok {
		return errors.New("presets.noprovider")
	}
	if c.Page != "" {
		return errors.New("presets.google")
	}
	if c.Soon {
		// Kept as it is on an edit; not added.
		if old == nil {
			return errors.New("presets.soon")
		}
		if prev, ok := mailFormOf(*old); !ok || prev.Provider != c.ID {
			return errors.New("presets.soon")
		}
	}
	if c.ID != "other" {
		f.IMAPHost, f.SMTPHost, f.IMAPPort, f.SMTPPort, f.Username = "", "", "", "", ""
		return nil
	}
	if f.IMAPHost == "" || f.SMTPHost == "" || strings.ContainsAny(f.IMAPHost+f.SMTPHost, " /:") {
		return errors.New("presets.badhosts")
	}
	if _, ok := port(f.IMAPPort, 993); !ok {
		return errors.New("form.badport")
	}
	if _, ok := port(f.SMTPPort, 465); !ok {
		return errors.New("form.badport")
	}
	return nil
}

// account is the mailbox as eriphe-mail reads it, named by its address;
// its password's variable is given by putAccount.
func (f mailForm) account(old *svcAccount) (svcAccount, error) {
	if err := f.normalize(old); err != nil {
		return svcAccount{}, err
	}
	a := svcAccount{Name: f.Address, Address: f.Address}
	switch c := f.Choice(); {
	case c.ID == "other":
		ip, _ := port(f.IMAPPort, 993)
		sp, _ := port(f.SMTPPort, 465)
		a.IMAP, a.SMTP, a.Username = &mailEndpoint{Host: f.IMAPHost, Port: ip}, &mailEndpoint{Host: f.SMTPHost, Port: sp}, f.Username
	case c.IMAP != "":
		a.IMAP, a.SMTP = &mailEndpoint{Host: c.IMAP, Port: 993}, &mailEndpoint{Host: c.SMTP, Port: 465}
	default:
		a.Provider = c.Provider
	}
	return a, nil
}

// mailFormOf reads the form back from a mailbox of eriphe-mail; ok is
// false for one on a connection's token (Settings → Tools, not this form).
func mailFormOf(a svcAccount) (mailForm, bool) {
	f := mailForm{Address: a.Address, Provider: a.Provider}
	if a.IMAP != nil && a.SMTP != nil {
		f.Provider = "other"
		for _, c := range mailChoices {
			if c.IMAP != "" && c.IMAP == a.IMAP.Host && c.SMTP == a.SMTP.Host && a.Username == "" {
				f.Provider = c.ID
			}
		}
		if f.Provider == "other" {
			f.IMAPHost, f.SMTPHost, f.Username = a.IMAP.Host, a.SMTP.Host, a.Username
			if a.IMAP.Port != 0 && a.IMAP.Port != 993 {
				f.IMAPPort = strconv.Itoa(a.IMAP.Port)
			}
			if a.SMTP.Port != 0 && a.SMTP.Port != 465 {
				f.SMTPPort = strconv.Itoa(a.SMTP.Port)
			}
		}
	} else if f.Provider == "" {
		f.Provider = guessMailProvider(a.Address)
	}
	return f, !a.signedIn()
}

// Calendar: a CalDAV account of eriphe-calendar with an app password
// (accounts.go).
// A calendar on a connection is added from Settings → Tools, with the
// account (web_connections.go); Microsoft needs an OAuth token lent by
// eriphe-core, which it does not do yet: it is shown as coming soon.
type calChoice struct {
	ID       string
	NeedsURL bool   // the server's address is asked
	Link     string // where the app password is made
	Soon     bool
	Page     string // connected on a page of its own (an account signed in), not with the form
}

var calChoices = []calChoice{
	{ID: "icloud", Link: "https://account.apple.com/account/manage"},
	{ID: "fastmail", Link: "https://app.fastmail.com/settings/security"},
	{ID: "nextcloud", NeedsURL: true},
	{ID: "caldav", NeedsURL: true},
	{ID: "google", Page: connectPage("google", toolCalendar)},
	{ID: "microsoft", Soon: true},
}

func calChoiceByID(id string) (calChoice, bool) {
	for _, c := range calChoices {
		if c.ID == id {
			return c, true
		}
	}
	return calChoice{}, false
}

// calDAVBase is the address eriphe-calendar starts from, for the test
// (servers/calendar/config.go).
func (f calendarForm) davURL() string {
	switch f.Provider {
	case "icloud":
		return "https://caldav.icloud.com/"
	case "fastmail":
		return "https://caldav.fastmail.com/dav/"
	}
	return f.URL
}

type calendarForm struct {
	Provider string
	URL      string
	Username string
	Timezone string
}

// Choice is the form's calendar service.
func (f calendarForm) Choice() calChoice { c, _ := calChoiceByID(f.Provider); return c }

func (f calendarForm) same(o calendarForm) bool {
	return f.Provider == o.Provider && f.URL == o.URL && strings.EqualFold(f.Username, o.Username)
}

// guessCalendarProvider finds a calendar's provider from its username.
func guessCalendarProvider(username string) string {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(username)), "@")
	switch domain {
	case "icloud.com", "me.com", "mac.com":
		return "icloud"
	case "fastmail.com", "fastmail.fm":
		return "fastmail"
	}
	return ""
}

func (f *calendarForm) normalize() error {
	f.Username = strings.TrimSpace(f.Username)
	if f.Provider == "" { // automatic, for the API's sake
		if f.Provider = guessCalendarProvider(f.Username); f.Provider == "" {
			if f.Username == "" {
				return errors.New("presets.nouser")
			}
			return errors.New("presets.calnoprovider")
		}
	}
	c, ok := calChoiceByID(f.Provider)
	if !ok {
		return errors.New("presets.noprovider")
	}
	if c.Soon {
		return errors.New("presets.soon")
	}
	if c.Page != "" {
		return errors.New("presets.google")
	}
	if f.Username == "" {
		return errors.New("presets.nouser")
	}
	if !c.NeedsURL {
		f.URL = ""
	} else {
		u, err := url.Parse(strings.TrimSpace(f.URL))
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("presets.badcaldav")
		}
		f.URL = u.String()
	}
	if f.Timezone = strings.TrimSpace(f.Timezone); f.Timezone == "" {
		f.Timezone = "Europe/Paris"
	}
	if _, err := time.LoadLocation(f.Timezone); err != nil {
		return errors.New("presets.badtz")
	}
	return nil
}

// account is the calendar account as eriphe-calendar reads it, named by
// its username, with its server's host when the username is not an
// address (a Nextcloud "sam"); its password's variable is given by
// putAccount.
func (f calendarForm) account() (svcAccount, error) {
	if err := f.normalize(); err != nil {
		return svcAccount{}, err
	}
	name := f.Username
	if u, err := url.Parse(f.URL); !strings.Contains(name, "@") && err == nil && u.Host != "" {
		name += "@" + u.Hostname()
	}
	return svcAccount{Name: name, Provider: f.Provider, Username: f.Username, URL: f.URL}, nil
}

// calendarFormOf reads the form back from an account of eriphe-calendar;
// ok is false for one this form does not make (Google, Microsoft).
func calendarFormOf(a svcAccount, zone string) (calendarForm, bool) {
	c, ok := calChoiceByID(a.Provider)
	return calendarForm{Provider: a.Provider, URL: a.URL, Username: a.Username, Timezone: zone},
		!a.signedIn() && ok && !c.Soon && c.Page == ""
}

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
