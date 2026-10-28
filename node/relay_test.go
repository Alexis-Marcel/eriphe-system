package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A task container's call to a catalog server goes to eriphe-core under
// the node's identity, never with the task's secret, and only for the
// servers lent to the task.
func TestRelay(t *testing.T) {
	var got *http.Request
	var body string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {}\n\n")
	}))
	defer core.Close()

	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1", State: stateRunning, secret: "task-secret", Spec: TaskSpec{Servers: []string{"github"}}}
	n.tasks[task.ID] = task
	mux := http.NewServeMux()
	mux.Handle("/mcp/{server}", n.relayHandler(&state{Core: core.URL, Name: "pc", Secret: "node-secret"}))
	relay := httptest.NewServer(mux)
	defer relay.Close()

	post := func(server, secret string) *http.Response {
		req, _ := http.NewRequest("POST", relay.URL+"/mcp/"+server, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := post("github", "task-secret"); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("relayed: %s", resp.Status)
	}
	if got.URL.Path != "/v1/node/tasks/t-1/mcp/github" || got.Header.Get("Authorization") != "Bearer node-secret" ||
		got.Header.Get("Eriphe-Node") != "pc" || !strings.Contains(body, "tools/list") {
		t.Fatalf("core got %s %v %q", got.URL.Path, got.Header, body)
	}
	got = nil
	if resp := post("github", "wrong"); resp.StatusCode != http.StatusUnauthorized || got != nil {
		t.Fatalf("wrong secret: %s", resp.Status)
	}
	if resp := post("mail", "task-secret"); resp.StatusCode != http.StatusForbidden || got != nil {
		t.Fatalf("server not lent: %s", resp.Status)
	}
	task.State = stateStopped
	if resp := post("github", "task-secret"); resp.StatusCode != http.StatusNotFound || got != nil {
		t.Fatalf("finished task: %s", resp.Status)
	}
}

// Claude Code is told to reach each lent server through the node.
func TestClaudeCatalogServers(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1", secret: "task-secret", Spec: TaskSpec{Servers: []string{"github"}}}
	args, _ := claudeCode{}.command(n, task)
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	for i, a := range args {
		if a == "--mcp-config" {
			json.Unmarshal([]byte(args[i+1]), &cfg)
		}
	}
	gh := cfg.MCPServers["github"]
	if gh.URL != "http://eriphe-node:8790/mcp/github" || gh.Headers["Authorization"] != "Bearer task-secret" {
		t.Fatalf("mcp config = %+v", cfg)
	}
}
