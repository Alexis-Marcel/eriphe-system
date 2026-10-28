package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client speaks eriphe-core's HTTP API: the conversation with the brain and
// the worker tasks.
type Client struct {
	URL   string // eriphe-core's base URL, e.g. http://host:8800
	Token string
	HTTP  *http.Client
}

func NewClient(baseURL, token string) *Client {
	// No global timeout: long polls set their own through the context.
	return &Client{URL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{}}
}

// Message is one entry of the conversation: role is user, brain or event.
type Message struct {
	Seq  int       `json:"seq"`
	Time time.Time `json:"time"`
	Role string    `json:"role"`
	Text string    `json:"text"`
}

// Task is a worker task as eriphe-core lists it.
type Task struct {
	ID         string    `json:"id"`
	Node       string    `json:"node"`
	Agent      string    `json:"agent,omitempty"`
	Runtime    string    `json:"runtime"`
	Model      string    `json:"model,omitempty"`
	Identities string    `json:"identities,omitempty"`
	Dir        string    `json:"dir"`
	Prompt     string    `json:"prompt"`
	State      string    `json:"state"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

// Event is one thing that happened in a task: text, tool_use, permission,
// permission_answer, turn_end, error or state.
type Event struct {
	Seq  int            `json:"seq"`
	Time time.Time      `json:"time"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// TaskStatus is a task with what it waits for and its recent events.
type TaskStatus struct {
	Task              Task           `json:"task"`
	PendingPermission map[string]any `json:"pending_permission,omitempty"`
	LastText          string         `json:"last_text,omitempty"`
	AuthError         bool           `json:"auth_error,omitempty"`
	Recent            []Event        `json:"recent"`
}

// APIError is an answer of eriphe-core other than a success.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("eriphe-core: %d %s", e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("eriphe-core: %d %s", e.Status, e.Msg)
}

// call sends one request; timeout bounds it on top of ctx (0 for none).
func (c *Client) call(ctx context.Context, method, path string, timeout time.Duration, in, out any) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return &APIError{Status: resp.StatusCode, Msg: strings.TrimSpace(string(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// pollTimeout leaves the server time to answer a long poll of wait seconds.
func pollTimeout(wait int) time.Duration { return time.Duration(wait)*time.Second + 15*time.Second }

const (
	shortTimeout  = 15 * time.Second
	actionTimeout = time.Minute // answers and stops go through to the node
)

// Messages returns the conversation after seq after, waiting up to wait
// seconds for something new.
func (c *Client) Messages(ctx context.Context, after, wait int) ([]Message, error) {
	var out []Message
	err := c.call(ctx, http.MethodGet, fmt.Sprintf("/v1/messages?after=%d&wait=%d", after, wait), pollTimeout(wait), nil, &out)
	return out, err
}

// Send posts a message to the brain and returns its seq.
func (c *Client) Send(ctx context.Context, text string) (int, error) {
	var out struct {
		Seq int `json:"seq"`
	}
	err := c.call(ctx, http.MethodPost, "/v1/messages", shortTimeout, map[string]string{"text": text}, &out)
	return out.Seq, err
}

// Tasks lists worker tasks, the most recent first; finished ones too if all.
func (c *Client) Tasks(ctx context.Context, all bool) ([]Task, error) {
	path := "/v1/tasks"
	if all {
		path += "?all=1"
	}
	var out []Task
	err := c.call(ctx, http.MethodGet, path, shortTimeout, nil, &out)
	return out, err
}

// Task returns a task's status with its last n events.
func (c *Client) Task(ctx context.Context, id string, n int) (*TaskStatus, error) {
	var out TaskStatus
	err := c.call(ctx, http.MethodGet, fmt.Sprintf("/v1/tasks/%s?events=%d", url.PathEscape(id), n), shortTimeout, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Events returns a task's events after seq after, waiting up to wait seconds.
func (c *Client) Events(ctx context.Context, id string, after, wait int) ([]Event, error) {
	var out []Event
	err := c.call(ctx, http.MethodGet, fmt.Sprintf("/v1/tasks/%s/events?after=%d&wait=%d", url.PathEscape(id), after, wait),
		pollTimeout(wait), nil, &out)
	return out, err
}

// Answer allows or denies a task's pending permission (allow set; permission
// names it, needed when several are pending), or replies to an idle task.
func (c *Client) Answer(ctx context.Context, id, permission, reply string, allow *bool) (string, error) {
	in := map[string]any{"reply": reply}
	if permission != "" {
		in["permission_id"] = permission
	}
	if allow != nil {
		in["allow"] = *allow
	}
	var out struct {
		Result string `json:"result"`
	}
	err := c.call(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(id)+"/answer", actionTimeout, in, &out)
	return out.Result, err
}

// Stop stops a task's agent.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(id)+"/stop", actionTimeout, nil, nil)
}
