package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The tool catalog: MCP servers eriphe-core reaches on behalf of tasks. A
// task never sees a vendor credential: its MCP configuration points at its
// node, which relays to eriphe-core, which calls the server with the real
// credential (see pool.go and proxy.go). Like agents, servers are what the
// brain picks from: "use" tells it what each one is for.

// Server is one entry of the catalog: either remote (url and auth) or local
// (run: a container a node runs as a service, see services).
type Server struct {
	URL  string      `json:"url,omitempty"`
	Auth *ServerAuth `json:"auth,omitempty"`
	// Run is the container spec of a local server, run by a node. It is
	// read by the services module; the catalog only reaches it through
	// pool.dialLocal.
	Run json.RawMessage `json:"run,omitempty"`
	Use string          `json:"use"` // what it is for, for the brain
	// TrustAnnotations lets the server's own readOnlyHint count: a read-only
	// tool of a trusted server is the brain's to allow, the others the user's.
	TrustAnnotations bool `json:"trust_annotations,omitempty"`
	// Timeout of one tool call, in seconds (default 600).
	Timeout int `json:"timeout,omitempty"`
	// AlwaysLoad has a harness that defers MCP tools until they are searched
	// for (Claude Code) load this server's upfront: a search costs a model
	// round trip, too slow for the house at the voice.
	AlwaysLoad bool `json:"always_load,omitempty"`
}

// ServerAuth is how eriphe-core authenticates to a remote server: exactly
// one of a bearer token in a file, OAuth, or none.
type ServerAuth struct {
	BearerFile string       `json:"bearer_file,omitempty"`
	OAuth      *OAuthConfig `json:"oauth,omitempty"`
	None       bool         `json:"none,omitempty"`
}

// OAuthConfig configures the authorization code flow of the MCP
// authorization spec. Without a client, eriphe-core uses a Client ID
// Metadata Document if given and supported, else registers itself (DCR).
type OAuthConfig struct {
	Scopes           []string `json:"scopes,omitempty"`              // empty: what the server advertises
	ClientIDFile     string   `json:"client_id_file,omitempty"`      // a pre-registered client
	ClientSecretFile string   `json:"client_secret_file,omitempty"`  // its secret, if confidential
	ClientMetadata   string   `json:"client_metadata_url,omitempty"` // CIMD: an https URL hosting the client's metadata
	// RedirectURI is the loopback redirect registered for a pre-registered
	// client, e.g. http://127.0.0.1:8976/callback; `eriphe connect` listens on it.
	RedirectURI string `json:"redirect_uri,omitempty"`
}

func (s Server) local() bool { return len(s.Run) > 0 }

func (s Server) authKind() string {
	switch {
	case s.local():
		return "local"
	case s.Auth == nil:
		return ""
	case s.Auth.OAuth != nil:
		return "oauth"
	case s.Auth.BearerFile != "":
		return "bearer"
	default:
		return "none"
	}
}

// serverName is a name Claude Code keeps as is in mcp__<server>__<tool>:
// no underscore, so the tool name splits back unambiguously.
var serverName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// reserved are the names of the MCP servers a task already has.
var reserved = []string{"eriphe-core", "eriphe-node"}

func (c *Config) checkServers() error {
	for name, s := range c.Servers {
		if !serverName.MatchString(name) || slices.Contains(reserved, name) {
			return fmt.Errorf("server %q: a name of lowercase letters, digits and dashes (not %s)", name, strings.Join(reserved, ", "))
		}
		if s.Use == "" {
			return fmt.Errorf("server %s: use is required (what it is for, for the brain)", name)
		}
		if s.Timeout < 0 {
			return fmt.Errorf("server %s: bad timeout", name)
		}
		if s.local() {
			if s.URL != "" || s.Auth != nil {
				return fmt.Errorf("server %s: a local server (run) has no url or auth", name)
			}
			var spec map[string]any
			if json.Unmarshal(s.Run, &spec) != nil {
				return fmt.Errorf("server %s: run must be an object", name)
			}
			continue
		}
		u, err := url.Parse(s.URL)
		if s.URL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("server %s: url (http or https) or run is required", name)
		}
		a := s.Auth
		if a == nil {
			return fmt.Errorf(`server %s: auth is required: {"bearer_file": ...}, {"oauth": {...}} or {"none": true}`, name)
		}
		n := 0
		for _, set := range []bool{a.BearerFile != "", a.OAuth != nil, a.None} {
			if set {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("server %s: auth is exactly one of bearer_file, oauth or none", name)
		}
		if o := a.OAuth; o != nil {
			if o.ClientSecretFile != "" && o.ClientIDFile == "" {
				return fmt.Errorf("server %s: oauth client_secret_file needs client_id_file", name)
			}
			if o.ClientMetadata != "" {
				if m, err := url.Parse(o.ClientMetadata); err != nil || m.Scheme != "https" || m.Path == "" || m.Path == "/" {
					return fmt.Errorf("server %s: oauth client_metadata_url must be an https URL with a path", name)
				}
			}
			if o.RedirectURI != "" && !loopbackRedirect(o.RedirectURI) {
				return fmt.Errorf("server %s: oauth redirect_uri must be http://127.0.0.1:<port>/callback", name)
			}
		}
	}
	if c.Brain != nil {
		for name := range c.Brain.MCPServers {
			if _, ok := c.Servers[name]; ok {
				return fmt.Errorf("brain: mcp_servers.%s is also in servers: keep only the catalog entry", name)
			}
		}
		// The brain may be granted a tool added in the workspace: an
		// unknown server is checked for its form only, and left out until
		// it exists (brainTools).
		for _, g := range c.Brain.Tools {
			if err := checkGrantEntry(g); err != nil {
				return fmt.Errorf("brain.tools: %w", err)
			}
		}
	}
	return nil
}

// loopbackRedirect accepts the redirect of a native app: http on the IPv4
// loopback, a port, and the /callback path the CLI serves.
func loopbackRedirect(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.Path == "/callback"
}

// A grant is what a task may use of the catalog: server names (all their
// tools) or server.tool, where tool may hold * globs: "github.get_*".

// checkGrant validates a grant and returns it normalized (sorted, without
// duplicates).
func (c *Config) checkGrant(grant []string) ([]string, error) {
	out := []string{}
	for _, g := range grant {
		g = strings.TrimSpace(g)
		server, _, _ := strings.Cut(g, ".")
		if _, ok := c.Servers[server]; !ok {
			return nil, fmt.Errorf("unknown server %q (see list_tools)", server)
		}
		if err := checkGrantEntry(g); err != nil {
			return nil, err
		}
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out, nil
}

// checkGrantEntry checks the form of one grant entry: a server name, or
// server.tool with a valid glob.
func checkGrantEntry(g string) error {
	server, tool, scoped := strings.Cut(strings.TrimSpace(g), ".")
	if !serverName.MatchString(server) {
		return fmt.Errorf("%q: not a server name", g)
	}
	if scoped {
		if tool == "" {
			return fmt.Errorf("%q: a tool name or glob is required after the dot", g)
		}
		if _, err := path.Match(tool, ""); err != nil {
			return fmt.Errorf("%q: %w", g, err)
		}
	}
	return nil
}

// brainTools is the brain's grant, without the servers the catalog does
// not have (yet).
func (c *Config) brainTools() []string {
	if c.Brain == nil {
		return nil
	}
	var out []string
	for _, g := range c.Brain.Tools {
		server, _, _ := strings.Cut(strings.TrimSpace(g), ".")
		if _, ok := c.Servers[server]; ok {
			out = append(out, strings.TrimSpace(g))
		}
	}
	return out
}

// grantServers are the servers a grant touches.
func grantServers(grant []string) []string {
	var out []string
	for _, g := range grant {
		server, _, _ := strings.Cut(g, ".")
		if !slices.Contains(out, server) {
			out = append(out, server)
		}
	}
	sort.Strings(out)
	return out
}

// grantAllows tells whether a grant covers one tool of a server.
func grantAllows(grant []string, server, tool string) bool {
	for _, g := range grant {
		s, pattern, scoped := strings.Cut(g, ".")
		if s != server {
			continue
		}
		if !scoped {
			return true
		}
		if ok, _ := path.Match(pattern, tool); ok {
			return true
		}
	}
	return false
}

func splitGrant(s string) []string {
	var out []string
	for _, g := range strings.Split(s, ",") {
		if g != "" {
			out = append(out, g)
		}
	}
	return out
}

// claudeToolName is how Claude Code names a server's tool: characters an
// API tool name cannot hold become underscores.
func claudeToolName(server, tool string) string {
	return "mcp__" + server + "__" + badToolChars.ReplaceAllString(tool, "_")
}

var badToolChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// splitToolName parses mcp__<server>__<tool>; ok is false for any other name.
func splitToolName(name string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(name, "mcp__")
	if !found {
		return "", "", false
	}
	server, tool, ok = strings.Cut(rest, "__")
	return server, tool, ok && server != "" && tool != ""
}
