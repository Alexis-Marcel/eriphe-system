package main

import (
	"strings"
	"testing"
	"time"
)

// A card a thread shows is answered on the thread's tile, whatever its
// weight (one that interrupts too): the queue's tiles are for the cards no
// thread shows, and the thread's tile is the user's, first.
func TestThreadCardNeverInTheQueue(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "Invoice", Goal: "Pay the invoice.", Steps: []stepSpec{
		{Key: "pay", Title: "Pay", Kind: stepApproval, Question: "Pay it?"},
	}}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	ask := c.wholeThreadAsks(th.ID)[0]
	x := cardOf(t, c, cardThread, ask)
	today := time.Now().Truncate(24 * time.Hour)
	for _, w := range []int{2, weightBlocksATask, weightInterrupts} {
		e := c.queuePlace(attentionItem{Ref: cardItemRef(x.ID), Weight: w}, today, c.threadShownCards())
		if e.surface != threadSurfaceID(th.ID) || !e.about {
			t.Fatalf("weight %d: shown by %q", w, e.surface)
		}
	}
	if _, _, meta := threadSurfaceOf(t, c, th.ID); meta.State != tileYours {
		t.Fatalf("its tile: %q", meta.State)
	}
}

// A thread's tile state is code's (#574): new until the user opens it, and
// new again when something changes after their visit (a badge outside the
// focus too: the page's metadata carries it); quiet once seen; urgent when
// a note is due within two hours, written again by the tick as that time
// comes.
func TestThreadTileState(t *testing.T) {
	c, l := surfaceCore(t)
	th := itemsThread(t, c, l)
	c.keepThreadSurface(th.ID)
	if _, _, meta := threadSurfaceOf(t, c, th.ID); meta.State != tileNew || meta.New == 0 {
		t.Fatalf("never opened: %q, %d new", meta.State, meta.New)
	}

	// Opened: nothing new, nothing waits, nothing runs.
	c.threadVisited(th.ID, time.Now())
	if _, _, meta := threadSurfaceOf(t, c, th.ID); meta.State != tileQuiet || meta.New != 0 {
		t.Fatalf("opened: %q, %d new", meta.State, meta.New)
	}

	// A note written after the visit: new, with its count.
	// (what was there moves back a minute, the visit ten seconds)
	c.store.db.Exec(`UPDATE threads SET created = created - 60, visited = visited - 10, seen = seen - 10 WHERE id = ?`, th.ID)
	c.store.db.Exec(`UPDATE thread_items SET noted = noted - 60 WHERE thread = ?`, th.ID)
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Épargne", Note: "Un versement est arrivé.", State: itemStateInfo}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	c.keepThreadSurface(th.ID)
	if _, _, meta := threadSurfaceOf(t, c, th.ID); meta.State != tileNew || meta.New != 1 {
		t.Fatalf("a note after the visit: %q, %d new", meta.State, meta.New)
	}
	c.threadVisited(th.ID, time.Now())

	// A note due in three hours: not urgent yet.
	soon := time.Now().In(c.userZone()).Add(3 * time.Hour).Format("2006-01-02T15:04")
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Courant", Note: "Solde bas avant le loyer.", State: itemStateAct, Due: soon}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	c.threadVisited(th.ID, time.Now())
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if meta.State == tileUrgent || s.Weight >= weightUrgent {
		t.Fatalf("due in 3 h, urgent: %q weight %d", meta.State, s.Weight)
	}
	// Time passes: due within two hours, the tick turns it urgent.
	c.store.db.Exec(`UPDATE thread_items SET due = ? WHERE thread = ? AND item = 'Courant'`, time.Now().Add(90*time.Minute).Unix(), th.ID)
	c.keepThreadSurfaces()
	s, data, meta := threadSurfaceOf(t, c, th.ID)
	if meta.State != tileUrgent || s.Weight < weightUrgent || tileStateOf(s) != tileUrgent {
		t.Fatalf("due within 2 h: %q weight %d", meta.State, s.Weight)
	}
	if p, _ := data["progress"].(map[string]any); p["label"] != "Urgent" {
		t.Fatalf("its pill: %v", data["progress"])
	}
	// Dealt with: no longer urgent.
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Courant", Note: "Viré.", State: itemStateDone}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	c.keepThreadSurface(th.ID)
	if s, _, meta := threadSurfaceOf(t, c, th.ID); meta.State == tileUrgent || s.Weight >= weightUrgent {
		t.Fatalf("done, still urgent: %q weight %d", meta.State, s.Weight)
	}
}

// What waits for the user is theirs: a thread one of whose cards waits is
// yours, ahead of everything; an ended one is done, last.
func TestThreadTileStateYoursAndDone(t *testing.T) {
	steps := sampleSteps("fr", time.Now())
	th := Thread{ID: 1, State: threadOpen}
	if got := threadTileState(th, steps, true, 50, 0); got != tileYours {
		t.Fatalf("waits: %q", got)
	}
	if got := threadTileState(th, steps, true, 90, 3); got != tileYours {
		t.Fatalf("waits and urgent: %q", got)
	}
	if got := threadTileState(th, steps, false, 50, 0); got != tileRunning {
		t.Fatalf("a step runs: %q", got)
	}
	th.State = threadDone
	if got := threadTileState(th, steps, true, 90, 3); got != tileDone {
		t.Fatalf("ended: %q", got)
	}
}

// No model orders the workspace and none judges it: no composer role, and
// a session on the workspace journals no surface signal.
func TestNoComposer(t *testing.T) {
	if _, ok := assistantRoles["composer"]; ok {
		t.Fatal("a composer role is still registered")
	}
	c := focusCore(t)
	now := time.Now()
	c.focusSurface("comptes", now)
	c.focusSurface("", now)
	writeTestSurface(t, c, surfaceIn{ID: "nouveau", Title: "Nouveau", Weight: 30})
	c.relayout()
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind LIKE 'surface.%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d surface signals journaled", n)
	}
	// An assistant of the role gone, left in the catalog, is left out at
	// load; the rest loads.
	if err := c.store.putCatalog(kindAssistant, "composer", Assistant{Role: "composer", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	d, skipped := loadCatalog(c.base, c.store)
	if _, ok := d.Assistants["composer"]; ok || !strings.Contains(skipped[kindAssistant+"/composer"], "unknown role") {
		t.Fatalf("assistants %v, skipped %v", d.Assistants, skipped)
	}
}

// What the user placed is kept past their session, until placed again,
// reset or its surface gone; what they closed only for the session.
func TestPlacementKept(t *testing.T) {
	c, _ := surfaceCore(t)
	writeTestSurface(t, c, surfaceIn{ID: "agenda", Title: "Agenda", Weight: 30})
	writeTestSurface(t, c, surfaceIn{ID: "meteo", Title: "Météo", Weight: 60})
	writeTestSurface(t, c, surfaceIn{ID: "notes", Title: "Notes", Weight: 50})
	before := time.Now().Add(-5 * time.Hour) // a session that ended
	if err := c.placeSurface("agenda", gestureResized, sizeL, 0, 0, before); err != nil {
		t.Fatal(err)
	}
	c.closeSurface("notes", before)
	c.layout.userLoaded, c.layout.user = false, userPlacement{} // a restart
	l := c.relayout()
	if tl, ok := tileOf(l, "agenda"); !ok || !tl.Pinned || tl.Size != sizeL || tl.Col != 0 || tl.Row != 0 {
		t.Fatalf("the placement went with the session: %s", tilesOf(l))
	}
	if ch, ok := chipOf(l, "notes"); ok && ch.Closed {
		t.Fatal("closed last session, still closed")
	}
	// Reset: code lays everything out in its order again.
	if _, err := c.arrangeWorkspace(workspaceArrangeIn{Reset: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if tl, _ := tileOf(c.relayout(), "agenda"); tl.Pinned {
		t.Fatal("reset, still pinned")
	}
	// The brain places at the user's request; the surface gone, its pin goes.
	if _, err := c.arrangeWorkspace(workspaceArrangeIn{Items: []arrangeItemIn{{ID: "meteo", Size: sizeXL}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if tl, _ := tileOf(c.relayout(), "meteo"); !tl.Pinned || tl.Size != sizeXL {
		t.Fatalf("arranged: %s", tilesOf(c.relayout()))
	}
	if err := c.surfaceRemove("meteo", surfaceAuthorBrain); err != nil {
		t.Fatal(err)
	}
	c.relayout()
	if _, ok := c.placementNow(time.Now()).Pins["meteo"]; ok {
		t.Fatal("the pin of a surface gone stayed")
	}
}
