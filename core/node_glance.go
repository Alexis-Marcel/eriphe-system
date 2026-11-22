package main

import (
	"encoding/json"
	"math"
	"slices"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A node's glance: a figure one of its sources holds (a source's
// "glance", docs/node-format.md), that its bubble shows as a small tile
// when the figure moved past its threshold, until the user opens the
// node. Nothing here knows what the figure is: the PEA, the errors of the
// prod, the house's consumption.
//
//   - Code reads the figure at each read of its source (readNodeOnce: on
//     its period, after a change of its server, at a monitor's look),
//     without a model: the number at its path, a JSON number only.
//   - It keeps here, in core.db (node_glance), what the variation needs:
//     the figure's first value of the user's day, its value before it last
//     changed, its last one, and a few recent values for the tile's line,
//     one every glanceSample at most, the last one live.
//   - The variation is from the reference the glance says (the day's
//     first value, the previous one, a fixed base), in percent of it or in
//     the figure's units. The node is past its threshold once the variation
//     reaches it, and stays so until it falls under glanceOff of it: a
//     figure hovering at its threshold does not flap.
//   - Opening the node (the workspace's view, web_gestures.go) while it is
//     past its threshold marks it seen: its bubble is a circle again, on
//     every screen and after a reload, until the variation falls under
//     glanceOff and crosses again. A node open when it crosses is seen.
//   - The graph is told when what its tile shows changes (graphEvNode);
//     the workspace draws at most a few tiles, the furthest past their
//     threshold first, and never one for a node that waits for the user
//     (graph/glance.ts).
//   - Its label is the author's words: on a block marked external, it may
//     be someone else's, and the tile's words say so (labelExternal).

const nodeGlanceSchema = `CREATE TABLE IF NOT EXISTS node_glance (
	node   TEXT PRIMARY KEY,
	spec   TEXT NOT NULL,              -- the block, source and glance as declared: one changed starts afresh
	day    TEXT NOT NULL DEFAULT '',   -- the user's day of first
	first  REAL NOT NULL DEFAULT 0,    -- the figure's first value that day
	prev   REAL NOT NULL DEFAULT 0,    -- its value before it last changed
	value  REAL NOT NULL DEFAULT 0,    -- its last value
	series TEXT NOT NULL DEFAULT '[]', -- its recent values, [[unix, value], …]
	shown  INTEGER NOT NULL DEFAULT 0, -- past its threshold
	seen   INTEGER NOT NULL DEFAULT 0, -- opened since it crossed
	ext    INTEGER NOT NULL DEFAULT 0  -- its block is external: its label may be someone else's words
);`

func init() { extraSchema = append(extraSchema, nodeGlanceSchema) }

const (
	// glanceOff is the share of its threshold under which a glance past it
	// is no longer: it does not flap at the threshold.
	glanceOff = 0.85
	// glanceSample is the least time between two values of its line.
	glanceSample = 30 * time.Minute
	// glanceSeries is the most values of its line.
	glanceSeries = 32
)

// glanceSpec is what a glance is declared as: its block, source and
// glance, as one string.
type glanceSpec struct {
	Block  string          `json:"block"`
	Name   string          `json:"name"`
	Glance nodefile.Glance `json:"glance"`
}

// glanceState is a node's glance as kept.
type glanceState struct {
	Spec   string
	Day    string
	First  float64
	Prev   float64
	Value  float64
	Series [][2]float64 // [unix, value]
	Shown  bool
	Seen   bool
	Ext    bool
}

// graphGlance is a node's glance as the workspace gets it, on its row:
// its label, its last value, the reference and the variation from it (in
// its unit: percent or amount), its threshold, its recent values, whether
// it is past its threshold (shown) and seen since.
type graphGlance struct {
	Label         string    `json:"label"`
	LabelExternal bool      `json:"labelExternal,omitempty"`
	Unit          string    `json:"unit"`
	Value         float64   `json:"value"`
	Ref           float64   `json:"ref"`
	Delta         float64   `json:"delta"`
	Threshold     float64   `json:"threshold"`
	Series        []float64 `json:"series"`
	Shown         bool      `json:"shown,omitempty"`
	Seen          bool      `json:"seen,omitempty"`
}

// stepGlance is a glance's state after its figure was read as v at now,
// the user's day in loc; open tells the node's page is open (a crossing
// seen at once).
func stepGlance(st glanceState, g nodefile.Glance, v float64, now time.Time, loc *time.Location, open bool) glanceState {
	day := now.In(loc).Format(time.DateOnly)
	switch {
	case st.Day == "":
		st.Day, st.First, st.Prev, st.Value = day, v, v, v
	case day != st.Day:
		// A new day: its first value; the line of a day starts afresh.
		st.Day, st.First = day, v
		if g.Compare == "" || g.Compare == nodefile.GlanceDay {
			st.Series = nil
		}
	}
	if v != st.Value {
		st.Prev, st.Value = st.Value, v
	}
	// Its line: values glanceSample apart, then the last one, live.
	at := float64(now.Unix())
	if n := len(st.Series); n >= 2 && at-st.Series[n-2][0] < glanceSample.Seconds() {
		st.Series[n-1] = [2]float64{at, v}
	} else {
		st.Series = append(st.Series, [2]float64{at, v})
	}
	if n := len(st.Series); n > glanceSeries {
		st.Series = st.Series[n-glanceSeries:]
	}
	w := math.Abs(g.Variation(v, g.Reference(st.First, st.Prev))) / g.Threshold
	was := st.Shown
	st.Shown = w >= 1 || was && w >= glanceOff
	switch {
	case w < glanceOff:
		st.Seen = false // re-armed: it may cross again
	case st.Shown && !was && open:
		st.Seen = true // crossed under the user's eyes
	}
	return st
}

// view is what the workspace gets of a glance's state.
func (st glanceState) view(g nodefile.Glance) *graphGlance {
	if st.Day == "" {
		return nil // no figure read yet
	}
	ref := g.Reference(st.First, st.Prev)
	series := make([]float64, len(st.Series))
	for i, p := range st.Series {
		series[i] = p[1]
	}
	return &graphGlance{Label: g.Label, LabelExternal: st.Ext, Unit: cmpOr(g.Unit, nodefile.GlancePercent), Value: st.Value, Ref: ref,
		Delta: g.Variation(st.Value, ref), Threshold: g.Threshold, Series: series, Shown: st.Shown, Seen: st.Seen}
}

// loadGlance is a node's glance as kept; ok false when none.
func (c *core) loadGlance(id string) (glanceState, bool) {
	st, ok := c.glancesOf(`WHERE node = ?`, id)[id]
	return st, ok
}

// glancesOf are the glances kept in node_glance, by node.
func (c *core) glancesOf(where string, args ...any) map[string]glanceState {
	out := map[string]glanceState{}
	rows, err := c.store.db.Query(`SELECT node, spec, day, first, prev, value, series, shown, seen, ext FROM node_glance `+where, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st glanceState
		var id, series string
		var shown, seen, ext int
		if rows.Scan(&id, &st.Spec, &st.Day, &st.First, &st.Prev, &st.Value, &series, &shown, &seen, &ext) != nil {
			continue
		}
		json.Unmarshal([]byte(series), &st.Series)
		st.Shown, st.Seen, st.Ext = shown == 1, seen == 1, ext == 1
		out[id] = st
	}
	return out
}

// allGlances are every node's glance as the graph shows it (nodeGlance),
// read at once; a node with none is not there.
func (c *core) allGlances() map[string]*graphGlance {
	out := map[string]*graphGlance{}
	for id, st := range c.glancesOf(``) {
		out[id] = st.glanceView()
	}
	return out
}

// saveGlance keeps a node's glance.
func (c *core) saveGlance(id string, st glanceState) {
	series, _ := json.Marshal(st.Series)
	if st.Series == nil {
		series = []byte("[]")
	}
	c.store.db.Exec(`INSERT INTO node_glance (node, spec, day, first, prev, value, series, shown, seen, ext) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node) DO UPDATE SET spec = excluded.spec, day = excluded.day, first = excluded.first, prev = excluded.prev, value = excluded.value,
		  series = excluded.series, shown = excluded.shown, seen = excluded.seen, ext = excluded.ext`,
		id, st.Spec, st.Day, st.First, st.Prev, st.Value, string(series), boolInt(st.Shown), boolInt(st.Seen), boolInt(st.Ext))
}

// nodeGlance is a node's glance as its row gives it; nil when it has none.
func (c *core) nodeGlance(id string) *graphGlance {
	if v, ok := c.glances.Load(id); ok {
		return v.(*graphGlance)
	}
	var out *graphGlance
	if st, ok := c.loadGlance(id); ok {
		out = st.glanceView()
	}
	c.glances.Store(id, out)
	return out
}

// glanceView is what the graph shows of a glance kept: nil when its spec
// does not read.
func (st glanceState) glanceView() *graphGlance {
	var spec glanceSpec
	if json.Unmarshal([]byte(st.Spec), &spec) != nil {
		return nil
	}
	return st.view(spec.Glance)
}

// keepGlance keeps what the workspace shows of a node's glance, and tells
// the graph when it changed.
func (c *core) keepGlance(id string, g *graphGlance) {
	was := c.nodeGlance(id)
	c.glances.Store(id, g)
	a, _ := json.Marshal(was)
	b, _ := json.Marshal(g)
	if string(a) != string(b) {
		c.graphSaid(graphEvent{Kind: graphEvNode, Node: id})
	}
}

// noteGlance reads a node's glance from its sources' reads (the first
// source that declares one, by block then name), keeps it, and tells the
// graph when what its tile shows changed.
func (c *core) noteGlance(id string, reads []nodeRead, now time.Time) {
	i := slices.IndexFunc(reads, func(r nodeRead) bool { return r.Source.Glance != nil })
	if i < 0 {
		if _, ok := c.loadGlance(id); ok {
			c.store.db.Exec(`DELETE FROM node_glance WHERE node = ?`, id)
		}
		c.keepGlance(id, nil)
		return
	}
	r := reads[i]
	g := *r.Source.Glance
	raw, _ := json.Marshal(glanceSpec{Block: r.Block, Name: r.Name, Glance: g})
	st, ok := c.loadGlance(id)
	if !ok || st.Spec != string(raw) {
		st = glanceState{Spec: string(raw)} // new, or changed: from its next value
	}
	if v, ok := g.Figure(r.Value); ok {
		st = stepGlance(st, g, v, now, c.userZone(), slices.Contains(c.view().Open, id))
	}
	if d, err := c.nodes.Read(id, 0); err == nil {
		if b := d.Node.Block(r.Block); b != nil {
			st.Ext = b.External
		}
	}
	c.saveGlance(id, st)
	c.keepGlance(id, st.view(g))
}

// glanceOpened marks seen the glances past their threshold of the nodes
// the user just opened.
func (c *core) glanceOpened(ids []string) {
	for _, id := range ids {
		st, ok := c.loadGlance(id)
		if !ok || !st.Shown || st.Seen {
			continue
		}
		var spec glanceSpec
		if json.Unmarshal([]byte(st.Spec), &spec) != nil {
			continue
		}
		st.Seen = true
		c.saveGlance(id, st)
		c.keepGlance(id, st.view(spec.Glance))
	}
}

// forgetGlances drops the glances of the nodes no longer live.
func (c *core) forgetGlances(live map[string]bool) {
	rows, err := c.store.db.Query(`SELECT node FROM node_glance`)
	if err != nil {
		return
	}
	var gone []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && !live[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		c.store.db.Exec(`DELETE FROM node_glance WHERE node = ?`, id)
		c.glances.Delete(id)
	}
}
