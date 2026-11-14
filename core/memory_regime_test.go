package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A proposal nobody decided is dropped after 14 days: a memory forgotten,
// an intention cancelled; a younger one waits on.
func TestProposalsExpire(t *testing.T) {
	m, clock := testMemory(t)
	old := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	oldIntent := mustIntend(t, m, &Intention{Text: "Payer le garage", State: intentProposed, SourceKind: srcExtraction, Trust: trustExternal})
	clock.add(10 * 24 * time.Hour)
	young := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le garage ferme lundi", Status: memProposed, SourceKind: srcExtraction})
	kept := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Sam habite à Lyon"})
	clock.add(5 * 24 * time.Hour)
	n, err := m.expireProposals()
	if err != nil || n != 2 {
		t.Fatalf("expired %d: %v", n, err)
	}
	if x, _ := m.memory(old.ID); x.Status != memDeleted {
		t.Fatalf("an old proposal: %+v", x)
	}
	if x, _ := m.intention(oldIntent.ID); x.State != intentCancelled {
		t.Fatalf("an old proposed intention: %+v", x)
	}
	for _, id := range []int64{young.ID, kept.ID} {
		if x, _ := m.memory(id); x.Status == memDeleted {
			t.Fatalf("dropped too soon: %+v", x)
		}
	}
	if n, _ := m.expireProposals(); n != 0 {
		t.Fatalf("expired again: %d", n)
	}
}

// A memory.db at schema 6 is brought to 7: the columns nothing read are
// gone, the tasks' episodes forgotten, the journal and the rest kept.
func TestMemorySchema6To7(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE meta SET value = '6' WHERE key = 'schema_version'`,
		`ALTER TABLE memories ADD COLUMN embedding BLOB`,
		`ALTER TABLE memories ADD COLUMN confidence REAL NOT NULL DEFAULT 1`,
	} {
		if _, err := m.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	task := mustAdd(t, m, &Memory{Kind: kindEpisode, Subject: "project:site", Text: "La tâche a préparé le devis", SourceKind: srcExtraction,
		SourceRef: "task:t-1", Trust: trustExternal})
	run := mustAdd(t, m, &Memory{Kind: kindEpisode, Text: "La revue a tourné", SourceKind: srcTask, SourceRef: "task:t-2", Trust: trustFirstParty})
	journal := mustAdd(t, m, &Memory{Kind: kindEpisode, Subject: journalSubject, Text: "Sam a parlé du site", SourceKind: srcExtraction,
		SourceRef: "msg:1", Trust: trustFirstParty})
	fact := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le devis est dans devis.md", SourceKind: srcExtraction, SourceRef: "task:t-1"})
	m.db.Close()
	if m, err = openMemory(path); err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	if v := m.schemaVersion(); v != memorySchemaVersion {
		t.Fatalf("schema %d", v)
	}
	var cols int
	m.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name IN ('embedding', 'confidence')`).Scan(&cols)
	if cols != 0 {
		t.Fatalf("%d dead columns left", cols)
	}
	for id, want := range map[int64]string{task.ID: memDeleted, run.ID: memDeleted, journal.ID: memActive, fact.ID: memActive} {
		if x, _ := m.memory(id); x.Status != want {
			t.Errorf("memory %d (%s): %s, want %s", id, x.Text, x.Status, want)
		}
	}
}

// The extraction's proposals have no card: « Ce qu'Eriphe a appris » says
// how many wait in À valider, and that they expire.
func TestLearnedPageSaysWhatWaits(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	mustAdd(t, tw.c.mem, &Memory{Kind: kindFact, Text: "Une rumeur", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	_, body, _ := tw.do("GET", "/settings/learned", nil, nil)
	if !strings.Contains(body, "1 souvenir(s) proposé(s)") || !strings.Contains(body, `href="/settings/approve"`) {
		t.Fatalf("page:\n%s", body)
	}
}
