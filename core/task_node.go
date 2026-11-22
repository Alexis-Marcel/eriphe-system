package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A worker task works in a node of its own (#629): code opens it when the
// task starts, a child of the node run_task names (node), else at the
// top. The task writes it as it goes with eriphe-nodes (task_nodes.go),
// on its own node only. Its state is code's: running, waiting (held for
// its card, or for a permission), idle (its turn ended), done (it ended).
//
// At the end of each turn, its last message, its report, is filed by
// code, never cut: whole in the node's trace, and as the node's doc when
// the task wrote none. The brain is woken with a short message (title,
// state, first line) that points to the node (idleEvent).
//
// A task tainted at start, or by what it read, writes external: its node
// and the blocks it writes are marked so by code, in the same version.

const taskNodeSchema = `CREATE TABLE IF NOT EXISTS task_nodes (
	task  TEXT PRIMARY KEY,
	node  TEXT NOT NULL,
	filed TEXT NOT NULL DEFAULT ''  -- the hash of the last report filed
);`

func init() { extraSchema = append(extraSchema, taskNodeSchema) }

// taskNodeGoalMax is the most runes of a task's node's goal, made from its
// contract's objective or its prompt's first line.
const taskNodeGoalMax = 300

// taskNode is the node task works in; "" for none.
func (c *core) taskNode(task string) string {
	var id string
	c.store.db.QueryRow(`SELECT node FROM task_nodes WHERE task = ?`, task).Scan(&id)
	return id
}

// nodeParent checks the node a task is asked to work under: one of the
// graph's, not archived.
func (c *core) nodeParent(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if c.nodes == nil {
		return errors.New("node: the graph is not open on this eriphe-core")
	}
	sum, err := c.nodes.Get(id)
	if err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if !sum.Archived.IsZero() {
		return fmt.Errorf("node %s is archived", id)
	}
	return nil
}

// openTaskNode opens a worker task's node, a child of parent ("" for the
// top), titled as the task, external when the task starts tainted.
func (c *core) openTaskNode(t *TaskRow, r runRequest, held bool) error {
	if c.nodes == nil {
		return nil
	}
	state := nodestore.Running
	if held {
		state = nodestore.Waiting
	}
	if r.inNode != "" { // a step's task in the step's node (node_steps.go), a skill's run in its standing node
		if _, err := c.store.db.Exec(`INSERT INTO task_nodes (task, node) VALUES (?, ?)`, t.ID, r.inNode); err != nil {
			return err
		}
		c.nodes.SetState(r.inNode, state)
		return nil
	}
	neutral := fmt.Sprintf(tr(c.userLang(), "tasknode.title"), t.ID)
	title := t.Title
	if title == "" {
		title = neutral
	}
	goal := ""
	if r.Contract != nil {
		goal = r.Contract.Objective
	}
	if goal == "" {
		goal, _, _ = strings.Cut(strings.TrimSpace(r.Prompt), "\n")
	}
	origin := r.origin
	switch {
	case origin != nil:
	case r.byBrain:
		origin = c.turnOrigin()
	default:
		origin = c.workOrigin(strings.TrimSpace(r.Node), "")
	}
	if origin != nil {
		cp := *origin
		cp.Task, cp.Node = t.ID, strings.TrimSpace(r.Node)
		origin = &cp
	}
	nw := nodestore.New{Title: truncate(oneLine(title), nodefile.TitleMax-1), Parent: strings.TrimSpace(r.Node),
		Goal: truncate(oneLine(goal), taskNodeGoalMax), External: len(r.taint) > 0, OpenedBy: origin, Author: c.taskAuthor(t.ID)}
	// A title or a goal the filter refuses (« Renouvelle le token: expiré »)
	// does not stop the task: its node goes without the goal, then with a
	// neutral title.
	d, err := c.nodes.Create(nw)
	if err != nil {
		nw.Goal = ""
		d, err = c.nodes.Create(nw)
	}
	if err != nil {
		nw.Title = neutral
		d, err = c.nodes.Create(nw)
	}
	if err != nil {
		return fmt.Errorf("its node: %w", err)
	}
	id := d.Node.Meta.ID
	if _, err := c.store.db.Exec(`INSERT INTO task_nodes (task, node) VALUES (?, ?)`, t.ID, id); err != nil {
		return err
	}
	c.nodes.SetState(id, state)
	return nil
}

// standingWork tells a standing node: tasks work in it, none ends it.
func (c *core) standingWork(id string) bool {
	sum, err := c.nodes.Get(id)
	return err == nil && sum.Lifetime == nodefile.Standing
}

// taskAuthor is who a task writes as: skill:<name> for a skill's run,
// task:<id> for another.
func (c *core) taskAuthor(task string) string {
	if r := c.runOfTask(task); r != nil {
		return "skill:" + r.Skill
	}
	return "task:" + task
}

// taskNodeState follows a task's state on its node.
func (c *core) taskNodeState(task, state string) {
	id := c.taskNode(task)
	if id == "" || c.nodes == nil {
		return
	}
	if st, ok := c.nodeStep(id); ok && (st.Task != task || state != stateWaitingPermission && state != stateRunning && state != statePending) {
		return // a step's node: its engine says where it stands (node_steps.go)
	}
	if c.standingWork(id) && (state == stateStopped || state == stateFailed || state == stateLost) {
		state = stateIdle // a standing node outlives the runs that work in it
	}
	var to string
	switch state {
	case stateRunning, statePending:
		to = nodestore.Running
	case stateWaitingPermission:
		to = nodestore.Waiting
	case stateIdle:
		to = nodestore.Idle
	case stateStopped, stateFailed, stateLost:
		to = nodestore.Done
	default:
		return
	}
	if to == nodestore.Idle && c.nodeCardWaits(id) {
		to = nodestore.Waiting // a card of its waits for the user (node_turn.go)
	}
	if err := c.nodes.SetState(id, to); err != nil {
		log.Printf("task %s: node %s: %v", task, id, err)
	}
}

// fileTaskReport files a task's report, its last message, in its node:
// whole in its trace (in several entries past the store's limit for
// one), and as its doc when the task wrote none (never a standing
// node's: its runs report in its trace only). Filed once a turn: the
// same report is not filed again. It says what went wrong, for the
// brain; "" when nothing did.
func (c *core) fileTaskReport(task string, r *checkedReceipt) string {
	id := c.taskNode(task)
	if id == "" || c.nodes == nil {
		return ""
	}
	st, err := c.status(task, 0)
	if err != nil {
		return ""
	}
	report := strings.TrimSpace(st.LastText)
	if r == nil {
		r = st.Receipt
	}
	if report == "" && !r.missing() {
		report = strings.TrimSpace(r.Summary)
	}
	if report == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(report))
	hash := hex.EncodeToString(sum[:])
	var filed string
	c.store.db.QueryRow(`SELECT filed FROM task_nodes WHERE task = ?`, task).Scan(&filed)
	if filed == hash {
		return ""
	}
	c.store.db.Exec(`UPDATE task_nodes SET filed = ? WHERE task = ?`, hash, task)
	author := c.taskAuthor(task)
	if c.writeTaint(task) != "" {
		c.markNodeExternal(id, author) // the report may carry someone else's words: marked before it is filed
	}
	var problems []string
	for _, part := range traceParts(report, nodestore.MaxTrace) {
		if err := c.nodes.AppendTrace(id, author, part); err != nil {
			problems = append(problems, "sa trace l'a refusé : "+err.Error())
			break
		}
	}
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return strings.Join(problems, " ; ")
	}
	if strings.TrimSpace(d.Node.Text()) == "" && len(d.Node.Blocks()) == 0 && d.Node.Meta.Lifetime != nodefile.Standing {
		if _, err := c.writeTaskBody(task, report, d.Version.Hash, nil); err != nil {
			problems = append(problems, "il n'a pas pu devenir son document : "+err.Error())
		}
	}
	if len(problems) > 0 {
		log.Printf("task %s: its report in node %s: %s", task, id, strings.Join(problems, "; "))
	}
	return strings.Join(problems, " ; ")
}

// traceParts cuts a report into parts of max bytes at most, at a line's
// end when it can, never inside a character: each one a trace entry,
// all of it kept.
func traceParts(text string, max int) []string {
	var out []string
	for len(text) > max {
		cut := strings.LastIndexByte(text[:max], '\n')
		if cut <= max/2 {
			cut = max
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
		}
		out = append(out, text[:cut])
		text = text[cut:]
	}
	return append(out, text)
}

// traceTask writes a line in a task's node's trace: what Eriphe or the
// user settled for it (answer_task, a release's card). tainted, when
// the line may carry someone else's words, marks the node external.
func (c *core) traceTask(task, author, line, tainted string) {
	id := c.taskNode(task)
	if id == "" || c.nodes == nil {
		return
	}
	if tainted != "" {
		c.markNodeExternal(id, author) // before the line: never words without the mark
	}
	if err := c.nodes.AppendTrace(id, author, line); err != nil {
		log.Printf("task %s: node %s: trace: %v", task, id, err)
	}
}

// markRead, set by a test, runs between markNodeExternal's read and its
// write.
var markRead func(id string)

// markNodeExternal marks a node external: it holds someone else's words.
// It is called before those words are written. A write made meanwhile (a
// conflict) is no reason to lose the mark: it is tried again on the new
// version.
func (c *core) markNodeExternal(id, author string) {
	for try := 0; try < 3; try++ {
		d, err := c.nodes.Read(id, 0)
		if err != nil || d.Node.Meta.External {
			return
		}
		if markRead != nil {
			markRead(id)
		}
		_, err = c.nodes.Change(id, d.Version.Hash, author, func(n *nodefile.Node) error {
			n.Meta.External = true
			return nil
		})
		if errors.Is(err, nodestore.ErrConflict) {
			continue
		}
		if err != nil {
			log.Printf("node %s: external: %v", id, err)
		}
		return
	}
	log.Printf("node %s: external: not marked, written over three times in a row", id)
}

// firstLine is a text's first line that says something, short.
func firstLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#>*-"))
		if l != "" && !strings.HasPrefix(l, "```") {
			return truncate(l, 200)
		}
	}
	return ""
}

// checksLine says, in a task's trace, that Eriphe fixed its contract's
// checks, and what it replied.
func checksLine(lang string, checks []taskCheck, reply string) string {
	names := make([]string, 0, len(checks))
	for _, k := range checks {
		names = append(names, k.Name)
	}
	if reply = strings.TrimSpace(reply); reply != "" {
		return fmt.Sprintf(tr(lang, "tasknode.checksreply"), strings.Join(names, ", "), reply)
	}
	return fmt.Sprintf(tr(lang, "tasknode.checks"), strings.Join(names, ", "))
}

// answerTaint is why what the brain answers a task may carry what someone
// else wrote: its turn's taint, and the task's own when its result is
// tainted (the question it answers came from there).
func (c *core) answerTaint(task string) string {
	why := c.turnTaint()
	if t := c.taskResultTaint(task); t != "" && !slices.Contains(why, t) {
		why = append(why, t)
	}
	return strings.Join(why, ", ")
}
