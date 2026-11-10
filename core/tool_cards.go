package main

// Tool cards: what each of the catalog's local tools is, as data. A card
// gives the image eriphe-core launches and what the tool declares (the
// contract of servers/README.md): whether each account has its own level,
// how an account is signed in at a provider, whether it has places. core
// reads the cards, never an image or a tool's name in its logic
// (TestNoToolImageInLogic): another image of mail is another card.

// toolCard is one tool of the catalog that eriphe-core launches and
// configures, with several accounts.
type toolCard struct {
	ID     string // its kind of tool (toolKinds): what a connection serves
	Base   string // the catalog name it is made under
	Image  string // without its tag
	Preset string // the preset of its words (preset.<id>.use) and of its icon
	// OwnVersion: one of Eriphe's own images, which runs the version of
	// eriphe-core (pinnedImage, services.go).
	OwnVersion bool
	// Scopes: its configuration holds the scopes each account was granted.
	Scopes bool
	// AccountLevels: each account has its own access level (a mailbox read
	// only beside another free); without, the server's level holds for all.
	AccountLevels bool
	// SameBy: two accounts of one name are one account when these keys of
	// theirs are equal too (a calendar is also where it is); none: one name
	// is one account (a mailbox is its address).
	SameBy []string
	// SignIns are what it needs of an account signed in at a provider.
	SignIns []toolSignIn
	// Places: it has places, kept in its configuration (places.go).
	Places *placeKeeper
	// Cases are the categories its tools can fall in (access_grid.go),
	// the only rows of its grid; none: all of them.
	Cases []string
	// Account is its account's form (account_forms.go); nil: its accounts
	// are connections only.
	Account *accountShape
}

// cases are the categories of its grid.
func (t toolCard) cases() []string {
	if len(t.Cases) == 0 {
		return categories
	}
	return t.Cases
}

// The kinds of tools a connection can serve.
const (
	toolMail     = "mail"     // mailService
	toolCalendar = "calendar" // calendarService
	toolFiles    = "files"    // filesService
)

// toolKinds are the kinds, in the order they are shown.
var toolKinds = []string{toolMail, toolCalendar, toolFiles}

var (
	mailService = toolCard{ID: toolMail, Base: "mail", Image: "ghcr.io/alexis-marcel/eriphe-mail", Preset: "mail",
		OwnVersion: true, AccountLevels: true,
		SignIns: []toolSignIn{
			// Gmail over IMAP and SMTP with XOAUTH2: the whole mailbox;
			// Eriphe holds the account's level itself.
			{Provider: "google", Read: []string{scopeMail}, AccountProvider: "gmail", AccountFields: []string{"address"}},
		},
		Account: &mailShape}
	calendarService = toolCard{ID: toolCalendar, Base: "agenda", Image: "ghcr.io/alexis-marcel/eriphe-calendar", Preset: "calendar",
		OwnVersion: true, AccountLevels: true, SameBy: []string{"provider", "url"},
		SignIns: []toolSignIn{
			// Google Calendar over CalDAV.
			{Provider: "google", Read: []string{scopeCalendar}, AccountProvider: "google", AccountFields: []string{"username"}},
		},
		Account: &calendarShape}
	filesService = toolCard{ID: toolFiles, Base: "fichiers", Image: "ghcr.io/alexis-marcel/eriphe-files", Preset: "gdrive",
		OwnVersion: true, Scopes: true, AccountLevels: true,
		SignIns: []toolSignIn{
			// Google Drive: read only, or also add, modify and trash files.
			{Provider: "google", Read: []string{scopeDriveReadonly}, Write: []string{scopeDrive}, AccountProvider: "google"},
		},
		// Nothing of a folder is sent anywhere: no Envoyer.
		Places: &placeKeeper{Key: "places", Browse: "browse_places", Arg: "folder"}, Cases: []string{catRead, catChange, catDelete}}

	// toolCards are the cards, in the order their kinds are shown.
	toolCards = []toolCard{mailService, calendarService, filesService}
)

// The mail's account: a mailbox of eriphe-mail with a password. The user
// picks the kind of mailbox first; each one has its own form. Checked
// against each provider's help (October 2026):
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
var mailShape = accountShape{
	Words: "mailp", Pick: "mailp.%s.domains", Note: "mailp.%s.note", Steps: "mailp.%s.steps",
	Soon: "mailp.%s.soonlong", Howto: "mailp.%s.howtotitle", SoonWhy: "pick.mail.soonwhy",
	ChoiceField: "mail_provider", NameBy: "address", GuessFrom: "address",
	Choices: []accountChoice{
		{ID: "google", SignIn: "google"},
		{ID: "gmail", Set: map[string]any{"provider": "gmail"}, AppPass: true, SpacedPassword: true, Link: "https://myaccount.google.com/apppasswords"},
		{ID: "icloud", Set: map[string]any{"provider": "icloud"}, AppPass: true, Link: "https://account.apple.com/account/manage"},
		{ID: "outlook", Set: map[string]any{"provider": "outlook"}, Soon: true},
		{ID: "orange", Set: map[string]any{"provider": "orange"}, AppPass: true, Link: "https://espace-client.orange.fr/"},
		{ID: "free", Set: map[string]any{"imap.host": "imap.free.fr", "imap.port": 993, "smtp.host": "smtp.free.fr", "smtp.port": 465}},
		{ID: "ovh", Set: map[string]any{"provider": "ovh"}, Link: "https://www.ovh.com/manager/"},
		{ID: "fastmail", Set: map[string]any{"provider": "fastmail"}, AppPass: true, Link: "https://app.fastmail.com/settings/security"},
		{ID: "other", Custom: true, Ask: []string{"imap_host", "smtp_host", "imap_port", "smtp_port", "mail_user"}},
		{ID: "office365", Set: map[string]any{"provider": "office365"}, Soon: true, Hidden: true},
	},
	Fields: []accountField{
		{Name: "address", Key: "address", Kind: "email", Label: "mail.address", Placeholder: "mailp.%s.placeholder", Else: "mailp.other.placeholder", Wide: true},
		{Name: "imap_host", Key: "imap.host", Kind: "host", Label: "mail.imap", Example: "imap.exemple.fr", Only: true},
		{Name: "smtp_host", Key: "smtp.host", Kind: "host", Label: "mail.smtp", Example: "smtp.exemple.fr", Only: true},
		{Name: "imap_port", Key: "imap.port", Kind: "port", Label: "mail.imapport", Example: "993", Default: "993", Only: true, Advanced: true},
		{Name: "smtp_port", Key: "smtp.port", Kind: "port", Label: "mail.smtpport", Hint: "mail.smtpporthint", Example: "465", Default: "465", Only: true, Advanced: true},
		{Name: "mail_user", Key: "username", Kind: "text", Label: "mail.username", Placeholder: "mail.usernameplaceholder", Only: true, Advanced: true, Wide: true},
	},
	Guess: map[string]string{"gmail.com": "gmail", "googlemail.com": "gmail", "icloud.com": "icloud", "me.com": "icloud", "mac.com": "icloud",
		"outlook.": "outlook", "hotmail.": "outlook", "live.": "outlook", "msn.com": "outlook",
		"orange.fr": "orange", "wanadoo.fr": "orange", "free.fr": "free", "fastmail.com": "fastmail", "fastmail.fm": "fastmail"},
	Password: passwordWords{Label: "mail.boxpassword", Hint: "mail.boxpasswordhint", AppLabel: "mail.password", AppHint: "mail.passwordhint",
		HintOf: "mailp.%s.passwordhint", HowtoNote: "mail.howto.safe"},
}

// The calendar's account: a CalDAV account of eriphe-calendar with an app
// password. A calendar on a connection is added with its account
// (connection_tools.go); Microsoft needs an OAuth token lent by
// eriphe-core, which it does not do yet: it is shown as coming soon.
var calendarShape = accountShape{
	Words: "calp", Pick: "calp.%s.desc", Note: "calp.%s.desc", Steps: "calp.%s.steps", Howto: "cal.howtotitle", SoonWhy: "pick.calendar.soonwhy",
	ChoiceField: "cal_provider", NameBy: "cal_user", NameHost: "cal_url", GuessFrom: "cal_user", NoChoice: "presets.calnoprovider",
	Choices: []accountChoice{
		{ID: "icloud", Set: map[string]any{"provider": "icloud"}, Link: "https://account.apple.com/account/manage"},
		{ID: "fastmail", Set: map[string]any{"provider": "fastmail"}, Link: "https://app.fastmail.com/settings/security"},
		{ID: "nextcloud", Set: map[string]any{"provider": "nextcloud"}, Ask: []string{"cal_url"}},
		{ID: "caldav", Set: map[string]any{"provider": "caldav"}, Ask: []string{"cal_url"}, Custom: true},
		{ID: "google", SignIn: "google"},
		{ID: "microsoft", Set: map[string]any{"provider": "microsoft"}, Soon: true},
	},
	Fields: []accountField{
		{Name: "cal_url", Key: "url", Kind: "url", Label: "cal.url", Placeholder: "calp.%s.urlplaceholder", Hint: "calp.%s.urlhint", Only: true, Wide: true},
		{Name: "cal_user", Key: "username", Kind: "user", Label: "calp.%s.user", Placeholder: "calp.%s.userplaceholder"},
	},
	Guess:    map[string]string{"icloud.com": "icloud", "me.com": "icloud", "mac.com": "icloud", "fastmail.com": "fastmail", "fastmail.fm": "fastmail"},
	Password: passwordWords{Label: "cal.password", Hint: "cal.passwordhint", AppLabel: "cal.password", AppHint: "cal.passwordhint"},
	Server:   []accountField{{Name: "timezone", Key: "timezone", Kind: "zone", Label: "cal.tz", Hint: "cal.tzhint", Default: "Europe/Paris", Advanced: true}},
}
