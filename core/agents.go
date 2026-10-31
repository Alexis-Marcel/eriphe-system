package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	// A self-hosted model server (openai-compatible): its /v1 address and
	// the models it serves. Neither is secret; its key, if any, is.
	BaseURL string   `json:"base_url,omitempty"`
	Models  []string `json:"models,omitempty"`
	// Or the node that lends it (its -model-server), in place of BaseURL:
	// the tasks reach it through eriphe-core and that node's tunnel, and
	// its key, if any, never leaves eriphe-core (model_relay.go).
	Node string `json:"node,omitempty"`
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
		if err := checkProviderEntry(name, p); err != nil {
			return err
		}
	}
	for name, a := range c.Agents {
		if err := c.checkAgentEntry(name, a); err != nil {
			return err
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

// checkProviderEntry checks one provider of the configuration.
func checkProviderEntry(name string, p Provider) error {
	k, ok := kindOf(p.Kind)
	if !ok {
		return fmt.Errorf("provider %s: unknown kind %q", name, p.Kind)
	}
	if p.Kind == selfHostedKind {
		if err := checkSelfHostedProvider(p); err != nil {
			return fmt.Errorf("provider %s: %w", name, err)
		}
	} else if p.Secret == "" {
		return fmt.Errorf("provider %s: secret is required", name)
	} else if p.BaseURL != "" || len(p.Models) > 0 || p.Node != "" {
		return fmt.Errorf("provider %s: only a self-hosted model server (%s) takes base_url, node and models", name, selfHostedKind)
	}
	if k.CustomEnv {
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
	return nil
}

// checkSelfHostedProvider checks where a self-hosted model server is, and
// its models. Its key is optional: a model server on the LAN often has none.
func checkSelfHostedProvider(p Provider) error {
	switch {
	case p.Node != "" && p.BaseURL != "":
		return errors.New("base_url or node, not both")
	case p.Node != "":
		if !nodeName.MatchString(p.Node) {
			return fmt.Errorf("bad node %q", p.Node)
		}
	default:
		if err := checkBaseURL(p.BaseURL); err != nil {
			return err
		}
	}
	return checkModelIDs(p.Models)
}

// checkAgentEntry checks one agent of the configuration against its provider.
func (c *Config) checkAgentEntry(name string, a Agent) error {
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
	return nil
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
	if p.Secret == "" || p.Kind == selfHostedKind && p.Node != "" {
		// A self-hosted model server without a key, or one behind a node,
		// whose key eriphe-core adds on the way (model_relay.go).
		return map[string]string{}, nil
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

// modelEndpoint points a task whose agent uses a self-hosted model server
// at it: its address and key variable for OpenCode, and that one host:port
// added to its egress rule, which its relay lets through even on the LAN.
// A task without egress gets none. env is what lendAgent lent.
func (c *Config) modelEndpoint(agentName string, env map[string]string, spec *nodeTaskSpec) {
	_, a, err := c.agent(agentName)
	if err != nil {
		return
	}
	p := c.Providers[a.Provider]
	if p.Kind != selfHostedKind || a.Runtime != "opencode" {
		return
	}
	if p.Node != "" {
		// Behind a node: through the task's own node and eriphe-core, never
		// the network; its egress rule stays as it is.
		spec.ModelEndpoint = &nodeModelEndpoint{Relay: true}
		return
	}
	k, _ := kindOf(selfHostedKind)
	ep := &nodeModelEndpoint{BaseURL: p.BaseURL}
	if _, ok := env[k.Env]; ok {
		ep.KeyEnv = k.Env
	}
	spec.ModelEndpoint = ep
	hostport, ok := endpointHostPort(p.BaseURL)
	if !ok || spec.Egress == nil || spec.Egress.Mode == "none" {
		return
	}
	// The rule may be shared with the stored spec: a copy is changed.
	e := *spec.Egress
	e.Allow = slices.Clone(e.Allow)
	if !slices.Contains(e.Endpoints, hostport) {
		e.Endpoints = append(slices.Clone(e.Endpoints), hostport)
	}
	spec.Egress = &e
}

// checkBaseURL tells whether a self-hosted model server's address can be
// used: http(s), a host, no credentials, query nor fragment, and a host
// the tasks can reach. Tasks run in containers, where the loopback is not
// the model server's machine, and a link-local address is the cloud's
// metadata: the user gives the machine's LAN address or a public host.
func checkBaseURL(raw string) error {
	if raw == "" {
		return errors.New("a self-hosted model server needs its base_url (http://192.0.2.10:11434/v1)")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("base_url %q is not an http(s) address", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "# ") || strings.ContainsFunc(raw, func(r rune) bool { return !configSafe(r) }) {
		return fmt.Errorf("base_url %q: an address without credentials, query or fragment", raw)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("base_url %q: bad port", raw)
		}
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("base_url %q: tasks run in containers, where localhost is not the model server's machine; give its address on the network", raw)
	}
	if a, err := netip.ParseAddr(host); err == nil && !reachableEndpoint(a) {
		return fmt.Errorf("base_url %q: %s cannot be a model server's address (loopback, link-local, multicast or unspecified); give the machine's address on the network", raw, host)
	}
	return nil
}

// reachableEndpoint tells whether an address may be a model server's: any
// unicast address, private ones included, but not the loopback, link-local
// (169.254.169.254 is the cloud's metadata), multicast, unspecified nor
// "this network" ones.
func reachableEndpoint(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && a.IsGlobalUnicast() && !(a.Is4() && a.As4()[0] == 0)
}

// endpointHostPort is the host:port a base URL connects to.
func endpointHostPort(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port), true
}

// checkModelIDs tells whether a self-hosted server's models can be named:
// one word each, as the server lists them (qwen3:8b, org/model).
func checkModelIDs(models []string) error {
	if len(models) > 64 {
		return errors.New("more than 64 models")
	}
	for _, m := range models {
		if m == "" || len(m) > 200 || strings.ContainsFunc(m, func(r rune) bool { return r <= ' ' || !configSafe(r) }) {
			return fmt.Errorf("model %q is not a model name (one word, as the server lists it)", m)
		}
	}
	return nil
}

// selfHostedKey tells whether a self-hosted server's key can be lent: OpenCode
// substitutes it into its configuration's text ({env:…}), where a quote, a
// backslash, a brace or a control character would not stay a key.
func selfHostedKey(key string) bool {
	return !strings.ContainsFunc(key, func(r rune) bool { return !configSafe(r) })
}

// configSafe tells whether a character of a self-hosted server's address,
// model or key can go into OpenCode's configuration text: not a quote nor
// a backslash (they end a JSON string), not a brace (OpenCode expands
// {env:…} and {file:…}), not a control character.
func configSafe(r rune) bool {
	return r >= ' ' && r != 0x7f && r != '"' && r != '\\' && r != '{' && r != '}'
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
