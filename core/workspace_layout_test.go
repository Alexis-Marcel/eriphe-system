package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func poolOf(weights ...int) []surface {
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	var out []surface
	for i, w := range weights {
		at := base.Add(time.Duration(i) * time.Minute)
		out = append(out, surface{ID: fmt.Sprintf("s-%d", i+1), Title: fmt.Sprintf("Surface %d", i+1), Author: "brain", Weight: w,
			Why: "why " + fmt.Sprint(i+1), Created: at, Updated: at})
	}
	return out
}

func tilesOf(l workspaceLayout) string {
	var out []string
	for _, t := range l.Items {
		out = append(out, fmt.Sprintf("%s/%s@%d,%d", t.ID, t.Size, t.Col, t.Row))
	}
	return strings.Join(out, " ")
}

func chipsOf(l workspaceLayout) []string {
	var out []string
	for _, c := range l.Chips {
		out = append(out, c.ID)
	}
	return out
}

// overlaps says whether two tiles of a layout share a cell, or one leaves
// the grid: the page never scrolls.
func overlaps(l workspaceLayout) string {
	var grid [layoutRows][layoutCols]string
	for _, t := range l.Items {
		if t.Col < 0 || t.Row < 0 || t.Col+t.W > layoutCols || t.Row+t.H > layoutRows {
			return t.ID + " leaves the grid"
		}
		for r := t.Row; r < t.Row+t.H; r++ {
			for c := t.Col; c < t.Col+t.W; c++ {
				if grid[r][c] != "" {
					return t.ID + " over " + grid[r][c]
				}
				grid[r][c] = t.ID
			}
		}
	}
	return ""
}

// Without any model, code lays the pool out by weight: the heaviest first
// and biggest, one xl at most, on the grid without overlap.
func TestLayoutByWeight(t *testing.T) {
	pool := poolOf(30, 100, 60, 95, 80)
	l := layoutOf(pool, "")
	if bad := overlaps(l); bad != "" {
		t.Fatal(bad)
	}
	got := tilesOf(l)
	if !strings.HasPrefix(got, "s-2/xl@0,0 s-4/l@6,0 s-5/l") || len(l.Items) != 5 || len(l.Chips) != 0 {
		t.Fatalf("tiles %s, chips %v", got, chipsOf(l))
	}
	if l.Ambient != "urgent" {
		t.Errorf("ambient %s", l.Ambient)
	}
	// Nothing heavy: calm. One surface fills its rows.
	one := layoutOf(poolOf(50), "")
	if len(one.Items) != 1 || one.Items[0].W != layoutCols || one.Ambient != "calm" {
		t.Fatalf("one %+v", one)
	}
	// None: an empty layout, never nil lists.
	empty := layoutOf(nil, "")
	raw, _ := json.Marshal(empty)
	if !strings.Contains(string(raw), `"items":[]`) || !strings.Contains(string(raw), `"chips":[]`) {
		t.Fatalf("empty %s", raw)
	}
}

// Code orders the workspace by each tile's state, no model: what waits
// for the user, then what is urgent, then what is new, then what runs,
// what is quiet and what ended; then the heaviest, the latest changed and
// the id, so the same pool always gives the same layout.
func TestLayoutOrderByState(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	tile := func(id, state string, weight int) surface {
		return surface{ID: id, Title: id, Author: "brain", Weight: weight, Updated: at, Look: surfaceLook{State: state}}
	}
	pool := []surface{
		tile("done", tileDone, 30),
		tile("quiet", tileQuiet, 50),
		tile("running", tileRunning, 50),
		tile("new", tileNew, 50),
		tile("urgent", tileUrgent, 80),
		tile("yours", tileYours, 70),
		tile("plain", "", 85), // no state written: by its weight, urgent
	}
	want := []string{"yours", "plain", "urgent", "new", "running", "quiet", "done"}
	order := func(l workspaceLayout) []string {
		var out []string
		for _, t := range l.Items {
			out = append(out, t.ID)
		}
		for _, c := range l.Chips {
			out = append(out, c.ID)
		}
		return out
	}
	first := layoutOf(pool, "")
	if got := order(first); !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	// The same inputs in another order: the same layout.
	for range 5 {
		shuffled := slices.Clone(pool)
		slices.Reverse(shuffled)
		shuffled[0], shuffled[3] = shuffled[3], shuffled[0]
		if got := layoutOf(shuffled, ""); tilesOf(got) != tilesOf(first) || !slices.Equal(chipsOf(got), chipsOf(first)) {
			t.Fatalf("not deterministic: %s / %s", tilesOf(got), tilesOf(first))
		}
	}
	// Opened, a tile goes on stage; the heaviest of what waits stays in front of it.
	l := layoutOf(pool, "quiet")
	if l.Items[0].ID != "plain" || l.Items[1].ID != "urgent" || l.Items[2].ID != "quiet" || l.Items[2].Size != sizeXL {
		t.Fatalf("focus: %s", tilesOf(l))
	}
	if !l.Items[0].Urgent || l.Items[2].Urgent {
		t.Fatalf("urgent flags: %+v", l.Items[:3])
	}
	// The page's metadata says the same state.
	if tileStateOf(pool[6]) != tileUrgent || tileStateOf(surface{Weight: 40}) != tileQuiet {
		t.Fatal("state by weight")
	}
}

// When the grid is full, what does not fit shrinks, then folds into the
// chip strip, the order kept; the strip has a cap.
func TestLayoutFoldWhenFull(t *testing.T) {
	var weights []int
	for range 30 {
		weights = append(weights, 75)
	}
	l := layoutOf(poolOf(weights...), "")
	if bad := overlaps(l); bad != "" {
		t.Fatal(bad)
	}
	if len(l.Items) == 0 || len(l.Items)+len(l.Chips) != min(30, len(l.Items)+layoutChipsMax) {
		t.Fatalf("%d tiles, %d chips", len(l.Items), len(l.Chips))
	}
	sizes := map[string]int{}
	for _, t := range l.Items {
		sizes[t.Size]++
	}
	if sizes[sizeL] == 0 || sizes[sizeL] == len(l.Items) {
		t.Errorf("nothing shrank: %v", sizes)
	}
	// The first folded comes right after the last placed, by weight then time.
	last := l.Items[len(l.Items)-1].ID
	if l.Chips[0].ID == last || l.Chips[0].Title == "" {
		t.Errorf("chips %+v", l.Chips[:2])
	}
}

// What the user opened is placed first after the urgent, xl (l when an
// urgent one holds the xl), for focusFor or until they go back.
func TestLayoutFocus(t *testing.T) {
	pool := poolOf(100, 20, 50)
	l := layoutOf(pool, "s-2")
	if l.Focus != "s-2" || l.Items[0].ID != "s-1" || l.Items[1].ID != "s-2" || l.Items[1].Size != sizeL {
		t.Fatalf("focus: %s", tilesOf(l))
	}
	// The page lays an open surface out at once as code does: it knows what is urgent.
	if !l.Items[0].Urgent || l.Items[1].Urgent {
		t.Fatalf("urgent: %+v", l.Items[:2])
	}
	// Opened, an urgent surface stays where it is: first.
	if l := layoutOf(pool, "s-1"); l.Items[0].ID != "s-1" || l.Items[0].Size != sizeXL {
		t.Fatalf("urgent focus: %s", tilesOf(l))
	}
	if l := layoutOf(pool, "gone"); l.Focus != "" || l.Items[0].ID != "s-1" {
		t.Fatalf("a focus not in the pool: %s", tilesOf(l))
	}

	c, _ := surfaceCore(t)
	for _, id := range []string{"first", "second"} {
		if _, errs, err := c.fixtureSurface("brain", surfaceIn{ID: id, Title: id, Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}); err != nil || len(errs) > 0 {
			t.Fatal(err, errs)
		}
	}
	now := time.Now()
	c.focusSurface("second", now)
	if l := c.currentLayout(); l.Focus != "second" || l.Items[0].ID != "second" {
		t.Fatalf("focused: %s", tilesOf(l))
	}
	if c.workspaceFocus(now.Add(focusFor+time.Second)) != "" {
		t.Fatal("focus kept past its time")
	}
}

// relayout keeps the layout, tells those who wait when it changed and only
// then, and stores it.
func TestRelayout(t *testing.T) {
	c, _ := surfaceCore(t)
	l, changed := c.watchLayout()
	if len(l.Items) != 0 {
		t.Fatalf("layout %+v", l)
	}
	if _, errs, err := c.fixtureSurface("brain", surfaceIn{ID: "one", Title: "One", Weight: weightUrgent - 1,
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}); err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	select {
	case <-changed:
	default:
		t.Fatal("not told of the new surface")
	}
	l, changed = c.watchLayout()
	if tilesOf(l) != "one/l@0,0" {
		t.Fatalf("layout %s", tilesOf(l))
	}
	c.relayout()
	select {
	case <-changed:
		t.Fatal("told with nothing changed")
	default:
	}
	var stored workspaceLayout
	if json.Unmarshal([]byte(c.store.get(layoutKey)), &stored) != nil || tilesOf(stored) != "one/l@0,0" {
		t.Fatalf("stored %q", c.store.get(layoutKey))
	}
	if err := c.surfaceRemove("one", surfaceByLifetime); err != nil {
		t.Fatal(err)
	}
	if l := c.currentLayout(); len(l.Items) != 0 {
		t.Fatalf("after removal %s", tilesOf(l))
	}
}

// layoutOf lays the pool out, with the surface the user opened ("" for
// none), and no placement of the user's.
func layoutOf(pool []surface, focus string) workspaceLayout {
	return layoutStage(pool, focus, "", nil, userPlacement{})
}
