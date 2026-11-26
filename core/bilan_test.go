package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A view as a bilan (docs/node-format.md): rows that carry the card that
// waits on them, Lists with several filters, settled items that leave,
// decided cards that leave the page's foot, and the view's skill woken by
// the user's hand.

// pendingAsks: what still asks something at a page's foot.
func TestPendingAsks(t *testing.T) {
	for _, tc := range []struct {
		v    proposalView
		asks bool
	}{
		{proposalView{State: cardPending}, true},
		{proposalView{State: cardApproved, Exec: callRunning}, true},
		{proposalView{State: cardApproved, Exec: callFailed}, false}, // decided: its ⋯ menu lists it
		{proposalView{State: cardGone}, false},
		{proposalView{State: cardApproved, Exec: callDone}, false},
		{proposalView{State: cardApproved}, false},
		{proposalView{State: cardRejected}, false},
	} {
		if got := pendingAsks(tc.v); got != tc.asks {
			t.Errorf("%+v: %v", tc.v, got)
		}
	}
	raw, _ := json.Marshal(nodePage{})
	if !strings.Contains(string(raw), `"settled"`) {
		t.Error("a page has no settled cards field")
	}
}

// A long text argument is lifted out of Détails, named by the tool's
// schema's title, else in words for a common name, else by its own; a
// short one stays.
func TestCallShownNames(t *testing.T) {
	c, _, _ := viewCore(t)
	long := strings.Repeat("Une phrase assez longue. ", 6)
	in := map[string]any{"id": "m1", "body": long, "note": "court"}
	a := accessQuestion{Call: "mcp__monitor__send " + mustJSON(callFields(in)), Input: mustJSON(in)}
	fs, names := c.callShown(a, "fr")
	if len(fs) != 1 || fs[0].Name != "Texte" || fs[0].Value != long || !slices.Equal(names, []string{"body"}) {
		t.Errorf("fr: %+v %v", fs, names)
	}
	if fs, _ := c.callShown(a, "en"); len(fs) != 1 || fs[0].Name != "Text" {
		t.Errorf("en: %+v", fs)
	}
	b := accessQuestion{Call: "mcp__monitor__send []", Input: mustJSON(map[string]any{"id": "m1", "poem": "un\ndeux"})}
	if fs, _ := c.callShown(b, "fr"); len(fs) != 1 || fs[0].Name != "poem" {
		t.Errorf("by its name: %+v", fs)
	}
	r := accessQuestion{Call: "mcp__monitor__reply []", Input: mustJSON(map[string]any{"id": "m1", "text": long})}
	if fs, _ := c.callShown(r, "fr"); len(fs) != 1 || fs[0].Name != "Ta réponse" {
		t.Errorf("by its schema's title: %+v", fs)
	}
	if d := callDetail(a, []string{"body"}); strings.Contains(d.Value, "phrase") || !strings.Contains(d.Value, "court") {
		t.Errorf("details %+v", d)
	}
}
