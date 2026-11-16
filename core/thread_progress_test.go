package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// remember kind progress writes its subject's thread summary, written
// over after; never a memory, never from a tainted turn, never on a thread
// that ended; and code never opens a thread for it: a subject with none is
// refused (open_thread first). The card shows it.
func TestRememberProgressIsThreadSummary(t *testing.T) {
	c, _ := testMemoryCore(t)
	ownerSays(c, "Le portfolio avance")
	if _, err := c.brainRemember(rememberIn{Kind: "progress", About: "project:portfolio", Text: "Fait : accueil"}); err != errNoProgressThread {
		t.Fatalf("a subject with no thread: %v", err)
	}
	if _, err := c.brainRemember(rememberIn{Kind: "progress", Text: "Fait : accueil"}); err != errNoProgressThread {
		t.Fatalf("a turn about no thread: %v", err)
	}
	if n, _ := c.threads(false, 10); len(n) != 0 {
		t.Fatalf("code opened %d threads", len(n))
	}
	opened, err := c.openThread(threadIn{Title: "Portfolio", Goal: "Le portfolio est en ligne.", Alias: "project:portfolio"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	id := opened.ID
	out, err := c.brainRemember(rememberIn{Kind: "progress", About: "project:portfolio", Text: "Fait : accueil ; reste : contact"})
	if err != nil {
		t.Fatal(err)
	}
	if out["thread"] != id {
		t.Fatalf("not its thread: %v", out)
	}
	if th, _ := c.thread(id); th.Summary != "Fait : accueil ; reste : contact" {
		t.Fatalf("thread: %+v", th)
	}
	if _, err := c.brainRemember(rememberIn{Kind: "progress", About: "project:portfolio", Text: "Bloqué sur les photos", State: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if th, _ := c.thread(id); th.Summary != "Bloqué. Bloqué sur les photos" {
		t.Fatalf("summary: %q", th.Summary)
	}
	if n, _ := c.threads(false, 10); len(n) != 1 {
		t.Fatalf("%d threads", len(n))
	}
	if ts, _ := c.threadsWhere(`title LIKE 'Suivi de%'`); len(ts) != 0 {
		t.Fatalf("a thread opened by code: %+v", ts)
	}
	if n := c.mem.count(`kind = 'progress'`); n != 0 {
		t.Fatalf("%d progress memories", n)
	}
	if card, _ := c.aboutCard(threadRef(id), threadCardChars); !strings.Contains(card, "résumé : Bloqué. Bloqué sur les photos") {
		t.Errorf("card:\n%s", card)
	}
	// Without a subject, the turn's thread's.
	c.turnThread(id)
	if _, err := c.brainRemember(rememberIn{Kind: "progress", Text: "Photos reçues"}); err != nil {
		t.Fatal(err)
	}
	if th, _ := c.thread(id); th.Summary != "Photos reçues" {
		t.Fatalf("summary: %q", th.Summary)
	}

	// A thread that ended is not reopened behind the user's back.
	brun, err := c.openThread(threadIn{Title: "Brun", Goal: "En ligne.", Alias: "project:brun"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.closeLight(brun.ID, "En ligne.", "brain"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainRemember(rememberIn{Kind: "progress", About: "project:brun", Text: "Encore"}); err == nil || !strings.Contains(err.Error(), "reopen_thread") {
		t.Fatalf("ended thread: %v", err)
	}

	// A turn that read external content writes no summary.
	c.store.addEvents("t-brain", []EventRow{{Seq: 1, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "WebFetch"}}})
	if _, err := c.brainRemember(rememberIn{Kind: "progress", About: "project:portfolio", Text: "LU SUR LE WEB"}); err != errProgressTainted {
		t.Fatalf("tainted: %v", err)
	}
	if th, _ := c.thread(id); strings.Contains(th.Summary, "LU SUR LE WEB") {
		t.Fatal("a tainted turn wrote the summary")
	}
	// Unless the thread's own steps read external content: its summary is
	// never quoted then, and a wake they tainted still says where it stands.
	c.insertSteps(id, []stepSpec{{Key: "read", Title: "Lire la page", Kind: stepTask}}, false)
	c.setStep(id, "read", map[string]any{"tainted": "external:web"})
	if _, err := c.brainRemember(rememberIn{Kind: "progress", About: "project:portfolio", Text: "La page est relue"}); err != nil {
		t.Fatalf("a thread already external: %v", err)
	}
	if card, _ := c.aboutCard(threadRef(id), threadCardChars); strings.Contains(card, "La page est relue") {
		t.Errorf("an external thread's summary quoted:\n%s", card)
	}
}

// Closing a thread on its goal writes a dated decision of its subject;
// closed again after a reopen, the new one supersedes it.
func TestCloseWritesDecision(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "Site en ligne", Goal: "Le site répond.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.closeLight(th.ID, "Déployé sur le NAS", "brain"); err != nil {
		t.Fatal(err)
	}
	xs, _ := c.mem.list(`kind = 'decision' AND status = 'active'`, nil, "", 5)
	if len(xs) != 1 || xs[0].About != threadRef(th.ID) || xs[0].SourceKind != srcThread || xs[0].SourceRef != threadRef(th.ID) ||
		xs[0].Trust != trustFirstParty || !strings.Contains(xs[0].Text, "Fil « Site en ligne » clos le "+time.Now().Format("2006-01-02")+" : Déployé sur le NAS") {
		t.Fatalf("decision: %+v", xs)
	}
	if _, err := c.reopenThread(th.ID, "le certificat expire", "brain"); err != nil {
		t.Fatal(err)
	}
	if err := c.closeLight(th.ID, "Certificat renouvelé", "brain"); err != nil {
		t.Fatal(err)
	}
	ys, _ := c.mem.list(`kind = 'decision' AND status = 'active' AND superseded_by IS NULL`, nil, "", 5)
	if len(ys) != 1 || ys[0].Supersedes != xs[0].ID || !strings.Contains(ys[0].Text, "Certificat renouvelé") {
		t.Fatalf("after the second close: %+v", ys)
	}
	// Stopped, it decided nothing.
	other, _ := c.openThread(threadIn{Title: "Autre", Goal: "Rien.", Alias: "project:autre"}, nil, byBrain)
	c.stopThread(context.Background(), other.ID, "brain")
	if n := c.mem.count(`kind = 'decision' AND about = '` + threadRef(other.ID) + `'`); n != 0 {
		t.Fatalf("a stopped thread wrote %d decisions", n)
	}
	// A tainted thread's decision is external: named, never quoted.
	ext, _ := c.openThread(threadIn{Title: "Mail", Goal: "Répondre.", Alias: "topic:mail-42"}, []string{"external:mail:42"}, byBrain)
	c.closeLight(ext.ID, "Répondu", "brain")
	if zs, _ := c.mem.list(`kind = 'decision' AND about = ?`, []any{threadRef(ext.ID)}, "", 1); len(zs) != 1 || zs[0].Trust != trustExternal {
		t.Fatalf("tainted thread's decision: %+v", zs)
	}
}

// An open intention of a live light thread's subject becomes its deadline
// step, once; a proposed one, one of another subject or of a led thread
// does not.
func TestIntentionFollowsThread(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "Site", Goal: "En ligne.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	due := c.mem.now().Add(48 * time.Hour)
	x := mustIntend(t, c.mem, &Intention{Text: "Relancer Lina pour le site", About: "project:site", DueAt: due})
	mustIntend(t, c.mem, &Intention{Text: "Une idée proposée", About: "project:site", DueAt: due, State: intentProposed})
	mustIntend(t, c.mem, &Intention{Text: "Rendre le livre", About: "project:autre", DueAt: due})
	c.threadsTick(time.Now())
	c.threadsTick(time.Now())
	steps, _ := c.steps(th.ID)
	if len(steps) != 1 || steps[0].Key != intentionStepKey(x.ID) || steps[0].Kind != stepDeadline || steps[0].Intention != x.ID {
		t.Fatalf("steps: %+v", steps)
	}
	// Dropped, it is not added again.
	c.setStep(th.ID, steps[0].Key, map[string]any{"status": stepDropped})
	c.threadsTick(time.Now())
	if steps, _ := c.steps(th.ID); len(steps) != 1 {
		t.Fatalf("added again: %+v", steps)
	}
}

// A card born in a turn about a thread shows in its timeline: a memory
// the turn proposes, as well as a task's start.
func TestCardOfThreadTurnInTimeline(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "Site", Goal: "En ligne.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	ownerSays(c, "Regarde la page")
	c.turnThread(th.ID)
	c.store.addEvents("t-brain", []EventRow{{Seq: 1, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "WebFetch"}}})
	out, err := c.brainRemember(rememberIn{Kind: "fact", Text: "La page dit que le site est lent"})
	if err != nil || out["status"] != memProposed {
		t.Fatalf("remember: %v %v", out, err)
	}
	if refs := timelineCards(t, c, th.ID); refs != proposalRef(cardMemory, out["id"].(int64)) {
		t.Fatalf("timeline: %q", refs)
	}
}

// Threads are born in one place, openThread, which the brain, a task and
// a skill's run reach through openThreadOnce: no other code path writes a
// thread (no « Suivi de… » opened for a progress, none for a task).
func TestThreadsBornInOnePlace(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, _ := os.ReadFile(f)
		src := string(raw)
		n := strings.Count(src, "INSERT INTO threads ")
		if f == "thread_engine.go" {
			n -= 2 // openThread: a light thread, a led one
		}
		if n != 0 {
			t.Errorf("%s writes a thread outside openThread (%d)", f, n)
		}
		if strings.Contains(src, `"Suivi de `) {
			t.Errorf("%s titles a thread by code", f)
		}
	}
}
