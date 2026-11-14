package main

import (
	"strings"
	"testing"
)

// A new label of a known word family is kept under the known topic;
// another one is new; a sub-ref of a known topic is known.
func TestReuseTopic(t *testing.T) {
	known := []topicView{{Ref: "topic:facture", Rule: true}, {Ref: "project:getout"}, {Ref: "person:lina"}}
	for _, c := range []struct {
		label, ref string
		known      bool
	}{
		{"factures", "topic:facture", true},
		{"Facturation", "topic:facture", true},
		{"project:getout:prod", "project:getout:prod", true},
		{"projet:getout", "project:getout", true},
		{"person:Lina", "person:lina", true},
		{"invoices", "topic:invoice", false},
		{"project:site", "project:site", false},
	} {
		ref, ok := reuseTopic(c.label, known)
		if ref != c.ref || ok != c.known {
			t.Errorf("reuseTopic(%q) = %q %v, want %q %v", c.label, ref, ok, c.ref, c.known)
		}
	}
	// Two known topics of one family: no guess.
	if ref, ok := reuseTopic("facturation", []topicView{{Ref: "topic:facture"}, {Ref: "topic:facturer"}}); ok || ref != "topic:facturation" {
		t.Errorf("ambiguous family mapped to %q", ref)
	}
}

// A rule kept before the vocabulary still matches the canonical label,
// and the brain's notify lands under the known topic, so the rule holds.
func TestTopicRuleCanonical(t *testing.T) {
	rules := []notifyRule{{ID: 1, Kind: ruleTopic, Match: "factures", Ceiling: 0, State: ruleActive}}
	for _, ref := range []string{"facture", "Factures", "topic:facture"} {
		if r := topicRule(rules, ref); r == nil || r.ID != 1 {
			t.Errorf("rule factures does not match %q", ref)
		}
	}
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	ownerSays(c, "Ne me préviens plus des factures")
	if _, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "facture", Ceiling: 0, State: ruleActive, By: "user"}); err != nil {
		t.Fatal(err)
	}
	out, err := c.brainNotify(notifyIn{Text: "Une facture est arrivée.", Weight: 5, Why: "Un mail de facture.", Ref: "Facturation"})
	if err != nil || out["outcome"] != levelNever || out["subject"] != "topic:facture" {
		t.Fatalf("notify under a family label: %v %v", out, err)
	}
	if out["topic"] == nil {
		t.Error("no note that the label was mapped to the known topic")
	}
	out, _ = c.brainNotify(notifyIn{Text: "Le colis part demain.", Weight: 2, Why: "Suivi.", Ref: "livraisons"})
	if note, _ := out["topic"].(string); !strings.Contains(note, "New topic topic:livraison") || !strings.Contains(note, "facture") {
		t.Errorf("a new topic is not flagged with the known ones: %v", out)
	}
	// A rule the brain writes for a family label uses the known topic, and
	// replaces the older one instead of adding a twin.
	seq := ownerSays(c, "En fait préviens-moi des factures au brief")
	got, err := c.brainRule(ruleIn{Match: "facturation", Ceiling: ptr(2), Why: "Demandé.", SourceRef: ref(seq)})
	if err != nil || got["rule"].(ruleView).Match != "topic:facture" {
		t.Fatalf("rule: %v %v", got, err)
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 || rs[0].Ceiling != 2 {
		t.Fatalf("rules now %+v", rs)
	}
}

// The known topics gather the rules', the notifications' and the
// memory's subjects, in one form, without ids.
func TestKnownTopics(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	ownerSays(c, "ok")
	c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "project:getout", Floor: 5, Ceiling: 5, State: ruleActive, By: "user"})
	c.brainNotify(notifyIn{Text: "x", Weight: 2, Why: "y", Ref: "Factures"})
	c.brainNotify(notifyIn{Text: "x", Weight: 2, Why: "y", Ref: "task:t-1"})
	seq := ownerSays(c, "Lina est ma sœur")
	if _, err := c.brainRemember(rememberIn{Text: "Lina est la sœur de Sam.", Kind: kindPerson, Subject: "person:Lina", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, tp := range c.knownTopics() {
		refs = append(refs, tp.Ref)
	}
	got := strings.Join(refs, " ")
	for _, want := range []string{"project:getout", "topic:facture", "person:lina"} {
		if !strings.Contains(got, want) {
			t.Errorf("known topics %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "task:") || refs[0] != "project:getout" {
		t.Errorf("known topics %q: ids listed, or the ruled one not first", got)
	}
}
