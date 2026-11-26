package main

import (
	"log"
	"sync"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A page's lines that follow the event journal (nodefile/listen.go):
// [18 385 €](on:monitor.changed/pea) is rewritten by code at each event of
// that kind and key, its value and its date, without a model nor a task.
// The journal is agnostic: a monitor is one of its sources; what must be
// understood (a new mail) stays a skill's, woken by the same event.
//
//   - The store's index knows the lines that listen (node_listens); code
//     subscribes to the journal and writes the nodes that follow each
//     event (heard), one at a time, in the journal's order.
//   - A value makes no version (nodestore's values.go): the file is
//     written; the value enters the history with the node's next
//     version, or with one version a day, author readings, before the
//     night's backup (nodes.go).
//   - An event written by someone else marks the callout that holds the
//     line external: code's, an author cannot take it off.
//   - A node's glance follows its line (node_glance.go): each change of a
//     node's file reads it again.

// listenQueue is the events and the nodes changed, waiting for runNodes.
type listenQueue struct {
	events chan journalEvent // the journal's events, in order
	mu     sync.Mutex
	docs   map[string]bool // the nodes whose file changed since
	kick   chan struct{}
}

// listenQueued is how many events wait at most; past it they are dropped
// (logged): a line is rewritten at the next one.
const listenQueued = 512

// newListenQueue makes the queue, before the store is watched.
func newListenQueue() listenQueue {
	return listenQueue{events: make(chan journalEvent, listenQueued), docs: map[string]bool{}, kick: make(chan struct{}, 1)}
}

// queueHeard is the journal's subscriber: it returns at once.
func (c *core) queueHeard(e journalEvent) {
	select {
	case c.listen.events <- e:
	default:
		log.Printf("nodes: event %s %s dropped: %d wait already", e.Kind, e.Key, listenQueued)
	}
}

// docChanged queues a node whose file changed, for its glance. It never
// reads the graph: the store calls it with its lock held.
func (c *core) docChanged(id string) {
	q := &c.listen
	if q.kick == nil {
		return
	}
	q.mu.Lock()
	q.docs[id] = true
	q.mu.Unlock()
	select {
	case q.kick <- struct{}{}:
	default:
	}
}

// changedDocs takes the nodes queued by docChanged.
func (c *core) changedDocs() []string {
	q := &c.listen
	q.mu.Lock()
	defer q.mu.Unlock()
	out := sortedKeys(q.docs)
	clear(q.docs)
	return out
}

// heard writes the lines that follow an event, in every node not
// archived whose page has one; then their glance.
func (c *core) heard(e journalEvent) {
	if c.nodes == nil {
		return
	}
	ids, err := c.nodes.Listening(e.Kind, e.Key)
	if err != nil {
		log.Printf("nodes: lines that follow %s: %v", e.Kind, err)
		return
	}
	if len(ids) == 0 {
		return
	}
	ev := nodefile.Heard{Kind: e.Kind, Key: e.Key, Payload: e.Payload, External: e.External,
		Stamp: nodefile.ReadStamp(e.At.In(c.userZone()), c.userLang())}
	for _, id := range ids {
		if _, err := c.nodes.WriteValues(id, func(n *nodefile.Node) error {
			_, err := n.Hear(ev)
			return err
		}); err != nil {
			log.Printf("nodes: %s: lines that follow %s: %v", id, e.Kind, err)
		}
		c.noteGlance(id, e.At)
	}
}
