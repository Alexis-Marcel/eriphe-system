package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The rule engine (rule_engine.go): when, if, then, with no model.

func TestCondParseAndHold(t *testing.T) {
	facts := map[string]string{"context.place": "home", "prod.etat": "dégradé", "prod.incidents": "3",
		"now.day": "sat", "now.time": "18:30"}
	get := func(p string) (string, bool) { v, ok := facts[p]; return v, ok }
	for cond, want := range map[string]bool{
		"context.place = home":                                true,
		"context.place=home":                                  true,
		"context.place != home":                               false,
		"prod.incidents > 2 and now.time >= 18:00":            true,
		"prod.incidents > 5 or prod.etat in (dégradé, panne)": true,
		"prod.etat in ('en panne')":                           false,
		"now.day in (weekend) et context.place = home":        true,
		"now.day = weekday":                                   false,
		"context.focus = on":                                  false, // unknown: false
		"context.focus != on":                                 false, // unknown: false either way
		"prod.incidents > 10 or context.place = away":         false,
	} {
		e, err := parseCond(cond)
		if err != nil {
			t.Errorf("%q: %v", cond, err)
			continue
		}
		if got := e.holds(get); got != want {
			t.Errorf("%q = %v, want %v", cond, got, want)
		}
	}
	for _, bad := range []string{"", "home", "context.place", "context.place ~ home", "context.place = home and",
		"context.place in home", "\"context.place\" = home", "context.place = home but now.time > 10:00",
		strings.Repeat("a.b = 1 and ", 9) + "a.b = 1"} {
		if _, err := parseCond(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A condition names closed facts only, with values off their lists refused.
func TestCondPathsChecked(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	setAssistant(c, Assistant{Name: "prod", Role: assistantRoleAssistant, State: testState})
	r := func(cond string) *rule {
		return &rule{Name: "x", Source: sourceSchedule, Spec: ruleWhen{Schedule: "daily 07:00"}, Prompt: "y", If: cond}
	}
	for _, ok := range []string{"context.place = home", "prod.prod in (ok, dégradé)", "prod.mails > 2", "prod.incidents >= 1",
		"now.day in (weekday) and now.time < 20:00", "context.studio.dnd = on"} {
		if err := c.checkRule(r(ok)); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"prod.note = x", "context.place = garden", "prod.prod = cassé", "nobody.x = 1",
		"context.mood = good", "now.time > soon", "prod.vitesse > 1"} {
		if err := c.checkRule(r(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// A fait rule follows a closed fact, never a text nor the time.
	for _, bad := range []string{"prod.note", "now.time", "context.mood"} {
		if err := c.checkRule(&rule{Name: "x", Source: sourceFact, Spec: ruleWhen{Fact: bad}, Prompt: "y"}); err == nil {
			t.Errorf("fact %q accepted", bad)
		}
	}
}

func putPlace(t *testing.T, c *core, value string, now time.Time) {
	t.Helper()
	s, err := checkSignal(ctxInput{Kind: sigPlace, Value: value}, nil, "src:1", now)
	if err != nil {
		t.Fatal(err)
	}
	c.putSignals([]ctxSignal{s})
}

// « Quand je rentre » : a fait rule fires when context.place becomes home,
// once per change, and its condition holds it back when false.
func TestFactRuleFires(t *testing.T) {
	c, now := watchCore(t, at(t, "2026-11-09", "17:00"))
	w := mustWatch(t, c, &rule{Name: "Chauffage", Source: sourceFact, Spec: ruleWhen{Fact: "context.place", Becomes: "home"},
		Prompt: "Dis-moi la température.", If: "now.time >= 18:00"})
	putPlace(t, c, "away", *now)
	c.ruleTick(t.Context()) // the first look only notes it
	if runs := runsOf(t, c, w.ID); len(runs) != 0 {
		t.Fatalf("fired on its first look: %+v", runs)
	}
	putPlace(t, c, "home", *now)
	c.ruleTick(t.Context())
	if runs := runsOf(t, c, w.ID); len(runs) != 1 || runs[0].Outcome != runSkipped || !strings.Contains(runs[0].Detail, "away → home") {
		t.Fatalf("17:00, the condition is false: %+v", runs)
	}
	*now = now.Add(2 * time.Hour)
	putPlace(t, c, "away", *now)
	c.ruleTick(t.Context()) // not the value it waits for
	putPlace(t, c, "home", *now)
	c.ruleTick(t.Context())
	c.ruleTick(t.Context()) // no change since: nothing more
	runs := runsOf(t, c, w.ID)
	if len(runs) != 2 || runs[1].Outcome != runPending || runs[1].External {
		t.Fatalf("19:00: %+v", runs)
	}
	if r, got := c.pendingRun(); r == nil || got.ID != w.ID {
		t.Fatal("the brain does not get it")
	}
}

// then notify: eriphe-core sends the rule's own text, without the brain.
func TestNotifyRule(t *testing.T) {
	c, now := watchCore(t, at(t, "2026-11-09", "10:00"))
	w := mustWatch(t, c, &rule{Name: "Sauvegarde", Source: sourceEnd, Spec: ruleWhen{Ends: "session"},
		Act: actNotify, ActText: "Une session est finie.", ActLevel: levelNow, ActRef: "session"})
	c.ruleEnded("task", "la tâche t-1 s'est terminée (stopped)") // not what it waits for
	c.ruleEnded("session", "la session #2 s'est terminée (done)")
	runs := runsOf(t, c, w.ID)
	if len(runs) != 1 || runs[0].Outcome != runNotified || runs[0].Notification == 0 {
		t.Fatalf("runs: %+v", runs)
	}
	n, err := c.notificationByID(runs[0].Notification)
	if err != nil || n.Text != "Une session est finie." || n.Source != notifyFromRule {
		t.Fatalf("notification: %+v %v", n, err)
	}
	if r, _ := c.pendingRun(); r != nil {
		t.Fatal("a notify rule woke the brain")
	}
	_ = now
	// No text: refused.
	if _, err := c.addRule(&rule{Name: "x", Source: sourceEnd, Act: actNotify, State: watchActive, By: "owner"}); err == nil {
		t.Fatal("a notify rule without its text")
	}
}

// then assistant: the run waits for that assistant, never for the brain.
func TestAssistantRuleWaitsForIt(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	if _, err := c.addRule(&rule{Name: "x", Source: sourceEnd, Prompt: "Trie.", Act: "assistant:tri", State: watchActive, By: "owner"}); err == nil {
		t.Fatal("a rule waking an assistant that does not exist")
	}
	setAssistant(c, Assistant{Name: "tri", Role: assistantRoleAssistant})
	w := mustWatch(t, c, &rule{Name: "Tri", Source: sourceEnd, Prompt: "Trie les mails.", Act: "assistant:tri"})
	c.ruleEnded("task", "la tâche t-1 s'est terminée (stopped)")
	if r, _ := c.pendingRun(); r != nil {
		t.Fatal("the brain got an assistant's run")
	}
	if r, got := c.pendingRunFor("assistant:tri"); r == nil || got.ID != w.ID {
		t.Fatal("the assistant does not get it")
	}
}

// The brain's rule tool writes an event rule, with its when, if and then.
func TestRuleToolEvent(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	seq := ownerSays(c, "Quand je rentre à la maison après 18 h, préviens-moi de lancer le chauffage")
	out, err := c.brainRule(ruleIn{Kind: kindEvent, Why: "Demandé.", SourceRef: ref(seq), Event: &ruleEventIn{
		Name: "Retour maison", Source: "fact", Fact: "context.place", Becomes: "home", If: "now.time >= 18:00",
		Then: "notify", Text: "Pense au chauffage.", Level: "now"}})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := out["rule"].(eventRuleView)
	if !ok || v.State != watchActive {
		t.Fatalf("%+v", out)
	}
	w, _ := c.rule(v.ID)
	if w.Source != sourceFact || w.If != "now.time >= 18:00" || w.Act != actNotify || w.ActText != "Pense au chauffage." {
		t.Fatalf("rule: %+v", w)
	}
}

// Existing watches become rules at start: same ids, history and webhook
// tokens.
func TestWatchesMigrateToRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eriphe.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE rules`, `DROP TABLE rule_runs`,
		`CREATE TABLE watches (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, source TEXT NOT NULL,
		  spec TEXT NOT NULL DEFAULT '{}', prompt TEXT NOT NULL, brief INTEGER NOT NULL DEFAULT 0,
		  state TEXT NOT NULL, why TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '',
		  tainted TEXT NOT NULL DEFAULT '', by TEXT NOT NULL, next_at INTEGER, last_fired INTEGER,
		  mark INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, updated INTEGER NOT NULL,
		  poll TEXT NOT NULL DEFAULT '', hook TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE watch_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, watch_id INTEGER NOT NULL, fired INTEGER NOT NULL,
		  cause TEXT NOT NULL, due INTEGER, detail TEXT NOT NULL DEFAULT '', external INTEGER NOT NULL DEFAULT 0,
		  woke INTEGER, outcome TEXT NOT NULL, notification INTEGER, action TEXT NOT NULL DEFAULT '', ended INTEGER)`,
		`CREATE INDEX watch_runs_watch ON watch_runs (watch_id, id)`,
		`INSERT INTO watches (id, name, source, prompt, state, by, created, updated, hook) VALUES (7, 'Prod', 'alerte', 'Regarde.', 'active', 'owner', 1, 1, 'hash-of-token')`,
		`INSERT INTO watch_runs (watch_id, fired, cause, outcome) VALUES (7, 2, 'alert', 'silent')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var name, hook, act string
	if err := s.db.QueryRow(`SELECT name, hook, act FROM rules WHERE id = 7`).Scan(&name, &hook, &act); err != nil || name != "Prod" || hook != "hash-of-token" || act != "" {
		t.Fatalf("rule: %q %q %q %v", name, hook, act, err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM rule_runs WHERE rule_id = 7`).Scan(&n)
	if n != 1 {
		t.Fatalf("history: %d", n)
	}
}
