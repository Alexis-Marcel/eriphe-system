package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCodexLive runs the real Codex of a worker image, hardened as every
// task, against a fake OpenAI Responses API on this machine (no ChatGPT
// plan needed): the model runs a command (allowed), then another (denied
// by the test's policy), then answers; a restart resumes the thread. It
// checks that "untrusted" asks before every command under Codex's
// "danger-full-access", and that the lent auth.json stays off the disk.
//
//	ERIPHE_LIVE_IMAGE=eriphe-base:dev ERIPHE_LIVE_AGENT_IMAGE=localhost:5001/eriphe-agent-{agent}:dev go test -run CodexLive -v
//
// ERIPHE_LIVE_HOST is how a container reaches this machine, if not as
// host.docker.internal (OrbStack: host.orb.internal).
func TestCodexLive(t *testing.T) {
	image := os.Getenv("ERIPHE_LIVE_IMAGE")
	if image == "" {
		t.Skip("ERIPHE_LIVE_IMAGE not set")
	}
	api := newFakeResponses(t)
	codexExtra = []string{
		`model_provider="fake"`,
		`model_providers.fake.name="fake"`,
		fmt.Sprintf(`model_providers.fake.base_url="http://%s:%d/v1"`, envOr("ERIPHE_LIVE_HOST", "host.docker.internal"), api.port),
		`model_providers.fake.wire_api="responses"`,
		`model_providers.fake.requires_openai_auth=false`,
		`model_providers.fake.supports_websockets=false`,
	}
	t.Cleanup(func() { codexExtra = nil })

	root, _ := filepath.EvalSymlinks(t.TempDir())
	n := newNode(options{root: root, hostRoot: root, docker: "docker", agentImage: os.Getenv("ERIPHE_LIVE_AGENT_IMAGE")})
	dir := filepath.Join(root, "tasks", "live")
	claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	jwt := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	auth := fmt.Sprintf(`{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"id_token":%q,"access_token":%q,"refresh_token":"","account_id":"a"},"last_refresh":%q}`,
		jwt, jwt, time.Now().UTC().Format(time.RFC3339))
	spec := TaskSpec{Runtime: "codex", Image: image, Model: "gpt-fake", Prompt: "Write a.txt, then b.txt.",
		Identity: map[string]string{"ERIPHE_CODEX_AUTH": auth}}
	start := func(first, session string) *Task {
		task := &Task{ID: "live", Spec: spec, State: stateRunning, dir: dir, SessionID: session, perms: map[string]*permission{}}
		if err := n.startContainer(task, first); err != nil {
			t.Fatal(err)
		}
		go func() { // the policy: a.txt yes, anything else no
			for !finished(task.state()) {
				task.mu.Lock()
				for _, p := range task.perms {
					select {
					case p.answer <- permAnswer{Allow: strings.Contains(subjectOf(p.Input), "a.txt"), Message: "only a.txt"}:
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
			if finished(task.state()) || i > 1200 {
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
	var asked []string
	for _, e := range eventsOf(task, "permission") {
		in, _ := e.Data["input"].(map[string]any)
		asked = append(asked, e.Data["tool_name"].(string)+" "+subjectOf(in))
	}
	if len(asked) != 2 || !strings.Contains(asked[0], "a.txt") || !strings.Contains(asked[1], "b.txt") {
		t.Errorf("permissions = %v", asked)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "a.txt")); err != nil {
		t.Errorf("a.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "b.txt")); err == nil {
		t.Error("b.txt written despite the denial")
	}
	if end := eventsOf(task, "turn_end")[0].Data; end["subtype"] != "success" || end["result"] != "Done." {
		t.Errorf("turn_end = %v", end)
	}
	// The credential is a link to the container's /tmp, gone with it.
	if target, err := os.Readlink(filepath.Join(dir, "agent", "codex", "auth.json")); err != nil || !strings.HasPrefix(target, "/tmp/") {
		t.Errorf("auth.json on disk: %q %v", target, err)
	}
	session := task.SessionID
	task.run.stop()
	for !finished(task.state()) {
		time.Sleep(100 * time.Millisecond)
	}

	task = start("And now?", session)
	idle(task, 1)
	if task.SessionID != session {
		t.Errorf("session %s, was %s", task.SessionID, session)
	}
	if !api.sawHistory() {
		t.Error("the resumed thread did not send its history")
	}
	task.run.stop()
	for !finished(task.state()) {
		time.Sleep(100 * time.Millisecond)
	}
}

// fakeResponses is OpenAI's Responses API as Codex streams it: by the
// number of command outputs already in the request, run a.txt, run b.txt,
// then answer.
type fakeResponses struct {
	port    int
	mu      sync.Mutex
	history bool
}

func newFakeResponses(t *testing.T) *fakeResponses {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeResponses{port: l.Addr().(*net.TCPAddr).Port}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return f
}

func (f *fakeResponses) sawHistory() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.history
}

func (f *fakeResponses) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/responses") {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Input []map[string]any `json:"input"`
	}
	json.Unmarshal(raw, &req)
	outputs, users := 0, 0
	for _, it := range req.Input {
		switch it["type"] {
		case "function_call_output":
			outputs++
		case "message":
			if it["role"] == "user" {
				users++
			}
		}
	}
	f.mu.Lock()
	if strings.Contains(string(raw), "And now?") && strings.Contains(string(raw), "Write a.txt") {
		f.history = true
	}
	f.mu.Unlock()
	var item map[string]any
	call := func(id, cmd string) map[string]any {
		args, _ := json.Marshal(map[string]any{"cmd": cmd, "yield_time_ms": 2000})
		return map[string]any{"type": "function_call", "call_id": id, "name": "exec_command", "arguments": string(args)}
	}
	switch {
	case strings.Contains(string(raw), "And now?"):
		item = map[string]any{"type": "message", "role": "assistant", "id": "m2", "content": []any{map[string]any{"type": "output_text", "text": "Still here."}}}
	case outputs == 0:
		item = call("c1", "echo hi > a.txt")
	case outputs == 1:
		item = call("c2", "echo no > b.txt")
	default:
		item = map[string]any{"type": "message", "role": "assistant", "id": "m1", "content": []any{map[string]any{"type": "output_text", "text": "Done."}}}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, ev := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "r1"}},
		{"type": "response.output_item.done", "item": item},
		{"type": "response.completed", "response": map[string]any{"id": "r1", "usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}}},
	} {
		raw, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], raw)
	}
}
