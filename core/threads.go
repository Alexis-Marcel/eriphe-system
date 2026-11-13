package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Threads (#464): a thread is one matter followed to its real goal (a
// pull request, an appointment, a brand name to find). An assistant has
// no end, a thread has one, a task is one step. It replaces the work
// session of the study « Sessions de travail » (2026-11-09).
//
// A thread is light by default: held by the brain, opened at once with no
// card and no budget, found again by its subject, closed only on its goal
// and reopened when its subject comes back. Its state is open, waiting,
// done or dropped.
//
// Led is an option for long autonomous work: a plan the user approves, a
// lead, a budget. Its engine's phase (proposed, running, paused, done,
// stopped, declined) moves its state:
//
//   - The brain opens a led thread with the user: a goal, what "done"
//     means, a first plan. The user approves the plan on a card: the steps
//     as planned start without asking again; a step added later asks its
//     own start card.
//   - The engine (thread_engine.go) is code, without a model: it starts
//     each step once those it depends on are accepted, in parallel when
//     they may (never two writers on one repository branch or folder),
//     passes them what the steps before produced, asks the cards of the
//     checkpoints and holds the limits.
//   - The lead judges each step on its evidence: accept, retry or reject,
//     and amends the plan within the limits. The brain is the lead for
//     now; a thread lead of its own, a temporary assistant, comes next.
//   - A report is only a claim: a step whose checks failed cannot be
//     accepted.
//
// Limits held by the code: 2 retries per step, 2 plan changes without
// asking the user, a budget of time per thread; past them the engine
// stops and says so.

const threadsSchema = `CREATE TABLE IF NOT EXISTS threads (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	title     TEXT NOT NULL,
	goal      TEXT NOT NULL,
	done_when TEXT NOT NULL DEFAULT '',
	mode      TEXT NOT NULL DEFAULT 'light', -- light: held by the brain; led: a plan, a lead, a budget
	subject   TEXT NOT NULL DEFAULT '',      -- the matter it follows, a ref: project:site, pr:owner/repo#12
	state     TEXT NOT NULL,                 -- open, waiting, done, dropped
	phase     TEXT NOT NULL DEFAULT '',      -- a led thread's engine: proposed, running, paused, done, stopped, declined
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
CREATE INDEX IF NOT EXISTS threads_subject ON threads (subject);
CREATE TABLE IF NOT EXISTS thread_steps (
	thread   INTEGER NOT NULL,
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
	card     TEXT NOT NULL DEFAULT '',    -- a call step's card: its proposal ref (access:<id>)
	due      INTEGER NOT NULL DEFAULT 0,  -- a deadline step's date
	tainted  TEXT NOT NULL DEFAULT '',    -- why its output may carry what someone else wrote
	PRIMARY KEY (thread, key)
);
CREATE INDEX IF NOT EXISTS thread_steps_task ON thread_steps (task);
CREATE TABLE IF NOT EXISTS thread_asks (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	thread  INTEGER NOT NULL,
	kind    TEXT NOT NULL,
	step    TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL DEFAULT '{}',
	why     TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT 'pending',
	created INTEGER NOT NULL,
	decided INTEGER NOT NULL DEFAULT 0
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
	schemaFixes = append(schemaFixes, sessionsToThreads, threadStepColumns) // in this order (thread_steps.go)
}

// A thread's modes.
const (
	modeLight = "light" // held by the brain: no plan card, no budget
	modeLed   = "led"   // a plan the user approves, a lead, a budget
)

// A thread's states, whatever its mode.
const (
	threadOpen    = "open"
	threadWaiting = "waiting" // on the user: its plan, more time; or paused
	threadDone    = "done"    // its goal reached
	threadDropped = "dropped" // stopped, or its plan declined
)

// A led thread's phases, its engine's; each moves its state (stateOf).
const (
	phaseProposed = "proposed" // its plan waits for the user's card
	phaseRunning  = "running"
	phasePaused   = "paused" // nothing new starts; held says why when the code paused it
	phaseDone     = "done"
	phaseStopped  = "stopped"
	phaseDeclined = "declined" // the user said no to its plan
)

// stateOf is the state a led thread's phase gives it.
func stateOf(phase string) string {
	switch phase {
	case phaseRunning:
		return threadOpen
	case phaseProposed, phasePaused:
		return threadWaiting
	case phaseDone:
		return threadDone
	default:
		return threadDropped
	}
}

// A step's kinds and statuses.
const (
	stepTask     = "task"     // an agent works on it, in a task
	stepChoice   = "choice"   // the user picks one of its options
	stepApproval = "approval" // the user says yes or no, on a card
	stepCall     = "call"     // a tool call: its access card is its yes, then its run is followed (thread_steps.go)
	stepUser     = "user"     // what only the user does: they say « C'est fait », or the holder sees it done
	stepCheck    = "check"    // a task or a read call that verifies: its outcome settles it
	stepDeadline = "deadline" // a date, or a linked intention: when due, the thread wakes

	stepWaiting  = "waiting"  // for the steps it comes after, or its turn to write
	stepRunning  = "running"  // its task works, or its question waits for the user
	stepReported = "reported" // its agent says it is done: only a claim
	stepVerified = "verified" // and the checks the node ran all passed
	stepAccepted = "accepted" // the lead judged it done
	stepRejected = "rejected" // the lead judged it failed, or the user said no
	stepBlocked  = "blocked"  // its task failed, stopped or was lost
	stepDropped  = "dropped"  // taken out of the plan
)

// What a thread asks the user, each on a card (cardThread).
const (
	askPlan     = "plan"     // the first plan
	askAmend    = "amend"    // a change of the plan past the limit
	askBudget   = "budget"   // more time, past the budget
	askApproval = "approval" // an approval step's question

	askPending  = "pending"
	askApproved = "approved"
	askRejected = "rejected"
	askGone     = "gone" // its thread ended first
)

// The limits held by the code.
const (
	stepRetries   = 2  // retries of one step, by the lead
	planAmends    = 2  // changes of the plan without asking the user
	defaultBudget = 60 // minutes, when the plan names none
	maxBudget     = 24 * 60
	maxSteps      = 20
	stepOutputMax = 2000 // runes of a step's output passed to the steps after it
	threadTextMax = 300
)

// cardThread is the card of what a thread asks the user (thread_asks).
const cardThread = "thread"

func init() { proposalKinds = append(proposalKinds, cardThread) }

// Thread is a work thread as the store keeps it.
type Thread struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	Goal     string    `json:"goal"`
	DoneWhen string    `json:"done_when,omitempty"`
	Mode     string    `json:"mode"`              // light or led
	Subject  string    `json:"subject,omitempty"` // the matter it follows, a ref
	State    string    `json:"state"`             // open, waiting, done, dropped
	Phase    string    `json:"phase,omitempty"`   // a led thread's engine
	Held     string    `json:"held,omitempty"`    // why the code paused it: budget
	Budget   int       `json:"budget"`            // minutes of running time it may take
	Amends   int       `json:"amends,omitempty"`  // changes of its plan made without asking
	Summary  string    `json:"summary,omitempty"`
	Tainted  string    `json:"tainted,omitempty"` // why its plan may carry what someone else wrote
	Lead     string    `json:"lead,omitempty"`    // the assistant that leads it; empty: the brain
	Spent    int64     `json:"spent"`             // seconds it ran, until since
	since    int64     // when it last started running
	told     string    // what the lead was last told of the whole thread: complete, stalled
	Created  time.Time `json:"created"`
	Started  time.Time `json:"started,omitzero"`
	Ended    time.Time `json:"ended,omitzero"`
}

// active tells whether it may still move: open or waiting.
func (s Thread) active() bool {
	return s.State == threadOpen || s.State == threadWaiting
}

// led tells whether it has a plan, a lead and a budget.
func (s Thread) led() bool { return s.Mode == modeLed }

// Look is what its page shows of where it stands: a led thread's phase,
// a light one's state.
func (s Thread) Look() string {
	if s.led() {
		return s.Phase
	}
	return s.State
}

// stepSpec is what a step is, as planned.
type stepSpec struct {
	Key       string         `json:"key" jsonschema:"a short id for the step, unique in the thread: plan, api, review"`
	Title     string         `json:"title" jsonschema:"what the user reads for this step, in a few words in their language"`
	Kind      string         `json:"kind,omitempty" jsonschema:"task (an agent works on it, the default), call (a tool call: its card is the user's yes, then its run is followed), user (what only the user does: they answer it is done), choice (the user picks one of options), approval (the user says yes or no on a card), check (a task or a read call that verifies: its outcome settles it) or deadline (a date: the thread wakes when it comes)"`
	After     []string       `json:"after,omitempty" jsonschema:"the keys of the steps it needs accepted first; their outputs are passed to it"`
	Prompt    string         `json:"prompt,omitempty" jsonschema:"task: what the agent must do, self-contained; the thread's goal and the outputs of the steps it comes after are added"`
	Agent     string         `json:"agent,omitempty" jsonschema:"task: agent from list_agents; empty for the default one"`
	Resources []ResourceRef  `json:"resources,omitempty" jsonschema:"task: what it is lent, as run_task's resources"`
	Contract  *taskContract  `json:"contract,omitempty" jsonschema:"task: as run_task's contract; its checks are the step's evidence"`
	Question  string         `json:"question,omitempty" jsonschema:"choice or approval: what the user is asked; user: what only the user does, in their language"`
	Options   []string       `json:"options,omitempty" jsonschema:"choice: what the user picks from, 2 to 6"`
	Tool      string         `json:"tool,omitempty" jsonschema:"call, or a check that reads: the tool to call, one of yours, by its full name"`
	Input     map[string]any `json:"input,omitempty" jsonschema:"call or check: the tool's input, exactly as it takes it"`
	Expect    string         `json:"expect,omitempty" jsonschema:"check by a call: words its result must contain for the check to hold; without, its running is enough"`
	Due       string         `json:"due,omitempty" jsonschema:"deadline: its date, YYYY-MM-DD or YYYY-MM-DDTHH:MM (local time)"`
	Intention int64          `json:"intention,omitempty" jsonschema:"deadline: the intention it follows (link_thread links one): due when it is"`
}

// Step is a step of a thread, as the store keeps it.
type Step struct {
	Thread   int64    `json:"-"`
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
	Card     string   `json:"card,omitempty"`    // a call step's card: its proposal ref
	DueAt    int64    `json:"due_at,omitempty"`  // a deadline step's date
	Tainted  string   `json:"tainted,omitempty"` // why its output may carry what someone else wrote
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
		case resFolder, resPlace:
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
	if s.Key == finalStep {
		return fmt.Errorf("step key %q is the final check's", s.Key)
	}
	if s.Title == "" {
		return fmt.Errorf("step %s: a title is needed", s.Key)
	}
	s.Title = truncate(s.Title, taskTitleMax)
	if s.Kind == "" {
		s.Kind = stepTask
	}
	if err := s.normalizeKind(); err != nil {
		return err
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

// checkPlan checks steps together with those already in the thread: keys
// unique, what each comes after exists, and no cycle.
func checkPlan(have []Step, add []stepSpec) error {
	known := map[string][]string{}
	for _, s := range have {
		if s.Status != stepDropped {
			known[s.Key] = s.After
		}
	}
	if len(known)+len(add) > maxSteps {
		return fmt.Errorf("at most %d steps in a thread", maxSteps)
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
func (s Thread) elapsed(now time.Time) time.Duration {
	d := time.Duration(s.Spent) * time.Second
	if s.Phase == phaseRunning && s.since > 0 {
		d += now.Sub(time.Unix(s.since, 0))
	}
	return d
}

// overBudget tells whether it ran past its budget.
func (s Thread) overBudget(now time.Time) bool {
	return s.Budget > 0 && s.elapsed(now) >= time.Duration(s.Budget)*time.Minute
}

var stepKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// The store.

const threadCols = `id, title, goal, done_when, mode, subject, state, phase, held, budget, amends, summary, tainted, lead, told, spent, since, created, started, ended`

func scanThread(row interface{ Scan(...any) error }) (Thread, error) {
	var s Thread
	var created, started, ended int64
	err := row.Scan(&s.ID, &s.Title, &s.Goal, &s.DoneWhen, &s.Mode, &s.Subject, &s.State, &s.Phase, &s.Held, &s.Budget, &s.Amends, &s.Summary, &s.Tainted, &s.Lead,
		&s.told, &s.Spent, &s.since, &created, &started, &ended)
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

// findThreads finds threads by their subject (the same ref, any case) or
// by words of their title, goal, subject or summary, active ones first,
// then the latest; ended ones too, to reopen one.
func (c *core) findThreads(subject, text string, all bool, limit int) ([]Thread, error) {
	var conds []string
	var args []any
	if subject = strings.TrimSpace(subject); subject != "" {
		conds = append(conds, `lower(subject) = lower(?)`)
		args = append(args, subject)
	}
	for _, w := range strings.Fields(text) {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(w)) + "%"
		conds = append(conds, `lower(title || ' ' || goal || ' ' || subject || ' ' || summary) LIKE ? ESCAPE '\'`)
		args = append(args, like)
	}
	if len(conds) == 0 && !all {
		conds = append(conds, activeThreads)
	}
	if len(conds) == 0 {
		conds = append(conds, `1`)
	}
	return c.threadsWhere(strings.Join(conds, ` AND `)+` ORDER BY state NOT IN ('open', 'waiting'), id DESC LIMIT ?`, append(args, limit)...)
}

const stepCols = `thread, key, n, title, kind, after, spec, status, planned, task, tries, failed, evidence, output, note, started, ended, card, due, tainted`

func scanStep(row interface{ Scan(...any) error }) (Step, error) {
	var s Step
	var after, spec string
	err := row.Scan(&s.Thread, &s.Key, &s.N, &s.Title, &s.Kind, &after, &spec, &s.Status, &s.Planned, &s.Task, &s.Tries, &s.Failed,
		&s.Evidence, &s.Output, &s.Note, &s.Started, &s.Ended, &s.Card, &s.DueAt, &s.Tainted)
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

func (c *core) steps(thread int64) ([]Step, error) {
	rows, err := c.store.db.Query(`SELECT `+stepCols+` FROM thread_steps WHERE thread = ? ORDER BY n`, thread)
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

func (c *core) step(thread int64, key string) (Step, error) {
	s, err := scanStep(c.store.db.QueryRow(`SELECT `+stepCols+` FROM thread_steps WHERE thread = ? AND key = ?`, thread, key))
	if errors.Is(err, sql.ErrNoRows) {
		return s, fmt.Errorf("thread %d has no step %q", thread, key)
	}
	return s, err
}

// stepOfTask is the step whose task id is, if any.
func (c *core) stepOfTask(id string) (Step, bool) {
	s, err := scanStep(c.store.db.QueryRow(`SELECT `+stepCols+` FROM thread_steps WHERE task = ? AND task != ''`, id))
	return s, err == nil
}

// insertSteps adds steps at the end of a thread's plan.
func (c *core) insertSteps(thread int64, specs []stepSpec, planned bool) error {
	var n int
	c.store.db.QueryRow(`SELECT COALESCE(MAX(n), 0) FROM thread_steps WHERE thread = ?`, thread).Scan(&n)
	for _, sp := range specs {
		n++
		raw, _ := json.Marshal(sp)
		if _, err := c.store.db.Exec(`INSERT INTO thread_steps (thread, key, n, title, kind, after, spec, status, planned, due) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			thread, sp.Key, n, sp.Title, sp.Kind, strings.Join(sp.After, ","), string(raw), stepWaiting, planned, sp.dueAt()); err != nil {
			return err
		}
	}
	c.threadMoved(thread)
	return nil
}

// setStep changes a step's columns.
func (c *core) setStep(thread int64, key string, fields map[string]any) error {
	var sets []string
	var args []any
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		sets = append(sets, k+" = ?")
		args = append(args, fields[k])
	}
	args = append(args, thread, key)
	_, err := c.store.db.Exec(`UPDATE thread_steps SET `+strings.Join(sets, ", ")+` WHERE thread = ? AND key = ?`, args...)
	c.threadMoved(thread)
	return err
}

// setThread changes a thread's columns; a led thread's phase moves its
// state with it.
func (c *core) setThread(id int64, fields map[string]any) error {
	if p, ok := fields["phase"].(string); ok && p != "" {
		fields["state"] = stateOf(p)
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

// threadAsk is what a thread asks the user.
type threadAsk struct {
	ID      int64
	Thread  int64
	Kind    string
	Step    string
	Payload json.RawMessage
	Why     string
	State   string
}

func (c *core) threadAsk(id int64) (threadAsk, error) {
	var a threadAsk
	var payload string
	err := c.store.db.QueryRow(`SELECT id, thread, kind, step, payload, why, state FROM thread_asks WHERE id = ?`, id).
		Scan(&a.ID, &a.Thread, &a.Kind, &a.Step, &payload, &a.Why, &a.State)
	a.Payload = json.RawMessage(payload)
	return a, err
}

// ask asks the user something on a card, once per thing: an ask of the
// same kind and step still waiting is kept. by is who asks it: the
// brain's card goes with its message of this turn; anyone else's (the
// engine, a lead, a skill) is shown at once (cardNow).
func (c *core) ask(thread int64, kind, step, why string, payload any, by string) (int64, error) {
	var id int64
	err := c.store.db.QueryRow(`SELECT id FROM thread_asks WHERE thread = ? AND kind = ? AND step = ? AND state = ?`, thread, kind, step, askPending).Scan(&id)
	if err == nil {
		return id, nil
	}
	raw, _ := json.Marshal(payload)
	res, err := c.store.db.Exec(`INSERT INTO thread_asks (thread, kind, step, payload, why, created) VALUES (?, ?, ?, ?, ?, ?)`,
		thread, kind, step, string(raw), truncate(why, threadTextMax), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	if by == byBrain {
		c.cardProposed(cardThread, id)
		return id, nil
	}
	title := ""
	if s, err := c.thread(thread); err == nil {
		title = s.Title
	}
	c.cardNow(cardThread, id, fmt.Sprintf(tr(c.userLang(), "thread.ask.message"), title), "thread:"+strconv.FormatInt(thread, 10))
	return id, nil
}

// proposedThreadAsks are the asks that wait, for the cards.
func (c *core) proposedThreadAsks() []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM thread_asks WHERE state = ? ORDER BY id`, askPending)
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

// closeAsks withdraws what a thread still asks: it ended.
func (c *core) closeAsks(thread int64) {
	c.store.db.Exec(`UPDATE thread_asks SET state = ?, decided = ? WHERE thread = ? AND state = ?`, askGone, time.Now().Unix(), thread, askPending)
}
