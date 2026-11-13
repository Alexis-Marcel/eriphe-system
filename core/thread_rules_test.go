package main

import (
	"strings"
	"testing"
)

// The thread rules the code holds for the brain: one thread a subject, a
// light one opened with its subject, and a task started in a turn about a
// light thread followed by that thread.

func openedThread(t *testing.T, out map[string]any) Thread {
	t.Helper()
	th, ok := out["thread"].(map[string]any)
	if !ok {
		t.Fatalf("no thread: %v", out)
	}
	return Thread{ID: int64(th["id"].(float64)), State: th["state"].(string), Mode: th["mode"].(string)}
}

// open_thread on a subject that has a thread gives that thread back, in
// any case; ended, it is reopened light, why « même sujet ».
func TestOpenThreadSameSubjectGivesItBack(t *testing.T) {
	c := stepsCore(t)
	first := lightThread(t, c)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "PR encore", "goal": "Fusionnée.", "subject": " PR:Example/Infra#12 "})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	if got := openedThread(t, out); got.ID != first.ID {
		t.Fatalf("a second thread %d for the subject of %d", got.ID, first.ID)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "Not a new thread") {
		t.Errorf("the result does not say it: %v", out)
	}
	if c.turn.aboutThread() != first.ID {
		t.Errorf("the turn is about %d", c.turn.aboutThread())
	}

	if err := c.closeLight(first.ID, "Fusionnée et déployée.", "brain"); err != nil {
		t.Fatal(err)
	}
	out, isErr = callBrainTool(t, c, "open_thread", map[string]any{"title": "PR de nouveau", "goal": "Fusionnée.", "subject": first.Subject})
	if isErr {
		t.Fatalf("open again: %v", out)
	}
	got := openedThread(t, out)
	if got.ID != first.ID || got.State != threadOpen || got.Mode != modeLight {
		t.Fatalf("not reopened: %+v", got)
	}
	if log := c.threadLog(first.ID, 10); !strings.Contains(threadLogText(log), "Thread rouvert : "+sameSubject) {
		t.Errorf("the log: %s", threadLogText(log))
	}
	if all, _ := c.threads(false, 10); len(all) != 1 {
		t.Errorf("%d threads for one subject", len(all))
	}
	// Another subject is another thread.
	out, _ = callBrainTool(t, c, "open_thread", map[string]any{"title": "Autre", "goal": "Autre.", "subject": "pr:example/infra#13"})
	if openedThread(t, out).ID == first.ID {
		t.Error("another subject gave the same thread")
	}
}

// A light thread needs its subject; a led one on the subject of an active
// thread is refused, naming it.
func TestOpenThreadSubjectRules(t *testing.T) {
	c, _ := threadCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Sans sujet", "goal": "Rien."})
	if !isErr {
		t.Fatalf("a light thread without subject opened: %v", out)
	}
	if all, _ := c.threads(false, 10); len(all) != 1 {
		t.Errorf("%d threads", len(all))
	}
	light, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque", "goal": "Un nom libre.", "subject": "brand:velora"})
	if isErr {
		t.Fatalf("open: %v", light)
	}
	out, isErr = callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque menée", "goal": "Un nom libre.", "subject": "brand:velora",
		"done_when": "Sam a un nom libre", "steps": []any{map[string]any{"key": "a", "title": "Chercher", "prompt": "Cherche."}}})
	if !isErr {
		t.Fatalf("a led thread beside the active one: %v", out)
	}
}

// run_task in a turn about a light thread is a task step of it; thread
// none keeps it a task of its own, and so does a turn about no thread.
func TestRunTaskInThreadTurnIsItsStep(t *testing.T) {
	c, _ := threadCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque Velora", "goal": "Sam sait si le nom est libre.", "subject": "brand:velora"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	th := openedThread(t, out)
	out, isErr = callBrainTool(t, c, "run_task", map[string]any{"title": "Rechercher la marque", "prompt": "Cherche qui détient la marque Velora."})
	if isErr {
		t.Fatalf("run_task: %v", out)
	}
	if int64(out["thread"].(float64)) != th.ID || out["step"] != stepTask {
		t.Fatalf("not a step of thread %d: %v", th.ID, out)
	}
	st, err := c.step(th.ID, stepTask)
	if err != nil || st.Kind != stepTask || st.Title != "Rechercher la marque" || !strings.HasPrefix(st.Prompt, "Cherche qui") {
		t.Fatalf("the step: %+v, %v", st, err)
	}
	if st.Task == "" || out["task"] != st.Task {
		t.Errorf("its task: %q, result %v", st.Task, out["task"])
	}
	// A second one gets a key of its own.
	out, _ = callBrainTool(t, c, "run_task", map[string]any{"prompt": "Cherche les marques proches."})
	if out["step"] != stepTask+"-2" {
		t.Errorf("second step: %v", out)
	}
	// thread none: a task of its own, the thread's steps unchanged.
	out, isErr = callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume les mails.", "thread": "none"})
	if isErr || out["id"] == nil || out["step"] != nil {
		t.Fatalf("thread none: %v", out)
	}
	if steps, _ := c.steps(th.ID); len(steps) != 2 {
		t.Errorf("%d steps", len(steps))
	}
	if _, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "x", "thread": "7"}); !isErr {
		t.Error("a thread other than none taken")
	}
	// A turn about no thread: a task of its own.
	c.turn.about(0)
	if out, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume l'agenda."}); isErr || out["id"] == nil {
		t.Fatalf("no thread: %v", out)
	}
	// About a thread that ended: a task of its own too.
	c.closeLight(th.ID, "Fait.", "brain")
	c.turn.about(th.ID)
	if out, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume la semaine."}); isErr || out["id"] == nil {
		t.Fatalf("ended thread: %v", out)
	}
}

func threadLogText(lines []ThreadLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}
