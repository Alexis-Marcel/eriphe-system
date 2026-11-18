package main

import (
	"strings"
	"testing"
)

// The thread rules the code holds for the brain: one thread a subject, one
// opened with its subject, and a task started in a turn about a thread
// about that thread.

func openedThread(t *testing.T, out map[string]any) Thread {
	t.Helper()
	th, ok := out["thread"].(map[string]any)
	if !ok {
		t.Fatalf("no thread: %v", out)
	}
	return Thread{ID: int64(th["id"].(float64)), State: th["state"].(string)}
}

// open_thread on a subject that has a thread gives that thread back, in
// any case; ended, it is reopened, why « même sujet ».
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
	if got.ID != first.ID || got.State != threadOpen {
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

// A thread needs its subject.
func TestOpenThreadSubjectRules(t *testing.T) {
	c := startCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Sans sujet", "goal": "Rien."})
	if !isErr {
		t.Fatalf("a thread without subject opened: %v", out)
	}
	if all, _ := c.threads(false, 10); len(all) != 0 {
		t.Errorf("%d threads", len(all))
	}
}

// run_task in a turn about a thread is a task about it, in a node of its
// own; one in a turn about no thread is about nothing, and code never
// opens a thread for it.
func TestRunTaskInThreadTurnIsAboutIt(t *testing.T) {
	c := startCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque Velora", "goal": "Sam sait si le nom est libre.", "alias": "brand:velora"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	th := openedThread(t, out)
	out, isErr = callBrainTool(t, c, "run_task", map[string]any{"title": "Rechercher la marque", "prompt": "Cherche qui détient la marque Velora."})
	if isErr {
		t.Fatalf("run_task: %v", out)
	}
	task, err := c.store.task(out["task"].(string))
	if err != nil || task.About != c.store.resolveAbout(threadRef(th.ID)) {
		t.Fatalf("not about thread %d: %+v %v", th.ID, task, err)
	}
	if ss, _ := out["start_state"].(map[string]any); ss["start"] != "started" {
		t.Errorf("its start: %v", out["start_state"])
	}
	// What run_task refuses of the task is refused as before.
	for name, bad := range map[string]map[string]any{
		"an agent that is none": {"prompt": "Corrige.", "agent": "nobody"},
		"a folder that is none": {"prompt": "Corrige.", "resources": []any{map[string]any{"kind": "folder", "ref": "nowhere", "access": "read"}}},
	} {
		if _, isErr := callBrainTool(t, c, "run_task", bad); !isErr {
			t.Errorf("%s: taken", name)
		}
	}
	// A turn about no thread: a task about nothing, and no thread opened.
	c.turn.about(0)
	before, _ := c.threads(false, 50)
	out, isErr = callBrainTool(t, c, "run_task", map[string]any{"title": "Résumer l'agenda", "prompt": "Résume l'agenda."})
	if isErr {
		t.Fatalf("no thread: %v", out)
	}
	if task, _ := c.store.task(out["task"].(string)); task.About != "" {
		t.Errorf("about %q", task.About)
	}
	if after, _ := c.threads(false, 50); len(after) != len(before) {
		t.Errorf("a thread opened for a task: %d, then %d", len(before), len(after))
	}
}

// A message the user wrote with a thread's surface open makes the turn
// about that thread, and so does one about a surface of its subject. A
// message about another surface, or none, leaves the turn about no thread.
func TestMessageAboutThreadMakesTurnItsOwn(t *testing.T) {
	c := startCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "PR alerting", "goal": "La PR est en prod.", "alias": "pr:example/infra#12"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	th := openedThread(t, out)
	turnOn := func(text, about string) {
		t.Helper()
		c.store.markDelivered(c.store.lastMessageSeq())
		seq, _ := c.store.addUserMessage(text, nil, c.openThreadOf(about)) // as handleSend writes it
		if about != "" {
			c.noteAbout(seq, about)
		}
		c.turn.start(int(seq), int(seq), 0)
		msgs, _ := c.store.undelivered()
		c.threadsDelivered(msgs)
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
	for _, about := range []string{"surface:meteo", "surface:thread-999", ""} {
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
