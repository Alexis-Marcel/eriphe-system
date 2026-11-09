package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Work sessions, as decided in the study « Sessions de travail »
// (2026-11-09): real work has several steps, not only code (rebuild a
// service, find a brand name, make a video). Three notions for the user:
// an assistant has no end, a session has one (a goal, a plan of steps, a
// space, checkpoints), a task is one step.
//
//   - The brain opens a session with the user: a goal, what "done" means,
//     a first plan. The user approves the plan on a card: the steps as
//     planned start without asking again; a step added later asks its own
//     start card.
//   - The engine (session_engine.go) is code, without a model: it starts
//     each step once those it depends on are accepted, in parallel when
//     they may (never two writers on one repository branch or folder),
//     passes them what the steps before produced, asks the cards of the
//     checkpoints and holds the limits.
//   - The lead judges each step on its evidence: accept, retry or reject,
//     and amends the plan within the limits. The brain is the lead for
//     now; a session lead of its own, a temporary assistant, comes next.
//   - A report is only a claim: a step whose checks failed cannot be
//     accepted.
//
// Limits held by the code: 2 retries per step, 2 plan changes without
// asking the user, a budget of time per session; past them the engine
// stops and says so.

const sessionsSchema = `CREATE TABLE IF NOT EXISTS sessions (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	title     TEXT NOT NULL,
	goal      TEXT NOT NULL,
	done_when TEXT NOT NULL DEFAULT '',
	state     TEXT NOT NULL,
	held      TEXT NOT NULL DEFAULT '',
	budget    INTEGER NOT NULL DEFAULT 0,
	amends    INTEGER NOT NULL DEFAULT 0,
	summary   TEXT NOT NULL DEFAULT '',
	tainted   TEXT NOT NULL DEFAULT '',
	lead      TEXT NOT NULL DEFAULT '',
	told      TEXT NOT NULL DEFAULT '',
	spent     INTEGER NOT NULL DEFAULT 0,
	since     INTEGER NOT NULL DEFAULT 0,
	created   INTEGER NOT NULL,
	started   INTEGER NOT NULL DEFAULT 0,
	ended     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_steps (
	session  INTEGER NOT NULL,
	key      TEXT NOT NULL,
	n        INTEGER NOT NULL,
	title    TEXT NOT NULL,
	kind     TEXT NOT NULL,
	after    TEXT NOT NULL DEFAULT '',
	spec     TEXT NOT NULL DEFAULT '{}',
	status   TEXT NOT NULL,
	planned  INTEGER NOT NULL DEFAULT 0,
	task     TEXT NOT NULL DEFAULT '',
	tries    INTEGER NOT NULL DEFAULT 0,
	failed   INTEGER NOT NULL DEFAULT 0,
	evidence TEXT NOT NULL DEFAULT '',
	output   TEXT NOT NULL DEFAULT '',
	note     TEXT NOT NULL DEFAULT '',
	started  INTEGER NOT NULL DEFAULT 0,
	ended    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (session, key)
);
CREATE INDEX IF NOT EXISTS session_steps_task ON session_steps (task);
CREATE TABLE IF NOT EXISTS session_asks (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	session INTEGER NOT NULL,
	kind    TEXT NOT NULL,
	step    TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL DEFAULT '{}',
	why     TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT 'pending',
	created INTEGER NOT NULL,
	decided INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_log (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	session INTEGER NOT NULL,
	at      INTEGER NOT NULL,
	who     TEXT NOT NULL,
	text    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS session_log_session ON session_log (session, id);`

func init() { extraSchema = append(extraSchema, sessionsSchema) }

// A session's states.
const (
	sessProposed = "proposed" // its plan waits for the user's card
	sessRunning  = "running"
	sessPaused   = "paused" // nothing new starts; held says why when the code paused it
	sessDone     = "done"
	sessStopped  = "stopped"
	sessDeclined = "declined" // the user said no to its plan
)

// A step's kinds and statuses.
const (
	stepTask     = "task"     // an agent works on it, in a task
	stepChoice   = "choice"   // the user picks one of its options
	stepApproval = "approval" // the user says yes or no, on a card

	stepWaiting  = "waiting"  // for the steps it comes after, or its turn to write
	stepRunning  = "running"  // its task works, or its question waits for the user
	stepReported = "reported" // its agent says it is done: only a claim
	stepVerified = "verified" // and the checks the node ran all passed
	stepAccepted = "accepted" // the lead judged it done
	stepRejected = "rejected" // the lead judged it failed, or the user said no
	stepBlocked  = "blocked"  // its task failed, stopped or was lost
	stepDropped  = "dropped"  // taken out of the plan
)

// What a session asks the user, each on a card (cardSession).
const (
	askPlan     = "plan"     // the first plan
	askAmend    = "amend"    // a change of the plan past the limit
	askBudget   = "budget"   // more time, past the budget
	askApproval = "approval" // an approval step's question

	askPending  = "pending"
	askApproved = "approved"
	askRejected = "rejected"
	askGone     = "gone" // its session ended first
)

// The limits held by the code.
const (
	stepRetries    = 2  // retries of one step, by the lead
	planAmends     = 2  // changes of the plan without asking the user
	defaultBudget  = 60 // minutes, when the plan names none
	maxBudget      = 24 * 60
	maxSteps       = 20
	stepOutputMax  = 2000 // runes of a step's output passed to the steps after it
	sessionTextMax = 300
)

// cardSession is the card of what a session asks the user (session_asks).
const cardSession = "session"

func init() { proposalKinds = append(proposalKinds, cardSession) }

// Session is a work session as the store keeps it.
type Session struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	Goal     string    `json:"goal"`
	DoneWhen string    `json:"done_when,omitempty"`
	State    string    `json:"state"`
	Held     string    `json:"held,omitempty"`   // why the code paused it: budget
	Budget   int       `json:"budget"`           // minutes of running time it may take
	Amends   int       `json:"amends,omitempty"` // changes of its plan made without asking
	Summary  string    `json:"summary,omitempty"`
	Tainted  string    `json:"tainted,omitempty"` // why its plan may carry what someone else wrote
	Lead     string    `json:"lead,omitempty"`    // the assistant that leads it; empty: the brain
	Spent    int64     `json:"spent"`             // seconds it ran, until since
	since    int64     // when it last started running
	told     string    // what the lead was last told of the whole session: complete, stalled
	Created  time.Time `json:"created"`
	Started  time.Time `json:"started,omitzero"`
	Ended    time.Time `json:"ended,omitzero"`
}

// active tells whether it may still move.
func (s Session) active() bool {
	return s.State == sessProposed || s.State == sessRunning || s.State == sessPaused
}

// stepSpec is what a step is, as planned.
type stepSpec struct {
	Key       string        `json:"key" jsonschema:"a short id for the step, unique in the session: plan, api, review"`
	Title     string        `json:"title" jsonschema:"what the user reads for this step, in a few words in their language"`
	Kind      string        `json:"kind,omitempty" jsonschema:"task (an agent works on it, the default), choice (the user picks one of options) or approval (the user says yes or no on a card)"`
	After     []string      `json:"after,omitempty" jsonschema:"the keys of the steps it needs accepted first; their outputs are passed to it"`
	Prompt    string        `json:"prompt,omitempty" jsonschema:"task: what the agent must do, self-contained; the session's goal and the outputs of the steps it comes after are added"`
	Agent     string        `json:"agent,omitempty" jsonschema:"task: agent from list_agents; empty for the default one"`
	Resources []ResourceRef `json:"resources,omitempty" jsonschema:"task: what it is lent, as run_task's resources"`
	Contract  *taskContract `json:"contract,omitempty" jsonschema:"task: as run_task's contract; its checks are the step's evidence"`
	Question  string        `json:"question,omitempty" jsonschema:"choice or approval: what the user is asked"`
	Options   []string      `json:"options,omitempty" jsonschema:"choice: what the user picks from, 2 to 6"`
}

// Step is a step of a session, as the store keeps it.
type Step struct {
	Session  int64    `json:"-"`
	stepSpec          // as planned
	N        int      `json:"n"`
	Status   string   `json:"status"`
	Planned  bool     `json:"planned"` // in the plan the user approved
	Task     string   `json:"task,omitempty"`
	Tries    int      `json:"tries,omitempty"`  // retries asked by the lead
	Failed   bool     `json:"failed,omitempty"` // a check failed, or the receipt claims what the checks deny
	Evidence string   `json:"evidence,omitempty"`
	Output   string   `json:"output,omitempty"` // what it produced, for the steps after it
	Note     string   `json:"note,omitempty"`   // the lead's last word on it
	Started  int64    `json:"started,omitempty"`
	Ended    int64    `json:"ended,omitempty"`
	scopes   []string // what it writes
}

// done tells whether the step is over, one way or another.
func (s Step) done() bool {
	return s.Status == stepAccepted || s.Status == stepRejected || s.Status == stepDropped
}

// writeScopes are what a step writes: a repository branch, a folder. Two
// steps writing one never run at once.
func writeScopes(rs []ResourceRef) []string {
	var out []string
	for _, r := range rs {
		if r.Access != accessWrite {
			continue
		}
		switch r.Kind {
		case resRepo:
			out = append(out, "repo:"+strings.ToLower(r.Ref)+"@"+r.Branch)
		case resFolder, resDriveFolder:
			out = append(out, r.key())
		}
	}
	return out
}

// normalize checks a step as planned, and fills its defaults.
func (s *stepSpec) normalize() error {
	s.Key, s.Title = strings.ToLower(strings.TrimSpace(s.Key)), oneLine(s.Title)
	if !stepKeyRE.MatchString(s.Key) {
		return fmt.Errorf("step key %q: 1 to 24 lowercase letters, digits and dashes", s.Key)
	}
	if s.Title == "" {
		return fmt.Errorf("step %s: a title is needed", s.Key)
	}
	s.Title = truncate(s.Title, taskTitleMax)
	if s.Kind == "" {
		s.Kind = stepTask
	}
	switch s.Kind {
	case stepTask:
		if strings.TrimSpace(s.Prompt) == "" {
			return fmt.Errorf("step %s: a task step needs its prompt", s.Key)
		}
		if s.Question != "" || len(s.Options) > 0 {
			return fmt.Errorf("step %s: a task step takes no question nor options", s.Key)
		}
		if s.Contract != nil {
			if err := s.Contract.normalize(); err != nil {
				return fmt.Errorf("step %s: %w", s.Key, err)
			}
		}
		rs, err := normalizeResources(runRequest{Resources: s.Resources})
		if err != nil {
			return fmt.Errorf("step %s: %w", s.Key, err)
		}
		s.Resources = rs
	case stepChoice, stepApproval:
		s.Question = strings.TrimSpace(s.Question)
		if s.Question == "" {
			return fmt.Errorf("step %s: a %s step needs its question", s.Key, s.Kind)
		}
		if s.Prompt != "" || s.Agent != "" || len(s.Resources) > 0 || s.Contract != nil {
			return fmt.Errorf("step %s: a %s step runs no task: no prompt, agent, resources nor contract", s.Key, s.Kind)
		}
		var opts []string
		for _, o := range s.Options {
			if o = oneLine(o); o != "" && !slices.Contains(opts, o) {
				opts = append(opts, truncate(o, 80))
			}
		}
		s.Options = opts
		if s.Kind == stepChoice && (len(opts) < 2 || len(opts) > 6) {
			return fmt.Errorf("step %s: a choice gives 2 to 6 options", s.Key)
		}
		if s.Kind == stepApproval && len(opts) > 0 {
			return fmt.Errorf("step %s: an approval is yes or no: no options", s.Key)
		}
	default:
		return fmt.Errorf("step %s: kind %q: task, choice or approval", s.Key, s.Kind)
	}
	var after []string
	for _, a := range s.After {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" && !slices.Contains(after, a) {
			after = append(after, a)
		}
	}
	s.After = after
	return nil
}

// checkPlan checks steps together with those already in the session: keys
// unique, what each comes after exists, and no cycle.
func checkPlan(have []Step, add []stepSpec) error {
	known := map[string][]string{}
	for _, s := range have {
		if s.Status != stepDropped {
			known[s.Key] = s.After
		}
	}
	if len(known)+len(add) > maxSteps {
		return fmt.Errorf("at most %d steps in a session", maxSteps)
	}
	for i := range add {
		if err := add[i].normalize(); err != nil {
			return err
		}
		if _, dup := known[add[i].Key]; dup {
			return fmt.Errorf("step key %q is used twice", add[i].Key)
		}
		known[add[i].Key] = add[i].After
	}
	for k, after := range known {
		for _, a := range after {
			if _, ok := known[a]; !ok {
				return fmt.Errorf("step %s comes after %q, which is not a step", k, a)
			}
			if a == k {
				return fmt.Errorf("step %s comes after itself", k)
			}
		}
	}
	// No cycle: peel the steps whose predecessors are all peeled.
	peeled := map[string]bool{}
	for len(peeled) < len(known) {
		moved := false
		for k, after := range known {
			if peeled[k] {
				continue
			}
			if !slices.ContainsFunc(after, func(a string) bool { return !peeled[a] }) {
				peeled[k], moved = true, true
			}
		}
		if !moved {
			return errors.New("the steps' order has a cycle: a step cannot come after itself, even through others")
		}
	}
	return nil
}

// elapsed is how long it ran, paused times left out.
func (s Session) elapsed(now time.Time) time.Duration {
	d := time.Duration(s.Spent) * time.Second
	if s.State == sessRunning && s.since > 0 {
		d += now.Sub(time.Unix(s.since, 0))
	}
	return d
}

// overBudget tells whether it ran past its budget.
func (s Session) overBudget(now time.Time) bool {
	return s.Budget > 0 && s.elapsed(now) >= time.Duration(s.Budget)*time.Minute
}

var stepKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// The store.

const sessionCols = `id, title, goal, done_when, state, held, budget, amends, summary, tainted, lead, told, spent, since, created, started, ended`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	var created, started, ended int64
	err := row.Scan(&s.ID, &s.Title, &s.Goal, &s.DoneWhen, &s.State, &s.Held, &s.Budget, &s.Amends, &s.Summary, &s.Tainted, &s.Lead,
		&s.told, &s.Spent, &s.since, &created, &started, &ended)
	s.Created, s.Started, s.Ended = time.Unix(created, 0), unixOrZero(started), unixOrZero(ended)
	return s, err
}

var errNoSession = errors.New("no such session")

func (c *core) session(id int64) (Session, error) {
	s, err := scanSession(c.store.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, errNoSession
	}
	return s, err
}

// sessions lists the sessions, the active ones first, then the latest.
func (c *core) sessions(activeOnly bool, limit int) ([]Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions`
	if activeOnly {
		q += ` WHERE state IN ('proposed', 'running', 'paused')`
	}
	q += ` ORDER BY state NOT IN ('proposed', 'running', 'paused'), id DESC LIMIT ?`
	rows, err := c.store.db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const stepCols = `session, key, n, title, kind, after, spec, status, planned, task, tries, failed, evidence, output, note, started, ended`

func scanStep(row interface{ Scan(...any) error }) (Step, error) {
	var s Step
	var after, spec string
	err := row.Scan(&s.Session, &s.Key, &s.N, &s.Title, &s.Kind, &after, &spec, &s.Status, &s.Planned, &s.Task, &s.Tries, &s.Failed,
		&s.Evidence, &s.Output, &s.Note, &s.Started, &s.Ended)
	if err != nil {
		return s, err
	}
	key, title, kind := s.Key, s.Title, s.Kind
	json.Unmarshal([]byte(spec), &s.stepSpec)
	s.Key, s.Title, s.Kind = key, title, kind
	s.After = nil
	if after != "" {
		s.After = strings.Split(after, ",")
	}
	s.scopes = writeScopes(s.Resources)
	return s, nil
}

func (c *core) steps(session int64) ([]Step, error) {
	rows, err := c.store.db.Query(`SELECT `+stepCols+` FROM session_steps WHERE session = ? ORDER BY n`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		s, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (c *core) step(session int64, key string) (Step, error) {
	s, err := scanStep(c.store.db.QueryRow(`SELECT `+stepCols+` FROM session_steps WHERE session = ? AND key = ?`, session, key))
	if errors.Is(err, sql.ErrNoRows) {
		return s, fmt.Errorf("session %d has no step %q", session, key)
	}
	return s, err
}

// stepOfTask is the step whose task id is, if any.
func (c *core) stepOfTask(id string) (Step, bool) {
	s, err := scanStep(c.store.db.QueryRow(`SELECT `+stepCols+` FROM session_steps WHERE task = ? AND task != ''`, id))
	return s, err == nil
}

// insertSteps adds steps at the end of a session's plan.
func (c *core) insertSteps(session int64, specs []stepSpec, planned bool) error {
	var n int
	c.store.db.QueryRow(`SELECT COALESCE(MAX(n), 0) FROM session_steps WHERE session = ?`, session).Scan(&n)
	for _, sp := range specs {
		n++
		raw, _ := json.Marshal(sp)
		if _, err := c.store.db.Exec(`INSERT INTO session_steps (session, key, n, title, kind, after, spec, status, planned) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			session, sp.Key, n, sp.Title, sp.Kind, strings.Join(sp.After, ","), string(raw), stepWaiting, planned); err != nil {
			return err
		}
	}
	return nil
}

// setStep changes a step's columns.
func (c *core) setStep(session int64, key string, fields map[string]any) error {
	var sets []string
	var args []any
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		sets = append(sets, k+" = ?")
		args = append(args, fields[k])
	}
	args = append(args, session, key)
	_, err := c.store.db.Exec(`UPDATE session_steps SET `+strings.Join(sets, ", ")+` WHERE session = ? AND key = ?`, args...)
	return err
}

func (c *core) setSession(id int64, fields map[string]any) error {
	var sets []string
	var args []any
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		sets = append(sets, k+" = ?")
		args = append(args, fields[k])
	}
	args = append(args, id)
	_, err := c.store.db.Exec(`UPDATE sessions SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// SessionLine is a line of a session's log: what happened, and who did it.
type SessionLine struct {
	At   time.Time `json:"at"`
	Who  string    `json:"who"` // engine, lead, user, brain
	Text string    `json:"text"`
}

func (c *core) logSession(id int64, who, text string) {
	c.store.db.Exec(`INSERT INTO session_log (session, at, who, text) VALUES (?, ?, ?, ?)`, id, time.Now().Unix(), who, truncate(text, 600))
}

// sessionLog is its last n lines, oldest first.
func (c *core) sessionLog(id int64, n int) []SessionLine {
	rows, err := c.store.db.Query(`SELECT at, who, text FROM (SELECT id, at, who, text FROM session_log WHERE session = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, id, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SessionLine
	for rows.Next() {
		var l SessionLine
		var at int64
		if rows.Scan(&at, &l.Who, &l.Text) == nil {
			l.At = time.Unix(at, 0)
			out = append(out, l)
		}
	}
	return out
}

// sessionAsk is what a session asks the user.
type sessionAsk struct {
	ID      int64
	Session int64
	Kind    string
	Step    string
	Payload json.RawMessage
	Why     string
	State   string
}

func (c *core) sessionAsk(id int64) (sessionAsk, error) {
	var a sessionAsk
	var payload string
	err := c.store.db.QueryRow(`SELECT id, session, kind, step, payload, why, state FROM session_asks WHERE id = ?`, id).
		Scan(&a.ID, &a.Session, &a.Kind, &a.Step, &payload, &a.Why, &a.State)
	a.Payload = json.RawMessage(payload)
	return a, err
}

// ask asks the user something on a card, once per thing: an ask of the
// same kind and step still waiting is kept.
func (c *core) ask(session int64, kind, step, why string, payload any) (int64, error) {
	var id int64
	err := c.store.db.QueryRow(`SELECT id FROM session_asks WHERE session = ? AND kind = ? AND step = ? AND state = ?`, session, kind, step, askPending).Scan(&id)
	if err == nil {
		return id, nil
	}
	raw, _ := json.Marshal(payload)
	res, err := c.store.db.Exec(`INSERT INTO session_asks (session, kind, step, payload, why, created) VALUES (?, ?, ?, ?, ?, ?)`,
		session, kind, step, string(raw), truncate(why, sessionTextMax), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	c.cardProposed(cardSession, id)
	c.syncCards() // also when no turn of the brain's takes it
	return id, nil
}

// proposedSessionAsks are the asks that wait, for the cards.
func (c *core) proposedSessionAsks() []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM session_asks WHERE state = ? ORDER BY id`, askPending)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// closeAsks withdraws what a session still asks: it ended.
func (c *core) closeAsks(session int64) {
	c.store.db.Exec(`UPDATE session_asks SET state = ?, decided = ? WHERE session = ? AND state = ?`, askGone, time.Now().Unix(), session, askPending)
}
