package main

import (
	"testing"
)

// Every yes button says what it does, its verb first, never a bare
// « Oui »: each kind of card has its verb, in each language, and a kind
// added without one fails here.
func TestEveryCardHasItsVerb(t *testing.T) {
	for _, kind := range proposalKinds {
		key, ok := kindVerbs[kind]
		if !ok {
			t.Errorf("kind %s has no verb", kind)
			continue
		}
		for _, lang := range []string{"fr", "en"} {
			v := proposal{Kind: kind}.VerbIn(lang)
			if v == "" || v == key || bareYes(v) {
				t.Errorf("kind %s in %s: yes says %q", kind, lang, v)
			}
			if _, ok := webStrings[lang][key]; !ok {
				t.Errorf("%s has no %s", lang, key)
			}
		}
	}
	// A verb of its own that says nothing gives way to its kind's.
	p := proposal{Kind: cardCalls, verb: func(string) string { return "Oui" }}
	if v := p.VerbIn("fr"); v != "Faire" {
		t.Errorf("a bare yes stays: %q", v)
	}
}

// A call's verb is its sentence's first word when it reads as one, or
// what its tool declares; for several items, how many.
func TestCallVerbs(t *testing.T) {
	for s, want := range map[string]string{
		"Envoyer un message à Sam":        "Envoyer",
		"Mettre 102 mails à la corbeille": "Mettre",
		"Répondre « peut-être » à Sam":    "Répondre",
		"« Dentiste » jeudi":              "",
		"envoyer":                         "",
		"":                                "",
		"Créer « Dentiste », jeudi":       "Créer",
	} {
		if got := firstVerb(s); got != want {
			t.Errorf("firstVerb(%q) = %q, want %q", s, got, want)
		}
	}
	c, _, r := mailView(t)
	keep(c, r.Task, "p1", "mcp__monitor__move", moveMail("sam@example.org", "archive", "m1"))
	endTurn(t, c, r.Task, "Un mail à archiver.")
	calls := cardsOf(c, cardCalls)
	if len(calls) != 1 {
		t.Fatalf("cards %+v", calls)
	}
	if v := calls[0].VerbIn("fr"); v != "Déplacer" {
		t.Errorf("one call's verb %q", v)
	}
	// Declared by its tool: its words, then how many.
	says := &toolSays{Text: map[string]string{"fr": "Mettre {item} à la corbeille"}, Verb: map[string]string{"fr": "Jeter", "en": "Trash"}}
	if v := inLang(says.Verb, "fr"); v != "Jeter" {
		t.Fatal(v)
	}
	if got := countedVerb("fr", "Jeter", 102); got != "Jeter les 102" || countedVerb("en", "Trash", 2) != "Trash all 2" {
		t.Errorf("%q", got)
	}
}
