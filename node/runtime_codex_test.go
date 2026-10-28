package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
)

// fakeCodex speaks Codex's app-server protocol on the other end of two
// pipes. A turn streams a message, asks to run a command and to change two
// files, then completes; "wait" blocks until turn/interrupt.
type fakeCodex struct {
	t     *testing.T
	model string // the model it says it runs, the asked one if empty
	in    *bufio.Scanner
	out   io.Writer

	wmu       sync.Mutex
	mu        sync.Mutex
	nextID    int
	replies   map[int]chan codexMessage
	interrupt chan struct{}
	requests  []codexMessage
	decisions []string
	unknown   *rpcError
	resumable bool
	script    func(f *fakeCodex, p map[string]any) // a turn of its own, instead of the usual one
}

func startCodex(t *testing.T, task *Task, first string, resumable bool, opts ...func(*fakeCodex)) (*codexSession, *fakeCodex, chan error) {
	toAgent, clientOut := io.Pipe()
	agentOut, fromAgent := io.Pipe()
	f := &fakeCodex{t: t, in: bufio.NewScanner(toAgent), out: fromAgent, replies: map[int]chan codexMessage{},
		interrupt: make(chan struct{}), resumable: resumable}
	for _, o := range opts {
		o(f)
	}
	go f.serve()
	s := codexRuntime{}.attach(task, clientOut).(*codexSession)
	done := make(chan error, 1)
	go func() { done <- s.run(first, agentOut) }()
	return s, f, done
}

func (f *fakeCodex) write(m map[string]any) {
	raw, _ := json.Marshal(m)
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.Write(append(raw, '\n'))
}

func (f *fakeCodex) ask(method string, params any) codexMessage {
	ch := make(chan codexMessage, 1)
	f.mu.Lock()
	id := f.nextID
	f.nextID++
	f.replies[id] = ch
	f.mu.Unlock()
	f.write(map[string]any{"id": id, "method": method, "params": params})
	return <-ch
}

func (f *fakeCodex) serve() {
	defer f.out.(io.Closer).Close()
	for f.in.Scan() {
		if strings.Contains(f.in.Text(), `"jsonrpc"`) {
			f.t.Errorf("the app-server takes no jsonrpc member: %s", f.in.Text())
		}
		var m codexMessage
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
		var p map[string]any
		json.Unmarshal(m.Params, &p)
		model, _ := p["model"].(string)
		if f.model != "" {
			model = f.model
		}
		switch m.Method {
		case "initialize":
			f.write(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "codex/0.164.0"}})
		case "thread/start":
			f.write(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": "thr_1"}, "model": model}})
		case "thread/resume":
			if !f.resumable {
				f.write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "no rollout"}})
				continue
			}
			f.write(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": p["threadId"]}, "model": model}})
		case "turn/start":
			f.write(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": "turn_1", "status": "inProgress"}}})
			go f.turn(p)
		case "turn/interrupt":
			close(f.interrupt)
			f.write(map[string]any{"id": m.ID, "result": map[string]any{}})
		}
	}
}

func (f *fakeCodex) note(method string, params map[string]any) {
	params["threadId"], params["turnId"] = "thr_1", "turn_1"
	f.write(map[string]any{"method": method, "params": params})
}

func (f *fakeCodex) turn(p map[string]any) {
	input, _ := p["input"].([]any)
	text, _ := input[0].(map[string]any)["text"].(string)
	if f.script != nil {
		f.script(f, p)
		return
	}
	if strings.HasSuffix(text, "wait") {
		<-f.interrupt
		f.note("turn/completed", map[string]any{"turn": map[string]any{"id": "turn_1", "status": "interrupted"}})
		return
	}
	if strings.HasSuffix(text, "fail") {
		f.note("error", map[string]any{"error": map[string]any{"message": "401 Unauthorized"}, "willRetry": false})
		f.note("turn/completed", map[string]any{"turn": map[string]any{"id": "turn_1", "status": "failed", "error": map[string]any{"message": "401 Unauthorized"}}})
		return
	}
	f.note("item/agentMessage/delta", map[string]any{"itemId": "m1", "delta": "Hel"})
	f.note("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m1", "text": "Hello"}})
	f.note("item/started", map[string]any{"item": map[string]any{"type": "commandExecution", "id": "c1", "command": "ls -la", "cwd": "/work"}})
	r := f.ask("item/commandExecution/requestApproval", map[string]any{"threadId": "thr_1", "turnId": "turn_1", "itemId": "c1",
		"command": "ls -la", "cwd": "/work"})
	f.note("item/started", map[string]any{"item": map[string]any{"type": "fileChange", "id": "p1",
		"changes": []map[string]any{{"path": "a.go", "kind": map[string]any{"type": "update"}, "diff": "x"}, {"path": "/work/.env", "kind": map[string]any{"type": "add"}, "diff": "y"}}}})
	r2 := f.ask("item/fileChange/requestApproval", map[string]any{"threadId": "thr_1", "turnId": "turn_1", "itemId": "p1"})
	u := f.ask("item/tool/requestUserInput", map[string]any{"threadId": "thr_1"})
	var d1, d2 struct {
		Decision string `json:"decision"`
	}
	json.Unmarshal(r.Result, &d1)
	json.Unmarshal(r2.Result, &d2)
	f.mu.Lock()
	f.decisions = append(f.decisions, d1.Decision, d2.Decision)
	f.unknown = u.Error
	f.mu.Unlock()
	f.note("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m2", "text": "Done"}})
	f.note("turn/completed", map[string]any{"turn": map[string]any{"id": "turn_1", "status": "completed"}})
}

func (f *fakeCodex) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, r.Method)
	}
	return out
}

func TestCodexTurns(t *testing.T) {
	task := newTestTask()
	task.Spec.Model = "gpt-6.1-sol"
	s, f, done := startCodex(t, task, "go", false)

	p := answerNext(t, task, permAnswer{Allow: true})
	if p.ToolName != "Bash" || p.Input["command"] != "ls -la" {
		t.Errorf("permission = %s %v", p.ToolName, p.Input)
	}
	// The file change: one question per file, the first allowed, the second denied.
	p = answerNext(t, task, permAnswer{Allow: true})
	if p.ToolName != "Edit" || p.Input["file_path"] != "/work/a.go" {
		t.Errorf("first file = %s %v", p.ToolName, p.Input)
	}
	p = answerNext(t, task, permAnswer{Allow: false, Message: "not the secrets"})
	if p.Input["file_path"] != "/work/.env" {
		t.Errorf("second file = %v", p.Input)
	}
	waitTurns(t, task, 1)
	if task.SessionID != "thr_1" {
		t.Errorf("session = %q", task.SessionID)
	}
	f.mu.Lock()
	if strings.Join(f.decisions, ",") != "accept,decline" || f.unknown == nil || f.unknown.Code != -32601 {
		t.Errorf("decisions %v, unknown %v", f.decisions, f.unknown)
	}
	start := f.requests[2]
	f.mu.Unlock()
	var sp map[string]any
	json.Unmarshal(start.Params, &sp)
	if start.Method != "thread/start" || sp["approvalPolicy"] != "untrusted" || sp["sandbox"] != "danger-full-access" || sp["model"] != "gpt-6.1-sol" {
		t.Errorf("thread/start = %s %v", start.Method, sp)
	}
	var texts []string
	for _, e := range eventsOf(task, "text") {
		texts = append(texts, e.Data["text"].(string))
	}
	if strings.Join(texts, "|") != "Hello|Done" {
		t.Errorf("texts = %v", texts)
	}
	var tools []string
	for _, e := range eventsOf(task, "tool_use") {
		tools = append(tools, e.Data["name"].(string))
	}
	if strings.Join(tools, ",") != "Bash,Edit,Edit" {
		t.Errorf("tools = %v", tools)
	}
	end := eventsOf(task, "turn_end")[0].Data
	if end["subtype"] != "success" || end["result"] != "Done" {
		t.Errorf("turn_end = %v", end)
	}

	// The next turn hears why the file was denied.
	if err := s.send("again"); err != nil {
		t.Fatal(err)
	}
	answerNext(t, task, permAnswer{Allow: true})
	answerNext(t, task, permAnswer{Allow: true})
	answerNext(t, task, permAnswer{Allow: true})
	waitTurns(t, task, 2)
	f.mu.Lock()
	var tp map[string]any
	json.Unmarshal(f.requests[len(f.requests)-1].Params, &tp)
	f.mu.Unlock()
	if in := tp["input"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(in, "/work/.env: not the secrets") || !strings.HasSuffix(in, "again") {
		t.Errorf("second prompt = %q", in)
	}
	s.stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCodexFailedTurn(t *testing.T) {
	task := newTestTask()
	task.Spec.Prompt = "fail"
	_, _, _ = startCodex(t, task, "fail", false)
	waitTurns(t, task, 1)
	end := eventsOf(task, "turn_end")[0].Data
	if end["subtype"] != "error_during_execution" || end["auth_error"] != true || !strings.Contains(end["result"].(string), "401") {
		t.Errorf("turn_end = %v", end)
	}
}

func TestCodexResume(t *testing.T) {
	task := newTestTask()
	task.SessionID = "thr_old"
	_, f, _ := startCodex(t, task, "carry on", true)
	answerNext(t, task, permAnswer{Allow: true})
	answerNext(t, task, permAnswer{Allow: true})
	answerNext(t, task, permAnswer{Allow: true})
	waitTurns(t, task, 1)
	if m := f.methods(); m[2] != "thread/resume" || task.SessionID != "thr_old" {
		t.Errorf("methods %v, session %q", m, task.SessionID)
	}

	// No rollout: a new thread, from the task's prompt.
	task2 := newTestTask()
	task2.SessionID = "thr_gone"
	_, f2, _ := startCodex(t, task2, "carry on", false)
	answerNext(t, task2, permAnswer{Allow: true})
	answerNext(t, task2, permAnswer{Allow: true})
	answerNext(t, task2, permAnswer{Allow: true})
	waitTurns(t, task2, 1)
	m := f2.methods()
	f2.mu.Lock()
	var tp map[string]any
	json.Unmarshal(f2.requests[len(f2.requests)-1].Params, &tp)
	f2.mu.Unlock()
	in := tp["input"].([]any)[0].(map[string]any)["text"].(string)
	if m[3] != "thread/start" || task2.SessionID != "thr_1" || !strings.HasPrefix(in, "go\n\ncarry on") {
		t.Errorf("methods %v, session %q, prompt %q", m, task2.SessionID, in)
	}
}

func TestCodexStopInterrupts(t *testing.T) {
	task := newTestTask()
	s, f, done := startCodex(t, task, "please wait", false)
	waitFor(t, func() bool {
		for _, m := range f.methods() {
			if m == "turn/start" {
				return true
			}
		}
		return false
	})
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.turn != "" })
	task.mu.Lock()
	task.stopping = true
	task.mu.Unlock()
	s.stop()
	<-done
	waitFor(t, func() bool {
		for _, m := range f.methods() {
			if m == "turn/interrupt" {
				return true
			}
		}
		return false
	})
}

func TestCodexWrongModel(t *testing.T) {
	task := newTestTask()
	task.Spec.Model = "gpt-6.1-sol"
	toAgent, clientOut := io.Pipe()
	agentOut, fromAgent := io.Pipe()
	f := &fakeCodex{t: t, model: "gpt-6-luna", in: bufio.NewScanner(toAgent), out: fromAgent, replies: map[int]chan codexMessage{}, interrupt: make(chan struct{})}
	go f.serve()
	s := codexRuntime{}.attach(task, clientOut).(*codexSession)
	if err := s.run("go", agentOut); err == nil || !strings.Contains(err.Error(), "gpt-6-luna") {
		t.Fatalf("run = %v", err)
	}
}

func TestCodexCommand(t *testing.T) {
	args, env := codexRuntime{}.command(newNode(options{}), newTestTask())
	line := strings.Join(args, " ")
	for _, want := range []string{"exec codex app-server", `sandbox_mode="danger-full-access"`, "unset ERIPHE_CODEX_AUTH",
		"/tmp/eriphe-codex/auth.json", "CODEX_HOME=/profile/codex"} {
		if !strings.Contains(line, want) {
			t.Errorf("command lacks %q: %s", want, line)
		}
	}
	if _, ok := env["ERIPHE_CODEX_AUTH"]; ok {
		t.Error("the runtime sets the credential itself")
	}
}

// answerNext answers the next permission request not answered yet: the
// codex runtime asks several in a row.
var answered sync.Map

func answerNext(t *testing.T, task *Task, a permAnswer) *permission {
	t.Helper()
	var p *permission
	waitFor(t, func() bool {
		task.mu.Lock()
		defer task.mu.Unlock()
		for _, x := range task.perms {
			if _, done := answered.Load(x); !done {
				p = x
				return true
			}
		}
		return false
	})
	answered.Store(p, true)
	p.answer <- a
	return p
}

func TestUnwrapShell(t *testing.T) {
	cases := map[string]string{
		`/bin/bash -lc 'echo hi > a.txt'`:         "echo hi > a.txt",
		`/bin/bash -lc 'rm -rf build'`:            "rm -rf build",
		`bash -lc 'echo '"'"'x'"'"' && cat .env'`: "echo 'x' && cat .env",
		`/bin/bash -lc "echo \"\$HOME\""`:         `echo "$HOME"`,
		`/bin/bash -lc ls`:                        "ls",
		`/bin/bash -lc 'ls' && rm -rf /`:          `/bin/bash -lc 'ls' && rm -rf /`,
		`git status`:                              "git status",
		`/bin/bash -lc 'unterminated`:             `/bin/bash -lc 'unterminated`,
	}
	for in, want := range cases {
		if got := unwrapShell(in); got != want {
			t.Errorf("unwrapShell(%s) = %q, want %q", in, got, want)
		}
	}
}
