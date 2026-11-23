package main

import (
	"testing"
	"time"
)

// declared is a tool's declaration as its server's _meta carries it.
func declared(t *testing.T, says map[string]any) *toolSays {
	t.Helper()
	s := saysOf(map[string]any{saysMetaKey: says})
	if s == nil {
		t.Fatalf("not read: %v", says)
	}
	return s
}

// A tool says its own call (as servers/calendar and servers/mail declare
// theirs): its sentence in the user's language, the item by its label
// (title and detail, marked when someone else wrote them), else the
// declared words, else counted; arguments by their declared words, a
// date as one; a part in [ ] only when its arguments are there. Never an
// id, never the account.
func TestToolSays(t *testing.T) {
	respond := declared(t, map[string]any{
		"text": map[string]string{"fr": "{response} {item}[ : « {comment} »]", "en": "{response} {item}[: “{comment}”]"},
		"item": map[string]string{"fr": "l'invitation", "en": "the invitation"},
		"values": map[string]any{"response": map[string]any{
			"accept":  map[string]string{"fr": "Accepter", "en": "Accept"},
			"decline": map[string]string{"fr": "Refuser", "en": "Decline"},
		}},
	})
	create := declared(t, map[string]any{"text": map[string]string{"fr": "Créer « {title} »[, {start}]", "en": "Create “{title}”[, {start}]"}})
	mails := map[string]any{"fr": []any{"1 mail", "{n} mails"}, "en": []any{"1 email", "{n} emails"}}
	move := declared(t, map[string]any{
		"text":   map[string]string{"fr": "Déplacer {item} vers {to}", "en": "Move {item} to {to}"},
		"items":  mails,
		"values": map[string]any{"to": map[string]any{"archive": map[string]string{"fr": "les archives", "en": "the archive"}}},
	})
	trash := declared(t, map[string]any{
		"text":  map[string]string{"fr": "Mettre {item} {junk}", "en": "Move {item} {junk}"},
		"items": mails,
		"values": map[string]any{"junk": map[string]any{
			"":     map[string]string{"fr": "à la corbeille", "en": "to the trash"},
			"true": map[string]string{"fr": "dans les indésirables", "en": "to junk"},
		}},
	})
	picnic := []itemLabel{{ID: "inv-1", Title: "Picnic", Detail: "Sam, lundi 14 h 10", External: true}}
	facture := []itemLabel{{ID: "m1", Title: "Facture", Detail: "Sam"}}
	two := []itemLabel{{ID: "m1", Title: "Facture"}, {ID: "m2", Title: "Devis"}}
	paris := time.FixedZone("Paris", 3600)
	for _, tc := range []struct {
		name   string
		says   *toolSays
		input  map[string]any
		labels []itemLabel
		n      int
		fr, en string
	}{
		// Its item bound by the source that listed it, whatever id the call
		// names (an invitation's event).
		{"respond", respond, map[string]any{"id": "ZTF8Y2FsfC91L2V2Lmljcw", "account": "sam@example.org", "response": "accept"}, picnic, 0,
			"Accepter « Picnic » · Sam, lundi 14 h 10 ⚠", "Accept “Picnic” · Sam, lundi 14 h 10 ⚠"},
		{"respond, a word", respond, map[string]any{"id": "x", "response": "decline", "comment": "Pas dispo"}, nil, 0,
			"Refuser l'invitation : « Pas dispo »", "Decline the invitation: “Pas dispo”"},
		{"create", create, map[string]any{"title": "Dentiste", "start": "2026-11-24T14:10", "account": "perso"}, nil, 0,
			"Créer « Dentiste », mardi 24 novembre, 14 h 10", "Create “Dentiste”, Tuesday, November 24, 14:10"},
		{"create, an offset", create, map[string]any{"title": "Dentiste", "start": "2026-11-24T13:00:00Z"}, nil, 0,
			"Créer « Dentiste », mardi 24 novembre, 14 h", "Create “Dentiste”, Tuesday, November 24, 14:00"},
		{"create, all day", create, map[string]any{"title": "Congés", "start": "2026-12-21"}, nil, 0,
			"Créer « Congés », lundi 21 décembre", "Create “Congés”, Monday, December 21"},
		{"create, no time", create, map[string]any{"title": "Dentiste"}, nil, 0, "Créer « Dentiste »", "Create “Dentiste”"},
		{"move one", move, map[string]any{"ids": []any{"m1"}, "to": "Archive"}, facture, 1,
			"Déplacer « Facture » · Sam vers les archives", "Move “Facture” · Sam to the archive"},
		{"move to a folder", move, map[string]any{"ids": []any{"m1", "m2"}, "to": "Factures"}, two, 2,
			"Déplacer 2 mails vers Factures", "Move 2 emails to Factures"},
		{"trash", trash, map[string]any{"ids": []any{"m1"}}, facture, 1,
			"Mettre « Facture » · Sam à la corbeille", "Move “Facture” · Sam to the trash"},
		{"trash, junk, unknown", trash, map[string]any{"ids": []any{"a", "b", "c"}, "junk": true}, nil, 3,
			"Mettre 3 mails dans les indésirables", "Move 3 emails to junk"},
	} {
		for lang, want := range map[string]string{"fr": tc.fr, "en": tc.en} {
			if got := tc.says.phrase(tc.input, tc.says.itemWords(tc.labels, tc.n, lang), tc.says.countWords(tc.n, lang), lang, paris); got != want {
				t.Errorf("%s (%s): %q, want %q", tc.name, lang, got, want)
			}
		}
	}
	// A language it does not declare: English.
	if got := create.phrase(map[string]any{"title": "X"}, "", "", "de", nil); got != "Create “X”" {
		t.Errorf("another language: %q", got)
	}
	// No declaration, or a broken one: none.
	if saysOf(nil) != nil || saysOf(map[string]any{saysMetaKey: "Créer"}) != nil || saysOf(map[string]any{saysMetaKey: map[string]any{"item": map[string]string{"fr": "x"}}}) != nil {
		t.Error("a declaration read from nothing")
	}
}

// A tool with only an English title, no declaration: its title, the item
// by its label (callPhrase); what is not a date is not one.
func TestTitleOnlyTool(t *testing.T) {
	got := callPhrase("mcp__box__file_away", "File away", map[string]any{"id": "f1"}, []itemLabel{{ID: "f1", Title: "Rapport"}}, "fr", 0)
	if got != "File away · « Rapport »" {
		t.Errorf("%q", got)
	}
	for _, s := range []string{"2026", "v2026-11-24", "Factures", "12345678"} {
		if w := dateWords(s, "fr", nil); w != "" {
			t.Errorf("%q said as a date: %q", s, w)
		}
	}
}
