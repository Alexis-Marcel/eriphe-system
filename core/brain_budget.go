package main

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's budget (#770): the brain orchestrates. It understands,
// chooses where a thing lives in the graph, launches a task on that node
// (run_task: node, skill, a short ask) and answers in a line naming the
// node; it does itself only a read, a gesture of a call or two, a yes or
// no, a one-line correction. A prompt rule did not hold it: on the
// owner's install (8 days, 244 turns, median 7 s, p90 50 s), 42 of the 48
// turns over 30 s were heavy writes by the brain itself: whole pages
// written (node_write), skills written, monitors tried, bursts of
// item_note (up to 50 in a turn) and series of node_edit. So code holds
// it: in a turn of the brain's, a whole page and more than a few writes
// are refused, with a hint to launch a task. Tasks are never budgeted:
// they work on their own server (task_nodes.go), never through here.
//
// What is never counted: reads, and what routes and launches (run_task,
// node_open with a short body, node_show, turn_home, the tasks' and
// steps' answers, a card withdrawn): the way out of a refusal always
// passes. write_skill, write_monitor and try_monitor stay the brain's,
// counted: no task proposes a new skill or monitor (a task proposes only
// its own skill's next version, skill_propose).
//
// The budget is per delivery: each delivery of messages or events (a
// turn of the brain's, as the turn report counts them) starts a fresh
// one, scaled by how many messages it brings, so a delivery of three
// tasks' ends may update their three nodes. It is not kept: a restart of
// eriphe-core starts a fresh one.
// A call that fails (stale, invalid) gives back what it took (refund):
// it changed nothing.
const (
	// brainWriteRunes is the most text a node write of the brain's
	// carries (node_open's title, goal and body; node_write's body;
	// node_edit's block or new text): a status line, a question and its
	// answers fit; a whole page (the bench's mails page: 1,166 to 1,416
	// runes, the owner's longer) is a task's work. It holds the turn's
	// node_write and node_edit together too: a page split in pieces is one.
	brainWriteRunes = 800
	// brainNodeWrites is how many node_write or node_edit a turn makes per
	// message it answers: the node of the turn and its parent, a card
	// placed. A series of edits is a rewrite: a task's.
	brainNodeWrites = 3
	// brainItemNotes is how many items a turn notes per message: a burst
	// (a whole inbox) is a skill's run or a task's.
	brainItemNotes = 3
	// brainSkillWrites: one skill proposed per turn, never a series.
	brainSkillWrites = 1
	// brainGestures is how many counted calls (the writes above, a
	// memory, a notification, a rule, a node's properties, a catalog call
	// that is not a read) a turn makes per message: a gesture is one or
	// two calls, a few for a turn that answers a card.
	brainGestures = 6
	// brainBudgetScale is the most a delivery multiplies the budget by.
	brainBudgetScale = 3
)

// Tool classes for the budget. Each of the brain's tools has one
// (brainToolClass); a tool added without one is counted, and
// TestBrainBudgetClassifiesTools says so.
const (
	budgetRead   = "read"   // never counted
	budgetRoute  = "route"  // routes or launches: never counted (node_open's size is held)
	budgetWrite  = "write"  // a node written: size held, counted as a node write
	budgetNote   = "note"   // item_note
	budgetSkill  = "skill"  // write_skill
	budgetCounts = "counts" // any other change
)

var brainToolClass = map[string]string{
	// Reads.
	"list_agents": budgetRead, "list_devices": budgetRead, "list_tools": budgetRead, "list_folders": budgetRead,
	"list_repos": budgetRead, "task_status": budgetRead, "list_tasks": budgetRead, "recall": budgetRead,
	"intentions": budgetRead, "learned": budgetRead, "read_attachment": budgetRead, "list_skills": budgetRead,
	"read_skill": budgetRead, "skill_runs": budgetRead, "monitors": budgetRead, "node_find": budgetRead,
	"node_read": budgetRead, "page_format": budgetRead, "notifications": budgetRead,
	"where_now": budgetRead, "since": budgetRead,
	// Routing and launching: the way out of a refusal.
	"run_task": budgetRoute, "answer_task": budgetRoute, "stop_task": budgetRoute, "node_open": budgetRoute,
	"node_show": budgetRoute, "turn_home": budgetRoute, "node_settle": budgetRoute, "withdraw_proposal": budgetRoute,
	// Writes.
	"node_write": budgetWrite, "node_edit": budgetWrite, "item_note": budgetNote, "write_skill": budgetSkill,
	"node_set": budgetCounts, "remember": budgetCounts, "update_memory": budgetCounts, "forget": budgetCounts,
	"intend": budgetCounts, "update_intention": budgetCounts, "unlearn": budgetCounts, "notify": budgetCounts,
	"rule": budgetCounts, "setting": budgetCounts, "write_monitor": budgetCounts, "try_monitor": budgetCounts,
}

// launchHint ends each refusal: what to do instead.
const launchHint = "launch a task on the node it is for: run_task with node, skill (list_skills, if one fits) and a one- or two-sentence ask; it reads the node itself. Then answer in one line naming the node."

// turnBudget is what a turn of the brain's spent.
type turnBudget struct {
	scale                               int // the messages the delivery brought, 1 to brainBudgetScale
	gestures, nodeWrites, notes, skills int
	runes                               int // of text written in nodes (node_write, node_edit)
}

// spent is what one call took from the budget, given back if it fails
// (refund): a stale write read again and made once counts once.
type spent struct {
	class string
	runes int
}

func newTurnBudget(messages int) turnBudget {
	return turnBudget{scale: min(max(messages, 1), brainBudgetScale)}
}

// charge counts a call of the brain's tool name with its arguments, or
// refuses it: the refusal's words, "" when it may run, and what it took.
func (b *turnBudget) charge(name string, args map[string]any) (string, spent) {
	class, ok := brainToolClass[name]
	if !ok {
		class = budgetCounts
	}
	b.scale = max(b.scale, 1)
	n := writtenRunes(name, args)
	switch {
	case class == budgetRead:
		return "", spent{}
	case name == "node_open" && n > brainWriteRunes:
		return fmt.Sprintf("Refused: node_open carries %d runes of text, more than %d: a whole page is a task's work. Open the node with a one-line body, then %s", n, brainWriteRunes, launchHint), spent{}
	case class == budgetRoute:
		return "", spent{}
	case class == budgetWrite && b.runes+n > brainWriteRunes*b.scale:
		return fmt.Sprintf("Refused: with this one, this turn would write %d runes in nodes, more than %d: writing a page is a task's work: %s", b.runes+n, brainWriteRunes*b.scale, launchHint), spent{}
	case b.gestures >= brainGestures*b.scale:
		return fmt.Sprintf("Refused: this turn already made %d changes, a gesture is one or two; more is work: %s", b.gestures, launchHint), spent{}
	case class == budgetWrite && b.nodeWrites >= brainNodeWrites*b.scale:
		return fmt.Sprintf("Refused: this turn already wrote nodes %d times; a series of writes is a rewrite: %s", b.nodeWrites, launchHint), spent{}
	case class == budgetNote && b.notes >= brainItemNotes*b.scale:
		return fmt.Sprintf("Refused: this turn already noted %d items; noting a whole source is a skill's run or a task's: %s", b.notes, launchHint), spent{}
	case class == budgetSkill && b.skills >= brainSkillWrites:
		return "Refused: one skill is proposed per turn; propose the next one in another turn, after the user's answer.", spent{}
	}
	s := spent{class: class}
	switch class {
	case budgetWrite:
		b.nodeWrites++
		b.runes += n
		s.runes = n
	case budgetNote:
		b.notes++
	case budgetSkill:
		b.skills++
	}
	b.gestures++
	return "", s
}

// chargeCatalog counts a catalog call of the brain's that is not a read
// (proxy.go): a change, as any other.
func (b *turnBudget) chargeCatalog() (string, spent) {
	b.scale = max(b.scale, 1)
	if b.gestures >= brainGestures*b.scale {
		return fmt.Sprintf("Refused: this turn already made %d changes, a gesture is one or two; more is work: %s", b.gestures, launchHint), spent{}
	}
	b.gestures++
	return "", spent{class: budgetCounts}
}

// refund gives back what a call that failed took: it changed nothing.
func (b *turnBudget) refund(s spent) {
	if s.class == "" {
		return
	}
	back := func(n *int, by int) { *n = max(*n-by, 0) }
	switch s.class {
	case budgetWrite:
		back(&b.nodeWrites, 1)
		back(&b.runes, s.runes)
	case budgetNote:
		back(&b.notes, 1)
	case budgetSkill:
		back(&b.skills, 1)
	}
	back(&b.gestures, 1)
}

// writtenRunes is the text a node write carries: what lands on the page.
// A work's steps (node_open's steps) are orchestration: not counted.
func writtenRunes(name string, args map[string]any) int {
	var keys []string
	switch name {
	case "node_open":
		keys = []string{"title", "goal", "body"}
	case "node_write":
		keys = []string{"body"}
	case "node_edit":
		keys = []string{"value", "new"}
	default:
		return 0
	}
	n := 0
	for _, k := range keys {
		n += textRunes(args[k])
	}
	return n
}

// textRunes counts the runes of a value: a string's, or an object's as
// JSON (a block).
func textRunes(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case string:
		return utf8.RuneCountInString(x)
	default:
		raw, _ := json.Marshal(x)
		return utf8.RuneCount(raw)
	}
}

// budgetMiddleware holds the brain to its budget on eriphe-core's own
// server (newMCPServer): every tool call passes here, and the brain's are
// counted (brainCall). Another client of that server (the owner's own,
// with the API token; a test in memory) is not the brain: not counted.
func (c *core) budgetMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || call.Params == nil || !c.brainCall(call.Extra) {
			return next(ctx, method, req)
		}
		var args map[string]any
		if len(call.Params.Arguments) > 0 {
			json.Unmarshal(call.Params.Arguments, &args) // invalid arguments: the tool says so
		}
		why, took := c.turn.charge(call.Params.Name, args)
		if why != "" {
			return toolError(why), nil
		}
		res, err := next(ctx, method, req)
		if r, ok := res.(*mcp.CallToolResult); err != nil || ok && r.IsError {
			c.turn.refund(took) // it changed nothing
		}
		return res, err
	}
}

// brainCallHeader marks a call the device relays for the brain's task
// (proxy.go): set by eriphe-core once the device and its task are known,
// never taken from the request.
const brainCallHeader = "Eriphe-Brain-Call"

// brainCall tells a call of the brain's: relayed by its device for its
// task (brainCallHeader), or made at self_url with the assistant's token
// (assistantToken), which only the brain holds.
func (c *core) brainCall(extra *mcp.RequestExtra) bool {
	if extra == nil || extra.Header == nil {
		return false
	}
	if extra.Header.Get(brainCallHeader) == "1" {
		return true
	}
	return c.token != "" && extra.Header.Get("Authorization") == "Bearer "+assistantToken(c.token)
}

// charge counts a call in the turn's budget (turnBudget.charge).
func (t *brainTurn) charge(name string, args map[string]any) (string, spent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.budget.charge(name, args)
}

// chargeCatalog counts a catalog change in the turn's budget.
func (t *brainTurn) chargeCatalog() (string, spent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.budget.chargeCatalog()
}

// refund gives back what a failed call took. A delivery since started a
// fresh budget: what it gives back is the new one's, never below nothing.
func (t *brainTurn) refund(s spent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.budget.refund(s)
}

// delivered starts the budget of a delivery of n messages.
func (t *brainTurn) delivered(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.budget = newTurnBudget(n)
}
