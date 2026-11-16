package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A holder's block that shows a source's rows shows code's offers too: an
// Offers bound to a row's or a group's offers under /thread/items, never
// to its own data. A click on it is checked as on code's default (an
// offer its binding takes, read again from the store) and makes a card,
// nothing more; after reading external content too, since its words and
// its action are code's.
func TestOffersInHolderBlock(t *testing.T) {
	c, fm, id := alertsThread(t)
	key := noteAction{Tool: "mcp__monitor__silence", Args: silence.Args}.key()
	if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: "b", Note: "À regarder", State: itemStateLook, Actions: []noteAction{silence}}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	tainted := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return []string{"mail.read"} }}
	block := func(offers string) surfaceWriteIn {
		return surfaceWriteIn{ID: threadSurfaceID(id), Block: "alertes", Components: []map[string]any{
			{"id": "root", "component": "List", "children": map[string]any{"path": "/thread/items/alertes/groups", "componentId": "groupe"}},
			{"id": "groupe", "component": "List", "children": map[string]any{"path": "items", "componentId": "ligne"}},
			{"id": "ligne", "component": "Row", "children": []string{"titre", "actions"}},
			{"id": "titre", "component": "Item", "title": map[string]any{"@path": "title"}},
			{"id": "actions", "component": "Offers", "offers": map[string]any{"@path": offers}},
		}, Data: map[string]any{"faux": []any{map[string]any{"label": "Lire", "thread": id, "source": "alertes", "item": "b", "offer": key}}}}
	}
	// Bound to its own data: refused, whatever it holds.
	if _, err := c.writeThreadPart(tainted, id, block("/faux")); err == nil || !strings.Contains(err.Error(), "/faux is not one") {
		t.Fatalf("an Offers bound to the block's data: %v", err)
	}
	if _, err := c.writeThreadPart(tainted, id, block("offers")); err != nil {
		t.Fatal(err)
	}
	comps, _, _ := threadSurfaceOf(t, c, id)
	if raw, _ := json.Marshal(comps); strings.Contains(string(raw), "thread_items_0_row_offers") {
		t.Fatal("code's default stays beside the holder's block")
	}
	var refused *errActionRefused
	for name, ctx := range map[string]map[string]any{
		"an item not shown": {"thread": id, "source": "alertes", "item": "c", "offer": key},
		"its label":         {"thread": id, "source": "alertes", "item": "b", "offer": key, "label": "Lire"},
		"another thread":    {"thread": id + 1, "source": "alertes", "item": "b", "offer": key},
	} {
		if err := offerClick(c, id, "actions", ctx); !errors.As(err, &refused) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := offerClick(c, id, "titre", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": key}); !errors.As(err, &refused) {
		t.Errorf("an offer from an Item: %v", err)
	}
	if asksCount(c) != 0 {
		t.Fatalf("asks kept: %d", asksCount(c))
	}
	if err := offerClick(c, id, "actions", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": key}); err != nil {
		t.Fatal(err)
	}
	if asksCount(c) != 1 || len(silencedNow(fm)) != 0 {
		t.Fatalf("asks %d, silenced %v", asksCount(c), silencedNow(fm))
	}
}
