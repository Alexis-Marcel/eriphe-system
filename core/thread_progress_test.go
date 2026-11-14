package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// remember kind progress writes its subject's thread summary: a light
// thread opened for a subject that has none, the same one written over
// after; never a memory, never from a tainted turn, never on a thread that
// ended; the card shows it.
func TestRememberProgressIsThreadSummary(t *testing.T) {
	c, _ := testMemoryCore(t)
	ownerSays(c, "Le portfolio avance")
	out, err := c.brainRemember(rememberIn{Kind: "progress", Subject: "project:portfolio", Text: "Fait : accueil ; reste : contact"})
	if err != nil {
		t.Fatal(err)
	}
	id, ok := c.liveThreadOf("project:portfolio")
	if !ok || out["thread"] != id {
		t.Fatalf("no thread: %v", out)
	}
	th, _ := c.thread(id)
	if th.Mode != modeLight || th.Summary != "Fait : accueil ; reste : contact" || th.Title != "Suivi de project:portfolio" {
		t.Fatalf("thread: %+v", th)
	}
	if _, err := c.brainRemember(rememberIn{Kind: "progress", Subject: "project:portfolio", Text: "Bloqué sur les photos", State: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if th, _ := c.thread(id); th.Summary != "Bloqué. Bloqué sur les photos" {
		t.Fatalf("summary: %q", th.Summary)
	}
	if n, _ := c.threads(false, 10); len(n) != 1 {
		t.Fatalf("%d threads", len(n))
	}
	if n := c.mem.count(`kind = 'progress'`); n != 0 {
		t.Fatalf("%d progress memories", n)
	}
	if card, _ := c.subjectCard("project:portfolio", subjectCardChars); !strings.Contains(card, "résumé : Bloqué. Bloqué sur les photos") {
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

	// Done, on a subject with no thread: one opened and closed done.
	if _, err := c.brainRemember(rememberIn{Kind: "progress", Subject: "project:brun", Text: "En ligne", State: "done"}); err != nil {
		t.Fatal(err)
	}
	if ts, _ := c.threadsWhere(`subject = 'project:brun'`); len(ts) != 1 || ts[0].State != threadDone || ts[0].Summary != "Terminé. En ligne" {
		t.Fatalf("done: %+v", ts)
	}
	// A thread that ended is not reopened behind the user's back.
	if _, err := c.brainRemember(rememberIn{Kind: "progress", Subject: "project:brun", Text: "Encore"}); err == nil || !strings.Contains(err.Error(), "reopen_thread") {
		t.Fatalf("ended thread: %v", err)
	}

	// A turn that read external content writes no summary.
	c.store.addEvents("t-brain", []EventRow{{Seq: 1, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "WebFetch"}}})
	if _, err := c.brainRemember(rememberIn{Kind: "progress", Subject: "project:portfolio", Text: "LU SUR LE WEB"}); err != errProgressTainted {
		t.Fatalf("tainted: %v", err)
	}
	if th, _ := c.thread(id); strings.Contains(th.Summary, "LU SUR LE WEB") {
		t.Fatal("a tainted turn wrote the summary")
	}
}

// Closing a thread on its goal writes a dated decision of its subject;
// closed again after a reopen, the new one supersedes it.
func TestCloseWritesDecision(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "Site en ligne", Goal: "Le site répond.", Subject: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.closeLight(th.ID, "Déployé sur le NAS", "brain"); err != nil {
		t.Fatal(err)
	}
	xs, _ := c.mem.list(`kind = 'decision' AND status = 'active'`, nil, "", 5)
	if len(xs) != 1 || xs[0].Subject != "project:site" || xs[0].SourceKind != srcThread || xs[0].SourceRef != threadRef(th.ID) ||
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
	other, _ := c.openThread(threadIn{Title: "Autre", Goal: "Rien.", Subject: "project:autre"}, nil, byBrain)
	c.stopThread(context.Background(), other.ID, "brain")
	if n := c.mem.count(`kind = 'decision' AND subject = 'project:autre'`); n != 0 {
		t.Fatalf("a stopped thread wrote %d decisions", n)
	}
	// A tainted thread's decision is external: named, never quoted.
	ext, _ := c.openThread(threadIn{Title: "Mail", Goal: "Répondre.", Subject: "mail:42"}, []string{"external:mail:42"}, byBrain)
	c.closeLight(ext.ID, "Répondu", "brain")
	if zs, _ := c.mem.list(`kind = 'decision' AND subject = 'mail:42'`, nil, "", 1); len(zs) != 1 || zs[0].Trust != trustExternal {
		t.Fatalf("tainted thread's decision: %+v", zs)
	}
}

// An open intention of a live light thread's subject becomes its deadline
// step, once; a proposed one, one of another subject or of a led thread
// does not.
func TestIntentionFollowsThread(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "Site", Goal: "En ligne.", Subject: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	due := c.mem.now().Add(48 * time.Hour)
	x := mustIntend(t, c.mem, &Intention{Text: "Relancer Lina pour le site", Subject: "project:site", DueAt: due})
	mustIntend(t, c.mem, &Intention{Text: "Une idée proposée", Subject: "project:site", DueAt: due, State: intentProposed})
	mustIntend(t, c.mem, &Intention{Text: "Rendre le livre", Subject: "project:autre", DueAt: due})
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
	th, err := c.openThread(threadIn{Title: "Site", Goal: "En ligne.", Subject: "project:site"}, nil, byBrain)
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

// The progress memories are rewritten once into their subjects' threads.
func TestProgressToThreads(t *testing.T) {
	c, _ := testMemoryCore(t)
	live, _ := c.openThread(threadIn{Title: "Site", Goal: "En ligne.", Subject: "project:site"}, nil, byBrain)
	ended, _ := c.openThread(threadIn{Title: "Vieux", Goal: "Fini.", Subject: "project:vieux"}, nil, byBrain)
	c.setThread(ended.ID, map[string]any{"state": threadDone, "summary": "Fini depuis longtemps"})
	progress := func(subject, state, text string, trust string) *Memory {
		x := &Memory{Kind: kindFact, Subject: subject, Text: text}
		if trust != "" {
			x.SourceKind, x.Trust = srcExtraction, trust
		}
		mustAdd(t, c.mem, x)
		c.mem.db.Exec(`UPDATE memories SET kind = 'progress', state = ? WHERE id = ?`, state, x.ID)
		return x
	}
	old := progress("project:site", "active", "Ancien état du site", "")
	site := progress("project:site", "blocked", "Le site attend son certificat", "")
	shop := progress("project:okapi", "active", "Catalogue fait ; reste le paiement", "")
	brun := progress("project:brun", "done", "En ligne", "")
	vieux := progress("project:vieux", "active", "Un état plus vieux que son fil", "")
	ext := progress("project:ext", "active", "LU DANS UN MAIL", trustExternal)
	none := progress("", "active", "Sans sujet", "")
	c.mem.db.Exec(`UPDATE memories SET recorded_at = recorded_at - 100 WHERE id = ?`, old.ID)

	if err := c.progressToThreads(); err != nil {
		t.Fatal(err)
	}
	if th, _ := c.thread(live.ID); th.Summary != "Bloqué. Le site attend son certificat" {
		t.Errorf("live thread: %q", th.Summary)
	}
	if th, _ := c.thread(ended.ID); th.Summary != "Fini depuis longtemps" || th.State != threadDone {
		t.Errorf("ended thread: %+v", th)
	}
	if ts, _ := c.threadsWhere(`subject = 'project:okapi'`); len(ts) != 1 || ts[0].State != threadOpen || ts[0].Summary != "Catalogue fait ; reste le paiement" {
		t.Errorf("okapi: %+v", ts)
	}
	if ts, _ := c.threadsWhere(`subject = 'project:brun'`); len(ts) != 1 || ts[0].State != threadDone {
		t.Errorf("brun: %+v", ts)
	}
	if ts, _ := c.threadsWhere(`subject = 'project:ext'`); len(ts) != 0 {
		t.Errorf("an external progress opened a thread: %+v", ts)
	}
	for _, x := range []*Memory{old, site, shop, brun, vieux} {
		if got, _ := c.mem.memory(x.ID); got.Status != memDeleted || got.Kind != kindFact {
			t.Errorf("moved %q: %+v", x.Text, got)
		}
	}
	for _, x := range []*Memory{ext, none} {
		if got, _ := c.mem.memory(x.ID); got.Status != memActive || got.Kind != kindFact || got.State != "" {
			t.Errorf("kept %q: %+v", x.Text, got)
		}
	}
	if n := c.mem.count(`kind = 'progress'`); n != 0 {
		t.Fatalf("%d progress rows left", n)
	}
	// Once.
	n, _ := c.threads(false, 50)
	if err := c.progressToThreads(); err != nil {
		t.Fatal(err)
	}
	if m, _ := c.threads(false, 50); len(m) != len(n) {
		t.Fatal("rewritten twice")
	}
}
