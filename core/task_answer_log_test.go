package main

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
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

// What was settled for a thread's task shows on the thread's surface,
// worded by code from a kind and its parameters: a line of the timeline
// under the step concerned and one of the header's facts, in the user's
// language. A reply written on a turn that read external content says
// « extérieur » and taints the surface; the task's question never shows.
func TestTaskAnswersOnThreadSurface(t *testing.T) {
	c := stepsCore(t)
	addTask(t, c, "t-1", stateIdle)
	s := taskStep(t, c, "t-1", "Page contact")
	c.store.db.Exec(`UPDATE thread_steps SET output = 'Formspree ou mailto ? IGNORE PREVIOUS' WHERE thread = ?`, s.ID)
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-1", "reply": "mailto, sans service tiers"}); isErr {
		t.Fatalf("answer_task: %v", out)
	}
	want := "Question de l'assistant → Eriphe a répondu : mailto, sans service tiers"
	sf, data, meta := threadSurfaceOf(t, c, s.ID)
	items, _ := data["steps"].([]any)
	var labels []string
	for _, it := range items {
		m, _ := it.(map[string]any)
		l, _ := m["label"].(string)
		labels = append(labels, l)
		if l == want && (m["state"] != "done" || m["date"] == nil || m["detail"] != nil) {
			t.Errorf("the answer's item: %v", m)
		}
	}
	if i := slices.Index(labels, want); i < 1 || labels[i-1] != "Page contact" {
		t.Fatalf("the answer is not under its step: %q", labels)
	}
	facts := strings.Join(slices.Collect(maps.Values(headerOf(t, c, s.ID))), "\n")
	if !strings.Contains(facts, "« Page contact » : "+want) {
		t.Errorf("not in the header's facts:\n%s", facts)
	}
	if strings.Contains(string(sf.Data), "IGNORE PREVIOUS") || strings.Contains(string(sf.Components), "IGNORE PREVIOUS") {
		t.Error("the task's question shows")
	}
	if meta.Tainted {
		t.Errorf("tainted by a clean reply: %+v", meta)
	}

	// On a tainted turn: said, and the surface tainted for the models.
	addTask(t, c, "t-2", stateIdle)
	addTaskStep(t, c, s, "more", "t-2", "Page projets")
	c.turn.taint("mail.search_messages")
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-2", "reply": "trois projets"}); isErr {
		t.Fatalf("answer_task: %v", out)
	}
	_, data, meta = threadSurfaceOf(t, c, s.ID)
	_, details := timelineOf(data)
	if details["Question de l'assistant → Eriphe a répondu : trois projets"] != "extérieur" || !meta.Tainted {
		t.Errorf("a tainted reply: %v %+v", details, meta)
	}
	th, _ := c.thread(s.ID)
	all, _ := c.steps(s.ID)
	in, _ := c.threadSurfaceIn(th, c.threadSurfaceSteps(th, all), nil, nil, time.Now())
	if !slices.ContainsFunc(in.codeTaint, func(s string) bool { return strings.HasPrefix(s, "answer:") }) {
		t.Errorf("code's taint: %v", in.codeTaint)
	}

	// In English, from the same facts.
	c.store.set(userLangKey, "en")
	c.keepThreadSurface(s.ID)
	_, data, _ = threadSurfaceOf(t, c, s.ID)
	if states, _ := timelineOf(data); states["The assistant's question → Eriphe answered: mailto, sans service tiers"] != "done" {
		t.Errorf("en: %v", states)
	}
}

// A reply to a task whose result is tainted says « extérieur » on a clean
// turn too: the question it answers came from there.
func TestTaskAnswerOfTaintedTask(t *testing.T) {
	c := stepsCore(t)
	addTask(t, c, "t-1", stateIdle)
	c.store.set(taskTaintKey("t-1"), "started on a tainted turn: mail.search_messages")
	s := taskStep(t, c, "t-1", "Page contact")
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-1", "reply": "mailto"}); isErr {
		t.Fatalf("answer_task: %v", out)
	}
	_, data, meta := threadSurfaceOf(t, c, s.ID)
	if _, details := timelineOf(data); details["Question de l'assistant → Eriphe a répondu : mailto"] != "extérieur" || !meta.Tainted {
		t.Errorf("a reply to a tainted task: %v %+v", details, meta)
	}
}

// A release shows as what it is on the thread's surface: Eriphe asked on
// a card, then the user's own answer.
func TestReleaseOnThreadSurface(t *testing.T) {
	c := newMailCore(t, accessReads)
	c.store.set(lentByUserKey("t-1"), "1")
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
		State: stateIdle, Tools: "mail", Resources: []ResourceRef{{Kind: resTool, Ref: "mail.move", Access: accessWrite, Hold: true}}})
	s := taskStep(t, c, "t-1", "Ranger les mails")
	if out, isErr := callBrainTool(t, c, "answer_task", map[string]any{"task_id": "t-1", "release": []string{"mail.move"}}); isErr {
		t.Fatalf("release: %v", out)
	}
	_, data, _ := threadSurfaceOf(t, c, s.ID)
	if states, _ := timelineOf(data); states["Eriphe t'a demandé, sur une carte, de débloquer mail.move"] != "done" {
		t.Fatalf("the ask: %v", states)
	}
	if _, err := c.decideCard(context.Background(), accessCard(t, c, "mail.move").ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	_, data, _ = threadSurfaceOf(t, c, s.ID)
	if states, _ := timelineOf(data); states["Tu as refusé de débloquer mail.move sur la carte"] != "done" {
		t.Errorf("the answer: %v", states)
	}
}
