package main

import (
	"log"
	"slices"
	"time"
)

// Whether the workspace serves the user (#431): what code put in front,
// and what the user did with it. Each time a surface becomes a tile of the
// layout (workspace_layout.go), its time in front starts; it ends when the
// surface leaves the tiles (folded, removed). The measure reads those
// spans with the user's gestures (workspace_log): a surface put in front
// is opened (an opened gesture while it was there), or ignored (it left
// the tiles unopened, or an ignored or closed gesture came first), or
// still in front. A surface the user opened from the fold, which became a
// tile because they opened it, was not put in front by code: not counted.
// Shown on Settings → Surfaces; no model, no score fed back.

const workspaceFrontSchema = `CREATE TABLE IF NOT EXISTS workspace_front (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	surface TEXT NOT NULL,
	shown   INTEGER NOT NULL,           -- when it became a tile
	gone    INTEGER NOT NULL DEFAULT 0  -- when it stopped being one, 0 while it is
);
CREATE INDEX IF NOT EXISTS workspace_front_shown ON workspace_front (shown);`

func init() { extraSchema = append(extraSchema, workspaceFrontSchema) }

const (
	surfaceUseWindow = 7 * 24 * time.Hour
	// openedFromFold: an open this little before a surface's time in front
	// started made it a tile (the user opened its chip).
	openedFromFold = 2 * time.Second
)

// noteFront keeps the surfaces in front as the layout l has them: a time
// in front starts for each tile not in front yet, and ends for each
// surface in front that is no longer a tile. Read from the store, so a
// restart neither starts nor ends one twice.
func (c *core) noteFront(l workspaceLayout, now time.Time) {
	rows, err := c.store.db.Query(`SELECT id, surface FROM workspace_front WHERE gone = 0`)
	if err != nil {
		log.Printf("workspace front: %v", err)
		return
	}
	open := map[string]int64{}
	for rows.Next() {
		var id int64
		var s string
		if rows.Scan(&id, &s) == nil {
			open[s] = id
		}
	}
	rows.Close()
	tiles := map[string]bool{}
	for _, t := range l.Items {
		tiles[t.ID] = true
		if _, ok := open[t.ID]; !ok {
			c.store.db.Exec(`INSERT INTO workspace_front (surface, shown) VALUES (?, ?)`, t.ID, now.Unix())
		}
	}
	for s, id := range open {
		if !tiles[s] {
			c.store.db.Exec(`UPDATE workspace_front SET gone = ? WHERE id = ?`, now.Unix(), id)
		}
	}
	c.store.db.Exec(`DELETE FROM workspace_front WHERE gone != 0 AND gone < ?`, now.Add(-gestureKeep).Unix())
}

// frontSpan is one time a surface was in front; Gone is zero while it is.
type frontSpan struct {
	Surface     string
	Shown, Gone time.Time
}

// surfaceUse is how the surfaces put in front fared.
type surfaceUse struct {
	Shown   int // put in front by code
	Opened  int // opened while in front
	Ignored int // left the tiles unopened, or ignored or closed first
	Front   int // still in front, not opened yet
	// ToOpen is the median time from in front to opened; 0 with none.
	ToOpen time.Duration
}

// IgnoredShare is the share of the surfaces put in front and settled
// (opened or ignored) that were ignored, 0 to 1.
func (u surfaceUse) IgnoredShare() float64 {
	if u.Opened+u.Ignored == 0 {
		return 0
	}
	return float64(u.Ignored) / float64(u.Opened+u.Ignored)
}

// surfaceUseOf measures spans against the gestures. Pure.
func surfaceUseOf(spans []frontSpan, gs []gesture) surfaceUse {
	var u surfaceUse
	var waits []time.Duration
	for _, sp := range spans {
		ref := "surface:" + sp.Surface
		var opened, dismissed time.Time
		fromFold := false
		for _, g := range gs {
			if g.Ref != ref || g.Time.Before(sp.Shown.Add(-openedFromFold)) || (!sp.Gone.IsZero() && g.Time.After(sp.Gone)) {
				continue
			}
			switch g.What {
			case gestureOpened:
				if !g.Time.After(sp.Shown) {
					fromFold = true
				} else if opened.IsZero() {
					opened = g.Time
				}
			case gestureIgnored, "closed": // closed: the page's close (#426)
				if dismissed.IsZero() {
					dismissed = g.Time
				}
			}
		}
		switch {
		case fromFold:
			continue
		case !opened.IsZero() && (dismissed.IsZero() || opened.Before(dismissed)):
			u.Opened++
			waits = append(waits, opened.Sub(sp.Shown))
		case !dismissed.IsZero() || !sp.Gone.IsZero():
			u.Ignored++
		default:
			u.Front++
		}
		u.Shown++
	}
	if n := len(waits); n > 0 {
		slices.Sort(waits)
		u.ToOpen = waits[n/2]
		if n%2 == 0 {
			u.ToOpen = (waits[n/2-1] + waits[n/2]) / 2
		}
	}
	return u
}

// surfaceUseSince measures the surfaces put in front since from.
func (c *core) surfaceUseSince(from, now time.Time) surfaceUse {
	rows, err := c.store.db.Query(`SELECT surface, shown, gone FROM workspace_front WHERE shown >= ? ORDER BY id`, from.Unix())
	if err != nil {
		return surfaceUse{}
	}
	var spans []frontSpan
	for rows.Next() {
		var sp frontSpan
		var shown, gone int64
		if rows.Scan(&sp.Surface, &shown, &gone) != nil {
			continue
		}
		sp.Shown = time.Unix(shown, 0)
		if gone > 0 {
			sp.Gone = time.Unix(gone, 0)
		}
		spans = append(spans, sp)
	}
	rows.Close()
	gs, _ := c.gestures(from.Add(-openedFromFold), now.Add(time.Second))
	return surfaceUseOf(spans, gs)
}
