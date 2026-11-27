package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// deviceClient talks to one eriphe-device, through the tunnel the device opened.
type deviceClient struct {
	name   string
	http   *http.Client
	stream *http.Client // the same tunnel without a timeout, for MCP sessions

	mu      sync.Mutex
	session *yamux.Session // nil while the device is not connected
	tls     bool           // the session came with the device's certificate
}

func newDeviceClient(name string) *deviceClient {
	n := &deviceClient{name: name}
	// Every request is a new stream of the tunnel; the host in the URL is unused.
	n.http = &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{
		DialContext: n.dial, MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute}}
	n.stream = &http.Client{Transport: n.http.Transport}
	return n
}

func (n *deviceClient) connected() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.session != nil
}

func (c *deviceClient) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://device"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return &deviceError{status: resp.StatusCode, msg: string(bytes.TrimSpace(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type deviceError struct {
	status int
	msg    string
}

func (e *deviceError) Error() string { return fmt.Sprintf("device: %d %s", e.status, e.msg) }

// info is the device's status. level is the context level it may report at
// (context.go): 0 nothing of the user, 1 idle and do not disturb, 2 the
// category of the app in front too. A device older than levels ignores it.
func (c *deviceClient) info(ctx context.Context, level int) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var raw json.RawMessage
	err := c.call(ctx, http.MethodGet, fmt.Sprintf("/v1/device?context=%d", level), nil, &raw)
	return raw, err
}

type deviceTaskSpec struct {
	Runtime  string            `json:"runtime"`
	Prompt   string            `json:"prompt"`
	Dir      string            `json:"dir,omitempty"`
	Repo     string            `json:"repo,omitempty"`
	Ref      string            `json:"ref,omitempty"`
	Branch   string            `json:"branch,omitempty"`
	Image    string            `json:"image,omitempty"`
	Model    string            `json:"model,omitempty"`
	Command  []string          `json:"command,omitempty"` // an ACP agent's command line (runtime acp)
	AgentEnv map[string]string `json:"agent_env,omitempty"`
	Resume   string            `json:"resume,omitempty"`
	TZ       string            `json:"tz,omitempty"` // the user's time zone

	// Catalog servers the device relays to eriphe-core for the task (proxy.go).
	Servers            []string       `json:"servers,omitempty"`
	AlwaysLoad         []string       `json:"always_load,omitempty"` // those of Servers loaded upfront
	MCPServers         map[string]any `json:"mcp_servers,omitempty"`
	AppendSystemPrompt string         `json:"append_system_prompt,omitempty"`
	AllowedTools       []string       `json:"allowed_tools,omitempty"`
	HarnessTools       []string       `json:"harness_tools,omitempty"` // an assistant: the only harness tools it keeps
	Profile            string         `json:"profile,omitempty"`
	SystemNetwork      bool           `json:"system_network,omitempty"`  // the brain reaches eriphe-core by name
	Stream             bool           `json:"stream,omitempty"`          // the brain: its text as it is written (brain_draft.go)
	NoAgentMemory      bool           `json:"no_agent_memory,omitempty"` // the brain: Eriphe's graph is its only memory
	// RepoSkills: the agent loads the skills its repository carries (the
	// user's own repository, or one trusted); off otherwise (repo_skills.go).
	RepoSkills bool `json:"repo_skills,omitempty"`
	// The user's skills, approved and on when the task started: its device
	// fetches and mounts them (skills_tasks.go).
	Skills []deviceSkill `json:"skills,omitempty"`

	// The user's folders lent to the task, mounted by its device (folders.go).
	Folders []deviceFolder `json:"folders,omitempty"`

	// Where the task may connect: the device's egress relay enforces it.
	Egress *deviceEgress `json:"egress,omitempty"`

	// What it uses with its own programs: the device decrypts those hosts
	// and hands their requests to eriphe-core (lent_access.go). An older device
	// ignores them: the placeholder then goes out as it is, and fails.
	Accesses []deviceAccess `json:"accesses,omitempty"`

	// The self-hosted model server of the task's agent (OpenCode), set
	// when the device fetches the task; its address is in Egress.Endpoints.
	ModelEndpoint *deviceModelEndpoint `json:"model_endpoint,omitempty"`

	// The checks of the task's contract, which its device runs in the
	// container when a turn ends, and how many times a failed one goes back
	// to the agent (contract.go). An older device ignores them.
	Checks      []taskCheck `json:"checks,omitempty"`
	CheckRounds int         `json:"check_rounds,omitempty"`

	// Untrusted: started on what came from outside (taskUntrusted); its
	// device gives it a cache of its own. An older device ignores it.
	Untrusted bool `json:"untrusted,omitempty"`
}

// deviceAccess is one access as a task's device gets it: never its secret.
type deviceAccess struct {
	Name        string     `json:"name"`
	Hosts       []string   `json:"hosts"` // host:port
	Env         string     `json:"env"`
	Placeholder string     `json:"placeholder"`
	SSH         *deviceSSH `json:"ssh,omitempty"` // an SSH access: no hosts, no placeholder
}

// deviceSSH is what a task's ssh is configured with: no secret.
type deviceSSH struct {
	Target    string `json:"target"` // host:port
	User      string `json:"user"`
	HostKey   string `json:"host_key"`
	PublicKey string `json:"public_key"`
}

// deviceModelEndpoint is where a self-hosted model server answers, and the
// variable of AgentEnv that carries its key, if it has one; or, with Relay,
// that the server is behind a device: the task reaches it through its own
// device and eriphe-core (model_relay.go).
type deviceModelEndpoint struct {
	BaseURL string `json:"base_url,omitempty"`
	KeyEnv  string `json:"key_env,omitempty"`
	Relay   bool   `json:"relay,omitempty"`
}

// deviceEgress is what a task may reach beyond the public Internet: Endpoints,
// host:port even on the LAN or another port, its self-hosted model server's.
type deviceEgress struct {
	Endpoints []string `json:"endpoints,omitempty"`
}

// defaultEgress is every task's rule for now: the public Internet.
func defaultEgress() *deviceEgress { return &deviceEgress{} }
