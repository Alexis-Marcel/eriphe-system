package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"time"
)

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

// The migration.

// legacyAccountsKey is where Google accounts were kept before connections.
const legacyAccountsKey = "google_accounts"

// migrateConnections makes a connection of each account kept before
// connections. Its token keeps its vault name, so no tool's environment
// changes; it gets where to refresh it.
func (c *core) migrateConnections() {
	raw := c.store.get(legacyAccountsKey)
	if raw == "" {
		return
	}
	var list []struct {
		Email     string   `json:"email"`
		Scopes    []string `json:"scopes,omitempty"`
		Reconnect string   `json:"reconnect,omitempty"`
		Checked   int64    `json:"checked,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		log.Printf("connections: the Google accounts could not be read: %v", err)
		return
	}
	p := legacyProvider
	now := time.Now().Unix()
	err := func() error {
		tx, err := c.store.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, a := range list {
			lapsed := a.Reconnect
			if lapsed == "the Google app it was connected with is refused" {
				lapsed = lapsedBadClient
			}
			scopes, _ := json.Marshal(a.Scopes)
			if _, err := tx.Exec(`INSERT OR IGNORE INTO connections (id, provider, identity, scopes, secret, lapsed, checked, created)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, newConnectionID(), p.Name, a.Email, string(scopes), legacyGoogleSecret(a.Email),
				lapsed, a.Checked, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM kv WHERE key = ?`, legacyAccountsKey); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		log.Printf("connections: the Google accounts were not moved: %v", err)
		return
	}
	if v := currentVault(); v != nil {
		for _, a := range list {
			addTokenURL(v, legacyGoogleSecret(a.Email), p.Endpoint.TokenURL)
		}
	}
	log.Printf("connections: %d Google account(s) are connections now", len(list))
}

// legacyGoogleSecret is the vault name a Google account's token had before
// connections; it keeps it.
func legacyGoogleSecret(email string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return legacySecretPrefix + "." + hex.EncodeToString(h[:6]) + ".token"
}
