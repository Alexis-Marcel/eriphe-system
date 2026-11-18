package main

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Threads (#464): a thread is one matter followed to its real goal (a
// pull request, an appointment, a brand name to find). An assistant has
// no end, a thread has one, a task is one step. It replaces the work
// session of the study « Sessions de travail » (2026-11-09).
//
// A thread is held by the brain, opened at once with no card, found again
// by its alias, closed only on its goal and reopened when its matter comes
// back. Its state is open, done or dropped. Work cut in steps runs as the
// steps of a node (node_steps.go); the graph's nodes replace threads
// (#625).

const threadsSchema = `CREATE TABLE IF NOT EXISTS threads (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	title     TEXT NOT NULL,
	goal      TEXT NOT NULL,
	state     TEXT NOT NULL,                 -- open, done, dropped
	summary   TEXT NOT NULL DEFAULT '',
	tainted   TEXT NOT NULL DEFAULT '',
	created   INTEGER NOT NULL,
	started   INTEGER NOT NULL DEFAULT 0,
	ended     INTEGER NOT NULL DEFAULT 0,
	summarized INTEGER NOT NULL DEFAULT 0, -- when its summary was last written
	seen      INTEGER NOT NULL DEFAULT 0,  -- the user's visit before the last one: its surface tells what changed since
	visited   INTEGER NOT NULL DEFAULT 0,  -- when the user last opened its surface
	ends      TEXT NOT NULL DEFAULT 'goal', -- how it ends: goal, standing, day
	tidied    INTEGER NOT NULL DEFAULT 0,  -- when the user tidied it away (« Ranger »), ended
	parent    INTEGER NOT NULL DEFAULT 0   -- the thread it is a child of, 0 for a top thread
);
CREATE TABLE IF NOT EXISTS thread_log (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	thread  INTEGER NOT NULL,
	at      INTEGER NOT NULL,
	who     TEXT NOT NULL,
	text    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS thread_log_thread ON thread_log (thread, id);`

func init() {
	extraSchema = append(extraSchema, threadsSchema)
	// A threads table made before ends, tidied and parent has none of them.
	schemaFixes = append(schemaFixes, func(db *sql.DB) error {
		if err := addColumn(db, "threads", "ends", "TEXT NOT NULL DEFAULT 'goal'"); err != nil {
			return err
		}
		// A thread's alias is in the names now (names.go): the column is
		// step 8's to convert, the one-live-thread index the names' key.
		for _, q := range []string{`DROP INDEX IF EXISTS threads_live_subject`, `DROP INDEX IF EXISTS threads_subject`} {
			if _, err := db.Exec(q); err != nil {
				return err
			}
		}
		if err := addColumn(db, "threads", "tidied", "INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		return addColumn(db, "threads", "parent", "INTEGER NOT NULL DEFAULT 0")
	})
}

// How a thread ends (#571). Its family is never shown to the user: it only
// changes its life.
const (
	endsGoal     = "goal"     // it ends on its goal; ended, it stays until « Ranger » or midnight
	endsStanding = "standing" // what the user manages continuously: never closed, stopped nor hidden
	endsDay      = "day"      // a view for the day: it ends by itself at the user's midnight
)

var threadEnds = []string{endsGoal, endsStanding, endsDay}

// A thread's states, whatever its mode.
const (
	threadOpen    = "open"
	threadWaiting = "waiting" // kept for the threads table's queries: no thread waits any more
	threadDone    = "done"    // its goal reached
	threadDropped = "dropped" // stopped, or its plan declined
)

// Thread is a work thread as the store keeps it.
type Thread struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	Goal       string    `json:"goal"`
	Alias      string    `json:"alias,omitempty"` // its latest key in the names (names.go): pr:owner/repo#12
	State      string    `json:"state"`           // open, done, dropped
	Summary    string    `json:"summary,omitempty"`
	Tainted    string    `json:"tainted,omitempty"` // why it may carry what someone else wrote
	Ends       string    `json:"ends"`              // goal, standing or day
	Parent     int64     `json:"parent,omitempty"`  // the thread it is a child of (thread_children.go)
	summarized int64     // when its summary was last written
	seen       int64     // the user's visit before the last one, 0 if none
	visited    int64     // the user's last visit, 0 if none: what changed since is new
	tidied     int64     // when the user tidied it away, ended
	Created    time.Time `json:"created"`
	Started    time.Time `json:"started,omitzero"`
	Ended      time.Time `json:"ended,omitzero"`
}

// active tells whether it may still move: open or waiting.
func (s Thread) active() bool {
	return s.State == threadOpen || s.State == threadWaiting
}

// standing tells whether it is what the user manages continuously: it
// never ends.
func (s Thread) standing() bool { return s.Ends == endsStanding }

// Look is what its page shows of where it stands: its state.
func (s Thread) Look() string { return s.State }

// The store.

const threadCols = `id, title, goal, ` + threadAliasCol + `, state, summary, tainted, created, started, ended, summarized, seen, visited, ends, tidied, parent`

// threadAliasCol is a thread's latest key in the names, "" for none.
const threadAliasCol = `COALESCE((SELECT name FROM names WHERE target = 'thread:' || threads.id AND instr(name, ':') > 0
	AND name NOT LIKE 'thread:%' ORDER BY seen DESC, name LIMIT 1), '')`

// threadOfAlias is the condition of a thread a key is an alias of.
const threadOfAlias = `id IN (SELECT CAST(substr(target, 8) AS INTEGER) FROM names WHERE name = ? AND target LIKE 'thread:%')`

func scanThread(row interface{ Scan(...any) error }) (Thread, error) {
	var s Thread
	var created, started, ended int64
	err := row.Scan(&s.ID, &s.Title, &s.Goal, &s.Alias, &s.State, &s.Summary, &s.Tainted,
		&created, &started, &ended, &s.summarized, &s.seen, &s.visited, &s.Ends, &s.tidied, &s.Parent)
	s.Created, s.Started, s.Ended = time.Unix(created, 0), unixOrZero(started), unixOrZero(ended)
	return s, err
}

var errNoThread = errors.New("no such thread")

func (c *core) thread(id int64) (Thread, error) {
	s, err := scanThread(c.store.db.QueryRow(`SELECT `+threadCols+` FROM threads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, errNoThread
	}
	return s, err
}

// threads lists the threads, the active ones first, then the latest.
func (c *core) threads(activeOnly bool, limit int) ([]Thread, error) {
	where := `1`
	if activeOnly {
		where = activeThreads
	}
	return c.threadsWhere(where+` ORDER BY state NOT IN ('open', 'waiting'), id DESC LIMIT ?`, limit)
}

// activeThreads is the condition of a thread open or waiting.
const activeThreads = `state IN ('open', 'waiting')`

func (c *core) threadsWhere(where string, args ...any) ([]Thread, error) {
	rows, err := c.store.db.Query(`SELECT `+threadCols+` FROM threads WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		s, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// findThreads finds threads by a key or a name (names.go), or by words
// of their title, goal, names or summary, active ones first, then the
// latest; ended ones too, to reopen one.
func (c *core) findThreads(alias, text string, all bool, limit int) ([]Thread, error) {
	var conds []string
	var args []any
	if alias != "" {
		key, err := c.aboutLookup(alias)
		if err != nil {
			return nil, fmt.Errorf("alias: %w", err)
		}
		id, ok := threadOfAbout(key)
		if !ok {
			return nil, nil
		}
		conds = append(conds, `id = ?`)
		args = append(args, id)
	}
	for _, w := range strings.Fields(text) {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(w)) + "%"
		conds = append(conds, `(lower(title || ' ' || goal || ' ' || summary) LIKE ? ESCAPE '\'
			OR id IN (SELECT CAST(substr(target, 8) AS INTEGER) FROM names WHERE target LIKE 'thread:%' AND lower(name) LIKE ? ESCAPE '\'))`)
		args = append(args, like, like)
	}
	if len(conds) == 0 && !all {
		conds = append(conds, activeThreads)
	}
	if len(conds) == 0 {
		conds = append(conds, `1`)
	}
	return c.threadsWhere(strings.Join(conds, ` AND `)+` ORDER BY state NOT IN ('open', 'waiting'), id DESC LIMIT ?`, append(args, limit)...)
}

// setThread changes a thread's columns.
func (c *core) setThread(id int64, fields map[string]any) error {
	if _, ok := fields["summary"]; ok {
		fields["summarized"] = time.Now().Unix() // the summary written again is a fact (thread_header.go)
	}
	var sets []string
	var args []any
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		sets = append(sets, k+" = ?")
		args = append(args, fields[k])
	}
	args = append(args, id)
	_, err := c.store.db.Exec(`UPDATE threads SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	c.threadMoved(id)
	return err
}

// ThreadLine is a line of a thread's log: what happened, and who did it.
type ThreadLine struct {
	At   time.Time `json:"at"`
	Who  string    `json:"who"` // engine, lead, user, brain
	Text string    `json:"text"`
}

func (c *core) logThread(id int64, who, text string) {
	c.store.db.Exec(`INSERT INTO thread_log (thread, at, who, text) VALUES (?, ?, ?, ?)`, id, time.Now().Unix(), who, truncate(text, 600))
	c.threadMoved(id)
}

// threadLog is its last n lines, oldest first.
func (c *core) threadLog(id int64, n int) []ThreadLine {
	rows, err := c.store.db.Query(`SELECT at, who, text FROM (SELECT id, at, who, text FROM thread_log WHERE thread = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, id, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ThreadLine
	for rows.Next() {
		var l ThreadLine
		var at int64
		if rows.Scan(&at, &l.Who, &l.Text) == nil {
			l.At = time.Unix(at, 0)
			out = append(out, l)
		}
	}
	return out
}
