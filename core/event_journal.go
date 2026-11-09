package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The event journal: one place where everything that happens is written,
// whatever its source, without being interpreted. Triggered skills (#275)
// subscribe to it; the brain reads it through since (kind event).
//
// A source writes an event with emit: its kind (mail.received,
// hook.received…), a key that groups the events of one thing (an address's
// source, a poll's name: a skill joins the events of an open key instead of
// starting again), its source, a short payload, and whether it is external:
// written by someone else (a mail, an alert's body, a tool not trusted).
// Whatever reads an external event's payload is tainted.
//
// Code produces events; it never decides what they mean. Nothing reads
// them yet but since: emitting one costs a row and the subscribers' calls.

const eventJournalSchema = `CREATE TABLE IF NOT EXISTS event_journal (
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, key TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL, at INTEGER NOT NULL, payload TEXT NOT NULL DEFAULT '{}',
  external INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS event_journal_at ON event_journal (at);
CREATE INDEX IF NOT EXISTS event_journal_kind ON event_journal (kind, at);`

func init() { extraSchema = append(extraSchema, eventJournalSchema) }

// The kinds eriphe-core itself emits. A server may emit others of its own
// (mail.received is the mail server's), never one of these families.
const (
	evHookReceived = "hook.received" // an incoming address was called (incoming_hooks.go)
	evTaskEnded    = "task.ended"
	evSessionEnded = "session.ended"
	evUserReturned = "user.returned"
)

const (
	eventKeep        = 30 * 24 * time.Hour
	eventPayloadMax  = 4096 // bytes of JSON
	eventStringMax   = 500  // runes of one string value
	eventKeyMax      = 100
	eventPerSourceHr = 600 // a source's events an hour, past which they are dropped
)

// eventKindRe: lowercase words joined by dots, two to four of them.
var eventKindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,3}$`)

// reservedEventFamilies are eriphe-core's: a server cannot emit them.
var reservedEventFamilies = []string{"hook.", "task.", "session.", "user.", "time.", "skill."}

// journalEvent is one event of the journal.
type journalEvent struct {
	ID       int64
	Kind     string
	Key      string
	Source   string // alert:<rule>, poll:<rule>, server:<name>, task, session, user, ha
	At       time.Time
	Payload  map[string]any
	External bool
}

// eventBus holds the journal's subscribers and its per-source counts.
type eventBus struct {
	cursorMu sync.Mutex      // the servers' cursors, and servers below (event_sources.go)
	servers  map[string]bool // the catalog servers that offer events

	mu   sync.Mutex
	subs []func(journalEvent)
	hour int64          // the hour the counts are for
	seen map[string]int // events of this hour, by source
}

// subscribe adds fn, called with each event once it is written. fn must
// return fast: it runs on the emitter's goroutine.
func (c *core) subscribeEvents(fn func(journalEvent)) {
	c.events.mu.Lock()
	c.events.subs = append(c.events.subs, fn)
	c.events.mu.Unlock()
}

// emit writes an event and hands it to the subscribers. Its payload is cut
// to size; a source past its hourly budget has its events dropped (logged).
func (c *core) emit(e journalEvent) (journalEvent, error) {
	if !eventKindRe.MatchString(e.Kind) {
		return e, fmt.Errorf("event kind %q: lowercase words joined by dots", e.Kind)
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.Key = truncate(strings.TrimSpace(e.Key), eventKeyMax)
	e.Payload = capPayload(e.Payload)
	if !c.events.allow(e.Source, e.At) {
		return e, fmt.Errorf("source %s sent more than %d events this hour: dropped", e.Source, eventPerSourceHr)
	}
	raw, _ := json.Marshal(e.Payload)
	res, err := c.store.db.Exec(`INSERT INTO event_journal (kind, key, source, at, payload, external) VALUES (?, ?, ?, ?, ?, ?)`,
		e.Kind, e.Key, e.Source, e.At.Unix(), string(raw), boolInt(e.External))
	if err != nil {
		return e, err
	}
	e.ID, _ = res.LastInsertId()
	c.events.mu.Lock()
	subs := append([]func(journalEvent){}, c.events.subs...)
	c.events.mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
	return e, nil
}

// emitLogged is emit for the sources that have no one to tell of an error.
func (c *core) emitLogged(e journalEvent) {
	if _, err := c.emit(e); err != nil {
		log.Printf("events: %s: %v", e.Kind, err)
	}
}

func (b *eventBus) allow(source string, at time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if h := at.Unix() / 3600; h != b.hour || b.seen == nil {
		b.hour, b.seen = h, map[string]int{}
	}
	b.seen[source]++
	return b.seen[source] <= eventPerSourceHr
}

// capPayload keeps a payload small: each string cut, nested values kept
// only while the whole stays under eventPayloadMax.
func capPayload(p map[string]any) map[string]any {
	out := map[string]any{}
	if len(p) == 0 {
		return out
	}
	size := 2
	for _, k := range sortedKeys(p) {
		v := p[k]
		if s, ok := v.(string); ok && utf8.RuneCountInString(s) > eventStringMax {
			v = string([]rune(s)[:eventStringMax]) + "…"
		}
		raw, err := json.Marshal(v)
		if err != nil {
			continue
		}
		if size+len(k)+len(raw)+4 > eventPayloadMax {
			out["truncated"] = true
			break
		}
		size += len(k) + len(raw) + 4
		out[k] = v
	}
	return out
}

// purgeEvents drops the events older than eventKeep.
func (c *core) purgeEvents(now time.Time) {
	if _, err := c.store.db.Exec(`DELETE FROM event_journal WHERE at < ?`, now.Add(-eventKeep).Unix()); err != nil {
		log.Printf("events: purge: %v", err)
	}
}

// eventsSince reads the journal from a time, oldest first.
func (c *core) eventsSince(from time.Time, limit int) ([]journalEvent, error) {
	rows, err := c.store.db.Query(`SELECT id, kind, key, source, at, payload, external FROM event_journal
		WHERE at >= ? ORDER BY at, id LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []journalEvent
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEvent(rows *sql.Rows) (journalEvent, error) {
	var e journalEvent
	var at int64
	var raw string
	var ext int
	if err := rows.Scan(&e.ID, &e.Kind, &e.Key, &e.Source, &at, &raw, &ext); err != nil {
		return e, err
	}
	e.At, e.External = time.Unix(at, 0), ext == 1
	json.Unmarshal([]byte(raw), &e.Payload)
	return e, nil
}

// eventHappenings are the journal's events for since: named by their kind,
// key and source; the payload quoted (external ones only on request).
func eventHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	evs, err := c.eventsSince(from, limit)
	if err != nil {
		return nil, err
	}
	var out []happening
	for _, e := range evs {
		h := happening{At: e.At, Kind: feedEvent, Event: "happened", Ref: fmt.Sprintf("event:%d", e.ID),
			Args: []any{e.Kind, cmpOr(e.Key, "-"), e.Source}, Trust: feedOwn}
		if len(e.Payload) > 0 {
			raw, _ := json.Marshal(e.Payload)
			h.Text = feedText(string(raw))
		}
		if e.External {
			h.Trust = feedExternal
		}
		out = append(out, h)
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sortedKeys are a map's keys, sorted.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
