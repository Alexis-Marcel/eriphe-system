package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Decisions and progress cards, and what the extraction now gives besides
// the text: keywords, importance, basis and a planned end.
func TestExtractionDurableState(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.mem.now = time.Now
	refs := map[string]string{"msg:1": trustOwner, "msg:2": trustOwner, "task:t1": trustExternal}
	apply := func(js string) (string, string) {
		var op extractionOp
		ops, err := parseExtraction(`{"operations": [` + js + `]}`)
		if err != nil || len(ops) != 1 {
			t.Fatalf("%s: %v", js, err)
		}
		op = ops[0]
		return c.applyOp(op, refs, "test")
	}
	// A decision stated by the user: active, with its keywords and importance.
	if why, res := apply(`{"op": "ADD", "kind": "decision", "subject": "project:portfolio", "text": "Astro plutôt que WordPress, parce que c'est rapide",
		"keywords": "choix framework", "importance": 4, "basis": "stated", "source_ref": "msg:1"}`); res != "added" {
		t.Fatalf("decision: %s %s", res, why)
	}
	d, _ := c.mem.recall(recallQuery{Query: "framework", Kinds: []string{kindDecision}})
	if len(d) != 1 || d[0].Importance != 4 || d[0].Basis != basisStated || d[0].Confidence != 0.9 {
		t.Fatalf("decision: %+v", d)
	}
	// A progress card, then its next state: one card, the old one dated.
	if why, res := apply(`{"op": "ADD", "kind": "progress", "subject": "project:portfolio", "text": "Fait : accueil ; reste : contact",
		"state": "active", "source_ref": "msg:1"}`); res != "added" {
		t.Fatalf("progress: %s %s", res, why)
	}
	cards, _ := c.mem.recall(recallQuery{Kinds: []string{kindProgress}, Subject: "project:portfolio"})
	if len(cards) != 1 || cards[0].State != progressActive {
		t.Fatalf("card: %+v", cards)
	}
	if why, res := apply(fmt.Sprintf(`{"op": "SUPERSEDE", "id": %d, "text": "Fait : accueil ; bloqué : photos", "state": "blocked", "source_ref": "msg:2"}`, cards[0].ID)); res != "superseded" {
		t.Fatalf("progress update: %s %s", res, why)
	}
	cards, _ = c.mem.recall(recallQuery{Kinds: []string{kindProgress}, Subject: "project:portfolio"})
	if len(cards) != 1 || cards[0].State != progressBlocked || cards[0].Kind != kindProgress {
		t.Fatalf("current card: %+v", cards)
	}
	if past, _ := c.mem.recall(recallQuery{Kinds: []string{kindProgress}, IncludePast: true}); len(past) != 2 {
		t.Fatalf("history of the card: %d", len(past))
	}
	// A state the card cannot have.
	if _, res := apply(`{"op": "ADD", "kind": "progress", "subject": "project:x", "text": "y", "state": "open", "source_ref": "msg:1"}`); res != "" {
		t.Fatalf("bad state: %s", res)
	}
	// What the extraction deduced is proposed, even from the user's words.
	if _, res := apply(`{"op": "ADD", "kind": "fact", "subject": "owner", "text": "Sam aime le café fort", "basis": "inferred", "source_ref": "msg:1"}`); res != "proposed" {
		t.Fatalf("inferred: %s", res)
	}
	amb, _ := c.mem.list(`basis = 'ambiguous'`, nil, "", 5)
	if _, res := apply(`{"op": "ADD", "kind": "fact", "subject": "owner", "text": "Sam part peut-être à Lisbonne", "basis": "ambiguous", "source_ref": "msg:1"}`); res != "proposed" {
		t.Fatalf("ambiguous: %s", res)
	}
	if amb, _ = c.mem.list(`basis = 'ambiguous'`, nil, "", 5); len(amb) != 1 || amb[0].Confidence != 0.3 {
		t.Fatalf("ambiguous: %+v", amb)
	}
	if why, res := apply(`{"op": "ADD", "kind": "fact", "text": "x", "basis": "guessed", "source_ref": "msg:1"}`); res != "" || !strings.Contains(why, "basis") {
		t.Fatalf("unknown basis: %s %s", res, why)
	}
	// A planned end: true until then, gone after.
	end := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	if why, res := apply(`{"op": "ADD", "kind": "fact", "subject": "owner", "text": "Sam est en congés", "valid_to": "` + end + `", "source_ref": "msg:1"}`); res != "added" {
		t.Fatalf("planned end: %s %s", res, why)
	}
	if xs, _ := c.mem.recall(recallQuery{Query: "congés"}); len(xs) != 1 {
		t.Fatalf("before its end: %v", ids(xs))
	}
	c.mem.now = func() time.Time { return time.Now().AddDate(0, 0, 11) }
	if xs, _ := c.mem.recall(recallQuery{Query: "congés"}); len(xs) != 0 {
		t.Fatalf("after its end: %v", ids(xs))
	}
	c.mem.now = time.Now
	if _, res := apply(`{"op": "ADD", "kind": "fact", "text": "z", "valid_to": "2020-01-01", "source_ref": "msg:1"}`); res != "" {
		t.Fatalf("an end in the past: %s", res)
	}
	// An external progress card is proposed, like any external fact.
	if _, res := apply(`{"op": "ADD", "kind": "progress", "subject": "project:y", "text": "Fait : tout", "state": "done", "source_ref": "task:t1"}`); res != "proposed" {
		t.Fatalf("external progress: %s", res)
	}
	// A task's receipt: its lesson is only proposed; my words never make one through the extraction.
	if _, res := apply(`{"op": "ADD", "kind": "lesson", "text": "Les tests du dépôt site demandent Node 22", "source_ref": "task:t1"}`); res != "proposed" {
		t.Fatalf("a task's lesson: %s", res)
	}
	if _, res := apply(`{"op": "ADD", "kind": "lesson", "text": "Toujours demander", "source_ref": "msg:1"}`); res != "" {
		t.Fatalf("a lesson from the extraction of my words: %s", res)
	}
	// The transcript gives each segment its date, for relative dates.
	out := renderTranscript([]segment{{Ref: "msg:1", Time: time.Date(2026, 9, 8, 10, 5, 0, 0, time.Local), Who: "owner", Trust: trustOwner, Text: "demain"}})
	if !strings.Contains(out, `time="2026-09-08 10:05 Tue"`) {
		t.Fatalf("segment date: %s", out)
	}
}

// Schema 2 to 3: every memory kept, its basis unknown.
func TestMemoryMigrationBasis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Sam a un vélo cargo", Keywords: "transport"})
	// Back to schema 2, as a database of the previous version would be.
	for _, q := range []string{`ALTER TABLE memories DROP COLUMN basis`, `DROP TABLE intentions`, `DROP TABLE intention_log`} {
		if _, err := m.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	m.db.Exec(`UPDATE meta SET value = '2' WHERE key = 'schema_version'`)
	m.db.Close()
	m, err = openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	xs, err := m.recall(recallQuery{Query: "vélos"})
	if err != nil || len(xs) != 1 || xs[0].Basis != "" || xs[0].Keywords != "transport" {
		t.Fatalf("after the migration: %+v %v", xs, err)
	}
	_ = sql.ErrNoRows
}
