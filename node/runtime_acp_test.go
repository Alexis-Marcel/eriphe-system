package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
)

// fakeAgent speaks ACP like OpenCode on the other end of two pipes. Its
// prompts stream a message in chunks, run a command after asking for it,
// then end the turn; "wait" blocks until session/cancel.
type fakeAgent struct {
	t           *testing.T
	loadSession bool
	model       string // the model it says it runs
	in          *bufio.Scanner
	out         io.Writer

	wmu      sync.Mutex
	mu       sync.Mutex
	nextID   int
	replies  map[int]chan rpcMessage
	cancel   chan struct{}
	requests []rpcMessage // what the client asked, in order
	prompts  []string
	outcomes []map[string]any                 // answers to request_permission
	unknown  *rpcError                        // answer to a request the client does not offer
	mcpHTTP  bool                             // it takes MCP servers over HTTP
	script   func(f *fakeAgent, m rpcMessage) // a turn of its own, instead of the usual one
}

func startFake(t *testing.T, task *Task, first string, loadSession bool, opts ...func(*fakeAgent)) (*acpSession, *fakeAgent, chan error) {
	toAgent, clientOut := io.Pipe()
	agentOut, fromAgent := io.Pipe()
	f := &fakeAgent{t: t, loadSession: loadSession, in: bufio.NewScanner(toAgent), out: fromAgent,
		replies: map[int]chan rpcMessage{}, cancel: make(chan struct{})}
	for _, o := range opts {
		o(f)
	}
	go f.serve()
	s := acpRuntime{}.attach(task, clientOut).(*acpSession)
	done := make(chan error, 1)
	go func() {
		done <- s.run(first, agentOut)
	}()
	return s, f, done
}

func (f *fakeAgent) write(m map[string]any) {
	m["jsonrpc"] = "2.0"
	raw, _ := json.Marshal(m)
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.Write(append(raw, '\n'))
}

func (f *fakeAgent) update(u map[string]any) {
	f.write(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": "s1", "update": u}})
}

// ask sends a request to the client and waits for its answer. Ids start at
// 0, as OpenCode's do.
func (f *fakeAgent) ask(method string, params any) rpcMessage {
	ch := make(chan rpcMessage, 1)
	f.mu.Lock()
	id := f.nextID
	f.nextID++
	f.replies[id] = ch
	f.mu.Unlock()
	f.write(map[string]any{"id": id, "method": method, "params": params})
	return <-ch
}

func (f *fakeAgent) serve() {
	defer f.out.(io.Closer).Close()
	for f.in.Scan() {
		var m rpcMessage
		if err := json.Unmarshal(f.in.Bytes(), &m); err != nil {
			f.t.Errorf("client sent %q: %v", f.in.Text(), err)
			continue
		}
		if m.Method == "" {
			var id int
			json.Unmarshal(m.ID, &id)
			f.mu.Lock()
			ch := f.replies[id]
			f.mu.Unlock()
			ch <- m
			continue
		}
		f.mu.Lock()
		f.requests = append(f.requests, m)
		f.mu.Unlock()
		switch m.Method {
		case "initialize":
			f.write(map[string]any{"id": m.ID, "result": map[string]any{"protocolVersion": 1,
				"agentCapabilities": map[string]any{"loadSession": f.loadSession,
					"mcpCapabilities": map[string]any{"http": f.mcpHTTP, "sse": false}}}})
		case "session/new":
			f.write(map[string]any{"id": m.ID, "result": map[string]any{"sessionId": "s1",
				"configOptions": []map[string]any{{"id": "model", "category": "model", "currentValue": f.model}}}})
		case "session/load":
			// The conversation so far is replayed before the answer.
			f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "old news"}})
			f.write(map[string]any{"id": m.ID, "result": map[string]any{}})
		case "session/prompt":
			go f.prompt(m)
		case "session/cancel":
			close(f.cancel)
		}
	}
}

func (f *fakeAgent) prompt(m rpcMessage) {
	var p struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	json.Unmarshal(m.Params, &p)
	text := p.Prompt[0].Text
	f.mu.Lock()
	f.prompts = append(f.prompts, text)
	f.mu.Unlock()
	if f.script != nil {
		f.script(f, m)
		return
	}
	if strings.HasSuffix(text, "wait") {
		<-f.cancel
		f.write(map[string]any{"id": m.ID, "result": map[string]any{"stopReason": "cancelled"}})
		return
	}
	for _, c := range []string{"Hel", "lo"} {
		f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": c}})
	}
	f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "bash", "kind": "execute",
		"status": "pending", "rawInput": map[string]any{}})
	f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "title": "ls -la", "kind": "execute",
		"status": "in_progress", "rawInput": map[string]any{"command": "ls -la"}})
	r := f.ask("session/request_permission", map[string]any{"sessionId": "s1",
		"toolCall": map[string]any{"toolCallId": "c1", "title": "ls -la", "kind": "execute", "rawInput": map[string]any{"command": "ls -la"}},
		"options": []map[string]any{{"optionId": "once", "kind": "allow_once"}, {"optionId": "always", "kind": "allow_always"},
			{"optionId": "reject", "kind": "reject_once"}}})
	var res struct {
		Outcome map[string]any `json:"outcome"`
	}
	json.Unmarshal(r.Result, &res)
	u := f.ask("fs/read_text_file", map[string]any{"sessionId": "s1", "path": "/work/x"})
	f.mu.Lock()
	f.outcomes = append(f.outcomes, res.Outcome)
	f.unknown = u.Error
	f.mu.Unlock()
	f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m2", "content": map[string]any{"type": "text", "text": "Done"}})
	f.write(map[string]any{"id": m.ID, "result": map[string]any{"stopReason": "end_turn"}})
}

func newTestTask() *Task {
	return &Task{ID: "t1", State: stateRunning, perms: map[string]*permission{}, Spec: TaskSpec{Prompt: "go"}}
}

// answer waits for the task's permission request and answers it as the
// inbox would.
func answer(t *testing.T, task *Task, a permAnswer) *permission {
	var p *permission
	waitFor(t, func() bool {
		task.mu.Lock()
		defer task.mu.Unlock()
		for _, x := range task.perms {
			p = x
		}
		return p != nil
	})
	p.answer <- a
	return p
}

func eventsOf(task *Task, typ string) []Event {
	task.mu.Lock()
	defer task.mu.Unlock()
	var out []Event
	for _, e := range task.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func waitTurns(t *testing.T, task *Task, n int) {
	t.Helper()
	waitFor(t, func() bool { return len(eventsOf(task, "turn_end")) == n && task.state() == stateIdle })
}

func TestACPTurns(t *testing.T) {
	task := newTestTask()
	s, f, done := startFake(t, task, "go", false)

	p := answer(t, task, permAnswer{Allow: true})
	if p.ToolName != "Bash" || p.Input["command"] != "ls -la" {
		t.Errorf("permission = %s %v", p.ToolName, p.Input)
	}
	waitTurns(t, task, 1)
	if task.SessionID != "s1" {
		t.Errorf("session = %q", task.SessionID)
	}
	texts := eventsOf(task, "text")
	if len(texts) != 2 || texts[0].Data["text"] != "Hello" || texts[1].Data["text"] != "Done" {
		t.Errorf("texts = %v", texts)
	}
	if tools := eventsOf(task, "tool_use"); len(tools) != 1 || tools[0].Data["name"] != "Bash" {
		t.Errorf("tool_use = %v", tools)
	}
	end := eventsOf(task, "turn_end")[0].Data
	if end["subtype"] != "success" || end["result"] != "Done" {
		t.Errorf("turn_end = %v", end)
	}
	if f.outcomes[0]["optionId"] != "once" || f.unknown == nil || f.unknown.Code != -32601 {
		t.Errorf("outcome %v, unknown method answered %v", f.outcomes[0], f.unknown)
	}

	// A second turn, denied: the agent hears why with the next one.
	if err := s.send("again"); err != nil {
		t.Fatal(err)
	}
	if err := s.send("too soon"); err != errNotIdle {
		t.Errorf("send during a turn: %v", err)
	}
	answer(t, task, permAnswer{Allow: false, Message: "no listing"})
	waitTurns(t, task, 2)
	if f.outcomes[1]["optionId"] != "reject" {
		t.Errorf("deny outcome = %v", f.outcomes[1])
	}
	if a := eventsOf(task, "permission_answer"); len(a) != 2 || a[1].Data["allow"] != false {
		t.Errorf("answers = %v", a)
	}
	if err := s.send("third"); err != nil {
		t.Fatal(err)
	}
	answer(t, task, permAnswer{Allow: true})
	waitTurns(t, task, 3)
	if !strings.Contains(f.prompts[2], "Bash ls -la: no listing") || !strings.HasSuffix(f.prompts[2], "third") {
		t.Errorf("third prompt = %q", f.prompts[2])
	}

	s.stop()
	if err := <-done; err != nil {
		t.Errorf("run: %v", err)
	}
}

func TestACPResume(t *testing.T) {
	task := newTestTask()
	task.SessionID = "s1"
	s, f, done := startFake(t, task, "carry on", true)
	answer(t, task, permAnswer{Allow: true})
	waitTurns(t, task, 1)
	f.mu.Lock()
	load := f.requests[1]
	f.mu.Unlock()
	var params map[string]any
	json.Unmarshal(load.Params, &params)
	if load.Method != "session/load" || params["sessionId"] != "s1" || params["cwd"] != "/work" {
		t.Errorf("second request = %s %v", load.Method, params)
	}
	if f.prompts[0] != "carry on" {
		t.Errorf("first prompt = %q", f.prompts[0])
	}
	for _, e := range eventsOf(task, "text") {
		if e.Data["text"] == "old news" {
			t.Error("the replayed conversation was reported")
		}
	}
	s.stop()
	<-done
}

// An agent that cannot load a session starts a new one from the prompt.
func TestACPResumeUnsupported(t *testing.T) {
	task := newTestTask()
	task.SessionID = "gone"
	task.Spec.Prompt = "the task"
	s, f, done := startFake(t, task, "carry on", false)
	answer(t, task, permAnswer{Allow: true})
	waitTurns(t, task, 1)
	if task.SessionID != "s1" || len(eventsOf(task, "error")) != 1 {
		t.Errorf("session %q, errors %v", task.SessionID, eventsOf(task, "error"))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requests[1].Method != "session/new" || f.prompts[0] != "the task\n\ncarry on" {
		t.Errorf("second request %s, prompt %q", f.requests[1].Method, f.prompts[0])
	}
	s.stop()
	<-done
}

func TestACPStopCancels(t *testing.T) {
	task := newTestTask()
	s, f, done := startFake(t, task, "wait", false)
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.prompts) == 1
	})
	s.stop()
	<-done
	select {
	case <-f.cancel:
	default:
		t.Error("no session/cancel")
	}
}

// A session on another model than the task's fails the task.
func TestACPWrongModel(t *testing.T) {
	task := newTestTask()
	task.Spec.Model = "openrouter/some-model"
	toAgent, clientOut := io.Pipe()
	agentOut, fromAgent := io.Pipe()
	f := &fakeAgent{t: t, model: "opencode/free-model", in: bufio.NewScanner(toAgent), out: fromAgent,
		replies: map[int]chan rpcMessage{}, cancel: make(chan struct{})}
	go f.serve()
	err := acpRuntime{}.attach(task, clientOut).run("go", agentOut)
	if err == nil || !strings.Contains(err.Error(), "opencode/free-model") {
		t.Errorf("run = %v", err)
	}
}

func TestOpenCodeConfig(t *testing.T) {
	env := opencodeSetup(&Task{Spec: TaskSpec{Model: "openrouter/some-model", Repo: "o/r"}})
	var cfg struct {
		Model      string            `json:"model"`
		Permission map[string]string `json:"permission"`
		Disabled   []string          `json:"disabled_providers"`
		Rules      []string          `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"edit", "bash", "webfetch"} {
		if cfg.Permission[p] != "ask" {
			t.Errorf("permission %s = %q", p, cfg.Permission[p])
		}
	}
	if cfg.Permission["websearch"] != "deny" {
		t.Errorf("websearch = %q, want deny", cfg.Permission["websearch"])
	}
	if cfg.Model != "openrouter/some-model" || len(cfg.Disabled) != 1 || cfg.Rules[0] != "/work/repo/AGENTS.md" ||
		env["OPENCODE_DISABLE_PROJECT_CONFIG"] != "1" || env["XDG_DATA_HOME"] != "/profile/data" {
		t.Errorf("config %+v, env %v", cfg, env)
	}
}

// OpenCode's tool calls, as eriphe-core's policy reads them.
func TestACPToolMapping(t *testing.T) {
	loc := func(p string) []struct {
		Path string `json:"path"`
	} {
		return []struct {
			Path string `json:"path"`
		}{{p}}
	}
	for _, c := range []struct {
		title, kind, key, want string
		input                  map[string]any
		tc                     acpToolCall
		name                   string
	}{
		{title: "write", kind: "edit", name: "Write", key: "file_path", want: "/work/b.txt", input: map[string]any{"filePath": "/work/b.txt"}},
		{title: "/work/b.txt", kind: "edit", name: "Edit", key: "file_path", want: "/work/b.txt", input: map[string]any{"filepath": "/work/b.txt"}},
		{title: "edit", kind: "edit", name: "Edit", key: "file_path", want: "/work/a.go", tc: acpToolCall{Locations: loc("/work/a.go")}},
		{title: "https://example.com", kind: "fetch", name: "WebFetch", key: "url", want: "https://example.com", input: map[string]any{"url": "https://example.com"}},
		{title: "go test ./...", kind: "execute", name: "Bash", key: "command", want: "go test ./..."},
		{title: "read", kind: "read", name: "Read", key: "file_path", want: "/work/x", input: map[string]any{"filePath": "/work/x"}},
		{title: "external_directory", kind: "other", name: "external_directory", key: "path", want: "/etc", tc: acpToolCall{Locations: loc("/etc")}},
	} {
		tc := c.tc
		tc.Title, tc.Kind, tc.RawInput = c.title, c.kind, c.input
		name := acpToolName(tc.Title, tc.Kind)
		in := acpInput(name, tc)
		if name != c.name || in[c.key] != c.want {
			t.Errorf("%s/%s: %s %v", c.title, c.kind, name, in)
		}
	}
}
