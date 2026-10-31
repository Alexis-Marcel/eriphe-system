package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A self-hosted model server: no key needed, an address the tasks can
// reach, and the task pointed at it with that one host:port let through.
func TestSelfHostedConfig(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "key")
	os.WriteFile(secret, []byte("k-123\n"), 0o600)
	good := func() *Config {
		return &Config{
			Providers:    map[string]Provider{"ollama": {Kind: selfHostedKind, BaseURL: "http://192.0.2.10:11434/v1", Models: []string{"qwen3:8b"}}},
			Agents:       map[string]Agent{"qwen": {Runtime: "opencode", Provider: "ollama", Model: "selfhosted/qwen3:8b"}},
			DefaultAgent: "qwen",
		}
	}
	cfg := good()
	if err := checkAll(cfg); err != nil {
		t.Fatal(err)
	}
	if env, err := cfg.lendAgent(cfg.Agents["qwen"]); err != nil || len(env) != 0 {
		t.Fatalf("lent without a key: %v, %v", env, err)
	}
	// The task: its address, and that host:port in a copy of its rule.
	shared := defaultEgress()
	spec := nodeTaskSpec{Egress: shared}
	cfg.modelEndpoint("qwen", map[string]string{}, &spec)
	if spec.ModelEndpoint == nil || spec.ModelEndpoint.BaseURL != "http://192.0.2.10:11434/v1" || spec.ModelEndpoint.KeyEnv != "" ||
		!slices.Equal(spec.Egress.Endpoints, []string{"192.0.2.10:11434"}) || len(shared.Endpoints) != 0 {
		t.Fatalf("spec %+v, egress %+v, shared %+v", spec.ModelEndpoint, spec.Egress, shared)
	}
	// With a key: its variable. The default port of https.
	cfg.Providers["ollama"] = Provider{Kind: selfHostedKind, Secret: secret, BaseURL: "https://llm.example.org/v1"}
	env, err := cfg.lendAgent(cfg.Agents["qwen"])
	if err != nil || env["SELFHOSTED_API_KEY"] != "k-123" {
		t.Fatalf("lent %v, %v", env, err)
	}
	spec = nodeTaskSpec{Egress: defaultEgress()}
	cfg.modelEndpoint("qwen", env, &spec)
	if spec.ModelEndpoint.KeyEnv != "SELFHOSTED_API_KEY" || !slices.Equal(spec.Egress.Endpoints, []string{"llm.example.org:443"}) {
		t.Fatalf("spec %+v, egress %+v", spec.ModelEndpoint, spec.Egress)
	}
	// A task without egress gets no endpoint; another provider's agent nothing.
	spec = nodeTaskSpec{Egress: &nodeEgress{Mode: "none"}}
	cfg.modelEndpoint("qwen", env, &spec)
	if len(spec.Egress.Endpoints) != 0 {
		t.Errorf("mode none: %+v", spec.Egress)
	}
	cfg.Providers["or"] = Provider{Kind: "openrouter-api", Secret: secret}
	cfg.Agents["kimi"] = Agent{Runtime: "opencode", Provider: "or", Model: "openrouter/kimi"}
	spec = nodeTaskSpec{Egress: defaultEgress()}
	if cfg.modelEndpoint("kimi", env, &spec); spec.ModelEndpoint != nil || len(spec.Egress.Endpoints) != 0 {
		t.Errorf("openrouter: %+v %+v", spec.ModelEndpoint, spec.Egress)
	}

	for url, ok := range map[string]bool{
		"http://192.0.2.10:11434/v1": true, "https://llm.example.org/v1": true, "http://[2001:db8::5]:8000/v1": true, "http://10.0.0.5:1234/v1": true,
		"": false, "192.0.2.10:11434": false, "ftp://192.0.2.10/v1": false, "http://user:pw@192.0.2.10/v1": false,
		"http://192.0.2.10/v1?x=1": false, "http://192.0.2.10/v1#f": false, "http://localhost:11434/v1": false,
		"http://127.0.0.1:11434/v1": false, "http://[::1]:11434/v1": false, "http://169.254.169.254/v1": false,
		"http://0.0.0.0:11434/v1": false, "http://224.0.0.1/v1": false, "http://192.0.2.10:99999/v1": false,
		"http://192.0.2.10/{file:x}": false, "http://192.0.2.10/v1\"": false,
	} {
		c := good()
		c.Providers["ollama"] = Provider{Kind: selfHostedKind, BaseURL: url}
		if err := checkAll(c); (err == nil) != ok {
			t.Errorf("base_url %q: %v", url, err)
		}
	}
	bad := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"a model with a space", func(c *Config) {
			c.Providers["ollama"] = Provider{Kind: selfHostedKind, BaseURL: "http://192.0.2.10/v1", Models: []string{"qwen 3"}}
		}, "not a model name"},
		{"a model of another provider", func(c *Config) { c.Agents["qwen"] = Agent{Runtime: "opencode", Provider: "ollama", Model: "openai/x"} }, "selfhosted/<model>"},
		{"claude code", func(c *Config) { c.Agents["qwen"] = Agent{Runtime: "claude-code", Provider: "ollama"} }, "cannot use"},
		{"a base_url elsewhere", func(c *Config) {
			c.Providers["or"] = Provider{Kind: "openrouter-api", Secret: secret, BaseURL: "http://192.0.2.10/v1"}
		}, "only a self-hosted"},
	}
	for _, b := range bad {
		c := good()
		b.edit(c)
		if err := checkAll(c); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: %v", b.name, err)
		}
	}
	if selfHostedKey(`a"b`) || selfHostedKey(`a\b`) || selfHostedKey("a\nb") || selfHostedKey("{env:HOME}") || !selfHostedKey("sk-local-123") {
		t.Error("selfHostedKey")
	}
}

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
