package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// One key per subject: case, accents, spaces, French and plural kind
// names, a topic's plural, unknown kinds and bare words as topics; a
// pull request, a mail's and an event's ids; owner and home alone.
func TestSubjectKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"  ":                      "",
		"project:getout":          "project:getout",
		" Project:Getout ":        "project:getout",
		"projet:Getout":           "project:getout",
		"Projet : Site Web":       "project:site-web",
		"Projet : Getout : Prod":  "project:getout:prod",
		"project:getout:releases": "project:getout:releases",
		"person:Léa Martin":       "person:lea-martin",
		"personne:jules":          "person:jules",
		"Personne: Hélène":        "person:helene",
		"client:Example Org":      "client:example-org",
		"pr:owner/repo#12":        "pr:owner/repo#12",
		" PR:Example/Infra#12 ":   "pr:example/infra#12",
		"pull-request:a/b#3":      "pr:a/b#3",
		"mail:INBOX/42":           "mail:INBOX/42",
		"mail:<ab.C@example.org>": "mail:<ab.C@example.org>",
		"event:ev-1@example.org":  "event:ev-1@example.org",
		"alerte:sauvegarde":       "alert:sauvegarde",
		"alert:7":                 "alert:7",
		"owner":                   "owner",
		"Owner":                   "owner",
		"owner:sam":               "owner",
		"home":                    "home",
		"maison:Salon":            "home:salon",
		"outil:Mail":              "tool:mail",
		"Factures":                "topic:facture",
		"facture":                 "topic:facture",
		"topic:factures":          "topic:facture",
		"sujet:Factures":          "topic:facture",
		"invoices":                "topic:invoice",
		"devis":                   "topic:devis",
		"colis":                   "topic:colis",
		"status":                  "topic:status",
		"notes de frais":          "topic:notes-de-frais",
		"workspace":               "topic:workspace",
		"journal":                 "topic:journal",
		"brand:velora":            "topic:brand-velora",
		"source:mail":             "topic:source-mail",
		"veille:x":                "topic:veille-x",
	} {
		got, err := subjectKey(in)
		if err != nil || got != want {
			t.Errorf("subjectKey(%q) = %q %v, want %q", in, got, err, want)
		}
		if again, err := subjectKey(got); err != nil || again != got {
			t.Errorf("subjectKey not idempotent on %q: %q %v", got, again, err)
		}
	}
	for _, bad := range []string{"Projet X !", "task:t-12", "intention:5", "card:12", "thread:3", "skill:veille", "pr:example",
		"pr:owner/repo", "pr:owner/repo#x", "mail:a b", "topic:", "project:", "a\nb", "project:" + strings.Repeat("x", 200)} {
		if got, err := subjectKey(bad); err == nil {
			t.Errorf("subjectKey(%q) = %q, want an error", bad, got)
		}
	}
	for _, k := range subjectKinds {
		if recordKinds[k] {
			t.Errorf("%s is both a subject's kind and a record's", k)
		}
	}
}

// A filter is a key, or every key of a kind.
func TestSubjectFilter(t *testing.T) {
	for in, want := range map[string]struct {
		key    string
		prefix bool
	}{
		"":              {"", false},
		"person:":       {"person:", true},
		"personne:*":    {"person:", true},
		"Projet:Site":   {"project:site", false},
		"project:site*": {"project:site", true},
	} {
		key, prefix, err := subjectFilter(in)
		if err != nil || key != want.key || prefix != want.prefix {
			t.Errorf("subjectFilter(%q) = %q %v %v, want %+v", in, key, prefix, err, want)
		}
	}
	if _, _, err := subjectFilter("nothing:"); err == nil {
		t.Error("a filter on an unknown kind")
	}
}

// The same subject, written another way by each tool, is one key in each
// store, and finds its thread, memory and rule again.
func TestOneSubjectEverywhere(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	const said, key = "Projet : Site Web", "project:site-web"
	seq := ownerSays(c, "Le site web passe en ligne vendredi, préviens-moi de tout ce qui le concerne")
	if _, err := c.brainRemember(rememberIn{Text: "Le site web passe en ligne vendredi.", Kind: kindFact, Subject: said, SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainIntend(intendIn{Text: "Relire la page d'accueil.", Subject: "projet:site web", OwedTo: "Personne:Sam", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	th, _, err := c.openThreadOnce(threadIn{Title: "Site web", Goal: "Le site est en ligne.", Subject: "PROJECT: site_web"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainRule(ruleIn{Match: "projets:Site Web", Outcome: levelNow, Why: "Demandé.", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	out, err := c.brainNotify(notifyIn{Text: "Le site est en ligne.", Level: levelNow, Why: "Déployé.", Ref: "Projet:Site-Web"})
	if err != nil {
		t.Fatal(err)
	}
	if out["ref"] != key {
		t.Errorf("notification under %v", out["ref"])
	}
	if th.Subject != key {
		t.Errorf("thread under %q", th.Subject)
	}
	if found, err := c.findThreads("projet:Site Web", "", true, 5); err != nil || len(found) != 1 || found[0].ID != th.ID {
		t.Errorf("find_thread: %+v %v", found, err)
	}
	xs, err := c.mem.recall(recallQuery{Subject: "Projet:site web"})
	if err != nil || len(xs) != 1 || xs[0].Subject != key {
		t.Errorf("recall by subject: %+v %v", xs, err)
	}
	var subj, owed string
	c.mem.db.QueryRow(`SELECT subject, owed_to FROM intentions`).Scan(&subj, &owed)
	if subj != key || owed != "person:sam" {
		t.Errorf("intention under %q, owed to %q", subj, owed)
	}
	rules, _ := c.notifyRules()
	if len(rules) != 1 || rules[0].Match != key {
		t.Errorf("rules %+v", rules)
	}
}

// A subject has one live thread, whoever opens it: the brain, a skill. An
// ended one is not reopened beside a live one; the index holds it too.
func TestOneLiveThreadPerSubject(t *testing.T) {
	c := stepsCore(t)
	first := lightThread(t, c) // pr:example/infra#12
	if _, err := c.openThread(threadIn{Title: "PR", Goal: "Merged.", Subject: "PR:Example/Infra#12"}, nil, "skill:watch-prs"); err == nil ||
		!strings.Contains(err.Error(), "follows pr:example/infra#12 already") {
		t.Fatalf("a skill opened a second live thread: %v", err)
	}
	if err := c.closeLight(first.ID, "Fusionnée.", byBrain); err != nil {
		t.Fatal(err)
	}
	second, err := c.openThread(threadIn{Title: "PR again", Goal: "Merged.", Subject: "pr:example/infra#12"}, nil, "skill:watch-prs")
	if err != nil {
		t.Fatalf("a subject whose thread ended: %v", err)
	}
	if _, err := c.reopenThread(first.ID, "La PR revient.", byBrain); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("an ended thread reopened beside the live one: %v", err)
	}
	if _, err := c.store.db.Exec(`INSERT INTO threads (title, goal, subject, state, created) VALUES ('x', 'y', ?, 'open', 0)`, second.Subject); err == nil {
		t.Fatal("the index lets two live threads follow one subject")
	}
	if _, err := c.openThread(threadIn{Title: "Sans sujet", Goal: "x"}, nil, "skill:x"); err != nil {
		t.Fatalf("a thread without subject: %v", err)
	}
	if _, err := c.openThread(threadIn{Title: "Sans sujet", Goal: "y"}, nil, "skill:x"); err != nil {
		t.Fatalf("a second thread without subject: %v", err)
	}
}

// core.db's subjects of an older eriphe-core become keys once: threads
// (of two live ones on one key, the latest keeps it), notifications
// about a subject (a record's ref kept) and topic rules.
func TestSubjectKeysRewriteCore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	db := s.db
	for _, q := range []string{
		`DROP INDEX threads_live_subject`,
		`DELETE FROM kv WHERE key = 'subject_keys'`,
		`CREATE TABLE facts (id INTEGER)`,
		`INSERT INTO threads (id, title, goal, subject, state, created) VALUES (1, 'a', 'g', 'Projet:Site', 'open', 0)`,
		`INSERT INTO threads (id, title, goal, subject, state, created) VALUES (2, 'b', 'g', 'project:site', 'open', 0)`,
		`INSERT INTO threads (id, title, goal, subject, state, created) VALUES (3, 'c', 'g', 'PR:Example/Infra#12', 'done', 0)`,
		`INSERT INTO threads (id, title, goal, subject, state, created) VALUES (4, 'd', 'g', 'brand:velora', 'open', 0)`,
		`INSERT INTO notifications (text, why, ref, source, level, outcome, reason, created, token) VALUES ('t', 'w', 'Factures', 'brain', 'now', 'now', 'sent', 0, '')`,
		`INSERT INTO notifications (text, why, ref, source, level, outcome, reason, created, token) VALUES ('t', 'w', 'skill:Veille', 'brain', 'now', 'now', 'sent', 0, '')`,
		`INSERT INTO rules (name, source, spec, prompt, state, why, source_ref, tainted, by, created, updated, act)
			VALUES ('n', 'notification', '{"kind":"topic","match":"projet:getout","outcome":"now"}', '', 'active', '', '', '', 'user', 0, 0, 'deliver')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	subjects := map[int64]string{}
	rows, _ := s.db.Query(`SELECT id, subject FROM threads`)
	for rows.Next() {
		var id int64
		var subj string
		rows.Scan(&id, &subj)
		subjects[id] = subj
	}
	rows.Close()
	if subjects[1] != "" || subjects[2] != "project:site" || subjects[3] != "pr:example/infra#12" || subjects[4] != "topic:brand-velora" {
		t.Errorf("threads %v", subjects)
	}
	var said string
	s.db.QueryRow(`SELECT text FROM thread_log WHERE thread = 1`).Scan(&said)
	if !strings.Contains(said, "thread 2") {
		t.Errorf("the older thread's log does not say why it lost its subject: %q", said)
	}
	var refs []string
	rows, _ = s.db.Query(`SELECT ref FROM notifications ORDER BY id`)
	for rows.Next() {
		var r string
		rows.Scan(&r)
		refs = append(refs, r)
	}
	rows.Close()
	if strings.Join(refs, " ") != "topic:facture skill:Veille" {
		t.Errorf("notifications %v", refs)
	}
	var spec string
	s.db.QueryRow(`SELECT spec FROM rules WHERE source = 'notification'`).Scan(&spec)
	if !strings.Contains(spec, `"match":"project:getout"`) {
		t.Errorf("rule %s", spec)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('facts', 'threads_live_subject')`).Scan(&n)
	if n != 1 {
		t.Errorf("facts kept or the index missing: %d", n)
	}
	// Once: a value written after is not rewritten again.
	s.db.Exec(`UPDATE notifications SET ref = 'Factures' WHERE ref = 'topic:facture'`)
	if err := subjectKeysCore(s.db); err != nil {
		t.Fatal(err)
	}
	if s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE ref = 'Factures'`).Scan(&n); n != 1 {
		t.Error("the rewrite ran twice")
	}
}

// A memory.db at schema 5 is brought to 6: its subjects keys, the
// commitment index and the facts tables gone, the stems following.
func TestMemorySchema5To6(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	m, err := openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, q := range []string{
		`UPDATE meta SET value = '5' WHERE key = 'schema_version'`,
		`CREATE INDEX memories_due ON memories (due_at) WHERE kind = 'commitment'`,
		`CREATE TABLE facts (id INTEGER)`, `CREATE TABLE facts_log (id INTEGER)`,
	} {
		if _, err := m.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ins := func(subject string) {
		if _, err := m.db.Exec(`INSERT INTO memories (kind, subject, text, valid_from, recorded_at, source_kind, trust, status, updated_at)
			VALUES ('fact', ?, 'Sam aime le site', ?, ?, 'owner', 'owner', 'active', ?)`, subject, now, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"projet:site web", "workspace", "owner", "person:Hélène", "Projet X !"} {
		ins(s)
	}
	if _, err := m.db.Exec(`INSERT INTO intentions (kind, text, owner, owed_to, subject, state, created_at, updated_at, source_kind, trust)
		VALUES ('promise', 'Rappeler', 'Owner', 'personne:Sam', 'projet:site', 'open', ?, ?, 'owner', 'owner')`, now, now); err != nil {
		t.Fatal(err)
	}
	m.db.Close()
	m, err = openMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	if v := m.schemaVersion(); v != memorySchemaVersion {
		t.Fatalf("schema %d", v)
	}
	var got []string
	rows, _ := m.db.Query(`SELECT subject FROM memories ORDER BY id`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		got = append(got, s)
	}
	rows.Close()
	if want := "project:site-web topic:workspace owner person:helene Projet X !"; strings.Join(got, " ") != want {
		t.Errorf("memories %q, want %q", strings.Join(got, " "), want)
	}
	var owner, owed, subj string
	m.db.QueryRow(`SELECT owner, owed_to, subject FROM intentions`).Scan(&owner, &owed, &subj)
	if owner != "owner" || owed != "person:sam" || subj != "project:site" {
		t.Errorf("intention %q %q %q", owner, owed, subj)
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('memories_due', 'facts', 'facts_log')`).Scan(&n)
	if n != 0 {
		t.Errorf("%d leftovers", n)
	}
	m.now = time.Now
	if xs, err := m.recall(recallQuery{Subject: "project:site-web"}); err != nil || len(xs) != 1 {
		t.Errorf("recall after the rewrite: %d %v", len(xs), err)
	}
}

// One normaliser: no other code makes its own subject key. The old
// functions are gone, and no file but subject.go lowers, trims or
// compares a subject in its own way.
func TestNoOtherSubjectNormaliser(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`\b(canonRef|normalizeSubject|refKinds|refKindAlias|idKinds|isIDRef|ruleRefRe|threadSubjectMax)\b`),
		regexp.MustCompile(`(?i)lower\(\s*subject\s*\)`),
		regexp.MustCompile(`strings\.(ToLower|TrimSpace|EqualFold|Fields|ReplaceAll)\([^()]*\b(in|x|s|r|q|f|th|old)\.(Subject|OwedTo|Owner|Match)\b`),
		regexp.MustCompile(`strings\.(ToLower|TrimSpace)\(\s*(subject|label)\s*\)`),
	}
	// Where the word means something else, or is searched as text.
	allowed := map[string]bool{
		"subject.go": true,
		// decisions.subject: the string the access policy compares; a
		// mail's subject line.
		"policy.go": true, "web_push.go": true,
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, re := range forbidden {
				if re.MatchString(line) {
					t.Errorf("%s:%d makes a subject key of its own (use subjectKey, subject.go): %s", f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
