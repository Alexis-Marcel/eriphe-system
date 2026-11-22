package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Steps (#630): a piece of work cut in steps is a node whose children are
// the steps. Each step is a child node with the steps it comes after, an
// agent's task that works in it, and a proof. The engine is code, without
// a model:
//
//   - It starts every step whose predecessors are accepted, in parallel
//     when they may (never two writers on one repository branch or
//     folder), passing them what the steps before produced.
//   - It accepts a proven step on its own: its task's receipt says done,
//     every check of its contract passed (a named test or command is one
//     of them), and nothing it claims is denied by what the device saw.
//     The decision goes into the parent's trace.
//   - The brain is woken only for an exception, once: a failed proof, a
//     blocked step (its task could not start, failed, stopped or was
//     lost), the budget spent, the final check not holding. It decides
//     with node_settle: accept, retry, drop or replan.
//   - Once every step is accepted, a fresh agent checks the work against
//     the parent's goal; its verdict goes into the parent's trace.
//
// Start cards follow the rights unchanged: a step starts as a task the
// brain starts, its cards asked at launch, tainted by its opener's taint
// and by what the steps before it handed it. One bounded addition: a
// step opened by a skill's run (or by a step of one) borrows only what
// the skill declares (its tools and repositories, approved with it). It
// starts clean, its Free writes holding, only when the run was clean
// (no external event), its opener was not tainted, it borrows nothing
// the skill does not declare, and nothing external was handed to it.
// Anything else asks its start card as any task's.
//
// What the engine knows is in core.db (node_steps, node_works), never
// read back from a node's file: an author writes the file, the rights
// and the proof are code's. It survives a restart: the tick picks up
// what moved while eriphe-core was down.

const nodeStepsSchema = `CREATE TABLE IF NOT EXISTS node_steps (
	node    TEXT PRIMARY KEY,           -- the step's node
	parent  TEXT NOT NULL,              -- the work's node
	n       INTEGER NOT NULL,           -- its place in the work
	key     TEXT NOT NULL,              -- its key in the plan it came with
	after   TEXT NOT NULL DEFAULT '',   -- the step nodes it comes after, comma separated
	spec    TEXT NOT NULL,              -- what it runs, JSON (stepSpec)
	status  TEXT NOT NULL,              -- waiting, running, accepted, failed, blocked, dropped
	task    TEXT NOT NULL DEFAULT '',   -- its task, the last one
	tries   INTEGER NOT NULL DEFAULT 0, -- retries asked by node_settle
	failed  INTEGER NOT NULL DEFAULT 0, -- a check failed: it is never accepted
	output  TEXT NOT NULL DEFAULT '',   -- its report, for the steps after it
	note    TEXT NOT NULL DEFAULT '',   -- why it failed or was blocked, a retry's note
	opener  TEXT NOT NULL,              -- who opened it: brain, task:<id>, skill:<name>
	tainted TEXT NOT NULL DEFAULT '',   -- why its opener may have been talked into it
	skill   TEXT NOT NULL DEFAULT '',   -- the skill whose run opened it: name@sha
	clean   INTEGER NOT NULL DEFAULT 0, -- a clean skill's run opened it, borrowing only what the skill declares
	started INTEGER NOT NULL DEFAULT 0,
	ended   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS node_steps_parent ON node_steps (parent, n);
CREATE INDEX IF NOT EXISTS node_steps_task ON node_steps (task);
CREATE TABLE IF NOT EXISTS node_works (
	parent  TEXT PRIMARY KEY,           -- the work's node
	state   TEXT NOT NULL,              -- running, held, checking, failed, done, stopped
	held    TEXT NOT NULL DEFAULT '',   -- why it is held: budget
	budget  INTEGER NOT NULL,           -- minutes of running time
	spent   INTEGER NOT NULL DEFAULT 0, -- seconds it ran, until since
	since   INTEGER NOT NULL DEFAULT 0, -- when it last started running
	final   TEXT NOT NULL DEFAULT '',   -- the final check's task, the last one
	checks  INTEGER NOT NULL DEFAULT 0, -- final checks run
	tainted TEXT NOT NULL DEFAULT ''    -- why the final check's verdict may carry someone else's words
);`

func init() { extraSchema = append(extraSchema, nodeStepsSchema) }

// A step's statuses.
const (
	stepWaiting  = "waiting"  // for the steps it comes after, or its turn to write
	stepRunning  = "running"  // its task works
	stepAccepted = "accepted" // proven, or accepted by the brain
	stepFailed   = "failed"   // its proof failed: the brain decides
	stepBlocked  = "blocked"  // its task could not start, failed, stopped or was lost: the brain decides
	stepDropped  = "dropped"  // taken out of the work
)

// A work's states.
const (
	workRunning  = "running"
	workHeld     = "held"     // nothing new starts: its budget is spent
	workChecking = "checking" // every step is accepted: a fresh agent checks the goal
	workFailed   = "failed"   // the final check does not hold: the brain decides
	workDone     = "done"     // every step accepted, the goal checked
	workStopped  = "stopped"  // node_settle dropped it
)

// Outcomes of node_settle.
const (
	settleAccept = "accept"
	settleRetry  = "retry"
	settleDrop   = "drop"
	settleReplan = "replan"
)

// Limits held by code.
const (
	stepRetries      = 2  // retries of one step
	finalChecks      = 2  // final checks of one work
	defaultBudget    = 60 // minutes of a work, when its opener names none
	maxBudget        = 24 * 60
	maxSteps         = 20  // unfinished steps of a work at once
	maxStepsEver     = 200 // steps of a work over its life
	stepOutputMax    = 2000
	stepNoteMax      = 300
	stepKeyMaxLength = 24
)

var stepKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// stepMu makes the engine's changes one at a time: a step's end, a
// settle and a tick at the same moment move a work once each.
var stepMu sync.Mutex

// stepSpec is a step as its opener gives it.
type stepSpec struct {
	Key       string        `json:"key" jsonschema:"a short id for the step, unique in this plan: style, plan, review"`
	Title     string        `json:"title" jsonschema:"what the user reads for this step, a few words in their language"`
	After     []string      `json:"after,omitempty" jsonschema:"the keys of the steps of this plan (or the node ids of steps already in the work) it needs accepted first; their results are passed to it"`
	Prompt    string        `json:"prompt" jsonschema:"what the agent must do, self-contained; the work's goal and the results of the steps it comes after are added"`
	Agent     string        `json:"agent,omitempty" jsonschema:"agent from list_agents; empty for the default one"`
	Resources []ResourceRef `json:"resources,omitempty" jsonschema:"what it is lent, as run_task's resources"`
	Contract  *taskContract `json:"contract,omitempty" jsonschema:"as run_task's contract: its checks (a test, a command) are the step's proof with its receipt"`
	Device    string        `json:"device,omitempty" jsonschema:"device name; empty lets the scheduler choose"`
}

// Step is a step as the engine keeps it.
type Step struct {
	Node    string   `json:"node"`
	Parent  string   `json:"parent"`
	N       int      `json:"n"`
	After   []string `json:"after,omitempty"`
	Spec    stepSpec `json:"spec"`
	Status  string   `json:"status"`
	Task    string   `json:"task,omitempty"`
	Tries   int      `json:"tries,omitempty"`
	Failed  bool     `json:"failed,omitempty"`
	Output  string   `json:"output,omitempty"`
	Note    string   `json:"note,omitempty"`
	Opener  string   `json:"opener"`
	Tainted string   `json:"tainted,omitempty"`
	Skill   string   `json:"skill,omitempty"`
	Clean   bool     `json:"clean,omitempty"`
	Started int64    `json:"started,omitempty"`
	Ended   int64    `json:"ended,omitempty"`
}

// over tells whether it ended one way or another: nothing more comes of it.
func (s Step) over() bool { return s.Status == stepAccepted || s.Status == stepDropped }

// scopes are what it writes.
func (s Step) scopes() []string { return writeScopes(s.Spec.Resources) }

// work is a work as the engine keeps it.
type work struct {
	Parent  string
	State   string
	Held    string
	Budget  int
	Spent   int64
	Since   int64
	Final   string
	Checks  int
	Tainted string
}

// elapsed is how long it ran, held times left out.
func (w work) elapsed(now time.Time) time.Duration {
	d := time.Duration(w.Spent) * time.Second
	if w.State == workRunning && w.Since > 0 {
		d += now.Sub(time.Unix(w.Since, 0))
	}
	return d
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

// appendNew appends what xs does not hold yet.
func appendNew(xs []string, add ...string) []string {
	for _, a := range add {
		if a != "" && !slices.Contains(xs, a) {
			xs = append(xs, a)
		}
	}
	return xs
}

// The store.

const stepCols = `node, parent, n, after, spec, status, task, tries, failed, output, note, opener, tainted, skill, clean, started, ended`

func scanStep(row interface{ Scan(...any) error }) (Step, error) {
	var s Step
	var after, spec string
	err := row.Scan(&s.Node, &s.Parent, &s.N, &after, &spec, &s.Status, &s.Task, &s.Tries, &s.Failed, &s.Output, &s.Note,
		&s.Opener, &s.Tainted, &s.Skill, &s.Clean, &s.Started, &s.Ended)
	if err != nil {
		return s, err
	}
	json.Unmarshal([]byte(spec), &s.Spec)
	if after != "" {
		s.After = strings.Split(after, ",")
	}
	return s, nil
}

func (c *core) workSteps(parent string) ([]Step, error) {
	rows, err := c.store.db.Query(`SELECT `+stepCols+` FROM node_steps WHERE parent = ? ORDER BY n`, parent)
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

// nodeStep is the step a node is; false when it is none.
func (c *core) nodeStep(node string) (Step, bool) {
	s, err := scanStep(c.store.db.QueryRow(`SELECT `+stepCols+` FROM node_steps WHERE node = ?`, node))
	return s, err == nil
}

// stepOfTask is the step whose last task is id; false when none.
func (c *core) stepOfTask(id string) (Step, bool) {
	s, err := scanStep(c.store.db.QueryRow(`SELECT `+stepCols+` FROM node_steps WHERE task = ? AND task != ''`, id))
	return s, err == nil
}

// setStep changes a step's columns.
func (c *core) setStep(node string, fields map[string]any) {
	var sets []string
	var args []any
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		sets = append(sets, k+" = ?")
		args = append(args, fields[k])
	}
	if _, err := c.store.db.Exec(`UPDATE node_steps SET `+strings.Join(sets, ", ")+` WHERE node = ?`, append(args, node)...); err != nil {
		log.Printf("step %s: %v", node, err)
	}
}

func (c *core) work(parent string) (work, bool) {
	var w work
	err := c.store.db.QueryRow(`SELECT parent, state, held, budget, spent, since, final, checks, tainted FROM node_works WHERE parent = ?`, parent).
		Scan(&w.Parent, &w.State, &w.Held, &w.Budget, &w.Spent, &w.Since, &w.Final, &w.Checks, &w.Tainted)
	return w, err == nil
}

// workOfFinal is the work whose final check task id is.
func (c *core) workOfFinal(id string) (work, bool) {
	var parent string
	if c.store.db.QueryRow(`SELECT parent FROM node_works WHERE final = ? AND final != ''`, id).Scan(&parent) != nil {
		return work{}, false
	}
	return c.work(parent)
}

// setWork changes a work's columns.
func (c *core) setWork(parent string, fields map[string]any) {
	var sets []string
	var args []any
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		sets = append(sets, k+" = ?")
		args = append(args, fields[k])
	}
	if _, err := c.store.db.Exec(`UPDATE node_works SET `+strings.Join(sets, ", ")+` WHERE parent = ?`, append(args, parent)...); err != nil {
		log.Printf("work %s: %v", parent, err)
	}
}

// stepState puts a step's node in the state its status gives it: running
// while its task works, done once accepted or dropped, idle otherwise.
func (c *core) stepState(node, status string) {
	if c.nodes == nil {
		return
	}
	state := nodestore.Idle
	switch status {
	case stepRunning:
		state = nodestore.Running
	case stepAccepted, stepDropped:
		state = nodestore.Done
	}
	if err := c.nodes.SetState(node, state); err != nil {
		log.Printf("step %s: state: %v", node, err)
	}
}

// traceWork writes a line in the work's node's trace, as Eriphe.
func (c *core) traceWork(parent, line, tainted string) {
	if c.nodes == nil {
		return
	}
	if tainted != "" {
		c.markNodeExternal(parent, "eriphe") // before the line: never words without the mark
	}
	if err := c.nodes.AppendTrace(parent, "eriphe", line); err != nil {
		log.Printf("work %s: trace: %v", parent, err)
	}
}

// Opening steps.

// stepOpener is who opens steps: the brain, or a task (a skill's run, a
// step, another).
type stepOpener struct {
	author  string   // brain, task:<id>, skill:<name>
	task    string   // the task that opens them, "" for the brain
	tainted []string // why what it says may be someone else's
}

// skillBorrow is what a step opened by a skill's run may borrow: the
// skill's declared tools and repositories, approved with it; run is
// whether the run was clean (no external event).
type skillBorrow struct {
	skill, sha   string
	tools, repos []string
	clean        bool
}

// borrowOf is what the opener's steps may borrow of a skill: its run's,
// or the step's of a run it is. nil when it is neither.
func (c *core) borrowOf(task string) *skillBorrow {
	if task == "" {
		return nil
	}
	if r := c.runOfTask(task); r != nil {
		b := &skillBorrow{skill: r.Skill, sha: r.SHA, clean: !r.External}
		if m, ok := c.skillManifestOf(r.SHA); ok && m.Trigger != nil {
			b.tools, b.repos = m.Trigger.Tools, m.Trigger.Repos
		}
		return b
	}
	if st, ok := c.stepOfTask(task); ok && st.Skill != "" {
		name, sha, _ := strings.Cut(st.Skill, "@")
		b := &skillBorrow{skill: name, sha: sha, clean: st.Clean}
		if m, ok := c.skillManifestOf(sha); ok && m.Trigger != nil {
			b.tools, b.repos = m.Trigger.Tools, m.Trigger.Repos
		}
		return b
	}
	return nil
}

// declares tells whether the skill declares every resource: its tools by
// server, its repositories by name. A folder, a place or an access is
// never a skill's to lend.
func (b *skillBorrow) declares(rs []ResourceRef) bool {
	for _, r := range rs {
		switch r.Kind {
		case resTool:
			server, _, _ := strings.Cut(r.Ref, ".")
			if !slices.Contains(b.tools, server) {
				return false
			}
		case resRepo:
			repo := repoOf(r.Ref)
			if repo == "" || !slices.ContainsFunc(b.repos, func(d string) bool { return strings.EqualFold(d, repo) }) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// normalize checks a step as given, and fills its defaults.
func (s *stepSpec) normalize() error {
	s.Key, s.Title = strings.ToLower(strings.TrimSpace(s.Key)), oneLine(s.Title)
	switch {
	case !stepKeyRE.MatchString(s.Key):
		return fmt.Errorf("step key %q: 1 to %d lowercase letters, digits and dashes", s.Key, stepKeyMaxLength)
	case s.Title == "":
		return fmt.Errorf("step %s: a title is needed", s.Key)
	case strings.TrimSpace(s.Prompt) == "":
		return fmt.Errorf("step %s: a prompt is needed", s.Key)
	}
	s.Title = truncate(s.Title, taskTitleMax)
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
	var after []string
	for _, a := range s.After {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" && !slices.Contains(after, a) {
			after = append(after, a)
		}
	}
	s.After = after
	return nil
}

// checkPlan checks steps given together, with the work's steps already
// there (have): keys unique in the plan, what each comes after is a step
// of the plan or of the work, no cycle, and the limits. It gives the
// plan in an order where each step comes after those it needs.
func checkPlan(have []Step, add []stepSpec) ([]stepSpec, error) {
	open := 0
	known := map[string]bool{}
	for _, s := range have {
		if !s.over() {
			open++
		}
		if s.Status != stepDropped {
			known[s.Node] = true
		}
	}
	if len(add) == 0 {
		return nil, errors.New("a plan of one step at least is needed")
	}
	if open+len(add) > maxSteps {
		return nil, fmt.Errorf("at most %d unfinished steps in a work: let some finish first", maxSteps)
	}
	if len(have)+len(add) > maxStepsEver {
		return nil, fmt.Errorf("at most %d steps in a work over its life: open another node for what comes next", maxStepsEver)
	}
	keys := map[string]int{}
	for i := range add {
		if err := add[i].normalize(); err != nil {
			return nil, err
		}
		if _, dup := keys[add[i].Key]; dup {
			return nil, fmt.Errorf("step key %q is used twice", add[i].Key)
		}
		keys[add[i].Key] = i
	}
	for _, s := range add {
		for _, a := range s.After {
			if _, ok := keys[a]; !ok && !known[a] {
				return nil, fmt.Errorf("step %s comes after %q, which is no step of this plan nor of the work", s.Key, a)
			}
			if a == s.Key {
				return nil, fmt.Errorf("step %s comes after itself", s.Key)
			}
		}
	}
	// In order: peel the steps whose predecessors of the plan are peeled.
	var out []stepSpec
	peeled := map[string]bool{}
	for len(out) < len(add) {
		moved := false
		for _, s := range add {
			if peeled[s.Key] {
				continue
			}
			if !slices.ContainsFunc(s.After, func(a string) bool { _, inPlan := keys[a]; return inPlan && !peeled[a] }) {
				peeled[s.Key], moved = true, true
				out = append(out, s)
			}
		}
		if !moved {
			return nil, errors.New("the steps' order has a cycle: a step cannot come after itself, even through others")
		}
	}
	return out, nil
}

// checkStepRequest refuses, before any node is made, what the step's
// task would be refused at its start: an agent, resources or a contract
// that are not, what a tainted opener may not lend.
func (c *core) checkStepRequest(st stepSpec, tainted []string) error {
	r := runRequest{Prompt: st.Prompt, Agent: st.Agent, Resources: st.Resources, Contract: st.Contract, Device: st.Device}
	if err := c.checkTaskRequest(r); err != nil {
		return fmt.Errorf("step %s: %w", st.Key, err)
	}
	if err := c.taintedLent(r, tainted); err != nil {
		return fmt.Errorf("step %s: %w", st.Key, err)
	}
	return nil
}

// checkTaskRequest refuses before anything is made what startTask would
// refuse of the task itself: no prompt, a contract or an agent that is
// none, what it is lent that cannot be.
func (c *core) checkTaskRequest(r runRequest) error {
	if strings.TrimSpace(r.Prompt) == "" {
		return errors.New("prompt is required")
	}
	if _, _, err := c.conf().agent(r.Agent); err != nil {
		return err
	}
	lent, err := c.resolveRequest(r) // resources.go
	if err != nil {
		return err
	}
	if r.Contract != nil {
		k := *r.Contract
		if err := k.normalize(); err != nil { // contract.go
			return err
		}
		return k.checkLent(lent.Repo != nil, folderNames(lent.Folders))
	}
	return nil
}

// checkSteps checks a plan of steps for a new work, as openSteps would,
// before anything is made.
func (c *core) checkSteps(specs []stepSpec, budget int, tainted []string) error {
	if budget < 0 || budget > maxBudget {
		return fmt.Errorf("budget_minutes: 1 to %d", maxBudget)
	}
	plan, err := checkPlan(nil, slices.Clone(specs))
	if err != nil {
		return err
	}
	for _, sp := range plan {
		if err := c.checkStepRequest(sp, tainted); err != nil {
			return err
		}
	}
	return nil
}

// openSteps opens steps under a work's node (parent): one child node a
// step, in an order where each comes after those it needs, and starts
// those that may. budget is the work's minutes, 0 for the default (or
// what it has). It gives the steps' nodes by key.
func (c *core) openSteps(ctx context.Context, parent string, specs []stepSpec, budget int, by stepOpener) (map[string]string, error) {
	stepMu.Lock()
	defer stepMu.Unlock()
	return c.openStepsLocked(ctx, parent, specs, budget, by)
}

func (c *core) openStepsLocked(ctx context.Context, parent string, specs []stepSpec, budget int, by stepOpener) (map[string]string, error) {
	if c.nodes == nil {
		return nil, errors.New("the graph is not open on this eriphe-core")
	}
	if budget < 0 || budget > maxBudget {
		return nil, fmt.Errorf("budget_minutes: 1 to %d", maxBudget)
	}
	sum, err := c.nodes.Get(parent)
	if err != nil {
		return nil, fmt.Errorf("node %s: %w", parent, err)
	}
	if !sum.Archived.IsZero() {
		return nil, fmt.Errorf("node %s is archived", parent)
	}
	have, err := c.workSteps(parent)
	if err != nil {
		return nil, err
	}
	specs, err = checkPlan(have, specs)
	if err != nil {
		return nil, err
	}
	for _, sp := range specs {
		if err := c.checkStepRequest(sp, by.tainted); err != nil {
			return nil, err
		}
	}
	// Every step is checked and given its node's id before anything is
	// made; then its row, all in one transaction; then its node. A node
	// that cannot be made takes the rows of the steps not made with it:
	// no row without its node, no node without its row.
	borrow := c.borrowOf(by.task)
	ids := map[string]string{}
	taken := map[string]bool{}
	for _, sp := range specs {
		id := c.nodes.NewID()
		for taken[id] {
			id = c.nodes.NewID()
		}
		taken[id] = true
		ids[sp.Key] = id
	}
	type planned struct {
		key, after string
		nw         nodestore.New
		spec       []byte
		skill      string
		clean      bool
	}
	var plan []planned
	for _, sp := range specs {
		var after []string
		for _, a := range sp.After {
			if id, ok := ids[a]; ok {
				after = append(after, id)
			} else {
				after = append(after, a) // a step node of the work
			}
		}
		goal := ""
		if sp.Contract != nil {
			goal = sp.Contract.Objective
		}
		if goal == "" {
			goal, _, _ = strings.Cut(strings.TrimSpace(sp.Prompt), "\n")
		}
		nw := nodestore.New{ID: ids[sp.Key], Title: sp.Title, Parent: parent, Goal: truncate(oneLine(goal), taskNodeGoalMax),
			External: len(by.tainted) > 0, OpenedBy: c.workOrigin(parent, by.task), Author: by.author}
		if err := c.nodes.Check(nw); err != nil {
			return nil, fmt.Errorf("step %s: %w", sp.Key, err)
		}
		var skill string
		clean := false
		if borrow != nil {
			skill = borrow.skill + "@" + borrow.sha
			clean = borrow.clean && len(by.tainted) == 0 && borrow.declares(sp.Resources)
		}
		raw, _ := json.Marshal(sp)
		plan = append(plan, planned{key: sp.Key, after: strings.Join(after, ","), nw: nw, spec: raw, skill: skill, clean: clean})
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i, p := range plan {
		if _, err := tx.Exec(`INSERT INTO node_steps (node, parent, n, key, after, spec, status, opener, tainted, skill, clean) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.nw.ID, parent, len(have)+i+1, p.key, p.after, string(p.spec), stepWaiting, by.author, strings.Join(by.tainted, ", "), p.skill, p.clean); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i, p := range plan {
		if err := stepCreated(c.nodes.Create(p.nw)); err != nil {
			for _, q := range plan[i:] {
				c.store.db.Exec(`DELETE FROM node_steps WHERE node = ?`, q.nw.ID)
				delete(ids, q.key)
			}
			return ids, fmt.Errorf("step %s: %w", p.key, err)
		}
	}
	now := time.Now().Unix()
	w, ok := c.work(parent)
	switch {
	case !ok:
		if budget == 0 {
			budget = defaultBudget
		}
		c.store.db.Exec(`INSERT INTO node_works (parent, state, budget, since) VALUES (?, ?, ?, ?)`, parent, workRunning, budget, now)
	case w.State != workRunning && w.State != workHeld:
		// New steps for a work that ended, or whose goal did not hold: it runs again.
		fields := map[string]any{"state": workRunning, "since": now, "final": "", "checks": 0}
		if budget > 0 {
			fields["budget"] = budget
		}
		c.setWork(parent, fields)
	case budget > 0:
		c.setWork(parent, map[string]any{"budget": budget})
	}
	keys := slices.Sorted(maps.Keys(ids))
	c.traceWork(parent, fmt.Sprintf("Étapes ouvertes par %s : %s.", by.author, strings.Join(keys, ", ")), strings.Join(by.tainted, ", "))
	c.advanceLocked(ctx, parent)
	return ids, nil
}

// stepCreated is a step node's creation, as openSteps sees it: a test
// makes it fail.
var stepCreated = func(_ *nodestore.Doc, err error) error { return err }

// The engine.

// advanceWork starts what may start in a work.
func (c *core) advanceWork(ctx context.Context, parent string) {
	stepMu.Lock()
	defer stepMu.Unlock()
	c.advanceLocked(ctx, parent)
}

func (c *core) advanceLocked(ctx context.Context, parent string) {
	w, ok := c.work(parent)
	if !ok || w.State != workRunning {
		return
	}
	if w.Budget > 0 && w.elapsed(time.Now()) >= time.Duration(w.Budget)*time.Minute {
		c.holdForBudget(w)
		return
	}
	steps, err := c.workSteps(parent)
	if err != nil {
		return
	}
	status := map[string]string{}
	var writing []string
	for _, st := range steps {
		status[st.Node] = st.Status
		if st.Status == stepRunning || st.Status == stepFailed { // a failed step's task waits for its retry
			writing = append(writing, st.scopes()...)
		}
	}
	busy := false // something runs, waits or waits for the brain
	for i, st := range steps {
		switch st.Status {
		case stepRunning, stepFailed, stepBlocked:
			busy = true
			continue
		case stepWaiting:
		default:
			continue
		}
		busy = true
		if slices.ContainsFunc(st.After, func(k string) bool { return status[k] != stepAccepted }) {
			continue
		}
		if slices.ContainsFunc(st.scopes(), func(sc string) bool { return slices.Contains(writing, sc) }) {
			continue // its turn to write comes
		}
		if err := c.startStep(ctx, st, steps, ""); err != nil {
			log.Printf("work %s: step %s: %v", parent, st.Node, err)
			note := truncate(err.Error(), stepNoteMax)
			c.setStep(st.Node, map[string]any{"status": stepBlocked, "note": note, "ended": time.Now().Unix()})
			c.stepState(st.Node, stepBlocked)
			c.traceWork(parent, fmt.Sprintf("L'étape « %s » n'a pas pu démarrer : %s", st.Spec.Title, note), "")
			c.wakeForWork(parent, fmt.Sprintf("L'étape « %s » (nœud %s) n'a pas pu démarrer : %s\nDécide avec node_settle : retry (en disant quoi changer), drop ou replan.", st.Spec.Title, st.Node, note), nil)
			status[st.Node] = stepBlocked
			continue
		}
		steps[i].Status, status[st.Node] = stepRunning, stepRunning
		writing = append(writing, st.scopes()...)
	}
	if busy || w.State != workRunning {
		return
	}
	if !slices.ContainsFunc(steps, func(st Step) bool { return st.Status == stepAccepted }) {
		return // nothing came of it: dropped whole
	}
	c.finishWork(ctx, w, steps)
}

// startStep starts a step's task, in its node, with the work's goal and
// what the steps it comes after produced; again is what a retry asks.
func (c *core) startStep(ctx context.Context, st Step, steps []Step, again string) error {
	goal := ""
	if d, err := c.nodes.Read(st.Parent, 0); err == nil {
		goal = d.Node.Meta.Goal
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(st.Spec.Prompt))
	b.WriteString("\n\n## Step\nThis is one step of a piece of work; Eriphe accepts it on its proof: your receipt says done and every check of your contract passes.")
	if goal != "" {
		fmt.Fprintf(&b, "\nThe work's goal: %s", goal)
	}
	// What the steps before it produced is passed on, and so is its taint.
	var handed []string
	for _, k := range st.After {
		i := slices.IndexFunc(steps, func(x Step) bool { return x.Node == k })
		if i < 0 {
			continue
		}
		if steps[i].Output != "" {
			fmt.Fprintf(&b, "\n\n### Result of the step « %s »\n%s", steps[i].Spec.Title, truncate(steps[i].Output, stepOutputMax))
		}
		handed = appendNew(handed, c.stepTaint(steps[i])...)
	}
	if again != "" {
		fmt.Fprintf(&b, "\n\n## Retry\nA previous attempt at this step was not accepted. What to fix: %s", again)
	}
	var taint []string
	if st.Tainted != "" {
		taint = strings.Split(st.Tainted, ", ")
	}
	taint = appendNew(taint, handed...)
	clean := st.Clean && len(taint) == 0
	r := runRequest{Title: st.Spec.Title, Prompt: b.String(), Resources: st.Spec.Resources, Agent: st.Spec.Agent, Contract: st.Spec.Contract,
		Device: st.Spec.Device, askStart: true, cleanStart: clean, taint: taint, inNode: st.Node}
	t, err := c.runTask(ctx, r)
	if err != nil {
		return err
	}
	if clean {
		c.store.set(lentByUserKey(t.ID), "1") // the user's own skill, approved with what it borrows (lend.go)
	}
	c.setStep(st.Node, map[string]any{"status": stepRunning, "task": t.ID, "started": time.Now().Unix(), "failed": false, "note": truncate(again, stepNoteMax), "ended": 0})
	c.stepState(st.Node, stepRunning)
	c.traceWork(st.Parent, fmt.Sprintf("L'étape « %s » démarre : tâche %s.", st.Spec.Title, t.ID), "")
	return nil
}

// stepTaint is why a step's result may carry someone else's words: what
// its opener was, what its task read.
func (c *core) stepTaint(st Step) []string {
	var out []string
	if st.Tainted != "" {
		out = strings.Split(st.Tainted, ", ")
	}
	if st.Task != "" {
		out = appendNew(out, c.taskResultTaint(st.Task))
	}
	return out
}

// proven tells whether a receipt proves a step: given, done, every check
// passed, nothing it claims denied by what the device saw.
func proven(r *checkedReceipt) bool {
	return !r.missing() && r.Status == "done" && len(r.failedChecks()) == 0 && !r.Unsupported
}

// stepTaskChanged follows a step's task, or a final check's: its turn's
// end is judged on its proof, its end before is a blocked step. It tells
// whether the task was one: the brain then hears of it only as an
// exception.
func (c *core) stepTaskChanged(id, state string, outcome turnOutcome) bool {
	if state == stateWaitingPermission {
		if _, ok := c.stepOfTask(id); ok {
			return false // a permission is the user's, told as any task's
		}
	}
	if w, ok := c.workOfFinal(id); ok {
		if state == stateWaitingPermission {
			return false
		}
		stepMu.Lock()
		defer stepMu.Unlock()
		c.finalChanged(w, id, state, outcome)
		return true
	}
	st, ok := c.stepOfTask(id)
	if !ok {
		return false
	}
	stepMu.Lock()
	defer stepMu.Unlock()
	if st, ok = c.stepOfTask(id); !ok || st.Status != stepRunning {
		return true // settled already, or stopped by the engine
	}
	ctx := context.Background()
	tst, err := c.status(id, 0)
	if err != nil {
		return true
	}
	var taint []string
	if why := c.taskResultTaint(id); why != "" {
		taint = append(taint, why)
	}
	now := time.Now().Unix()
	switch state {
	case stateIdle:
		r := outcome.receipt
		if r == nil {
			r = tst.Receipt
		}
		output := strings.TrimSpace(tst.LastText)
		if output == "" && !r.missing() {
			output = r.Summary
		}
		if proven(r) {
			c.setStep(st.Node, map[string]any{"status": stepAccepted, "output": output, "note": "", "ended": now})
			c.stepState(st.Node, stepAccepted)
			c.stopStepTask(ctx, id)
			c.traceWork(st.Parent, fmt.Sprintf("L'étape « %s » est acceptée sur sa preuve (%s).", st.Spec.Title, proofWords(r)), "")
			c.advanceLocked(ctx, st.Parent)
			return true
		}
		failed := !r.missing() && (len(r.failedChecks()) > 0 || r.Unsupported)
		why := "sans reçu lisible"
		if !r.missing() {
			why = r.verdict()
		}
		c.setStep(st.Node, map[string]any{"status": stepFailed, "failed": failed, "output": output, "note": truncate(why, stepNoteMax), "ended": now})
		c.stepState(st.Node, stepFailed)
		c.traceWork(st.Parent, fmt.Sprintf("L'étape « %s » n'a pas fait sa preuve : %s.", st.Spec.Title, why), strings.Join(taint, ", "))
		accept := "accept (son travail tient malgré tout)"
		if failed {
			accept = "pas accept : ses contrôles échouent"
		}
		c.wakeForWork(st.Parent, fmt.Sprintf("L'étape « %s » (nœud %s, tâche %s) n'a pas fait sa preuve : %s\nPremière ligne : %s\nDécide avec node_settle : retry (en disant quoi corriger ; %d essai(s) restant(s)), %s, drop ou replan.",
			st.Spec.Title, st.Node, id, why, firstLine(output), max(0, stepRetries-st.Tries), accept), taint)
	case stateStopped, stateFailed, stateLost:
		if c.store.get("brain_stopped:"+id) != "" {
			return true // stopped by the engine or the brain
		}
		note := truncate(state+c.endedNote(tst.Task), stepNoteMax)
		c.setStep(st.Node, map[string]any{"status": stepBlocked, "note": note, "ended": now})
		c.stepState(st.Node, stepBlocked)
		c.traceWork(st.Parent, fmt.Sprintf("L'étape « %s » s'est arrêtée (%s).", st.Spec.Title, state), "")
		c.wakeForWork(st.Parent, fmt.Sprintf("L'étape « %s » (nœud %s, tâche %s) s'est arrêtée : %s.\nDécide avec node_settle : retry (%d essai(s) restant(s)), drop ou replan.",
			st.Spec.Title, st.Node, id, note, max(0, stepRetries-st.Tries)), taint)
		c.advanceLocked(ctx, st.Parent) // what waited for its turn to write
	}
	return true
}

// proofWords say what proved a step: its checks, or its receipt alone.
func proofWords(r *checkedReceipt) string {
	if len(r.Checks) == 0 {
		return "son reçu dit fait"
	}
	var names []string
	for _, k := range r.Checks {
		names = append(names, k.Name)
	}
	return "contrôles passés : " + strings.Join(names, ", ")
}

// stopStepTask stops a step's task if it still runs, without telling.
func (c *core) stopStepTask(ctx context.Context, task string) {
	if task == "" {
		return
	}
	if t, err := c.store.task(task); err == nil && !finished(t.State) {
		c.stopByBrain(ctx, t.ID)
	}
}

// wakeForWork wakes the brain for an exception of a work: an event of
// the conversation, taint what its words make the turn that reads it.
func (c *core) wakeForWork(parent, text string, taint []string) {
	head, t := c.nodeHead(parent)
	line := head + " " + text
	if t != "" {
		taint = append(slices.Clone(taint), t)
	}
	seq, err := c.store.addNodeMessage(roleEvent, line, "", parent, true)
	if err != nil {
		return
	}
	if len(taint) > 0 {
		c.store.set(taintKey(int(seq)), strings.Join(taint, ", "))
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// holdForBudget holds a work past its budget: nothing new starts, the
// steps running finish, the brain decides.
func (c *core) holdForBudget(w work) {
	now := time.Now()
	c.setWork(w.Parent, map[string]any{"state": workHeld, "held": "budget", "spent": int64(w.elapsed(now) / time.Second), "since": 0})
	c.traceWork(w.Parent, fmt.Sprintf("Budget de %d min atteint : rien de nouveau ne démarre.", w.Budget), "")
	c.wakeForWork(w.Parent, fmt.Sprintf("Le budget de %d min est atteint : rien de nouveau ne démarre, les étapes en cours finissent.\nnode_settle sur le nœud, sans étape : accept donne %d min de plus, drop arrête le travail.", w.Budget, budgetMore(w)), nil)
}

// budgetMore is the time more a work is given: half its budget, 30
// minutes at least.
func budgetMore(w work) int { return max(30, w.Budget/2) }

// workForUser puts a done work before the user: its result, the
// accepted steps no other step comes after, waits for them until they, or
// the brain on their word (node_set done), say it is done; the work around
// it is done. A work with no such step waits itself.
func (c *core) workForUser(parent string) {
	steps, _ := c.workSteps(parent)
	after := map[string]bool{}
	for _, st := range steps {
		if st.Status != stepAccepted {
			continue // a dropped step is no one's sequel
		}
		for _, a := range st.After {
			after[a] = true
		}
	}
	var results []string
	for _, st := range steps {
		if st.Status == stepAccepted && !after[st.Node] {
			results = append(results, st.Node)
		}
	}
	if len(results) == 0 {
		c.nodes.SetState(parent, nodestore.Waiting)
		return
	}
	for _, id := range results {
		// One the user already said done, or archived, stays so.
		if sum, err := c.nodes.Get(id); err == nil && sum.Done.IsZero() && sum.Archived.IsZero() {
			c.nodes.SetState(id, nodestore.Waiting)
		}
	}
	c.nodes.SetState(parent, nodestore.Done)
}

// The final check.

// finishWork ends a work whose steps are all accepted or dropped: a fresh
// agent checks its goal; without one, it is done.
func (c *core) finishWork(ctx context.Context, w work, steps []Step) {
	goal := ""
	if d, err := c.nodes.Read(w.Parent, 0); err == nil {
		goal = strings.TrimSpace(d.Node.Meta.Goal)
	}
	if goal == "" {
		c.setWork(w.Parent, map[string]any{"state": workDone, "spent": int64(w.elapsed(time.Now()) / time.Second), "since": 0})
		c.traceWork(w.Parent, "Toutes les étapes sont acceptées.", "")
		c.workForUser(w.Parent)
		return
	}
	if err := c.startFinal(ctx, w, goal, steps); err != nil {
		c.setWork(w.Parent, map[string]any{"state": workFailed})
		c.traceWork(w.Parent, "Le contrôle final n'a pas pu démarrer : "+err.Error(), "")
		c.wakeForWork(w.Parent, "Toutes les étapes sont acceptées, mais le contrôle final de l'objectif n'a pas pu démarrer : "+err.Error()+
			"\nnode_settle sur le nœud, sans étape : retry le relance, accept tient le travail pour fait, drop l'arrête.", nil)
	}
}

// startFinal starts the final check: a fresh agent, lent read-only what
// the steps wrote, given the goal and what each step produced.
func (c *core) startFinal(ctx context.Context, w work, goal string, steps []Step) error {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a fresh judge, independent from the agents who did the work. Check whether this work reached its goal.\n\nGoal: %s\n\n", goal)
	b.WriteString("Inspect what you are lent (read-only) yourself; what the steps say below is only their claim.\n")
	var lent []ResourceRef
	seen := map[string]bool{}
	var taint []string
	for _, st := range steps {
		if st.Status != stepAccepted {
			continue
		}
		fmt.Fprintf(&b, "\n### Step « %s »\n%s\n", st.Spec.Title, truncate(st.Output, 1500))
		taint = appendNew(taint, c.stepTaint(st)...)
		for _, r := range st.Spec.Resources {
			if r.Kind != resRepo && r.Kind != resFolder && r.Kind != resPlace {
				continue
			}
			r.Access, r.Hold = accessRead, false
			if k := r.key() + "@" + r.Branch; !seen[k] {
				seen[k] = true
				lent = append(lent, r)
			}
		}
	}
	b.WriteString("\nYour receipt's status is done only if the goal holds, with how you checked it in verified; otherwise partial, with what is missing in not_verified.")
	title := tr(c.userLang(), "step.final.title")
	t, err := c.runTask(ctx, runRequest{Title: title, Prompt: b.String(), Resources: lent, Node: w.Parent, askStart: true, taint: taint})
	if err != nil {
		return err
	}
	c.setWork(w.Parent, map[string]any{"state": workChecking, "final": t.ID, "checks": w.Checks + 1, "tainted": strings.Join(taint, ", ")})
	c.traceWork(w.Parent, fmt.Sprintf("Toutes les étapes sont acceptées : un agent neuf vérifie l'objectif (tâche %s).", t.ID), "")
	return nil
}

// finalChanged reads the final check's verdict: the goal holds and the
// work is done, or the brain hears what is missing.
func (c *core) finalChanged(w work, id, state string, outcome turnOutcome) {
	if w.State != workChecking || w.Final != id {
		return
	}
	tst, err := c.status(id, 0)
	if err != nil {
		return
	}
	var r *checkedReceipt
	switch state {
	case stateIdle:
		if r = outcome.receipt; r == nil {
			r = tst.Receipt
		}
	case stateStopped, stateFailed, stateLost:
		if c.store.get("brain_stopped:"+id) != "" {
			return
		}
	default:
		return
	}
	c.stopStepTask(context.Background(), id)
	var taint []string
	if w.Tainted != "" {
		taint = strings.Split(w.Tainted, ", ")
	}
	if why := c.taskResultTaint(id); why != "" {
		taint = appendNew(taint, why)
	}
	if proven(r) {
		c.setWork(w.Parent, map[string]any{"state": workDone, "spent": int64(w.elapsed(time.Now()) / time.Second), "since": 0})
		c.traceWork(w.Parent, "Objectif vérifié par un agent neuf : "+r.verdict(), strings.Join(taint, ", "))
		c.workForUser(w.Parent)
		return
	}
	why := state
	if r != nil {
		why = "sans reçu lisible"
		if !r.missing() {
			why = r.verdict()
		}
	}
	c.setWork(w.Parent, map[string]any{"state": workFailed})
	c.traceWork(w.Parent, "Le contrôle final dit que l'objectif n'est pas atteint : "+why, strings.Join(taint, ", "))
	c.wakeForWork(w.Parent, fmt.Sprintf("Le contrôle final dit que l'objectif n'est pas atteint : %s\nnode_settle sur le nœud, sans étape : retry le relance (%d restant(s)), accept tient le travail pour fait, drop l'arrête ; ou ouvre d'autres étapes.",
		why, max(0, finalChecks-w.Checks)), taint)
}

// The brain's decisions.

// settleStep is the brain's word on an exception: on a step (accept,
// retry, drop, replan) or, with no step, on the work (accept: more time
// past the budget, or the work done past a final check; retry: the final
// check again; drop: the work stops).
func (c *core) settleStep(ctx context.Context, parent, step, outcome, note string) (string, error) {
	stepMu.Lock()
	defer stepMu.Unlock()
	w, ok := c.work(parent)
	if !ok {
		return "", fmt.Errorf("node %s has no steps", parent)
	}
	note = strings.TrimSpace(note)
	if step == "" {
		return c.settleWork(ctx, w, outcome, note)
	}
	st, ok := c.nodeStep(step)
	if !ok || st.Parent != parent {
		return "", fmt.Errorf("node %s is no step of %s", step, parent)
	}
	now := time.Now().Unix()
	switch outcome {
	case settleAccept:
		switch {
		case st.Status != stepFailed:
			return "", fmt.Errorf("step %s is %s: only a step whose proof failed is accepted by you", step, st.Status)
		case st.Failed:
			return "", errors.New("its checks failed: a report is only a claim. Retry it, saying what to fix, drop it or replan")
		}
		c.setStep(st.Node, map[string]any{"status": stepAccepted, "note": truncate(note, stepNoteMax), "ended": now})
		c.stepState(st.Node, stepAccepted)
		c.stopStepTask(ctx, st.Task)
		c.traceWork(parent, fmt.Sprintf("Étape « %s » acceptée par Eriphe.%s", st.Spec.Title, withNote(note)), "")
	case settleRetry:
		switch {
		case note == "":
			return "", errors.New("a retry says what to fix, in note")
		case st.Status != stepFailed && st.Status != stepBlocked:
			return "", fmt.Errorf("step %s is %s: nothing to retry", step, st.Status)
		case st.Tries >= stepRetries:
			return "", fmt.Errorf("step %s was retried %d times, the limit: drop it, replan, or ask %s", step, st.Tries, c.conf().User)
		case w.State == workHeld:
			return "", errors.New("the work's budget is spent: give it more time first (node_settle on the node, no step, accept)")
		}
		if err := c.retryStep(ctx, st, note); err != nil {
			return "", err
		}
		c.traceWork(parent, fmt.Sprintf("Étape « %s » à reprendre : %s", st.Spec.Title, note), "")
	case settleDrop:
		if st.over() {
			return "", fmt.Errorf("step %s is %s: nothing to drop", step, st.Status)
		}
		c.traceDrop(st, c.dropStep(ctx, st), "abandonnée", note)
	case settleReplan:
		return "", errors.New("replan gives the new steps: node_settle with steps")
	default:
		return "", fmt.Errorf("outcome %q: accept, retry, drop or replan", outcome)
	}
	c.advanceLocked(ctx, parent)
	return "Done.", nil
}

// traceDrop writes a step dropped, and those after it, in the work's trace.
func (c *core) traceDrop(st Step, gone []string, words, note string) {
	c.traceWork(st.Parent, fmt.Sprintf("Étape « %s » %s.%s", st.Spec.Title, words, withNote(note)), "")
	if len(gone) > 1 {
		c.traceWork(st.Parent, fmt.Sprintf("Avec elle, les étapes qui en dépendaient : %d.", len(gone)-1), "")
	}
}

// replan is the brain's new plan for a work: the step named and the
// steps after it are dropped (none for no step), and the steps given are
// opened in their place, by the brain, tainted by its turn. A plan
// refused changes nothing.
func (c *core) replan(ctx context.Context, parent, step, note string, specs []stepSpec, taint []string) (string, error) {
	if len(specs) == 0 {
		return "", errors.New("replan gives the new steps: steps, as node_open's")
	}
	stepMu.Lock()
	defer stepMu.Unlock()
	w, ok := c.work(parent)
	switch {
	case !ok:
		return "", fmt.Errorf("node %s has no steps", parent)
	case w.State == workStopped:
		return "", errors.New("the work is stopped: open another node for new work")
	case w.State == workChecking:
		return "", errors.New("the final check runs: wait for its verdict")
	}
	have, err := c.workSteps(parent)
	if err != nil {
		return "", err
	}
	var st Step
	var gone []string
	if step != "" {
		if st, ok = c.nodeStep(step); !ok || st.Parent != parent {
			return "", fmt.Errorf("node %s is no step of %s", step, parent)
		}
		if st.over() {
			return "", fmt.Errorf("step %s is %s: nothing to replan", step, st.Status)
		}
		gone = dropSet(have, st.Node)
		for i := range have {
			if slices.Contains(gone, have[i].Node) {
				have[i].Status = stepDropped
			}
		}
	}
	plan, err := checkPlan(have, slices.Clone(specs))
	if err != nil {
		return "", err
	}
	for _, sp := range plan {
		if err := c.checkStepRequest(sp, taint); err != nil {
			return "", err
		}
	}
	if step != "" {
		c.traceDrop(st, c.dropStep(ctx, st), "abandonnée pour un nouveau plan", note)
	}
	ids, err := c.openStepsLocked(ctx, parent, specs, 0, stepOpener{author: byBrain, tainted: taint})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Dropped %d step(s); opened %s.", len(gone), strings.Join(slices.Sorted(maps.Keys(ids)), ", ")), nil
}

// settleWork is the brain's word on a work, not on a step.
func (c *core) settleWork(ctx context.Context, w work, outcome, note string) (string, error) {
	now := time.Now().Unix()
	switch {
	case outcome == settleAccept && w.State == workHeld:
		more := budgetMore(w)
		c.setWork(w.Parent, map[string]any{"state": workRunning, "held": "", "budget": int(w.Spent/60) + more, "since": now})
		c.traceWork(w.Parent, fmt.Sprintf("%d min de plus.%s", more, withNote(note)), "")
		c.advanceLocked(ctx, w.Parent)
		return fmt.Sprintf("%d minutes more: the work goes on.", more), nil
	case outcome == settleAccept && w.State == workFailed:
		c.setWork(w.Parent, map[string]any{"state": workDone})
		c.traceWork(w.Parent, "Travail tenu pour fait par Eriphe."+withNote(note), "")
		c.workForUser(w.Parent)
		return "The work is done.", nil
	case outcome == settleRetry && w.State == workFailed:
		if w.Checks >= finalChecks {
			return "", fmt.Errorf("the goal was checked %d times and does not hold: replan with other steps, accept it as it is, or drop it", w.Checks)
		}
		c.setWork(w.Parent, map[string]any{"state": workRunning, "since": now})
		c.traceWork(w.Parent, "Contrôle final relancé."+withNote(note), "")
		c.advanceLocked(ctx, w.Parent)
		return "The final check runs again.", nil
	case outcome == settleDrop && w.State != workDone && w.State != workStopped:
		steps, _ := c.workSteps(w.Parent)
		for _, st := range steps {
			if !st.over() {
				c.stopStepTask(ctx, st.Task)
				c.setStep(st.Node, map[string]any{"status": stepDropped, "ended": now})
				c.stepState(st.Node, stepDropped)
			}
		}
		c.stopStepTask(ctx, w.Final)
		c.setWork(w.Parent, map[string]any{"state": workStopped, "spent": int64(w.elapsed(time.Now()) / time.Second), "since": 0})
		c.traceWork(w.Parent, "Travail arrêté par Eriphe."+withNote(note), "")
		return "The work is stopped.", nil
	}
	return "", fmt.Errorf("the work is %s: %s on the work itself is not possible now (name the step)", w.State, outcome)
}

func withNote(note string) string {
	if note == "" {
		return ""
	}
	return " " + note
}

// retryStep asks a step again: its idle task gets the note as its next
// turn; a task that ended, or past its turns, gives way to a new one.
func (c *core) retryStep(ctx context.Context, st Step, note string) error {
	if t, err := c.store.task(st.Task); err == nil && t.State == stateIdle {
		if _, err := c.answer(ctx, t.ID, "", "Ton travail n'est pas accepté tel quel. À corriger : "+note, nil, byBrain); err == nil {
			c.setStep(st.Node, map[string]any{"status": stepRunning, "tries": st.Tries + 1, "failed": false, "note": truncate(note, stepNoteMax), "ended": 0})
			c.stepState(st.Node, stepRunning)
			return nil
		}
	}
	steps, _ := c.workSteps(st.Parent)
	c.stopStepTask(ctx, st.Task)
	if err := c.startStep(ctx, st, steps, note); err != nil {
		return err
	}
	c.setStep(st.Node, map[string]any{"tries": st.Tries + 1})
	return nil
}

// dropStep drops a step and the steps that come after it, which can no
// longer start; their tasks stop. It gives the steps dropped.
func (c *core) dropStep(ctx context.Context, st Step) []string {
	steps, _ := c.workSteps(st.Parent)
	gone := dropSet(steps, st.Node)
	now := time.Now().Unix()
	for _, x := range steps {
		if slices.Contains(gone, x.Node) {
			c.stopStepTask(ctx, x.Task)
			c.setStep(x.Node, map[string]any{"status": stepDropped, "ended": now})
			c.stepState(x.Node, stepDropped)
		}
	}
	return gone
}

// dropSet is a step and the unfinished steps that come after it, which
// go with it.
func dropSet(steps []Step, node string) []string {
	gone := []string{node}
	for moved := true; moved; {
		moved = false
		for _, x := range steps {
			if x.over() || slices.Contains(gone, x.Node) {
				continue
			}
			if slices.ContainsFunc(x.After, func(a string) bool { return slices.Contains(gone, a) }) {
				gone = append(gone, x.Node)
				moved = true
			}
		}
	}
	return gone
}

// stepsTick is the engine's pass, every 15 seconds: the steps whose task
// ended, or ended its turn with a receipt, without the engine being told
// (eriphe-core restarted), the final checks likewise, then what may
// start.
func (c *core) stepsTick() {
	rows, err := c.store.db.Query(`SELECT parent, state, final FROM node_works WHERE state IN (?, ?)`, workRunning, workChecking)
	if err != nil {
		return
	}
	type live struct{ parent, state, final string }
	var ws []live
	for rows.Next() {
		var l live
		if rows.Scan(&l.parent, &l.state, &l.final) == nil {
			ws = append(ws, l)
		}
	}
	rows.Close()
	ctx := context.Background()
	for _, l := range ws {
		if l.state == workChecking {
			c.missedEnd(l.final)
			continue
		}
		steps, _ := c.workSteps(l.parent)
		for _, st := range steps {
			if st.Status == stepRunning {
				c.missedEnd(st.Task)
			}
		}
		c.advanceWork(ctx, l.parent)
	}
}

// missedEnd tells the engine of a task's end it may have missed: ended,
// or idle with its turn's receipt (one still asked for is waited for).
func (c *core) missedEnd(task string) {
	t, err := c.store.task(task)
	if err != nil {
		return
	}
	switch {
	case finished(t.State):
	case t.State == stateIdle:
		if st, err := c.status(task, 0); err != nil || st.Receipt.missing() {
			return
		}
	default:
		return
	}
	c.stepTaskChanged(t.ID, t.State, turnOutcome{})
}
