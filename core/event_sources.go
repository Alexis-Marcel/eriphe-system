package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The event journal's sources (event_journal.go), besides those that call
// emit where they are (a task's or a session's end):
//
//   - an alerte rule's webhook: alert.received, or alert.resolved when the
//     body's top-level status says so (Grafana's, Alertmanager's). Key: the
//     rule's name. External.
//   - an outil rule's poll, on a confirmed transition: tool.alert,
//     tool.recovered or tool.changed. Key: the rule's name. External unless
//     the tool's server is marked trusted (tool_trust.go), as its wake is.
//   - the user's return after an absence of awayAfter or more:
//     user.returned, with how long they were away.
//   - Home Assistant's mapped entities: ha.changed when a signal's value
//     changes. Closed values: never external.
//   - any catalog server that offers the resource eriphe://events: the mail
//     server's mail.received. See serverEvents below.
//
// The alert and poll sources still live in their rules: they move out of
// them when the rules go (#278).

const (
	serverEventsURI  = "eriphe://events"
	eventsTickEvery  = time.Minute
	returnedMarkKey  = "events_returned_mark" // the last return emitted, unix
	eventsCursorKey  = "events_cursor:"       // + server: epoch, seq and time of its last event read
	eventsPurgeEvery = 6 * time.Hour
)

// runEvents is the journal's timer: the user's return, and the purge.
func (c *core) runEvents(ctx context.Context) {
	tick := time.NewTicker(eventsTickEvery)
	defer tick.Stop()
	var purged time.Time
	for {
		now := c.notifyTime()
		c.tickReturned(now)
		c.tickServerEvents(ctx)
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

// emitAlert writes an alerte rule's webhook call.
func (c *core) emitAlert(w *rule, body []byte) {
	kind := evAlertReceived
	if alertResolved(body) {
		kind = evAlertResolved
	}
	c.emitLogged(journalEvent{Kind: kind, Key: w.Name, Source: fmt.Sprintf("alert:%d", w.ID), External: true,
		Payload: map[string]any{"rule": w.Name, "detail": alertDetail(body)}})
}

// emitPoll writes an outil rule's confirmed transition.
func (c *core) emitPoll(w *rule, cause, excerpt string) {
	kind := map[string]string{causePoll: evToolAlert, causeRecovered: evToolRecovered, causeChanged: evToolChanged}[cause]
	if kind == "" {
		return
	}
	c.emitLogged(journalEvent{Kind: kind, Key: w.Name, Source: fmt.Sprintf("poll:%d", w.ID),
		External: !c.trustsServer(w.Spec.Server),
		Payload:  map[string]any{"rule": w.Name, "server": w.Spec.Server, "tool": w.Spec.Tool, "result": excerpt}})
}

// emitHAChanges writes ha.changed for each house signal whose value moved
// since the last publish. The first publish only records them.
func (c *core) emitHAChanges(sigs []ctxSignal) {
	now := map[string]string{}
	for _, s := range sigs {
		now[s.Kind] = s.Value
	}
	c.ha.mu.Lock()
	prev, first := c.ha.published, c.ha.published == nil
	c.ha.published = now
	c.ha.mu.Unlock()
	if first {
		return
	}
	for _, k := range sortedKeys(now) {
		if prev[k] != now[k] {
			c.emitLogged(journalEvent{Kind: evHAChanged, Key: k, Source: "ha",
				Payload: map[string]any{"signal": k, "value": now[k], "was": prev[k]}})
		}
	}
	for k, v := range prev {
		if _, ok := now[k]; !ok {
			c.emitLogged(journalEvent{Kind: evHAChanged, Key: k, Source: "ha",
				Payload: map[string]any{"signal": k, "value": "", "was": v}})
		}
	}
}

// Events from a catalog server. A server that has events offers the
// resource eriphe://events. Reading it returns JSON:
//
//	{"epoch": "…", "events": [{"seq": 12, "kind": "mail.received", "key": "…",
//	  "at": "2026-11-09T09:04:00Z", "payload": {…}}, …]}
//
// the server's last events, oldest first: seq grows within an epoch (a new
// one at each start). eriphe-core reads it when the server's session opens,
// then every minute: the servers run stateless behind their node, so they
// cannot push; a minute is soon enough for skills that batch their events.
// eriphe-core keeps, per server, the epoch, seq and time of the last event
// it took: within an epoch it takes the higher seqs, from a new epoch the
// events after that time. A server seen for the first time has its backlog
// skipped. A server's kinds are its own: never one of eriphe-core's
// families (reservedEventFamilies). What it says is external unless the
// server is marked trusted.

type serverEventsDoc struct {
	Epoch  string `json:"epoch"`
	Events []struct {
		Seq     int64          `json:"seq"`
		Kind    string         `json:"kind"`
		Key     string         `json:"key"`
		At      time.Time      `json:"at"`
		Payload map[string]any `json:"payload"`
	} `json:"events"`
}

type eventsCursor struct {
	Epoch string `json:"epoch"`
	Seq   int64  `json:"seq"`
	At    int64  `json:"at"`
}

// watchServerEvents notes whether a server offers events, once its
// session is open, and takes those it missed.
func (c *core) watchServerEvents(ctx context.Context, server string, sess *mcp.ClientSession) {
	found := false
	if r := sess.InitializeResult(); r != nil && r.Capabilities != nil && r.Capabilities.Resources != nil {
		for res, err := range sess.Resources(ctx, nil) {
			if err != nil {
				break
			}
			if res.URI == serverEventsURI {
				found = true
				break
			}
		}
	}
	c.events.cursorMu.Lock()
	if c.events.servers == nil {
		c.events.servers = map[string]bool{}
	}
	if found {
		c.events.servers[server] = true
	} else {
		delete(c.events.servers, server)
	}
	c.events.cursorMu.Unlock()
	if found {
		c.readServerEvents(ctx, server, sess)
	}
}

// tickServerEvents reads the events of every server that offers some.
func (c *core) tickServerEvents(ctx context.Context) {
	c.events.cursorMu.Lock()
	var names []string
	for name := range c.events.servers {
		names = append(names, name)
	}
	c.events.cursorMu.Unlock()
	for _, name := range names {
		u := c.pool.get(name)
		if u == nil {
			continue
		}
		u.mu.Lock()
		sess := u.session
		u.mu.Unlock()
		if sess == nil {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		c.readServerEvents(rctx, name, sess)
		cancel()
	}
}

// readServerEvents reads a server's events and emits the new ones.
func (c *core) readServerEvents(ctx context.Context, server string, sess *mcp.ClientSession) {
	res, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: serverEventsURI})
	if err != nil {
		log.Printf("server %s: events: %v", server, err)
		return
	}
	var doc serverEventsDoc
	for _, ct := range res.Contents {
		if ct.URI == serverEventsURI && ct.Text != "" {
			if err := json.Unmarshal([]byte(ct.Text), &doc); err != nil {
				log.Printf("server %s: events: %v", server, err)
				return
			}
		}
	}
	c.takeServerEvents(server, doc)
}

// takeServerEvents emits what is new in doc, from the server's cursor.
func (c *core) takeServerEvents(server string, doc serverEventsDoc) int {
	c.events.cursorMu.Lock() // one read per server at a time
	defer c.events.cursorMu.Unlock()
	var cur eventsCursor
	known := json.Unmarshal([]byte(c.store.get(eventsCursorKey+server)), &cur) == nil
	next := eventsCursor{Epoch: doc.Epoch, Seq: cur.Seq, At: cur.At}
	if doc.Epoch != cur.Epoch {
		next.Seq = 0
	}
	n := 0
	for _, e := range doc.Events {
		var fresh bool
		switch {
		case !known:
			fresh = false // first sight: the backlog is skipped
		case doc.Epoch == cur.Epoch:
			fresh = e.Seq > cur.Seq
		default:
			fresh = e.At.Unix() > cur.At
		}
		next.Seq, next.At = max(next.Seq, e.Seq), max(next.At, e.At.Unix())
		if !fresh {
			continue
		}
		if reservedEventKind(e.Kind) {
			log.Printf("server %s: events: kind %q is eriphe-core's: ignored", server, e.Kind)
			continue
		}
		if _, err := c.emit(journalEvent{Kind: e.Kind, Key: e.Key, Source: "server:" + server, At: e.At,
			Payload: e.Payload, External: !c.trustsServer(server)}); err != nil {
			log.Printf("server %s: events: %v", server, err)
			continue
		}
		n++
	}
	if next != cur || !known {
		raw, _ := json.Marshal(next)
		c.store.set(eventsCursorKey+server, string(raw))
	}
	return n
}

func reservedEventKind(kind string) bool {
	for _, f := range reservedEventFamilies {
		if strings.HasPrefix(kind, f) {
			return true
		}
	}
	return false
}
