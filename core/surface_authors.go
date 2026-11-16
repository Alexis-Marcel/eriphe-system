package main

import (
	"strconv"
	"time"
)

// Who keeps the workspace's surfaces (#423). Nothing is recurring by
// default: an author writes what it has the context for.
//
//   - A thread's surface is code's (thread_surface.go): written from the
//     thread's data at each of its changes. Who holds the thread (the
//     brain, or its lead) adds its part to it. What else a lead writes
//     lives while its thread does (while: thread:<id>, its default
//     lifetime); code removes it when the thread ends (dropThreadSurfaces).
//   - A skill writes no surface: it acts on its thread (task_threads.go),
//     whose surface is code's.
//   - The brain writes what the user asks; a recurring request is a skill.
//     No model judges a surface stale or the workspace short of one: live
//     data is read again by code, and code orders the workspace
//     (workspace_layout.go).

// dropThreadSurfaces removes at once the surfaces whose lifetime ended
// with a thread: its lead's, and those kept while it ran. Its own surface
// stays a while (thread_surface.go).
func (c *core) dropThreadSurfaces(thread int64) {
	id := strconv.FormatInt(thread, 10)
	all, _ := c.surfaces()
	now := time.Now()
	for _, s := range all {
		if s.ID == threadSurfaceID(thread) {
			continue
		}
		if (s.Author == "lead:"+id || s.Lifetime.While == "thread:"+id) && c.surfaceEnded(s, now) {
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
