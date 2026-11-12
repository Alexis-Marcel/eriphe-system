package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
//   - What the user does on it feeds memory, implicitly: a module opened, a
//     module that left the screen unopened, are gestures (workspace_log)
//     the extraction reads as one segment of the day's transcript. No
//     counter and no score: the extraction infers, the user approves.
//   - A message written while a module is open carries its ref
//     (messageAbout): « montre-m'en moins » is said to Eriphe in words.
//   - The brain has two tools: workspace_show puts objects in front at once
//     (the composer composes again with them required), and
//     workspace_walkthrough walks the user through objects on the page
//     (the event stream's "walkthrough").

const (
	workspaceSubject = "workspace" // the subject of a preference about the workspace

	workspacePrefsMax  = 12 // workspace preferences a composer's turn reads, the latest first
	workspaceRecallMax = 3  // other preferences recalled from the queue's words
	workspacePrefMax   = 300

	workspaceShowFor    = time.Hour // what workspace_show puts in front stays there this long at most
	workspaceWalkMax    = 12        // refs a walkthrough goes through
	workspaceWalkReplay = 20 * time.Second

	gestureOpened  = "opened"  // the user opened a module (a focus)
	gestureIgnored = "ignored" // a module left the screen without being opened
	gestureRepeat  = 10 * time.Minute
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

const workspaceLogSchema = `CREATE TABLE IF NOT EXISTS workspace_log (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	time  INTEGER NOT NULL,
	what  TEXT NOT NULL,             -- opened, ignored
	ref   TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT ''   -- the object's title, as the page showed it (external)
);
CREATE INDEX IF NOT EXISTS workspace_log_time ON workspace_log (time);`

func init() {
	extraSchema = append(extraSchema, workspaceLogSchema)
	schemaFixes = append(schemaFixes, displayRulesGone)
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

var errGesture = errors.New("a gesture is opened or ignored, with a ref")

// noteGesture keeps a gesture, once per ref and kind of gesture in
// gestureRepeat; those older than gestureKeep go.
func (c *core) noteGesture(what, ref, title string, now time.Time) error {
	if what != gestureOpened && what != gestureIgnored {
		return errGesture
	}
	if !gestureRef(ref) {
		return errGesture
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM workspace_log WHERE ref = ? AND what = ? AND time > ?`,
		ref, what, now.Add(-gestureRepeat).Unix()).Scan(&n)
	if n > 0 {
		return nil
	}
	c.store.db.Exec(`DELETE FROM workspace_log WHERE time < ?`, now.Add(-gestureKeep).Unix())
	_, err := c.store.db.Exec(`INSERT INTO workspace_log (time, what, ref, title) VALUES (?, ?, ?, ?)`,
		now.Unix(), what, ref, truncate(oneLine(title), gestureTitle))
	return err
}

// gestureRef tells whether a gesture is about something the workspace
// shows: an object's ref, or a surface (surface:<id>).
func gestureRef(ref string) bool {
	if id, ok := strings.CutPrefix(ref, "surface:"); ok {
		return surfaceIDRe.MatchString(id)
	}
	_, err := parseRef(ref)
	return err == nil
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
// titles are the objects' (a mail's subject): external.
func gestureSegment(gs []gesture, user string) segment {
	var b strings.Builder
	fmt.Fprintf(&b, "What %s did on the composed workspace (gestures, not words; the titles are the objects', external):", user)
	for _, g := range gs {
		what := "opened"
		if g.What == gestureIgnored {
			what = "left unopened (it was shown, then left the screen)"
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

// noteAbout keeps the ref a message of the user's was written about (the
// module open on the workspace); a ref that does not parse is not kept.
func (c *core) noteAbout(seq int64, ref string) {
	if _, err := parseRef(ref); err == nil {
		c.store.set(aboutKey(seq), strings.TrimSpace(ref))
	}
}

// messageAbout is the ref a message was written about, "" for none.
func (c *core) messageAbout(seq int) string { return c.store.get(aboutKey(int64(seq))) }

// The brain's tools.

// workspaceShow is what the brain put in front (workspace_show), until
// Until: every snapshot holds those refs and every composition shows them
// as modules.
type workspaceShow struct {
	Seq   int64
	Refs  []string
	Why   string
	At    time.Time
	Until time.Time
}

// workspaceWalk is a walkthrough the brain asked for: the page goes
// through Refs, or what it shows when there are none.
type workspaceWalk struct {
	ID   int64     `json:"id"`
	Refs []string  `json:"refs"`
	At   time.Time `json:"at"`
}

func cleanRefs(in []string, max int) ([]string, error) {
	var out []string
	for _, r := range in {
		ref, err := parseRef(r)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", r, err)
		}
		if !slices.Contains(out, ref.String()) {
			out = append(out, ref.String())
		}
	}
	if len(out) > max {
		return nil, fmt.Errorf("at most %d refs", max)
	}
	return out, nil
}

// showOnWorkspace puts refs in front, for workspaceShowFor at most; none
// gives the front back to the composer. The composer composes again at
// once (runComposer).
func (c *core) showOnWorkspace(refs []string, why string, now time.Time) error {
	refs, err := cleanRefs(refs, compositionItemsMax)
	if err != nil {
		return err
	}
	s := &c.composer
	s.mu.Lock()
	defer s.mu.Unlock()
	s.showSeq++
	if len(refs) == 0 {
		s.show = nil
		return nil
	}
	s.show = &workspaceShow{Seq: s.showSeq, Refs: refs, Why: truncate(oneLine(why), compositionWhyMax), At: now, Until: now.Add(workspaceShowFor)}
	return nil
}

// shown is what the brain put in front and still is at now, nil for none;
// seq changes with each workspace_show.
func (c *core) shown(now time.Time) (show *workspaceShow, seq int64) {
	s := &c.composer
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.show != nil && now.After(s.show.Until) {
		s.show = nil
	}
	if s.show == nil {
		return nil, s.showSeq
	}
	cp := *s.show
	return &cp, s.showSeq
}

// walkWorkspace asks the open pages to walk the user through refs (none:
// through what they show).
func (c *core) walkWorkspace(refs []string, now time.Time) (workspaceWalk, error) {
	refs, err := cleanRefs(refs, workspaceWalkMax)
	if err != nil {
		return workspaceWalk{}, err
	}
	s := &c.composer
	s.mu.Lock()
	defer s.mu.Unlock()
	s.walk = workspaceWalk{ID: s.walk.ID + 1, Refs: append([]string{}, refs...), At: now}
	return s.walk, nil
}

// lastWalk is the last walkthrough asked, ID 0 before the first.
func (c *core) lastWalk() workspaceWalk {
	s := &c.composer
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.walk
}

// walkSeenAt is the walkthrough an event stream opened at now has already
// seen: the last one, unless it was asked just before (a page opened to
// be walked through).
func (c *core) walkSeenAt(now time.Time) int64 {
	w := c.lastWalk()
	if w.ID > 0 && now.Sub(w.At) < workspaceWalkReplay {
		return w.ID - 1
	}
	return w.ID
}

type workspaceShowIn struct {
	Refs []string `json:"refs" jsonschema:"the objects to put in front, the most important first (6 at most): mail:<id>, agenda:YYYY-MM-DD, event:<id>, session:<n>, card:<n>, task:<id>, project:<name>, alert:<n>; none gives the front back to the composer"`
	Why  string   `json:"why,omitempty" jsonschema:"why they are in front, a few words the user reads under them, in French"`
}

type workspaceWalkIn struct {
	Refs []string `json:"refs,omitempty" jsonschema:"the objects to walk the user through, in order (12 at most); none walks through what the workspace shows"`
}

func registerWorkspaceTools(s *mcp.Server, c *core) {
	tool(s, "workspace_show", "Put objects in front of the user's composed workspace now: it composes again at once with them as modules, and keeps them in front for an hour at most, or until you call it again (no refs: the composer decides again). For « montre-moi … », « mets … devant ». How the user wants the workspace in general is not this: remember it as a preference with subject workspace, which the composer reads every time.",
		func(ctx context.Context, in workspaceShowIn) (map[string]any, error) {
			if err := c.showOnWorkspace(in.Refs, in.Why, time.Now()); err != nil {
				return nil, err
			}
			if len(in.Refs) == 0 {
				return map[string]any{"result": "the composer decides what is in front again"}, nil
			}
			return map[string]any{"result": "shown", "note": "The workspace composes again with them in front, for an hour at most."}, nil
		})
	tool(s, "workspace_walkthrough", "Walk the user through objects on their composed workspace, one at a time, as they asked (« présente-moi », « fais-moi le tour »): the open workspace pages start at once. Without refs, it goes through what the workspace shows, the most important first.",
		func(ctx context.Context, in workspaceWalkIn) (map[string]any, error) {
			w, err := c.walkWorkspace(in.Refs, time.Now())
			if err != nil {
				return nil, err
			}
			return map[string]any{"result": "started", "refs": w.Refs, "note": "Only a workspace page open now shows it."}, nil
		})
}
