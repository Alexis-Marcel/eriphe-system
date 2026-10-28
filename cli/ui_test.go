package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var testNow = time.Date(2026, 10, 26, 15, 4, 0, 0, time.Local)

// testModel is a TUI fed directly with messages; its commands never run.
func testModel(t *testing.T, w, h int) model {
	t.Helper()
	m := newModel(NewClient("http://core.invalid", "x"))
	m.now = func() time.Time { return testNow }
	m = step(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(m, messagesMsg{msgs: []Message{
		{Seq: 1, Time: testNow, Role: "user", Text: "Start the tests"},
		{Seq: 2, Time: testNow, Role: "brain", Text: "On it: " + strings.Repeat("a long answer ", 20)},
		{Seq: 3, Time: testNow, Role: "event", Text: "Task t-1 waits for a permission"},
	}})
	return step(m, tasksMsg{tasks: []Task{
		{ID: "t-1", Node: "pc", Agent: "sonnet", Dir: "owner/repo@work", State: stateWaitingPermission},
		{ID: "t-2", Node: "nas", Agent: "haiku", State: stateIdle},
	}})
}

func step(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// screen renders the model as plain text and checks it fits the window.
func screen(t *testing.T, m model) string {
	t.Helper()
	out := m.View().Content
	lines := strings.Split(out, "\n")
	if len(lines) != m.high {
		t.Errorf("%d lines for a height of %d", len(lines), m.high)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("line %d is %d wide for %d: %q", i, w, m.width, ansi.Strip(l))
		}
	}
	plain := ansi.Strip(out)
	if os.Getenv("SHOW_SCREENS") != "" {
		t.Logf("\n%s", plain)
	}
	return plain
}

func TestMainView(t *testing.T) {
	for _, size := range [][2]int{{120, 32}, {80, 24}, {60, 20}} {
		m := testModel(t, size[0], size[1])
		s := screen(t, m)
		for _, want := range []string{"connected", "1 waiting", "you · 15:04", "Talk to Eriphe"} {
			if !strings.Contains(s, want) {
				t.Errorf("%v: %q missing", size, want)
			}
		}
		if size[0] >= narrowWidth && !strings.Contains(s, "PERMISSION") {
			t.Errorf("%v: the task list is missing", size)
		}
		m = step(m, key("tab"))
		if s := screen(t, m); !strings.Contains(s, "t-2") {
			t.Errorf("%v: tasks focus: t-2 missing", size)
		}
	}
}

func TestTaskView(t *testing.T) {
	m := testModel(t, 100, 30)
	m = step(m, key("tab"))
	m = step(m, key("enter"))
	if m.task == nil || m.task.id != "t-1" {
		t.Fatalf("task view not open: %+v", m.task)
	}
	st := &TaskStatus{
		Task:              Task{ID: "t-1", Node: "pc", Agent: "sonnet", State: stateWaitingPermission, Prompt: "Fix it", Created: testNow},
		PendingPermission: map[string]any{"id": "p1", "tool_name": "Bash", "input": map[string]any{"command": "go test ./..."}},
		Recent: []Event{
			{Seq: 1, Time: testNow, Type: "text", Data: map[string]any{"text": "Looking"}},
			{Seq: 2, Time: testNow, Type: "permission", Data: map[string]any{"id": "p1", "tool_name": "Bash"}},
		},
	}
	m = step(m, taskStatusMsg{gen: m.task.gen, st: st, initial: true})
	s := screen(t, m)
	for _, want := range []string{"Fix it", "Looking", "PERMISSION", "go test ./...", "y allow"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q missing", want)
		}
	}

	// n asks for a reason; esc cancels; s asks to confirm.
	m = step(m, key("n"))
	if m.task.mode != modeDenyReason || !strings.Contains(screen(t, m), "Reason for the agent") {
		t.Fatal("n does not ask for a reason")
	}
	m = step(m, key("esc"))
	m = step(m, key("s"))
	if m.task.mode != modeConfirmStop || !strings.Contains(screen(t, m), "Stop t-1?") {
		t.Fatal("s does not ask to confirm")
	}
	m = step(m, key("x")) // anything but y cancels
	if m.task.mode != modeNormal || m.task.busy {
		t.Fatal("stop not cancelled")
	}

	// Answers of an older view are dropped.
	m = step(m, taskEventsMsg{gen: m.task.gen - 1, events: []Event{{Seq: 9, Type: "text"}}})
	if m.task.lastSeq != 2 {
		t.Fatalf("stale events applied: %d", m.task.lastSeq)
	}
	m = step(m, taskEventsMsg{gen: m.task.gen, events: []Event{{Seq: 3, Time: testNow, Type: "state", Data: map[string]any{"state": "idle"}}}})
	if m.task.lastSeq != 3 || len(m.task.events) != 3 {
		t.Fatalf("events = %+v", m.task.events)
	}

	// Idle: r opens the reply input.
	st.Task.State, st.PendingPermission = stateIdle, nil
	m = step(m, taskStatusMsg{gen: m.task.gen, st: st})
	m = step(m, key("r"))
	if m.task.mode != modeReply || !strings.Contains(screen(t, m), "Reply to the task") {
		t.Fatal("r does not open the reply")
	}
	m = step(m, key("esc"))
	m = step(m, key("esc"))
	if m.task != nil {
		t.Fatal("esc does not close the task view")
	}
}

func TestDisconnected(t *testing.T) {
	m := testModel(t, 100, 30)
	m = step(m, messagesMsg{err: errors.New("connection refused")})
	s := screen(t, m)
	if !strings.Contains(s, "disconnected") || !strings.Contains(s, "retrying in 1s") {
		t.Fatalf("no disconnection shown:\n%s", s)
	}
	m = step(m, messagesMsg{err: errors.New("connection refused")})
	if m.backoff != 2*time.Second {
		t.Fatalf("backoff = %s", m.backoff)
	}
	m = step(m, messagesMsg{})
	if !m.online || m.backoff != 0 {
		t.Fatal("not reconnected")
	}
	m = step(m, sentMsg{text: "lost words", err: errors.New("timeout")})
	if m.input.Value() != "lost words" || !strings.Contains(screen(t, m), "not sent: timeout") {
		t.Fatal("a failed message is not given back")
	}
}

func TestNextBackoff(t *testing.T) {
	d := time.Duration(0)
	var got []time.Duration
	for range 7 {
		d = nextBackoff(d)
		got = append(got, d)
	}
	if got[0] != time.Second || got[1] != 2*time.Second || got[6] != maxBackoff {
		t.Fatalf("backoff = %v", got)
	}
}

func TestAppendMessages(t *testing.T) {
	have, last := appendMessages(nil, []Message{{Seq: 1}, {Seq: 2}}, 0)
	have, last = appendMessages(have, []Message{{Seq: 2}, {Seq: 3}}, last) // 2 again is dropped
	if len(have) != 3 || last != 3 {
		t.Fatalf("have = %+v, last = %d", have, last)
	}
	many := make([]Message, maxMessages+5)
	for i := range many {
		many[i].Seq = 10 + i
	}
	have, _ = appendMessages(have, many, last)
	if len(have) != maxMessages || have[len(have)-1].Seq != 10+maxMessages+4 {
		t.Fatalf("not capped: %d", len(have))
	}
}

func TestRenderMessage(t *testing.T) {
	msg := Message{Time: testNow, Role: "brain", Text: "one two three four five six seven eight"}
	got := ansi.Strip(renderMessage(msg, 16, testNow))
	lines := strings.Split(got, "\n")
	if lines[0] != "eriphe · 15:04" {
		t.Fatalf("header = %q", lines[0])
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "  ") || lipgloss.Width(l) > 16 {
			t.Fatalf("body line %q", l)
		}
	}
}

func TestRenderTaskRow(t *testing.T) {
	row := renderTaskRow(Task{ID: "t-1", Node: "pc", Agent: "opus", Dir: "owner/repo", State: stateRunning}, true, true, 40)
	first, second := ansi.Strip(row[0]), ansi.Strip(row[1])
	if !strings.HasPrefix(first, "▸ t-1") || !strings.HasSuffix(strings.TrimRight(first, " "), "● running") {
		t.Fatalf("first line %q", first)
	}
	if strings.TrimSpace(second) != "pc · opus · owner/repo" {
		t.Fatalf("second line %q", second)
	}
	if w := lipgloss.Width(row[0]); w != 40 {
		t.Fatalf("width = %d", w)
	}
}
