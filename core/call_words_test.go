package main

import (
	"context"
	"slices"
	"testing"
	"time"
)

// What a call of a tool that declares nothing does to what, in plain
// words, nothing of core's for its name: the tool's title, else its name;
// the items counted as « éléments ».
func TestCallPhrase(t *testing.T) {
	ids := func(xs ...string) []any {
		out := []any{}
		for _, x := range xs {
			out = append(out, x)
		}
		return out
	}
	for _, tc := range []struct {
		tool, title string
		input       map[string]any
		labels      []itemLabel
		lang, want  string
	}{
		// No verb of core's for a usual name: the title, then the name.
		{"mcp__box__trash", "", map[string]any{"account": "perso", "ids": ids("a", "b")}, nil, "fr", "Utiliser « trash » sur 2 éléments"},
		{"mcp__box__trash", "Throw away", map[string]any{"ids": ids("a")}, nil, "en", "Throw away · 1 item"},
		{"mcp__box__move", "Ranger", map[string]any{"ids": ids("a", "b"), "to": "Factures"}, nil, "fr", "Ranger · 2 éléments → Factures"},
		// A list its name says is of ids, any name.
		{"mcp__box__drop", "Retirer", map[string]any{"thing_ids": ids("e1", "e2", "e3")}, nil, "fr", "Retirer · 3 éléments"},
		// The source that lists them says no kind: « éléments ».
		{"mcp__box__remove", "", map[string]any{"ids": ids("f1", "f2")}, []itemLabel{{ID: "f1", Server: "box"}, {ID: "f2", Server: "box"}}, "fr", "Utiliser « remove » sur 2 éléments"},
		// One item it names, by a title: said by itself.
		{"mcp__box__reply", "Envoyer la réponse", map[string]any{"id": "a1", "body": "Merci"}, []itemLabel{{ID: "a1", Title: "Devis", Detail: "Sam"}}, "fr", "Envoyer la réponse · « Devis » · Sam"},
		// Nothing by id: the action alone.
		{"mcp__box__send", "Envoyer un mail", map[string]any{"to": "sam@example.org"}, nil, "fr", "Envoyer un mail"},
		{"mcp__ops__rollback", "", map[string]any{"version": "2.0.0"}, nil, "fr", "Utiliser « rollback »"},
	} {
		if got := callPhrase(tc.tool, tc.title, tc.input, tc.labels, tc.lang, 0); got != tc.want {
			t.Errorf("%s %v: %q, want %q", tc.tool, tc.input, got, tc.want)
		}
	}
	// A count given: calls that do the same to other items, together.
	if got := callPhrase("mcp__box__trash", "Jeter", map[string]any{"ids": ids("a")}, nil, "fr", 4); got != "Jeter · 4 éléments" {
		t.Errorf("counted: %q", got)
	}
}

// The items a call acts on, by their titles: five at most, then how many
// more; a title someone else wrote keeps its quiet mark.
func TestLabelLines(t *testing.T) {
	var ls []itemLabel
	for _, s := range []string{"Un", "Deux", "Trois", "Quatre", "Cinq", "Six", "Sept"} {
		ls = append(ls, itemLabel{ID: s, Title: s, Detail: "Sam"})
	}
	got := labelLines(ls, 9, "fr")
	if len(got) != 6 || got[0] != "« Un » · Sam" || got[5] != "et 4 autres" {
		t.Errorf("lines %q", got)
	}
	if got := labelLines(ls[:2], 2, "en"); !slices.Equal(got, []string{"“Un” · Sam", "“Deux” · Sam"}) {
		t.Errorf("two %q", got)
	}
	if got := labelLine(itemLabel{ID: "x", Title: "Payez 1,99 €", External: true}, "fr"); got != "« Payez 1,99 € » ⚠" {
		t.Errorf("external %q", got)
	}
	if got := labelLines(nil, 3, "fr"); len(got) != 0 {
		t.Errorf("no title known, no line: %q", got)
	}
}

// rawCall is a card's exact call among its fields: the last one, one
// click further than its Détails; nil when there is none.
func rawCall(fs []cardField) *cardField {
	if len(fs) == 0 || !fs[len(fs)-1].Raw || !fs[len(fs)-1].Detail {
		return nil
	}
	return &fs[len(fs)-1]
}

// brainCallOn has the brain call a tool held on a card.
func brainCallOn(t *testing.T, c *core, pid, tool string, input map[string]any) {
	t.Helper()
	c.store.addEvents("b-1", []EventRow{{Seq: len(pid), Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": pid, "tool_name": tool, "input": input}}})
	if err := c.askBrainAccess(context.Background(), &TaskRow{ID: "b-1"}, pid, tool, input, nil); err != nil {
		t.Fatal(err)
	}
}
