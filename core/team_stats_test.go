package main

import (
	"strings"
	"testing"
	"time"
)

// The team's measures (team_stats.go): from what eriphe-core keeps.

// A rule's last 30 days: fires, skipped, cards during its wakes (many: a
// standing approval may be missing), corrections, the brain's cost.
func TestRuleStats(t *testing.T) {
	c, now := watchCore(t, at(t, "2026-11-09", "10:00"))
	w := mustWatch(t, c, &rule{Name: "Prod", Source: sourceEnd, Prompt: "Regarde."})
	other := mustWatch(t, c, &rule{Name: "Autre", Source: sourceEnd, Prompt: "Rien."})
	base := now.Unix()
	res, err := c.store.db.Exec(`INSERT INTO notifications (created, text, why, source, level, outcome, reason, token, feedback)
		VALUES (?, 'Prod en panne', 'règle', ?, 'now', 'now', 'rule', 'x', 'down')`, base-450, notifyFromRule)
	if err != nil {
		t.Fatal(err)
	}
	nid, _ := res.LastInsertId()
	for _, q := range []struct {
		rule         int64
		fired, woke  int64
		ended        any
		outcome      string
		notification any
	}{
		{w.ID, base - 600, 0, nil, runSkipped, nil},
		{w.ID, base - 500, base - 500, base - 400, runNotified, nid},
		{w.ID, base - 300, base - 300, nil, runSaid, nil},
		{w.ID, base - 40*86400, base - 40*86400, nil, runSaid, nil}, // out of the window
		{other.ID, base - 200, base - 200, base - 100, runSilent, nil},
	} {
		var woke any
		if q.woke != 0 {
			woke = q.woke
		}
		if _, err := c.store.db.Exec(`INSERT INTO rule_runs (rule_id, fired, cause, woke, outcome, notification, ended) VALUES (?, ?, 'end', ?, ?, ?, ?)`,
			q.rule, q.fired, woke, q.outcome, q.notification, q.ended); err != nil {
			t.Fatal(err)
		}
	}
	for i, at := range []int64{base - 450, base - 250, base - 200, base - 5000} {
		c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, created) VALUES (?, 'memory', ?)`, "memory:"+string(rune('a'+i)), at)
	}
	c.store.insertTask(&TaskRow{ID: "t-brain2", Node: "pc", Kind: assistantRoleBrain, Runtime: "claude-code", State: stateIdle})
	c.store.addEvents("t-brain2", []EventRow{
		{Seq: 1, Time: time.Unix(base-450, 0), Type: "turn_end", Data: map[string]any{"cost_usd": 0.05}},
		{Seq: 2, Time: time.Unix(base-3000, 0), Type: "turn_end", Data: map[string]any{"cost_usd": 1.0}}, // in no wake
	})
	s := c.ruleStatsOf(w, statsSince(*now))
	if s.Fires != 3 || s.Skipped != 1 || s.Notified != 1 || s.Corrections != 1 {
		t.Fatalf("stats: %+v", s)
	}
	// Cards at -450 (its first wake) and -250, -200 (its second, an hour at
	// most); the one at -200 counts once although the other rule woke then.
	if s.Cards != 3 || s.Hint == "" {
		t.Fatalf("cards: %+v", s)
	}
	if s.Usage.Turns != 1 || s.Usage.CostUSD < 0.049 || s.Usage.CostUSD > 0.051 {
		t.Fatalf("its cost: %+v", s.Usage)
	}
	if s.Last.Unix() != base-300 {
		t.Fatalf("last: %v", s.Last)
	}
	if o := c.ruleStatsOf(other, statsSince(*now)); o.Fires != 1 || o.Hint != "" {
		t.Fatalf("the other: %+v", o)
	}
}

// An assistant's last 30 days: woken by its rules, its tasks' turns, its
// reports and notifications, the user's corrections, the values it wrote.
func TestAssistantStats(t *testing.T) {
	c, now := watchCore(t, at(t, "2026-11-09", "10:00"))
	a := Assistant{Name: "tri", Role: assistantRoleAssistant, DisplayName: "Tri", Disabled: true, Powers: []string{powerReports, powerNotifies}, State: testState}
	setAssistant(c, a)
	w := mustWatch(t, c, &rule{Name: "Tri", Source: sourceEnd, Prompt: "Trie.", Act: "assistant:tri"})
	base := now.Unix()
	c.store.db.Exec(`INSERT INTO rule_runs (rule_id, fired, cause, outcome) VALUES (?, ?, 'end', ?), (?, ?, 'end', ?)`,
		w.ID, base-100, runWoke, w.ID, base-50, runSkipped)
	c.noteAssistantTask("tri", "t-tri1")
	c.store.insertTask(&TaskRow{ID: "t-tri1", Node: "pc", Kind: assistantRoleAssistant, Runtime: "claude-code", State: stateIdle})
	c.store.addEvents("t-tri1", []EventRow{{Seq: 1, Time: time.Now(), Type: "turn_end", Data: map[string]any{"cost_usd": 0.01}}})
	if _, err := c.assistantReport(a, reportIn{Text: "Un mail urgent de la banque."}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.assistantNotify(a, notifyIn{Text: "Facture à payer", Why: "échéance demain", Level: levelBrief}); err != nil {
		t.Fatal(err)
	}
	c.store.db.Exec(`UPDATE notifications SET feedback = 'later' WHERE source = ?`, notifyFromAssistant)
	if _, err := c.setState(a, setStateIn{Values: map[string]any{"prod": "ok", "incidents": 2.0}}, nil, *now); err != nil {
		t.Fatal(err)
	}
	s := c.assistantStatsOf(a, statsSince(*now))
	if s.Wakes != 1 || s.Usage.Turns != 1 || !s.Usage.Costed || s.Reports != 1 || s.Notified != 1 || s.Corrections != 1 || s.Facts != 2 {
		t.Fatalf("stats: %+v", s)
	}
	if got := c.assistantEntries(*now); len(got) != 1 || got[0].Stats == nil || len(got[0].WakeRules) != 1 {
		t.Fatalf("the brain's view: %+v", got)
	}
}

// Who reads an assistant's facts: the brain, the rules that name them,
// the assistants lent them.
func TestFactReaders(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	setAssistant(c, Assistant{Name: "prod", Role: assistantRoleAssistant, State: testState})
	setAssistant(c, Assistant{Name: "tri", Role: assistantRoleAssistant, DisplayName: "Tri", Facts: []string{"prod"}})
	mustWatch(t, c, &rule{Name: "Incidents", Source: sourceSchedule, Spec: ruleWhen{Schedule: "daily 07:00"}, Prompt: "Regarde.", If: "prod.incidents > 2"})
	mustWatch(t, c, &rule{Name: "Autre", Source: sourceSchedule, Spec: ruleWhen{Schedule: "daily 08:00"}, Prompt: "Rien."})
	r := c.factReadersOf("prod")
	if !r.Summary || strings.Join(r.Rules, ",") != "Incidents" || strings.Join(r.Assistants, ",") != "Tri" {
		t.Fatalf("readers: %+v", r)
	}
}
