package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseModelServer(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"http://127.0.0.1:11434":       "http://127.0.0.1:11434",
		" http://127.0.0.1:11434/ ":    "http://127.0.0.1:11434",
		"http://127.0.0.1:11434/v1":    "http://127.0.0.1:11434",
		"http://127.0.0.1:11434/v1/":   "http://127.0.0.1:11434",
		"http://eriphe-ollama:11434":   "http://eriphe-ollama:11434",
		"https://192.0.2.10:8443/llm/": "https://192.0.2.10:8443/llm",
	} {
		if got, err := parseModelServer(in); err != nil || got != want {
			t.Errorf("parseModelServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"127.0.0.1:11434", "ftp://192.0.2.10", "http://", "http://u:p@127.0.0.1:11434", "http://127.0.0.1:11434/?x=1", "http://127.0.0.1:11434/#x"} {
		if got, err := parseModelServer(bad); err == nil {
			t.Errorf("parseModelServer(%q) = %q, accepted", bad, got)
		}
	}
}

// streamingModel is a fake OpenAI-compatible server: GET /v1/models, and a
// chat completion that streams two events, the second only once the test
// has read the first (so that a buffering hop would hang).
func streamingModel(t *testing.T, next <-chan struct{}) (*httptest.Server, *http.Request) {
	got := new(http.Request)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(r.Context())
		switch r.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"qwen3:8b"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-next:
			case <-time.After(5 * time.Second):
			}
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\ndata: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// readStream reads a streamed answer, letting the server go on only after
// its first event came through.
func readStream(t *testing.T, resp *http.Response, next chan<- struct{}) string {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream: %s %s", resp.Status, raw)
	}
	r := bufio.NewReader(resp.Body)
	first, err := r.ReadString('\n')
	if err != nil || !strings.Contains(first, "Hel") {
		t.Fatalf("first event %q, %v", first, err)
	}
	close(next)
	rest, _ := io.ReadAll(r)
	return first + string(rest)
}

// The node lends its model server to eriphe-core through the tunnel: only
// its /v1 API, GET and POST, streaming as it comes.
func TestNodeModelServer(t *testing.T) {
	next := make(chan struct{})
	model, got := streamingModel(t, next)
	n := newNode(options{modelServer: model.URL})
	api := http.NewServeMux()
	n.routes(api)
	tunnel := httptest.NewServer(api) // what eriphe-core reaches through the tunnel
	defer tunnel.Close()

	resp, err := http.Get(tunnel.URL + "/v1/model/models")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "qwen3:8b") || got.URL.Path != "/v1/models" {
		t.Fatalf("models: %s %s, server got %s", resp.Status, raw, got.URL.Path)
	}

	req, _ := http.NewRequest("POST", tunnel.URL+"/v1/model/chat/completions", strings.NewReader(`{"model":"qwen3:8b","stream":true}`))
	req.Header.Set("Authorization", "Bearer server-key")
	req.Header.Set("Cookie", "a=b")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if body := readStream(t, resp, next); !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream %q", body)
	}
	if got.URL.Path != "/v1/chat/completions" || got.Header.Get("Authorization") != "Bearer server-key" || got.Header.Get("Cookie") != "" {
		t.Fatalf("server got %s %v", got.URL.Path, got.Header)
	}

	// Only GET and POST.
	req, _ = http.NewRequest("DELETE", tunnel.URL+"/v1/model/models", nil)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %s", resp.Status)
	}
	// Nothing outside /v1: Ollama's own API (pull, delete) stays out of reach.
	got.URL.Path = ""
	for _, p := range []string{"..%2Fapi%2Ftags", "%2E%2E/api/pull", "x/..%2F..%2Fapi%2Fdelete", "a%5Cb"} {
		if resp, _ := http.Get(tunnel.URL + "/v1/model/" + p); resp.StatusCode != http.StatusBadRequest || got.URL.Path != "" {
			t.Errorf("%s: %s, server got %q", p, resp.Status, got.URL.Path)
		}
	}
	for p, ok := range map[string]bool{"chat/completions": true, "models": true, "models/": true, "": false, "../api/tags": false, "a/../b": false} {
		if _, got := modelPath(p); got != ok {
			t.Errorf("modelPath(%q) = %v", p, got)
		}
	}
	// The server down: a gateway error, saying so.
	model.Close()
	if resp, _ := http.Get(tunnel.URL + "/v1/model/models"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("server down: %s", resp.Status)
	}
	// A node without -model-server lends nothing.
	n.opt.modelServer = ""
	if resp, _ := http.Get(tunnel.URL + "/v1/model/models"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("no model server: %s", resp.Status)
	}
}

// A task reaches its model behind a node through its own node: relayed to
// eriphe-core under the node's identity, streaming, and only for a task
// whose agent uses one.
func TestModelRelay(t *testing.T) {
	next := make(chan struct{})
	var got *http.Request
	var body string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-next:
		case <-time.After(5 * time.Second):
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer core.Close()

	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1", State: stateRunning, secret: "task-secret",
		Spec: TaskSpec{Model: "selfhosted/qwen3:8b", ModelEndpoint: &ModelEndpoint{Relay: true}}}
	other := &Task{ID: "t-2", State: stateRunning, secret: "other-secret", Spec: TaskSpec{Model: "openrouter/x"}}
	n.tasks[task.ID], n.tasks[other.ID] = task, other
	mux := http.NewServeMux()
	mux.Handle("/model/v1/{path...}", n.modelRelayHandler(&state{Core: core.URL, Name: "pc", Secret: "node-secret"}))
	listener := httptest.NewServer(mux)
	defer listener.Close()

	post := func(secret string) *http.Response {
		req, _ := http.NewRequest("POST", listener.URL+"/model/v1/chat/completions", strings.NewReader(`{"model":"qwen3:8b","stream":true}`))
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if out := readStream(t, post("task-secret"), next); !strings.Contains(out, "[DONE]") {
		t.Fatalf("stream %q", out)
	}
	if got.URL.Path != "/v1/node/tasks/t-1/model/chat/completions" || got.Header.Get("Authorization") != "Bearer node-secret" ||
		got.Header.Get("Eriphe-Node") != "pc" || !strings.Contains(body, "qwen3:8b") {
		t.Fatalf("core got %s %v %q", got.URL.Path, got.Header, body)
	}
	got = nil
	for secret, want := range map[string]int{"wrong": http.StatusUnauthorized, "other-secret": http.StatusForbidden} {
		resp := post(secret)
		resp.Body.Close()
		if resp.StatusCode != want || got != nil {
			t.Errorf("%s: %s", secret, resp.Status)
		}
	}
	task.State = stateStopped
	if resp := post("task-secret"); resp.StatusCode != http.StatusNotFound || got != nil {
		t.Errorf("finished task: %s", resp.Status)
	}
}

// OpenCode is pointed at its node's model relay, with the task's secret as
// the key, read from its environment.
func TestOpenCodeModelRelay(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1", secret: "task-secret", Spec: TaskSpec{Model: "selfhosted/qwen3:8b", ModelEndpoint: &ModelEndpoint{Relay: true}}}
	_, env := opencodeRuntime.command(n, task)
	var cfg struct {
		Provider map[string]struct {
			Options map[string]any `json:"options"`
		} `json:"provider"`
	}
	raw := env["OPENCODE_CONFIG_CONTENT"]
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	o := cfg.Provider["selfhosted"].Options
	if o["baseURL"] != "http://eriphe-node:8790/model/v1" || o["apiKey"] != "{env:"+modelTokenEnv+"}" || env[modelTokenEnv] != "task-secret" {
		t.Fatalf("options %v, env %v", o, env[modelTokenEnv])
	}
	if strings.Contains(raw, "task-secret") {
		t.Error("the secret is in the configuration")
	}
	// A task with an address of its own has no token.
	task = &Task{ID: "t-2", secret: "s", Spec: TaskSpec{Model: "selfhosted/qwen3:8b", ModelEndpoint: &ModelEndpoint{BaseURL: "http://192.0.2.10:11434/v1"}}}
	if _, env := opencodeRuntime.command(n, task); env[modelTokenEnv] != "" {
		t.Errorf("token for a direct address")
	}
}
