package main

import (
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// Proposals in the blocks they concern (#428).
//
//   - Embedded: an author places a Proposal component whose proposalId is
//     a pending proposal's own ref (kind:id), written or bound to the
//     block's data. Its Oui and Non go to that proposal's card
//     (POST /cards/{id}/yes|no), as everywhere.
//   - Dated: a pending proposal that has a time shows as a ghost in the
//     Agendas that draw its day, with what it overlaps (GET
//     /api/proposals?day=), its Oui on it; an Agenda of its own page is
//     its place: not at the page's foot then (datedOn). Its time comes from
//     what the proposal is, never from a tool's name: an intention's due
//     date, or the arguments of a call it keeps that are a date or a
//     date-time (callSpan). A run's card keeps several calls: each dated
//     one is a ghost of its own, all answering the one card.
//   - Moved: when a card is made, decided (anywhere: its buttons, a
//     channel, a plain "oui", a page) or found decided, its ref
//     is noted here; the workspace's event stream sends it ("proposals"),
//     and the page reads again what shows it.

// proposalSpan is when a dated proposal takes place: a start, an end when
// it has one, the whole day when its time is a date.
type proposalSpan struct {
	Start  time.Time
	End    time.Time
	AllDay bool
}

// proposalSpans are the times of a proposal, in loc; none when it has
// none.
func (c *core) proposalSpans(kind string, id int64, loc *time.Location) []proposalSpan {
	switch kind {
	case cardIntention:
		if c.mem == nil {
			return nil
		}
		x, err := c.mem.intention(id)
		if err != nil || x.DueAt.IsZero() {
			return nil
		}
		return []proposalSpan{{Start: x.DueAt.In(loc)}}
	case cardAccess:
		a, err := c.accessAskOf(id)
		if err != nil {
			return nil
		}
		if s, ok := c.callSpan(a, loc); ok {
			return []proposalSpan{s}
		}
	case cardCalls:
		asks := c.batchAsks(id)
		var out []proposalSpan
		for _, a := range asks {
			if a.State != startProposed {
				continue
			}
			if s, ok := c.callSpan(a, loc); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// callSpan is the time of a call a card keeps: from the earliest of its
// arguments that are a date or a date-time to the latest. An argument
// counts when its tool's input schema says so (format date-time or date),
// or, the schema saying no format, when its whole value is one. No name
// of a tool or of an argument is read.
func (c *core) callSpan(a accessQuestion, loc *time.Location) (proposalSpan, bool) {
	name, _, _ := strings.Cut(a.Call, " ")
	var input any
	if name == "" || json.Unmarshal([]byte(a.Input), &input) != nil {
		return proposalSpan{}, false
	}
	return spanOf(datedValues(c.inputSchema(name), input, loc))
}

// inputSchema is a tool's input schema, by the name a card keeps it under;
// nil when the tool is not there.
func (c *core) inputSchema(name string) map[string]any {
	server, tool, ok := splitToolName(name)
	if !ok || c.pool == nil {
		return nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil
	}
	t := u.toolByClaudeName(tool)
	if t == nil || t.InputSchema == nil {
		return nil
	}
	raw, _ := json.Marshal(t.InputSchema)
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// datedValue is one date or date-time found in a call's arguments.
type datedValue struct {
	At   time.Time
	Date bool // a date, no time
}

// datedValues finds the dates and date-times in a value, walking its
// schema beside it.
func datedValues(schema map[string]any, v any, loc *time.Location) []datedValue {
	switch x := v.(type) {
	case string:
		format, _ := schema["format"].(string)
		switch format {
		case "date-time", "date":
		case "":
			if schemaFormats(schema) {
				return nil // the schema says what it is: not a time
			}
		default:
			return nil
		}
		if d, ok := parseDated(x, loc); ok && (format == "" || (format == "date") == d.Date) {
			return []datedValue{d}
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		var out []datedValue
		for _, k := range slices.Sorted(maps.Keys(x)) {
			sub, _ := props[k].(map[string]any)
			out = append(out, datedValues(sub, x[k], loc)...)
		}
		return out
	case []any:
		items, _ := schema["items"].(map[string]any)
		var out []datedValue
		for _, it := range x {
			out = append(out, datedValues(items, it, loc)...)
		}
		return out
	}
	return nil
}

// schemaFormats tells whether a string's schema says what it is: a format,
// an enum or a pattern.
func schemaFormats(schema map[string]any) bool {
	for _, k := range []string{"enum", "const", "pattern"} {
		if _, ok := schema[k]; ok {
			return true
		}
	}
	return false
}

// datedLayouts are the date-times a value may be, whole: with an offset,
// or in the user's zone.
var datedLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"}

// parseDated reads a value that is, whole, a date-time or a date.
func parseDated(s string, loc *time.Location) (datedValue, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 10 || len(s) > 40 {
		return datedValue{}, false
	}
	for i, layout := range datedLayouts {
		var t time.Time
		var err error
		if i == 0 {
			t, err = time.Parse(layout, s)
		} else {
			t, err = time.ParseInLocation(layout, s, loc)
		}
		if err == nil {
			return datedValue{At: t.In(loc)}, true
		}
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, loc); err == nil {
		return datedValue{At: t, Date: true}, true
	}
	return datedValue{}, false
}

// spanOf is the span of the times found: the earliest to the latest. A
// span of dates is all day, its last day included.
func spanOf(ds []datedValue) (proposalSpan, bool) {
	if len(ds) == 0 {
		return proposalSpan{}, false
	}
	slices.SortStableFunc(ds, func(a, b datedValue) int { return a.At.Compare(b.At) })
	first, last := ds[0], ds[len(ds)-1]
	s := proposalSpan{Start: first.At, AllDay: first.Date}
	if last.At.After(first.At) {
		s.End = last.At
	}
	return s, true
}

// shownDays are the days a block's components draw the dated proposals
// of (a2ui.DaysShown: an Agenda), each the start of its day in now's zone.
func shownDays(b *nodefile.Block, now time.Time) []time.Time {
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	var out []time.Time
	for _, comp := range comps {
		from, n, ok := a2ui.DaysShown(comp, now)
		for i := 0; ok && i < n; i++ {
			out = append(out, from.AddDate(0, 0, i))
		}
	}
	return out
}

// datedOn tells whether the proposal a card carries takes place on one of
// days (its spans in loc, as GET /api/proposals?day= finds them): an
// Agenda of the page draws it there, as a ghost with its Oui.
func (c *core) datedOn(ref string, days []time.Time, loc *time.Location) bool {
	kind, id, ok := parseProposalRef(ref)
	if !ok || len(days) == 0 {
		return false
	}
	for _, s := range c.proposalSpans(kind, id, loc) {
		if slices.ContainsFunc(days, s.on) {
			return true
		}
	}
	return false
}

// on tells whether the span touches the day that starts at from.
func (s proposalSpan) on(from time.Time) bool {
	to := from.AddDate(0, 0, 1)
	end := s.End
	switch {
	case s.AllDay && !end.IsZero():
		end = end.AddDate(0, 0, 1) // its last day included
	case s.AllDay:
		end = s.Start.AddDate(0, 0, 1)
	case end.IsZero():
		return !s.Start.Before(from) && s.Start.Before(to)
	}
	return s.Start.Before(to) && end.After(from)
}

// spanView is a span as the page reads it: times with the user's offset,
// a date for a day.
type spanView struct {
	Start  string `json:"start"`
	End    string `json:"end,omitempty"`
	AllDay bool   `json:"allDay,omitempty"`
}

func (s proposalSpan) view() spanView {
	layout := time.RFC3339
	if s.AllDay {
		layout = time.DateOnly
	}
	v := spanView{Start: s.Start.Format(layout), AllDay: s.AllDay}
	if !s.End.IsZero() {
		v.End = s.End.Format(layout)
	}
	return v
}

// waitingCards are the cards that wait, with their proposals, newest
// first: the one an author just caused is not cut off by a backlog.
func (c *core) waitingCards() []pendingCard {
	xs, err := c.cardsWhere(`state = ? ORDER BY id DESC`, cardPending)
	if err != nil {
		return nil
	}
	var out []pendingCard
	for _, x := range xs {
		if x, p := c.liveCard(x); x.State == cardPending && p.Kind != "" {
			out = append(out, pendingCard{x, p})
		}
	}
	return out
}

type pendingCard struct {
	card convoCard
	p    proposal
}

// userZone is the user's time zone.
func (c *core) userZone() *time.Location {
	if loc := c.conf().location; loc != nil {
		return loc
	}
	return time.Local
}

// The proposals that moved, for the event stream.

// proposalFeedMax is as many moves as the feed keeps: a stream further
// behind reads everything again.
const proposalFeedMax = 64

type proposalMove struct {
	seq   int64
	ref   string
	nodes []string // its card's nodes
}

// proposalFeed keeps the refs of the proposals that moved lately, and
// wakes who waits on them.
type proposalFeed struct {
	mu      sync.Mutex
	seq     int64
	moves   []proposalMove
	changed chan struct{}
}

// proposalMoved notes that a proposal's card was made or changed state,
// and tells the graph of the nodes it is linked to (node_status.go).
func (c *core) proposalMoved(ref string) {
	nodes := c.cardNodes(ref)
	for _, id := range nodes {
		c.nodeMoved(id, false)
	}
	f := &c.props
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.moves = append(f.moves, proposalMove{f.seq, ref, nodes})
	if len(f.moves) > proposalFeedMax {
		f.moves = f.moves[len(f.moves)-proposalFeedMax:]
	}
	if f.changed != nil {
		close(f.changed)
		f.changed = nil
	}
}

// proposalsEvent is the data of the stream's "proposals" event: the refs
// of the proposals that moved, or all when the stream fell behind; nodes,
// the nodes their cards are linked to, whose pages read them again (a new
// card is in no page yet).
type proposalsEvent struct {
	Refs  []string `json:"refs"`
	Nodes []string `json:"nodes,omitempty"`
	All   bool     `json:"all,omitempty"`
}

// watchProposals is what moved after seq, the last seq, and a channel
// closed at the next move.
func (c *core) watchProposals(after int64) (proposalsEvent, int64, <-chan struct{}) {
	f := &c.props
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changed == nil {
		f.changed = make(chan struct{})
	}
	ev := proposalsEvent{Refs: []string{}}
	if len(f.moves) > 0 && f.moves[0].seq > after+1 {
		ev.All = true
	}
	for _, m := range f.moves {
		if m.seq <= after {
			continue
		}
		if !slices.Contains(ev.Refs, m.ref) {
			ev.Refs = append(ev.Refs, m.ref)
		}
		for _, id := range m.nodes {
			if !slices.Contains(ev.Nodes, id) {
				ev.Nodes = append(ev.Nodes, id)
			}
		}
	}
	return ev, f.seq, f.changed
}

// rowsOf is how many rows a statement changed; 0 when it failed.
func rowsOf(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
