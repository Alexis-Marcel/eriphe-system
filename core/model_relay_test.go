package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// A provider names a self-hosted server's address or the node that lends
// it, not both.
func TestModelNodeConfig(t *testing.T) {
	for name, p := range map[string]Provider{
		"both":     {Kind: selfHostedKind, BaseURL: "http://192.0.2.10:11434/v1", Node: "gpu"},
		"bad node": {Kind: selfHostedKind, Node: "GPU box"},
		"other":    {Kind: "openrouter-api", Secret: "/x", Node: "gpu"},
	} {
		cfg := &Config{Providers: map[string]Provider{"p": p}}
		if err := cfg.checkAgents(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	cfg := &Config{Providers: map[string]Provider{"p": {Kind: selfHostedKind, Node: "gpu", Models: []string{"qwen3:8b"}}}}
	if err := cfg.checkAgents(); err != nil {
		t.Fatal(err)
	}
}

// fakeModel is an OpenAI-compatible server that streams a completion in
// two events, the second only once the test has read the first: a hop that
// buffered the answer would hang.
func fakeModel(t *testing.T, next <-chan struct{}) (*httptest.Server, *http.Request) {
	got := new(http.Request)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(context.Background())
		switch r.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:8b"},{"id":"llama3.1:8b"}]}`)
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

// attachModelNode connects a fake node that lends model (as eriphe-node
// -model-server does: its /v1/model/{path} is the server's /v1/{path}).
func attachModelNode(t *testing.T, c *core, name string, model *httptest.Server) *yamux.Session {
	t.Helper()
	target, _ := url.Parse(model.URL)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"model_server":true}`)) })
	api.Handle("/v1/model/{path...}", &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/v1/" + pr.In.PathValue("path")
		},
		FlushInterval: -1,
	})
	coreEnd, nodeEnd := net.Pipe()
	nodeSession, _ := yamux.Server(nodeEnd, tunnelConfig())
	go http.Serve(nodeSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach(name, coreSession, false)
	t.Cleanup(func() { coreSession.Close() })
	waitUntil(t, func() bool {
		nodes, _ := c.store.nodes()
		for _, n := range nodes {
			if n.Name == name && n.Online && parseInfo(n).ModelServer {
				return true
			}
		}
		return false
	})
	return coreSession
}

// readModelStream reads a streamed completion, letting the server go on
// only once its first event came through.
func readModelStream(t *testing.T, resp *http.Response, next chan<- struct{}) string {
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

// A task on one node uses the model another node lends: its node relays to
// eriphe-core, which goes on through the lending node's tunnel with the
// server's key; the answer streams; only that task's node, for a task whose
// agent uses that model, gets through.
func TestModelBehindNode(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "model-key")
	os.WriteFile(keyFile, []byte("sk-server\n"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers: map[string]Provider{
			"gpu":    {Kind: selfHostedKind, Node: "gpu", Secret: keyFile, Models: []string{"qwen3:8b"}},
			"remote": {Kind: "openrouter-api", Secret: keyFile},
		},
		Agents: map[string]Agent{
			"qwen":  {Runtime: "opencode", Provider: "gpu", Model: "selfhosted/qwen3:8b"},
			"cloud": {Runtime: "opencode", Provider: "remote", Model: "openrouter/x"},
		},
		DefaultAgent: "qwen",
	}
	if err := checkAll(cfg); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	store.registerNode("pc", hashSecret("pc-secret"))
	store.setNodeStatus("pc", true, []byte(`{"runtimes":["opencode"]}`))
	store.registerNode("gpu", hashSecret("gpu-secret"))

	// The task, on pc: its spec says to go through its node, and lends no key.
	task, err := c.runTask(context.Background(), runRequest{Prompt: "summarize", Node: "pc"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/v1/node/assignments?rev=0&wait=0", nil)
	req.Header.Set("Authorization", "Bearer pc-secret")
	req.Header.Set("Eriphe-Node", "pc")
	rec := httptest.NewRecorder()
	c.handleAssignments(rec, req)
	var a struct {
		Tasks []nodeAssignment `json:"tasks"`
	}
	json.NewDecoder(rec.Body).Decode(&a)
	if len(a.Tasks) != 1 {
		t.Fatalf("assignments: %s", rec.Body)
	}
	spec := a.Tasks[0].Spec
	if spec.ModelEndpoint == nil || !spec.ModelEndpoint.Relay || spec.ModelEndpoint.BaseURL != "" || len(spec.Identity) != 0 ||
		spec.Egress == nil || len(spec.Egress.Endpoints) != 0 {
		t.Fatalf("spec %+v, endpoint %+v, egress %+v", spec, spec.ModelEndpoint, spec.Egress)
	}
	if strings.Contains(rec.Body.String(), "sk-server") {
		t.Fatal("the server's key went to the task's node")
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/node/tasks/{id}/model/{path...}", c.taskModelHandler())
	api := httptest.NewServer(mux)
	defer api.Close()
	call := func(node, secret, method, path string) *http.Response {
		req, _ := http.NewRequest(method, api.URL+"/v1/node/tasks/"+task.ID+"/model/"+path, strings.NewReader(`{"model":"qwen3:8b","stream":true}`))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Eriphe-Node", node)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	status := func(resp *http.Response) int { resp.Body.Close(); return resp.StatusCode }

	// The lending machine is away.
	if s := status(call("pc", "pc-secret", "POST", "chat/completions")); s != http.StatusBadGateway {
		t.Errorf("offline: %d", s)
	}
	next := make(chan struct{})
	model, got := fakeModel(t, next)
	attachModelNode(t, c, "gpu", model)

	body := readModelStream(t, call("pc", "pc-secret", "POST", "chat/completions"), next)
	if !strings.Contains(body, `"lo"`) || !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream %q", body)
	}
	if got.URL.Path != "/v1/chat/completions" || got.Header.Get("Authorization") != "Bearer sk-server" || got.Header.Get("Eriphe-Node") != "" {
		t.Fatalf("model server got %s %v", got.URL.Path, got.Header)
	}
	resp := call("pc", "pc-secret", "GET", "models")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "qwen3:8b") {
		t.Fatalf("models: %s %s", resp.Status, raw)
	}

	for name, tc := range map[string]struct {
		node, secret, method, path string
		want                       int
	}{
		"wrong secret":   {"pc", "nope", "POST", "chat/completions", http.StatusUnauthorized},
		"another node":   {"gpu", "gpu-secret", "POST", "chat/completions", http.StatusNotFound},
		"another method": {"pc", "pc-secret", "DELETE", "models", http.StatusMethodNotAllowed},
		"out of /v1":     {"pc", "pc-secret", "GET", "..%2F..%2Fapi%2Ftags", http.StatusBadRequest},
	} {
		if s := status(call(tc.node, tc.secret, tc.method, tc.path)); s != tc.want {
			t.Errorf("%s: %d, want %d", name, s, tc.want)
		}
	}

	// An agent that uses another model has no way to this one.
	store.db.Exec(`UPDATE tasks SET agent = 'cloud' WHERE id = ?`, task.ID)
	if s := status(call("pc", "pc-secret", "POST", "chat/completions")); s != http.StatusForbidden {
		t.Errorf("another agent: %d", s)
	}
	// Nor a finished task.
	store.db.Exec(`UPDATE tasks SET agent = 'qwen' WHERE id = ?`, task.ID)
	store.updateTask(task.ID, map[string]any{"state": stateStopped})
	if s := status(call("pc", "pc-secret", "POST", "chat/completions")); s != http.StatusNotFound {
		t.Errorf("finished: %d", s)
	}
}

// The provider form offers the devices that lend a model, tests the model
// through the device's tunnel, and saves the device in place of an address.
func TestWebModelNode(t *testing.T) {
	tw, c, csrf := catalogWeb(t, noAgents)
	_, body, _ := tw.do("GET", "/settings/providers/new?kind=openai-compatible", nil, nil)
	if !strings.Contains(body, "Sur un de mes appareils") || !strings.Contains(body, "Aucun appareil ne prête") {
		t.Errorf("form without a device: %s", body)
	}

	c.store.registerNode("gpu", hashSecret("x"))
	next := make(chan struct{})
	model, _ := fakeModel(t, next)
	attachModelNode(t, c.core, "gpu", model)
	_, body, _ = tw.do("GET", "/settings/providers/new?kind=openai-compatible", nil, nil)
	if !strings.Contains(body, `<option value="gpu"`) || !strings.Contains(body, `name="where" value="node" checked`) ||
		!strings.Contains(body, `hx-post="/settings/provider-models" hx-trigger="load"`) {
		t.Errorf("form with a device: %s", body)
	}
	// The device chosen, its models are listed at once.
	if _, b, _ := tw.post("/settings/provider-models", url.Values{"kind": {selfHostedKind}, "where": {"node"}, "node": {"gpu"}}, csrf); !strings.Contains(b, `name="model" value="qwen3:8b" checked`) {
		t.Errorf("models through the device: %s", b)
	}
	form := url.Values{"kind": {selfHostedKind}, "where": {"node"}, "node": {"gpu"}}
	if _, b, _ := tw.post("/settings/provider-test", form, csrf); !strings.Contains(b, "2 modèle(s)") || !strings.Contains(b, "<code>qwen3:8b</code>") {
		t.Errorf("test through the device: %s", b)
	}
	if _, b, _ := tw.post("/settings/provider-test", url.Values{"kind": {selfHostedKind}, "where": {"node"}, "node": {"pc"}}, csrf); !strings.Contains(b, "pc n&#39;est pas connecté") {
		t.Errorf("test through an absent device: %s", b)
	}
	for _, node := range []string{"pc", ""} {
		if _, b, _ := tw.post("/settings/providers", url.Values{"kind": {selfHostedKind}, "where": {"node"}, "node": {node}, "models": {"qwen3:8b"}}, csrf); !strings.Contains(b, "Choisis un appareil") {
			t.Errorf("device %q saved: %s", node, b)
		}
	}
	form.Set("models", "qwen3:8b")
	form.Set("base_url", "http://192.0.2.10:11434/v1") // hidden, left by the other choice
	if _, b, h := tw.post("/settings/providers", form, csrf); h.Get("HX-Redirect") != "/settings/agents?created=selfhosted" {
		t.Fatalf("refused: %s", b)
	}
	m := c.conf()
	if p := m.Providers["selfhosted"]; p.Node != "gpu" || p.BaseURL != "" || p.Secret != "" {
		t.Fatalf("provider %+v", p)
	}
	if a := m.Agents["qwen3-8b"]; a.Model != "selfhosted/qwen3:8b" || a.Provider != "selfhosted" {
		t.Fatalf("agents %+v", m.Agents)
	}
	// Its edit form keeps the device chosen, and the stored device is tested.
	_, body, _ = tw.do("GET", "/settings/providers/selfhosted/edit", nil, nil)
	if !strings.Contains(body, `name="where" value="node" checked`) || !strings.Contains(body, `<option value="gpu" selected`) {
		t.Errorf("edit form: %s", body)
	}
	if _, b, _ := tw.post("/settings/provider-test/selfhosted", url.Values{}, csrf); !strings.Contains(b, "2 modèle(s)") {
		t.Errorf("stored device: %s", b)
	}
}
