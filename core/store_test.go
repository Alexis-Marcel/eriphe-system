package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.registerNode("pc", hashSecret("secret")); err != nil {
		t.Fatal(err)
	}
	if err := s.setNodeStatus("pc", true, []byte(`{"cpus":20}`)); err != nil {
		t.Fatal(err)
	}
	if !s.checkNode("pc", hashSecret("secret")) || s.checkNode("pc", hashSecret("other")) {
		t.Fatal("checkNode")
	}
	nodes, _ := s.nodes()
	if len(nodes) != 1 || !nodes[0].Online || string(nodes[0].Info) != `{"cpus":20}` {
		t.Fatalf("nodes = %+v", nodes)
	}

	task := &TaskRow{ID: "t-1", Node: "pc", Runtime: "claude-code", Identity: "claude", Dir: "/w", Prompt: "do", State: stateRunning}
	if err := s.insertTask(task); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.activeCount("pc"); n != 1 {
		t.Fatalf("active = %d", n)
	}
	now := time.Now()
	evs := []EventRow{{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "hi"}},
		{Seq: 2, Time: now, Type: "state", Data: map[string]any{"state": "idle"}}}
	if err := s.addEvents("t-1", evs); err != nil {
		t.Fatal(err)
	}
	if err := s.addEvents("t-1", evs[1:]); err != nil { // replays are ignored
		t.Fatal(err)
	}
	got, _ := s.task("t-1")
	if got.LastSeq != 2 || got.Identity != "claude" {
		t.Fatalf("task = %+v", got)
	}
	last, _ := s.events("t-1", 1)
	if len(last) != 1 || last[0].Seq != 2 {
		t.Fatalf("events = %+v", last)
	}
	s.updateTask("t-1", map[string]any{"state": stateStopped})
	if active, _ := s.tasks(true, 10); len(active) != 0 {
		t.Fatalf("active tasks = %d", len(active))
	}
	if _, err := s.task("nope"); err != errNoTask {
		t.Fatalf("missing task: %v", err)
	}
}
