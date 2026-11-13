package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The composed workspace's memory and tools (#409). The workspace keeps no
// rule and has no fixed control but the Oui / Non of a card:
//
//   - How the user wants it (what in front, what less, what never) is a
//     preference memory with subject "workspace": the brain remembers it
//     from the user's words, the nightly extraction proposes one from the
//     conversation or from what the user did. The composer reads those
//     preferences, and the user's other preferences that bear on the queue,
//     with every snapshot (workspacePreferences), and composes again when
//     one enters memory (workspacePrefStamp).
//   - What the user does on it feeds memory, implicitly: a surface opened, a
//     tile that left the grid unopened, a tile closed, moved or resized,
//     a message written about it, are gestures (workspace_log) the
//     extraction reads as one segment of the day's transcript. No counter
//     and no score: the extraction infers, the user approves. What they do
//     to the layout holds for their session (workspace_gestures.go).
//   - A message written while a surface is open carries its ref
//     (messageAbout): « montre-m'en moins » is said to Eriphe in words.

const (
	workspaceSubject = "workspace" // the subject of a preference about the workspace

	workspacePrefsMax  = 12 // workspace preferences a composer's turn reads, the latest first
	workspaceRecallMax = 3  // other preferences recalled from the queue's words
	workspacePrefMax   = 300

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

// The display rules (#402) are gone: their table, and their cards.
func displayRulesGone(db *sql.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS attention_rules`); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM attention WHERE ref IN (SELECT 'card:' || id FROM convo_cards WHERE kind = 'showrule')`); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM convo_cards WHERE kind = 'showrule'`)
	return err
}

// The composition by ref and its canvas of objects (#424) are gone: the
// last composition kept, and the gestures and messages about an object
// rather than a surface.
func refCompositionGone(db *sql.DB) error {
	for _, q := range []string{
		`DELETE FROM kv WHERE key = 'composition'`,
		`DELETE FROM kv WHERE key LIKE 'about_msg:%' AND value NOT LIKE 'surface:%'`,
		`DELETE FROM workspace_log WHERE ref NOT LIKE 'surface:%'`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

const workspaceLogSchema = `CREATE TABLE IF NOT EXISTS workspace_log (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	time  INTEGER NOT NULL,
	what  TEXT NOT NULL,             -- opened, ignored, closed, moved, resized, about
	ref   TEXT NOT NULL,             -- surface:<id>
	title TEXT NOT NULL DEFAULT ''   -- the surface's title, from a clean turn of its author
);
CREATE INDEX IF NOT EXISTS workspace_log_time ON workspace_log (time);`

func init() {
	extraSchema = append(extraSchema, workspaceLogSchema)
	schemaFixes = append(schemaFixes, displayRulesGone, refCompositionGone)
}

// Preferences.

// workspacePreferences are the user's preferences the composer reads with a
// snapshot: those about the workspace, the latest first, then the few
// others the words of what is on it (titles, whys) recall. Active and current ones only: a
// proposed preference waits for the user.
func (c *core) workspacePreferences(words []string) []string {
	if c.mem == nil {
		return nil
	}
	xs, _ := c.mem.list(`kind = ? AND subject = ? AND status = ? AND (valid_to IS NULL OR valid_to > ?)`,
		[]any{kindPreference, workspaceSubject, memActive, c.mem.now().Unix()}, `updated_at DESC, id DESC`, workspacePrefsMax)
	if q := strings.TrimSpace(strings.Join(words, " ")); q != "" {
		more, _ := c.mem.recall(recallQuery{Query: q, Kinds: []string{kindPreference}, Limit: workspaceRecallMax})
		xs = append(xs, more...)
	}
	var out []string
	seen := map[int64]bool{}
	for _, x := range xs {
		if seen[x.ID] {
			continue
		}
		seen[x.ID] = true
		out = append(out, truncate(oneLine(x.Text), workspacePrefMax))
	}
	return out
}

// workspacePrefStamp changes when a workspace preference enters memory, is
// changed or leaves it; changed is the stamp of the latest change, words
// that preference's text.
func (c *core) workspacePrefStamp() (stamp string, changed int64, words string) {
	if c.mem == nil {
		return "", 0, ""
	}
	var n int
	var last sql.NullInt64
	c.mem.db.QueryRow(`SELECT COUNT(*), MAX(updated_at) FROM memories WHERE kind = ? AND subject = ? AND status = ?`,
		kindPreference, workspaceSubject, memActive).Scan(&n, &last)
	c.mem.db.QueryRow(`SELECT text FROM memories WHERE kind = ? AND subject = ? AND status = ? ORDER BY updated_at DESC, id DESC LIMIT 1`,
		kindPreference, workspaceSubject, memActive).Scan(&words)
	return fmt.Sprintf("%d:%d", n, last.Int64), last.Int64, words
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
// and the composer and the brain (French) read each.
var gestureKinds = map[string]struct{ en, fr string }{
	gestureOpened:  {"opened", "ouvert"},
	gestureIgnored: {"left unopened (it was shown, then left the screen)", "laissé sans l'ouvrir"},
	gestureClosed:  {"closed (not to be shown again until the next session)", "fermé"},
	gestureMoved:   {"moved (placed it elsewhere)", "déplacé"},
	gestureResized: {"resized", "redimensionné"},
	gestureAbout:   {"wrote a message about", "écrit à propos de"},
}

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
	id, ok := strings.CutPrefix(ref, "surface:")
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

// gestureSegment is the gestures of a window as one segment of the
// extraction's transcript: what the user did, not what they said. The
// titles are the surfaces', as their authors wrote them: external.
func gestureSegment(gs []gesture, user string) segment {
	var b strings.Builder
	fmt.Fprintf(&b, "What %s did on the composed workspace (gestures, not words; the titles are the surfaces', external):", user)
	for _, g := range gs {
		what := g.What
		if k, ok := gestureKinds[g.What]; ok {
			what = k.en
		}
		fmt.Fprintf(&b, "\n- %s %s %s", g.Time.Local().Format("15:04"), what, g.Ref)
		if g.Title != "" {
			fmt.Fprintf(&b, " %q", g.Title)
		}
	}
	last := gs[len(gs)-1]
	return segment{Ref: fmt.Sprintf("workspace:%d", last.ID), Time: last.Time, Who: "workspace", Trust: trustExternal, Text: b.String()}
}

// A message about what is open.

func aboutKey(seq int64) string { return fmt.Sprintf("about_msg:%d", seq) }

// noteAbout keeps the surface a message of the user's was written about
// (the one open on the workspace, surface:<id>), and the gesture; anything
// else is not kept.
func (c *core) noteAbout(seq int64, ref string) {
	ref = strings.TrimSpace(ref)
	if gestureRef(ref) {
		c.store.set(aboutKey(seq), ref)
		c.noteGesture(gestureAbout, ref, time.Now())
	}
}

// messageAbout is the ref a message was written about, "" for none.
func (c *core) messageAbout(seq int) string { return c.store.get(aboutKey(int64(seq))) }
