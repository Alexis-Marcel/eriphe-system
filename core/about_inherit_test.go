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

// What the brain makes in a turn about a node is about the node: a task,
// a notification, an intention. An about given wins.
func TestTurnNodeIsTheAbout(t *testing.T) {
	c := graphCore(t)
	out, isErr := callBrainTool(t, c, "node_open", map[string]any{"title": "Marque Velora", "goal": "Sam sait si le nom est libre."})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	key := nodeRef(out["id"].(string))
	step, isErr := callBrainTool(t, c, "run_task", map[string]any{"ask": "Cherche qui détient la marque Velora.", "title": "Velora"})
	if isErr {
		t.Fatalf("run_task: %v", step)
	}
	if got := aboutOfRow(c, "tasks", "id = ?", step["task"]); got != key {
		t.Errorf("task under %q", got)
	}
	if out, _ := callBrainTool(t, c, "notify", map[string]any{"text": "Velora est prise.", "weight": 2, "why": "La recherche a fini."}); out["about"] != key {
		t.Errorf("notify: %v", out)
	}
	out, isErr = callBrainTool(t, c, "intend", map[string]any{"text": "Relancer l'avocate pour Velora.", "kind": "reminder"})
	if isErr {
		t.Fatalf("intend: %v", out)
	}
	if x, _ := c.intents.intention(int64(out["id"].(float64))); x == nil || x.About != key {
		t.Errorf("intention under %+v", x)
	}
	if out, isErr := callBrainTool(t, c, "intend", map[string]any{"text": "Relancer l'avocat sur Velora.", "due_at": "2026-12-01"}); isErr {
		t.Fatalf("intend: %v", out)
	}
	var intent string
	c.intents.db.QueryRow(`SELECT about FROM intentions`).Scan(&intent)
	if intent != key {
		t.Errorf("intention under %q", intent)
	}
}

// Without a node, the one entity my message names is the turn's; two
// named give none.
func TestNamedIsTheTurns(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	c.store.noteAbout("project:site")
	ownerSays(c, "Préviens-moi quand le site est en ligne")
	c.turn.name(c.named("Préviens-moi quand le site est en ligne"))
	if out, _ := c.brainNotify(notifyIn{Text: "En ligne.", Weight: 2, Why: "Déployé."}); out["about"] != "project:site" {
		t.Errorf("one named: %v", out)
	}
	c.turn.name([]string{"project:site", "person:sam"})
	if out, _ := c.brainNotify(notifyIn{Text: "Encore.", Weight: 2, Why: "Encore."}); out["about"] != nil {
		t.Errorf("two named: %v", out)
	}
	c.turn.name(nil)
	if out, _ := c.brainNotify(notifyIn{Text: "Rien.", Weight: 2, Why: "Rien.", Ref: "intention:4"}); out["about"] != nil || out["ref"] != "intention:4" {
		t.Errorf("none: %v", out)
	}
}

// A rule on a key matches a notification about it; a rule on a record
// (skill:<name>) still matches its ref.
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
	c := graphCore(t)
	th := workNode(t, c, "")
	x := mustIntend(t, c.intents, &Intention{About: "person:sam", Text: "Relire les PR avec Sam.", SourceKind: srcBrain, Trust: trustExternal,
		State: intentProposed})
	c.cardProposed(cardIntention, x.ID)
	if got := aboutOfRow(c, "convo_cards", "ref = ?", proposalRef(cardIntention, x.ID)); got != "person:sam" {
		t.Errorf("intention card under %q", got)
	}
	c.attend(attentionItem{Ref: "alert:1", Weight: 2, Why: "a reply", Source: nodeRef(th)})
	if got := aboutOfRow(c, "attention", "ref = 'alert:1'"); got != nodeRef(th) {
		t.Errorf("item from a node under %q", got)
	}
	c.attend(attentionItem{Ref: "alert:2", Weight: 2, Why: "a bill", Source: "skill:mail", About: "topic:facture"})
	if items := c.attentionWhere(`ref = 'alert:2'`); len(items) != 1 || items[0].About != "topic:facture" {
		t.Errorf("item %+v", items)
	}
	// A card of a proposal about nothing: the turn's node.
	c.turn.about(th)
	out, err := c.brainRule(ruleIn{Kind: ruleQuiet, From: "22:00", To: "07:00", Why: "Proposé."})
	if err != nil {
		t.Fatal(err)
	}
	if got := aboutOfRow(c, "convo_cards", "kind = ?", cardRule); got != nodeRef(th) {
		t.Errorf("rule card under %q (%v)", got, out)
	}
	// Raised by a task: the task's.
	c.turn.about("")
	c.store.insertTask(&TaskRow{ID: "t-9", Runtime: "claude-code", State: "running", Prompt: "x", About: "project:site"})
	y := mustIntend(t, c.intents, &Intention{Text: "Renouveler le certificat du site.", SourceKind: srcTask, SourceRef: "task:t-9", Trust: trustExternal, State: intentProposed})
	c.cardNow(cardIntention, y.ID, "Je te le rappelle ?", "task:t-9")
	if got := aboutOfRow(c, "convo_cards", "ref = ?", proposalRef(cardIntention, y.ID)); got != "project:site" {
		t.Errorf("task's card under %q", got)
	}
	var item string
	c.store.db.QueryRow(`SELECT a.about FROM attention a JOIN convo_cards x ON a.ref = 'card:' || x.id WHERE x.ref = ?`, proposalRef(cardIntention, y.ID)).Scan(&item)
	if item != "project:site" {
		t.Errorf("its item under %q", item)
	}
}

// A skill's run is about its events' key when it names a thing; a node
// that is not a node's id refuses the skill.
func TestSkillRunSubject(t *testing.T) {
	st, err := parseSkillTrigger(skillManifest{When: "monitor.failed key=sauvegarde", Node: "k3x9m2p7qa"})
	if err != nil || st.Node != "k3x9m2p7qa" {
		t.Fatalf("declared: %+v %v", st, err)
	}
	monitor := []journalEvent{{Kind: "monitor.failed", Key: "sauvegarde"}}
	for _, tc := range []struct {
		key    string
		events []journalEvent
		want   string
	}{
		{"sauvegarde", monitor, "topic:sauvegarde"},
		{"pr:example/infra#12", nil, "pr:example/infra#12"},
		{"t-20261114-1", []journalEvent{{Kind: "task.ended"}}, ""},
		{"", monitor, ""},
	} {
		if got := skillRunAlias(tc.key, tc.events); got != tc.want {
			t.Errorf("%q %v: %q, want %q", tc.key, tc.events, got, tc.want)
		}
	}
	if _, err := parseSkillTrigger(skillManifest{When: "monitor.new", Node: "Mes mails"}); err == nil {
		t.Error("a node that is no id")
	}
	if _, err := parseSkillTrigger(skillManifest{Node: "k3x9m2p7qa"}); err == nil {
		t.Error("a node without when")
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
