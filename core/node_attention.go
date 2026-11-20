package main

// A node that waits for the user because of what a source lists (#674's
// follow-up): a source marked attention (docs/node-format.md) makes its
// node wait while it lists items, as a card does, and rest once it lists
// none. Nothing here knows a source: « Outils à revoir » (tool_review.go)
// is one such node, any other block may be another. The answer is kept
// per node, known again at each read of its sources (readNodeOnce), and
// the graph is told when it turns.

// nodeAttention tells whether a source of node id marked attention lists
// items now, by the reads kept.
func (c *core) nodeAttention(id string) bool {
	if v, ok := c.attention.Load(id); ok {
		return v.(bool)
	}
	return c.noteAttention(id, c.nodeReads(id))
}

// noteAttention keeps what a node's reads say of its attention, tells the
// graph when it turned, and returns it.
func (c *core) noteAttention(id string, reads []nodeRead) bool {
	on := false
	for _, r := range reads {
		if r.Source.Attention && len(sourceItems(r.Name, r.Source, r.Value, 1)) > 0 {
			on = true
			break
		}
	}
	if was, ok := c.attention.Swap(id, on); ok && was.(bool) != on || !ok && on {
		c.graphSaid(graphEvent{Kind: graphEvNode, Node: id})
	}
	return on
}
