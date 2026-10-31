package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// agentRun is the run of a fake docker whose container is a Claude Code
// that ends each turn it is given: it records every line of its input in
// <dir>/stdin and answers with a result, in session s-1.
const agentRun = `d=$(dirname "$0")
  while IFS= read -r line; do
    printf '%s\n' "$line" >> "$d/stdin"
    echo '{"type":"result","subtype":"success","session_id":"s-1","result":"done"}'
  done`

// lifecycleNode is a node whose docker is fakeDocker with run.
func lifecycleNode(t *testing.T, run string) (n *node, dir string) {
	docker, dir := fakeDocker(t, run)
	root := filepath.Join(dir, "root")
	return newNode(options{root: root, hostRoot: root, docker: docker, image: "worker", taskNetwork: "eriphe-tasks"}), dir
}

func eventTypes(t *Task) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var types []string
	for _, e := range t.events {
		types = append(types, e.Type)
	}
	return types
}

func lastError(t *Task) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.events) - 1; i >= 0; i-- {
		if t.events[i].Type == "error" {
			msg, _ := t.events[i].Data["message"].(string)
			return msg
		}
	}
	return ""
}

func readSaved(t *testing.T, n *node, id string) saved {
	t.Helper()
	var s saved
	raw, err := os.ReadFile(filepath.Join(n.taskDir(id), "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func fileLines(path string) []string {
	raw, _ := os.ReadFile(path)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// An assigned task runs, ends its turn, gets the next message once idle,
// and goes away with its folder when eriphe-core no longer wants it.
func TestTaskLifecycle(t *testing.T) {
	n, dir := lifecycleNode(t, agentRun)
	a := assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi"}}
	n.reconcile([]assignment{a})
	task := n.get("t-1")
	if task == nil {
		t.Fatal("not launched")
	}
	if task.Image != "worker" || task.Spec.Image != "worker" {
		t.Errorf("image %q, want the node's default", task.Image)
	}
	waitFor(t, func() bool { return task.state() == stateIdle })
	if s := readSaved(t, n, "t-1"); s.Restarts != 0 {
		t.Errorf("saved %+v", s)
	}
	if got := eventTypes(task); !slices.Contains(got, "turn_end") || got[0] != "state" {
		t.Errorf("events %v", got)
	}
	task.mu.Lock()
	sid := task.SessionID
	task.mu.Unlock()
	if sid != "s-1" {
		t.Errorf("session %q", sid)
	}

	// The next message, delivered once: assignments come again unchanged.
	a.Inbox = []inboxItem{{Seq: 1, Kind: "message", Data: map[string]any{"text": "and then?"}}}
	n.reconcile([]assignment{a})
	n.reconcile([]assignment{a})
	waitFor(t, func() bool { return len(fileLines(filepath.Join(dir, "stdin"))) == 2 })
	waitFor(t, func() bool { return task.state() == stateIdle })
	if in := fileLines(filepath.Join(dir, "stdin")); !strings.Contains(in[0], `"hi"`) || !strings.Contains(in[1], "and then?") {
		t.Errorf("agent input %q", in)
	}
	if s := readSaved(t, n, "t-1"); s.Delivered != 1 {
		t.Errorf("saved %+v, want delivered 1", s)
	}

	// No longer wanted: stopped, then forgotten with its folder.
	n.reconcile(nil)
	waitFor(t, func() bool { return task.state() == stateStopped })
	if n.get("t-1") == nil {
		t.Fatal("removed before it was over")
	}
	n.reconcile(nil)
	if n.get("t-1") != nil {
		t.Error("still there")
	}
	if _, err := os.Stat(n.taskDir("t-1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("its folder is left: %v", err)
	}
}

// A task that ran here before resumes its session with a note to carry on;
// one without a session starts over; one restarted too often gives up.
func TestLaunchRestart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prev  saved
		state string
		first string
	}{
		{"resumes", saved{SessionID: "s-0", Seq: 7, Delivered: 2, Restarts: 1}, stateIdle, "You were interrupted"},
		{"no session", saved{Seq: 3}, stateIdle, `"hi"`},
		{"gives up", saved{SessionID: "s-0", Restarts: maxRestarts}, stateFailed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, dir := lifecycleNode(t, agentRun)
			os.MkdirAll(n.taskDir("t-1"), 0o700)
			raw, _ := json.Marshal(tc.prev)
			os.WriteFile(filepath.Join(n.taskDir("t-1"), "task.json"), raw, 0o600)
			n.launch(assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi"}})
			task := n.get("t-1")
			waitFor(t, func() bool { return task.state() == tc.state })
			task.mu.Lock()
			restarts, seq, delivered := task.restarts, task.events[0].Seq, task.delivered
			task.mu.Unlock()
			if restarts != tc.prev.Restarts+1 || seq != tc.prev.Seq+1 || delivered != tc.prev.Delivered {
				t.Errorf("restarts %d, first seq %d, delivered %d from %+v", restarts, seq, delivered, tc.prev)
			}
			if tc.state == stateFailed {
				if !strings.Contains(lastError(task), "giving up") {
					t.Errorf("error %q", lastError(task))
				}
				return
			}
			if got := eventTypes(task); got[0] != "restart" {
				t.Errorf("events %v, want a restart first", got)
			}
			if in := fileLines(filepath.Join(dir, "stdin")); !strings.Contains(in[0], tc.first) {
				t.Errorf("first turn %q, want %q", in[0], tc.first)
			}
		})
	}
}

// A task that cannot start fails with the reason, and nothing runs.
func TestLaunchFails(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		spec     TaskSpec
		want     string
	}{
		{"bad id", "../t", TaskSpec{Runtime: "claude-code"}, "bad task or profile name"},
		{"bad profile", "t-1", TaskSpec{Runtime: "claude-code", Profile: "a/b"}, "bad task or profile name"},
		{"unknown runtime", "t-1", TaskSpec{Runtime: "nope"}, "nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, dir := lifecycleNode(t, agentRun)
			n.launch(assignment{ID: tc.id, Desired: "running", Spec: tc.spec})
			task := n.get(tc.id)
			waitFor(t, func() bool { return task.state() == stateFailed })
			if !strings.Contains(lastError(task), tc.want) {
				t.Errorf("error %q, want %q", lastError(task), tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "args")); err == nil {
				t.Error("a container was run")
			}
		})
	}
}

// A task whose folder cannot be made fails.
func TestLaunchNoFolder(t *testing.T) {
	n, _ := lifecycleNode(t, agentRun)
	os.MkdirAll(filepath.Dir(n.opt.root), 0o700)
	os.WriteFile(n.opt.root, nil, 0o600) // a file where the root should be
	n.launch(assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code"}})
	task := n.get("t-1")
	if task.state() != stateFailed || lastError(task) == "" {
		t.Errorf("state %s, error %q", task.state(), lastError(task))
	}
}

// A task stopped before it ever ran here is reported stopped and its
// folder removed; a running one asked to stop is stopped.
func TestReconcileStopped(t *testing.T) {
	n, dir := lifecycleNode(t, agentRun)
	os.MkdirAll(n.taskDir("t-0"), 0o700)
	n.reconcile([]assignment{{ID: "t-0", Desired: "stopped"}})
	if task := n.get("t-0"); task == nil || task.state() != stateStopped {
		t.Fatalf("got %+v", task)
	}
	if _, err := os.Stat(n.taskDir("t-0")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("its folder is left: %v", err)
	}

	a := assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi"}}
	n.reconcile([]assignment{a})
	task := n.get("t-1")
	waitFor(t, func() bool { return task.state() == stateIdle })
	a.Desired = "stopped"
	n.reconcile([]assignment{a})
	waitFor(t, func() bool { return task.state() == stateStopped })
	if !slices.Contains(fileLines(filepath.Join(dir, "calls")), "stop -t 10 eriphe-task-t-1") {
		t.Errorf("calls %v", fileLines(filepath.Join(dir, "calls")))
	}
	// Stopped is no failure, and it stays known while assigned.
	n.reconcile([]assignment{a})
	if n.get("t-1") == nil || lastError(task) != "" {
		t.Errorf("error %q", lastError(task))
	}
}

// fakeSession is an agent that records what it is sent.
type fakeSession struct {
	mu   sync.Mutex
	sent []string
	err  error
}

func (s *fakeSession) run(string, io.Reader) error { return nil }

func (s *fakeSession) send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, text)
	return nil
}

func (s *fakeSession) stop() {}

// deliver answers a pending permission at once, holds a message until the
// agent is idle, and never hands the same item twice.
func TestDeliver(t *testing.T) {
	n := newNode(options{root: t.TempDir()})
	run := &fakeSession{}
	os.MkdirAll(n.taskDir("t-1"), 0o700)
	task := &Task{ID: "t-1", State: stateRunning, dir: n.taskDir("t-1"), perms: map[string]*permission{}, run: run}
	p := &permission{ID: "p-1", answer: make(chan permAnswer, 1)}
	task.perms["p-1"] = p
	task.inbox = []inboxItem{
		{Seq: 1, Kind: "permission", Data: map[string]any{"id": "p-1", "allow": true, "message": "ok"}},
		{Seq: 2, Kind: "permission", Data: map[string]any{"id": "gone", "allow": true}}, // from before a restart
		{Seq: 3, Kind: "message", Data: map[string]any{"text": "next"}},
	}
	n.deliver(task)
	if a := <-p.answer; !a.Allow || a.Message != "ok" {
		t.Errorf("answer %+v", a)
	}
	if task.delivered != 2 || len(run.sent) != 0 {
		t.Fatalf("delivered %d, sent %v: the agent is busy", task.delivered, run.sent)
	}

	task.State = stateIdle
	run.err = errNotIdle
	n.deliver(task)
	if task.delivered != 2 {
		t.Errorf("delivered %d although the agent refused", task.delivered)
	}

	run.err = nil
	n.deliver(task)
	n.deliver(task)
	if !slices.Equal(run.sent, []string{"next"}) || task.delivered != 3 || task.state() != stateRunning {
		t.Errorf("sent %v, delivered %d, state %s", run.sent, task.delivered, task.state())
	}
	if s := readSaved(t, n, "t-1"); s.Delivered != 3 {
		t.Errorf("saved %+v", s)
	}
}

// A task without an agent yet keeps its messages.
func TestDeliverNotRunning(t *testing.T) {
	n := newNode(options{})
	task := &Task{ID: "t-1", State: stateIdle, perms: map[string]*permission{},
		inbox: []inboxItem{{Seq: 1, Kind: "message", Data: map[string]any{"text": "hi"}}}}
	n.deliver(task)
	if task.delivered != 0 {
		t.Errorf("delivered %d", task.delivered)
	}
}

// Only what changed in a service is reported; a failed report is tried
// again, a service eriphe-core forgot is not.
func TestReportServices(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/broken/"):
			w.WriteHeader(http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "/forgotten/"):
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c := &coreClient{s: &state{Core: srv.URL, Name: "node-1", Secret: "s"}, http: srv.Client()}
	n := newNode(options{})
	for _, name := range []string{"mail", "broken", "forgotten", "same"} {
		n.services[name] = &service{name: name, state: svcRunning}
	}
	n.services["same"].reported = serviceReport{State: svcRunning}

	n.reportServices(c)
	n.reportServices(c)
	mu.Lock()
	defer mu.Unlock()
	count := map[string]int{}
	for _, p := range paths {
		count[p]++
	}
	want := map[string]int{
		"/v1/node/services/mail/report":      1,
		"/v1/node/services/broken/report":    2,
		"/v1/node/services/forgotten/report": 1,
	}
	if len(count) != len(want) {
		t.Errorf("reports %v", count)
	}
	for p, c := range want {
		if count[p] != c {
			t.Errorf("%s reported %d times, want %d", p, count[p], c)
		}
	}
}
