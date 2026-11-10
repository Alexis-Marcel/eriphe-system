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
	driveTokenEnv = "FILES_GOOGLE_TOKEN"
)

// legacySecretPrefix starts the vault name of an account's token from
// before connections: google.<hash>.token.
const legacySecretPrefix = "google"
