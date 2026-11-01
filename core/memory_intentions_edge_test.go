package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Edges: a day's deadline is not late at its morning reminder; telling the
// brain never undoes a snooze made meanwhile; a text change keeps a snooze;
// a told external intention is not quoted again; migrated late ones wait.
func TestIntentionEdges(t *testing.T) {
	m, clock := testMemory(t)
	clock.t = time.Date(2026, 11, 5, 8, 0, 0, 0, time.Local)
	day := mustIntend(t, m, &Intention{Text: "Déclarer l'URSSAF", DueAt: time.Date(2026, 11, 5, 0, 0, 0, 0, time.Local)})
	clock.t = clock.t.Add(time.Hour + time.Minute)
	m.tickIntentions()
	x, _ := m.intention(day.ID)
	if x.State != intentDue || x.Overdue(clock.t) || x.DueAt.Hour() != 23 {
		t.Fatalf("a day's deadline at 9:01: %+v overdue %v", x, x.Overdue(clock.t))
	}
	due, _ := m.toSurface()
	// Snoozed between the look and the telling: the snooze stands.
	if _, err := m.changeIntention(day.ID, intentionChange{State: intentSnoozed, Until: clock.t.Add(time.Hour)}, "owner", ""); err != nil {
		t.Fatal(err)
	}
	m.surfaced(due, "test")
	if x, _ = m.intention(day.ID); x.State != intentSnoozed || x.NextCheckAt.IsZero() || x.Surfaced != 0 {
		t.Fatalf("the snooze was undone: %+v", x)
	}
	// A new text keeps the snooze.
	if x, err := m.changeIntention(day.ID, intentionChange{Text: ptr("Déclarer le CA à l'URSSAF")}, "owner", ""); err != nil || x.State != intentSnoozed {
		t.Fatalf("text change: %+v %v", x, err)
	}
	// Changed meanwhile, becomeDue reports nothing.
	m.changeIntention(day.ID, intentionChange{State: intentDone}, "owner", "")
	if got, err := m.becomeDue(day.ID, "clock", ""); got != nil || err != nil {
		t.Fatalf("becomeDue on a closed one: %+v %v", got, err)
	}
	// Told once, an external intention is named, not quoted, in later packets.
	ext := mustIntend(t, m, &Intention{Text: "SECRET Payer le fournisseur", RemindAt: clock.t.Add(-time.Minute), SourceKind: srcExtraction,
		Trust: trustExternal})
	m.tickIntentions()
	p, _ := m.wakePacket()
	m.surfaced(p.Due, "test")
	if p, _ = m.wakePacket(); len(p.Pending) != 1 || p.Pending[0].ID != ext.ID || strings.Contains(p.render("Sam"), "SECRET") {
		t.Fatalf("pending external quoted:\n%s", p.render("Sam"))
	}
}

// The upgrade does not wake the brain for every old late commitment.
func TestMigrationLateCommitments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Rendre le livre", DueAt: time.Now().AddDate(0, 0, -10)})
	for _, q := range []string{`DROP TABLE intentions`, `DROP TABLE intention_log`, `UPDATE meta SET value = '3' WHERE key = 'schema_version'`} {
		if _, err := m.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	m.db.Close()
	if m, err = openMemory(path); err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	xs, _ := m.intentions(intentDue)
	if len(xs) != 1 || !xs[0].Overdue(time.Now()) {
		t.Fatalf("late commitment: %+v", xs)
	}
	if again, _ := m.toSurface(); len(again) != 0 {
		t.Fatalf("a wake for an old late commitment: %v", again)
	}
}
