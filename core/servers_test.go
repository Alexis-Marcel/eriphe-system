package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServersConfig(t *testing.T) {
	ok := map[string]Server{
		"github":  {URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{BearerFile: "/secrets/github"}, Use: "issues and PRs"},
		"notion":  {URL: "https://mcp.notion.com/mcp", Auth: &ServerAuth{OAuth: &OAuthConfig{}}, Use: "notes"},
		"docs":    {URL: "http://docs:8080/mcp", Auth: &ServerAuth{None: true}, Use: "documentation"},
		"browser": {Run: json.RawMessage(`{"image":"mcp/browser"}`), Use: "a browser"},
	}
	cfg := &Config{Servers: ok, Brain: &BrainConfig{Tools: []string{"github.get_*", "notion"}}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		srv  Server
		want string
	}{
		{"Git_Hub", Server{URL: "https://x", Auth: &ServerAuth{None: true}, Use: "x"}, "name"},
		{"eriphe-core", Server{URL: "https://x", Auth: &ServerAuth{None: true}, Use: "x"}, "name"},
		{"x", Server{URL: "https://x", Auth: &ServerAuth{None: true}}, "use"},
		{"x", Server{URL: "ftp://x", Auth: &ServerAuth{None: true}, Use: "x"}, "url"},
		{"x", Server{Use: "x"}, "url"},
		{"x", Server{URL: "https://x", Use: "x"}, "auth is required"},
		{"x", Server{URL: "https://x", Auth: &ServerAuth{None: true, BearerFile: "/f"}, Use: "x"}, "exactly one"},
		{"x", Server{URL: "https://x", Auth: &ServerAuth{}, Use: "x"}, "exactly one"},
		{"x", Server{URL: "https://x", Auth: &ServerAuth{OAuth: &OAuthConfig{ClientSecretFile: "/s"}}, Use: "x"}, "client_id_file"},
		{"x", Server{URL: "https://x", Auth: &ServerAuth{OAuth: &OAuthConfig{ClientMetadata: "http://x/c.json"}}, Use: "x"}, "client_metadata_url"},
		{"x", Server{URL: "https://x", Auth: &ServerAuth{OAuth: &OAuthConfig{RedirectURI: "http://localhost:9/cb"}}, Use: "x"}, "redirect_uri"},
		{"x", Server{Run: json.RawMessage(`{}`), URL: "https://x", Use: "x"}, "local"},
		{"x", Server{Run: json.RawMessage(`"img"`), Use: "x"}, "object"},
	}
	for _, b := range bad {
		cfg := &Config{Servers: map[string]Server{b.name: b.srv}}
		if err := cfg.checkServers(); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s %+v: %v, want %q", b.name, b.srv, err, b.want)
		}
	}
	// The brain may name a tool the workspace adds later: left out until then.
	cfg.Brain.Tools = []string{"mail", "github.get_*"}
	if err := cfg.checkServers(); err != nil {
		t.Errorf("brain.tools with a server not yet in the catalog: %v", err)
	}
	if got := cfg.brainTools(); len(got) != 1 || got[0] != "github.get_*" {
		t.Errorf("brainTools = %v", got)
	}
	cfg.Brain.Tools = []string{"mail.[x"}
	if err := cfg.checkServers(); err == nil {
		t.Error("brain.tools with a bad glob accepted")
	}
	cfg.Brain.Tools = nil
	cfg.Brain.MCPServers = map[string]MCPServer{"github": {URL: "https://x"}}
	if err := cfg.checkServers(); err == nil {
		t.Error("a server both in servers and brain.mcp_servers accepted")
	}
}

func TestGrants(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{"github": {}, "notes": {}}}
	g, err := cfg.checkGrant([]string{"notes", "github.get_*", "notes", " github.list_issues"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(g, ",") != "github.get_*,github.list_issues,notes" {
		t.Fatalf("normalized = %v", g)
	}
	for _, bad := range [][]string{{"mail"}, {"github."}, {"github.[x"}, {"mail.read"}} {
		if _, err := cfg.checkGrant(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if s := grantServers(g); strings.Join(s, ",") != "github,notes" {
		t.Fatalf("servers = %v", s)
	}
	cases := []struct {
		server, tool string
		want         bool
	}{
		{"github", "get_issue", true},
		{"github", "get_pull_request", true},
		{"github", "list_issues", true},
		{"github", "list_pull_requests", false},
		{"github", "create_issue", false},
		{"notes", "anything", true},
		{"mail", "send", false},
	}
	for _, c := range cases {
		if got := grantAllows(g, c.server, c.tool); got != c.want {
			t.Errorf("%s.%s = %v", c.server, c.tool, got)
		}
	}
	if s, tl, ok := splitToolName("mcp__home-assistant__HassTurnOn"); !ok || s != "home-assistant" || tl != "HassTurnOn" {
		t.Fatalf("split = %q %q %v", s, tl, ok)
	}
	if _, _, ok := splitToolName("Bash"); ok {
		t.Fatal("Bash split")
	}
	if n := claudeToolName("docs", "search.v2"); n != "mcp__docs__search_v2" {
		t.Fatalf("claude name = %s", n)
	}
}
