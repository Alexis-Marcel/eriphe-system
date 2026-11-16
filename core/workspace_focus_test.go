package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Relations are found from what surfaces declare, never from a tool's
// name: the same source, the same ref they live with (a lead's thread, a
// task of that thread), a day and what is dated on it, the same server.
func TestSurfaceRelations(t *testing.T) {
	list := map[string]surfaceSource{"/rows": {Tool: "ledger/rows", Args: map[string]any{"since": "week", "n": 5}}}
	focus := surface{ID: "rows", Author: "lead:7", Sources: list, Lifetime: surfaceLifetime{While: "thread:7"},
		Data:       json.RawMessage(`{"next":{"due":"2026-11-13T09:30:00+01:00"}}`),
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}
	pool := []surface{
		focus,
		{ID: "same", Author: "brain", Weight: 20, Sources: map[string]surfaceSource{"/r": {Tool: "ledger/rows", Args: map[string]any{"n": 5, "since": "week"}}}},
		{ID: "server", Author: "brain", Weight: 90, Sources: map[string]surfaceSource{"/b": {Tool: "ledger/balances"}}},
		{ID: "task", Author: "task:t-1", Weight: 50},
		{ID: "lead-other", Author: "brain", Lifetime: surfaceLifetime{While: "thread:7"}, Weight: 60},
		{ID: "day", Author: "skill:morning", Weight: 50, Components: json.RawMessage(`[{"id":"d","component":"Day","date":{"@path":"/day"},"events":[]}]`),
			Data: json.RawMessage(`{"day":"2026-11-13"}`)},
		{ID: "other-day", Author: "brain", Components: json.RawMessage(`[{"id":"d","component":"Day","date":"2026-11-20","events":[]}]`)},
		{ID: "resource", Author: "brain", Sources: map[string]surfaceSource{"/x": {Resource: "notes", URI: "notes://today"}}},
		{ID: "unrelated", Author: "brain", Weight: 100},
	}
	threadOf := func(task string) int64 {
		if task == "t-1" {
			return 7
		}
		return 0
	}
	got := surfaceRelations(focus, pool, threadOf)
	var b strings.Builder
	for _, r := range got {
		b.WriteString(r.ID + ":" + r.Kind + ":" + r.Ref + " ")
	}
	want := "same:source: lead-other:ref:thread:7 task:ref:thread:7 day:day: server:server: "
	if b.String() != want {
		t.Fatalf("relations\n got %s\nwant %s", b.String(), want)
	}

	// The reverse: a day open relates what is dated on it.
	dated := surface{ID: "dated", Author: "brain", Data: json.RawMessage(`{"items":[{"at":"2026-11-13"}]}`)}
	if got := surfaceRelations(pool[5], append(pool, dated), threadOf); len(got) != 2 || got[0].ID != "dated" || got[1].ID != "rows" || got[0].Kind != relDayItem {
		t.Fatalf("a day's relations: %+v", got)
	}
	// A resource is a source too; nothing relates to what reads nothing alike.
	alike := surface{ID: "alike", Author: "brain", Sources: map[string]surfaceSource{"/y": {Resource: "notes", URI: "notes://today"}}}
	if got := surfaceRelations(pool[7], []surface{pool[7], alike, pool[8]}, nil); len(got) != 1 || got[0].ID != "alike" || got[0].Kind != relSource {
		t.Fatalf("a resource's relations: %+v", got)
	}
	if got := surfaceRelations(pool[8], pool, threadOf); len(got) != 0 {
		t.Fatalf("the brain's own surfaces relate by author: %+v", got)
	}
}

// focusCore is a core with three surfaces: what the user opens (its data
// from an external source), one reading the same, and one unrelated.
func focusCore(t *testing.T) *core {
	t.Helper()
	c, _ := surfaceCore(t)
	writeSurface(t, c, "comptes", `[{"id":"root","component":"Text","text":"x"}]`, "", true)
	writeSurface(t, c, "soldes", `[{"id":"root","component":"Text","text":"y"}]`, "", true)
	writeTestSurface(t, c, surfaceIn{ID: "agenda", Title: "Agenda", Weight: 60})
	return c
}

// Opening a surface lays the workspace out around it at once, by code: it
// large, what relates to it around (marked), the rest folded; back, the
// workspace in its order comes back as it was.
func TestFocusAndBack(t *testing.T) {
	c := focusCore(t)
	base := c.currentLayout()
	if base.Items[0].ID != "agenda" {
		t.Fatalf("base: %s", tilesOf(base))
	}
	now := time.Now()
	c.focusSurface("comptes", now)
	l := c.currentLayout()
	if l.Focus != "comptes" {
		t.Fatalf("focus %q", l.Focus)
	}
	f, _ := tileOf(l, "comptes")
	r, ok := tileOf(l, "soldes")
	if f.Size != sizeXL || !ok || !r.Related || f.Related {
		t.Fatalf("around the focus: %s (%+v)", tilesOf(l), l.Items)
	}
	if _, ok := tileOf(l, "agenda"); ok {
		t.Fatalf("unrelated, still a tile: %s", tilesOf(l))
	}

	// Back: the workspace at once, the same as before.
	c.focusSurface("", now)
	after := c.currentLayout()
	if tilesOf(after) != tilesOf(base) || after.Focus != "" {
		t.Fatalf("back:\n got %s\nwant %s", tilesOf(after), tilesOf(base))
	}
	for _, it := range after.Items {
		if it.Related {
			t.Fatalf("still marked related: %+v", it)
		}
	}

	// Past its time, the focus ends.
	c.focusSurface("agenda", now)
	if l := c.relayout(); l.Focus != "agenda" {
		t.Fatalf("focus %q", l.Focus)
	}
	c.layout.mu.Lock()
	c.layout.focusAt = now.Add(-focusFor - time.Minute)
	c.layout.mu.Unlock()
	if l := c.relayout(); l.Focus != "" || tilesOf(l) != tilesOf(base) {
		t.Fatalf("past its time: focus %q %s", l.Focus, tilesOf(l))
	}

	// Its surface removed: no longer open.
	c.focusSurface("soldes", now)
	if err := c.surfaceRemove("soldes", surfaceAuthorBrain); err != nil {
		t.Fatal(err)
	}
	if l := c.currentLayout(); l.Focus != "" {
		t.Fatalf("a removed surface is still open: %q", l.Focus)
	}
}

// Opening the one open again is the same focus.
func TestFocusOpenedAgain(t *testing.T) {
	c := focusCore(t)
	now := time.Now()
	c.focusSurface("agenda", now)
	c.focusSurface("comptes", now.Add(200*time.Millisecond))
	c.layout.mu.Lock()
	since := c.layout.focusSince
	c.layout.mu.Unlock()
	c.focusSurface("comptes", now.Add(2*time.Second))
	c.layout.mu.Lock()
	again := c.layout.focusSince
	c.layout.mu.Unlock()
	if !again.Equal(since) {
		t.Fatal("opened again: a new focus")
	}
	if l := c.currentLayout(); l.Focus != "comptes" {
		t.Fatalf("opened again: focus %q", l.Focus)
	}
}

// Opening a surface sends nothing to the brain (#550) and asks no model:
// not on opening, not on going back and forth between surfaces, not on the
// one it shows external content of.
func TestFocusAsksNothing(t *testing.T) {
	c := focusCore(t)
	before := c.store.lastMessageSeq()
	now := time.Now()
	for i, id := range []string{"comptes", "agenda", "comptes", "", "agenda", "comptes"} {
		at := now.Add(time.Duration(i) * time.Second)
		c.focusSurface(id, at)
		if l := c.currentLayout(); l.Focus != id {
			t.Fatalf("opened %q, focus %q", id, l.Focus)
		}
	}
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 {
		t.Fatalf("the brain is asked: %+v", msgs)
	}
	if n := c.store.lastMessageSeq(); n != before {
		t.Fatalf("an opening wrote a message: %d after %d", n, before)
	}
	if raw, _ := json.Marshal(c.currentLayout()); strings.Contains(string(raw), `"say"`) || strings.Contains(string(raw), `"eriphe"`) {
		t.Fatalf("the layout still carries a line: %s", raw)
	}
}

// With nothing related, code places the rest in its order around the
// focus, rather than folding everything.
func TestFocusAlone(t *testing.T) {
	c, _ := surfaceCore(t)
	writeTestSurface(t, c, surfaceIn{ID: "seule", Title: "Seule", Weight: 30})
	writeTestSurface(t, c, surfaceIn{ID: "agenda", Title: "Agenda", Weight: 60})
	c.focusSurface("seule", time.Now())
	l := c.currentLayout()
	if f, _ := tileOf(l, "seule"); l.Focus != "seule" || f.Size != sizeXL {
		t.Fatalf("focus: %s", tilesOf(l))
	}
	if _, ok := tileOf(l, "agenda"); !ok {
		t.Fatalf("alone, the rest is folded: %s", tilesOf(l))
	}
}
