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
	// only beside another free); without, the server's level holds for all
	// (the files', whose places each have their own access).
	AccountLevels bool
	// ByName: two accounts of one name are one account (a mailbox is its
	// address); without, they are also where they are (a calendar).
	ByName bool
	// SignIns are what it needs of an account signed in at a provider.
	SignIns []toolSignIn
	// Places: it has places, kept in its configuration (places.go).
	Places *placeKeeper
}

var (
	mailService = toolCard{ID: toolMail, Base: "mail", Image: "ghcr.io/alexis-marcel/eriphe-mail", Preset: "mail",
		OwnVersion: true, AccountLevels: true, ByName: true,
		SignIns: []toolSignIn{
			// Gmail over IMAP and SMTP with XOAUTH2: the whole mailbox;
			// Eriphe holds the account's level itself.
			{Provider: "google", Read: []string{scopeMail}, AccountProvider: "gmail", AccountAddress: true},
		}}
	calendarService = toolCard{ID: toolCalendar, Base: "agenda", Image: "ghcr.io/alexis-marcel/eriphe-calendar", Preset: "calendar",
		OwnVersion: true, AccountLevels: true,
		SignIns: []toolSignIn{
			// Google Calendar over CalDAV.
			{Provider: "google", Read: []string{scopeCalendar}, AccountProvider: "google", AccountUsername: true},
		}}
	filesService = toolCard{ID: toolDrive, Base: "fichiers", Image: "ghcr.io/alexis-marcel/eriphe-files", Preset: "gdrive",
		OwnVersion: true, Scopes: true, ByName: true,
		SignIns: []toolSignIn{
			// Google Drive: read only, or also create files.
			{Provider: "google", Read: []string{scopeDriveReadonly}, Write: []string{scopeDrive}, AccountProvider: "google"},
		},
		Places: &placeKeeper{Key: "places", Browse: "browse_places"}}

	// toolCards are the cards, in the order their kinds are shown.
	toolCards = []toolCard{mailService, calendarService, filesService}
)
