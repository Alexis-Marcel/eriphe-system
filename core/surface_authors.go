package main

// Who keeps the workspace's surfaces (#423, #574): code only.
//
//   - What waits for the user is the attention queue's
//     (workspace_queue.go), code's.
//   - Work and views are nodes (node_live.go, task_nodes.go): a skill's
//     run writes in its node, a block's sources are read by code. No model
//     judges a surface stale or the workspace short of one.

// dropFreeSurfaces removes, at start, every surface code's queue did not
// write: what nothing would keep or let the user take away.
func (c *core) dropFreeSurfaces() {
	all, _ := c.surfaces()
	for _, s := range all {
		if s.Author != surfaceAuthorCore {
			c.surfaceRemove(s.ID, surfaceByLifetime)
		}
	}
}

// A proposal waiting without a card in front of the user (the memory's
// extraction, a turn that said nothing of it) enters the attention queue
// at its card's weight (cardWeight), unless it is there already: the
// queue delivers it like anything else (attention.go).

const cardsAttendedEachMax = 50

// attendQueuedCards puts the queued cards in the attention queue, an item
// each, delivered together.
func (c *core) attendQueuedCards() {
	c.syncCards()
	queued, err := c.cardsWhere(`seq = 0 AND turn = 0 AND state = ? AND kind != ? ORDER BY id LIMIT ?`, cardPending, cardDigest, cardsAttendedEachMax)
	if err != nil {
		return
	}
	n := 0
	for _, x := range queued {
		var in int
		if c.store.db.QueryRow(`SELECT COUNT(*) FROM attention WHERE ref = ?`, cardItemRef(x.ID)).Scan(&in); in > 0 {
			continue // there already
		}
		x, p := c.liveCard(x)
		if x.State != cardPending {
			continue
		}
		c.attendCardHeld(x, p, "", "")
		n++
	}
	if n > 0 {
		c.attentionNow()
	}
}
