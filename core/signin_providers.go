package main

import (
	"strings"

	"golang.org/x/oauth2"
)

// The providers an account signs in at (oauth_client.go), as data: adding
// one is an entry here, and its guide's texts in the text tables, not code.

var providerGoogle = &signInProvider{
	Name:  "google",
	Label: "Google",
	Endpoint: oauth2.Endpoint{
		AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:  "https://oauth2.googleapis.com/token",
		AuthStyle: oauth2.AuthStyleInParams,
	},
	RevokeURL: "https://oauth2.googleapis.com/revoke",
	Scopes:    []string{"openid", "email"},
	AuthParams: map[string]string{
		// Google gives a refresh token at every consent, not only the first.
		"prompt": "consent",
		// The scopes granted before stay with the new token: a service
		// added keeps the others.
		"include_granted_scopes": "true",
	},
	ClientIDSuffix: ".apps.googleusercontent.com",
	Redirect:       redirectPublicHTTPS,
	ForgetAt:       "myaccount.google.com/connections",
	App: appGuide{
		Steps: []appStep{
			{Text: "app.google.project", Link: "https://console.cloud.google.com/projectcreate", LinkText: "console.cloud.google.com › New project"},
			{Text: "app.google.apis", Link: "https://console.cloud.google.com/flows/enableapi?apiid=gmail.googleapis.com,caldav.googleapis.com,drive.googleapis.com",
				LinkText: "Gmail API, CalDAV API, Google Drive API"},
			{Text: "app.google.branding", Link: "https://console.cloud.google.com/auth/overview", LinkText: "Google Auth Platform"},
			{Text: "app.google.audience", Link: "https://console.cloud.google.com/auth/audience", LinkText: "Audience"},
			{Text: "app.google.scopes", Link: "https://console.cloud.google.com/auth/scopes", LinkText: "Data access", Copy: appCopyScopes},
			{Text: "app.google.client", Link: "https://console.cloud.google.com/auth/clients/create", LinkText: "Clients › Create client", Copy: appCopyCallback},
			{Text: "app.google.secret"},
		},
		Scopes:        strings.Join([]string{scopeMail, scopeCalendar, scopeDrive, scopeDriveReadonly}, ","),
		Source:        "https://support.google.com/cloud/answer/15549945",
		JSON:          true,
		IDPlaceholder: "….apps.googleusercontent.com",
	},
}

// signInProviders are the providers, by name.
var signInProviders = map[string]*signInProvider{providerGoogle.Name: providerGoogle}

// Google's scopes for the tools.
const (
	scopeMail          = "https://mail.google.com/"
	scopeCalendar      = "https://www.googleapis.com/auth/calendar"
	scopeDrive         = "https://www.googleapis.com/auth/drive"
	scopeDriveReadonly = "https://www.googleapis.com/auth/drive.readonly"
)

// toolSignIns are what each kind of tool needs of an account at a provider
// (connection_tools.go).
var toolSignIns = []toolSignIn{
	// Gmail over IMAP and SMTP with XOAUTH2: the whole mailbox; Eriphe
	// holds the account's level itself.
	{Tool: toolMail, Provider: "google", Read: []string{scopeMail}, AccountProvider: "gmail", AccountAddress: true},
	// Google Calendar over CalDAV.
	{Tool: toolCalendar, Provider: "google", Read: []string{scopeCalendar}, AccountProvider: "google", AccountUsername: true},
	// Google Drive: read only, or also create files.
	{Tool: toolDrive, Provider: "google", Read: []string{scopeDriveReadonly}, Write: []string{scopeDrive}, AccountProvider: "google"},
}
