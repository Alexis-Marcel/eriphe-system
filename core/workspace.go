package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The composed workspace's memory and tools (#409). The workspace keeps no
// rule and has no fixed control but the Oui / Non of a card:
//
//   - Code orders it (workspace_layout.go), no model. How the user wants
//     it laid out (« l'agenda en grand ») the brain applies at once with
//     workspace_arrange: a placement code keeps (workspace_gestures.go).
//     What they want to see more or less of (« moins de lettres de news »)
//     is a preference memory about "topic:workspace", from the user's words
//     (the nightly extraction never writes a preference): whoever notes
//     the items of a thread (the brain, a thread's lead, a skill that
//     watches a source) reads it, and notes those items so.
//   - What the user does on it: a surface opened, a tile that left the
//     grid unopened, a tile closed, moved or resized, a message written
//     about it, are gestures (workspace_log) the brain reads. No counter
//     and no score.
//   - A message written while a surface is open carries its ref
//     (messageAbout): « montre-m'en moins » is said to Eriphe in words.

const (
	workspaceEntity = "topic:workspace" // what a preference about the workspace is about

	workspaceTitleMax = 120 // a surface's title, quoted to the brain
	seePrefsMax       = 12  // the preferences about what the user sees a watcher reads, the latest first
	seePrefMax        = 300 // runes of each

	gestureOpened  = "opened"  // the user opened a surface (a focus)
	gestureIgnored = "ignored" // a tile left the grid without being opened
	gestureClosed  = "closed"  // the user closed a tile: not shown again this session (workspace_gestures.go)
	gestureMoved   = "moved"   // the user moved a tile: pinned there this session
	gestureResized = "resized" // the user changed a tile's size: pinned at it this session
	gestureAbout   = "about"   // the user wrote a message with it open
	gestureRepeat  = 10 * time.Minute
	gestureSteps   = time.Minute // moves or resizes of the same tile this close, one after the other, are one
	gestureKeep    = 30 * 24 * time.Hour
	gestureTitle   = 120
)

// isWorkspaceEntity tells an about that is the workspace's.
func isWorkspaceEntity(s string) bool {
	k, _ := aliasKey(s)
	return k == workspaceEntity
}

const workspaceLogSchema = `CREATE TABLE IF NOT EXISTS workspace_log (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	time  INTEGER NOT NULL,
	what  TEXT NOT NULL,             -- opened, ignored, closed, moved, resized, about
	ref   TEXT NOT NULL,             -- surface:<id>
	title TEXT NOT NULL DEFAULT ''   -- the surface's title, from a clean turn of its author
);
CREATE INDEX IF NOT EXISTS workspace_log_time ON workspace_log (time);`

func init() { extraSchema = append(extraSchema, workspaceLogSchema) }

// seePreferences are the user's preferences about what they see (about
// topic:workspace), the latest first: what a skill that notes a thread's
// items reads with its run (skillRunPrompt). Active and current ones only:
// a proposed preference waits for the user.
func (c *core) seePreferences() []string {
	if c.mem == nil {
		return nil
	}
	xs, _ := c.mem.list(`kind = ? AND about = ? AND status = ? AND (valid_to IS NULL OR valid_to > ?)`,
		[]any{kindPreference, workspaceEntity, memActive, c.mem.now().Unix()}, `updated_at DESC, id DESC`, seePrefsMax)
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, truncate(oneLine(x.Text), seePrefMax))
	}
	return out
}

// Gestures: what the user does on the workspace, for the extraction.

// gesture is one thing the user did on the workspace.
type gesture struct {
	ID    int64
	Time  time.Time
	What  string
	Ref   string
	Title string
}

// gestureKinds are the gestures kept, and how the extraction (English)
// and the brain (French) read each.
var gestureKinds = map[string]struct{ en, fr string }{
	gestureOpened:  {"opened", "ouvert"},
	gestureIgnored: {"left unopened (it was shown, then left the screen)", "laissé sans l'ouvrir"},
	gestureClosed:  {"closed (not to be shown again until the next session)", "fermé"},
	gestureMoved:   {"moved (placed it elsewhere)", "déplacé"},
	gestureResized: {"resized", "redimensionné"},
	gestureAbout:   {"wrote a message about", "écrit à propos de"},
}

// surfaceRefPrefix names a surface in a gesture and in what a message was
// written about: surface:<id>.
const surfaceRefPrefix = "surface:"

var errGesture = errors.New("a gesture is opened, ignored, closed, moved, resized or about, with a surface:<id>")

// noteGesture keeps a gesture on a surface (surface:<id>); opened, ignored
// and about once per surface in gestureRepeat; each close counts, and each
// move or resize but those that go on the one just before (gestureSteps);
// those older than gestureKeep go. Its title is the one core has, and only
// when its author wrote it in a clean turn: never the page's.
func (c *core) noteGesture(what, ref string, now time.Time) error {
	if _, ok := gestureKinds[what]; !ok {
		return errGesture
	}
	if !gestureRef(ref) {
		return errGesture
	}
	title := c.surfaceCleanTitle(strings.TrimPrefix(ref, "surface:"))
	if what == gestureOpened || what == gestureIgnored || what == gestureAbout {
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM workspace_log WHERE ref = ? AND what = ? AND time > ?`,
			ref, what, now.Add(-gestureRepeat).Unix()).Scan(&n)
		if n > 0 {
			return nil
		}
	}
	if what == gestureMoved || what == gestureResized {
		// One move in steps (the arrow keys, a click a size) is one gesture.
		var id int64
		var last, lastRef string
		c.store.db.QueryRow(`SELECT id, what, ref FROM workspace_log WHERE time > ? ORDER BY id DESC LIMIT 1`,
			now.Add(-gestureSteps).Unix()).Scan(&id, &last, &lastRef)
		if id > 0 && last == what && lastRef == ref {
			_, err := c.store.db.Exec(`UPDATE workspace_log SET time = ? WHERE id = ?`, now.Unix(), id)
			return err
		}
	}
	c.store.db.Exec(`DELETE FROM workspace_log WHERE time < ?`, now.Add(-gestureKeep).Unix())
	_, err := c.store.db.Exec(`INSERT INTO workspace_log (time, what, ref, title) VALUES (?, ?, ?, ?)`,
		now.Unix(), what, ref, truncate(oneLine(title), gestureTitle))
	return err
}

// gestureRef tells whether a gesture is about a surface (surface:<id>),
// the one thing the workspace shows.
func gestureRef(ref string) bool {
	id, ok := strings.CutPrefix(ref, surfaceRefPrefix)
	return ok && surfaceIDRe.MatchString(id)
}

// gestures are the gestures between from and to, the oldest first.
func (c *core) gestures(from, to time.Time) ([]gesture, error) {
	rows, err := c.store.db.Query(`SELECT id, time, what, ref, title FROM workspace_log WHERE time >= ? AND time < ? ORDER BY id`,
		from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gesture
	for rows.Next() {
		var g gesture
		var at int64
		if err := rows.Scan(&g.ID, &at, &g.What, &g.Ref, &g.Title); err != nil {
			return nil, err
		}
		g.Time = time.Unix(at, 0)
		out = append(out, g)
	}
	return out, rows.Err()
}

// A message about what is open.

func aboutMsgKey(seq int64) string { return fmt.Sprintf("about_msg:%d", seq) }

// noteAbout keeps the surface a message of the user's was written about
// (the one open on the workspace, surface:<id>), and the gesture; anything
// else is not kept.
func (c *core) noteAbout(seq int64, ref string) {
	ref = strings.TrimSpace(ref)
	if gestureRef(ref) {
		c.store.set(aboutMsgKey(seq), ref)
		c.noteGesture(gestureAbout, ref, time.Now())
	}
}

// messageAbout is the ref a message was written about, "" for none.
func (c *core) messageAbout(seq int) string { return c.store.get(aboutMsgKey(int64(seq))) }
