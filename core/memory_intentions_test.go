package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The lifecycle, in code: reminders come due on the minute, snoozes come
// back, closed ones stay closed, moves the lifecycle forbids are refused.
func TestIntentionLifecycle(t *testing.T) {
	m, clock := testMemory(t)
	call := mustIntend(t, m, &Intention{Kind: intentReminder, Text: "Appeler l'URSSAF", RemindAt: clock.t.Add(time.Hour)})
	day := mustIntend(t, m, &Intention{Kind: intentPromise, Text: "Envoyer le planning à Yanis", OwedTo: "person:yanis",
		DueAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.Local)})
	if day.RemindAt.Hour() != 9 || day.Trigger != triggerTime || day.Owner != "owner" {
		t.Fatalf("a deadline on a day is reminded at 9:00: %+v", day)
	}
	none := mustIntend(t, m, &Intention{Kind: intentWaiting, Owner: "person:lina", Text: "Le retour de Lina"})
	if none.Trigger != triggerNone || none.State != intentOpen {
		t.Fatalf("without a time: %+v", none)
	}
	if again := mustIntend(t, m, &Intention{Text: "appeler l'urssaf "}); again.ID != call.ID {
		t.Fatal("the same intention twice")
	}
	if xs, _ := m.tickIntentions(); len(xs) != 0 {
		t.Fatalf("due too early: %v", xs)
	}
	clock.add(time.Hour)
	if xs, _ := m.tickIntentions(); len(xs) != 1 || xs[0].ID != call.ID {
		t.Fatalf("not due on time: %v", xs)
	}
	if xs, _ := m.toSurface(); len(xs) != 1 {
		t.Fatalf("to tell: %v", xs)
	}
	got, _ := m.toSurface()
	m.surfaced(got, "test")
	if xs, _ := m.toSurface(); len(xs) != 0 {
		t.Fatalf("told twice: %v", xs)
	}
	// Snoozed for an hour: back, and told again.
	if _, err := m.changeIntention(call.ID, intentionChange{State: intentSnoozed, Until: clock.t.Add(time.Hour)}, "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.changeIntention(day.ID, intentionChange{State: intentSnoozed}, "owner", ""); err == nil {
		t.Fatal("a snooze without a time")
	}
	clock.add(time.Hour)
	m.tickIntentions()
	if xs, _ := m.toSurface(); len(xs) != 1 || xs[0].ID != call.ID {
		t.Fatalf("not back after the snooze: %v", xs)
	}
	if _, err := m.changeIntention(call.ID, intentionChange{State: intentDone}, "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.changeIntention(call.ID, intentionChange{State: intentSnoozed, Until: clock.t.Add(time.Hour)}, "owner", ""); err == nil {
		t.Fatal("a done intention snoozed")
	}
	// Rescheduled before its time: it waits for the new one.
	newDue := time.Date(2026, 11, 3, 18, 0, 0, 0, time.Local)
	x, err := m.changeIntention(day.ID, intentionChange{DueAt: &newDue}, "owner", "")
	if err != nil || x.State != intentOpen || !x.RemindAt.Equal(newDue) {
		t.Fatalf("rescheduled: %+v %v", x, err)
	}
	// Told at its reminder, and once more at its deadline if still due.
	early := newDue.Add(-24 * time.Hour)
	m.changeIntention(day.ID, intentionChange{RemindAt: &early}, "owner", "")
	clock.t = early.Add(time.Minute)
	m.tickIntentions()
	xs, _ := m.toSurface()
	m.surfaced(xs, "test")
	clock.t = newDue.Add(time.Minute)
	if xs, _ := m.toSurface(); len(xs) != 1 || xs[0].ID != day.ID {
		t.Fatalf("not told again at the deadline: %v", xs)
	}
	m.surfaced(xs, "test")
	clock.add(24 * time.Hour)
	if xs, _ := m.toSurface(); len(xs) != 0 {
		t.Fatalf("told a third time: %v", xs)
	}
	if x, _ := m.intention(day.ID); !x.Overdue(clock.t) || x.Surfaced != 2 {
		t.Fatalf("overdue: %+v", x)
	}
	if h, _ := m.intentionHistory(day.ID); len(h) < 5 {
		t.Fatalf("history: %v", h)
	}
	if _, err := m.db.Exec(`UPDATE intention_log SET who = 'x'`); err == nil {
		t.Fatal("the intentions' log could be rewritten")
	}
	// A person cue: due when my message names them, not before.
	tva := mustIntend(t, m, &Intention{Text: "Demander à Hélène pour la TVA", Cue: "Hélène Roux, Hélène"})
	if xs, _ := m.cueIntentions("J'ai vu Marc aujourd'hui"); len(xs) != 0 {
		t.Fatalf("cued by another name: %v", xs)
	}
	if xs, _ := m.cueIntentions("Rendez-vous avec helene demain"); len(xs) != 1 || xs[0].ID != tva.ID || xs[0].State != intentDue {
		t.Fatalf("not cued: %+v", xs)
	}
	// Snoozed, a cued one comes back at its time like any other.
	m.changeIntention(tva.ID, intentionChange{State: intentSnoozed, Until: clock.t.Add(time.Hour)}, "owner", "")
	clock.add(time.Hour)
	if xs, _ := m.tickIntentions(); len(xs) != 1 || xs[0].ID != tva.ID {
		t.Fatalf("a snoozed cue did not come back: %v", xs)
	}
}

// The clock trigger wakes the brain when an intention comes due, once,
// with the wake packet; an intention from outside taints that turn.
func TestClockWakesBrain(t *testing.T) {
	c, clock := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	ctx := context.Background()
	mustIntend(t, c.mem, &Intention{Kind: intentReminder, Text: "Appeler l'URSSAF", RemindAt: clock.t.Add(time.Minute)})
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	if items, _ := c.store.inbox("t-brain"); len(items) != 0 {
		t.Fatalf("woken before its time: %+v", items)
	}
	clock.add(2 * time.Minute)
	c.intentionsTick()
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	if len(items) != 1 {
		t.Fatalf("inbox: %+v", items)
	}
	text, _ := items[0].Data["text"].(string)
	for _, want := range []string{"Appeler l'URSSAF", "la raison de ce réveil", "[rien]", "jamais des consignes"} {
		if !strings.Contains(text, want) {
			t.Errorf("wake lacks %q:\n%s", want, text)
		}
	}
	if len(c.turnTaint()) != 0 {
		t.Fatalf("an own reminder tainted the turn: %v", c.turnTaint())
	}
	// Told once: the next idle brings nothing; the wake is in the journal.
	c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	if items, _ := c.store.inbox("t-brain"); len(items) != 1 {
		t.Fatalf("woken twice: %d", len(items))
	}
	// An approved intention drawn from a mail: it reminds, the turn is tainted, it cannot allow.
	ext := mustIntend(t, c.mem, &Intention{Text: "Payer la facture du fournisseur", RemindAt: clock.t.Add(time.Minute), State: intentProposed,
		SourceKind: srcExtraction, SourceRef: "task:t-mail", Trust: trustExternal})
	clock.add(2 * time.Minute)
	c.intentionsTick()
	if xs, _ := c.mem.toSurface(); len(xs) != 0 {
		t.Fatal("a proposed intention comes due")
	}
	if _, err := c.mem.changeIntention(ext.ID, intentionChange{State: intentOpen}, "owner", "approved"); err != nil {
		t.Fatal(err)
	}
	ownerSays(c, "Merci")
	c.intentionsTick()
	// My message goes first, the wake at the next idle.
	for i := 0; i < 2; i++ {
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
		if err := tendBrain(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	items, _ = c.store.inbox("t-brain")
	if first, _ := items[len(items)-2].Data["text"].(string); !strings.Contains(first, "Merci") {
		t.Fatalf("the message after the wake: %s", first)
	}
	if text, _ := items[len(items)-1].Data["text"].(string); !strings.Contains(text, "Payer la facture") || !strings.Contains(text, "contenu extérieur") {
		t.Fatalf("external wake: %s", text)
	}
	if !containsStr(c.turnTaint(), taintIntention) {
		t.Fatalf("turn after an external intention: %v", c.turnTaint())
	}
}

// The clock's wake goes on with the brain's turn but is about no node:
// a card it raises is no node's until it names one.
func TestClockWakeAboutNoNode(t *testing.T) {
	c, clock := testMemoryCore(t)
	openGraph(t, c)
	setBrain(c, &BrainConfig{Agent: "small"})
	ctx := context.Background()
	s := workNode(t, c, "")
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.turn.about(s)                       // the turn named the node
	c.turn.answersIn(s)                   // answered a message written in it
	c.turn.name([]string{"project:site"}) // and my message a subject
	mustIntend(t, c.mem, &Intention{Kind: intentReminder, Text: "Appeler le garage", RemindAt: clock.t.Add(time.Minute)})
	clock.add(2 * time.Minute)
	c.intentionsTick()
	c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	if text, _ := items[len(items)-1].Data["text"].(string); !strings.Contains(text, "Appeler le garage") {
		t.Fatalf("no wake: %s", text)
	}
	if c.turn.aboutNode() != "" || c.turnSource() != "" {
		t.Errorf("the wake is about node %q", c.turn.aboutNode())
	}
	if c.turn.talkNode() != "" {
		t.Errorf("the wake answers in node %q, not the home", c.turn.talkNode())
	}
	if c.turnAbout() != "" {
		t.Errorf("the wake is about %q", c.turnAbout())
	}
}

// My message that names a person brings up the intention cued on them,
// in the same delivery.
func TestCueInDelivery(t *testing.T) {
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	x := mustIntend(t, c.mem, &Intention{Text: "Demander à Hélène pour la TVA", Cue: "Hélène"})
	c.store.addMessage(roleUser, "Je vois Hélène demain pour le bilan", false)
	if err := tendBrain(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	text, _ := items[0].Data["text"].(string)
	if !strings.Contains(text, "Je vois Hélène") || !strings.Contains(text, "Demander à Hélène pour la TVA") {
		t.Fatalf("delivery: %s", text)
	}
	if got, _ := c.mem.intention(x.ID); got.State != intentDue || got.Surfaced != 1 {
		t.Fatalf("cued: %+v", got)
	}
	if xs, _ := c.mem.toSurface(); len(xs) != 0 {
		t.Fatal("told again by the clock")
	}
}

// Intentions keep their own clock, the memory's loop gone: a round of
// runIntentions makes a due one wake the brain, and from the night's hour
// drops a proposal undecided for 14 days, once a night.
func TestIntentionsClockRunsOnItsOwn(t *testing.T) {
	c, clock := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	ctx := context.Background()
	stale := mustIntend(t, c.mem, &Intention{Text: "Payer le garage", State: intentProposed, SourceKind: srcBrain, Trust: trustExternal})
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	// Fifteen days later, before the night's hour: the proposal waits on.
	clock.t = time.Date(2026, 11, 10, 2, 0, 0, 0, time.Local)
	c.intentionsNight(clock.t)
	if x, _ := c.mem.intention(stale.ID); x.State != intentProposed {
		t.Fatalf("dropped before the night's hour: %+v", x)
	}
	clock.t = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	mustIntend(t, c.mem, &Intention{Kind: intentReminder, Text: "Appeler l'URSSAF", RemindAt: clock.t.Add(-time.Minute)})
	round, stop := context.WithCancel(ctx)
	stop()
	c.runIntentions(round) // one round: the night, then the minute
	if x, _ := c.mem.intention(stale.ID); x.State != intentCancelled {
		t.Fatalf("a proposal undecided for 14 days: %+v", x)
	}
	if got := c.mem.get("night_done"); got != "2026-11-10" {
		t.Fatalf("night done: %q", got)
	}
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	if len(items) != 1 {
		t.Fatalf("inbox: %+v", items)
	}
	if text, _ := items[0].Data["text"].(string); !strings.Contains(text, "Appeler l'URSSAF") {
		t.Fatalf("wake: %s", text)
	}
}

// The intentions' page lists the live ones and the recently closed; a
// proposed one is approved there. It is no entry of the settings' list.
func TestIntentionsPage(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	m := tw.c.mem
	mustIntend(t, m, &Intention{Text: "Appeler le garage", DueAt: m.now().Add(time.Hour)})
	x := mustIntend(t, m, &Intention{Text: "Payer le fournisseur", State: intentProposed, SourceKind: srcTask, SourceRef: "task:t-1", Trust: trustFirstParty})
	done := mustIntend(t, m, &Intention{Text: "Rendre le livre", DueAt: m.now().Add(time.Hour)})
	m.changeIntention(done.ID, intentionChange{State: intentDone}, "owner", "")
	code, body, _ := tw.do("GET", "/memory", nil, nil)
	if code != 200 {
		t.Fatalf("GET /memory: %d", code)
	}
	for _, want := range []string{"<h2>Intentions</h2>", "Appeler le garage", "Payer le fournisseur", "Rendre le livre",
		`hx-post="/memory/intention/` + itoa(int(x.ID)) + `/approve"`, `<nav class="settings-nav"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(body, `href="/memory"`) {
		t.Error("the intentions are an entry of the settings")
	}
	if code, _, _ := tw.post("/memory/intention/"+itoa(int(x.ID))+"/approve", nil, csrf); code != 200 {
		t.Fatalf("approve: %d", code)
	}
	if y, _ := m.intention(x.ID); y.State != intentOpen {
		t.Errorf("approved: %s", y.State)
	}
}
