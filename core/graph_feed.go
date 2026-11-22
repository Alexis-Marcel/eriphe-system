package main

import (
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// What changes on the graph, for the workspaces that watch it (#633): the
// workspace's stream (web_sse.go) sends it as its "node", "doc",
// "progress", "source" and "show" events.
//
//   - node: a node's row changed (its title, state, parent, links,
//     version): the store tells (nodestore.Watch), and a card of its moved;
//   - doc: a new version of its file, or its trace grew;
//   - progress: a task said where its work is (task_nodes.go);
//   - source: a live block's source gave a new value (node_live.go);
//   - show: the brain asks the user's screen to open a node's page
//     (node_show.go).
//
// The feed keeps the last events only: a stream that fell behind starts
// again from the graph as it is.

const graphKept = 512

// Kinds of a graph event.
const (
	graphEvNode     = "node"
	graphEvDoc      = "doc"
	graphEvTrace    = "trace" // sent as a doc event, its trace grew
	graphEvProgress = "progress"
	graphEvSource   = "source"
	graphEvShow     = "show"
)

// graphEvent is one change of the graph.
type graphEvent struct {
	Seq   int64
	Kind  string
	Node  string
	Block string // source: the block and the source's name
	Name  string
	Text  string // progress: its line
	At    time.Time
}

// graphFeed is the graph's last changes and who waits for the next one.
type graphFeed struct {
	mu       sync.Mutex
	seq      int64
	events   []graphEvent
	changed  chan struct{}           // closed and replaced at each change
	progress map[string]progressLine // the last line of each node's work, while it runs
}

// progressLine is the last thing a node's task said of its work.
type progressLine struct {
	Text string
	At   time.Time
}

// useNodes is the graph eriphe-core works on, watched for the workspace.
func (c *core) useNodes(s *nodestore.Store) {
	c.nodes = s
	s.Watch(func(ch nodestore.Change) {
		switch ch.What {
		case nodestore.ChangedDoc:
			c.graphSaid(graphEvent{Kind: graphEvNode, Node: ch.ID})
			c.graphSaid(graphEvent{Kind: graphEvDoc, Node: ch.ID})
		case nodestore.ChangedTrace:
			c.graphSaid(graphEvent{Kind: graphEvTrace, Node: ch.ID})
		case nodestore.ChangedState:
			// Its work stopped or moved on: the line said while it ran
			// goes; a new run says its own.
			c.forgetProgress(ch.ID)
			c.graphSaid(graphEvent{Kind: graphEvNode, Node: ch.ID})
		default:
			c.graphSaid(graphEvent{Kind: graphEvNode, Node: ch.ID})
		}
	})
}

// graphSaid adds an event and wakes who waits. It never reads the graph:
// the store calls it with its lock held.
func (c *core) graphSaid(ev graphEvent) {
	f := &c.graph
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	ev.Seq = f.seq
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	switch ev.Kind {
	case graphEvProgress:
		if f.progress == nil {
			f.progress = map[string]progressLine{}
		}
		f.progress[ev.Node] = progressLine{Text: ev.Text, At: ev.At}
	}
	f.events = append(f.events, ev)
	if len(f.events) > graphKept {
		f.events = append([]graphEvent(nil), f.events[len(f.events)-graphKept:]...)
	}
	if f.changed != nil {
		close(f.changed)
	}
	f.changed = make(chan struct{})
}

// graphAfter are the events after seq, the last seq, whether some were
// dropped since (the caller starts again from the graph), and a channel
// closed at the next event.
func (c *core) graphAfter(seq int64) (evs []graphEvent, last int64, behind bool, next <-chan struct{}) {
	f := &c.graph
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changed == nil {
		f.changed = make(chan struct{})
	}
	if len(f.events) > 0 && f.events[0].Seq > seq+1 {
		behind = true
	}
	for _, ev := range f.events {
		if ev.Seq > seq {
			evs = append(evs, ev)
		}
	}
	return evs, f.seq, behind, f.changed
}

// graphHead is the last seq, and a channel closed at the next event.
func (c *core) graphHead() (int64, <-chan struct{}) {
	f := &c.graph
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changed == nil {
		f.changed = make(chan struct{})
	}
	return f.seq, f.changed
}

// progressOf is the last line of a node's work, if any.
func (c *core) progressOf(id string) (progressLine, bool) {
	f := &c.graph
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.progress[id]
	return p, ok
}

// forgetProgress drops a node's line of work: its state changed.
func (c *core) forgetProgress(id string) {
	f := &c.graph
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.progress, id)
}

// nodeProgress is a task's line of where its node's work is.
func (c *core) nodeProgress(id, text string) {
	c.graphSaid(graphEvent{Kind: graphEvProgress, Node: id, Text: text})
}
