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
// Code orders the workspace, no model (#574), and lays it out again
// whenever the pool changes (relayout). Each surface has a state code
// computed (tileStateOf: yours, urgent, new, running, quiet, done) and a
// weight; the order is what the user placed first, then the heaviest of
// what waits (weight 80 and up: the queue's 4 and 5, what a task waits on
// or what interrupts), then a surface opened (large, with what relates to
// it around, the rest folded), then the others by state (yours, urgent,
// new, running, quiet, done), weight, last change and id: the same pool
// gives the same layout. A standing thread with nothing for the user
// (quiet or running, foldsQuiet) is not one of those others: it is a chip,
// a tile again once yours, urgent or new. Each is put at the first place where it fits, top
// left first; one that fits nowhere shrinks a size, then is folded.
//
// A surface that opens inside another (Look.Inside: a child thread's, its
// parent's) is neither a tile nor a chip while that other is on the
// workspace: opened, the other is on stage with it inside (onStage).
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

// A tile's state (#574), computed by code, never by a model: what the
// page colours and badges, and the first key of the workspace's order.
const (
	tileYours   = "yours"   // something waits for the user: a card, a step of theirs
	tileUrgent  = "urgent"  // weight 80 and up, or a note due within dueSoon
	tileNew     = "new"     // something changed since the user last opened it
	tileRunning = "running" // a step runs
	tileQuiet   = "quiet"   // nothing to do
	tileDone    = "done"    // its thread ended

	dueSoon = 2 * time.Hour // a note due this close (or past) makes its thread urgent
)

var tileRank = map[string]int{tileYours: 0, tileUrgent: 1, tileNew: 2, tileRunning: 3, tileQuiet: 4, tileDone: 5}

// tileStateOf is a surface's state: the one code wrote with it (a
// thread's, a queue's), else by its weight.
func tileStateOf(s surface) string {
	if _, ok := tileRank[s.Look.State]; ok {
		return s.Look.State
	}
	if s.Weight >= weightUrgent {
		return tileUrgent
	}
	return tileQuiet
}

// foldsQuiet tells a surface code folds into a chip while it has nothing
// for the user: a standing thread's (Look.Folds), quiet or running. Yours,
// urgent or new, it is a tile again, in the usual order; opened, placed by
// the user, or on stage before the one opened, it is a tile all the same.
func foldsQuiet(s surface) bool {
	st := tileStateOf(s)
	return s.Look.Folds && (st == tileQuiet || st == tileRunning)
}

// tileOrder is the workspace's order: by state, then the heaviest, the
// latest changed, and the id, so the same pool is always laid out the
// same.
func tileOrder(a, b surface) int {
	return cmp.Or(cmp.Compare(tileRank[tileStateOf(a)], tileRank[tileStateOf(b)]), cmp.Compare(b.Weight, a.Weight),
		b.Updated.Compare(a.Updated), cmp.Compare(a.ID, b.ID))
}

// queueSurfaceWeight is a surface's weight for an attention item's (1 to
// 5): 5 is 100, 4 is 80 (urgent), 3 is 60, 2 is 40, 1 is 20.
func queueSurfaceWeight(w int) int { return min(max(w, 1), 5) * 20 }

// workspaceLayout is what the page lays out: the event stream's "layout".
type workspaceLayout struct {
	Focus    string       `json:"focus,omitempty"`    // the surface the user opened
	FocusBy  string       `json:"focusBy,omitempty"`  // who put it on stage: "" the user
	Previous string       `json:"previous,omitempty"` // what was on stage before it, a tile reduced next to it
	Inside   string       `json:"inside,omitempty"`   // the surface open inside the one on stage: a child thread's, in its parent's
	Items    []layoutTile `json:"items"`              // the tiles, each at its place on the grid
	Chips    []layoutChip `json:"chips"`              // the folded surfaces, in order
}

// layoutTile is one surface on the grid: col and row from 0, w and h in
// cells (the size's, or wider over columns left empty on its rows).
type layoutTile struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Size    string `json:"size"`
	Col     int    `json:"col"`
	Row     int    `json:"row"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Why     string `json:"why,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`  // placed by the user, or by the brain at their request
	Related bool   `json:"related,omitempty"` // related to the surface open (workspace_focus.go)
	Urgent  bool   `json:"urgent,omitempty"`  // placed before a surface the user opens (weightUrgent): the page keeps it in front when it lays an opening out at once
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

// layoutStage lays the pool out with the user's placement (workspace_gestures.go):
// what they closed is folded last, marked closed, and never opened; what
// they placed (or the brain placed at their request) comes first, at its
// place when it has one, at its size, and keeps both: no growing, no
// widening. Then the heaviest of what waits, then the surface opened
// (focus: xl), the one on stage before it (previous: a tile, m at most,
// never folded for the others, workspace_front.go) and those related to it
// (related, found by code, workspace_focus.go: m at most, the rest folded),
// then the others in their order (tileOrder).
func layoutStage(pool []surface, focus, previous string, related []string, pl userPlacement) workspaceLayout {
	all := pool
	focus, inside := onStage(all, focus)
	var closed []surface
	var inner []string
	pool = slices.DeleteFunc(slices.Clone(all), func(s surface) bool {
		if laidInside(all, s) {
			inner = append(inner, s.ID)
			return true // in the surface it opens inside, only
		}
		if _, ok := pl.Closed[s.ID]; ok {
			closed = append(closed, s)
			return true
		}
		return false
	})
	if _, ok := pl.Closed[focus]; ok {
		focus = ""
	}
	slices.SortStableFunc(pool, tileOrder)
	byID := map[string]surface{}
	for _, s := range pool {
		byID[s.ID] = s
	}
	if _, ok := byID[focus]; !ok {
		related = nil
	}
	related = slices.DeleteFunc(slices.Clone(related), func(id string) bool { return slices.Contains(inner, id) })
	l := workspaceLayout{Items: []layoutTile{}, Chips: []layoutChip{}}

	var wants []layoutWant
	added := map[string]bool{}
	add := func(s surface, focus bool) {
		if added[s.ID] {
			return
		}
		added[s.ID] = true
		w := layoutWant{s: s, size: sizeOfWeight(s.Weight), why: s.Why, focus: focus}
		if p, ok := pl.Pins[s.ID]; ok {
			w.pin = &p
			if _, known := tileDims[p.Size]; known {
				w.size = p.Size
			}
		} else if focus {
			w.size = sizeXL
		}
		w.why = truncate(oneLine(w.why), layoutWhyMax)
		wants = append(wants, w)
	}
	for _, id := range pl.order() {
		if s, ok := byID[id]; ok {
			add(s, id == focus)
		}
	}
	if _, ok := byID[focus]; ok {
		for _, s := range pool {
			if s.Weight >= weightUrgent && s.ID != focus {
				add(s, false) // what waits stays in front of what the user opened
			}
		}
	}
	if s, ok := byID[focus]; ok {
		add(s, true)
		l.Focus = focus
		if s, ok := byID[previous]; ok && previous != focus && !added[previous] {
			add(s, false)
			if w := &wants[len(wants)-1]; sizeRank[w.size] > sizeRank[sizeM] {
				w.size = sizeM
			}
		}
	}
	for _, id := range related {
		if s, ok := byID[id]; ok && !added[id] {
			add(s, false)
			if w := &wants[len(wants)-1]; w.pin == nil && sizeRank[w.size] > sizeRank[sizeM] {
				w.size = sizeM
			}
		}
	}
	if len(related) == 0 {
		for _, s := range pool {
			if !foldsQuiet(s) {
				add(s, false) // a standing thread with nothing for the user stays a chip
			}
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
	isRelated := map[string]bool{}
	if l.Focus != "" {
		for _, id := range related {
			isRelated[id] = id != l.Focus
		}
	}
	spots := packTilesAt(sizes, at)
	// Room left: what matters grows a size, the first first, never into xl
	// (xl is for what is urgent or asked so), and only if nothing else
	// shrinks or folds for it. What the user placed keeps its size.
	for i, w := range wants {
		if spots[i] == nil || w.s.Weight < weightGrow || w.pin != nil || w.s.ID == previous {
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
		l.Items = append(l.Items, layoutTile{ID: w.s.ID, Title: w.s.Title, Size: s.size, Col: s.col, Row: s.row, W: d[0], H: d[1], Why: w.why, Pinned: w.pin != nil, Related: isRelated[w.s.ID], Urgent: w.s.Weight >= weightUrgent})
	}
	widenTiles(l.Items)
	if l.Focus != "" && !slices.ContainsFunc(l.Items, func(t layoutTile) bool { return t.ID == l.Focus }) {
		l.Focus = "" // folded: what the user placed fills the grid
	}
	if l.Focus != "" && previous != l.Focus && slices.ContainsFunc(l.Items, func(t layoutTile) bool { return t.ID == previous }) {
		l.Previous = previous
	}
	if l.Focus != "" {
		l.Inside = inside
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
	return l
}

// onStage is what opening a surface puts on stage: the surface itself, or,
// for one that opens inside another on the workspace (a child thread's),
// that other, with it inside.
func onStage(pool []surface, id string) (focus, inside string) {
	if i := slices.IndexFunc(pool, func(s surface) bool { return s.ID == id }); i >= 0 && laidInside(pool, pool[i]) {
		return pool[i].Look.Inside, id
	}
	return id, ""
}

// laidInside tells whether a surface opens inside another of the pool,
// and so is never laid out on its own; should that other be gone, it is
// laid out as any.
func laidInside(pool []surface, s surface) bool {
	return s.Look.Inside != "" && slices.ContainsFunc(pool, func(p surface) bool { return p.ID == s.Look.Inside })
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

// packTilesAt puts each tile, in order, at the first place where it fits,
// top left first; one that fits nowhere shrinks, then is left out (nil). A
// tile with a place asked (at[i]: col, row) goes there first, at its size,
// when it is on the grid and free; else it is packed with the others.
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

// layoutState is what eriphe-core keeps of the layout: the choice it lays
// out, the surface opened, the last layout and who waits for the next.
type layoutState struct {
	mu         sync.Mutex
	focus      string
	focusAt    time.Time  // when it was last opened: it lasts focusFor from then
	focusSince time.Time  // when it began: opened again, it is the same focus
	f          focusState // who put it there, what was on stage before
	last       workspaceLayout
	raw        string
	changed    chan struct{} // closed when the layout changes
	kick       chan struct{} // the queue changed: its surfaces are made again (workspace_queue.go)
	syncMu     sync.Mutex    // one sync of the queue's surfaces at a time

	user       userPlacement       // what the user closed and placed this session (workspace_gestures.go)
	userLoaded bool                // user was read from the store
	read       map[string][]string // queue surfaces the user opened: the refs of the items other than cards they showed, which end once the user leaves it
}

// focusSurface is the surface the user opened: code places it first, for
// focusFor or until another is opened, what relates to it around
// (workspace_focus.go). "" ends the focus: the layout before it
// comes back.
func (c *core) focusSurface(id string, now time.Time) {
	st := &c.layout
	st.mu.Lock()
	was := st.focus
	if was != "" && now.Sub(st.focusAt) > focusFor {
		was = ""
	}
	if id != was {
		st.f.prev = was // "" when the stage was empty, or ends
		if id == "" {
			st.f.prev = ""
		}
	}
	st.focus, st.focusAt, st.f.by = id, now, ""
	if id != was {
		st.focusSince = now
		if id == "" {
			st.focusSince = time.Time{}
		}
	}
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

// relayout lays the pool out again: at once, no model. Those waiting for
// the layout are told when it changed.
func (c *core) relayout() workspaceLayout {
	now := time.Now()
	focus := c.workspaceFocus(now)
	pool, err := c.surfaces()
	var related []string
	if err == nil && focus != "" {
		stage, _ := onStage(pool, focus)
		related = relatedIDs(c.focusRelations(stage, pool))
	}
	start := c.workspaceSessionStart(now)
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		return st.last
	}
	previous := ""
	if focus != "" {
		previous = st.f.prev
	}
	l := layoutStage(pool, focus, previous, related, c.placementLocked(start, pool))
	if l.Focus != "" {
		l.FocusBy = st.f.by
	}
	raw, _ := json.Marshal(l)
	if string(raw) != st.raw {
		st.last, st.raw = l, string(raw)
		if st.changed != nil {
			close(st.changed)
		}
		st.changed = make(chan struct{})
		c.store.set(layoutKey, string(raw))
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
