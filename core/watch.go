package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Watches: what wakes the brain when something happens, without a message
// of the user's. One tool, several sources:
//
//   - horaire: at times of day, in the user's zone (watch_schedule.go): the
//     morning brief, the Sunday review;
//   - retour: when the user comes back after an absence longer than a
//     threshold (user_state.go): the point made on their return;
//   - outil: eriphe-core calls a read-only catalog tool at an interval and
//     tests a condition on its result, without a model (watch_poll.go);
//   - alerte: a service calls the watch's own webhook (watch_alert.go).
//
// What outil and alerte bring is external content: the turn they wake is
// tainted. A watch may carry one action the user authorised in advance
// (watch_grant.go), which the policy lets through on its wake turn.
//
// eriphe-core fires them without a model (watch_tick.go): firing records a
// run and wakes the brain at its next idle with the watch's prompt
// (deliverWatch). The brain then speaks through notify, or stays silent
// ("[rien]"): each run keeps what it did, for the watch's history, since and
// the next brief. Nothing is imposed: the brain creates a watch only when
// the user asked for it in a clean turn; otherwise it is proposed, and the
// user approves it in Settings, where every watch is listed, paused, edited
// and deleted.

const watchSchema = `CREATE TABLE IF NOT EXISTS watches (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, source TEXT NOT NULL,
  spec TEXT NOT NULL DEFAULT '{}', prompt TEXT NOT NULL, brief INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL, why TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '',
  tainted TEXT NOT NULL DEFAULT '', by TEXT NOT NULL, next_at INTEGER, last_fired INTEGER,
  mark INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS watch_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, watch_id INTEGER NOT NULL, fired INTEGER NOT NULL,
  cause TEXT NOT NULL, due INTEGER, detail TEXT NOT NULL DEFAULT '', external INTEGER NOT NULL DEFAULT 0,
  woke INTEGER, outcome TEXT NOT NULL, notification INTEGER, action TEXT NOT NULL DEFAULT '', ended INTEGER);
CREATE INDEX IF NOT EXISTS watch_runs_watch ON watch_runs (watch_id, id);
CREATE INDEX IF NOT EXISTS watch_runs_fired ON watch_runs (fired);`

func init() {
	extraSchema = append(extraSchema, watchSchema)
	// Added with the sources outil and alerte: the poll's state, the webhook's token hash.
	extraColumns = append(extraColumns, `ALTER TABLE watches ADD COLUMN poll TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE watches ADD COLUMN hook TEXT NOT NULL DEFAULT ''`)
}

const (
	sourceSchedule = "horaire"
	sourceReturn   = "retour"
	sourcePoll     = "outil"
	sourceAlert    = "alerte"

	watchActive   = "active"
	watchPaused   = "paused"
	watchProposed = "proposed"

	// What a run became: waiting for the brain's idle, then woken, then
	// what the brain did of it.
	runPending  = "pending"
	runWoke     = "woke"
	runNotified = "notified" // it called notify
	runSaid     = "said"     // it wrote in the conversation only
	runSilent   = "silent"   // "[rien]"
	runActed    = "acted"    // it used the watch's authorised action
	runMissed   = "missed"   // too late to be worth waking for (eriphe-core was down)
	runDropped  = "dropped"  // the watch was paused or deleted before the wake

	watchNameMax   = 80
	watchPromptMax = 1000
	watchMax       = 50 // live watches at most

	defaultReturnAfter = 3 * time.Hour
	maxReturnAfter     = 7 * 24 * time.Hour
)

var (
	watchSources = []string{sourceSchedule, sourceReturn, sourcePoll, sourceAlert}
	// Their English names, which the brain may use too.
	watchSourceAliases = map[string]string{"schedule": sourceSchedule, "return": sourceReturn, "tool": sourcePoll,
		"poll": sourcePoll, "alert": sourceAlert, "webhook": sourceAlert}
)

// watchSpec is what a source needs, kept as JSON.
type watchSpec struct {
	Schedule string `json:"schedule,omitempty"`      // horaire
	After    int    `json:"after_minutes,omitempty"` // retour: the absence it takes, in minutes

	// outil: the read-only catalog tool called, its fixed arguments, every
	// how many minutes, and the condition tested on its result.
	Server string         `json:"server,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Every  int            `json:"every_minutes,omitempty"`
	Cond   *watchCond     `json:"cond,omitempty"`
	Judge  string         `json:"judge,omitempty"` // a yes/no question for the judge (judge.go), after the condition

	// alerte: the sender signs its calls (HMAC-SHA256, its secret in the vault).
	Signed bool `json:"signed,omitempty"`
}

// watch is one watch.
type watch struct {
	ID        int64
	Name      string
	Source    string
	Spec      watchSpec
	Prompt    string // what the brain is asked to do when it fires
	Brief     bool   // a brief: its wake writes last_brief (since.go)
	State     string
	Why       string
	SourceRef string
	Tainted   string // proposed in a turn that had read this
	By        string // brain or owner
	NextAt    time.Time
	LastFired time.Time
	Mark      int64 // retour: the return (unix) it last fired for
	Created   time.Time
	Poll      pollState // outil: what the last calls saw
	Hook      string    // alerte: the hash of its webhook's token; "" until one is made
}

// returnAfter is the absence a retour watch waits for.
func (w *watch) returnAfter() time.Duration {
	if w.Spec.After <= 0 {
		return defaultReturnAfter
	}
	return time.Duration(w.Spec.After) * time.Minute
}

// When says when it fires, for people and the brain.
func (w *watch) When() string {
	switch w.Source {
	case sourceSchedule:
		return w.Spec.Schedule
	case sourceReturn:
		return "> " + span(w.returnAfter())
	case sourcePoll:
		return fmt.Sprintf("%s.%s / %d min", w.Spec.Server, w.Spec.Tool, w.Spec.Every)
	case sourceAlert:
		return "webhook"
	}
	return ""
}

// check validates a watch and normalises it.
func (w *watch) check() error {
	w.Name = strings.Join(strings.Fields(w.Name), " ")
	w.Prompt = strings.TrimSpace(w.Prompt)
	w.Why = truncate(strings.TrimSpace(w.Why), notifyWhyMax)
	if s, ok := watchSourceAliases[w.Source]; ok {
		w.Source = s
	}
	switch {
	case w.Name == "" || utf8.RuneCountInString(w.Name) > watchNameMax:
		return fmt.Errorf("name is 1 to %d characters", watchNameMax)
	case w.Prompt == "" || utf8.RuneCountInString(w.Prompt) > watchPromptMax:
		return fmt.Errorf("prompt is 1 to %d characters: what to do when it fires", watchPromptMax)
	case !containsStr(watchSources, w.Source):
		return fmt.Errorf("source is %s", strings.Join(watchSources, " or "))
	}
	switch w.Source {
	case sourceSchedule:
		w.Spec = watchSpec{Schedule: strings.Join(strings.Fields(w.Spec.Schedule), " ")}
		if _, err := parseSchedule(w.Spec.Schedule); err != nil {
			return err
		}
	case sourceReturn:
		w.Spec = watchSpec{After: w.Spec.After}
		if w.Spec.After == 0 {
			w.Spec.After = int(defaultReturnAfter.Minutes())
		}
		if d := w.returnAfter(); d < awayAfter || d > maxReturnAfter {
			return fmt.Errorf("after is between %s and %s", span(awayAfter), span(maxReturnAfter))
		}
	case sourcePoll:
		return w.checkPoll()
	case sourceAlert:
		w.Spec = watchSpec{Signed: w.Spec.Signed}
	}
	return nil
}

// CondText says an outil watch's condition, for people and the brain.
func (w *watch) CondText() string {
	c := w.Spec.Cond
	if c == nil {
		return ""
	}
	path := c.Path
	if path == "" {
		path = "result"
	}
	switch c.Kind {
	case condCompare:
		return fmt.Sprintf("%s %s %s", path, c.Op, c.Value)
	case condChanged:
		return path + " changed"
	}
	return "call fails"
}

// checkWatch is check, and for an outil watch that eriphe-core may call its tool.
func (c *core) checkWatch(w *watch) error {
	if err := w.check(); err != nil {
		return err
	}
	if w.Source == sourcePoll {
		return c.checkPollTool(w)
	}
	return nil
}

// parseAfter reads an absence: 3h, 90m, 1h30, or minutes.
func parseAfter(s string) (int, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if s == "" {
		return 0, nil
	}
	if m, err := strconv.Atoi(s); err == nil {
		return m, nil
	}
	if h, m, ok := strings.Cut(s, "h"); ok && m != "" && !strings.HasSuffix(m, "m") {
		s = h + "h" + m + "m"
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a duration like 3h or 90m", s)
	}
	return int(d.Minutes()), nil
}

const watchCols = `id, name, source, spec, prompt, brief, state, why, source_ref, tainted, by, next_at, last_fired, mark, created,
	poll, hook`

func scanWatch(row interface{ Scan(...any) error }) (*watch, error) {
	var w watch
	var spec, poll string
	var next, last sql.NullInt64
	var created int64
	if err := row.Scan(&w.ID, &w.Name, &w.Source, &spec, &w.Prompt, &w.Brief, &w.State, &w.Why, &w.SourceRef, &w.Tainted,
		&w.By, &next, &last, &w.Mark, &created, &poll, &w.Hook); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(spec), &w.Spec)
	json.Unmarshal([]byte(poll), &w.Poll)
	w.NextAt, w.LastFired, w.Created = fromNull(next), fromNull(last), time.Unix(created, 0)
	return &w, nil
}

var errNoWatch = errors.New("no such watch")

func (c *core) watch(id int64) (*watch, error) {
	w, err := scanWatch(c.store.db.QueryRow(`SELECT `+watchCols+` FROM watches WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoWatch
	}
	return w, err
}

// watches lists the watches in these states (all if none), by id.
func (c *core) watches(states ...string) ([]*watch, error) {
	q := `SELECT ` + watchCols + ` FROM watches`
	var args []any
	if len(states) > 0 {
		q += ` WHERE state IN (` + placeholders(len(states)) + `)`
		for _, s := range states {
			args = append(args, s)
		}
	}
	rows, err := c.store.db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// schedule sets when an active watch of source horaire fires next, from now.
func (c *core) schedule(w *watch, now time.Time) {
	w.NextAt = time.Time{}
	if w.State == watchActive && w.Source == sourcePoll {
		w.NextAt = now // its first look, at the next tick
	}
	if w.State != watchActive || w.Source != sourceSchedule {
		return
	}
	if r, err := parseSchedule(w.Spec.Schedule); err == nil {
		w.NextAt = r.next(now, c.notifyZone())
	}
}

// addWatch keeps a checked watch.
func (c *core) addWatch(w *watch) (*watch, error) {
	if err := c.checkWatch(w); err != nil {
		return nil, err
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM watches`).Scan(&n)
	if n >= watchMax {
		return nil, fmt.Errorf("at most %d watches: delete one first", watchMax)
	}
	now := c.notifyTime()
	c.schedule(w, now)
	spec, _ := json.Marshal(w.Spec)
	res, err := c.store.db.Exec(`INSERT INTO watches (name, source, spec, prompt, brief, state, why, source_ref, tainted, by,
		next_at, mark, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.Name, w.Source, string(spec), w.Prompt, w.Brief, w.State, w.Why, w.SourceRef, w.Tainted, w.By,
		unixOrNil(w.NextAt), now.Unix(), now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	w.ID, _ = res.LastInsertId()
	w.Mark, w.Created = now.Unix(), now
	return w, nil
}

// saveWatch writes a changed watch back: its fields, its state, its next fire.
func (c *core) saveWatch(w *watch) error {
	if err := c.checkWatch(w); err != nil {
		return err
	}
	now := c.notifyTime()
	c.schedule(w, now)
	spec, _ := json.Marshal(w.Spec)
	w.Poll = pollState{} // a watch changed or resumed looks afresh
	res, err := c.store.db.Exec(`UPDATE watches SET name = ?, source = ?, spec = ?, prompt = ?, brief = ?, state = ?, why = ?,
		next_at = ?, updated = ?, poll = '' WHERE id = ?`, w.Name, w.Source, string(spec), w.Prompt, w.Brief, w.State, w.Why,
		unixOrNil(w.NextAt), now.Unix(), w.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoWatch
	}
	return nil
}

// setWatchState pauses, resumes or approves a watch. A watch that starts
// again does not fire for what happened while it was off.
func (c *core) setWatchState(id int64, state string) (*watch, error) {
	w, err := c.watch(id)
	if err != nil {
		return nil, err
	}
	if state == watchActive && w.State != watchActive && w.Source == sourceReturn {
		c.store.db.Exec(`UPDATE watches SET mark = ? WHERE id = ?`, c.notifyTime().Unix(), id)
	}
	w.State = state
	return w, c.saveWatch(w)
}

// deleteWatch removes a watch and its history.
func (c *core) deleteWatch(id int64) error {
	res, err := c.store.db.Exec(`DELETE FROM watches WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoWatch
	}
	c.store.db.Exec(`DELETE FROM watch_runs WHERE watch_id = ?`, id)
	c.revokeGrants(id, "watch deleted")
	return nil
}

// watchRun is one firing of a watch, and what came of it.
type watchRun struct {
	ID           int64
	WatchID      int64
	Fired        time.Time
	Cause        string    // time, late, return…
	Due          time.Time // when it was meant to fire
	Detail       string    // what fired it, written by code (or drawn from outside: External)
	External     bool
	Woke         time.Time
	Outcome      string
	Notification int64
	Action       string
	Ended        time.Time
}

const runCols = `id, watch_id, fired, cause, due, detail, external, woke, outcome, COALESCE(notification, 0), action, ended`

func scanRun(row interface{ Scan(...any) error }) (*watchRun, error) {
	var r watchRun
	var fired int64
	var due, woke, ended sql.NullInt64
	if err := row.Scan(&r.ID, &r.WatchID, &fired, &r.Cause, &due, &r.Detail, &r.External, &woke, &r.Outcome,
		&r.Notification, &r.Action, &ended); err != nil {
		return nil, err
	}
	r.Fired, r.Due, r.Woke, r.Ended = time.Unix(fired, 0), fromNull(due), fromNull(woke), fromNull(ended)
	return &r, nil
}

func (c *core) watchRuns(where string, args ...any) ([]*watchRun, error) {
	rows, err := c.store.db.Query(`SELECT `+runCols+` FROM watch_runs WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*watchRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// recentRuns are a watch's last runs, newest first.
func (c *core) recentRuns(id int64, n int) []*watchRun {
	runs, _ := c.watchRuns(`watch_id = ? ORDER BY id DESC LIMIT ?`, id, n)
	return runs
}

// watchHappenings are the watches' runs for since: each firing, and what
// the brain did of it once its turn ended. A run's detail drawn from
// outside is external: named, not quoted, unless asked for.
func watchHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	rows, err := c.store.db.Query(`SELECT r.watch_id, r.fired, r.cause, r.detail, r.external, r.outcome, COALESCE(r.ended, 0),
		COALESCE(r.notification, 0), r.action, COALESCE(w.name, '')
		FROM watch_runs r LEFT JOIN watches w ON w.id = r.watch_id
		WHERE r.fired >= ? OR r.ended >= ? ORDER BY r.id LIMIT ?`, from.Unix(), from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []happening
	for rows.Next() {
		var id, fired, ended, notif int64
		var cause, detail, outcome, action, name string
		var external bool
		if err := rows.Scan(&id, &fired, &cause, &detail, &external, &outcome, &ended, &notif, &action, &name); err != nil {
			return nil, err
		}
		ref, trust := fmt.Sprintf("watch:%d", id), feedOwn
		if external {
			trust = feedExternal
		}
		if fired >= from.Unix() && outcome != runMissed {
			out = append(out, happening{At: time.Unix(fired, 0), Kind: feedWatch, Event: "fired", Ref: ref,
				Args: []any{name, trKey("since.watch.cause." + cause)}, Text: feedText(detail), Trust: trust})
		}
		if ended == 0 || ended < from.Unix() {
			continue
		}
		h := happening{At: time.Unix(ended, 0), Kind: feedWatch, Event: outcome, Ref: ref, Args: []any{name}, Trust: feedOwn}
		switch outcome {
		case runNotified:
			h.Args = append(h.Args, notif)
		case runActed:
			h.Text = feedText(action)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
