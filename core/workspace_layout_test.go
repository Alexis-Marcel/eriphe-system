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
	l := layoutOf(pool, fallbackChoice(momentChange, time.Now()), "")
	if bad := overlaps(l); bad != "" {
		t.Fatal(bad)
	}
	got := tilesOf(l)
	if !strings.HasPrefix(got, "s-2/xl@0,0 s-4/l@6,0 s-5/l") || len(l.Items) != 5 || len(l.Chips) != 0 {
		t.Fatalf("tiles %s, chips %v", got, chipsOf(l))
	}
	if l.Ambient != "urgent" || l.By != fallbackBy {
		t.Errorf("ambient %s, by %s", l.Ambient, l.By)
	}
	// Nothing heavy: calm. One surface fills its rows.
	one := layoutOf(poolOf(50), fallbackChoice(momentChange, time.Now()), "")
	if len(one.Items) != 1 || one.Items[0].W != layoutCols || one.Ambient != "calm" {
		t.Fatalf("one %+v", one)
	}
	// None: an empty layout, never nil lists.
	empty := layoutOf(nil, fallbackChoice(momentChange, time.Now()), "")
	raw, _ := json.Marshal(empty)
	if !strings.Contains(string(raw), `"items":[]`) || !strings.Contains(string(raw), `"chips":[]`) {
		t.Fatalf("empty %s", raw)
	}
}

// Urgent first: a surface of weight 80 or more is placed ahead of the
// composer's choice, at once, even one the composer left out; a surface
// written after the choice is placed by its weight; what the choice left
// out is folded.
func TestLayoutUrgentFirst(t *testing.T) {
	pool := poolOf(50, 50, 50)
	ch := workspaceChoice{By: composedBy, Seen: []string{"s-1", "s-2", "s-3"},
		Items: []choiceItem{{ID: "s-2", Size: sizeXL, Why: "the composer's why"}, {ID: "s-1", Size: sizeM}, {ID: "nope", Size: sizeL}}}
	l := layoutOf(pool, ch, "")
	if got := tilesOf(l); got != "s-2/xl@0,0 s-1/l@6,0" || !slices.Equal(chipsOf(l), []string{"s-3"}) || l.Items[0].Why != "the composer's why" {
		t.Fatalf("composer's choice: %s, chips %v, %+v", got, chipsOf(l), l.Items)
	}
	// An urgent surface arrives, unseen by the composer: in front at once.
	pool = append(pool, surface{ID: "q-card-9", Title: "Paiement", Author: surfaceAuthorCore, Weight: queueSurfaceWeight(5), Why: "Attend ton accord"})
	// And a plain new one: placed by weight after the choice.
	pool = append(pool, surface{ID: "s-new", Title: "Nouvelle", Author: "brain", Weight: 40})
	start := time.Now()
	l = layoutOf(pool, ch, "")
	if d := time.Since(start); d > 250*time.Millisecond { // well under 10 ms; a loaded runner is slower
		t.Errorf("layout took %s", d)
	}
	if l.Items[0].ID != "q-card-9" || l.Items[0].Size != sizeXL || l.Items[0].Col != 0 || l.Items[0].Row != 0 {
		t.Fatalf("urgent not first: %s", tilesOf(l))
	}
	// One xl at most: the composer's xl is an l now.
	if i := slices.IndexFunc(l.Items, func(t layoutTile) bool { return t.ID == "s-2" }); i < 0 || l.Items[i].Size != sizeL {
		t.Fatalf("composer's xl: %s", tilesOf(l))
	}
	if !slices.ContainsFunc(l.Items, func(t layoutTile) bool { return t.ID == "s-new" }) || slices.Contains(chipsOf(l), "s-new") {
		t.Fatalf("new surface: %s, chips %v", tilesOf(l), chipsOf(l))
	}
	if !slices.Equal(chipsOf(l), []string{"s-3"}) || l.Ambient != "urgent" {
		t.Fatalf("chips %v, ambient %s", chipsOf(l), l.Ambient)
	}
	if bad := overlaps(l); bad != "" {
		t.Fatal(bad)
	}
}

// When the grid is full, what does not fit shrinks, then folds into the
// chip strip, the order kept; the strip has a cap.
func TestLayoutFoldWhenFull(t *testing.T) {
	var weights []int
	for range 30 {
		weights = append(weights, 75)
	}
	l := layoutOf(poolOf(weights...), fallbackChoice(momentChange, time.Now()), "")
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

// What the user opened is placed first after the urgent, at l at least,
// for focusFor or until they go back.
func TestLayoutFocus(t *testing.T) {
	pool := poolOf(100, 20, 50)
	l := layoutOf(pool, fallbackChoice(momentChange, time.Now()), "s-2")
	if l.Focus != "s-2" || l.Items[0].ID != "s-1" || l.Items[1].ID != "s-2" || l.Items[1].Size != sizeL {
		t.Fatalf("focus: %s", tilesOf(l))
	}
	// The page lays an open surface out at once as code does: it knows what is urgent.
	if !l.Items[0].Urgent || l.Items[1].Urgent {
		t.Fatalf("urgent: %+v", l.Items[:2])
	}
	// Opened, an urgent surface stays where it is: first.
	if l := layoutOf(pool, fallbackChoice(momentChange, time.Now()), "s-1"); l.Items[0].ID != "s-1" || l.Items[0].Size != sizeXL {
		t.Fatalf("urgent focus: %s", tilesOf(l))
	}
	if l := layoutOf(pool, fallbackChoice(momentChange, time.Now()), "gone"); l.Focus != "" || l.Items[0].ID != "s-1" {
		t.Fatalf("a focus not in the pool: %s", tilesOf(l))
	}

	c, _ := surfaceCore(t)
	for _, id := range []string{"first", "second"} {
		if _, errs, err := c.surfaceWrite("brain", surfaceIn{ID: id, Title: id, Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}); err != nil || len(errs) > 0 {
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
	if _, errs, err := c.surfaceWrite("brain", surfaceIn{ID: "one", Title: "One", Weight: 90,
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}); err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	select {
	case <-changed:
	default:
		t.Fatal("not told of the new surface")
	}
	l, changed = c.watchLayout()
	if tilesOf(l) != "one/xl@0,0" {
		t.Fatalf("layout %s", tilesOf(l))
	}
	c.relayout()
	select {
	case <-changed:
		t.Fatal("told with nothing changed")
	default:
	}
	var stored workspaceLayout
	if json.Unmarshal([]byte(c.store.get(layoutKey)), &stored) != nil || tilesOf(stored) != "one/xl@0,0" {
		t.Fatalf("stored %q", c.store.get(layoutKey))
	}
	if err := c.surfaceRemove("one", surfaceByUser); err != nil {
		t.Fatal(err)
	}
	if l := c.currentLayout(); len(l.Items) != 0 {
		t.Fatalf("after removal %s", tilesOf(l))
	}
}
