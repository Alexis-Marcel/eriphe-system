package main

import (
	"encoding/json"
	"sync"
	"time"
)

// node_show (#632): the brain opens a node's page on the user's screen,
// beside what is open. Code keeps the request (kv node_show), so a page
// that connects later still gets it, and tells the pages that watch
// (watchShown): the graph workspace (#633) opens the page. It writes
// nothing in the node.

const nodeShowKey = "node_show"

// nodeShown is the last node shown: which, by whom, when; Seq grows with
// each request, so a page tells a new one from one it opened already.
type nodeShown struct {
	Node string    `json:"node"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Seq  int64     `json:"seq"`
}

// showFeed is the requests' subscribers.
type showFeed struct {
	mu      sync.Mutex
	changed chan struct{} // closed and replaced at each request
}

// showNode asks the user's screen to open a node's page.
func (c *core) showNode(id, by string) {
	last, _ := c.lastShown()
	x := nodeShown{Node: id, By: by, At: time.Now(), Seq: last.Seq + 1}
	if raw, err := json.Marshal(x); err == nil {
		c.store.set(nodeShowKey, string(raw))
	}
	f := &c.shows
	f.mu.Lock()
	if f.changed != nil {
		close(f.changed)
	}
	f.changed = make(chan struct{})
	f.mu.Unlock()
}

// lastShown is the last request; false when there was none.
func (c *core) lastShown() (nodeShown, bool) {
	var x nodeShown
	raw := c.store.get(nodeShowKey)
	return x, raw != "" && json.Unmarshal([]byte(raw), &x) == nil
}

// watchShown is the last request and a channel closed at the next one.
func (c *core) watchShown() (nodeShown, bool, <-chan struct{}) {
	f := &c.shows
	f.mu.Lock()
	if f.changed == nil {
		f.changed = make(chan struct{})
	}
	ch := f.changed
	f.mu.Unlock()
	x, ok := c.lastShown()
	return x, ok, ch
}
