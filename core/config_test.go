package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The time zone is Europe/Paris unless the configuration names another, and
// a zone that does not exist stops eriphe-core at once.
func TestConfigTimeZone(t *testing.T) {
	dir := t.TempDir()
	load := func(extra string) (*Config, error) {
		path := filepath.Join(dir, "core.json")
		os.WriteFile(path, []byte(`{"token_file": "token"`+extra+`}`), 0o600)
		return loadConfig(path)
	}
	c, err := load("")
	if err != nil || c.TimeZone != "Europe/Paris" || c.location.String() != "Europe/Paris" {
		t.Fatalf("default = %v, %v", c, err)
	}
	if c, err = load(`, "timezone": "America/New_York"`); err != nil || c.location.String() != "America/New_York" {
		t.Fatalf("set = %v, %v", c, err)
	}
	if _, err = load(`, "timezone": "Europe/Nowhere"`); err == nil || !strings.Contains(err.Error(), "Europe/Nowhere") {
		t.Fatalf("bad zone: %v", err)
	}
}

// The brain's turn opens with the date and time in the user's zone.
func TestNowLine(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	now := time.Date(2026, 10, 26, 23, 1, 0, 0, time.UTC).In(paris)
	if got, want := nowLine(now), "[maintenant 2026-10-27 00:01 Europe/Paris (Tuesday)]"; got != want {
		t.Fatalf("nowLine = %q, want %q", got, want)
	}
}

// The brain may run an agent added from the workspace: the file alone does
// not know it, the merged catalog does.
func TestBrainAgentFromWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.json")
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	os.WriteFile(path, []byte(`{"token_file": "token", "default_agent": "opus",
		"providers": {"anthropic": {"kind": "claude-subscription", "secret": "`+secret+`"}},
		"agents": {"opus": {"runtime": "claude-code", "provider": "anthropic"}},
		"brain": {"node": "nas", "agent": "free", "prompt_file": "p.md"}}`), 0o600)
	c, err := loadConfig(path)
	if err != nil {
		t.Fatalf("a workspace agent refused by the file check: %v", err)
	}
	c.Agents["free"] = Agent{Runtime: "opencode", Provider: "anthropic", Model: "anthropic/claude-sonnet"}
	if err := c.checkAgents(); err != nil && strings.Contains(err.Error(), "is not an agent") {
		t.Fatalf("with the catalog: %v", err)
	}
	delete(c.Agents, "free")
	if err := c.checkAgents(); err == nil || !strings.Contains(err.Error(), "is not an agent") {
		t.Fatalf("unknown everywhere: %v", err)
	}
}

// eriphe-core starts with the brain on an agent the workspace added, its
// provider loaded before it.
func TestBrainOnWorkspaceAgentAtStart(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "or")
	os.WriteFile(key, []byte("sk-or-test"), 0o600)
	store.putCatalog(kindProvider, "openrouter", Provider{Kind: "openrouter-api", Secret: key})
	store.putCatalog(kindAgent, "free", Agent{Runtime: "opencode", Provider: "openrouter", Model: "openrouter/example/model:free"})
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	cfg := &Config{DefaultAgent: "opus", Brain: &BrainConfig{Node: "nas", Agent: "free"},
		Providers: map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents:    map[string]Agent{"opus": {Runtime: "claude-code", Provider: "anthropic"}}}
	c := newCore(cfg, store)
	if len(c.skipped) != 0 {
		t.Fatalf("left out: %v", c.skipped)
	}
	if _, a, err := c.conf().agent("free"); err != nil || a.Runtime != "opencode" {
		t.Fatalf("brain agent: %v %v", a, err)
	}
}
