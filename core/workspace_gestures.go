package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// What the user does on the workspace's surfaces, and what Eriphe sees of
// it (#426).
//
//   - Gestures (workspace.go's workspace_log): opened, ignored, closed,
//     moved, resized, about (a message written with it open). Opened is a
//     focus, laid out around (workspace_focus.go); the extraction reads
//     them all.
//   - The placement: what the user moved or resized is pinned there, at
//     that size, until they or the brain place it again or reset
//     (workspace_arrange), or the surface goes: how the user wants the
//     workspace laid out is kept, applied by code. The brain places too,
//     at the user's request, as the user would (« l'agenda en grand »).
//     What the user closed is not shown again (folded last, marked closed)
//     for the session: it lasts while the user is active, with no gap of
//     more than an hour, within one day (from 05:00, the activity day of
//     notify_quiet.go). Kept in kv (placementKey): a restart keeps it.
//   - The queue: closing a surface of the queue (author core) hides it for
//     the session and ends its items other than cards (cards wait for
//     their answer); an item other than a card also ends once the user
//     opened its surface and left it, or a day after it was delivered.
//   - The brain reads the workspace in each turn (workspaceBlock): what is
//     on the screen, at which size and place, what is open, folded and
//     closed, and the last gestures; workspace_read gives the detail.

const (
	placementKey   = "workspace_placement"
	sessionGap     = time.Hour      // away longer, the session ends
	queueItemKeep  = 24 * time.Hour // an item other than a card ends this long after it was delivered
	placedByUser   = "user"
	placedByBrain  = "brain"
	placementSizes = "s, m, l, xl"
)

// userPlacement is what the user placed, and closed this session.
type userPlacement struct {
	Closed map[string]int64   `json:"closed,omitempty"` // id → when (unix)
	Pins   map[string]tilePin `json:"pins,omitempty"`
}

// tilePin is a surface placed by the user, or by the brain at their
// request: at its size, at its place when Placed, else in Order.
type tilePin struct {
	Size   string `json:"size"`
	Col    int    `json:"col"`
	Row    int    `json:"row"`
	Placed bool   `json:"placed"`
	Order  int    `json:"order"`
	At     int64  `json:"at"`
	By     string `json:"by"` // user or brain
}

// order is the pinned surfaces in the order they are laid out: those with a
// place first, the latest first (it wins the place), then the others in
// their order.
func (p userPlacement) order() []string {
	ids := make([]string, 0, len(p.Pins))
	for id := range p.Pins {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		x, y := p.Pins[a], p.Pins[b]
		switch {
		case x.Placed != y.Placed:
			if x.Placed {
				return -1
			}
			return 1
		case x.Placed && x.At != y.At:
			return int(y.At - x.At)
		case x.Order != y.Order:
			return x.Order - y.Order
		case x.At != y.At:
			return int(y.At - x.At)
		}
		return strings.Compare(a, b)
	})
	return ids
}

func (p userPlacement) empty() bool { return len(p.Closed) == 0 && len(p.Pins) == 0 }

func (p userPlacement) clone() userPlacement {
	out := userPlacement{Closed: map[string]int64{}, Pins: map[string]tilePin{}}
	for k, v := range p.Closed {
		out.Closed[k] = v
	}
	for k, v := range p.Pins {
		out.Pins[k] = v
	}
	return out
}

// The session.

// activityDayStart is when now's activity day began: 05:00, the day before
// when it is earlier.
func activityDayStart(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc).Add(-dayTurn)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).Add(dayTurn)
}

// sessionStartIn is when the session going on at now began, from the
// user's activity times: the first one after the last gap of more than
// sessionGap within the day. Away longer than sessionGap now, it ended
// sessionGap after the last; with no activity today, the day's start.
func sessionStartIn(ts []time.Time, now time.Time, loc *time.Location) time.Time {
	day := activityDayStart(now, loc)
	start, prev := day, time.Time{}
	ts = slices.Clone(ts)
	slices.SortFunc(ts, func(a, b time.Time) int { return a.Compare(b) })
	for _, t := range ts {
		if t.Before(day) || t.After(now) {
			continue
		}
		if prev.IsZero() || t.Sub(prev) > sessionGap {
			start = t
		}
		prev = t
	}
	if !prev.IsZero() && now.Sub(prev) > sessionGap {
		start = prev.Add(sessionGap)
	}
	return start
}

// workspaceSessionStart is when the workspace's session began: from the
// user's activity (their messages, a tab in front, their answers to
// notifications, their gestures on the workspace).
func (c *core) workspaceSessionStart(now time.Time) time.Time {
	loc := c.conf().location
	if loc == nil {
		loc = time.Local
	}
	day := activityDayStart(now, loc)
	var ts []time.Time
	for _, src := range activitySources {
		got, err := src(c, day)
		if err != nil {
			continue
		}
		for _, s := range got {
			if !s.Unwelcome {
				ts = append(ts, s.At)
			}
		}
	}
	return sessionStartIn(ts, now, loc)
}

// placementLocked is the placement (c.layout.mu held): what was closed
// before the session's start is dropped, for good; a pin stays while its
// surface does (pool, when given: a pin of a surface gone goes).
func (c *core) placementLocked(start time.Time, pool []surface) userPlacement {
	st := &c.layout
	c.loadPlacementLocked()
	changed := false
	for id, at := range st.user.Closed {
		if at < start.Unix() {
			delete(st.user.Closed, id)
			changed = true
		}
	}
	if pool != nil {
		here := map[string]bool{}
		for _, s := range pool {
			here[s.ID] = true
		}
		for id := range st.user.Pins {
			if !here[id] {
				delete(st.user.Pins, id)
				changed = true
			}
		}
	}
	if changed {
		c.savePlacementLocked()
	}
	return st.user.clone()
}

func (c *core) loadPlacementLocked() {
	st := &c.layout
	if !st.userLoaded {
		st.userLoaded = true
		if raw := c.store.get(placementKey); raw != "" {
			json.Unmarshal([]byte(raw), &st.user)
		}
	}
}

func (c *core) savePlacementLocked() {
	st := &c.layout
	if st.user.empty() {
		c.store.set(placementKey, "")
		return
	}
	if raw, err := json.Marshal(st.user); err == nil {
		c.store.set(placementKey, string(raw))
	}
}

// placementActive tells whether the user closed something this session:
// the layout is made again as time passes, for the session's end.
func (c *core) placementActive() bool {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	c.loadPlacementLocked()
	return len(st.user.Closed) > 0
}

// changePlacement changes the placement, keeps it, and lays the workspace
// out again.
func (c *core) changePlacement(now time.Time, change func(*userPlacement)) workspaceLayout {
	start := c.workspaceSessionStart(now)
	st := &c.layout
	st.mu.Lock()
	c.placementLocked(start, nil)
	if st.user.Closed == nil {
		st.user.Closed = map[string]int64{}
	}
	if st.user.Pins == nil {
		st.user.Pins = map[string]tilePin{}
	}
	change(&st.user)
	c.savePlacementLocked()
	st.mu.Unlock()
	return c.relayout()
}

// Gestures on a surface.

// surfaceCleanTitle is a surface's title when its author wrote it in a
// clean turn, "" otherwise (or when there is no such surface): what may be
// quoted to the brain and kept for the extraction.
func (c *core) surfaceCleanTitle(id string) string {
	s, err := c.surface(id)
	if err != nil || len(s.Look.AuthorTaint) > 0 {
		return ""
	}
	return s.Title
}

var errPlacement = errors.New("a place is a size (" + placementSizes + ") with a column and a row where it fits on the 12 × 8 grid")

// openedOnWorkspace is the user opening a surface (focusSurface): it is no
// longer closed, and the items of the queue other than cards it shows are
// read: they end once the user leaves it (queueSweep).
func (c *core) openedOnWorkspace(id string, now time.Time) {
	st := &c.layout
	st.mu.Lock()
	c.loadPlacementLocked()
	if _, ok := st.user.Closed[id]; ok {
		delete(st.user.Closed, id)
		c.savePlacementLocked()
	}
	st.mu.Unlock()
	refs := notCards(c.queueItemsOf(id))
	if len(refs) == 0 {
		return
	}
	st.mu.Lock()
	if st.focus == id { // still the one open: a sweep never ends it under the click
		if st.read == nil {
			st.read = map[string][]string{}
		}
		st.read[id] = refs
	}
	st.mu.Unlock()
}

// notCards are the refs of the items that are not cards: a card waits for
// its answer, whatever the user saw.
func notCards(items []attentionItem) []string {
	var out []string
	for _, it := range items {
		if !strings.HasPrefix(it.Ref, "card:") {
			out = append(out, it.Ref)
		}
	}
	return out
}

// closeSurface is the user closing a surface: not shown again this
// session (folded last). One of the queue's ends its items but its cards:
// a card waits for its answer (it may not be in the conversation yet), its
// surface back the next session.
func (c *core) closeSurface(id string, now time.Time) error {
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	var ended []string
	if s.Author == surfaceAuthorCore {
		ended = notCards(c.queueItemsOf(id))
	}
	c.noteGesture(gestureClosed, "surface:"+id, now)
	if c.workspaceFocus(now) == id {
		c.focusSurface("", now)
	}
	c.changePlacement(now, func(p *userPlacement) {
		p.Closed[id] = now.Unix()
		delete(p.Pins, id)
	})
	for _, ref := range ended {
		c.attentionDone(ref)
	}
	return nil
}

// placeSurface is the user moving a surface or changing its size (what:
// moved or resized): pinned there, at that size, until placed again.
func (c *core) placeSurface(id, what, size string, col, row int, now time.Time) error {
	if what != gestureMoved && what != gestureResized {
		return errGesture
	}
	if _, err := c.surface(id); err != nil {
		return err
	}
	if !fitsGrid(size, col, row) {
		return errPlacement
	}
	c.noteGesture(what, "surface:"+id, now)
	c.changePlacement(now, func(p *userPlacement) {
		delete(p.Closed, id)
		p.Pins[id] = tilePin{Size: size, Col: col, Row: row, Placed: true, At: now.Unix(), By: placedByUser}
	})
	return nil
}

// fitsGrid tells whether a tile of size at col, row is on the grid.
func fitsGrid(size string, col, row int) bool {
	d, ok := tileDims[size]
	return ok && col >= 0 && row >= 0 && col+d[0] <= layoutCols && row+d[1] <= layoutRows
}

// The queue's natural end.

// queueSweep ends the items other than cards that the user has seen: a day
// after they were delivered, or once the user opened their surface and
// left it. A card stays until it is decided; an item held (for a
// breakpoint or the brief) is not delivered yet; one raised again since
// counts from then.
func (c *core) queueSweep(now time.Time) {
	cutoff := now.Add(-queueItemKeep).Unix()
	const ended = `ref NOT LIKE 'card:%' AND delivered > 0 AND delivered <= ? AND time <= ?`
	res, err := c.store.db.Exec(`DELETE FROM attention WHERE `+ended, cutoff, cutoff)
	if n, _ := rowsOf(res, err); n > 0 {
		c.kickWorkspace()
	}
	st := &c.layout
	st.mu.Lock()
	focus := st.focus // read with the read set: one opened since is both
	if focus != "" && now.Sub(st.focusAt) > focusFor {
		focus = ""
	}
	var left []string
	for id, refs := range st.read {
		if id != focus {
			left = append(left, refs...) // what it showed when opened: an item come since was not seen
			delete(st.read, id)
		}
	}
	st.mu.Unlock()
	for _, ref := range left {
		c.attentionDone(ref)
	}
}
