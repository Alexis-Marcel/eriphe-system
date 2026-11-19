package main

import (
	"encoding/json"
	"time"
)

// node_show (#632): the brain opens a node's page on the user's screen,
// beside what is open. Code keeps the request (kv node_show), so a page
// that connects later still gets it, and tells the pages that watch
// (graph_feed.go's show event): the graph workspace opens the page. It
// writes nothing in the node. A show opens the page beside what is open,
// the console where it was; one that takes the console (talk: a subject
// opened from the home, node_turn.go's moveHomeTurn) moves the console
// into it too, when it talked to the home.

const nodeShowKey = "node_show"

// nodeShown is the last node shown: which, by whom, when; Seq grows with
// each request, so a page tells a new one from one it opened already.
type nodeShown struct {
	Node string    `json:"node"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Seq  int64     `json:"seq"`
	Talk bool      `json:"talk,omitempty"` // the console, at the home, moves into it
}

// showNode asks the user's screen to open a node's page; talk: and to
// talk to it, from the home.
func (c *core) showNode(id, by string, talk bool) {
	last, _ := c.lastShown()
	x := nodeShown{Node: id, By: by, At: time.Now(), Seq: last.Seq + 1, Talk: talk}
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
