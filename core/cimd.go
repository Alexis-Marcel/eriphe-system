package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Eriphe's own OAuth client, for the servers that take a Client ID
// Metadata Document only (no dynamic registration, no app to make at the
// vendor's): ElevenLabs. The document is published by the project on
// GitHub Pages (site/oauth/client.json); its URL is the client_id. It
// describes no installation: one loopback redirect for every Eriphe, so
// the service never learns where an Eriphe runs. The browser lands on
// that address, which does not load unless `eriphe connect` listens there;
// from the workspace, its address is pasted back (web_servers.go).
//
// Dynamic registration stays first: the document is used only when a
// server offers no other way and no client is configured for it.

const (
	builtinClientID = "https://alexis-marcel.github.io/eriphe-system/oauth/client.json"
	builtinRedirect = "http://127.0.0.1:8976/callback"
)

// cimdCache remembers what a server's authorization server offers, for a
// while: a connect asks it once, then the workspace and the API agree.
type cimdCache struct {
	mu   sync.Mutex
	seen map[string]cimdSeen
}

type cimdSeen struct {
	only bool
	at   time.Time
}

const cimdCacheFor = 10 * time.Minute

// usesBuiltinClient tells whether a connect of this server signs in with
// Eriphe's published client: an OAuth server with no client of its own
// (configured, typed in the form, or registered before), whose
// authorization server takes Client ID Metadata Documents and offers no
// dynamic registration.
func (c *core) usesBuiltinClient(ctx context.Context, name string, s Server, creds credentials) bool {
	if s.authKind() != "oauth" || strings.TrimSpace(creds["client-id"]) != "" {
		return false
	}
	if o := oauthOf(s); o != nil && (o.ClientIDFile != "" || o.ClientMetadata != "" || o.RedirectURI != "") {
		return false
	}
	if reg := c.store.oauthClient(name); reg != nil {
		return false
	}
	c.cimd.mu.Lock()
	seen, ok := c.cimd.seen[s.URL]
	c.cimd.mu.Unlock()
	if ok && time.Since(seen.at) < cimdCacheFor {
		return seen.only
	}
	d, err := discover(ctx, &http.Client{Transport: transportOf(c.pool.http)}, s.URL)
	if err != nil || d.Issuer == "" {
		return false // unknown: the usual way, which says what fails
	}
	only := d.CIMD && !d.DCR
	c.cimd.mu.Lock()
	if c.cimd.seen == nil {
		c.cimd.seen = map[string]cimdSeen{}
	}
	c.cimd.seen[s.URL] = cimdSeen{only: only, at: time.Now()}
	c.cimd.mu.Unlock()
	return only
}

// signedInWithBuiltinClient tells whether a server's stored token came
// from Eriphe's published client: a new connect takes the same way.
func (c *core) signedInWithBuiltinClient(name string) bool {
	cfg, _, err := c.store.oauthToken(name)
	return err == nil && cfg != nil && cfg.ClientID == builtinClientID
}
