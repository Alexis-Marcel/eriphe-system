package main

import (
	"slices"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's weight comes from its links, its status and its freshness.
func TestNodeWeight(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	fresh, old := now.Add(-time.Hour), now.Add(-8*24*time.Hour)
	for _, c := range []struct {
		name            string
		depth, incoming int
		status          string
		updated         time.Time
		want            float64
	}{
		{"a subject", 0, 0, nodeIdle, fresh, 0.95},
		{"a part of it", 1, 0, nodeIdle, fresh, 0.55},
		{"deeper", 3, 0, nodeIdle, fresh, 0.42},
		{"two nodes link to it", 1, 2, nodeIdle, fresh, 0.63},
		{"a hub: ten link to it or sit under it", 0, 10, nodeIdle, fresh, 1.35},
		{"a hub: ten count", 0, 40, nodeIdle, fresh, 1.35},
		{"a hub below", 1, 10, nodeIdle, fresh, 0.95},
		{"waits for the user", 1, 0, nodeWaits, fresh, 0.9},
		{"runs", 1, 0, nodeRunning, fresh, 0.73},
		{"done", 1, 0, nodeDone, fresh, 0.45},
		{"untouched for a week", 1, 0, nodeIdle, old, 0.43},
		{"a hub that waits", 0, 10, nodeWaits, fresh, 1.7},
		{"at least", 5, 0, nodeDone, old, 0.2},
	} {
		if got := nodeWeight(c.depth, c.incoming, c.status, c.updated, now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if got := nodeWeight(9, 0, nodeDone, old, now); got != 0.2 {
		t.Errorf("deep, done and old: %v", got)
	}
}

// Depths come from the list itself: a parent out of it, or a loop, ends
// the climb.
func TestNodeDepths(t *testing.T) {
	d := nodeDepths([]nodestore.Summary{{ID: "a"}, {ID: "b", Parent: "a"}, {ID: "c", Parent: "b"}, {ID: "d", Parent: "gone"},
		{ID: "x", Parent: "y"}, {ID: "y", Parent: "x"}})
	if d["a"] != 0 || d["b"] != 1 || d["c"] != 2 || d["d"] != 0 || d["x"] != weightDepthMost {
		t.Fatalf("depths %v", d)
	}
}

// The graph's rows carry the weight, the same for the whole graph and for
// one row; the brain sees the nodes at work or waiting the heaviest first:
// a subject that waits before a part of it that waits, before a part that
// runs.
func TestWeightOrdersGraphAndBrain(t *testing.T) {
	c := graphCore(t)
	subject := mustNode(t, c, nodestore.New{Title: "Le site"})
	part := mustNode(t, c, nodestore.New{Title: "Le certificat", Parent: subject})
	runs := mustNode(t, c, nodestore.New{Title: "Le déploiement", Parent: subject})
	setStatus(t, c, part, nodeWaits)
	setStatus(t, c, runs, nodeRunning)
	setStatus(t, c, subject, nodeWaits)
	// Another subject that waits, changed after it, with nothing under it:
	// the one its parts sit under is heavier, and comes first.
	alone := mustNode(t, c, nodestore.New{Title: "Le garage"})
	setStatus(t, c, alone, nodeWaits)
	rows, err := c.graphRows()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		sum, _ := c.nodes.Get(r.ID)
		if one := c.graphRow(sum); one.Weight != r.Weight || r.Weight == 0 {
			t.Errorf("%s: %v in the graph, %v alone", r.ID, r.Weight, one.Weight)
		}
	}
	var order []string
	for _, x := range c.activeNodes(0) {
		order = append(order, x.ID)
	}
	if want := []string{subject, alone, part, runs}; !slices.Equal(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
}

// A node many others link to, or sit under, is heavy; when a node links to
// it, or moves under it or away, its row is sent again with its new
// weight, and so is the row of the node it left.
func TestLinkingResendsRows(t *testing.T) {
	c := graphCore(t)
	c.useNodes(c.nodes)
	hub := mustNode(t, c, nodestore.New{Title: "Ma garde-robe"})
	other := mustNode(t, c, nodestore.New{Title: "Le grenier"})
	weight := func(id string) float64 {
		sum, _ := c.nodes.Get(id)
		return c.graphRow(sum).Weight
	}
	resent := func(seq int64) map[string]bool {
		evs, _, _, _ := c.graphAfter(seq)
		out := map[string]bool{}
		for _, ev := range evs {
			if ev.Kind == graphEvNode {
				out[ev.Node] = true
			}
		}
		return out
	}
	alone := weight(hub)

	// A study linking to it by its text, a choice under it.
	seq, _ := c.graphHead()
	mustNode(t, c, nodestore.New{Title: "Étude des manteaux", Body: "Pour [[Ma garde-robe]]."})
	if !resent(seq)[hub] {
		t.Fatal("a link to it did not send its row again")
	}
	seq, _ = c.graphHead()
	choice := mustNode(t, c, nodestore.New{Title: "Choix des chaussures", Parent: hub})
	if !resent(seq)[hub] {
		t.Fatal("a child did not send its row again")
	}
	if w := weight(hub); w != alone+2*weightIncoming {
		t.Fatalf("weight %v, alone %v", w, alone)
	}
	for range 8 {
		mustNode(t, c, nodestore.New{Title: "Un choix", Parent: hub})
	}
	if w := weight(hub); w <= weight(other)+0.3 {
		t.Fatalf("a hub of ten %v, a fresh subject %v", w, weight(other))
	}

	// Moved away: both rows again, the one it left lighter.
	before := weight(hub)
	seq, _ = c.graphHead()
	if _, err := c.nodes.SetParent(choice, other, "user"); err != nil {
		t.Fatal(err)
	}
	if r := resent(seq); !r[hub] || !r[other] {
		t.Fatalf("moved: resent %v", r)
	}
	if weight(hub) >= before {
		t.Fatal("the node it left is no lighter")
	}
}
