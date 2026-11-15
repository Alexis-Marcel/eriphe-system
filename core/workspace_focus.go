package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// Focus (#429): one workspace, and opening a surface does what a separate
// dashboard would. The surface the user opens (the open action,
// surface_actions.go) is placed first by code at once (workspace_layout.go);
// once the focus settles (focusSettle, so a quick run of clicks recomposes
// once), the composer recomposes around it: it large, what relates to it
// around, the rest folded. Nothing is sent to the brain (#550): opening a
// surface, or going back to one, is not a turn; what it shows says where
// things stand.
//
//   - Relations are found by code, from what every surface declares, never
//     from a tool's name (surfaceRelations): the same source, the same
//     server, the same ref it lives while or its author stands for (a
//     thread, its lead and its steps' tasks, a task, a skill), the same
//     proposal shown in a Proposal (the queue's tile of it), a day: a Day
//     component on a date the focused surface carries, or the reverse.
//   - Two choices are kept: the base one, composed with nothing open
//     (workspaceChoice, kept in the store), and the one around the focus, in
//     memory, for that focus only. Back (or the focus closed, removed or
//     past focusFor) lays the base out again at once, with no model; it is
//     composed again only when the pool or the queue changed meanwhile.

const (
	momentFocus  = "focus" // the user opened a surface: the workspace is composed around it
	relationsMax = 6
	datesMax     = 64

	relSource  = "source"  // reads the same thing
	relServer  = "server"  // reads from the same server
	relRef     = "ref"     // lives with the same thread, task, card or skill
	relDay     = "day"     // the day of a date it carries
	relDayItem = "dayitem" // carries a date of the day it shows
)

var focusSettle = 800 * time.Millisecond // a focus unchanged this long is composed around

// relationWords are each relation as the composer reads it.
var relationWords = map[string]string{
	relSource:  "même source",
	relServer:  "même outil",
	relRef:     "même %s",
	relDay:     "son jour",
	relDayItem: "un élément de ce jour",
}

// refKindWords name what two surfaces share by its ref's kind.
var refKindWords = map[string]string{"thread": "thread", "task": "tâche", "card": "proposition", "proposal": "proposition", "skill": "compétence"}

// focusRelation is a surface related to the one open, and how.
type focusRelation struct {
	ID   string
	Kind string // relSource, relServer, relRef, relDay, relDayItem
	Ref  string // for relRef: the ref shared
}

// words is the relation in French, for a model's turn.
func (r focusRelation) words() string {
	if r.Kind == relRef {
		kind, _, _ := strings.Cut(r.Ref, ":")
		return fmt.Sprintf(relationWords[relRef], refKindWords[kind])
	}
	return relationWords[r.Kind]
}

var relationRank = map[string]int{relSource: 0, relRef: 1, relDay: 2, relDayItem: 2, relServer: 3}

// surfaceRelations are the surfaces of the pool related to the focused
// one, the closest first (then the heaviest), relationsMax at most. It
// reads only what surfaces declare: sources, lifetime, author, components
// and data. threadOf is the thread a task is a step of, 0 for none.
func surfaceRelations(focus surface, pool []surface, threadOf func(task string) int64) []focusRelation {
	fsrc, fsrv := sourceKeys(focus)
	frefs := liveRefs(focus, threadOf)
	fdates, fdays := datesOf(focus), daysOf(focus)
	var out []focusRelation
	weight := map[string]int{}
	for _, s := range pool {
		if s.ID == focus.ID {
			continue
		}
		weight[s.ID] = s.Weight
		src, srv := sourceKeys(s)
		r := focusRelation{ID: s.ID}
		switch {
		case overlap(fsrc, src) != "":
			r.Kind = relSource
		case overlap(frefs, liveRefs(s, threadOf)) != "":
			r.Kind, r.Ref = relRef, overlap(frefs, liveRefs(s, threadOf))
		case overlap(fdates, daysOf(s)) != "":
			r.Kind = relDay
		case overlap(fdays, datesOf(s)) != "":
			r.Kind = relDayItem
		case overlap(fsrv, srv) != "":
			r.Kind = relServer
		default:
			continue
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b focusRelation) int {
		return cmp.Or(cmp.Compare(relationRank[a.Kind], relationRank[b.Kind]), cmp.Compare(weight[b.ID], weight[a.ID]), cmp.Compare(a.ID, b.ID))
	})
	if len(out) > relationsMax {
		out = out[:relationsMax]
	}
	return out
}

// overlap is a key both sets hold, the least first; "" for none.
func overlap(a, b map[string]bool) string {
	var keys []string
	for k := range a {
		if b[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return slices.Min(keys)
}

// sourceKeys are what a surface reads: each source exactly (a tool and its
// arguments, or a resource), and each server it reads from.
func sourceKeys(s surface) (sources, servers map[string]bool) {
	sources, servers = map[string]bool{}, map[string]bool{}
	for _, src := range s.Sources {
		if src.Resource != "" {
			sources["resource\x00"+src.Resource+"\x00"+src.URI] = true
			servers[src.Resource] = true
			continue
		}
		args, _ := json.Marshal(src.Args) // map keys sorted: the same arguments, the same bytes
		sources["tool\x00"+src.Tool+"\x00"+string(args)] = true
		server, _, _ := strings.Cut(src.Tool, "/")
		servers[server] = true
	}
	return sources, servers
}

// liveRefs are the refs a surface lives with: the one its lifetime waits on
// and the one its author stands for (a lead its thread, a task itself and
// the thread it is a step of, a skill itself). The brain and code stand
// for no one in particular.
func liveRefs(s surface, threadOf func(task string) int64) map[string]bool {
	out := map[string]bool{}
	add := func(ref string) {
		kind, id, _ := strings.Cut(ref, ":")
		switch kind {
		case "thread", "card", "skill":
			out[ref] = true
		case "lead":
			out["thread:"+id] = true
		case "task":
			out[ref] = true
			if threadOf != nil {
				if n := threadOf(id); n > 0 {
					out["thread:"+strconv.FormatInt(n, 10)] = true
				}
			}
		}
	}
	add(s.Lifetime.While)
	add(s.Author)
	for _, ref := range proposalsIn(s) {
		out["proposal:"+ref] = true // the queue's tile of the same proposal, answerable there
	}
	return out
}

// proposalsIn are the proposals a surface's Proposal components show:
// their proposalId, written or bound to its data (under a template too).
func proposalsIn(s surface) []string {
	var comps []map[string]any
	if json.Unmarshal(s.Components, &comps) != nil {
		return nil
	}
	var out []string
	for _, comp := range comps {
		if ref, ok := comp["proposalId"].(string); ok && comp["component"] == "Proposal" {
			out = append(out, ref)
		}
	}
	var data any
	json.Unmarshal(s.Data, &data)
	for _, b := range a2ui.Eriphe().BoundPaths(s.Components) {
		if b.Prop == "proposalId" && !b.List {
			for _, v := range valuesAt(data, tokens(b.Path)) {
				if ref, ok := v.(string); ok {
					out = append(out, ref)
				}
			}
		}
	}
	return out
}

var isoDayRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:$|T|\s)`)

// datesOf are the days (YYYY-MM-DD) a surface carries: every string of its
// data and components that starts with an ISO date, datesMax at most.
func datesOf(s surface) map[string]bool {
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		if len(out) >= datesMax {
			return
		}
		switch x := v.(type) {
		case string:
			if m := isoDayRe.FindStringSubmatch(x); m != nil {
				if _, err := time.Parse(time.DateOnly, m[1]); err == nil {
					out[m[1]] = true
				}
			}
		case map[string]any:
			for _, k := range slices.Sorted(func(yield func(string) bool) {
				for k := range x {
					if !yield(k) {
						return
					}
				}
			}) {
				walk(x[k])
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, raw := range []json.RawMessage{s.Data, s.Components} {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walk(v)
		}
	}
	return out
}

// daysOf are the days a surface shows as a day: its Day components' dates,
// written or bound to its data.
func daysOf(s surface) map[string]bool {
	out := map[string]bool{}
	var comps []map[string]any
	if json.Unmarshal(s.Components, &comps) != nil {
		return out
	}
	var data any
	json.Unmarshal(s.Data, &data)
	for _, comp := range comps {
		if comp["component"] != "Day" {
			continue
		}
		var vals []any
		switch d := comp["date"].(type) {
		case string:
			vals = []any{d}
		case map[string]any:
			if p, ok := d["@path"].(string); ok {
				vals = valuesAt(data, tokens(p))
			}
		}
		for _, v := range vals {
			if str, ok := v.(string); ok {
				if m := isoDayRe.FindStringSubmatch(str); m != nil {
					out[m[1]] = true
				}
			}
		}
	}
	return out
}

// focusRelations are the surfaces related to the one open, from the pool.
func (c *core) focusRelations(id string, pool []surface) []focusRelation {
	i := slices.IndexFunc(pool, func(s surface) bool { return s.ID == id })
	if i < 0 {
		return nil
	}
	return surfaceRelations(pool[i], pool, func(task string) int64 {
		n, _ := c.threadOfTask(task) // web_threads.go
		return n
	})
}

// focusState is what eriphe-core keeps of the focus (in layoutState): the
// choice made around it and what the base was when it began.
type focusState struct {
	choice     *workspaceChoice // composed around For, since Since
	choiceFor  string
	choiceAt   time.Time
	baseStamp  string        // the pool and the queue when the focus began
	baseStale  bool          // the base choice's reason changed meanwhile: composed again on back
	kick       chan struct{} // the focus changed (runComposer)
	settledFor string        // the last focus settleFocus handled
	by         string        // who put the focus there: "" the user, focusByBrain (workspace_front.go)
	prev       string        // what was on stage before: a tile reduced next to it (workspace_front.go)
}

// focusKey names one focus: a surface opened at a time.
func focusKey(id string, since time.Time) string {
	if id == "" {
		return ""
	}
	return id + "@" + strconv.FormatInt(since.UnixNano(), 10)
}

// focusKicked is the channel told when the focus changes.
func (c *core) focusKicked() <-chan struct{} {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.f.kick == nil {
		st.f.kick = make(chan struct{}, 1)
	}
	return st.f.kick
}

// focusChangedLocked notes a new focus (c.layout.mu held): from none, the
// base is what back returns to; the composer is told.
func (c *core) focusChangedLocked(was string) {
	st := &c.layout
	if was == "" && st.focus != "" {
		st.f.baseStamp = c.baseStamp()
		st.f.baseStale = false
	}
	if st.f.kick == nil {
		st.f.kick = make(chan struct{}, 1)
	}
	select {
	case st.f.kick <- struct{}{}:
	default:
	}
}

// baseStamp is what of the pool and the queue the base choice depends on.
func (c *core) baseStamp() string { return c.queueStamp() + c.poolStamp() }

// focusNow is the focus now and since when, "" for none: a surface that
// is no longer there is open no more.
func (c *core) focusNow(now time.Time) (string, time.Time) {
	id := c.workspaceFocus(now)
	if id != "" {
		if _, err := c.surface(id); err != nil {
			id = ""
		}
	}
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	if id == "" {
		return "", time.Time{}
	}
	return id, st.focusSince
}

// layoutChoiceLocked is the choice laid out (c.layout.mu held): the one
// composed around the focus while it lasts, the base otherwise.
func (c *core) layoutChoiceLocked(focus string, pool []surface) workspaceChoice {
	st := &c.layout
	if f := st.f.choice; f != nil && focus != "" && st.f.choiceFor == focusKey(focus, st.focusSince) &&
		slices.ContainsFunc(pool, func(s surface) bool { return s.ID == focus }) {
		return *f
	}
	return c.choiceLocked()
}

// keepFocusChoice keeps a choice made around a focus, if that focus still
// lasts; false when it ended or changed meanwhile: the choice is dropped.
func (c *core) keepFocusChoice(id string, since time.Time, ch workspaceChoice) bool {
	st := &c.layout
	st.mu.Lock()
	if st.focus != id || !st.focusSince.Equal(since) {
		st.mu.Unlock()
		return false
	}
	st.f.choice, st.f.choiceFor, st.f.choiceAt = &ch, focusKey(id, since), time.Now()
	st.mu.Unlock()
	c.relayout()
	return true
}

// markBaseStale says the base choice must be composed again once the
// focus ends: the pool, the queue or a preference changed meanwhile.
func (c *core) markBaseStale() {
	st := &c.layout
	st.mu.Lock()
	st.f.baseStale = true
	st.mu.Unlock()
}

// focusFallback is the choice around a focus by code: it, then what relates
// to it by closeness; everything seen, so the rest waits folded (the urgent
// and what the user placed are placed by code all the same). With nothing
// related, the rest fills the room left by weight.
func focusFallback(s poolSnapshot) workspaceChoice {
	ch := fallbackChoice(s.Moment, s.Now)
	if _, ok := s.entry(s.Focus); !ok {
		return ch
	}
	if len(s.Related) > 0 {
		ch.Seen = []string{}
		for _, e := range s.Pool {
			ch.Seen = append(ch.Seen, e.ID)
		}
	}
	ch.Items = append(ch.Items, choiceItem{ID: s.Focus, Size: sizeXL})
	for _, r := range s.Related {
		e, _ := s.entry(r.ID)
		size := sizeOfWeight(e.Weight)
		if sizeRank[size] > sizeRank[sizeM] {
			size = sizeM
		}
		ch.Items = append(ch.Items, choiceItem{ID: r.ID, Size: size})
	}
	ch.Related = relatedIDs(s.Related)
	return ch
}

func relatedIDs(rs []focusRelation) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// settleFocus acts on the focus once it settled: a new one is composed
// around; one that ended gives the base back,
// composed again only if its reason changed (then true: the caller
// recomposes).
func (c *core) settleFocus(now time.Time) (recompose bool) {
	id, since := c.focusNow(now)
	key := focusKey(id, since)
	st := &c.layout
	st.mu.Lock()
	if key == st.f.settledFor {
		st.mu.Unlock()
		return false
	}
	st.f.settledFor = key
	if id == "" {
		stale := st.f.baseStale || (st.f.baseStamp != "" && st.f.baseStamp != c.baseStamp())
		st.f.choice, st.f.choiceFor, st.f.baseStamp, st.f.baseStale = nil, "", "", false
		st.f.by, st.f.prev = "", ""
		st.mu.Unlock()
		c.relayout()
		return stale
	}
	st.mu.Unlock()
	c.recompose(momentFocus, "")
	return false
}

// focusLapsed tells whether the focus ended with no one telling (its time
// passed, or its surface went): the end is settled on the composer's tick.
func (c *core) focusLapsed(now time.Time) bool {
	id, _ := c.focusNow(now)
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	return id == "" && st.f.settledFor != ""
}
