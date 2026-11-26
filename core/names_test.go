package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// names are the rows of the names index, name → target.
func names(t *testing.T, c *core) map[string]string {
	t.Helper()
	rows, err := c.store.db.Query(`SELECT name, target FROM names`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, target string
		rows.Scan(&n, &target)
		out[n] = target
	}
	return out
}

// Every key written is noted in the names, once; what is written about a
// key after is about it; a record's ref is not an about.
func TestAboutNotedWhenWritten(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	seq := ownerSays(c, "Le site web passe en ligne vendredi, préviens-moi")
	if _, err := c.brainRemember(rememberIn{Text: "Le site web passe en ligne vendredi.", Kind: kindFact, About: "Projet : Site Web", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainIntend(intendIn{Text: "Relire la page d'accueil.", About: "topic:accueil", OwedTo: "Personne:Sam", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainRule(ruleIn{Match: "alerte:backup", Floor: 5, Why: "Demandé.", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainNotify(notifyIn{Text: "Fini.", Weight: 2, Why: "Fini.", Ref: "task:t-1"}); err != nil {
		t.Fatal(err)
	}
	got := names(t, c)
	want := map[string]string{"alert:backup": "alert:backup", "owner": "owner", "person:sam": "person:sam",
		"project:site-web": "project:site-web", "topic:accueil": "topic:accueil"}
	if len(got) != len(want) {
		t.Fatalf("names %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s → %q, want %q", k, got[k], v)
		}
	}
	var about string
	c.mem.db.QueryRow(`SELECT about FROM memories WHERE kind = 'fact'`).Scan(&about)
	if about != "project:site-web" {
		t.Errorf("a memory about the key is about %q", about)
	}
	// A mail or an event is a page's reference, never an about.
	if _, err := aboutKey("mail:abc"); err == nil || !strings.Contains(err.Error(), "[m1]: mail:<id>") {
		t.Errorf("mail:abc: %v", err)
	}
}

// A message names entities by the words of their keys; never by a word
// too short, an id, or Eriphe's own topics; a name inside a longer one
// named too gives way.
func TestNamed(t *testing.T) {
	c, _ := testMemoryCore(t)
	for _, k := range []string{"project:site", "project:site-web", "person:sam", "topic:facture", "project:x", "topic:workspace"} {
		c.store.noteAbout(k)
	}
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"Où en est le site web ?", []string{"project:site-web"}},
		{"Le site est lent", []string{"project:site"}},
		{"Dis à Sam que les factures sont payées", []string{"person:sam", "topic:facture"}},
		{"La case x, l'espace workspace", nil},
		{"Rien à voir", nil},
	} {
		got := c.named(tc.text)
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q: %v, want %v", tc.text, got, tc.want)
		}
	}
}

// What my message names goes with it to the brain, keys only; the turn
// is about it until the next delivery.
func TestNamedInDelivery(t *testing.T) {
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	c.store.noteAbout("project:site-web")
	c.store.addMessage(roleUser, "Où en est le site web ?", false)
	if err := tendBrain(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	text, _ := items[len(items)-1].Data["text"].(string)
	if !strings.Contains(text, "[nommés] Ce message nomme : project:site-web.") {
		t.Fatalf("delivery: %s", text)
	}
	if got := c.turn.named(); !slices.Equal(got, []string{"project:site-web"}) {
		t.Errorf("turn named %v", got)
	}
	if got := c.turnAbout(); got != "project:site-web" {
		t.Errorf("turn about %q", got)
	}
	var st brainTurnState
	json.Unmarshal([]byte(c.store.get("brain_turn")), &st)
	if !slices.Equal(st.Named, []string{"project:site-web"}) {
		t.Errorf("kept %+v", st)
	}
	c.turn.start(0, 0, 0)
	if len(c.turn.named()) != 0 {
		t.Error("a new turn kept what the last one named")
	}
}

// A person's cue matches the same words as an entity's name: plural and
// accents aside.
func TestCueMatchesLikeNames(t *testing.T) {
	c, _ := testMemoryCore(t)
	mustIntend(t, c.mem, &Intention{Text: "Rendre les clés", Cue: "Hélène Dupont"})
	xs, err := c.mem.cueIntentions("J'ai vu helene dupont hier")
	if err != nil || len(xs) != 1 {
		t.Fatalf("cue: %+v %v", xs, err)
	}
}

// memory.db at schema 7 says subject: opened, it is at 8, about, its
// values kept and found by full text.
func TestMemoryMigratesToAbout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	v7 := func(s string) string { return strings.ReplaceAll(s, "about", "subject") }
	for _, q := range []string{`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`, v7(memorySchema), v7(intentionsSchema), learningSchema,
		`INSERT INTO meta VALUES ('schema_version', '7')`,
		`INSERT INTO memories (kind, subject, text, valid_from, recorded_at, source_kind, trust, status, updated_at)
			VALUES ('fact', 'project:site', 'Le site est hébergé sur le NAS', 1, 1, 'user', 'first_party', 'active', 1)`,
		`INSERT INTO intentions (kind, text, subject, state, source_kind, trust, created_at, updated_at)
			VALUES ('todo', 'Relire', 'project:site', 'open', 'user', 'first_party', 1, 1)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	if v := m.schemaVersion(); v != memorySchemaVersion {
		t.Fatalf("schema %d", v)
	}
	var about, intent string
	m.db.QueryRow(`SELECT about FROM memories`).Scan(&about)
	m.db.QueryRow(`SELECT about FROM intentions`).Scan(&intent)
	if about != "project:site" || intent != "project:site" {
		t.Errorf("about %q, intention %q", about, intent)
	}
	if xs, err := m.recall(recallQuery{Query: "hébergé NAS"}); err != nil || len(xs) != 1 {
		t.Errorf("full text: %+v %v", xs, err)
	}
	if xs, err := m.recall(recallQuery{About: "project:site"}); err != nil || len(xs) != 1 {
		t.Errorf("about: %+v %v", xs, err)
	}
}

// core.db's tables made before say subject: opened, they say about.
func TestStoreRenamesSubject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(strings.Replace(cardPushesSchema, "about   TEXT NOT NULL DEFAULT ''", "subject TEXT NOT NULL", 1)); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO card_pushes (at, subject, place, chars) VALUES (1, 'project:site', 'turn', 10)`)
	db.Close()
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var about string
	if err := s.db.QueryRow(`SELECT about FROM card_pushes`).Scan(&about); err != nil || about != "project:site" {
		t.Errorf("about %q %v", about, err)
	}
	if _, err := openStore(path); err != nil {
		t.Errorf("opened again: %v", err)
	}
}
