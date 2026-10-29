package main

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Two levels, like a RuntimeClass: providers are where models live and the
// credential to reach them; agents are what the brain picks for a task: a
// runtime, a provider, a model, and when to use it. The brain is an agent too.

// Provider is a connection to models. Its secret is lent to each task that
// uses it, as the environment variable its kind expects.
type Provider struct {
	Kind   string `json:"kind"`   // claude-subscription, anthropic-api, openai-api, ...
	Secret string `json:"secret"` // file holding the credential
	// Env is the variable that carries it, for a kind that lets the
	// provider name it (custom-api); the kind's otherwise.
	Env string `json:"env,omitempty"`
}

// Agent is one choice the brain has for a task.
type Agent struct {
	Runtime  string `json:"runtime"`         // agent program the node injects into the task: claude-code, opencode, codex, or acp (the image's own)
	Provider string `json:"provider"`        // key of providers
	Model    string `json:"model,omitempty"` // model name for the runtime (opencode: provider/model); empty for its default
	Image    string `json:"image,omitempty"` // worker image; the node's default if empty
	// Command is an ACP agent's command line (runtime acp), one argument
	// per item, run as is in the container: never through a shell.
	Command []string `json:"command,omitempty"`
	Use     string   `json:"use"`             // when to pick this agent, for the brain
	Needs   []string `json:"needs,omitempty"` // node labels required, e.g. gpu
	// Soft preferences of the scheduler: cpu (more CPUs), memory (more RAM),
	// or any other word, a node label to prefer.
	Prefers []string `json:"prefers,omitempty"`
}

func (a Agent) placement() placement {
	return placement{Runtime: a.Runtime, Needs: a.Needs, Prefers: a.Prefers, Image: a.Image}
}

func (c *Config) checkAgents() error {
	for name, p := range c.Providers {
		if _, ok := kindOf(p.Kind); !ok {
			return fmt.Errorf("provider %s: unknown kind %q", name, p.Kind)
		}
		if p.Secret == "" {
			return fmt.Errorf("provider %s: secret is required", name)
		}
		if k, _ := kindOf(p.Kind); k.CustomEnv {
			if err := checkCredentialEnv(p.Env); err != nil {
				return fmt.Errorf("provider %s: %w", name, err)
			}
		} else if p.Env != "" {
			return fmt.Errorf("provider %s: a %s provider's variable is %s, it takes no env", name, p.Kind, k.Env)
		}
		// Its sign-in is refreshed, and the new tokens kept: in the vault.
		if p.Kind == "chatgpt-subscription" && !strings.HasPrefix(p.Secret, vaultPrefix) {
			return fmt.Errorf("provider %s: a ChatGPT subscription is signed in from the workspace, its secret is vault:<name>", name)
		}
	}
	for name, a := range c.Agents {
		p, ok := c.Providers[a.Provider]
		if !ok {
			return fmt.Errorf("agent %s: unknown provider %q", name, a.Provider)
		}
		k, _ := kindOf(p.Kind)
		if !slices.Contains(k.Runtimes, a.Runtime) {
			return fmt.Errorf("agent %s: runtime %q cannot use a %s provider", name, a.Runtime, p.Kind)
		}
		// OpenCode picks the key by the model's provider: they must agree.
		if want := k.ModelNS + "/"; harnessOf(a.Runtime).ProviderModels && !strings.HasPrefix(a.Model, want) {
			return fmt.Errorf("agent %s: an %s model with a %s provider is %s<model>, not %q", name, a.Runtime, p.Kind, want, a.Model)
		}
		if a.Runtime == "acp" {
			if err := checkACPCommand(a.Command); err != nil {
				return fmt.Errorf("agent %s: %w", name, err)
			}
			if a.Model != "" {
				return fmt.Errorf("agent %s: an ACP agent runs the model of its own settings or command, not %q", name, a.Model)
			}
		} else if len(a.Command) > 0 {
			return fmt.Errorf("agent %s: only an acp agent takes a command, not a %s one", name, a.Runtime)
		}
		for _, pref := range a.Prefers {
			// Labels are free text, but a preference is one word.
			if pref == "" || strings.ContainsAny(pref, " ,") {
				return fmt.Errorf("agent %s: bad preference %q (cpu, memory or a node label)", name, pref)
			}
		}
	}
	if len(c.Agents) == 0 {
		return nil
	}
	if _, ok := c.Agents[c.DefaultAgent]; !ok {
		return fmt.Errorf("default_agent %q is not an agent", c.DefaultAgent)
	}
	return nil // the assistants' agents are checkAssistants'
}

// agent returns the named agent, or the default one.
func (c *Config) agent(name string) (string, Agent, error) {
	if name == "" {
		name = c.DefaultAgent
	}
	a, ok := c.Agents[name]
	if !ok {
		return "", Agent{}, fmt.Errorf("unknown agent %q (see list_agents)", name)
	}
	return name, a, nil
}

// lendAgent reads the credential of an agent's provider for one task.
func (c *Config) lendAgent(a Agent) (map[string]string, error) {
	p := c.Providers[a.Provider]
	if p.Kind == "chatgpt-subscription" {
		env, err := lendChatGPT(p.Secret)
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", a.Provider, err)
		}
		return env, nil
	}
	v, err := readSecret(p.Secret)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", a.Provider, err)
	}
	k, _ := kindOf(p.Kind)
	if k.CustomEnv {
		return map[string]string{p.Env: v}, nil
	}
	return map[string]string{k.Env: v}, nil
}

// credentialEnv is a variable name as a provider may give it: capitals, digits
// and underscores.
var credentialEnv = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reservedEnv are variables a credential must not take: the container's
// own, the node's and eriphe's, and those that change how a program runs.
var reservedEnv = []string{"PATH", "HOME", "USER", "SHELL", "PWD", "TERM", "LANG", "TZ", "TMPDIR",
	"GH_TOKEN", "GITHUB_TOKEN", "NODE_OPTIONS", "NODE_PATH", "NODE_EXTRA_CA_CERTS", "NODE_TLS_REJECT_UNAUTHORIZED",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "PYTHONPATH", "PYTHONSTARTUP",
	"BASH_ENV", "ENV", "PROMPT_COMMAND", "IFS"}

var reservedEnvPrefixes = []string{"ERIPHE_", "LD_", "DYLD_", "GIT_", "MISE_", "XDG_", "DOCKER_", "NPM_CONFIG_"}

// checkCredentialEnv tells whether a provider may name its variable so.
func checkCredentialEnv(name string) error {
	if !credentialEnv.MatchString(name) {
		return fmt.Errorf("its variable %q is not a name in capitals, digits and _ (OPENAI_API_KEY…)", name)
	}
	if slices.Contains(reservedEnv, name) || slices.ContainsFunc(reservedEnvPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
		return fmt.Errorf("its variable %s is the system's, not a credential's", name)
	}
	return nil
}

// shells cannot start an ACP agent: a command is a program and its
// arguments, not a script to interpret.
var shells = []string{"sh", "bash", "dash", "ash", "zsh", "ksh", "mksh", "fish", "csh", "tcsh", "busybox"}

// checkACPCommand tells whether an ACP agent's command can be run as is:
// a program, by name or absolute path, then its arguments, without a
// shell or a line break.
func checkACPCommand(cmd []string) error {
	if len(cmd) == 0 {
		return errors.New("an acp agent needs its command (goose acp, qwen --acp…)")
	}
	if len(cmd) > 32 {
		return errors.New("its command has more than 32 arguments")
	}
	for _, arg := range cmd {
		if arg == "" || len(arg) > 1024 || strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("its command has an empty, too long or multi-line argument %q", arg)
		}
	}
	prog := cmd[0]
	if strings.ContainsAny(prog, " \t") || strings.Contains(prog, "/") && !path.IsAbs(prog) {
		return fmt.Errorf("its program %q is a name or an absolute path, without spaces", prog)
	}
	if slices.Contains(shells, path.Base(prog)) || path.Base(prog) == "env" {
		return fmt.Errorf("its program is %s: an ACP agent runs as is, not through a shell", path.Base(prog))
	}
	return nil
}

// agentInfo is what the brain sees of an agent.
type agentInfo struct {
	Name      string   `json:"name"`
	Runtime   string   `json:"runtime"`
	Model     string   `json:"model,omitempty"`
	Use       string   `json:"use"`
	Needs     []string `json:"needs,omitempty"`
	Prefers   []string `json:"prefers,omitempty"`
	Default   bool     `json:"default,omitempty"`
	Available bool     `json:"available"` // an online node can run it now
}

func (c *core) listAgents() ([]agentInfo, error) {
	nodes, err := c.store.nodes()
	if err != nil {
		return nil, err
	}
	out := []agentInfo{}
	for name, a := range c.conf().Agents {
		available := false
		for _, n := range nodes {
			if n.Online && fits(n, a.Runtime, a.Needs) {
				available = true
			}
		}
		out = append(out, agentInfo{Name: name, Runtime: a.Runtime, Model: a.Model, Use: a.Use, Needs: a.Needs, Prefers: a.Prefers,
			Default: name == c.conf().DefaultAgent, Available: available})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// fits tells whether a node offers a runtime and carries every label needed.
func fits(n NodeRow, runtime string, needs []string) bool {
	return placement{Runtime: runtime, Needs: needs}.fits(n)
}

// fits tells whether a node can take what p asks: its runtime (or, for a
// service, that it runs services) and every label needed.
func (p placement) fits(n NodeRow) bool {
	info := parseInfo(n)
	if p.Service && !info.Services || !p.Service && !slices.Contains(info.Runtimes, p.Runtime) {
		return false
	}
	for _, l := range p.Needs {
		if !slices.Contains(info.Labels, l) {
			return false
		}
	}
	if p.Machine != "" && n.Name != p.Machine {
		return false
	}
	for _, s := range p.Shares {
		if !slices.ContainsFunc(info.Shares, func(ns nodeShare) bool { return ns.Name == s }) {
			return false
		}
	}
	return true
}
