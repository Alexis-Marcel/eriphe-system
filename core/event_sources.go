package main

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The event journal's sources (event_journal.go), besides those that call
// emit where they are (a task's or a session's end):
//
//   - a monitor (monitors.go): it polls a tool, or listens on an incoming
//     address (incoming_hooks.go): monitor.*, keyed by its name.
//   - the user's return after an absence of awayAfter or more:
//     user.returned, with how long they were away.
//   - a catalog server that offers the resource eriphe://events (the mail
//     server's mail.received, or another server's own kinds), once a
//     monitor follows it (way follow). See serverEvents below.
//
// eriphe-core knows no tool by its name: what happens outside reaches the
// journal only through a monitor the user approved (monitors.go).

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
// Only a server a monitor follows is read, and its events are the
// monitor's: keyed by its name (the server's own key stays in the payload,
// as key, unless the payload has one), its source monitor:<name>; three
// failed reads in a row write monitor.failed, as a poll's looks.
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
	if found && c.followerOf(server, true) != nil {
		c.readServerEvents(ctx, server, sess)
	}
}

// offersEvents tells whether a server offers eriphe://events.
func (c *core) offersEvents(server string) bool {
	c.events.cursorMu.Lock()
	defer c.events.cursorMu.Unlock()
	return c.events.servers[server]
}

// tickServerEvents reads the events of every server a monitor follows.
func (c *core) tickServerEvents(ctx context.Context) {
	ms, _ := c.monitors()
	var names []string
	for _, m := range ms {
		if m.Spec.Way == wayFollow && m.State == monitorOn && c.offersEvents(m.Spec.Server) {
			names = append(names, m.Spec.Server)
		}
	}
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

// readServerEvents reads the events of a server its monitor follows, and
// emits the new ones; the monitor keeps whether the read worked.
func (c *core) readServerEvents(ctx context.Context, server string, sess *mcp.ClientSession) {
	m := c.followerOf(server, true)
	if m == nil {
		return
	}
	doc, err := serverEventsOf(ctx, sess)
	st := m.Look
	if c.noteLook(m, &st, err) {
		st.Known = true
		c.takeServerEvents(m, doc)
	} else {
		log.Printf("server %s: events: %v", server, err)
	}
	st.Checked = time.Now().Unix()
	raw, _ := json.Marshal(st)
	c.store.db.Exec(`UPDATE monitors SET look = ? WHERE id = ?`, string(raw), m.ID)
}

func serverEventsOf(ctx context.Context, sess *mcp.ClientSession) (serverEventsDoc, error) {
	var doc serverEventsDoc
	res, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: serverEventsURI})
	if err != nil {
		return doc, err
	}
	for _, ct := range res.Contents {
		if ct.URI == serverEventsURI && ct.Text != "" {
			if err := json.Unmarshal([]byte(ct.Text), &doc); err != nil {
				return doc, err
			}
		}
	}
	return doc, nil
}

// takeServerEvents emits what is new in doc, from the server's cursor, as
// its monitor's events.
func (c *core) takeServerEvents(m *monitor, doc serverEventsDoc) int {
	server := m.Spec.Server
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
		p := e.Payload
		if p == nil {
			p = map[string]any{}
		}
		if _, ok := p["key"]; !ok && e.Key != "" {
			p["key"] = e.Key
		}
		p["monitor"] = m.Name
		if _, err := c.emit(journalEvent{Kind: e.Kind, Key: m.Name, Source: "monitor:" + m.Name, At: e.At,
			Payload: p, External: !c.trustsServer(server)}); err != nil {
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

// taskEnded is the lifecycle's hook: a worker task finished.
func (c *core) taskEnded(id, state string) {
	t, err := c.store.task(id)
	if err != nil || t.Kind != "worker" {
		return
	}
	c.emitLogged(journalEvent{Kind: evTaskEnded, Key: id, Source: "task", Payload: map[string]any{"task": id, "state": state}})
}
