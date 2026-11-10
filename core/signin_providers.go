package main

import "golang.org/x/oauth2"

// The providers an account signs in at (oauth_client.go), as data: adding
// one is an entry here, not code.

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
}

// signInProviders are the providers, by name.
var signInProviders = map[string]*signInProvider{providerGoogle.Name: providerGoogle}
