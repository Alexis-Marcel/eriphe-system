package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOpenCodeLive runs the real OpenCode, injected into a worker image
// and hardened as every task, on one of OpenCode's free models: two turns
// with an allowed command and a denied write, then a restart that loads
// the session.
//
//	ERIPHE_LIVE_IMAGE=eriphe-base:dev ERIPHE_LIVE_AGENT_IMAGE=localhost:5001/eriphe-agent-{agent}:dev go test -run OpenCodeLive -v
func TestOpenCodeLive(t *testing.T) {
	image := os.Getenv("ERIPHE_LIVE_IMAGE")
	if image == "" {
		t.Skip("ERIPHE_LIVE_IMAGE not set")
	}
	model := envOr("ERIPHE_LIVE_MODEL", "opencode/big-pickle")
	root, _ := filepath.EvalSymlinks(t.TempDir())
	n := newNode(options{root: root, hostRoot: root, docker: "docker", cacheVolume: os.Getenv("ERIPHE_LIVE_CACHE"),
		agentImage: os.Getenv("ERIPHE_LIVE_AGENT_IMAGE")})
	dir := filepath.Join(root, "tasks", "live")
	spec := TaskSpec{Runtime: "opencode", Image: image, Model: model,
		Prompt: "Use the bash tool to run exactly `echo hi > a.txt`. Then use the write tool to create b.txt containing x. " +
			"If a tool call is refused, do not retry it. Then reply with one short sentence."}
	start := func(first string, session string) *Task {
		task := &Task{ID: "live", Spec: spec, State: stateRunning, dir: dir, SessionID: session, perms: map[string]*permission{}}
		if err := n.startContainer(task, first); err != nil {
			t.Fatal(err)
		}
		go func() { // the policy: commands yes, writes no
			for !finished(task.state()) {
				task.mu.Lock()
				for _, p := range task.perms {
					select {
					case p.answer <- permAnswer{Allow: p.ToolName == "Bash", Message: "writing b.txt is not allowed"}:
					default:
					}
				}
				task.mu.Unlock()
				time.Sleep(50 * time.Millisecond)
			}
		}()
		return task
	}
	idle := func(task *Task, turns int) {
		t.Helper()
		for i := 0; len(eventsOf(task, "turn_end")) < turns || task.state() != stateIdle; i++ {
			if finished(task.state()) || i > 3000 {
				t.Fatalf("state %s, events %v", task.state(), task.events)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	task := start(spec.Prompt, "")
	idle(task, 1)
	for _, e := range task.events {
		t.Logf("%s %v", e.Type, e.Data)
	}
	var tools []string
	for _, e := range eventsOf(task, "permission") {
		in, _ := e.Data["input"].(map[string]any)
		tools = append(tools, e.Data["tool_name"].(string)+" "+subjectOf(in))
	}
	if !strings.Contains(strings.Join(tools, ","), "Bash echo hi > a.txt") || !strings.Contains(strings.Join(tools, ","), "Write /work/b.txt") {
		t.Errorf("permissions = %v", tools)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "a.txt")); err != nil {
		t.Errorf("a.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "b.txt")); err == nil {
		t.Error("b.txt written despite the denial")
	}
	session := task.SessionID
	task.run.stop()
	for !finished(task.state()) {
		time.Sleep(100 * time.Millisecond)
	}

	// After a restart, the same session, and the denial's reason with it.
	task = start("Which file were you not allowed to write, and why? One sentence.", session)
	idle(task, 1)
	for _, e := range task.events {
		t.Logf("%s %v", e.Type, e.Data)
	}
	end := eventsOf(task, "turn_end")[0].Data
	if task.SessionID != session || !strings.Contains(end["result"].(string), "b.txt") {
		t.Errorf("session %s (was %s), answer %q", task.SessionID, session, end["result"])
	}
	task.run.stop()
	for !finished(task.state()) {
		time.Sleep(100 * time.Millisecond)
	}
}

// A self-hosted model server: OpenCode's openai-compatible provider, at its
// address, with its key read from the task's environment, never written in
// the configuration.
func TestOpenCodeSelfHosted(t *testing.T) {
	type provider struct {
		NPM     string         `json:"npm"`
		Options map[string]any `json:"options"`
		Models  map[string]struct {
			Name     string `json:"name"`
			ToolCall bool   `json:"tool_call"`
		} `json:"models"`
	}
	config := func(spec TaskSpec) (map[string]provider, []string, string) {
		t.Helper()
		env := opencodeSetup(&Task{Spec: spec})
		var cfg struct {
			Model    string              `json:"model"`
			Provider map[string]provider `json:"provider"`
			Disabled []string            `json:"disabled_providers"`
		}
		if err := json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg.Provider, cfg.Disabled, env["OPENCODE_CONFIG_CONTENT"]
	}
	spec := TaskSpec{Model: "selfhosted/qwen3:8b", Identity: map[string]string{"SELFHOSTED_API_KEY": "k-123"},
		ModelEndpoint: &ModelEndpoint{BaseURL: "http://192.0.2.10:11434/v1", KeyEnv: "SELFHOSTED_API_KEY"}}
	prov, disabled, raw := config(spec)
	p, ok := prov["selfhosted"]
	if !ok || p.NPM != "@ai-sdk/openai-compatible" || p.Options["baseURL"] != "http://192.0.2.10:11434/v1" ||
		p.Options["apiKey"] != "{env:SELFHOSTED_API_KEY}" || !p.Models["qwen3:8b"].ToolCall || len(p.Models) != 1 {
		t.Fatalf("provider %+v", prov)
	}
	if len(disabled) != 1 || disabled[0] != "opencode" {
		t.Errorf("disabled %v", disabled)
	}
	// Minutes to read a long prompt on a home machine: no 5-minute cut.
	if p.Options["timeout"] != false || p.Options["headerTimeout"] != float64(1800000) || p.Options["chunkTimeout"] != float64(600000) {
		t.Errorf("timeouts %v", p.Options)
	}
	if strings.Contains(raw, "k-123") {
		t.Error("the key is in the configuration")
	}
	// Its model is in its configuration: no catalog fetched from models.dev.
	if env := opencodeSetup(&Task{Spec: spec}); env["OPENCODE_DISABLE_MODELS_FETCH"] != "1" {
		t.Error("the models catalog is still fetched")
	}
	if env := opencodeSetup(&Task{Spec: TaskSpec{Model: "openai/gpt-5"}}); env["OPENCODE_DISABLE_MODELS_FETCH"] != "" {
		t.Error("a cloud model loses its catalog")
	}
	// No key: no apiKey.
	spec.Identity, spec.ModelEndpoint.KeyEnv = nil, ""
	if prov, _, _ := config(spec); prov["selfhosted"].Options["apiKey"] != nil {
		t.Errorf("apiKey without a key: %+v", prov)
	}
	// A key OpenCode could not read as one stays out.
	spec.Identity, spec.ModelEndpoint.KeyEnv = map[string]string{"SELFHOSTED_API_KEY": `a"b`}, "SELFHOSTED_API_KEY"
	if prov, _, _ := config(spec); prov["selfhosted"].Options["apiKey"] != nil {
		t.Errorf("a key with a quote: %+v", prov)
	}
	// Another provider's model, an address OpenCode would expand, a model
	// with a brace: no block.
	for _, s := range []TaskSpec{
		{Model: "openrouter/x", ModelEndpoint: &ModelEndpoint{BaseURL: "http://192.0.2.10:11434/v1"}},
		{Model: "selfhosted/x", ModelEndpoint: &ModelEndpoint{BaseURL: "http://192.0.2.10/{file:/etc/passwd}"}},
		{Model: "selfhosted/{env:HOME}", ModelEndpoint: &ModelEndpoint{BaseURL: "http://192.0.2.10:11434/v1"}},
		{Model: "selfhosted/x", ModelEndpoint: &ModelEndpoint{BaseURL: "file:///etc/passwd"}},
		{Model: "selfhosted/x"},
	} {
		if prov, _, _ := config(s); prov != nil {
			t.Errorf("%+v: provider %+v", s, prov)
		}
	}
}
