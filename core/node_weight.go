package main

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's weight is computed once, here, from what it is, never chosen
// by a model: its links (how deep it sits under its parents, and above
// all how many nodes link to it or sit under it: a hub, « Ma garde-robe »
// with its studies and choices, is heavy), its status (what waits for the user weighs most,
// what runs a little more, what is done less) and its freshness (a node
// untouched for a week weighs less). The graph draws by it, a bubble's
// size and light (its row's weight); the brain sees the nodes at work or
// waiting in its order (activeNodes).

const (
	// weightOld is when a node no longer counts as fresh.
	weightOld = 7 * 24 * time.Hour
	// weightIncomingMost is how many of the nodes that link to it or sit
	// under it count, weightIncoming what each adds.
	weightIncomingMost = 10
	weightIncoming     = 0.04
	// weightMost is the heaviest a node is.
	weightMost = 1.8
	// weightDepthMost is how far up its parents a depth is read.
	weightDepthMost = 32
)

// nodeWeight is a node's weight, between 0.15 and 1.8, to the hundredth:
// depth 0 for a node at the top, incoming the live nodes that link to it
// or sit under it.
func nodeWeight(depth, incoming int, status string, updated, now time.Time) float64 {
	v := 0.42
	switch depth {
	case 0:
		v = 0.95
	case 1:
		v = 0.55
	}
	v += weightIncoming * float64(min(incoming, weightIncomingMost))
	switch status {
	case nodeWaits:
		v += 0.35
	case nodeRunning:
		v += 0.18
	case nodeDone:
		v -= 0.1
	}
	if now.Sub(updated) > weightOld {
		v -= 0.12
	}
	return math.Round(max(0.15, min(weightMost, v))*100) / 100
}

// nodeDepths is each node's depth under its parents, from the list
// itself: a parent not in it ends the climb, and so does a loop.
func nodeDepths(xs []nodestore.Summary) map[string]int {
	parent := make(map[string]string, len(xs))
	for _, x := range xs {
		parent[x.ID] = x.Parent
	}
	out := make(map[string]int, len(xs))
	for _, x := range xs {
		d := 0
		for p := x.Parent; p != "" && d < weightDepthMost; p = parent[p] {
			if _, ok := parent[p]; !ok {
				break
			}
			d++
		}
		out[x.ID] = d
	}
	return out
}

// nodeDepth is one node's depth, read up its parents in the index, the
// archived ones left out as the graph leaves them.
func (c *core) nodeDepth(sum nodestore.Summary) int {
	d := 0
	for p := sum.Parent; p != "" && d < weightDepthMost; d++ {
		up, err := c.nodes.Get(p)
		if err != nil || !up.Archived.IsZero() { // as the graph lists them (nodeDepths)
			break
		}
		p = up.Parent
	}
	return d
}

// nodeIncoming is, for each node of the list, how many others of it link
// to it or sit under it (each counted once), as Store.Incoming counts
// them for one.
func nodeIncoming(xs []nodestore.Summary) map[string]int {
	out := make(map[string]int, len(xs))
	for _, x := range xs {
		to := map[string]bool{}
		if x.Parent != "" {
			to[x.Parent] = true
		}
		for _, l := range x.Links {
			to[l] = true
		}
		delete(to, x.ID)
		for t := range to {
			out[t]++
		}
	}
	return out
}

// byWeight sorts nodes the heaviest first, then the last changed, then by
// id; depths from nodeDepths, incoming from nodeIncoming.
func byWeight(xs []statusNode, depths, incoming map[string]int, now time.Time) {
	w := make(map[string]float64, len(xs))
	for _, x := range xs {
		w[x.ID] = nodeWeight(depths[x.ID], incoming[x.ID], x.Status, x.Updated, now)
	}
	slices.SortStableFunc(xs, func(a, b statusNode) int {
		return cmp.Or(cmp.Compare(w[b.ID], w[a.ID]), b.Updated.Compare(a.Updated), strings.Compare(a.ID, b.ID))
	})
}
