package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// "Autre service": the user pastes an MCP server's address and Eriphe finds
// how to get in, the way a connect would (oauth.go), without registering
// anything: an initialize without a credential, then the protected
// resource's metadata (RFC 9728) and its authorization server's (RFC 8414).

const (
	reachOpen        = "open"        // answers without an account
	reachOAuth       = "oauth"       // OAuth with dynamic client registration: Connect works
	reachApp         = "app"         // OAuth, but a client must be made at the vendor
	reachToken       = "token"       // wants a credential without saying how: a key or a token
	reachRefused     = "refused"     // 403
	reachNotMCP      = "notmcp"      // not an MCP server at this address
	reachUnreachable = "unreachable" // no answer
)

// discovery is what Eriphe found at an address.
type discovery struct {
	URL    string
	Reach  string
	Status int    // the initialize's HTTP status
	Detail string // the raw reason, for the curious
	DCR    bool   // dynamic client registration
	CIMD   bool   // Client ID Metadata Documents
	Secret bool   // takes confidential clients only: Eriphe registers with a secret
	Issuer string
	Scopes []string // the scopes the server asks for, or advertises
	Read   []string // those that read, when they split into reads and writes
	Preset toolPreset
}

// ReadOnly tells whether Eriphe can ask the service for a read-only token.
func (d discovery) ReadOnly() bool { return len(d.Read) > 0 }

// Connectable tells whether the form can sign in at once.
func (d discovery) Connectable() bool { return d.Reach == reachOAuth }

// Host is the address's host, for the tool's name.
func (d discovery) Host() string {
	u, _ := url.Parse(d.URL)
	return u.Hostname()
}

// guessName is a tool name from an MCP server's host: "mcp.example.com" →
// "example".
func guessName(host string) string {
	parts := strings.Split(strings.ToLower(host), ".")
	for len(parts) > 2 && slices.Contains([]string{"mcp", "api", "www", "ai", "app"}, parts[0]) {
		parts = parts[1:]
	}
	if len(parts) >= 2 {
		parts = parts[:len(parts)-1]
	}
	name := slugName(strings.Join(parts, "-"))
	if len(name) > 32 {
		name = strings.Trim(name[:32], "-")
	}
	return name
}

var errBadURL, errNoURL = errors.New("form.badurl"), errors.New("form.nourl")

// discover looks at an MCP server's address.
func discover(ctx context.Context, client *http.Client, raw string) (discovery, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return discovery{}, errBadURL
	}
	d := discovery{URL: u.String()}
	if p, ok := presetByURL(d.URL); ok {
		d.Preset = p
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"Eriphe","version":"` + version + `"}}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewBufferString(init))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		d.Reach, d.Detail = reachUnreachable, err.Error()
		return d, nil
	}
	resp.Body.Close()
	d.Status = resp.StatusCode
	switch ct := resp.Header.Get("Content-Type"); {
	case resp.StatusCode == http.StatusOK && (strings.Contains(ct, "json") || strings.Contains(ct, "event-stream")):
		d.Reach = reachOpen
		return d, nil
	case resp.StatusCode == http.StatusForbidden:
		d.Reach = reachRefused
		return d, nil
	case resp.StatusCode != http.StatusUnauthorized:
		d.Reach, d.Detail = reachNotMCP, resp.Status
		return d, nil
	}
	challenges, _ := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	var metaURL string
	for _, c := range challenges {
		if c.Scheme == "bearer" {
			metaURL = c.Params["resource_metadata"]
			if s := c.Params["scope"]; s != "" {
				d.Scopes = strings.Fields(s)
			}
		}
	}
	prm := resourceMetadata(ctx, client, metaURL, u)
	issuer := ""
	if prm != nil {
		if len(prm.AuthorizationServers) > 0 {
			issuer = prm.AuthorizationServers[0]
		}
		if len(d.Scopes) == 0 {
			d.Scopes = prm.ScopesSupported
		}
	} else {
		issuer = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String() // the 2025-03-26 fallback: the server is its own
	}
	asm, err := auth.GetAuthServerMetadata(ctx, issuer, client)
	if err != nil || asm == nil {
		d.Reach = reachToken
		if err != nil {
			d.Detail = err.Error()
		}
		return d, nil
	}
	d.Issuer, d.DCR, d.CIMD = asm.Issuer, asm.RegistrationEndpoint != "", asm.ClientIDMetadataDocumentSupported
	methods := asm.TokenEndpointAuthMethodsSupported
	d.Secret = len(methods) > 0 && !slices.Contains(methods, "none")
	if set, ok := splitScopes(d.Scopes); ok {
		d.Read = set.read
	}
	d.Reach = reachApp
	if d.DCR && (!d.Secret || secretMethod(methods) != "") {
		d.Reach = reachOAuth
	}
	return d, nil
}

// resourceMetadata finds a protected resource's metadata: where the
// challenge says, at the path of the endpoint, then at the root.
func resourceMetadata(ctx context.Context, client *http.Client, metaURL string, u *url.URL) *oauthex.ProtectedResourceMetadata {
	type try struct{ meta, resource string }
	var tries []try
	if metaURL != "" {
		tries = append(tries, try{metaURL, u.String()})
	}
	at := *u
	at.Path = "/.well-known/oauth-protected-resource/" + strings.TrimLeft(u.Path, "/")
	tries = append(tries, try{at.String(), u.String()})
	at.Path, at.RawQuery = "/.well-known/oauth-protected-resource", ""
	tries = append(tries, try{at.String(), (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()})
	for _, t := range tries {
		if prm, err := oauthex.GetProtectedResourceMetadata(ctx, t.meta, t.resource, client); err == nil && prm != nil {
			return prm
		}
	}
	return nil
}
