package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Decisions and progress, and what the extraction now gives besides
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
	if len(d) != 1 || d[0].Importance != 4 || d[0].Basis != basisStated {
		t.Fatalf("decision: %+v", d)
	}
	// A progress is its subject's thread summary: a light thread opened
	// for it, then written over; no memory.
	if why, res := apply(`{"op": "ADD", "kind": "progress", "subject": "project:portfolio", "text": "Fait : accueil ; reste : contact",
		"state": "active", "source_ref": "msg:1"}`); res != "added" {
		t.Fatalf("progress: %s %s", res, why)
	}
	id, ok := c.liveThreadOf("project:portfolio")
	if th, _ := c.thread(id); !ok || th.Mode != modeLight || th.Summary != "Fait : accueil ; reste : contact" {
		t.Fatalf("thread: %+v", th)
	}
	if why, res := apply(`{"op": "ADD", "kind": "progress", "subject": "project:portfolio", "text": "Fait : accueil ; bloqué : photos", "state": "blocked", "source_ref": "msg:2"}`); res != "added" {
		t.Fatalf("progress update: %s %s", res, why)
	}
	if th, _ := c.thread(id); th.Summary != "Bloqué. Fait : accueil ; bloqué : photos" {
		t.Fatalf("summary: %q", th.Summary)
	}
	if n := c.mem.count(`kind = 'progress'`); n != 0 {
		t.Fatalf("%d progress memories", n)
	}
	// A state it cannot have.
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
	if amb, _ = c.mem.list(`basis = 'ambiguous'`, nil, "", 5); len(amb) != 1 || amb[0].Basis != basisAmbiguous {
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
	// An external progress is not kept: a summary is quoted in every card.
	if why, res := apply(`{"op": "ADD", "kind": "progress", "subject": "project:y", "text": "Fait : tout", "state": "done", "source_ref": "task:t1"}`); res != "" || !strings.Contains(why, "external") {
		t.Fatalf("external progress: %s %s", res, why)
	}
	if _, ok := c.liveThreadOf("project:y"); ok {
		t.Fatal("an external progress opened a thread")
	}
	// Never a lesson nor a preference, from a task or from my words: only
	// the brain, on my own message, writes one.
	for _, js := range []string{
		`{"op": "ADD", "kind": "lesson", "text": "Les tests du dépôt site demandent Node 22", "source_ref": "task:t1"}`,
		`{"op": "ADD", "kind": "lesson", "text": "Toujours demander", "source_ref": "msg:1"}`,
		`{"op": "ADD", "kind": "preference", "subject": "topic:workspace", "text": "Sam veut la météo devant", "source_ref": "msg:1"}`,
	} {
		if why, res := apply(js); res != "" || !strings.Contains(why, "only the user's own words") {
			t.Fatalf("%s: %s %s", js, res, why)
		}
	}
	// One episode a day, the journal; never one for a task.
	if why, res := apply(`{"op": "ADD", "kind": "episode", "subject": "project:site", "text": "La tâche a fini", "source_ref": "msg:1"}`); res != "" || !strings.Contains(why, "journal") {
		t.Fatalf("a task's episode: %s %s", res, why)
	}
	if why, res := apply(`{"op": "ADD", "kind": "episode", "subject": "topic:journal", "text": "Sam a parlé du site", "source_ref": "msg:1"}`); res != "added" {
		t.Fatalf("the journal: %s %s", res, why)
	}
	// The day's second run completes it, the subject kept from the old one.
	journal, _ := c.mem.list(`kind = 'episode' AND subject = ? AND status = 'active'`, []any{journalSubject}, "", 1)
	if why, res := apply(fmt.Sprintf(`{"op": "SUPERSEDE", "id": %d, "text": "Sam a parlé du site, puis du devis", "source_ref": "msg:2"}`, journal[0].ID)); res != "superseded" {
		t.Fatalf("the journal completed: %s %s", res, why)
	}
	wake := mustAdd(t, c.mem, &Memory{Kind: kindEpisode, Subject: heartbeatSubject, Text: "Rien à faire.", SourceKind: srcBrain, Trust: trustFirstParty})
	if why, res := apply(fmt.Sprintf(`{"op": "SUPERSEDE", "id": %d, "text": "Autre chose", "source_ref": "msg:2"}`, wake.ID)); res != "" || !strings.Contains(why, "journal") {
		t.Fatalf("a wake replaced: %s %s", res, why)
	}
	// The transcript gives each segment its date, for relative dates.
	out := renderTranscript([]segment{{Ref: "msg:1", Time: time.Date(2026, 9, 8, 10, 5, 0, 0, time.Local), Who: "owner", Trust: trustOwner, Text: "demain"}})
	if !strings.Contains(out, `time="2026-09-08 10:05 Tue"`) {
		t.Fatalf("segment date: %s", out)
	}
}
