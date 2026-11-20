package main

import (
	"slices"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The incident: a question said in a subject's bar, about something else,
// needed a card. Said with turn_home, the turn is about no node: its card
// waits above the bar, wherever it talks, and its answer is filed in no
// node.
func TestTurnHomeCardWaitsAboveTheBar(t *testing.T) {
	c, answer := netCore(t, "Tu peux voir si le dernier film est dispo ?", "")
	focus, err := c.nodes.Create(nodestore.New{Title: "Une vue mails rangée par sections", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := focus.Node.Meta.ID
	// Said in the node's bar: it belongs to it, and the turn is about it.
	c.store.db.Exec(`UPDATE messages SET node = ? WHERE role = ?`, id, roleUser)
	c.turn.answersIn(id)
	c.turn.about(id)
	if out, _ := c.turnHome(); out["note"] != "This turn is about no node now, as at the home." {
		t.Fatalf("turn_home: %v", out)
	}
	if c.turnSource() != "" || !c.turn.answersAtHome() {
		t.Fatalf("the turn is still about %q", c.turnSource())
	}
	if _, err := c.proposeView(proposeViewIn{Title: "Mes films", Body: "Les films.\n"}, nil); err != nil {
		t.Fatal(err)
	}
	answer("Tu me donnes ton accord pour films ?\n\nJe lance la recherche dès que tu valides la carte.")
	ref := proposalRef(cardViewKind, 1)
	if nodes := c.cardNodes(ref); len(nodes) != 0 {
		t.Errorf("the card waits in the unrelated node: %v", nodes)
	}
	w := &web{c: c}
	if ex, _ := w.exchangeFor(id, "", "", false); !slices.Equal(ex.Cards, []string{ref}) {
		t.Errorf("the card is not above the bar: %v", ex.Cards)
	}
	if m := lastBrainMessage(t, c); m.Filed != "" {
		t.Errorf("the answer was filed in %s", m.Filed)
	}
	// Said at the home, turn_home changes nothing.
	c2, _ := netCore(t, "Bonjour", "")
	if out, _ := c2.turnHome(); c2.turn.answersAtHome() {
		t.Errorf("turn_home at the home: %v", out)
	}
}

// A card raised in a turn that answers the user, linked to a node, opens
// that node's page beside what is open: the user sees it at once.
func TestTurnCardShowsItsNode(t *testing.T) {
	c, answer := netCore(t, "Garde un œil sur mes films", "")
	n, err := c.nodes.Create(nodestore.New{Title: "Films", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Node.Meta.ID
	c.turnNode(id)
	if _, err := c.proposeView(proposeViewIn{Title: "Mes films", Body: "Les films.\n"}, nil); err != nil {
		t.Fatal(err)
	}
	if nodes := c.cardNodes(proposalRef(cardViewKind, 1)); !slices.Equal(nodes, []string{id}) {
		t.Fatalf("the card's nodes: %v", nodes)
	}
	if x, _ := c.lastShown(); x.Node == id {
		t.Fatalf("shown before the turn's answer: %+v", x)
	}
	answer("Dis oui sur sa carte.")
	if x, ok := c.lastShown(); !ok || x.Node != id || x.Talk {
		t.Errorf("the card's node is not shown: %+v", x)
	}
}
