package main

import (
	"math"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A glance's figure, read after read: past its threshold once the
// variation reaches it, not again until it falls under glanceOff of it;
// seen when opened, re-armed once under glanceOff; a new day starts from
// its first value.
func TestGlanceStep(t *testing.T) {
	loc := time.FixedZone("Paris", 3600)
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, loc)
	g := nodefile.Glance{Label: "PEA", Path: "total", Threshold: 2}
	var st glanceState
	read := func(v float64, at time.Duration, open bool) *graphGlance {
		t.Helper()
		st = stepGlance(st, g, v, start.Add(at), loc, open)
		return st.view(g)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

	if x := read(100, 0, false); x.Shown || x.Delta != 0 || x.Ref != 100 || x.Value != 100 || x.Unit != nodefile.GlancePercent {
		t.Fatalf("first read: %+v", x)
	}
	if x := read(101.9, 5*time.Minute, false); x.Shown || !near(x.Delta, 1.9) {
		t.Fatalf("under its threshold: %+v", x)
	}
	if x := read(102.1, 10*time.Minute, false); !x.Shown || !near(x.Delta, 2.1) || x.Seen {
		t.Fatalf("past its threshold: %+v", x)
	}
	// Hovering just under it: still past (0.85 × 2 = 1.7), no flapping.
	if x := read(101.8, 15*time.Minute, false); !x.Shown {
		t.Fatalf("flapped at 1.8 %%: %+v", x)
	}
	if x := read(101.6, 20*time.Minute, false); x.Shown {
		t.Fatalf("still past at 1.6 %%: %+v", x)
	}
	// Crosses again, seen: stays seen while past, re-armed under 1.7 %.
	read(102.5, 25*time.Minute, false)
	st.Seen = true // the user opened it (glanceOpened)
	if x := read(103, 30*time.Minute, false); !x.Shown || !x.Seen {
		t.Fatalf("seen and past: %+v", x)
	}
	if x := read(101.5, 35*time.Minute, false); x.Shown || x.Seen {
		t.Fatalf("not re-armed: %+v", x)
	}
	if x := read(102.4, 40*time.Minute, false); !x.Shown || x.Seen {
		t.Fatalf("crossed again: %+v", x)
	}
	// Crossing under the user's eyes, its page open: seen at once.
	read(100, 45*time.Minute, false)
	if x := read(97, 50*time.Minute, true); !x.Shown || !x.Seen || !near(x.Delta, -3) {
		t.Fatalf("crossed while open: %+v", x)
	}
	// The next day: its first value is the reference, its line afresh.
	if x := read(97.5, 24*time.Hour, false); x.Shown || x.Ref != 97.5 || len(x.Series) != 1 {
		t.Fatalf("a new day: %+v", x)
	}
}

// Its line: values glanceSample apart, the last one live, glanceSeries at
// most; compared to the previous value, or a fixed base in its units.
func TestGlanceSeriesAndReferences(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 11, 20, 8, 0, 0, 0, loc)
	g := nodefile.Glance{Label: "Conso", Path: "kw", Threshold: 2, Compare: nodefile.GlancePrevious}
	var st glanceState
	for i, v := range []float64{10, 10, 10.1} {
		st = stepGlance(st, g, v, start.Add(time.Duration(i)*5*time.Minute), loc, false)
	}
	if x := st.view(g); len(x.Series) != 2 || x.Series[1] != 10.1 || !(math.Abs(x.Delta-1) < 1e-9) {
		t.Fatalf("live last value: %+v", x)
	}
	st = stepGlance(st, g, 11, start.Add(31*time.Minute), loc, false)
	if x := st.view(g); len(x.Series) != 3 || x.Ref != 10.1 || !x.Shown {
		t.Fatalf("a new sample, from the previous value: %+v", x)
	}
	for i := range 100 {
		st = stepGlance(st, g, 11, start.Add(time.Hour+time.Duration(i)*glanceSample), loc, false)
	}
	if n := len(st.view(g).Series); n != glanceSeries {
		t.Fatalf("%d values in its line", n)
	}
	// The same value again: no change, the reference stays the previous one.
	if x := st.view(g); x.Ref != 10.1 || x.Delta == 0 {
		t.Fatalf("previous: %+v", x)
	}
	base := 4.0
	fixed := nodefile.Glance{Label: "Erreurs", Path: "rate", Threshold: 5, Compare: nodefile.GlanceFixed, Base: &base, Unit: nodefile.GlanceAmount}
	var e glanceState
	e = stepGlance(e, fixed, 12, start, loc, false)
	if x := e.view(fixed); !x.Shown || x.Delta != 8 || x.Ref != 4 || x.Unit != nodefile.GlanceAmount {
		t.Fatalf("fixed, in amount: %+v", x)
	}
}

// glanceBody is a node's body whose block reads a source with a glance.
func glanceBody(glance string) string {
	return "Le PEA.\n\n```eriphe:block pea\n" +
		`{"components": [{"id": "root", "component": "Text", "text": "PEA"}], "sources": {"pea": {"tool": "bourse/portefeuille", "glance": ` + glance + `}}}` + "\n```\n"
}

// A node's glance, from its source's reads to its row in the graph: kept
// in core.db, so a restart and every screen know it; opening the node in
// the workspace marks it seen; a glance taken away, or its node gone, is
// forgotten.
func TestNodeGlance(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	id := mustNode(t, c, nodestore.New{Title: "Investissements", Body: glanceBody(`{"label": "PEA", "path": "total", "threshold": 2}`), Author: byBrain})
	read := func(v any, at time.Time) *graphGlance {
		t.Helper()
		reads := c.nodeReads(id)
		if len(reads) != 1 {
			t.Fatalf("reads %+v", reads)
		}
		reads[0].Value = v
		c.noteGlance(id, reads, at)
		sum, _ := c.nodes.Get(id)
		return c.graphRow(sum).Glance
	}
	now := time.Now()
	if x := read(nil, now); x != nil {
		t.Fatalf("a glance before any figure: %+v", x)
	}
	if x := read(map[string]any{"total": "100 €"}, now); x != nil {
		t.Fatalf("words taken for a figure: %+v", x)
	}
	head, _ := c.graphHead()
	if x := read(map[string]any{"total": 100.0}, now); x == nil || x.Label != "PEA" || x.Value != 100 || x.Shown || x.LabelExternal {
		t.Fatalf("first figure: %+v", x)
	}
	if evs, _, _, _ := c.graphAfter(head); len(evs) == 0 {
		t.Error("the graph is not told of its glance")
	}
	if x := read(map[string]any{"total": 103.0}, now.Add(time.Minute)); !x.Shown || x.Seen {
		t.Fatalf("past its threshold: %+v", x)
	}
	// Opened in the workspace: seen, on every screen, after a restart too.
	if code, _ := gesture(t, tw, csrf, "PUT", "/api/espace/view", espaceView{Open: []string{id}, Talk: id}); code != http.StatusNoContent {
		t.Fatalf("view: %d", code)
	}
	c.glances = sync.Map{}
	sum, _ := c.nodes.Get(id)
	if x := c.graphRow(sum).Glance; x == nil || !x.Seen || !x.Shown || x.Value != 103 {
		t.Fatalf("not seen after a restart: %+v", x)
	}
	// Closed; the figure falls back under 1.7 %: re-armed.
	gesture(t, tw, csrf, "PUT", "/api/espace/view", espaceView{Open: []string{}})
	if x := read(map[string]any{"total": 101.0}, now.Add(2*time.Minute)); x.Shown || x.Seen {
		t.Fatalf("re-armed: %+v", x)
	}
	if x := read(map[string]any{"total": 102.5}, now.Add(3*time.Minute)); !x.Shown || x.Seen {
		t.Fatalf("crossed again: %+v", x)
	}
	// Its glance changed: from its next figure, afresh.
	d, _ := c.nodes.Read(id, 0)
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, func(nodefile.Source) error { return nil }, func(n *nodefile.Node) error {
		return n.AuthorBody(glanceBody(`{"label": "PEA", "path": "total", "threshold": 5}`))
	}); err != nil {
		t.Fatal(err)
	}
	if x := read(map[string]any{"total": 102.5}, now.Add(4*time.Minute)); x.Threshold != 5 || x.Ref != 102.5 || x.Shown {
		t.Fatalf("a changed glance: %+v", x)
	}
	// Taken away: gone, and forgotten.
	d, _ = c.nodes.Read(id, 0)
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, func(nodefile.Source) error { return nil }, func(n *nodefile.Node) error {
		return n.AuthorBody("```eriphe:block pea\n" + `{"components": [{"id": "root", "component": "Text", "text": "PEA"}], "sources": {"pea": {"tool": "bourse/portefeuille"}}}` + "\n```\n")
	}); err != nil {
		t.Fatal(err)
	}
	if x := read(map[string]any{"total": 110.0}, now.Add(5*time.Minute)); x != nil {
		t.Fatalf("a glance taken away: %+v", x)
	}
	if _, ok := c.loadGlance(id); ok {
		t.Error("a glance taken away is still kept")
	}
	c.saveGlance(id, glanceState{Spec: "{}"})
	c.forgetGlances(map[string]bool{})
	if _, ok := c.loadGlance(id); ok {
		t.Error("the glance of a node gone is still kept")
	}
}

// One glance per node, and its label marked when its block may hold
// someone else's words.
func TestNodeGlanceGuard(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	two := glanceBody(`{"label": "PEA", "path": "total", "threshold": 2}`) + "\n```eriphe:block cto\n" +
		`{"components": [{"id": "root", "component": "Text", "text": "CTO"}], "sources": {"cto": {"tool": "bourse/compte", "glance": {"label": "CTO", "path": "total", "threshold": 2}}}}` + "\n```\n"
	id := mustNode(t, c, nodestore.New{Title: "Investissements", Body: "Rien.\n", Author: byBrain})
	d, _ := c.nodes.Read(id, 0)
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, func(nodefile.Source) error { return nil }, func(n *nodefile.Node) error { return n.AuthorBody(two) }); err == nil {
		t.Fatal("two glances on a node")
	}
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, true, func(nodefile.Source) error { return nil }, func(n *nodefile.Node) error {
		return n.AuthorBody(glanceBody(`{"label": "PEA", "path": "total", "threshold": 2}`))
	}); err != nil {
		t.Fatal(err)
	}
	reads := c.nodeReads(id)
	reads[0].Value = map[string]any{"total": 1.0}
	c.noteGlance(id, reads, time.Now())
	if x := c.nodeGlance(id); x == nil || !x.LabelExternal {
		t.Fatalf("a label written after an external read is not marked: %+v", x)
	}
}
