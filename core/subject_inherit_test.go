package main

import (
	"context"
	"path/filepath"
	"testing"
)

// subjectOfRow is the subject column of a row of a table.
func subjectOfRow(c *core, table, where string, args ...any) string {
	var s string
	c.store.db.QueryRow(`SELECT subject FROM `+table+` WHERE `+where, args...).Scan(&s)
	return s
}

// What the brain makes in a turn about a thread takes the thread's
// subject: a task (a step of it, or one of its own), a notification, a
// memory, an intention, a surface. A subject given wins.
func TestTurnThreadGivesItsSubject(t *testing.T) {
	c := startCore(t)
	m, _ := testMemory(t)
	c.useMemory(m)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque Velora", "goal": "Sam sait si le nom est libre.", "subject": "brand:velora"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	const key = "topic:brand-velora"
	step, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Cherche qui détient la marque Velora."})
	if isErr {
		t.Fatalf("run_task: %v", step)
	}
	own, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Résume les mails.", "thread": "none"})
	if isErr {
		t.Fatalf("run_task none: %v", own)
	}
	given, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Relis le site.", "thread": "none", "subject": "Projet: Site"})
	if isErr {
		t.Fatalf("run_task given: %v", given)
	}
	for name, id := range map[string]any{"step": step["task"], "own": own["id"]} {
		if got := subjectOfRow(c, "tasks", "id = ?", id); got != key {
			t.Errorf("%s task under %q", name, got)
		}
	}
	if got := subjectOfRow(c, "tasks", "id = ?", given["id"]); got != "project:site" {
		t.Errorf("given task under %q", got)
	}
	if out, _ := callBrainTool(t, c, "notify", map[string]any{"text": "Velora est prise.", "weight": 2, "why": "La recherche a fini."}); out["subject"] != key {
		t.Errorf("notify: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "remember", map[string]any{"text": "La marque Velora est déposée.", "kind": "fact"}); isErr {
		t.Fatalf("remember: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "remember", map[string]any{"text": "Lina est avocate en marques.", "kind": "person"}); isErr {
		t.Fatalf("remember: %v", out)
	}
	var fact, pref string
	c.mem.db.QueryRow(`SELECT subject FROM memories WHERE kind = 'fact'`).Scan(&fact)
	c.mem.db.QueryRow(`SELECT subject FROM memories WHERE kind = 'person'`).Scan(&pref)
	if fact != key || pref == key {
		t.Errorf("fact under %q, person under %q", fact, pref)
	}
	if out, isErr := callBrainTool(t, c, "intend", map[string]any{"text": "Relancer l'avocat sur Velora.", "due_at": "2026-12-01"}); isErr {
		t.Fatalf("intend: %v", out)
	}
	var intent string
	c.mem.db.QueryRow(`SELECT subject FROM intentions`).Scan(&intent)
	if intent != key {
		t.Errorf("intention under %q", intent)
	}
	if out, isErr := callBrainTool(t, c, "surface_write", map[string]any{"id": "velora", "title": "Velora",
		"components": []any{map[string]any{"id": "root", "component": "Text", "text": "Prise."}}}); isErr {
		t.Fatalf("surface_write: %v", out)
	}
	if got := subjectOfRow(c, "surfaces", "id = 'velora'"); got != key {
		t.Errorf("surface under %q", got)
	}
}

// Without a thread, the one subject my message names is the turn's; two
// named give none.
func TestNamedSubjectIsTheTurns(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	c.store.keepSubject("project:site", "")
	ownerSays(c, "Préviens-moi quand le site est en ligne")
	c.turn.name(c.subjectsNamed("Préviens-moi quand le site est en ligne"))
	if out, _ := c.brainNotify(notifyIn{Text: "En ligne.", Weight: 2, Why: "Déployé."}); out["subject"] != "project:site" {
		t.Errorf("one named: %v", out)
	}
	c.turn.name([]string{"project:site", "person:sam"})
	if out, _ := c.brainNotify(notifyIn{Text: "Encore.", Weight: 2, Why: "Encore."}); out["subject"] != nil {
		t.Errorf("two named: %v", out)
	}
	c.turn.name(nil)
	if out, _ := c.brainNotify(notifyIn{Text: "Rien.", Weight: 2, Why: "Rien.", Ref: "intention:4"}); out["subject"] != nil || out["ref"] != "intention:4" {
		t.Errorf("none: %v", out)
	}
}

// A rule on a subject matches a notification by its subject; a rule on a
// record (skill:<name>) still matches its ref.
func TestRuleMatchesSubjectOrRef(t *testing.T) {
	rules := []notifyRule{
		{ID: 1, Kind: ruleTopic, Match: "project:site", Ceiling: 0, State: ruleActive},
		{ID: 2, Kind: ruleTopic, Match: "skill:veille", Floor: 5, Ceiling: 5, State: ruleActive},
	}
	for _, tc := range []struct {
		it   notifyItem
		want int64
	}{
		{notifyItem{Subject: "project:site:prod", Ref: "skill:veille"}, 1},
		{notifyItem{Subject: "topic:autre", Ref: "skill:veille"}, 2},
		{notifyItem{Ref: "intention:3"}, 0},
	} {
		var got int64
		if r := itemRule(rules, tc.it); r != nil {
			got = r.ID
		}
		if got != tc.want {
			t.Errorf("%+v: rule %d, want %d", tc.it, got, tc.want)
		}
	}
}

// A card takes its proposal's subject, else its maker's; an attention
// item its card's, or its source's.
func TestCardAndItemSubjects(t *testing.T) {
	c := stepsCore(t)
	th := lightThread(t, c) // pr:example/infra#12
	x := &Memory{Kind: kindFact, Subject: "person:sam", Text: "Sam relit les PR le matin.", SourceKind: srcBrain, Trust: trustExternal,
		Status: memProposed}
	if err := c.mem.add(x, "brain"); err != nil {
		t.Fatal(err)
	}
	c.cardProposed(cardMemory, x.ID)
	if got := subjectOfRow(c, "convo_cards", "ref = ?", proposalRef(cardMemory, x.ID)); got != "person:sam" {
		t.Errorf("memory card under %q", got)
	}
	c.attend(attentionItem{Ref: "mail:1", Weight: 2, Why: "a reply", Source: threadRef(th.ID)})
	if got := subjectOfRow(c, "attention", "ref = 'mail:1'"); got != th.Subject {
		t.Errorf("item from a thread under %q", got)
	}
	c.attend(attentionItem{Ref: "mail:2", Weight: 2, Why: "a bill", Source: "skill:mail", Subject: "topic:facture"})
	if items := c.attentionWhere(`ref = 'mail:2'`); len(items) != 1 || items[0].Subject != "topic:facture" {
		t.Errorf("item %+v", items)
	}
	// A card of a proposal with no subject: the turn's thread's.
	c.turn.about(th.ID)
	out, err := c.brainRule(ruleIn{Kind: ruleQuiet, From: "22:00", To: "07:00", Why: "Proposé."})
	if err != nil {
		t.Fatal(err)
	}
	if got := subjectOfRow(c, "convo_cards", "kind = ?", cardRule); got != th.Subject {
		t.Errorf("rule card under %q (%v)", got, out)
	}
	// Raised by a task: the task's.
	c.turn.about(0)
	c.store.insertTask(&TaskRow{ID: "t-9", Runtime: "claude-code", State: "running", Prompt: "x", Subject: "project:site"})
	y := &Memory{Kind: kindFact, Text: "Le site a un certificat neuf.", SourceKind: srcTask, SourceRef: "task:t-9", Trust: trustExternal, Status: memProposed}
	if err := c.mem.add(y, "task"); err != nil {
		t.Fatal(err)
	}
	c.cardNow(cardMemory, y.ID, "Je retiens ?", "task:t-9")
	if got := subjectOfRow(c, "convo_cards", "ref = ?", proposalRef(cardMemory, y.ID)); got != "project:site" {
		t.Errorf("task's card under %q", got)
	}
	var item string
	c.store.db.QueryRow(`SELECT a.subject FROM attention a JOIN convo_cards x ON a.ref = 'card:' || x.id WHERE x.ref = ?`, proposalRef(cardMemory, y.ID)).Scan(&item)
	if item != "project:site" {
		t.Errorf("its item under %q", item)
	}
}

// A surface keeps its subject when written again without one; a subject
// given changes it.
func TestSurfaceKeepsItsSubject(t *testing.T) {
	c := stepsCore(t)
	th := lightThread(t, c)
	write := func(subject string) {
		t.Helper()
		in := surfaceIn{ID: "pr", Title: "PR", Components: []byte(`[{"id":"root","component":"Text","text":"x"}]`), Subject: subject}
		if subject == "" {
			in.inherit = c.turnSubject()
		}
		if _, errs, err := c.surfaceWrite(surfaceAuthorBrain, in); err != nil || len(errs) > 0 {
			t.Fatal(err, errs)
		}
	}
	c.turn.about(th.ID)
	write("")
	if got := subjectOfRow(c, "surfaces", "id = 'pr'"); got != th.Subject {
		t.Fatalf("new surface under %q", got)
	}
	c.store.keepSubject("project:site", "")
	c.turn.name([]string{"project:site"})
	c.turn.about(0)
	write("")
	if got := subjectOfRow(c, "surfaces", "id = 'pr'"); got != th.Subject {
		t.Errorf("rewritten under %q", got)
	}
	write("project:site")
	if got := subjectOfRow(c, "surfaces", "id = 'pr'"); got != "project:site" {
		t.Errorf("given, under %q", got)
	}
}

// An older store gets the subject columns at start, and opens.
func TestSubjectColumnsOnAnOlderStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range subjectTables {
		if _, err := s.db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN subject`); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
	}
	s.db.Exec(`DELETE FROM kv WHERE key = ?`, notificationSubjects)
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range subjectTables {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'subject'`, table).Scan(&n)
		if n != 1 {
			t.Errorf("%s has no subject", table)
		}
	}
	if s.get(notificationSubjects) == "" {
		t.Error("notifications not moved")
	}
}

// A skill's run is about the subject it declares, else its events' key
// when it names a thing; a bad subject refuses the skill.
func TestSkillRunSubject(t *testing.T) {
	st, err := parseSkillTrigger(skillManifest{When: "monitor.failed key=sauvegarde", Subject: "Alerte: sauvegarde"})
	if err != nil || st.Subject != "alert:sauvegarde" {
		t.Fatalf("declared: %+v %v", st, err)
	}
	monitor := []journalEvent{{Kind: "monitor.failed", Key: "sauvegarde"}}
	for _, tc := range []struct {
		st     *skillTrigger
		key    string
		events []journalEvent
		want   string
	}{
		{st, "autre", nil, "alert:sauvegarde"},
		{&skillTrigger{}, "sauvegarde", monitor, "topic:sauvegarde"},
		{&skillTrigger{}, "pr:example/infra#12", nil, "pr:example/infra#12"},
		{&skillTrigger{}, "t-20261114-1", []journalEvent{{Kind: "task.ended"}}, ""},
		{&skillTrigger{}, "", monitor, ""},
	} {
		if got := skillRunSubject(tc.st, tc.key, tc.events); got != tc.want {
			t.Errorf("%q %v: %q, want %q", tc.key, tc.events, got, tc.want)
		}
	}
	if _, err := parseSkillTrigger(skillManifest{When: "monitor.new", Subject: "task:t-1"}); err == nil {
		t.Error("a record as a skill's subject")
	}
	if _, err := parseSkillTrigger(skillManifest{Subject: "project:site"}); err == nil {
		t.Error("a subject without when")
	}
	c, now := skillCore(t)
	sk := triggeredSkill{Name: "veille", Trigger: &skillTrigger{When: "monitor.failed", Kind: "monitor.failed", Kinds: []string{"monitor.failed"}}}
	c.startSkillRun(context.Background(), sk, "alert:sauvegarde", nil, *now)
	rs := runsOfSkill(t, c, "veille")
	if len(rs) != 1 || rs[0].Task == "" {
		t.Fatalf("runs %+v", rs)
	}
	if got := c.taskSubject(rs[0].Task); got != "alert:sauvegarde" {
		t.Errorf("run's task under %q", got)
	}
}

// What the extraction writes from a task takes the task's subject: an
// episode whatever was guessed, a fact when none was given.
func TestExtractionTakesTheTasksSubject(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Runtime: "claude-code", State: "stopped", Prompt: "x", Subject: "project:site"})
	for _, tc := range []struct {
		op   extractionOp
		ref  string
		want string
	}{
		{extractionOp{Kind: kindDecision, Subject: "topic:deploiement"}, "task:t-1", "project:site"},
		{extractionOp{Kind: kindFact}, "msg:3,task:t-1", "project:site"},
		{extractionOp{Kind: kindFact, Subject: "person:sam"}, "task:t-1", "person:sam"},
		{extractionOp{Kind: kindEpisode, Subject: journalSubject}, "msg:3,task:t-1", journalSubject},
		{extractionOp{Kind: kindFact, Subject: "topic:x"}, "msg:3", "topic:x"},
	} {
		if got := c.opSubject(tc.op, tc.ref); got != tc.want {
			t.Errorf("%+v from %s: %q, want %q", tc.op, tc.ref, got, tc.want)
		}
	}
}

// A notification's subject moves out of its ref once; a record's ref stays.
func TestNotificationSubjectsMoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"project:site", "skill:veille", ""} {
		s.db.Exec(`INSERT INTO notifications (created, text, why, ref, source, weight, outcome, reason, token) VALUES (1, 'x', '', ?, 'brain', 5, 'now', '', ?)`, ref, ref+"t")
	}
	s.db.Exec(`DELETE FROM kv WHERE key = ?`, notificationSubjects)
	if err := subjectColumns(s.db); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string][2]string{"project:sitet": {"", "project:site"}, "skill:veillet": {"skill:veille", ""}} {
		var r, sub string
		s.db.QueryRow(`SELECT ref, subject FROM notifications WHERE token = ?`, ref).Scan(&r, &sub)
		if r != want[0] || sub != want[1] {
			t.Errorf("%s: ref %q subject %q", ref, r, sub)
		}
	}
}
