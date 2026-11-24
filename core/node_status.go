package main

import (
	"log"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's status (#703): running, waits, done or idle. It is computed
// each time from what eriphe-core knows of the node, never stored, and
// the graph, the page, node_find, node_read and the brain's context all
// read it from here:
//
//   - waits: a card linked to it waits for the user's answer (node_turn.go):
//     whatever asks the user's yes is a card, a task's held start and the
//     access a task waits on included, so the card is the one truth of what
//     waits; or it is the result of a finished work the user has not said
//     done yet: an accepted step no other accepted step comes after, or the
//     work's node itself when no step is such a result (node_steps.go). That
//     one stays computed, not a card: the user reads the result and says it
//     done in words, no button asks it;
//   - running: a task works in it (held for its start card, it does not
//     yet), or it is a step whose task runs;
//   - done: its file says done, or the steps' engine finished it (a step
//     accepted or dropped, a work done whose results wait in its steps);
//   - idle: anything else. A task that ended (stopped, failed, lost)
//     leaves its node idle, never done: only the user, or the brain on
//     their word, says a node is done.
//
// Waits comes first: what the user must answer is never hidden under a
// node at work. An archived node waits for no one.
//
// Whatever these come from tells the graph when it moves (nodeMoved): a
// card the moment its proposal moves, wherever it was answered
// (settleCards), never on a timer.

// A node's statuses.
const (
	nodeRunning = "running"
	nodeWaits   = "waits"
	nodeDone    = "done"
	nodeIdle    = "idle"
)

// statusFacts are what nodes' statuses are computed from, read in a
// constant number of queries, whatever the size of the graph.
type statusFacts struct {
	cards   map[string]bool     // nodes a waiting card is linked to
	tasks   map[string]bool     // nodes a live task works in, not held for its start
	steps   map[string]string   // step's node → its status
	results map[string]bool     // results of a finished work, waiting until the user says them done
	settled map[string]bool     // nodes the steps' engine finished
	homes   map[string][]string // node → the skills that maintain it (node_home.go)
}

// of is a node's status.
func (f statusFacts) of(sum nodestore.Summary) string {
	id, done := sum.ID, !sum.Done.IsZero()
	running := f.tasks[id] || f.steps[id] == stepRunning
	if !sum.Archived.IsZero() {
		switch {
		case running:
			return nodeRunning
		case done:
			return nodeDone
		}
		return nodeIdle
	}
	switch {
	case f.cards[id], !done && f.results[id]:
		return nodeWaits
	case running:
		return nodeRunning
	case done, f.settled[id]:
		return nodeDone
	}
	return nodeIdle
}

// statusFacts reads what the statuses of the graph's nodes come from;
// only one node's (and its work's, for a step) when only is given.
func (c *core) statusFacts(only string) statusFacts {
	f := statusFacts{cards: map[string]bool{}, tasks: map[string]bool{}, steps: map[string]string{},
		results: map[string]bool{}, settled: map[string]bool{}, homes: c.homeSkills()}
	db := c.store.db
	filter := func(q, col string) (string, []any) {
		if only == "" {
			return q, nil
		}
		return q + ` AND ` + col + ` = ?`, []any{only}
	}

	q, args := filter(`SELECT DISTINCT nc.node FROM node_cards nc JOIN convo_cards cc ON cc.ref = nc.ref WHERE cc.state = '`+cardPending+`'`, `nc.node`)
	if rows, err := db.Query(q, args...); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				f.cards[id] = true
			}
		}
		rows.Close()
	}

	q, args = filter(`SELECT DISTINCT tn.node FROM task_nodes tn JOIN tasks t ON t.id = tn.task
		WHERE t.state IN ('`+statePending+`', '`+stateRunning+`', '`+stateWaitingPermission+`') AND t.desired != '`+desiredHeld+`'`, `tn.node`)
	if rows, err := db.Query(q, args...); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				f.tasks[id] = true
			}
		}
		rows.Close()
	}

	// The steps, with their work's state: a step's status, and the results
	// of each work done. One node's: the steps of its work, or of the work
	// it is.
	q = `SELECT s.node, s.parent, s.status, s.after, COALESCE(w.state, '') FROM node_steps s LEFT JOIN node_works w ON w.parent = s.parent`
	args = nil
	if only != "" {
		q += ` WHERE s.parent = ? OR s.parent IN (SELECT parent FROM node_steps WHERE node = ?)`
		args = []any{only, only}
	}
	type stepRow struct{ node, status, after string }
	works := map[string][]stepRow{}
	finished := map[string]bool{}
	if rows, err := db.Query(q+` ORDER BY s.parent, s.n`, args...); err == nil {
		for rows.Next() {
			var s stepRow
			var parent, work string
			if rows.Scan(&s.node, &parent, &s.status, &s.after, &work) != nil {
				continue
			}
			f.steps[s.node] = s.status
			works[parent] = append(works[parent], s)
			finished[parent] = work == workDone
		}
		rows.Close()
	}
	for parent, steps := range works {
		after := map[string]bool{} // what an accepted step comes after: no result
		for _, s := range steps {
			if s.status == stepAccepted && s.after != "" {
				for _, a := range strings.Split(s.after, ",") {
					after[a] = true
				}
			}
		}
		results := 0
		for _, s := range steps {
			switch {
			case s.status == stepAccepted && finished[parent] && !after[s.node]:
				f.results[s.node] = true
				results++
			case s.status == stepAccepted, s.status == stepDropped:
				f.settled[s.node] = true
			}
		}
		switch {
		case !finished[parent]:
		case results == 0:
			f.results[parent] = true // nothing to read in its steps: the work itself
		default:
			f.settled[parent] = true
		}
	}
	return f
}

// nodeStatus is a node's status now.
func (c *core) nodeStatus(sum nodestore.Summary) string {
	return c.statusFacts(sum.ID).of(sum)
}

// statusNode is a node and its status now.
type statusNode struct {
	nodestore.Summary
	Status string
}

// withStatus gives each node its status, read at once.
func (c *core) withStatus(xs []nodestore.Summary) []statusNode {
	if len(xs) == 0 {
		return nil
	}
	f := c.statusFacts("")
	out := make([]statusNode, 0, len(xs))
	for _, x := range xs {
		out = append(out, statusNode{x, f.of(x)})
	}
	return out
}

// activeNodes are the nodes at work or waiting for the user, archived
// ones left out, the last changed first.
func (c *core) activeNodes(limit int) []statusNode {
	if c.nodes == nil {
		return nil
	}
	xs, err := c.nodes.Graph()
	if err != nil {
		log.Printf("nodes: %v", err)
		return nil
	}
	var out []statusNode
	for _, x := range c.withStatus(xs) {
		if x.Status == nodeRunning || x.Status == nodeWaits {
			out = append(out, x)
		}
	}
	slices.SortStableFunc(out, func(a, b statusNode) int {
		if d := b.Updated.Compare(a.Updated); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// nodeMoved tells the graph a node's status may have moved; ended: its
// run or its step stopped, and the line it said while it ran goes (a new
// run says its own).
func (c *core) nodeMoved(id string, ended bool) {
	if id == "" {
		return
	}
	if ended {
		c.forgetProgress(id)
	}
	c.graphSaid(graphEvent{Kind: graphEvNode, Node: id})
}

// taskNodeMoved follows a task's state: the cards that waited on a task
// are read again (an access card, a held start, gone with it), and its
// node's graph is told.
func (c *core) taskNodeMoved(task, state string) {
	c.settleCards(taskCardKinds...)
	if id := c.taskNode(task); id != "" && c.nodes != nil {
		c.nodeMoved(id, state != stateRunning && state != statePending)
	}
}

// taskCardKinds are the kinds of proposals a task's state decides: its
// held start and its access stop waiting with it, a run's calls with its
// run.
var taskCardKinds = []string{cardTaskStart, cardAccess, cardRunCalls, cardCalls}

// warmNodeCaches fills what the graph's rows read of each node and keeps
// in memory (its glance) for those not read yet, in a constant number of
// queries: the graph never reads its nodes one by one.
func (c *core) warmNodeCaches(xs []nodestore.Summary) {
	var glance []string
	for _, x := range xs {
		if _, ok := c.glances.Load(x.ID); !ok {
			glance = append(glance, x.ID)
		}
	}
	if len(glance) > 0 {
		kept := c.allGlances()
		for _, id := range glance {
			c.glances.LoadOrStore(id, kept[id])
		}
	}
}
