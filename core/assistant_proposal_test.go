package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Creation by Eriphe (assistant_proposal.go, assistant_templates.go): a
// template, a creation card, the user's yes; a rule first.

// Each assistant template makes a valid assistant and wake rules; each
// rule template is a valid rule once its tool is filled.
func TestTemplatesHold(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	for _, tp := range assistantTemplates {
		switch tp.Kind {
		case templateAssistant:
			in := fillFromTemplate(proposeAssistantIn{}, tp)
			spec := Assistant{Name: in.Name, Role: assistantRoleAssistant, Instructions: in.Mission, Powers: in.Powers, Views: in.Views}
			for _, f := range in.State {
				spec.State = append(spec.State, f.field())
			}
			if err := checkAssistant(c.conf(), in.Name, spec); err != nil {
				t.Errorf("%s: %v", tp.Name, err)
			}
			if len(in.Wake) == 0 || len(in.Justification) == 0 {
				t.Errorf("%s: no wake or no reason", tp.Name)
			}
			for _, ev := range in.Wake {
				if _, err := c.wakeRule(ev, ""); err != nil {
					t.Errorf("%s wake: %v", tp.Name, err)
				}
			}
		case templateRule:
			if tp.Rule == nil || tp.Rule.Source != sourcePoll {
				t.Errorf("%s: no event", tp.Name)
			}
		default:
			t.Errorf("%s: kind %q", tp.Name, tp.Kind)
		}
	}
}

// A rule first: no reason, no assistant; a rule's template is a rule.
func TestProposeAssistantNeedsAReason(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	_, err := c.proposeAssistant(proposeAssistantIn{Name: "prod", Mission: "Surveille la prod.",
		Wake: []ruleEventIn{{Name: "x", Source: sourceSchedule, Schedule: "daily 07:00", Prompt: "Regarde."}}})
	if err == nil || !strings.Contains(err.Error(), "rule") {
		t.Fatalf("an assistant without its reason: %v", err)
	}
	if _, err := c.proposeAssistant(proposeAssistantIn{Template: "surveiller-service", Name: "prod", Why: "x", Justification: []string{whyWake}}); err == nil {
		t.Fatal("a rule's template made an assistant")
	}
	if _, err := c.proposeAssistant(proposeAssistantIn{Template: "tri-mails", Why: "x", Justification: []string{"because"}}); err == nil {
		t.Fatal("a reason off the list")
	}
	// A wake does nothing but wake it.
	if _, err := c.proposeAssistant(proposeAssistantIn{Template: "tri-mails", Why: "Il lit les mails à ta place.", Tools: []string{"mail"},
		Wake: []ruleEventIn{{Name: "x", Source: sourceSchedule, Schedule: "daily 07:00", Prompt: "Trie.", Then: actNotify, Text: "Coucou"}}}); err == nil {
		t.Fatal("a wake that notifies")
	}
}

// From the mail template: a card with its six blocks; nothing exists until
// the yes; the yes makes the assistant and its rule; a no makes nothing.
func TestProposeAssistantOnCard(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	seq := ownerSays(c, "Trie mes mails toutes les heures, que je voie les urgents")
	out, err := c.proposeAssistant(proposeAssistantIn{Template: "tri-mails", Tools: []string{"mail"}, SourceRef: ref(seq),
		Why: "Il lit les mails à ta place : leur contenu ne passe jamais par moi."})
	if err != nil {
		t.Fatal(err)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "Only proposed") {
		t.Fatalf("note: %+v", out)
	}
	if _, ok := c.conf().Assistants["tri-mails"]; ok {
		t.Fatal("made before the yes")
	}
	id := out["proposal"].(int64)
	p := c.proposalOf(cardNewAssistant, id)
	if !p.Pending || !strings.Contains(p.Text("fr"), "Tri des mails") {
		t.Fatalf("card: %+v %q", p, p.Text("fr"))
	}
	got := map[string]string{}
	for _, f := range p.FieldsIn("en") {
		got[f.Name] = f.Value
	}
	for _, block := range []string{"Assistant", "Facts", "Events", "Rules", "Channels", "Tools", "Why an assistant", "Where to see it", "Estimated cost"} {
		if got[block] == "" {
			t.Errorf("block %s missing: %+v", block, got)
		}
	}
	if !strings.Contains(got["Facts"], "mails (rows: id, de, sujet") || !strings.Contains(got["Rules"], "the assistant Tri des mails wakes up") ||
		!strings.Contains(got["Tools"], "mail") || !strings.Contains(got["Estimated cost"], "wakes a month") {
		t.Errorf("blocks: %+v", got)
	}
	if _, err := c.proposeAssistant(proposeAssistantIn{Template: "tri-mails", Tools: []string{"mail"}, Why: "x"}); err == nil {
		t.Fatal("proposed twice")
	}

	if err := c.decideProposal(context.Background(), cardNewAssistant, id, true); err != nil {
		t.Fatal(err)
	}
	a, ok := c.conf().Assistants["tri-mails"]
	if !ok || a.Role != assistantRoleAssistant || len(a.State) != 1 || len(a.Views) != 3 || a.DisplayName != "Tri des mails" {
		t.Fatalf("assistant: %+v", a)
	}
	var wake *rule
	rs, _ := c.rules()
	for _, r := range rs {
		if r.ThenAssistant() == "tri-mails" {
			wake = r
		}
	}
	if wake == nil || wake.State != watchActive || wake.Source != sourceSchedule || wake.By != "owner" {
		t.Fatalf("its rule: %+v", wake)
	}
	if c.proposalOf(cardNewAssistant, id).Pending {
		t.Fatal("still waits")
	}

	// Another, refused: nothing made.
	out, err = c.proposeAssistant(proposeAssistantIn{Template: "veille-techno", Name: "veille",
		Mission: "Tu fais la veille de {{user}} sur Go et SQLite.", Why: "Il lit le web à ta place."})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.decideProposal(context.Background(), cardNewAssistant, out["proposal"].(int64), false); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.conf().Assistants["veille"]; ok {
		t.Fatal("made on a no")
	}
}

// An assistant reads others' facts only when lent them.
func TestLentFacts(t *testing.T) {
	c, a := stateCore(t)
	a.Disabled = false
	setAssistant(c.core, a)
	now := time.Now()
	if _, err := c.setState(c.conf().Assistants["prod"], setStateIn{Values: map[string]any{"prod": "ok"}}, nil, now); err != nil {
		t.Fatal(err)
	}
	tri := Assistant{Name: "tri", Role: assistantRoleAssistant, Facts: []string{"prod"}}
	if err := checkAssistant(c.conf(), "tri", tri); err != nil {
		t.Fatal(err)
	}
	out, err := c.lentFacts(tri, factsIn{}, now)
	if err != nil {
		t.Fatal(err)
	}
	views := out["facts"].([]factView)
	if len(views) == 0 {
		t.Fatal("nothing read")
	}
	for _, v := range views {
		if !strings.HasPrefix(v.Fact, "prod.") {
			t.Fatalf("read a fact not lent: %+v", v)
		}
	}
	if _, err := c.lentFacts(tri, factsIn{Owner: "context"}, now); err == nil {
		t.Fatal("read an owner not lent")
	}
	if out, _ := c.lentFacts(Assistant{Name: "x", Facts: []string{"context"}}, factsIn{}, now); len(out["facts"].([]factView)) != 0 {
		t.Fatal("context read through another's lending")
	}
	if err := checkAssistant(c.conf(), "tri", Assistant{Role: assistantRoleAssistant, Facts: []string{"tri"}}); err == nil {
		t.Fatal("its own facts lent")
	}
	if err := checkAssistant(c.conf(), "tri", Assistant{Role: assistantRoleAssistant, Facts: []string{"eriphe"}}); err == nil {
		t.Fatal("a reserved owner lent")
	}
}

// The cost of a wake is its model's turns, where they said.
func TestTurnCost(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	if _, ok := c.store.turnCost("opus", time.Unix(0, 0)); ok {
		t.Fatal("a cost from nothing")
	}
	c.store.insertTask(&TaskRow{ID: "t-a", Node: "pc", Runtime: "claude-code", Model: "opus", State: stateIdle})
	now := time.Now()
	c.store.addEvents("t-a", []EventRow{
		{Seq: 1, Time: now, Type: "turn_end", Data: map[string]any{"cost_usd": 0.02}},
		{Seq: 2, Time: now, Type: "turn_end", Data: map[string]any{"cost_usd": 0.04}},
	})
	if got, ok := c.store.turnCost("opus", now.Add(-time.Hour)); !ok || got < 0.029 || got > 0.031 {
		t.Fatalf("cost: %v %v", got, ok)
	}
}
