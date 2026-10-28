package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOpenCodeLive runs the real OpenCode of a worker image, hardened as
// every task, on one of OpenCode's free models: two turns with an allowed
// command and a denied write, then a restart that loads the session.
//
//	ERIPHE_LIVE_IMAGE=eriphe-worker:dev go test -run Live -v
func TestOpenCodeLive(t *testing.T) {
	image := os.Getenv("ERIPHE_LIVE_IMAGE")
	if image == "" {
		t.Skip("ERIPHE_LIVE_IMAGE not set")
	}
	model := envOr("ERIPHE_LIVE_MODEL", "opencode/big-pickle")
	root, _ := filepath.EvalSymlinks(t.TempDir())
	n := newNode(options{root: root, hostRoot: root, docker: "docker", cacheVolume: os.Getenv("ERIPHE_LIVE_CACHE")})
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
