package main

import (
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's weight comes from its links, its status and its freshness.
func TestNodeWeight(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	fresh, old := now.Add(-time.Hour), now.Add(-8*24*time.Hour)
	for _, c := range []struct {
		name         string
		depth, links int
		status       string
		updated      time.Time
		want         float64
	}{
		{"a subject", 0, 0, nodeIdle, fresh, 0.95},
		{"a part of it", 1, 0, nodeIdle, fresh, 0.55},
		{"deeper", 3, 0, nodeIdle, fresh, 0.42},
		{"linked", 1, 2, nodeIdle, fresh, 0.61},
		{"linked a lot: five count", 1, 40, nodeIdle, fresh, 0.7},
		{"waits for the user", 1, 0, nodeWaits, fresh, 0.9},
		{"runs", 1, 0, nodeRunning, fresh, 0.73},
		{"done", 1, 0, nodeDone, fresh, 0.45},
		{"untouched for a week", 1, 0, nodeIdle, old, 0.43},
		{"at most", 0, 9, nodeWaits, fresh, 1.3},
		{"at least", 5, 0, nodeDone, old, 0.2},
	} {
		if got := nodeWeight(c.depth, c.links, c.status, c.updated, now); got != c.want {
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
	if len(order) != 3 || order[0] != subject || order[1] != part || order[2] != runs {
		t.Fatalf("order %v, want %v", order, []string{subject, part, runs})
	}
}
