package main

// What installs from before connections still hold, under the names they
// had when Google was the one provider an account signed in at. The names
// are kept so that nothing they hold moves: a tool's environment, a token
// in the vault, an old address. Nothing new is written under them but the
// variables' names, which tools keep reading.

// The variables a tool reads an account's token from (with _<hash>).
const (
	mailTokenEnv  = "MAIL_GOOGLE_TOKEN"
	calTokenEnv   = "CALENDAR_GOOGLE_TOKEN"
	driveTokenEnv = "FILES_GOOGLE_TOKEN" // an account's is FILES_GOOGLE_TOKEN_<hash> (driveTokenEnvOf)
)

// legacySecretPrefix starts the vault name of an account's token from
// before connections: google.<hash>.token.
const legacySecretPrefix = "google"

// legacyProvider is the provider of an account written before providers
// were named: a files account of no provider, the accounts kept before
// connections.
var legacyProvider = providerGoogle

// signInProvider is the provider a files account is signed in at.
func (a filesAccount) signInProvider() string { return cmpOr(a.Provider, legacyProvider.Name) }

// oldConnectionPages are the pages before connections, kept as addresses
// that lead to Settings → Tools.
var oldConnectionPages = []string{"/settings/google", "/settings/drive"}
