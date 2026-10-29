package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsConfig(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok\n"), 0o600)
	cfg := &Config{
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Model: "sonnet"}},
		DefaultAgent: "sonnet",
	}
	if err := checkAll(cfg); err != nil {
		t.Fatal(err)
	}
	name, a, err := cfg.agent("")
	if err != nil || name != "sonnet" {
		t.Fatalf("default agent = %q, %v", name, err)
	}
	env, err := cfg.lendAgent(a)
	if err != nil || env["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" {
		t.Fatalf("lent = %v, %v", env, err)
	}
	if _, _, err := cfg.agent("gpt"); err == nil {
		t.Fatal("unknown agent accepted")
	}

	// OpenCode with an API key, in the variable of its provider.
	cfg.Providers["openrouter"] = Provider{Kind: "openrouter-api", Secret: secret}
	cfg.Agents["kimi"] = Agent{Runtime: "opencode", Provider: "openrouter", Model: "openrouter/moonshotai/kimi-k2"}
	if err := checkAll(cfg); err != nil {
		t.Fatal(err)
	}
	if env, err := cfg.lendAgent(cfg.Agents["kimi"]); err != nil || env["OPENROUTER_API_KEY"] != "tok" || len(env) != 1 {
		t.Fatalf("lent = %v, %v", env, err)
	}

	bad := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"unknown kind", func(c *Config) { c.Providers["x"] = Provider{Kind: "magic", Secret: secret} }, "unknown kind"},
		{"unknown provider", func(c *Config) { c.Agents["y"] = Agent{Runtime: "claude-code", Provider: "nope"} }, "unknown provider"},
		{"runtime mismatch", func(c *Config) { c.Agents["z"] = Agent{Runtime: "opencode", Provider: "anthropic"} }, "cannot use"},
		{"api key for claude code", func(c *Config) {
			c.Providers["oa"] = Provider{Kind: "openai-api", Secret: secret}
			c.Agents["z"] = Agent{Runtime: "claude-code", Provider: "oa"}
		}, "cannot use"},
		{"opencode model of another provider", func(c *Config) {
			c.Providers["oa"] = Provider{Kind: "openai-api", Secret: secret}
			c.Agents["z"] = Agent{Runtime: "opencode", Provider: "oa", Model: "openrouter/gpt-5"}
		}, "openai/<model>"},
		{"default", func(c *Config) { c.DefaultAgent = "opus" }, "default_agent"},
		{"brain", func(c *Config) { c.Brain = &BrainConfig{Agent: "opus"} }, "brain"},
		{"brain on a runtime without MCP", func(c *Config) {
			withHarness(t, "opencode", func(h *harness) { h.MCP = false })
			c.Providers["oa"] = Provider{Kind: "openai-api", Secret: secret}
			c.Agents["gpt"] = Agent{Runtime: "opencode", Provider: "oa", Model: "openai/gpt-5"}
			c.Brain = &BrainConfig{Agent: "gpt"}
		}, "takes MCP servers and a system prompt"},
		{"brain on a runtime without a system prompt", func(c *Config) {
			withHarness(t, "opencode", func(h *harness) { h.SystemPrompt = promptPrepended })
			c.Providers["oa"] = Provider{Kind: "openai-api", Secret: secret}
			c.Agents["gpt"] = Agent{Runtime: "opencode", Provider: "oa", Model: "openai/gpt-5"}
			c.Brain = &BrainConfig{Agent: "gpt"}
		}, "takes MCP servers and a system prompt"},
	}
	for _, b := range bad {
		c := *cfg
		c.Providers = map[string]Provider{"anthropic": cfg.Providers["anthropic"]}
		c.Agents = map[string]Agent{"sonnet": cfg.Agents["sonnet"]}
		b.edit(&c)
		if err := checkAll(&c); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: %v", b.name, err)
		}
		restoreHarnesses()
	}

	// A runtime that takes both can be the brain.
	cfg.Providers["oa"] = Provider{Kind: "openai-api", Secret: secret}
	cfg.Agents["gpt"] = Agent{Runtime: "opencode", Provider: "oa", Model: "openai/gpt-5"}
	cfg.Brain = &BrainConfig{Agent: "gpt"}
	if err := checkAll(cfg); err != nil {
		t.Fatalf("brain on opencode: %v", err)
	}
}

// An ACP agent by its command, with a key in the variable its provider
// names.
func TestACPAgentsConfig(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "key")
	os.WriteFile(secret, []byte("tok\n"), 0o600)
	good := func() *Config {
		return &Config{
			Providers: map[string]Provider{
				"gemini": {Kind: "google-api", Secret: secret},
				"custom": {Kind: "custom-api", Secret: secret, Env: "DASHSCOPE_API_KEY"},
			},
			Agents: map[string]Agent{
				"qwen": {Runtime: "acp", Provider: "custom", Command: []string{"qwen", "--acp"}},
			},
			DefaultAgent: "qwen",
		}
	}
	cfg := good()
	if err := checkAll(cfg); err != nil {
		t.Fatal(err)
	}
	if env, err := cfg.lendAgent(cfg.Agents["qwen"]); err != nil || env["DASHSCOPE_API_KEY"] != "tok" || len(env) != 1 {
		t.Fatalf("custom lent %v, %v", env, err)
	}
	bad := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"no command", func(c *Config) { c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom"} }, "needs its command"},
		{"a shell", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom", Command: []string{"/bin/sh", "-c", "qwen --acp"}}
		}, "not through a shell"},
		{"env", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom", Command: []string{"env", "X=1", "qwen"}}
		}, "not through a shell"},
		{"a relative path", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom", Command: []string{"./qwen", "--acp"}}
		}, "absolute path"},
		{"a command line in one string", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom", Command: []string{"qwen --acp"}}
		}, "without spaces"},
		{"a line break", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom", Command: []string{"qwen", "--acp\nrm -rf /"}}
		}, "multi-line"},
		{"a model", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "custom", Command: []string{"qwen"}, Model: "qwen4"}
		}, "its own settings"},
		{"a command for another runtime", func(c *Config) {
			c.Agents["kimi"] = Agent{Runtime: "opencode", Provider: "gemini", Model: "google/gemini-4-argon", Command: []string{"opencode"}}
		}, "only an acp agent"},
		{"acp with a known provider", func(c *Config) {
			c.Agents["qwen"] = Agent{Runtime: "acp", Provider: "gemini", Command: []string{"qwen"}}
		}, "cannot use"},
		{"opencode with a custom provider", func(c *Config) {
			c.Agents["kimi"] = Agent{Runtime: "opencode", Provider: "custom", Model: "openrouter/kimi"}
		}, "cannot use"},
		{"no variable", func(c *Config) { c.Providers["custom"] = Provider{Kind: "custom-api", Secret: secret} }, "is not a name"},
		{"a lowercase variable", func(c *Config) {
			c.Providers["custom"] = Provider{Kind: "custom-api", Secret: secret, Env: "api_key"}
		}, "is not a name"},
		{"a system variable", func(c *Config) {
			c.Providers["custom"] = Provider{Kind: "custom-api", Secret: secret, Env: "NODE_OPTIONS"}
		}, "the system's"},
		{"an eriphe variable", func(c *Config) {
			c.Providers["custom"] = Provider{Kind: "custom-api", Secret: secret, Env: "ERIPHE_INSTRUCTIONS"}
		}, "the system's"},
		{"a variable for a known kind", func(c *Config) {
			c.Providers["gemini"] = Provider{Kind: "google-api", Secret: secret, Env: "GEMINI_API_KEY"}
		}, "takes no env"},
		{"brain on acp", func(c *Config) { c.Brain = &BrainConfig{Agent: "qwen"} }, "the brain needs"},
	}
	for _, b := range bad {
		c := good()
		b.edit(c)
		if err := checkAll(c); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: %v", b.name, err)
		}
	}
}
