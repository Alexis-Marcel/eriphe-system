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

// nodeClient talks to one eriphe-node, through the tunnel the node opened.
type nodeClient struct {
	name   string
	http   *http.Client
	stream *http.Client // the same tunnel without a timeout, for MCP sessions

	mu      sync.Mutex
	session *yamux.Session // nil while the node is not connected
	tls     bool           // the session came with the node's certificate
}

func newNodeClient(name string) *nodeClient {
	n := &nodeClient{name: name}
	// Every request is a new stream of the tunnel; the host in the URL is unused.
	n.http = &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{
		DialContext: n.dial, MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute}}
	n.stream = &http.Client{Transport: n.http.Transport}
	return n
}

func (n *nodeClient) connected() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.session != nil
}

func (c *nodeClient) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://node"+path, body)
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
		return &nodeError{status: resp.StatusCode, msg: string(bytes.TrimSpace(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type nodeError struct {
	status int
	msg    string
}

func (e *nodeError) Error() string { return fmt.Sprintf("node: %d %s", e.status, e.msg) }

func (c *nodeClient) info(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var raw json.RawMessage
	err := c.call(ctx, http.MethodGet, "/v1/node", nil, &raw)
	return raw, err
}

type nodeTaskSpec struct {
	Runtime  string            `json:"runtime"`
	Prompt   string            `json:"prompt"`
	Dir      string            `json:"dir,omitempty"`
	Repo     string            `json:"repo,omitempty"`
	Ref      string            `json:"ref,omitempty"`
	Branch   string            `json:"branch,omitempty"`
	Image    string            `json:"image,omitempty"`
	Model    string            `json:"model,omitempty"`
	Command  []string          `json:"command,omitempty"` // an ACP agent's command line (runtime acp)
	Identity map[string]string `json:"identity,omitempty"`
	Resume   string            `json:"resume,omitempty"`
	TZ       string            `json:"tz,omitempty"` // the user's time zone

	// Catalog servers the node relays to eriphe-core for the task (proxy.go).
	Servers            []string       `json:"servers,omitempty"`
	AlwaysLoad         []string       `json:"always_load,omitempty"` // those of Servers loaded upfront
	MCPServers         map[string]any `json:"mcp_servers,omitempty"`
	AppendSystemPrompt string         `json:"append_system_prompt,omitempty"`
	AllowedTools       []string       `json:"allowed_tools,omitempty"`
	Profile            string         `json:"profile,omitempty"`
	SystemNetwork      bool           `json:"system_network,omitempty"`  // the brain reaches eriphe-core by name
	Stream             bool           `json:"stream,omitempty"`          // the brain: its text as it is written (brain_draft.go)
	NoAgentMemory      bool           `json:"no_agent_memory,omitempty"` // the brain: Eriphe's memory is its only one

	// The user's folders lent to the task, mounted by its node (folders.go).
	Folders []nodeFolder `json:"folders,omitempty"`

	// Where the task may connect: the node's egress relay enforces it.
	Egress *nodeEgress `json:"egress,omitempty"`
}

// nodeEgress is a task's egress rule, like a Kubernetes NetworkPolicy:
// "internet" (public addresses only, never the LAN) or "none", and when
// Allow is set, only those hosts ("github.com", "*.npmjs.org").
type nodeEgress struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow,omitempty"`
}

// defaultEgress is every task's rule for now: the public Internet.
func defaultEgress() *nodeEgress { return &nodeEgress{Mode: "internet"} }
