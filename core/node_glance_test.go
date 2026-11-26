package main

import (
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
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

// glancePage is a page whose callout shows the PEA at a glance, its line
// following the monitor pea.
func glancePage(opts string) string {
	return "La bourse.\n\n> [!figures" + opts + "]\n> - PEA : [100 €](on:monitor.changed/pea)\n"
}

// A node's glance follows its line: the values an event writes there, kept
// in core.db so a restart and every screen know it; opening the node in
// the workspace marks it seen; a glance changed starts afresh, one taken
// away, or its node archived, is forgotten.
func TestNodeGlance(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	id := mustNode(t, c, nodestore.New{Title: "Investissements", Body: glancePage("|glance 2%"), Author: byBrain})
	now := time.Now()
	row := func() *graphGlance {
		sum, _ := c.nodes.Get(id)
		return c.graphRow(sum).Glance
	}
	hear := func(v any, at time.Time) *graphGlance {
		t.Helper()
		c.heard(journalEvent{Kind: evMonitorChanged, Key: "pea", Source: "monitor:pea", At: at, Payload: map[string]any{"value": v}})
		return row()
	}
	edit := func(body string) {
		t.Helper()
		d, _ := c.nodes.Read(id, 0)
		if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, func(nodefile.Source) error { return nil }, func(n *nodefile.Node) error {
			return n.AuthorBody(body)
		}); err != nil {
			t.Fatal(err)
		}
		c.noteGlance(id, now) // as runNodes does after a change of the file
	}
	if x := row(); x != nil {
		t.Fatalf("a glance before its page was read: %+v", x)
	}
	head, _ := c.graphHead()
	c.noteGlance(id, now)
	if x := row(); x == nil || x.Label != "PEA" || x.Value != 100 || x.Shown || x.LabelExternal {
		t.Fatalf("its line's figure: %+v", x)
	}
	if evs, _, _, _ := c.graphAfter(head); len(evs) == 0 {
		t.Error("the graph is not told of its glance")
	}
	if x := hear(101.0, now); x.Shown || x.Value != 101 {
		t.Fatalf("under its threshold: %+v", x)
	}
	if x := hear(103.0, now.Add(time.Minute)); !x.Shown || x.Seen || x.Value != 103 {
		t.Fatalf("past its threshold: %+v", x)
	}
	// Opened in the workspace: seen, on every screen, after a restart too.
	if code, _ := gesture(t, tw, csrf, "PUT", "/api/espace/view", espaceView{Open: []string{id}, Talk: id}); code != http.StatusNoContent {
		t.Fatalf("view: %d", code)
	}
	c.glances = sync.Map{}
	if x := row(); x == nil || !x.Seen || !x.Shown || x.Value != 103 {
		t.Fatalf("not seen after a restart: %+v", x)
	}
	// Closed; the figure falls back under 1.7 %: re-armed.
	gesture(t, tw, csrf, "PUT", "/api/espace/view", espaceView{Open: []string{}})
	if x := hear(101.0, now.Add(2*time.Minute)); x.Shown || x.Seen {
		t.Fatalf("re-armed: %+v", x)
	}
	if x := hear(102.5, now.Add(3*time.Minute)); !x.Shown || x.Seen {
		t.Fatalf("crossed again: %+v", x)
	}
	// Its threshold changed: from the line's value, afresh.
	d, _ := c.nodes.Read(id, 0)
	body, _ := d.Node.Body()
	edit(strings.Replace(body, "glance 2%", "glance 5%", 1))
	if x := row(); x.Threshold != 5 || x.Ref != 103 || x.Value != 103 || x.Shown {
		t.Fatalf("a changed glance: %+v", x)
	}
	// Taken away: gone, and forgotten.
	edit(glancePage(""))
	if x := row(); x != nil {
		t.Fatalf("a glance taken away: %+v", x)
	}
	if _, ok := c.loadGlance(id); ok {
		t.Error("a glance taken away is still kept")
	}
	// Declared by the node's property instead, then archived.
	d, _ = c.nodes.Read(id, 0)
	if _, err := c.nodes.Change(id, d.Version.Hash, byBrain, func(n *nodefile.Node) error { return n.Meta.SetProp("glance", "10%") }); err != nil {
		t.Fatal(err)
	}
	c.noteGlance(id, now)
	if x := row(); x == nil || x.Threshold != 10 {
		t.Fatalf("by its property: %+v", x)
	}
	if err := c.nodes.Archive(id, byUser); err != nil {
		t.Fatal(err)
	}
	c.noteGlance(id, now)
	if _, ok := c.loadGlance(id); ok {
		t.Error("the glance of a node archived is still kept")
	}
}

// An event written by someone else marks the callout its value is in:
// the tile says its label may be someone else's words, and no author takes
// the mark off.
func TestNodeGlanceExternal(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	id := mustNode(t, c, nodestore.New{Title: "Investissements", Body: glancePage("|glance 2%"), Author: byBrain})
	c.heard(journalEvent{Kind: evMonitorChanged, Key: "pea", Source: "monitor:pea", At: time.Now(), External: true, Payload: map[string]any{"value": 98.0}})
	if x := c.nodeGlance(id); x == nil || !x.LabelExternal || x.Value != 98 {
		t.Fatalf("a label in a callout marked external is not marked: %+v", x)
	}
	d, _ := c.nodes.Read(id, 0)
	body, _ := d.Node.Body()
	if !strings.Contains(body, "> [!figures|glance 2%|external]\n") {
		t.Fatalf("not marked:\n%s", body)
	}
	// Whoever reads it is tainted, the brain or a task.
	sum, _ := c.nodes.Get(id)
	var tainted []string
	if _, err := c.readNodeFor(sum, 0, nodeReading{taint: func(ref string) { tainted = append(tainted, ref) }}); err != nil || !slices.Contains(tainted, nodeRef(id)) {
		t.Errorf("a read of it tainted %v (%v)", tainted, err)
	}
	_, err := c.authorWrite(id, d.Version.Hash, byBrain, false, func(nodefile.Source) error { return nil }, func(n *nodefile.Node) error {
		return n.AuthorBody(strings.Replace(body, "|external", "", 1))
	})
	if !errors.Is(err, nodefile.ErrCodeField) {
		t.Fatalf("an author took the mark off: %v", err)
	}
}
