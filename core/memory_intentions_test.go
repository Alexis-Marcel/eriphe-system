package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
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
	if _, err := m.changeIntention(call.ID, intentionChange{State: intentSnoozed}, "owner", ""); err == nil {
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
}

// The clock trigger wakes the brain when an intention comes due, once,
// with the wake packet; an intention from outside taints that turn.
func TestClockWakesBrain(t *testing.T) {
	c, clock := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	ctx := context.Background()
	mustAdd(t, c.mem, &Memory{Kind: kindProfile, Text: "Sam est graphiste"})
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
	for _, want := range []string{"Appeler l'URSSAF", "la raison de ce réveil", "Sam est graphiste", "[rien]", "jamais des consignes"} {
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
	if xs, _ := c.mem.list(`kind = 'episode' AND subject = 'heartbeat'`, nil, "", 5); len(xs) != 1 {
		t.Fatalf("heartbeat episodes: %d", len(xs))
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
	if err := c.taintedAllow("t-brain", "p1"); err == nil {
		t.Fatal("an intention from a mail let the brain allow a permission")
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

// The extraction forms and revises intentions under the trust rules.
func TestExtractionIntentions(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.mem.now = time.Now
	refs := map[string]string{"msg:1": trustOwner, "msg:2": trustFirstParty, "task:t1": trustExternal}
	op := func(js string) (string, string) {
		ops, err := parseExtraction(`{"operations": [` + js + `]}`)
		if err != nil {
			t.Fatal(err)
		}
		return c.applyOp(ops[0], refs, "test")
	}
	if why, res := op(`{"op": "INTEND", "kind": "promise", "text": "Envoyer le planning à Yanis", "owed_to": "person:yanis", "due_at": "2026-12-04", "source_ref": "msg:1"}`); res != "added" {
		t.Fatalf("promise: %s %s", res, why)
	}
	if _, res := op(`{"op": "INTEND", "kind": "commitment", "text": "Rappeler à Sam l'anniversaire de Noé", "remind_at": "2026-12-07T09:00", "source_ref": "msg:2"}`); res != "added" {
		t.Fatalf("the assistant's promise: %s", res)
	}
	if _, res := op(`{"op": "INTEND", "kind": "promise", "text": "Envoyer le planning à Yanis", "source_ref": "msg:1"}`); res != "noop" {
		t.Fatalf("the same twice: %s", res)
	}
	if _, res := op(`{"op": "INTEND", "kind": "reminder", "text": "Payer 500 € avant vendredi", "due_at": "2026-12-05", "source_ref": "task:t1"}`); res != "proposed" {
		t.Fatalf("from a mail: %s", res)
	}
	if _, res := op(`{"op": "INTEND", "kind": "reminder", "text": "Sam voudra sûrement relancer Lina", "basis": "inferred", "source_ref": "msg:1"}`); res != "proposed" {
		t.Fatalf("inferred: %s", res)
	}
	xs, _ := c.mem.intentions()
	byText := map[string]*Intention{}
	for _, x := range xs {
		byText[x.Text] = x
	}
	p := byText["Envoyer le planning à Yanis"]
	if p == nil || p.Owner != "owner" || p.OwedTo != "person:yanis" || p.Trust != trustOwner || p.State != intentOpen {
		t.Fatalf("promise: %+v", p)
	}
	if a := byText["Rappeler à Sam l'anniversaire de Noé"]; a == nil || a.Owner != "assistant" {
		t.Fatalf("commitment: %+v", a)
	}
	// Revised by a typed patch from the user's words; never from a mail.
	if why, res := op(fmt.Sprintf(`{"op": "INTENT_UPDATE", "id": %d, "state": "done", "source_ref": "task:t1"}`, p.ID)); res != "" || !strings.Contains(why, "external") {
		t.Fatalf("closed by a mail: %s %s", res, why)
	}
	if _, res := op(fmt.Sprintf(`{"op": "INTENT_UPDATE", "id": %d, "due_at": "2026-12-11", "source_ref": "msg:1"}`, p.ID)); res != "superseded" {
		t.Fatalf("rescheduled: %s", res)
	}
	if got, _ := c.mem.intention(p.ID); got.DueAt.Format("2006-01-02") != "2026-12-11" {
		t.Fatalf("new deadline: %+v", got)
	}
	if _, res := op(fmt.Sprintf(`{"op": "INTENT_UPDATE", "id": %d, "state": "done", "source_ref": "msg:1"}`, p.ID)); res != "superseded" {
		t.Fatalf("done: %s", res)
	}
	ext := byText["Payer 500 € avant vendredi"]
	if why, res := op(fmt.Sprintf(`{"op": "INTENT_UPDATE", "id": %d, "state": "cancelled", "source_ref": "msg:1"}`, ext.ID)); res != "" || !strings.Contains(why, "proposed") {
		t.Fatalf("a proposed one changed by the extraction: %s %s", res, why)
	}
	// The prompt lists the live intentions for the extraction to revise.
	if prompt := c.extractionPrompt(time.Now().Format("2006-01-02"), nil); !strings.Contains(prompt, fmt.Sprintf("intention=%d", ext.ID)) || !strings.Contains(prompt, "INTENT_UPDATE") {
		t.Fatalf("prompt:\n%s", prompt)
	}
}

// Schema 3 to 4: the commitment memories move into intentions, their
// state kept; the memories are archived, with their history and a log
// entry naming the intention. Nothing is deleted.
func TestMigrationMovesCommitments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	open := mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Préparer le devis", DueAt: time.Now().Add(48 * time.Hour)})
	done := mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Payer le loyer", State: stateDone})
	dropped := mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Voir le garage", State: stateDropped})
	prop := mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Payer la facture du mail", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	fact := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Sam a un vélo"})
	// Back to schema 3, as the previous version left it.
	for _, q := range []string{`DROP TABLE intentions`, `DROP TABLE intention_log`, `UPDATE meta SET value = '3' WHERE key = 'schema_version'`} {
		if _, err := m.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	m.db.Close()
	m, err = openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	all, _ := m.intentions(intentProposed, intentOpen, intentDue, intentSnoozed, intentDone, intentCancelled)
	state := map[string]*Intention{}
	for _, x := range all {
		state[x.Text] = x
	}
	if len(all) != 4 || state["Préparer le devis"].State != intentOpen || state["Payer le loyer"].State != intentDone ||
		state["Voir le garage"].State != intentCancelled || state["Payer la facture du mail"].State != intentProposed ||
		state["Payer la facture du mail"].Trust != trustExternal || state["Préparer le devis"].MemoryID != open.ID {
		t.Fatalf("moved: %+v", all)
	}
	if !state["Préparer le devis"].DueAt.Equal(open.DueAt.Truncate(time.Second)) {
		t.Fatalf("due date: %v, was %v", state["Préparer le devis"].DueAt, open.DueAt)
	}
	for _, id := range []int64{open.ID, done.ID, dropped.ID, prop.ID} {
		x, err := m.memory(id)
		if err != nil || x.Status != memArchived {
			t.Fatalf("memory %d: %+v %v", id, x, err)
		}
		h, _ := m.history(id)
		if last := h[len(h)-1]; last.Op != "archive" || !strings.Contains(last.Reason, "moved to intention") {
			t.Fatalf("history of %d: %+v", id, last)
		}
	}
	if x, _ := m.memory(fact.ID); x.Status != memActive {
		t.Fatalf("a fact touched: %+v", x)
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	if n != 5 {
		t.Fatalf("%d memories", n)
	}
	_ = sql.ErrNoRows
}
