package main

import (
	"testing"
	"time"
)

func TestShortDir(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"owner/repo", 20, "owner/repo"},
		{"owner/repo@feat/x", 20, "owner/repo@feat/x"},
		{"https://github.com/owner/repo.git", 20, "owner/repo"},
		{"https://github.com/owner/repo.git@work", 20, "owner/repo@work"},
		{"git@github.com:owner/repo.git", 20, "owner/repo"},
		{"/home/me/projects/very/long/path", 10, "…long/path"},
		{"", 10, "-"},
	} {
		if got := shortDir(c.in, c.n); got != c.want {
			t.Errorf("shortDir(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestEventText(t *testing.T) {
	for _, c := range []struct {
		e           Event
		label, text string
	}{
		{Event{Type: "text", Data: map[string]any{"text": " hi \n"}}, "agent", "hi"},
		{Event{Type: "tool_use", Data: map[string]any{"name": "Read", "input": map[string]any{"file_path": "/a"}}}, "tool", `Read {"file_path":"/a"}`},
		{Event{Type: "permission", Data: map[string]any{"id": "p", "tool_name": "Bash", "input": map[string]any{"command": "ls"}}}, "permission", `asks to use Bash {"command":"ls"}`},
		{Event{Type: "permission", Data: map[string]any{"id": "p"}}, "permission", "asks to use a tool"},
		{Event{Type: "permission_answer", Data: map[string]any{"allow": true}}, "answer", "allowed"},
		{Event{Type: "permission_answer", Data: map[string]any{"allow": false, "message": "no"}}, "answer", "denied: no"},
		{Event{Type: "turn_end", Data: map[string]any{"subtype": "success"}}, "turn", "turn ended"},
		{Event{Type: "turn_end", Data: map[string]any{"subtype": "error_max_turns", "auth_error": true}}, "turn", "turn ended (error_max_turns), authentication error"},
		{Event{Type: "error", Data: map[string]any{"message": "boom", "stderr": "trace"}}, "error", "boom\ntrace"},
		{Event{Type: "state", Data: map[string]any{"state": "idle"}}, "state", "idle"},
		{Event{Type: "other", Data: map[string]any{"k": 1}}, "other", `{"k":1}`},
	} {
		label, text := eventText(c.e)
		if label != c.label || text != c.text {
			t.Errorf("eventText(%+v) = %q, %q; want %q, %q", c.e, label, text, c.label, c.text)
		}
	}
}

func TestClockAndTruncate(t *testing.T) {
	now := time.Date(2026, 10, 26, 18, 0, 0, 0, time.Local)
	if got := clock(now.Add(-time.Hour), now); got != "17:00" {
		t.Errorf("today: %q", got)
	}
	if got := clock(now.AddDate(0, 0, -2), now); got != "Oct 24 18:00" {
		t.Errorf("earlier: %q", got)
	}
	if got := truncate("héllo world", 5); got != "héll…" {
		t.Errorf("truncate: %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short: %q", got)
	}
	if n := waitingCount([]Task{{State: stateWaitingPermission}, {State: stateIdle}, {State: stateWaitingPermission}}); n != 2 {
		t.Errorf("waiting = %d", n)
	}
	if got := taskWhere(Task{Node: "pc", Agent: "opus", Dir: "o/r"}, 10); got != "pc · opus · o/r" {
		t.Errorf("where: %q", got)
	}
}
