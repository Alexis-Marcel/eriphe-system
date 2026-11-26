package main

import (
	"database/sql"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

// Proposals in the pages they concern (#428).
//
//   - Moved: when a card is made, decided (anywhere: its buttons, a
//     channel, a plain "oui", a page) or found decided, its ref
//     is noted here; the workspace's event stream sends it ("proposals"),
//     and the page reads again what shows it.

// inputSchema is a tool's input schema, by the name a card keeps it under;
// nil when the tool is not there.
func (c *core) inputSchema(name string) map[string]any {
	server, tool, ok := splitToolName(name)
	if !ok || c.pool == nil {
		return nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil
	}
	t := u.toolByClaudeName(tool)
	if t == nil || t.InputSchema == nil {
		return nil
	}
	raw, _ := json.Marshal(t.InputSchema)
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// userZone is the user's time zone.
func (c *core) userZone() *time.Location {
	if loc := c.conf().location; loc != nil {
		return loc
	}
	return time.Local
}

// The proposals that moved, for the event stream.

// proposalFeedMax is as many moves as the feed keeps: a stream further
// behind reads everything again.
const proposalFeedMax = 64

type proposalMove struct {
	seq   int64
	ref   string
	nodes []string // its card's nodes
}

// proposalFeed keeps the refs of the proposals that moved lately, and
// wakes who waits on them.
type proposalFeed struct {
	mu      sync.Mutex
	seq     int64
	moves   []proposalMove
	changed chan struct{}
}

// proposalMoved notes that a proposal's card was made or changed state,
// and tells the graph of the nodes it is linked to (node_status.go).
func (c *core) proposalMoved(ref string) {
	nodes := c.cardNodes(ref)
	for _, id := range nodes {
		c.nodeMoved(id, false)
	}
	f := &c.props
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.moves = append(f.moves, proposalMove{f.seq, ref, nodes})
	if len(f.moves) > proposalFeedMax {
		f.moves = f.moves[len(f.moves)-proposalFeedMax:]
	}
	if f.changed != nil {
		close(f.changed)
		f.changed = nil
	}
}

// proposalsEvent is the data of the stream's "proposals" event: the refs
// of the proposals that moved, or all when the stream fell behind; nodes,
// the nodes their cards are linked to, whose pages read them again (a new
// card is in no page yet).
type proposalsEvent struct {
	Refs  []string `json:"refs"`
	Nodes []string `json:"nodes,omitempty"`
	All   bool     `json:"all,omitempty"`
}

// watchProposals is what moved after seq, the last seq, and a channel
// closed at the next move.
func (c *core) watchProposals(after int64) (proposalsEvent, int64, <-chan struct{}) {
	f := &c.props
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changed == nil {
		f.changed = make(chan struct{})
	}
	ev := proposalsEvent{Refs: []string{}}
	if len(f.moves) > 0 && f.moves[0].seq > after+1 {
		ev.All = true
	}
	for _, m := range f.moves {
		if m.seq <= after {
			continue
		}
		if !slices.Contains(ev.Refs, m.ref) {
			ev.Refs = append(ev.Refs, m.ref)
		}
		for _, id := range m.nodes {
			if !slices.Contains(ev.Nodes, id) {
				ev.Nodes = append(ev.Nodes, id)
			}
		}
	}
	return ev, f.seq, f.changed
}

// rowsOf is how many rows a statement changed; 0 when it failed.
func rowsOf(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
