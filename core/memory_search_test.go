package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueryTime(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.Local) // a Monday
	d := func(s string) time.Time { x, _ := time.ParseInLocation("2006-01-02", s, time.Local); return x }
	for _, c := range []struct {
		q, from, to, rest string
	}{
		{"Que s'est-il passé hier ?", "2026-10-11", "2026-10-12", "que s est-il passe ?"},
		{"qu'a fait Camille avant-hier", "2026-10-10", "2026-10-11", ""},
		{"Qu'a fait Camille la semaine dernière ?", "2026-10-05", "2026-10-12", ""},
		{"Où vivait-elle début septembre ?", "2026-09-01", "2026-09-12", ""},
		{"fin septembre", "2026-09-20", "2026-10-01", ""},
		{"Le chiffre du mois dernier", "2026-09-01", "2026-10-01", ""},
		{"le 19 septembre", "2026-09-17", "2026-09-22", ""},
		{"le 1er octobre 2025", "2025-09-29", "2025-10-04", ""},
		{"what did we do on May 3?", "2026-05-01", "2026-05-06", ""}, // the nearest May 3
		{"il y a trois jours", "2026-10-08", "2026-10-11", ""},
		{"2026-09-24", "2026-09-22", "2026-09-27", ""},
	} {
		r, rest, ok := queryTime(c.q, now)
		if !ok {
			t.Errorf("%q: no time read", c.q)
			continue
		}
		if !r.From.Equal(d(c.from)) || !r.To.Equal(d(c.to)) {
			t.Errorf("%q: %s – %s, want %s – %s", c.q, r.From.Format("2006-01-02"), r.To.Format("2006-01-02"), c.from, c.to)
		}
		if c.rest != "" && rest != c.rest {
			t.Errorf("%q: rest %q", c.q, rest)
		}
	}
	for _, q := range []string{"Quel est le tarif de Studio Vega ?", "mon rendez-vous de mai", "le 45 septembre", "sept clients"} {
		if r, _, ok := queryTime(q, now); ok {
			t.Errorf("%q read as %v – %v", q, r.From, r.To)
		}
	}
}

func TestStems(t *testing.T) {
	for _, pair := range [][2]string{{"anniversaire", "anniversaires"}, {"grimper", "grimpe"}, {"déménagé", "déménagement"}, {"factures", "facturé"}} {
		if a, b := stemWord(pair[0]), stemWord(pair[1]); a != b {
			t.Errorf("%s → %s, %s → %s", pair[0], a, pair[1], b)
		}
	}
	if q := ftsQuery("Quel est le nom de son chien ?"); strings.Contains(q, `"quel"`) || strings.Contains(q, `"le"`) || !strings.Contains(q, `stems : "chien"`) {
		t.Errorf("query %q", q)
	}
	if q := ftsQuery("Où est Camille ?", "camille"); q != "" {
		t.Errorf("the owner's name is looked for: %q", q)
	}
}

// Word families, keywords and importance in recall.
func TestRecallStemsAndKeywords(t *testing.T) {
	m, _ := testMemory(t)
	anniv := mustAdd(t, m, &Memory{Kind: kindFact, Subject: "person:noe", Text: "L'anniversaire de Noé est le 14 octobre"})
	velo := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Sam se déplace en vélo cargo", Keywords: "voiture bagnole transport"})
	low := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le café du coin ouvre à 7 h", Importance: 1})
	high := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le café est interdit par le médecin", Importance: 5})
	for q, want := range map[string]int64{"anniversaires": anniv.ID, "quelle bagnole ?": velo.ID, "café": high.ID} {
		xs, err := m.recall(recallQuery{Query: q})
		if err != nil || len(xs) == 0 || xs[0].ID != want {
			t.Errorf("%q: %v %v, want %d", q, ids(xs), err, want)
		}
	}
	_ = low
	if _, err := m.update(velo.ID, memoryPatch{Keywords: ptr("auto")}, "test", ""); err != nil {
		t.Fatal(err)
	}
	if xs, _ := m.recall(recallQuery{Query: "bagnole"}); len(xs) != 0 {
		t.Errorf("old keywords still indexed: %v", ids(xs))
	}
	if xs, _ := m.recall(recallQuery{Query: "auto"}); len(xs) != 1 {
		t.Errorf("new keywords not indexed: %v", ids(xs))
	}
	if err := m.add(&Memory{Kind: kindFact, Text: "x", Importance: 9, SourceKind: srcOwner, Trust: trustOwner}, "test"); err == nil {
		t.Error("importance 9 accepted")
	}
}

// A question about another time: what was true then comes first.
func TestRecallTimeInQuestion(t *testing.T) {
	m, clock := testMemory(t)
	clock.t = time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local)
	lyon := mustAdd(t, m, &Memory{Kind: kindFact, Subject: "owner", Text: "Sam habite à Lyon"})
	clock.t = time.Date(2026, 9, 19, 10, 0, 0, 0, time.Local)
	mustAdd(t, m, &Memory{Kind: kindFact, Subject: "owner", Text: "Sam habite à Nantes", Supersedes: lyon.ID})
	ep := mustAdd(t, m, &Memory{Kind: kindEpisode, Text: "Déménagement avec un camion loué"})
	clock.t = time.Date(2026, 10, 12, 10, 0, 0, 0, time.Local)
	xs, _ := m.recall(recallQuery{Query: "Où habitait Sam début septembre ?"})
	if len(xs) == 0 || xs[0].ID != lyon.ID {
		t.Errorf("début septembre: %v", ids(xs))
	}
	if xs, _ = m.recall(recallQuery{Query: "Où habite Sam ?"}); len(xs) != 1 || xs[0].ID == lyon.ID {
		t.Errorf("now: %v", ids(xs))
	}
	if xs, _ = m.recall(recallQuery{Query: "Que s'est-il passé le 19 septembre ?"}); len(xs) == 0 || xs[0].ID != ep.ID {
		t.Errorf("19 septembre: %v", ids(xs))
	}
}

// The memory.db of the previous version (schema 1) is migrated in place:
// every memory and its history kept, the index rebuilt with the stems.
func TestMemoryMigrationFromSchema1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`, memoryMigrations[0].sql,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '1')`,
		`INSERT INTO memories (kind, subject, text, links, valid_from, recorded_at, source_kind, trust, status, pinned, updated_at)
		 VALUES ('profile', 'owner', 'Sam est graphiste', '[]', 1, 1, 'owner', 'owner', 'active', 1, 1),
		        ('fact', 'person:noe', 'Les anniversaires de Noé se fêtent en octobre', '[]', 1, 1, 'extraction', 'owner', 'active', 0, 1),
		        ('episode', 'task', 'Résumé des mails', '[]', 1, 1, 'extraction', 'external', 'proposed', 0, 1)`,
		`INSERT INTO memory_log (memory_id, time, who, op, reason) VALUES (2, 1, 'brain', 'add', '')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v: %s", err, q)
		}
	}
	db.Close()
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	if v := m.schemaVersion(); v != len(memoryMigrations) {
		t.Fatalf("schema %d", v)
	}
	if n := m.count(`1 = 1`); n != 3 {
		t.Fatalf("%d memories after the migration", n)
	}
	x, _ := m.memory(2)
	if x.Importance != 3 || x.Keywords != "" || x.Text != "Les anniversaires de Noé se fêtent en octobre" {
		t.Fatalf("migrated memory: %+v", x)
	}
	if h, _ := m.history(2); len(h) != 1 {
		t.Fatalf("history: %+v", h)
	}
	if xs, _ := m.recall(recallQuery{Query: "anniversaire"}); len(xs) != 1 || xs[0].ID != 2 {
		t.Fatalf("stems of an old memory: %v", ids(xs))
	}
	// The index follows the changes made after.
	if _, err := m.update(2, memoryPatch{Text: ptr("Noé aime le vélo")}, "test", ""); err != nil {
		t.Fatal(err)
	}
	if xs, _ := m.recall(recallQuery{Query: "anniversaire"}); len(xs) != 0 {
		t.Fatalf("old text still indexed: %v", ids(xs))
	}
	if xs, _ := m.recall(recallQuery{Query: "vélos"}); len(xs) != 1 {
		t.Fatalf("new text not indexed: %v", ids(xs))
	}
	var integrity string
	m.db.QueryRow(`INSERT INTO memories_fts (memories_fts) VALUES ('integrity-check')`).Scan(&integrity)
	if _, err := m.db.Exec(`INSERT INTO memories_fts (memories_fts) VALUES ('integrity-check')`); err != nil {
		t.Fatalf("index integrity: %v", err)
	}
}
