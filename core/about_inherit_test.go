package main

import (
	"context"
	"testing"
)

// aboutOfRow is the about column of a row of a table.
func aboutOfRow(c *core, table, where string, args ...any) string {
	var s string
	c.store.db.QueryRow(`SELECT about FROM `+table+` WHERE `+where, args...).Scan(&s)
	return s
}

// What the brain makes in a turn about a thread is about the thread: a
// task (a step of it), a notification, a memory, an intention, the
// thread's surface it adds its part to. An about given wins.
func TestTurnThreadIsTheAbout(t *testing.T) {
	c := startCore(t)
	m, _ := testMemory(t)
	c.useMemory(m)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Marque Velora", "goal": "Sam sait si le nom est libre.", "alias": "brand:velora"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	key := threadRef(openedThread(t, out).ID)
	step, isErr := callBrainTool(t, c, "run_task", map[string]any{"prompt": "Cherche qui détient la marque Velora."})
	if isErr {
		t.Fatalf("run_task: %v", step)
	}
	if got := aboutOfRow(c, "tasks", "id = ?", step["task"]); got != key {
		t.Errorf("step task under %q", got)
	}
	if out, _ := callBrainTool(t, c, "notify", map[string]any{"text": "Velora est prise.", "weight": 2, "why": "La recherche a fini."}); out["about"] != key {
		t.Errorf("notify: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "remember", map[string]any{"text": "La marque Velora est déposée.", "kind": "fact"}); isErr {
		t.Fatalf("remember: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "remember", map[string]any{"text": "Lina est avocate en marques.", "kind": "person"}); isErr {
		t.Fatalf("remember: %v", out)
	}
	var fact, pref string
	c.mem.db.QueryRow(`SELECT about FROM memories WHERE kind = 'fact'`).Scan(&fact)
	c.mem.db.QueryRow(`SELECT about FROM memories WHERE kind = 'person'`).Scan(&pref)
	if fact != key || pref == key {
		t.Errorf("fact under %q, person under %q", fact, pref)
	}
	if out, isErr := callBrainTool(t, c, "intend", map[string]any{"text": "Relancer l'avocat sur Velora.", "due_at": "2026-12-01"}); isErr {
		t.Fatalf("intend: %v", out)
	}
	var intent string
	c.mem.db.QueryRow(`SELECT about FROM intentions`).Scan(&intent)
	if intent != key {
		t.Errorf("intention under %q", intent)
	}
	sid := threadSurfaceID(openedThread(t, out).ID)
	if out, isErr := callBrainTool(t, c, "surface_write", map[string]any{"id": sid,
		"components": []any{map[string]any{"id": "root", "component": "Text", "text": "Prise."}}}); isErr {
		t.Fatalf("surface_write: %v", out)
	}
	if got := aboutOfRow(c, "surfaces", "id = ?", sid); got != key {
		t.Errorf("surface under %q", got)
	}
}

// Without a thread, the one thread or entity my message names is the
// turn's; two named give none.
func TestNamedIsTheTurns(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	th, err := c.openThread(threadIn{Title: "Site", Goal: "En ligne.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	ownerSays(c, "Préviens-moi quand le site est en ligne")
	c.turn.name(c.named("Préviens-moi quand le site est en ligne"))
	if out, _ := c.brainNotify(notifyIn{Text: "En ligne.", Weight: 2, Why: "Déployé."}); out["about"] != threadRef(th.ID) {
		t.Errorf("one named: %v", out)
	}
	c.turn.name([]string{threadRef(th.ID), "person:sam"})
	if out, _ := c.brainNotify(notifyIn{Text: "Encore.", Weight: 2, Why: "Encore."}); out["about"] != nil {
		t.Errorf("two named: %v", out)
	}
	c.turn.name(nil)
	if out, _ := c.brainNotify(notifyIn{Text: "Rien.", Weight: 2, Why: "Rien.", Ref: "intention:4"}); out["about"] != nil || out["ref"] != "intention:4" {
		t.Errorf("none: %v", out)
	}
}

// A rule on a key matches a notification about it, or about the thread
// it is an alias of; a rule on a record (skill:<name>, thread:<id>) still
// matches its ref.
func TestRuleMatchesAboutOrRef(t *testing.T) {
	rules := []notifyRule{
		{ID: 1, Kind: ruleTopic, Match: "project:site", Ceiling: 0, State: ruleActive},
		{ID: 2, Kind: ruleTopic, Match: "skill:veille", Floor: 5, Ceiling: 5, State: ruleActive},
	}
	for _, tc := range []struct {
		it   notifyItem
		want int64
	}{
		{notifyItem{About: "project:site:prod", Ref: "skill:veille"}, 1},
		{notifyItem{About: "topic:autre", Ref: "skill:veille"}, 2},
		{notifyItem{Ref: "intention:3"}, 0},
		{notifyItem{About: "thread:4", Aliases: []string{"project:site"}}, 1},
		{notifyItem{About: "thread:4", Aliases: []string{"le site"}}, 0},
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

// A card takes its proposal's about, else its maker's; an attention item
// its card's, or its source's.
func TestCardAndItemAbouts(t *testing.T) {
	c := stepsCore(t)
	th := lightThread(t, c) // pr:example/infra#12
	x := &Memory{Kind: kindFact, About: "person:sam", Text: "Sam relit les PR le matin.", SourceKind: srcBrain, Trust: trustExternal,
		Status: memProposed}
	if err := c.mem.add(x, "brain"); err != nil {
		t.Fatal(err)
	}
	c.cardProposed(cardMemory, x.ID)
	if got := aboutOfRow(c, "convo_cards", "ref = ?", proposalRef(cardMemory, x.ID)); got != "person:sam" {
		t.Errorf("memory card under %q", got)
	}
	c.attend(attentionItem{Ref: "alert:1", Weight: 2, Why: "a reply", Source: threadRef(th.ID)})
	if got := aboutOfRow(c, "attention", "ref = 'alert:1'"); got != threadRef(th.ID) {
		t.Errorf("item from a thread under %q", got)
	}
	c.attend(attentionItem{Ref: "alert:2", Weight: 2, Why: "a bill", Source: "skill:mail", About: "topic:facture"})
	if items := c.attentionWhere(`ref = 'alert:2'`); len(items) != 1 || items[0].About != "topic:facture" {
		t.Errorf("item %+v", items)
	}
	// A card of a proposal about nothing: the turn's thread.
	c.turn.about(th.ID)
	out, err := c.brainRule(ruleIn{Kind: ruleQuiet, From: "22:00", To: "07:00", Why: "Proposé."})
	if err != nil {
		t.Fatal(err)
	}
	if got := aboutOfRow(c, "convo_cards", "kind = ?", cardRule); got != threadRef(th.ID) {
		t.Errorf("rule card under %q (%v)", got, out)
	}
	// Raised by a task: the task's.
	c.turn.about(0)
	c.store.insertTask(&TaskRow{ID: "t-9", Runtime: "claude-code", State: "running", Prompt: "x", About: "project:site"})
	y := &Memory{Kind: kindFact, Text: "Le site a un certificat neuf.", SourceKind: srcTask, SourceRef: "task:t-9", Trust: trustExternal, Status: memProposed}
	if err := c.mem.add(y, "task"); err != nil {
		t.Fatal(err)
	}
	c.cardNow(cardMemory, y.ID, "Je retiens ?", "task:t-9")
	if got := aboutOfRow(c, "convo_cards", "ref = ?", proposalRef(cardMemory, y.ID)); got != "project:site" {
		t.Errorf("task's card under %q", got)
	}
	var item string
	c.store.db.QueryRow(`SELECT a.about FROM attention a JOIN convo_cards x ON a.ref = 'card:' || x.id WHERE x.ref = ?`, proposalRef(cardMemory, y.ID)).Scan(&item)
	if item != "project:site" {
		t.Errorf("its item under %q", item)
	}
}

// A surface keeps its about when written again without one; one given
// changes it.
func TestSurfaceKeepsItsAbout(t *testing.T) {
	c := stepsCore(t)
	th := lightThread(t, c)
	write := func(about string) {
		t.Helper()
		in := surfaceIn{ID: "pr", Title: "PR", Components: []byte(`[{"id":"root","component":"Text","text":"x"}]`), About: about}
		if about == "" {
			in.inherit = c.turnAbout()
		}
		if _, errs, err := c.fixtureSurface(surfaceAuthorBrain, in); err != nil || len(errs) > 0 {
			t.Fatal(err, errs)
		}
	}
	c.turn.about(th.ID)
	write("")
	if got := aboutOfRow(c, "surfaces", "id = 'pr'"); got != threadRef(th.ID) {
		t.Fatalf("new surface under %q", got)
	}
	c.store.noteAbout("project:site")
	c.turn.name([]string{"project:site"})
	c.turn.about(0)
	write("")
	if got := aboutOfRow(c, "surfaces", "id = 'pr'"); got != threadRef(th.ID) {
		t.Errorf("rewritten under %q", got)
	}
	write("project:site")
	if got := aboutOfRow(c, "surfaces", "id = 'pr'"); got != "project:site" {
		t.Errorf("given, under %q", got)
	}
}

// A skill's run is about the thread alias it declares, else its events' key
// when it names a thing; a bad subject refuses the skill.
func TestSkillRunSubject(t *testing.T) {
	st, err := parseSkillTrigger(skillManifest{When: "monitor.failed key=sauvegarde", Thread: "Alerte: sauvegarde"})
	if err != nil || st.Thread != "alert:sauvegarde" {
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
		if got := skillRunAlias(tc.st, tc.key, tc.events); got != tc.want {
			t.Errorf("%q %v: %q, want %q", tc.key, tc.events, got, tc.want)
		}
	}
	if _, err := parseSkillTrigger(skillManifest{When: "monitor.new", Thread: "task:t-1"}); err == nil {
		t.Error("a record as a skill's subject")
	}
	if _, err := parseSkillTrigger(skillManifest{Thread: "project:site"}); err == nil {
		t.Error("a subject without when")
	}
	c, now := skillCore(t)
	sk := triggeredSkill{Name: "veille", Trigger: &skillTrigger{When: "monitor.failed", Kind: "monitor.failed", Kinds: []string{"monitor.failed"}}}
	c.startSkillRun(context.Background(), sk, "alert:sauvegarde", nil, *now)
	rs := runsOfSkill(t, c, "veille")
	if len(rs) != 1 || rs[0].Task == "" {
		t.Fatalf("runs %+v", rs)
	}
	if got := c.taskAbout(rs[0].Task); got != "alert:sauvegarde" {
		t.Errorf("run's task under %q", got)
	}
}

// What the extraction writes from a task takes the task's subject: an
// episode whatever was guessed, a fact when none was given.
func TestExtractionTakesTheTasksSubject(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Runtime: "claude-code", State: "stopped", Prompt: "x", About: "project:site"})
	for _, tc := range []struct {
		op   extractionOp
		ref  string
		want string
	}{
		{extractionOp{Kind: kindDecision, About: "topic:deploiement"}, "task:t-1", "project:site"},
		{extractionOp{Kind: kindFact}, "msg:3,task:t-1", "project:site"},
		{extractionOp{Kind: kindFact, About: "person:sam"}, "task:t-1", "person:sam"},
		{extractionOp{Kind: kindEpisode, About: journalEntity}, "msg:3,task:t-1", journalEntity},
		{extractionOp{Kind: kindFact, About: "topic:x"}, "msg:3", "topic:x"},
	} {
		if got := c.opAbout(tc.op, tc.ref); got != tc.want {
			t.Errorf("%+v from %s: %q, want %q", tc.op, tc.ref, got, tc.want)
		}
	}
}
