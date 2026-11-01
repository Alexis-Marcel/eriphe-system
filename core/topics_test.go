package main

import (
	"strings"
	"testing"
)

// One form per ref: case, accents, spaces, French kind names and the
// plural of a one-word topic; an entity's slug kept as written.
func TestCanonRef(t *testing.T) {
	for in, want := range map[string]string{
		"project:getout":          "project:getout",
		" Project:Getout ":        "project:getout",
		"projet:Getout":           "project:getout",
		"Projet : Getout : Prod":  "project:getout:prod",
		"person:Léa Martin":       "person:lea-martin",
		"personne:jules":          "person:jules",
		"Factures":                "facture",
		"facture":                 "facture",
		"topic:factures":          "facture",
		"sujet:Factures":          "facture",
		"invoices":                "invoice",
		"devis":                   "devis",
		"colis":                   "colis",
		"status":                  "status",
		"notes de frais":          "notes-de-frais",
		"source:mails":            "source:mails",
		"task:t-12":               "task:t-12",
		"Projet X !":              "",
		"":                        "",
		"project:getout:releases": "project:getout:releases",
	} {
		if got := canonRef(in); got != want {
			t.Errorf("canonRef(%q) = %q, want %q", in, got, want)
		}
	}
	// Idempotent.
	for _, s := range []string{"Factures", "projet:Getout", "person:Léa Martin", "notes de frais"} {
		if once := canonRef(s); canonRef(once) != once {
			t.Errorf("canonRef not idempotent on %q", s)
		}
	}
}

// A new label of a known word family is kept under the known topic;
// another one is new; a sub-ref of a known topic is known.
func TestReuseTopic(t *testing.T) {
	known := []topicView{{Ref: "facture", Rule: true}, {Ref: "project:getout"}, {Ref: "person:lina"}}
	for _, c := range []struct {
		label, ref string
		known      bool
	}{
		{"factures", "facture", true},
		{"Facturation", "facture", true},
		{"project:getout:prod", "project:getout:prod", true},
		{"projet:getout", "project:getout", true},
		{"person:Lina", "person:lina", true},
		{"invoices", "invoice", false},
		{"project:site", "project:site", false},
	} {
		ref, ok := reuseTopic(c.label, known)
		if ref != c.ref || ok != c.known {
			t.Errorf("reuseTopic(%q) = %q %v, want %q %v", c.label, ref, ok, c.ref, c.known)
		}
	}
	// Two known topics of one family: no guess.
	if ref, ok := reuseTopic("facturation", []topicView{{Ref: "facture"}, {Ref: "facturer"}}); ok || ref != "facturation" {
		t.Errorf("ambiguous family mapped to %q", ref)
	}
}

// A rule kept before the vocabulary still matches the canonical label,
// and the brain's notify lands under the known topic, so the rule holds.
func TestTopicRuleCanonical(t *testing.T) {
	rules := []notifyRule{{ID: 1, Kind: ruleTopic, Match: "factures", Outcome: levelNever, State: ruleActive}}
	for _, ref := range []string{"facture", "Factures", "topic:facture"} {
		if r := topicRule(rules, ref); r == nil || r.ID != 1 {
			t.Errorf("rule factures does not match %q", ref)
		}
	}
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	ownerSays(c, "Ne me préviens plus des factures")
	if _, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "facture", Outcome: levelNever, State: ruleActive, By: "user"}); err != nil {
		t.Fatal(err)
	}
	out, err := c.brainNotify(notifyIn{Text: "Une facture est arrivée.", Level: levelNow, Why: "Un mail de facture.", Ref: "Facturation"})
	if err != nil || out["outcome"] != levelNever || out["ref"] != "facture" {
		t.Fatalf("notify under a family label: %v %v", out, err)
	}
	if out["topic"] == nil {
		t.Error("no note that the label was mapped to the known topic")
	}
	out, _ = c.brainNotify(notifyIn{Text: "Le colis part demain.", Level: levelBrief, Why: "Suivi.", Ref: "livraisons"})
	if note, _ := out["topic"].(string); !strings.Contains(note, "New topic livraison") || !strings.Contains(note, "facture") {
		t.Errorf("a new topic is not flagged with the known ones: %v", out)
	}
	// A rule the brain writes for a family label uses the known topic, and
	// replaces the older one instead of adding a twin.
	seq := ownerSays(c, "En fait préviens-moi des factures au brief")
	got, err := c.brainRule(ruleIn{Match: "facturation", Outcome: levelBrief, Why: "Demandé.", SourceRef: ref(seq)})
	if err != nil || got["rule"].(ruleView).Match != "facture" {
		t.Fatalf("rule: %v %v", got, err)
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 || rs[0].Outcome != levelBrief {
		t.Fatalf("rules now %+v", rs)
	}
}

// The known topics gather the rules', the notifications' and the
// memory's subjects, in one form, without ids.
func TestKnownTopics(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	ownerSays(c, "ok")
	c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "project:getout", Outcome: levelNow, State: ruleActive, By: "user"})
	c.brainNotify(notifyIn{Text: "x", Level: levelBrief, Why: "y", Ref: "Factures"})
	c.brainNotify(notifyIn{Text: "x", Level: levelBrief, Why: "y", Ref: "task:t-1"})
	seq := ownerSays(c, "Lina est ma sœur")
	if _, err := c.brainRemember(rememberIn{Text: "Lina est la sœur de Sam.", Kind: kindPerson, Subject: "person:Lina", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, tp := range c.knownTopics() {
		refs = append(refs, tp.Ref)
	}
	got := strings.Join(refs, " ")
	for _, want := range []string{"project:getout", "facture", "person:lina"} {
		if !strings.Contains(got, want) {
			t.Errorf("known topics %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "task:") || refs[0] != "project:getout" {
		t.Errorf("known topics %q: ids listed, or the ruled one not first", got)
	}
}
