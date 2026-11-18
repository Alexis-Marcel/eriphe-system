package main

import (
	"encoding/json"
	"time"
)

// node_show (#632): the brain opens a node's page on the user's screen,
// beside what is open. Code keeps the request (kv node_show), so a page
// that connects later still gets it, and tells the pages that watch
// (graph_feed.go's show event): the graph workspace opens the page. It
// writes nothing in the node.

const nodeShowKey = "node_show"

// nodeShown is the last node shown: which, by whom, when; Seq grows with
// each request, so a page tells a new one from one it opened already.
type nodeShown struct {
	Node string    `json:"node"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Seq  int64     `json:"seq"`
}

// showNode asks the user's screen to open a node's page.
func (c *core) showNode(id, by string) {
	last, _ := c.lastShown()
	x := nodeShown{Node: id, By: by, At: time.Now(), Seq: last.Seq + 1}
	if raw, err := json.Marshal(x); err == nil {
		c.store.set(nodeShowKey, string(raw))
	}
	c.graphSaid(graphEvent{Kind: graphEvShow, Node: id})
}

// lastShown is the last request; false when there was none.
func (c *core) lastShown() (nodeShown, bool) {
	var x nodeShown
	raw := c.store.get(nodeShowKey)
	return x, raw != "" && json.Unmarshal([]byte(raw), &x) == nil
}
