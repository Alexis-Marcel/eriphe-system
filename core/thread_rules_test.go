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
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "PR encore", "goal": "Fusionnée.", "alias": " PR:Example/Infra#12 "})
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
	out, isErr = callBrainTool(t, c, "open_thread", map[string]any{"title": "PR de nouveau", "goal": "Fusionnée.", "alias": first.Alias})
	if isErr {
		t.Fatalf("open again: %v", out)
	}
	got := openedThread(t, out)
	if got.ID != first.ID || got.State != threadOpen || got.Mode != modeLight {
		t.Fatalf("not reopened: %+v", got)
	}
	if log := c.threadLog(first.ID, 10); !strings.Contains(threadLogText(log), "Thread rouvert : "+sameMatter) {
		t.Errorf("the log: %s", threadLogText(log))
	}
	if all, _ := c.threads(false, 10); len(all) != 1 {
		t.Errorf("%d threads for one subject", len(all))
	}
	// Another subject is another thread.
	out, _ = callBrainTool(t, c, "open_thread", map[string]any{"title": "Autre", "goal": "Autre.", "alias": "pr:example/infra#13"})
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
	light, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque", "goal": "Un nom libre.", "alias": "brand:velora"})
	if isErr {
		t.Fatalf("open: %v", light)
	}
	out, isErr = callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque menée", "goal": "Un nom libre.", "alias": "brand:velora",
		"done_when": "Sam a un nom libre", "steps": []any{map[string]any{"key": "a", "title": "Chercher", "prompt": "Cherche."}}})
	if !isErr {
		t.Fatalf("a led thread beside the active one: %v", out)
	}
}

// run_task in a turn about a light thread is a task step of it, a surface
// to fill too; a turn about a led thread, an ended one or none refuses it:
// a task is never loose, and code never opens a thread for it.
func TestRunTaskInThreadTurnIsItsStep(t *testing.T) {
	c, led := threadCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque Velora", "goal": "Sam sait si le nom est libre.", "alias": "brand:velora"})
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
	if ss, _ := out["start_state"].(map[string]any); ss["start"] != "started" {
		t.Errorf("its start: %v", out["start_state"])
	}
	// A second one gets a key of its own.
	out, _ = callBrainTool(t, c, "run_task", map[string]any{"prompt": "Cherche les marques proches."})
	if out["step"] != stepTask+"-2" {
		t.Errorf("second step: %v", out)
	}
	// thread none is gone: a task is never left loose.
	if _, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume les mails.", "thread": "none"}); !isErr {
		t.Fatal("thread none taken")
	}
	if steps, _ := c.steps(th.ID); len(steps) != 2 {
		t.Errorf("%d steps", len(steps))
	}
	if _, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "x", "thread": "7"}); !isErr {
		t.Error("a thread other than none taken")
	}
	// What run_task refuses of the task is refused as before, no step born.
	for name, bad := range map[string]map[string]any{
		"an agent that is none": {"prompt": "Corrige.", "agent": "nobody"},
		"a folder that is none": {"prompt": "Corrige.", "resources": []any{map[string]any{"kind": "folder", "ref": "nowhere", "access": "read"}}},
	} {
		if _, isErr := callBrainTool(t, c, "run_task", bad); !isErr {
			t.Errorf("%s: taken", name)
		}
	}
	// A turn about a led thread: refused, its plan changes with amend_thread.
	c.turn.about(led.ID)
	if out, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume la veille."}); !isErr {
		t.Fatalf("led thread: %v", out)
	}
	if steps, _ := c.steps(led.ID); len(steps) != 4 { // its plan, untouched
		t.Errorf("the led thread has %d steps", len(steps))
	}
	// A turn about no thread: refused, and no thread opened for it.
	c.turn.about(0)
	before, _ := c.threads(false, 50)
	if out, isErr := callBrainTool(t, c, "run_task", map[string]any{"title": "Résumer l'agenda", "prompt": "Résume l'agenda."}); !isErr {
		t.Fatalf("no thread: %v", out)
	}
	if after, _ := c.threads(false, 50); len(after) != len(before) {
		t.Errorf("a thread opened for a task: %d, then %d", len(before), len(after))
	}
	// A turn about no thread that names a subject with a live light
	// thread: a step of that one, no new thread (one thread a subject).
	c.turn.name([]string{"brand:velora"})
	out, isErr = callBrainTool(t, c, "run_task", map[string]any{"title": "Vérifier l'INPI", "prompt": "Cherche Velora à l'INPI."})
	if isErr || out["thread"] == nil || int64(out["thread"].(float64)) != th.ID {
		t.Fatalf("the subject's thread %d: %v", th.ID, out)
	}
	c.turn.name(nil)
	// About a thread that ended: refused, reopen it first.
	c.closeLight(th.ID, "Fait.", "brain")
	c.turn.about(th.ID)
	if out, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume la semaine."}); !isErr {
		t.Fatalf("ended thread: %v", out)
	}
}

// A message the user wrote with a thread's surface open makes the turn
// about that thread, and so does one about a surface of its subject:
// run_task then starts a step of it. A message about another surface, or
// none, leaves the turn about no thread.
func TestMessageAboutThreadMakesTurnItsOwn(t *testing.T) {
	c, _ := threadCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "PR alerting", "goal": "La PR est en prod.", "alias": "pr:example/infra#12"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	th := openedThread(t, out)
	turnOn := func(text, about string) {
		t.Helper()
		c.store.markDelivered(c.store.lastMessageSeq())
		seq, _ := c.store.addMessage(roleUser, text, false)
		if about != "" {
			c.noteAbout(seq, about)
		}
		c.turn.start(int(seq), int(seq), 0)
		msgs, _ := c.store.undelivered()
		c.threadsDelivered(msgs)
	}

	turnOn("Refais les règles proprement.", "surface:"+threadSurfaceID(th.ID))
	if c.turn.aboutThread() != th.ID {
		t.Fatalf("the turn is about %d, not %d", c.turn.aboutThread(), th.ID)
	}
	out, isErr = callBrainTool(t, c, "run_task", map[string]any{"title": "Refaire les règles", "prompt": "Refais les règles de latence."})
	if isErr || out["thread"] == nil || int64(out["thread"].(float64)) != th.ID || out["step"] == nil {
		t.Fatalf("not a step of thread %d: %v", th.ID, out)
	}

	// A surface of the thread's subject: its thread.
	if _, _, err := c.fixtureSurface("brain", surfaceIn{ID: "pr-12", Title: "PR 12", About: "pr:example/infra#12",
		Components: []byte(`[{"id":"root","component":"Text","text":"PR 12"}]`)}); err != nil {
		t.Fatal(err)
	}
	turnOn("Et maintenant ?", "surface:pr-12")
	if c.turn.aboutThread() != th.ID {
		t.Errorf("about a surface of its subject: turn about %d", c.turn.aboutThread())
	}

	// Another surface, a thread that is none, or nothing: no thread.
	if _, _, err := c.fixtureSurface("brain", surfaceIn{ID: "meteo", Title: "Météo",
		Components: []byte(`[{"id":"root","component":"Text","text":"Soleil"}]`)}); err != nil {
		t.Fatal(err)
	}
	for _, about := range []string{"surface:meteo", "surface:" + threadSurfaceID(999), ""} {
		turnOn("Et la météo ?", about)
		if c.turn.aboutThread() != 0 {
			t.Errorf("about %q: turn about %d", about, c.turn.aboutThread())
		}
	}
}

func threadLogText(lines []ThreadLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}
