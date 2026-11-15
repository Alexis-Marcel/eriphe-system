package main

import (
	"context"
	"strings"
	"testing"
)

// taskStep makes a task the running step of a light thread.
func taskStep(t *testing.T, c *core, task, title string) Thread {
	t.Helper()
	s := lightThread(t, c)
	addTaskStep(t, c, s, "work", task, title)
	return s
}

// addTaskStep adds a running task step to a thread.
func addTaskStep(t *testing.T, c *core, s Thread, key, task, title string) {
	t.Helper()
	if _, err := c.store.db.Exec(`INSERT INTO thread_steps (thread, key, n, title, kind, after, spec, status, planned, due, task) VALUES (?, ?, (SELECT COUNT(*) + 1 FROM thread_steps WHERE thread = ?), ?, ?, '', '{}', ?, 0, 0, ?)`,
		s.ID, key, s.ID, title, stepTask, stepRunning, task); err != nil {
		t.Fatal(err)
	}
}

// lastLine is a thread's last log line.
func lastLine(t *testing.T, c *core, s Thread) ThreadLine {
	t.Helper()
	l := c.threadLog(s.ID, 1)
	if len(l) != 1 {
		t.Fatalf("thread %d has no log", s.ID)
	}
	return l[0]
}

// What Eriphe answers a task's question shows in its thread; nothing else
// changes: the task gets the reply as before, a task outside any thread
// logs nowhere.
func TestAnswerTaskShowsInThread(t *testing.T) {
	c := stepsCore(t)
	addTask(t, c, "t-1", stateIdle)
	s := taskStep(t, c, "t-1", "Page contact")
	before := len(c.threadLog(s.ID, 100))
	out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-1", "reply": "mailto, sans service tiers"})
	if isErr || out["result"] != "message sent" {
		t.Fatalf("answer_task: %v", out)
	}
	l := lastLine(t, c, s)
	if l.Who != byBrain || l.Text != "Étape « Page contact » : question de l'assistant → Eriphe a répondu : mailto, sans service tiers" {
		t.Errorf("the line: %+v", l)
	}
	if n := len(c.threadLog(s.ID, 100)); n != before+1 {
		t.Errorf("%d lines, want %d", n, before+1)
	}
	if st, _ := c.step(s.ID, "work"); st.Status != stepRunning || st.Note != "" {
		t.Errorf("the step changed: %+v", st)
	}
	if task, _ := c.store.task("t-1"); task.State != stateRunning {
		t.Errorf("the task: %s", task.State)
	}

	// Its contract's checks fixed: said too.
	addTask(t, c, "t-3", stateIdle)
	addTaskStep(t, c, s, "tests", "t-3", "Tests")
	c.store.setContract("t-3", &taskContract{Objective: "Les tests passent.", Checks: []taskCheck{{Name: "tests", Run: "go test ./...", Dir: "/work/nowhere"}}})
	c.store.setSpec("t-3", nodeTaskSpec{})
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-3", "checks": []any{map[string]any{"name": "tests", "run": "go test ./..."}}}); isErr {
		t.Fatalf("checks: %v", out)
	}
	if l := lastLine(t, c, s); l.Who != byBrain || l.Text != "Étape « Tests » : Eriphe a corrigé les contrôles de l'assistant (tests)." {
		t.Errorf("the checks line: %+v", l)
	}

	// A refused answer logs nothing.
	if _, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-1", "reply": "encore"}); !isErr {
		t.Fatal("a running task answered")
	}
	// A task of no thread: answered, logged nowhere.
	addTask(t, c, "t-2", stateIdle)
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-2", "reply": "oui"}); isErr {
		t.Fatalf("answer_task: %v", out)
	}
	if n := len(c.threadLog(s.ID, 100)); n != before+2 {
		t.Errorf("%d lines after refused and threadless answers, want %d", n, before+2)
	}
}

// A release is logged as what it is: Eriphe asks on a card, the user
// answers. Nothing is released by the brain's line.
func TestReleaseShowsInThreadAsAnAsk(t *testing.T) {
	c := newMailCore(t, accessReads)
	c.store.set(lentByUserKey("t-1"), "1")
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
		State: stateIdle, Tools: "mail", Resources: []ResourceRef{{Kind: resTool, Ref: "mail.move", Access: accessWrite, Hold: true}}})
	s := taskStep(t, c, "t-1", "Ranger les mails")
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-1", "release": []string{" mail.move "}}); isErr {
		t.Fatalf("release: %v", out)
	}
	l := lastLine(t, c, s)
	if l.Who != byBrain || !strings.Contains(l.Text, "a demandé") || !strings.Contains(l.Text, "sur une carte") ||
		!strings.Contains(l.Text, "débloquer mail.move : rien n'est débloqué") {
		t.Errorf("the ask: %+v", l)
	}
	if task, _ := c.store.task("t-1"); !task.Resources[0].Hold {
		t.Fatal("released by the brain's ask")
	}
	if _, err := c.decideCard(context.Background(), accessCard(t, c, "mail.move").ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if l := lastLine(t, c, s); l.Who != byUser || !strings.Contains(l.Text, "a débloqué mail.move sur la carte") {
		t.Errorf("the answer: %+v", l)
	}
}
