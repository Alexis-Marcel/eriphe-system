package main

import (
	"context"
	"strconv"
	"time"
)

// The event journal's sources (event_journal.go), besides those that call
// emit where they are (a task's or a session's end):
//
//   - a monitor (monitors.go): it polls a tool or reads a resource of a
//     catalog server, or listens on an incoming address
//     (incoming_hooks.go): monitor.*, keyed by its name.
//   - the user's return after an absence of awayAfter or more:
//     user.returned, with how long they were away.
//
// eriphe-core knows no tool by its name: what happens outside reaches the
// journal only through a monitor the user approved (monitors.go).

const (
	eventsTickEvery  = time.Minute
	returnedMarkKey  = "events_returned_mark" // the last return emitted, unix
	eventsPurgeEvery = 6 * time.Hour
)

// runEvents is the journal's timer: the user's return, the attention
// queue's breakpoints and caps (attention.go), and the purge.
func (c *core) runEvents(ctx context.Context) {
	c.subscribeEvents(c.attentionOnEvent)
	tick := time.NewTicker(eventsTickEvery)
	defer tick.Stop()
	var purged time.Time
	for {
		now := c.notifyTime()
		c.tickReturned(now)
		c.attentionBreak(breakCap, now)
		if now.Sub(purged) >= eventsPurgeEvery {
			c.purgeEvents(now)
			purged = now
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// tickReturned emits user.returned once per return, when the user's first
// activity after an absence is recent.
func (c *core) tickReturned(now time.Time) {
	a, ok := c.lastAbsence(now)
	if !ok || a.Back.IsZero() || now.Sub(a.Back) >= awayAfter {
		return
	}
	mark, _ := strconv.ParseInt(c.store.get(returnedMarkKey), 10, 64)
	if a.Back.Unix() <= mark {
		return
	}
	c.store.set(returnedMarkKey, strconv.FormatInt(a.Back.Unix(), 10))
	loc := now.Location()
	c.emitLogged(journalEvent{Kind: evUserReturned, Source: "user", At: a.Back, Payload: map[string]any{
		"left": a.Left.In(loc).Format(time.RFC3339), "back": a.Back.In(loc).Format(time.RFC3339),
		"away_minutes": int(a.Back.Sub(a.Left) / time.Minute)}})
}

// taskEnded is the lifecycle's hook: a worker task finished.
func (c *core) taskEnded(id, state string) {
	t, err := c.store.task(id)
	if err != nil || t.Kind != "worker" {
		return
	}
	c.emitLogged(journalEvent{Kind: evTaskEnded, Key: id, Source: "task", Payload: map[string]any{"task": id, "state": state}})
}
