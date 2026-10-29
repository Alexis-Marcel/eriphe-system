package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is eriphe-core's configuration file (JSON). Secrets are never in it:
// it only points at files.
type Config struct {
	Listen    string              `json:"listen"`               // e.g. "0.0.0.0:8800"
	PublicURL string              `json:"public_url,omitempty"` // how nodes reach eriphe-core, for join commands
	TokenFile string              `json:"token_file"`           // bearer token for the MCP and the API
	DB        string              `json:"db"`                   // SQLite file
	User      string              `json:"user,omitempty"`       // how the brain names me
	TimeZone  string              `json:"timezone,omitempty"`   // my IANA time zone; Europe/Paris by default
	Providers map[string]Provider `json:"providers"`            // where models are
	Agents    map[string]Agent    `json:"agents"`               // what the brain can pick for a task
	// The agent of a task that names none.
	DefaultAgent string              `json:"default_agent"`
	Identities   map[string]Identity `json:"identities"` // other credentials a task can borrow, e.g. github
	// The brain: the built-in assistant eriphe (assistants.go). Only the
	// file's: the merged configuration has it in Assistants.
	Brain *BrainConfig `json:"brain,omitempty"`
	// Other long-lived assistants, by name (assistants.go).
	Assistants map[string]Assistant `json:"assistants,omitempty"`
	Policy     *Policy              `json:"policy,omitempty"` // permission rules, on top of the built-in ones
	// The tool catalog: MCP servers tasks reach through eriphe-core (servers.go).
	Servers map[string]Server `json:"servers,omitempty"`
	// Cloud storage accounts: where the Eriphe app's OAuth client is (cloud.go).
	Cloud *CloudConfig `json:"cloud,omitempty"`

	// The run specs of the catalog's local servers, by name (services.go).
	runSpecs map[string]RunSpec
	location *time.Location // TimeZone, loaded
	fileOnly bool           // while the file is checked alone, before the workspace's catalog
	// The vault's key file, if ERIPHE_VAULT_KEY is not set; vault.key next to the database by default.
	VaultKeyFile string     `json:"vault_key_file,omitempty"`
	Web          *WebConfig `json:"web,omitempty"` // the workspace (web.go); off without it

	Memory *MemoryConfig `json:"memory,omitempty"` // Eriphe's memory (memory.go)
}

// BrainConfig describes the permanent orchestrator: a long-lived task on a
// node that only has MCP tools (eriphe-core's own, plus the servers listed).
// It is the spec of the built-in assistant eriphe (assistants.go).
type BrainConfig struct {
	Node  string `json:"node,omitempty"`  // empty lets the scheduler choose
	Agent string `json:"agent,omitempty"` // empty lets the workspace choose (catalog.go)
	// Its standing instructions: brain-prompt.md, built in, unless a file
	// replaces them. {{user}} is replaced by the user's name.
	PromptFile string `json:"prompt_file,omitempty"`
	// eriphe-core's MCP URL as seen from the brain's node. Without it, the
	// brain reaches eriphe-core through its node, as it reaches the catalog.
	SelfURL string `json:"self_url,omitempty"`
	// Tools is the brain's grant from the catalog, like a task's: server
	// names or server.tool globs.
	Tools []string `json:"tools,omitempty"`
	// Deprecated: MCP servers given to the brain directly, with their token,
	// and allowed without asking. Move them to servers and brain.tools.
	MCPServers map[string]MCPServer `json:"mcp_servers,omitempty"`
}

// MCPServer is an extra HTTP MCP server for the brain; its bearer token
// comes from a file or an environment variable of the brain's process.
// Deprecated: see BrainConfig.MCPServers.
type MCPServer struct {
	URL       string `json:"url"`
	TokenFile string `json:"token_file,omitempty"`
	TokenEnv  string `json:"token_env,omitempty"`
}

// Identity is a credential eriphe-core lends to a task: the content of File
// is passed to the agent as the environment variable Env.
type Identity struct {
	Env  string `json:"env"`
	File string `json:"file"`
}

// defaultData is where eriphe-core keeps its files without a configuration
// file: the image's volume.
const defaultData = "/data"

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// loadOrDefault reads the configuration file, as it is, or, without one
// (none given, or none there), runs on defaults with every file in the
// data directory.
func loadOrDefault(path, data string) (*Config, error) {
	if path != "" {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			return loadConfig(path)
		}
		log.Printf("%s: no such file: running on defaults in %s", path, data)
	}
	return defaultConfig(data)
}

// defaultConfig is eriphe-core without a configuration file: its API on
// every interface, the workspace and the brain on, nothing else. Its files
// are in data, made at first start and reused after: the API token here,
// the vault key, the database and the workspace's certificate where they
// are opened.
func defaultConfig(data string) (*Config, error) {
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	c := &Config{Listen: "0.0.0.0:8800", TokenFile: filepath.Join(data, "core-token"), DB: filepath.Join(data, "core.db"),
		Web: &WebConfig{}, Brain: &BrainConfig{}}
	// The container's own zone, when it is given one.
	if tz := os.Getenv("TZ"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			c.TimeZone = tz
		}
	}
	if err := ensureToken(c.TokenFile); err != nil {
		return nil, fmt.Errorf("token: %w (can this user write to %s?)", err, data)
	}
	if err := c.complete("defaults"); err != nil {
		return nil, err
	}
	return c, nil
}

// ensureToken creates the API token's file, unless it exists.
func ensureToken(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	b := make([]byte, 32)
	rand.Read(b)
	_, err = f.WriteString(hex.EncodeToString(b) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		log.Printf("created the API token in %s", path)
	}
	return err
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err = json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.TokenFile == "" {
		return nil, fmt.Errorf("%s: token_file is required", path)
	}
	if err := c.complete(path); err != nil {
		return nil, err
	}
	return &c, nil
}

// complete fills in what a configuration leaves out, and checks it. where
// is what the errors name: the file, or "defaults".
func (c *Config) complete(where string) error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8800"
	}
	if c.DB == "" {
		c.DB = "eriphe-core.db"
	}
	if c.User == "" {
		c.User = "l'utilisateur"
	}
	if c.TimeZone == "" {
		c.TimeZone = "Europe/Paris"
	}
	var err error
	if c.location, err = time.LoadLocation(c.TimeZone); err != nil {
		return fmt.Errorf("%s: timezone %q: %w", where, c.TimeZone, err)
	}
	c.fileOnly = true
	err = c.checkAgents()
	c.fileOnly = false
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if err := c.checkServers(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if c.runSpecs, err = c.localServers(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if c.Brain != nil && len(c.Brain.MCPServers) > 0 {
		log.Printf("%s: brain.mcp_servers is deprecated: move them to servers and brain.tools", where)
	}
	if err := c.checkFileAssistants(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if c.Policy != nil {
		if err := c.Policy.compile(); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}
	return nil
}

// readSecret reads a secret from a file, or from the vault for "vault:<name>".
func readSecret(path string) (string, error) {
	if name, ok := strings.CutPrefix(path, vaultPrefix); ok {
		return vaultSecret(name)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return s, nil
}

// lend reads identities for one task, into env. The values go to the node
// and are never stored or logged.
func (c *Config) lend(names []string, env map[string]string) error {
	for _, name := range names {
		id, ok := c.Identities[name]
		if !ok {
			return fmt.Errorf("unknown identity %q", name)
		}
		v, err := readSecret(id.File)
		if err != nil {
			return fmt.Errorf("identity %q: %w", name, err)
		}
		env[id.Env] = v
	}
	return nil
}
