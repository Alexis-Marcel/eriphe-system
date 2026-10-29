package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startOnDefaults opens what main opens, on defaults in dir: the
// configuration, the store, the vault and the workspace's certificate.
func startOnDefaults(t *testing.T, dir string) (*Config, *web) {
	t.Helper()
	cfg, err := loadOrDefault("", dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openStore(cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	if err := startVault(cfg, store); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { setVault(nil) })
	w, err := newWeb(newCore(cfg, store), *cfg.Web)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.certificate(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg, w
}

// With no configuration file, eriphe-core makes what it lacks in the data
// directory at first start, and finds it again at the next one.
func TestStartOnEmptyDataDir(t *testing.T) {
	t.Setenv(vaultKeyEnv, "")
	dir := filepath.Join(t.TempDir(), "data")
	cfg, w := startOnDefaults(t, dir)
	if cfg.Listen != "0.0.0.0:8800" || cfg.Web == nil || cfg.Brain == nil || cfg.Brain.Agent != "" ||
		cfg.Brain.PromptFile != "" || cfg.Brain.SelfURL != "" || cfg.PublicURL != "" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.Policy != nil || len(cfg.Servers) != 0 || len(cfg.Agents) != 0 || len(cfg.Identities) != 0 {
		t.Fatalf("defaults that are someone's own: %+v", cfg)
	}
	if w.cfg.Listen != "0.0.0.0:8443" || w.setup == "" {
		t.Fatalf("workspace on %s, setup code %q", w.cfg.Listen, w.setup)
	}
	files := map[string][]byte{}
	for _, name := range []string{"core-token", "core.db", "vault.key", "web-cert.pem", "web-key.pem"} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s not made: %v", name, err)
		}
		files[name] = raw
		secret := name == "core-token" || name == "vault.key" || name == "web-key.pem"
		if fi, _ := os.Stat(path); secret && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", name, fi.Mode().Perm())
		}
	}
	token, err := readSecret(cfg.TokenFile)
	if err != nil || len(token) != 64 {
		t.Fatalf("token %q, %v", token, err)
	}

	if again, _ := startOnDefaults(t, dir); again.TokenFile != cfg.TokenFile {
		t.Fatalf("token file moved to %s", again.TokenFile)
	}
	for _, name := range []string{"core-token", "vault.key", "web-cert.pem", "web-key.pem"} {
		if raw, _ := os.ReadFile(filepath.Join(dir, name)); !bytes.Equal(raw, files[name]) {
			t.Errorf("%s made again at the second start", name)
		}
	}
}

// A data directory that cannot be written says so.
func TestDefaultsUnwritableData(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	if _, err := loadOrDefault("", dir); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v", err)
	}
}

// A file is read as it is; a missing one, or none, runs on defaults.
func TestConfigFileOrDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.json")
	os.WriteFile(path, []byte(`{"listen": "127.0.0.1:9900", "token_file": "/secrets/token", "db": "/srv/core.db"}`), 0o600)
	c, err := loadOrDefault(path, filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	// The file wins, and what it leaves out stays off, as before.
	if c.Listen != "127.0.0.1:9900" || c.TokenFile != "/secrets/token" || c.DB != "/srv/core.db" || c.Web != nil || c.Brain != nil {
		t.Fatalf("from the file = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); err == nil {
		t.Fatal("the data directory made with a file")
	}
	c, err = loadOrDefault(filepath.Join(dir, "missing.json"), filepath.Join(dir, "data"))
	if err != nil || c.TokenFile != filepath.Join(dir, "data", "core-token") || c.Web == nil {
		t.Fatalf("missing file = %+v, %v", c, err)
	}
	os.WriteFile(path, []byte(`{"listen": "0.0.0.0:8800"}`), 0o600)
	if _, err := loadOrDefault(path, dir); err == nil || !strings.Contains(err.Error(), "token_file is required") {
		t.Fatalf("a file without token_file: %v", err)
	}
	// No -config: defaults, even next to a core.json.
	t.Chdir(dir)
	if c, err := loadOrDefault("", filepath.Join(dir, "data")); err != nil || c.Web == nil {
		t.Fatalf("no -config = %+v, %v", c, err)
	}
}

// A configuration as the NAS has one keeps every value: nothing of the
// defaults comes in.
func TestOldConfigUnchanged(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	prompt := filepath.Join(dir, "brain-prompt.md")
	os.WriteFile(prompt, []byte("Mes consignes pour {{user}}"), 0o600)
	path := filepath.Join(dir, "core.json")
	os.WriteFile(path, []byte(`{"listen": "0.0.0.0:8800", "public_url": "http://nas.example.org:8800",
		"web": {"listen": "0.0.0.0:8443", "hostnames": ["nas.example.org"]},
		"token_file": "/secrets/core-token", "db": "/data/core.db", "user": "Sam",
		"providers": {"anthropic": {"kind": "claude-subscription", "secret": "`+secret+`"}},
		"agents": {"opus": {"runtime": "claude-code", "provider": "anthropic", "model": "opus"}},
		"default_agent": "opus", "identities": {"github": {"env": "GH_TOKEN", "file": "/secrets/github-token"}},
		"brain": {"node": "nas", "agent": "opus", "prompt_file": "`+prompt+`", "self_url": "http://eriphe-core:8800/mcp",
			"mcp_servers": {"extra": {"url": "http://extra.example.org/mcp"}}, "tools": ["docs"]},
		"policy": {"rules": [{"tool": "Bash", "match": "^go test", "decision": "allow"}]}}`), 0o600)
	c, err := loadOrDefault(path, filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	b := c.Brain
	if c.Listen != "0.0.0.0:8800" || c.PublicURL != "http://nas.example.org:8800" || c.Web.Listen != "0.0.0.0:8443" ||
		c.TokenFile != "/secrets/core-token" || c.DB != "/data/core.db" || c.User != "Sam" || c.DefaultAgent != "opus" ||
		c.Identities["github"].Env != "GH_TOKEN" || len(c.Policy.Rules) != 1 {
		t.Fatalf("config = %+v", c)
	}
	if b.Node != "nas" || b.Agent != "opus" || b.SelfURL != "http://eriphe-core:8800/mcp" || b.MCPServers["extra"].URL == "" ||
		strings.Join(b.Tools, ",") != "docs" {
		t.Fatalf("brain = %+v", b)
	}
	if got, err := b.assistant().standingPrompt(); err != nil || got != "Mes consignes pour {{user}}" {
		t.Fatalf("prompt file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); err == nil {
		t.Fatal("the data directory made with a file")
	}
}

// Without prompt_file, the brain gets the instructions built in.
func TestBuiltinBrainPrompt(t *testing.T) {
	raw, _ := os.ReadFile("brain-prompt.md")
	got, err := (&BrainConfig{}).assistant().standingPrompt()
	if err != nil || got != string(raw) || !strings.Contains(got, "{{user}}") {
		t.Fatalf("built-in prompt = %q, %v", truncate(got, 80), err)
	}
	if _, err := (&BrainConfig{PromptFile: "/nonexistent/brain.md"}).assistant().standingPrompt(); err == nil {
		t.Fatal("a missing prompt file is not an error")
	}
}
