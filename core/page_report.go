package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The render report: what a page shows once written, said back to its
// author in the same turn, so that it fixes its own page instead of
// leaving it wrong. Each block's state (ok, error, waiting, empty), its
// sources', the rows each of its lists shows, the bindings that lead
// nowhere in what a source gave, the setup buttons code hides, and, on a
// page that lasts, the figures written by hand. Code's words and numbers
// only: a source's error is quoted only when its server is trusted, so
// the report reads nothing someone else wrote.

// pageReportWait is how long a write waits for its page's sources: one
// slower says it is still reading.
const pageReportWait = 5 * time.Second

// Block states, as the report says them.
const (
	pageOK      = "ok"
	pageError   = "error"
	pageWaiting = "waiting"
	pageEmpty   = "empty"
)

// rowProps are the properties of a component bound to a list it draws a
// row of each item of (Table's rows, Agenda's events, Timeline's items).
var rowProps = []string{"rows", "events", "items"}

// pageReport reads node id's sources, waiting pageReportWait at most, and
// says what each of its blocks shows. A page whose blocks read sources
// lasts: a figure written by hand on it goes stale.
func (c *core) pageReport(id string) []map[string]any {
	if c.nodes == nil {
		return nil
	}
	d, err := c.nodes.Read(id, 0)
	if err != nil || len(d.Node.Blocks()) == 0 {
		return nil
	}
	// Read now; one already under way (its write's own, a node_find
	// source's) reads it all again after, and is waited for too.
	deadline := time.Now().Add(pageReportWait)
	done := make(chan struct{})
	c.live.wg.Add(1)
	go func() {
		defer c.live.wg.Done()
		defer close(done)
		c.readNode(context.Background(), id, nil, time.Now())
		for time.Now().Before(deadline) {
			if _, busy := c.live.busy.Load(id); !busy {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
	}
	reads := c.keptReads(id)
	lasting := slices.ContainsFunc(d.Node.Blocks(), func(b *nodefile.Block) bool { return len(b.Sources) > 0 })
	var out []map[string]any
	for _, b := range d.Node.Blocks() {
		if b.Card != "" {
			continue
		}
		out = append(out, c.blockReport(id, b, reads, lasting))
	}
	return out
}

// blockReport is what one block shows.
func (c *core) blockReport(id string, b *nodefile.Block, reads []nodeRead, lasting bool) map[string]any {
	r := map[string]any{"block": b.Name}
	if err := b.Validate(); err != nil {
		r["state"], r["error"] = pageError, err.Error()
		return r
	}
	v := c.blockView(id, b, reads)
	state := pageOK
	worse := func(s string) {
		rank := map[string]int{pageOK: 0, pageEmpty: 1, pageWaiting: 2, pageError: 3}
		if rank[s] > rank[state] {
			state = s
		}
	}
	trusted := !b.External && !c.untrustedSources(b)
	read := map[string]bool{} // the sources with a value
	if len(b.Sources) > 0 {
		srcs := map[string]string{}
		for _, name := range slices.Sorted(maps.Keys(b.Sources)) {
			got, ok := readOf(reads, b.Name, name)
			switch {
			case ok && got.Error != "":
				msg := "failed"
				if trusted {
					msg += ": " + truncate(got.Error, 200)
				}
				srcs[name] = msg
				worse(pageError)
			case ok && got.Waiting != "":
				srcs[name] = "waits: " + got.Waiting
				worse(pageWaiting)
			case !ok || got.Value == nil:
				srcs[name] = "still reading"
				worse(pageWaiting)
			default:
				srcs[name] = pageOK
			}
			read[name] = ok && got.Value != nil
		}
		r["sources"] = srcs
	}
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	rows, nowhere := c.blockRows(b, comps, v.Data, read, trusted)
	if len(rows) > 0 {
		r["rows"] = rows
		if !slices.ContainsFunc(slices.Collect(maps.Values(rows)), func(n int) bool { return n > 0 }) {
			worse(pageEmpty)
		}
	}
	var problems []string
	for _, p := range nowhere {
		problems = append(problems, p)
		worse(pageError)
	}
	if lasting {
		for _, comp := range comps {
			if _, written := comp["value"].(map[string]any); comp["component"] == "Metric" && comp["value"] != nil && !written {
				problems = append(problems, fmt.Sprintf("Metric %v: a figure written by hand goes stale on a page that lasts: bind it to its source's field (@path) or count a List (list)", comp["id"]))
			}
		}
	}
	if len(problems) > 0 {
		r["problems"] = problems
	}
	if hidden := c.setUpButtons(comps); len(hidden) > 0 {
		r["hidden"] = hidden // what they set up is: code hides them
	}
	r["state"] = state
	return r
}

// readOf is the read kept of a block's source.
func readOf(reads []nodeRead, block, name string) (nodeRead, bool) {
	for _, r := range reads {
		if r.Block == block && r.Name == name {
			return r, true
		}
	}
	return nodeRead{}, false
}

// blockRows are the rows each list of a block shows, by component (a
// List's or a Grid's template, a Table's rows…), its filters applied, and
// what its bindings into the sources read find nothing at: what is there
// instead said only when its servers are trusted (trusted), its keys
// being the source's words.
func (c *core) blockRows(b *nodefile.Block, comps []map[string]any, data map[string]any, read map[string]bool, trusted bool) (map[string]int, []string) {
	rows := map[string]int{}
	var nowhere []string
	seen := map[string]bool{}
	for _, bound := range a2ui.Eriphe().BoundPaths(b.Components) {
		toks := tokens(bound.Path)
		name := at0(toks)
		if _, ok := b.Sources[name]; ok && !read[name] {
			continue // not read yet: nothing to say of it
		}
		if strings.Contains(bound.Path, "[") && bound.List {
			// A List that filters its source's items: the rows code keeps.
			src, f, err := listBinding(bound.Path, b.Sources)
			if err == nil {
				rows[bound.Component] = len(sourceRows(src, b.Sources[src], data[src], f))
			}
			continue
		}
		toks = unfiltered(toks)
		if _, ok := b.Sources[name]; ok && !slices.ContainsFunc(toks, codeField) {
			if ok, hint := nodefile.Leads(data, toks, bound.List); !ok && !seen[bound.Path] {
				seen[bound.Path] = true
				if !trusted {
					hint = ""
				}
				nowhere = append(nowhere, fmt.Sprintf("%s %s: nothing at %s in what the source %s gave%s", bound.Type, bound.Component, bound.Path, name, hint))
				continue
			}
		}
		if slices.Contains(toks, "*") || !bound.List && !slices.Contains(rowProps, bound.Prop) {
			continue
		}
		if list, ok := valueAt(data, toks).([]any); ok {
			rows[bound.Component] = len(list)
		}
	}
	return rows, nowhere
}

// codeField tells a token that names a field code adds to a source's
// items (_note, _state…): an item has it only once noted.
func codeField(t string) bool { return strings.HasPrefix(t, "_") }

// valueAt is the value a path's tokens lead to in v; nil when none.
func valueAt(v any, toks []string) any {
	for _, t := range toks {
		switch x := v.(type) {
		case map[string]any:
			v = x[t]
		case []any:
			i, err := strconv.Atoi(t)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}
