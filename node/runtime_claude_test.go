package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// Lines as `claude -p --output-format stream-json --verbose
// --include-partial-messages` writes them, trimmed.
const (
	lineStart  = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"s-1","parent_tool_use_id":null}`
	lineHi     = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}},"session_id":"s-1","parent_tool_use_id":null}`
	lineThere  = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there!"}},"session_id":"s-1","parent_tool_use_id":null}`
	lineInput  = `{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"echo"}},"session_id":"s-1","parent_tool_use_id":null}`
	lineSub    = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"subagent"}},"session_id":"s-1","parent_tool_use_id":"toolu_1"}`
	lineWhole  = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]},"session_id":"s-1","parent_tool_use_id":null}`
	lineStop   = `{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"s-1","parent_tool_use_id":null}`
	lineResult = `{"type":"result","subtype":"success","result":"Hi there!","session_id":"s-1"}`
)

func TestTextDelta(t *testing.T) {
	for line, want := range map[string]string{
		lineStart: "", lineHi: "Hi", lineThere: " there!", lineInput: "", lineSub: "", lineStop: "", lineWhole: "",
	} {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if got := textDelta(ev); got != want {
			t.Errorf("textDelta(%s) = %q, want %q", line, got, want)
		}
	}
}

// Only a task asked to stream gets the partial messages.
func TestClaudeStreamFlag(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	for _, stream := range []bool{false, true} {
		args, _ := claudeCode{}.command(n, &Task{ID: "t-1", Spec: TaskSpec{Stream: stream}})
		if got := slices.Contains(args, "--include-partial-messages"); got != stream {
			t.Errorf("stream %v: --include-partial-messages %v", stream, got)
		}
	}
}

// Eriphe's sessions never push to the account's phone nor join Remote
// Control: Eriphe decides when to tell its user.
func TestClaudeNoDevicePush(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1"}
	args, _ := claudeCode{}.command(n, task)
	if i := slices.Index(args, "--settings"); i < 0 || i+1 >= len(args) || args[i+1] != runPath(claudeSettings) {
		t.Fatalf("no --settings file: %v", args)
	}
	var settings map[string]any
	if err := json.Unmarshal(claudeCode{}.files(n, task)[claudeSettings], &settings); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"agentPushNotifEnabled", "inputNeededNotifEnabled", "remoteControlAtStartup"} {
		if settings[key] != false {
			t.Errorf("%s = %v, want false", key, settings[key])
		}
	}
}

// A persistent session takes today's prompt when resumed; the brain's
// agent keeps no memory of its own.
func TestClaudeBrainSession(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	args, env := claudeCode{}.command(n, &Task{ID: "t-1", Spec: TaskSpec{Profile: "brain", NoAgentMemory: true}})
	if i := slices.Index(args, "--system-prompt-snapshot"); i < 0 || args[i+1] != "off" {
		t.Errorf("args = %v", args)
	}
	if env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" || env["CLAUDE_CODE_DISABLE_CLAUDE_MDS"] != "1" {
		t.Errorf("env = %v", env)
	}
	args, env = claudeCode{}.command(n, &Task{ID: "t-2"})
	if slices.Contains(args, "--system-prompt-snapshot") || env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "" {
		t.Errorf("a worker: %v %v", args, env)
	}
}

func (t *Task) draftNow() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.draft
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The deltas grow the draft, reported a few at a time; the whole message
// ends it as a text event.
func TestClaudeDraft(t *testing.T) {
	var mu sync.Mutex
	pokes := 0
	task := &Task{ID: "t-1", perms: map[string]*permission{}, onChange: func() { mu.Lock(); pokes++; mu.Unlock() }}
	agentOut, fromAgent := io.Pipe()
	s := claudeCode{}.attach(task, nopCloser{io.Discard})
	done := make(chan error, 1)
	go func() { done <- s.run("hello", agentOut) }()
	say := func(line string) { fromAgent.Write([]byte(line + "\n")) }

	say(lineStart)
	say(lineHi)
	say(lineSub)
	say(lineThere)
	eventually(t, "the draft", func() bool { return task.draftNow() == "Hi there!" })
	task.mu.Lock()
	if len(task.events) != 0 {
		t.Errorf("the draft made events: %+v", task.events)
	}
	task.mu.Unlock()
	eventually(t, "the draft's report", func() bool { mu.Lock(); defer mu.Unlock(); return pokes >= 1 })

	say(lineWhole)
	say(lineStop)
	say(lineResult)
	fromAgent.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d := task.draftNow(); d != "" {
		t.Errorf("draft after the message = %q", d)
	}
	var texts []string
	for _, e := range task.events {
		if e.Type == "text" {
			texts = append(texts, e.Data["text"].(string))
		}
	}
	if !slices.Equal(texts, []string{"Hi there!"}) {
		t.Errorf("texts = %q", texts)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// An API error comes as the agent's text from a model of its own, and a
// result that is an error although its subtype says success: both are
// marked, so that eriphe-core never shows it as the agent's answer.
func TestClaudeAPIError(t *testing.T) {
	task := &Task{ID: "t-1", perms: map[string]*permission{}}
	agentOut, fromAgent := io.Pipe()
	s := claudeCode{}.attach(task, nopCloser{io.Discard})
	done := make(chan error, 1)
	go func() { done <- s.run("hello", agentOut) }()
	for _, line := range []string{
		`{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"API Error: Connection refused (ECONNREFUSED)"}]},"session_id":"s-1","parent_tool_use_id":null}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"API Error: Connection refused (ECONNREFUSED)","session_id":"s-1"}`,
	} {
		fromAgent.Write([]byte(line + "\n"))
	}
	fromAgent.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	text, end := eventsOf(task, "text"), eventsOf(task, "turn_end")
	if len(text) != 1 || text[0].Data["error"] != true {
		t.Errorf("text = %+v", text)
	}
	if len(end) != 1 || end[0].Data["is_error"] != true {
		t.Errorf("turn_end = %+v", end)
	}
}

// The draft goes with the report, and a new draft alone is worth one.
func TestReportDraft(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := &coreClient{s: &state{Core: srv.URL, Name: "node-1", Secret: "s"}, http: srv.Client()}
	n := newNode(options{})
	task := &Task{ID: "t-1", State: stateRunning}
	task.reported.state = stateRunning

	task.draft = "Hi"
	n.report(c, task)
	n.report(c, task) // nothing new
	task.draft = "Hi there"
	n.report(c, task)
	task.emit("text", map[string]any{"text": "Hi there!"})
	n.report(c, task)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("%d reports: %+v", len(got), got)
	}
	if got[0]["draft"] != "Hi" || got[1]["draft"] != "Hi there" {
		t.Errorf("drafts = %v, %v", got[0]["draft"], got[1]["draft"])
	}
	if _, ok := got[2]["draft"]; ok || len(got[2]["events"].([]any)) != 1 {
		t.Errorf("the text's report = %+v", got[2])
	}
}
