package main

import (
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// Focus (#429): one workspace, and opening a surface does what a separate
// dashboard would. The surface the user opens (the open action,
// surface_actions.go) is placed first by code at once (workspace_layout.go):
// it large, what relates to it around, the rest folded. Nothing is sent to
// the brain (#550) and no model is asked: opening a surface, or going back
// to one, is not a turn; what it shows says where things stand.
//
//   - Relations are found by code, from what every surface declares, never
//     from a tool's name (surfaceRelations): the same source, the same
//     server, the same ref it lives while or its author stands for (a
//     thread and its tasks, a task, a skill), the same
//     proposal shown in a Proposal (the queue's tile of it), a day: a Day
//     component on a date the focused surface carries, or the reverse.
//   - Back (or the focus closed, removed or past focusFor) lays the
//     workspace out again at once, in its order.

const (
	relationsMax = 6
	datesMax     = 64

	relSource  = "source"  // reads the same thing
	relServer  = "server"  // reads from the same server
	relRef     = "ref"     // lives with the same thread, task, card or skill
	relDay     = "day"     // the day of a date it carries
	relDayItem = "dayitem" // carries a date of the day it shows
)

// focusRelation is a surface related to the one open, and how.
type focusRelation struct {
	ID   string
	Kind string // relSource, relServer, relRef, relDay, relDayItem
	Ref  string // for relRef: the ref shared
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
// and the one its author stands for (a task itself and the thread it is
// about, a skill itself). The brain and code stand
// for no one in particular.
func liveRefs(s surface, threadOf func(task string) int64) map[string]bool {
	out := map[string]bool{}
	add := func(ref string) {
		kind, id, _ := strings.Cut(ref, ":")
		switch kind {
		case "thread", "card", "skill":
			out[ref] = true
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
		return c.refThread("task:" + task) // thread_progress.go
	})
}

// focusState is what eriphe-core keeps of the focus (in layoutState).
type focusState struct {
	by   string // who put the focus there: "" the user, focusByBrain (workspace_front.go)
	prev string // what was on stage before: a tile reduced next to it (workspace_front.go)
}

func relatedIDs(rs []focusRelation) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}
