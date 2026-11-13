package main

import (
	"cmp"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

// The workspace's layout (#422): where each surface (surfaces.go) is on the
// page, owned by eriphe-core. The page is a fixed grid of 12 × 8 cells that
// never scrolls; a surface is a tile of one of four sizes (s 3×1, m 3×2,
// l 4×3, xl 6×5, one xl at most), or a chip in the strip above the grid
// when it is folded.
//
// The composer (composer.go) chooses from the pool of surfaces what is shown
// now and how big (a workspaceChoice). Code lays that choice out (layoutOf)
// at once, never waiting for a model, and lays out again whenever the pool
// changes (relayout): the urgent ones first (weight 80 and up: the queue's
// 4 and 5, what a task waits on or what interrupts), then a surface the
// user opened, then the composer's choice in its order, then the
// surfaces it has not seen yet, by weight. Each is put at the first place
// where it fits, top left first; one that fits nowhere shrinks a size, then
// is folded. What the choice left out is folded too: nothing in the pool
// goes unseen while the chip strip has room.
//
// The layout goes to the page as the event stream's "layout" event (JSON,
// workspaceLayout), sent whenever it changes, after the "a2ui" event of the
// surfaces it names. The last one is kept in kv (layoutKey).

const (
	layoutCols     = 12
	layoutRows     = 8
	layoutChipsMax = 12
	layoutWhyMax   = 60
	layoutKey      = "workspace_layout"
	choiceKey      = "workspace_choice"

	weightUrgent = 80 // a surface this heavy is placed first by code
	weightGrow   = 40 // a surface this heavy grows a size when the grid has room
	focusFor     = 15 * time.Minute

	sizeS  = "s"
	sizeM  = "m"
	sizeL  = "l"
	sizeXL = "xl"
)

// tileDims are each size's cells: width, height.
var tileDims = map[string][2]int{sizeXL: {6, 5}, sizeL: {4, 3}, sizeM: {3, 2}, sizeS: {3, 1}}

var (
	smallerSize = map[string]string{sizeXL: sizeL, sizeL: sizeM, sizeM: sizeS}
	biggerSize  = map[string]string{sizeS: sizeM, sizeM: sizeL, sizeL: sizeXL}
	sizeRank    = map[string]int{sizeS: 1, sizeM: 2, sizeL: 3, sizeXL: 4}
)

// sizeOfWeight is a surface's size by its weight (1 to 100).
func sizeOfWeight(w int) string {
	switch {
	case w >= 90:
		return sizeXL
	case w >= 70:
		return sizeL
	case w >= 40:
		return sizeM
	}
	return sizeS
}

// queueSurfaceWeight is a surface's weight for an attention item's (1 to
// 5): 5 is 100, 4 is 80 (urgent), 3 is 60, 2 is 40, 1 is 20.
func queueSurfaceWeight(w int) int { return min(max(w, 1), 5) * 20 }

// workspaceChoice is what the composer chose (or code, by weight): the
// surfaces shown now, in order, with their size and why; Seen, the pool
// it chose from, so a surface written since is placed by weight at once.
type workspaceChoice struct {
	Eriphe string       `json:"eriphe"`
	Items  []choiceItem `json:"items"`
	Seen   []string     `json:"seen"`
	By     string       `json:"by"` // composer or fallback
	Moment string       `json:"moment,omitempty"`
	At     time.Time    `json:"at"`
}

type choiceItem struct {
	ID   string `json:"id"`
	Size string `json:"size"`
	Why  string `json:"why,omitempty"`
}

// workspaceLayout is what the page lays out: the event stream's "layout".
type workspaceLayout struct {
	Eriphe  string       `json:"eriphe"`            // Eriphe's line, "" when the composer did not write one
	By      string       `json:"by"`                // composer or fallback
	Ambient string       `json:"ambient"`           // calm, decision or urgent: by the heaviest tile
	Focus   string       `json:"focus,omitempty"`   // the surface the user opened
	Items   []layoutTile `json:"items"`             // the tiles, each at its place on the grid
	Chips   []layoutChip `json:"chips"`             // the folded surfaces, in order
	Moment  string       `json:"moment,omitempty"`  // the choice's moment
	At      time.Time    `json:"at,omitzero"`       // when the choice was made
	Missing []string     `json:"missing,omitempty"` // what the composer said is missing (surface.missing)
}

// layoutTile is one surface on the grid: col and row from 0, w and h in
// cells (the size's, or wider over columns left empty on its rows).
type layoutTile struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Size   string `json:"size"`
	Col    int    `json:"col"`
	Row    int    `json:"row"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	Why    string `json:"why,omitempty"`
	Pinned bool   `json:"pinned,omitempty"` // placed by the user, or by the brain at their request, this session
}

// layoutChip is a folded surface.
type layoutChip struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Why    string `json:"why,omitempty"`
	Closed bool   `json:"closed,omitempty"` // the user closed it this session
}

// layoutWant is a surface asked on the grid, at a size.
type layoutWant struct {
	s     surface
	size  string
	why   string
	focus bool
	pin   *tilePin
}

// layoutOf lays a choice out over the pool, with the surface the user
// opened ("" for none). It is pure: no model, no store.
func layoutOf(pool []surface, ch workspaceChoice, focus string) workspaceLayout {
	return layoutWith(pool, ch, focus, userPlacement{})
}

// layoutWith lays a choice out with the user's placement of the session
// (workspace_gestures.go): what they closed is folded last, marked closed,
// and never opened; what they placed (or the brain placed at their
// request) comes first, at its place when it has one, at its size, and
// keeps both: no growing, no widening. Then the order of layoutOf.
func layoutWith(pool []surface, ch workspaceChoice, focus string, pl userPlacement) workspaceLayout {
	var closed []surface
	pool = slices.DeleteFunc(slices.Clone(pool), func(s surface) bool {
		if _, ok := pl.Closed[s.ID]; ok {
			closed = append(closed, s)
			return true
		}
		return false
	})
	if _, ok := pl.Closed[focus]; ok {
		focus = ""
	}
	slices.SortStableFunc(pool, func(a, b surface) int {
		return cmp.Or(cmp.Compare(b.Weight, a.Weight), b.Updated.Compare(a.Updated), cmp.Compare(a.ID, b.ID))
	})
	byID := map[string]surface{}
	for _, s := range pool {
		byID[s.ID] = s
	}
	chosen := map[string]choiceItem{}
	for _, it := range ch.Items {
		if _, ok := byID[it.ID]; ok {
			if _, dup := chosen[it.ID]; !dup {
				chosen[it.ID] = it
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ch.Seen {
		seen[id] = true
	}
	l := workspaceLayout{Eriphe: ch.Eriphe, By: ch.By, Moment: ch.Moment, At: ch.At, Items: []layoutTile{}, Chips: []layoutChip{}}

	var wants []layoutWant
	added := map[string]bool{}
	add := func(s surface, focus bool) {
		if added[s.ID] {
			return
		}
		added[s.ID] = true
		w := layoutWant{s: s, size: sizeOfWeight(s.Weight), why: s.Why, focus: focus}
		if it, ok := chosen[s.ID]; ok {
			if _, known := tileDims[it.Size]; known {
				w.size = it.Size
			}
			w.why = cmp.Or(it.Why, w.why)
		}
		if p, ok := pl.Pins[s.ID]; ok {
			w.pin = &p
			if _, known := tileDims[p.Size]; known {
				w.size = p.Size
			}
		} else if focus && sizeRank[w.size] < sizeRank[sizeL] {
			w.size = sizeL
		}
		w.why = truncate(oneLine(w.why), layoutWhyMax)
		wants = append(wants, w)
	}
	for _, id := range pl.order() {
		if s, ok := byID[id]; ok {
			add(s, id == focus)
		}
	}
	for _, s := range pool {
		if s.Weight >= weightUrgent && s.ID != focus {
			add(s, false)
		}
	}
	if s, ok := byID[focus]; ok {
		add(s, true)
		l.Focus = focus
	}
	for _, it := range ch.Items {
		if s, ok := byID[it.ID]; ok {
			add(s, false)
		}
	}
	for _, s := range pool {
		if !seen[s.ID] {
			add(s, false)
		}
	}

	// One xl at most: the first (one the user placed comes first).
	xl := false
	for i := range wants {
		if wants[i].size == sizeXL {
			if xl {
				wants[i].size = sizeL
			}
			xl = true
		}
	}
	sizes := make([]string, len(wants))
	at := make([]*[2]int, len(wants))
	for i, w := range wants {
		sizes[i] = w.size
		if w.pin != nil && w.pin.Placed {
			at[i] = &[2]int{w.pin.Col, w.pin.Row}
		}
	}
	spots := packTilesAt(sizes, at)
	// Room left: what matters grows a size, the first first, never into xl
	// (xl is for what is urgent or asked so), and only if nothing else
	// shrinks or folds for it. What the user placed keeps its size.
	for i, w := range wants {
		if spots[i] == nil || w.s.Weight < weightGrow || w.pin != nil {
			continue
		}
		up := biggerSize[spots[i].size]
		if up == "" || up == sizeXL {
			continue
		}
		trial := make([]string, len(wants))
		for j := range wants {
			trial[j] = sizes[j]
			if spots[j] != nil {
				trial[j] = spots[j].size
			}
		}
		trial[i] = up
		if next := packTilesAt(trial, at); keeps(spots, next, trial) {
			spots = next
		}
	}

	var folded []layoutWant
	for i, w := range wants {
		s := spots[i]
		if s == nil {
			folded = append(folded, w)
			continue
		}
		d := tileDims[s.size]
		l.Items = append(l.Items, layoutTile{ID: w.s.ID, Title: w.s.Title, Size: s.size, Col: s.col, Row: s.row, W: d[0], H: d[1], Why: w.why, Pinned: w.pin != nil})
	}
	widenTiles(l.Items)
	if l.Focus != "" && !slices.ContainsFunc(l.Items, func(t layoutTile) bool { return t.ID == l.Focus }) {
		l.Focus = "" // folded: what the user placed fills the grid
	}
	for _, w := range folded {
		l.Chips = append(l.Chips, layoutChip{ID: w.s.ID, Title: w.s.Title, Why: w.why})
	}
	for _, s := range pool {
		if !added[s.ID] {
			l.Chips = append(l.Chips, layoutChip{ID: s.ID, Title: s.Title, Why: truncate(oneLine(s.Why), layoutWhyMax)})
		}
	}
	for _, s := range closed {
		l.Chips = append(l.Chips, layoutChip{ID: s.ID, Title: s.Title, Closed: true})
	}
	if len(l.Chips) > layoutChipsMax {
		l.Chips = l.Chips[:layoutChipsMax]
	}
	l.Ambient = "calm"
	for _, t := range l.Items {
		switch w := byID[t.ID].Weight; {
		case w >= 90:
			l.Ambient = "urgent"
		case w >= weightUrgent && l.Ambient == "calm":
			l.Ambient = "decision"
		}
	}
	return l
}

// keeps tells whether next places every tile spots placed, each at the
// size asked in trial, and folds the same.
func keeps(spots, next []*tileSpot, trial []string) bool {
	for j := range next {
		if (spots[j] == nil) != (next[j] == nil) || (next[j] != nil && next[j].size != trial[j]) {
			return false
		}
	}
	return true
}

type tileSpot struct {
	size     string
	col, row int
}

// packTiles puts each tile, in order, at the first place where it fits, top
// left first; one that fits nowhere shrinks, then is left out (nil).
func packTiles(sizes []string) []*tileSpot { return packTilesAt(sizes, nil) }

// packTilesAt is packTiles with places asked: a tile with one (at[i]: col,
// row) goes there first, at its size, when it is on the grid and free;
// else it is packed with the others, in order.
func packTilesAt(sizes []string, at []*[2]int) []*tileSpot {
	var grid [layoutRows][layoutCols]bool
	free := func(col, row, w, h int) bool {
		if col < 0 || row < 0 || col+w > layoutCols || row+h > layoutRows {
			return false
		}
		for r := row; r < row+h; r++ {
			for c := col; c < col+w; c++ {
				if grid[r][c] {
					return false
				}
			}
		}
		return true
	}
	take := func(col, row, w, h int) {
		for r := row; r < row+h; r++ {
			for c := col; c < col+w; c++ {
				grid[r][c] = true
			}
		}
	}
	out := make([]*tileSpot, len(sizes))
	for i, want := range sizes {
		if i >= len(at) || at[i] == nil {
			continue
		}
		if d, ok := tileDims[want]; ok && free(at[i][0], at[i][1], d[0], d[1]) {
			take(at[i][0], at[i][1], d[0], d[1])
			out[i] = &tileSpot{size: want, col: at[i][0], row: at[i][1]}
		}
	}
	for i, want := range sizes {
		if out[i] != nil {
			continue
		}
	sizes:
		for size := want; size != ""; size = smallerSize[size] {
			d := tileDims[size]
			for row := 0; row+d[1] <= layoutRows; row++ {
				for col := 0; col+d[0] <= layoutCols; col++ {
					if !free(col, row, d[0], d[1]) {
						continue
					}
					take(col, row, d[0], d[1])
					out[i] = &tileSpot{size: size, col: col, row: row}
					break sizes
				}
			}
		}
	}
	return out
}

// widenTiles stretches tiles to the right over columns nobody uses on their
// rows, so the sizes' widths leave no empty strip at the edge; a tile the
// user placed keeps its size.
func widenTiles(tiles []layoutTile) {
	used := func(col, row, h, self int) bool {
		for i, t := range tiles {
			if i != self && col >= t.Col && col < t.Col+t.W && row < t.Row+t.H && t.Row < row+h {
				return true
			}
		}
		return false
	}
	for i := range tiles {
		for !tiles[i].Pinned && tiles[i].Col+tiles[i].W < layoutCols && !used(tiles[i].Col+tiles[i].W, tiles[i].Row, tiles[i].H, i) {
			tiles[i].W++
		}
	}
}

// fallbackChoice is the choice by weight: nothing seen, so every surface is
// laid out by its weight, the heaviest first.
func fallbackChoice(moment string, now time.Time) workspaceChoice {
	return workspaceChoice{By: fallbackBy, Moment: moment, At: now, Items: []choiceItem{}}
}

// layoutState is what eriphe-core keeps of the layout: the choice it lays
// out, the surface opened, the last layout and who waits for the next.
type layoutState struct {
	mu      sync.Mutex
	choice  *workspaceChoice
	loaded  bool
	focus   string
	focusAt time.Time
	last    workspaceLayout
	raw     string
	changed chan struct{} // closed when the layout changes
	kick    chan struct{} // the queue changed: its surfaces are made again (workspace_queue.go)
	syncMu  sync.Mutex    // one sync of the queue's surfaces at a time

	missingSent map[string]time.Time // surface.missing told, by text
	staleSent   map[string]time.Time // surface.stale told, by id: the surface's version it was told for

	user       userPlacement   // what the user closed and placed this session (workspace_gestures.go)
	userLoaded bool            // user was read from the store
	read       map[string]bool // queue surfaces the user opened: their item ends once they leave it
}

// workspaceChoiceNow is the choice laid out: the last kept, or by weight.
func (c *core) workspaceChoiceNow() workspaceChoice {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	return c.choiceLocked()
}

func (c *core) choiceLocked() workspaceChoice {
	st := &c.layout
	if !st.loaded {
		st.loaded = true
		var ch workspaceChoice
		if raw := c.store.get(choiceKey); raw != "" && json.Unmarshal([]byte(raw), &ch) == nil {
			st.choice = &ch
		}
	}
	if st.choice == nil {
		return fallbackChoice(momentStart, time.Time{})
	}
	return *st.choice
}

// lastChoice is the last choice kept, nil before the first.
func (c *core) lastChoice() *workspaceChoice {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	c.choiceLocked()
	if st.choice == nil {
		return nil
	}
	ch := *st.choice
	return &ch
}

// keepChoice keeps a choice, in memory and in the store, and lays it out.
func (c *core) keepChoice(ch workspaceChoice) workspaceLayout {
	st := &c.layout
	st.mu.Lock()
	st.choice, st.loaded = &ch, true
	st.mu.Unlock()
	if raw, err := json.Marshal(ch); err == nil {
		c.store.set(choiceKey, string(raw))
	}
	return c.relayout()
}

// focusSurface is the surface the user opened: code places it first, for
// focusFor or until another is opened.
func (c *core) focusSurface(id string, now time.Time) {
	st := &c.layout
	st.mu.Lock()
	st.focus, st.focusAt = id, now
	st.mu.Unlock()
	if id != "" {
		c.openedOnWorkspace(id, now) // no longer closed; a queue item read (workspace_gestures.go)
	}
	c.relayout()
}

// layoutFocused tells whether the last layout has a surface opened.
func (c *core) layoutFocused() bool {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.last.Focus != ""
}

// workspaceFocus is the surface opened, "" when none is.
func (c *core) workspaceFocus(now time.Time) string {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.focus != "" && now.Sub(st.focusAt) > focusFor {
		st.focus = ""
	}
	return st.focus
}

// relayout lays the pool out again, with the choice kept: at once, no
// model. Those waiting for the layout are told when it changed.
func (c *core) relayout() workspaceLayout {
	now := time.Now()
	focus := c.workspaceFocus(now)
	pool, err := c.surfaces()
	start := c.workspaceSessionStart(now)
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		return st.last
	}
	l := layoutWith(pool, c.choiceLocked(), focus, c.placementLocked(start))
	raw, _ := json.Marshal(l)
	if string(raw) != st.raw {
		st.last, st.raw = l, string(raw)
		if st.changed != nil {
			close(st.changed)
		}
		st.changed = make(chan struct{})
		c.store.set(layoutKey, string(raw))
		c.noteFront(l, pool, time.Now()) // what is in front, for the measure of use (workspace_use.go)
	}
	return l
}

// currentLayout is the layout as it is now.
func (c *core) currentLayout() workspaceLayout {
	l, _ := c.watchLayout()
	return l
}

// watchLayout is the layout now, and a channel closed when it changes.
func (c *core) watchLayout() (workspaceLayout, <-chan struct{}) {
	st := &c.layout
	st.mu.Lock()
	empty := st.raw == ""
	st.mu.Unlock()
	if empty {
		c.relayout()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.changed == nil {
		st.changed = make(chan struct{})
	}
	return st.last, st.changed
}
