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
	Form  string // generic, mail or calendar
	Name  string // the catalog name it suggests
	Mark  string // the two letters of its badge
	Kind  string // remote or local
	URL   string
	Auth  string // none, bearer or oauth
	Trust bool   // the default of "what Eriphe may do without asking": reads free
	// The address differs per person (Home Assistant's host): the form asks for it.
	AskURL bool
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
	// servers/files: the folders given to Eriphe in Google Drive. Its page
	// connects the account and picks the folders (web_drive.go).
	{ID: "gdrive", Form: "drive", Name: filesServer, Mark: "GD", Kind: "local", Trust: true},
	{ID: "remote", Form: "generic", Mark: "↗", Kind: "remote", Auth: "none"},
	{ID: "local", Form: "generic", Mark: "▣", Kind: "local"},
}

// Page is where the preset's tile leads.
func (p toolPreset) Page() string {
	switch {
	case p.Form == "drive":
		return "/settings/drive"
	case p.ID == "home-assistant":
		return "/settings/home-assistant" // both ways: its tools, and its voice (web_homeassistant.go)
	}
	return "/settings/tools/new?preset=" + p.ID
}

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
	return toolPreset{}, false
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

// Mail: eriphe-mail with one account and a password. The user picks the
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
}

var mailChoices = []mailChoice{
	{ID: "gmail", Provider: "gmail", AppPass: true, Link: "https://myaccount.google.com/apppasswords"},
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

type mailAccount struct {
	Name     string        `json:"name"`
	Address  string        `json:"address"`
	Provider string        `json:"provider,omitempty"`
	Username string        `json:"username,omitempty"`
	IMAP     *mailEndpoint `json:"imap,omitempty"`
	SMTP     *mailEndpoint `json:"smtp,omitempty"`
	Auth     struct {
		PasswordEnv string `json:"password_env"`
	} `json:"auth"`
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
// was typed.
func (f *mailForm) normalize(old *RunSpec) error {
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
	if c.Soon {
		// Kept as it is on an edit; not added.
		if prev, ok := mailFormOf(deref(old)); !ok || prev.Provider != c.ID {
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

func deref(s *RunSpec) RunSpec {
	if s == nil {
		return RunSpec{}
	}
	return *s
}

// mailRun builds eriphe-mail's run spec: MAIL_CONFIG with one account, its
// password from the vault (bound by the catalog).
func (f mailForm) run(old *RunSpec) (RunSpec, error) {
	if err := f.normalize(old); err != nil {
		return RunSpec{}, err
	}
	a := mailAccount{Name: "mail", Address: f.Address}
	a.Auth.PasswordEnv = mailSecretEnv
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
	raw, _ := json.Marshal(map[string]any{"accounts": []mailAccount{a}})
	spec := RunSpec{Image: mailImage, Transport: transportStdio, Env: map[string]EnvValue{"MAIL_CONFIG": {Value: string(raw)}}}
	if old != nil {
		keepOptions(&spec, *old, mailSecretEnv)
	}
	return spec, nil
}

// mailFormOf reads the form back from a mail server's spec, if it is one.
func mailFormOf(spec RunSpec) (mailForm, bool) {
	if !strings.HasPrefix(spec.Image, mailImage) {
		return mailForm{}, false
	}
	var cfg struct {
		Accounts []mailAccount `json:"accounts"`
	}
	if json.Unmarshal([]byte(spec.Env["MAIL_CONFIG"].Value), &cfg) != nil || len(cfg.Accounts) != 1 {
		return mailForm{}, false
	}
	a := cfg.Accounts[0]
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
	return f, a.Auth.PasswordEnv == mailSecretEnv
}

// Calendar: eriphe-calendar with one CalDAV account and an app password.
// Google and Microsoft need an OAuth token lent by eriphe-core, which it
// does not do yet: they are shown as coming soon.
type calChoice struct {
	ID       string
	NeedsURL bool   // the server's address is asked
	Link     string // where the app password is made
	Soon     bool
}

var calChoices = []calChoice{
	{ID: "icloud", Link: "https://account.apple.com/account/manage"},
	{ID: "fastmail", Link: "https://app.fastmail.com/settings/security"},
	{ID: "nextcloud", NeedsURL: true},
	{ID: "caldav", NeedsURL: true},
	{ID: "google", Soon: true},
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

type calendarAccount struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	URL      string `json:"url,omitempty"`
	Username string `json:"username"`
	Auth     struct {
		PasswordEnv string `json:"password_env"`
	} `json:"auth"`
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

func (f calendarForm) run(old *RunSpec) (RunSpec, error) {
	if err := f.normalize(); err != nil {
		return RunSpec{}, err
	}
	a := calendarAccount{Name: f.Provider, Provider: f.Provider, Username: f.Username, URL: f.URL}
	a.Auth.PasswordEnv = calSecretEnv
	raw, _ := json.Marshal(map[string]any{"timezone": f.Timezone, "accounts": []calendarAccount{a}})
	spec := RunSpec{Image: calendarImage, Transport: transportStdio, Env: map[string]EnvValue{"CALENDAR_CONFIG": {Value: string(raw)}}}
	if old != nil {
		keepOptions(&spec, *old, calSecretEnv)
	}
	return spec, nil
}

func calendarFormOf(spec RunSpec) (calendarForm, bool) {
	if !strings.HasPrefix(spec.Image, calendarImage) {
		return calendarForm{}, false
	}
	var cfg struct {
		Timezone string            `json:"timezone"`
		Accounts []calendarAccount `json:"accounts"`
	}
	if json.Unmarshal([]byte(spec.Env["CALENDAR_CONFIG"].Value), &cfg) != nil || len(cfg.Accounts) != 1 {
		return calendarForm{}, false
	}
	a := cfg.Accounts[0]
	c, ok := calChoiceByID(a.Provider)
	return calendarForm{Provider: a.Provider, URL: a.URL, Username: a.Username, Timezone: cfg.Timezone},
		a.Auth.PasswordEnv == calSecretEnv && ok && !c.Soon
}

// keepOptions carries over, on an edit, what the simple form does not
// show: placement, volume, the password's vault reference.
func keepOptions(spec *RunSpec, old RunSpec, secretEnv string) {
	spec.Node, spec.Needs, spec.Prefers, spec.Volume, spec.Egress = old.Node, old.Needs, old.Prefers, old.Volume, old.Egress
	if v, ok := old.Env[secretEnv]; ok && v.File != "" {
		spec.Env[secretEnv] = v
	}
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
