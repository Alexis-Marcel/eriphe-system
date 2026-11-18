package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The user's marks (#610): keep, drop or done on something a thread
// shows, an option, a candidate, a line of a checklist. Code records them in
// the thread's data, never in the composed part, so a rewrite of the part
// never loses one.
//
//   - A mark is a closed action (a2ui.ActionMark): {list, item, mark},
//     ids only, the thread the surface's. Only the Mark component sends
//     it: who holds the thread places a Mark in a block of its part
//     (a2ui.CheckActions: on a thread's surface only), its list and item
//     literal ids or bound to the data (a row of a list), the marks it
//     offers (keep and drop, done). The page reads its state at
//     /thread/marks/<list>/<item>.
//   - When the page sends one, code checks it against the surface as it
//     is stored (markOf): the component is a Mark of the thread's own
//     surface, the mark one it offers or "", and list and item are the
//     literal ones or values the binding takes in the surface's data,
//     each an id (a2ui.MarkIDOK). The thread is active.
//   - Kept per thread, list and item (thread_marks); "" takes one away.
//     Served at /thread/marks/<list>/<item> (/thread/marks always there),
//     and read_thread returns them.
//   - A mark starts no turn: it is a gesture (noteMarkGesture), which the
//     brain reads with the workspace at its next turn, by ids only.
//   - A mark set on a tainted surface is external: its ids may come from
//     someone else's data (no charset tells an id from words), so its
//     gesture does not quote them, and read_thread taints its reader as a
//     tainted note does (readMarksTaint).

// threadMarksPath is where a thread's surface serves its marks.
const threadMarksPath = "/" + threadDataKey + "/" + threadMarksKey

// threadMarksKey is the marks' key in code's data.
const threadMarksKey = "marks"

const threadMarksSchema = `CREATE TABLE IF NOT EXISTS thread_marks (
	thread   INTEGER NOT NULL,
	list     TEXT NOT NULL,
	item     TEXT NOT NULL,
	mark     TEXT NOT NULL,              -- keep, drop, done
	at       INTEGER NOT NULL,
	external INTEGER NOT NULL DEFAULT 0, -- set on a tainted surface: its ids may be someone else's words
	PRIMARY KEY (thread, list, item)
);`

func init() { extraSchema = append(extraSchema, threadMarksSchema) }

// threadMark is one mark of the user's on a thread.
type threadMark struct {
	List     string `json:"list"`
	Item     string `json:"item"`
	Mark     string `json:"mark"`
	At       string `json:"at"`
	External bool   `json:"external,omitempty"` // set on a tainted surface
}

// threadMarkTaint is why reading an external mark taints: + list/item.
const threadMarkTaint = "mark:"

// readMarksTaint is why reading a thread's marks taints its reader: each
// external one.
func readMarksTaint(marks []threadMark) []string {
	var out []string
	for _, m := range marks {
		if m.External {
			out = append(out, threadMarkTaint+m.List+"/"+m.Item)
		}
	}
	return out
}

// setMark records a mark of the user's ("" takes it away), external when
// set on a tainted surface, notes the gesture and writes the thread's
// surface again. It wakes no one.
func (c *core) setMark(thread int64, list, item, mark string, external bool, now time.Time) error {
	var err error
	if mark == "" {
		_, err = c.store.db.Exec(`DELETE FROM thread_marks WHERE thread = ? AND list = ? AND item = ?`, thread, list, item)
	} else {
		_, err = c.store.db.Exec(`INSERT INTO thread_marks (thread, list, item, mark, at, external) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (thread, list, item) DO UPDATE SET mark = excluded.mark, at = excluded.at, external = excluded.external`,
			thread, list, item, mark, now.Unix(), external)
	}
	if err != nil {
		return err
	}
	c.noteMarkGesture(thread, list, item, mark, external, now) // workspace.go
	c.keepThreadSurface(thread)
	return nil
}

// marksOf are a thread's marks, by list, the oldest first.
func (c *core) marksOf(thread int64) []threadMark {
	rows, err := c.store.db.Query(`SELECT list, item, mark, at, external FROM thread_marks WHERE thread = ? ORDER BY list, at, item`, thread)
	if err != nil {
		return nil
	}
	defer rows.Close()
	loc := c.userZone()
	var out []threadMark
	for rows.Next() {
		var m threadMark
		var at int64
		if rows.Scan(&m.List, &m.Item, &m.Mark, &at, &m.External) == nil {
			m.At = time.Unix(at, 0).In(loc).Format("2006-01-02 15:04")
			out = append(out, m)
		}
	}
	return out
}

// marksData are a thread's marks as its surface's data holds them,
// /thread/marks/<list>/<item>: an empty object when it has none.
func (c *core) marksData(thread int64) map[string]any {
	out := map[string]any{}
	for _, m := range c.marksOf(thread) {
		list, _ := out[m.List].(map[string]any)
		if list == nil {
			list = map[string]any{}
			out[m.List] = list
		}
		list[m.Item] = m.Mark
	}
	return out
}

// markOf is the mark a click sets: the clicked component is a Mark of the
// thread's own surface, the mark one it offers ("" clears it), its list
// and item the ones written or values its binding takes in the data, each
// an id; whatever else the page sent is refused. external: the surface is
// tainted, its ids may be someone else's words.
func (c *core) markOf(s surface, a a2ui.Action) (thread int64, list, item, mark string, external bool, refused *errActionRefused) {
	th, ok := threadOfSurface(s.ID)
	comp, at, found := componentOf(s, a.SourceComponentID)
	if !ok || s.Author != threadAuthor(th) || !found || comp["component"] != "Mark" {
		return 0, "", "", "", false, &errActionRefused{Path: at, Message: "a mark comes from a Mark of a thread's own surface"}
	}
	str := func(k string) (string, bool) { // an id bound to a number comes as one
		var v any
		json.Unmarshal(a.Context[k], &v)
		switch x := v.(type) {
		case string:
			return x, true
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), true
		}
		return "", false
	}
	offered, _ := comp["marks"].([]any)
	mark, okMark := str("mark")
	list, okList := str("list")
	item, okItem := str("item")
	if len(a.Context) != 3 || !okMark || !okList || !okItem || mark != "" && !slices.Contains(offered, any(mark)) {
		return 0, "", "", "", false, &errActionRefused{Path: at, Message: `a mark's context is {"list", "item", "mark"}: a mark its Mark offers, or "" to clear it`}
	}
	for k, v := range map[string]string{"list": list, "item": item} {
		if !a2ui.MarkIDOK(v) || !markShown(s, comp, at, k, v) {
			return 0, "", "", "", false, &errActionRefused{Path: at + "/" + k, Message: "this Mark does not mark that " + k}
		}
	}
	if t, err := c.thread(th); err != nil || !t.active() {
		return 0, "", "", "", false, &errActionRefused{Message: "the thread has ended: it takes no mark"}
	}
	return th, list, item, mark, s.Tainted != "", nil
}

// markShown tells whether a Mark's list or item, as the page sent it, is
// the one it carries: its literal, or one of the values its binding takes
// in the surface's data.
func markShown(s surface, comp map[string]any, at, prop, sent string) bool {
	if lit, ok := comp[prop].(string); ok {
		return lit == sent
	}
	_, values := boundValues(s, at, prop)
	for _, v := range values {
		if x, ok := v.(string); ok && x == sent {
			return true
		}
		if x, ok := v.(float64); ok && strconv.FormatFloat(x, 'f', -1, 64) == sent {
			return true
		}
	}
	return false
}
