package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
)

// Taint: what the brain read in its current turn. A turn starts when
// eriphe-core delivers it a message of mine (tendBrain); a call the brain
// makes to an external catalog server, which eriphe-core proxies, taints it.
// A delivery of events only does not start a clean turn: what the brain
// read is still in its context, so the turn goes on and its taint with it,
// until I am back in the loop.

type brainTurn struct {
	mu        sync.Mutex
	from, to  int // the seqs of the messages delivered in this turn
	eventSeq  int // the brain task's last event when the turn started
	tainted   []string
	node      string               // the node the turn is about: the last one it named (node_turn.go)
	talk      string               // the node its answer belongs to, "" for the home (node_turn.go)
	filed     bool                 // the brain wrote a node in this turn: its answer needs no filing (node_net.go)
	answer    int                  // its answer is the brain's messages past this seq; 0: no answer to file (node_net.go)
	asked     bool                 // it answers messages of the user's, not events or a wake only (node_turn.go)
	home      bool                 // the user.s message, said in a node, is not about it: the turn answers as at the home (node_turn.go)
	namedRefs []string             // the entities my messages of the turn name (names.go)
	keep      func(brainTurnState) // stores it, so a restart of eriphe-core does not forget it
	budget    turnBudget           // what this delivery spent: never kept (brain_budget.go)
}

// brainTurnState is the turn as the store keeps it (kv brain_turn).
type brainTurnState struct {
	From     int      `json:"from"`
	To       int      `json:"to"`
	EventSeq int      `json:"event_seq"`
	Tainted  []string `json:"tainted,omitempty"`
	Node     string   `json:"node,omitempty"`
	Talk     string   `json:"talk,omitempty"`
	Filed    bool     `json:"filed,omitempty"`
	Answer   int      `json:"answer,omitempty"`
	Named    []string `json:"named,omitempty"`
	Asked    bool     `json:"asked,omitempty"`
	Home     bool     `json:"home,omitempty"`
}

// span is the seqs of the messages delivered in this turn; 0, 0 if none.
func (t *brainTurn) span() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.from, t.to
}

// taintUnknown taints a brain's turn that eriphe-core cannot tell about,
// started before it was kept: until my next message, it is as if it read
// something.
const taintUnknown = "eriphe-core.restarted"

// load restores the kept turn. Without one while a brain runs, the turn
// is tainted: what it read before the restart is not known.
func (t *brainTurn) load(raw string, brainRuns bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var st brainTurnState
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil {
		if brainRuns {
			t.tainted = []string{taintUnknown}
		}
		return
	}
	t.from, t.to, t.eventSeq, t.tainted, t.node, t.talk, t.filed, t.namedRefs, t.answer, t.asked, t.home = st.From, st.To, st.EventSeq, st.Tainted, st.Node, st.Talk, st.Filed, st.Named, st.Answer, st.Asked, st.Home
}

// save is called with t.mu held.
func (t *brainTurn) save() {
	if t.keep != nil {
		t.keep(brainTurnState{t.from, t.to, t.eventSeq, append([]string(nil), t.tainted...), t.node, t.talk, t.filed, t.answer, append([]string(nil), t.namedRefs...), t.asked, t.home})
	}
}

func (t *brainTurn) start(from, to, eventSeq int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.from, t.to, t.eventSeq, t.tainted, t.node, t.talk, t.filed, t.namedRefs = from, to, eventSeq, nil, "", "", false, nil
	t.answer = to // what it writes past the messages delivered answers them; none delivered, nothing to file
	t.asked = from != 0
	t.home = false
	t.budget = newTurnBudget(1)
	t.save()
}

// goOn extends the turn over a delivery of events only (or a resumed
// session): it keeps its taint, carried adds what the events showed so far
// (turnTaint, read before eventSeq moves on), and my messages of the turn
// stay its own for the memory.
func (t *brainTurn) goOn(from, to, eventSeq int, carried []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.from == 0 {
		t.from = from
	}
	t.to, t.eventSeq = max(t.to, to), eventSeq
	t.filed = false // a new answer: the net looks at it alone (node_net.go)
	t.answer = to   // past what it was just given; given nothing, nothing to file
	t.asked = false // events or a wake: no request of the user's to answer now
	t.home = false
	t.budget = newTurnBudget(1) // a delivery: a fresh budget (brain_budget.go)
	for _, why := range carried {
		if !containsStr(t.tainted, why) {
			t.tainted = append(t.tainted, why)
		}
	}
	t.save()
}

// answersUser makes the turn gone on over events answer the user: one of
// them tells what came of their Oui or Non on a card (nodeEvent). It
// keeps its taint and its context: only who it answers changes.
func (t *brainTurn) answersUser() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asked = true
	t.save()
}

func (t *brainTurn) taint(why string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !containsStr(t.tainted, why) {
		t.tainted = append(t.tainted, why)
		t.save()
	}
}

func (t *brainTurn) snapshot() (from, to, eventSeq int, tainted []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.from, t.to, t.eventSeq, append([]string(nil), t.tainted...)
}

// calledCatalog is the proxy's hook: a task called a catalog tool. A tool
// not marked trusted (tool_trust.go) taints the brain's turn, or the task
// that called it (taskRead).
func (c *core) calledCatalog(taskID, server, tool string) {
	if taskID == "" {
		return
	}
	if taskID == c.brainTaskID() {
		c.brainCalledCatalog(taskID, server, tool)
		return
	}
	if !c.trustsServer(server) {
		c.taskRead(taskID, claudeToolName(server, tool))
	}
}

// brainCalledCatalog taints the brain's turn with a call to a tool not
// trusted.
func (c *core) brainCalledCatalog(taskID, server, tool string) {
	if taskID == "" || taskID != c.brainTaskID() {
		return
	}
	if c.trustsServer(server) {
		return
	}
	c.turn.taint(server + "." + tool)
}

// turnTaint is why the brain's current turn is tainted, if it is: the
// proxy's record, and the turn's own events for its MCP tools (in case one
// reached its harness another way) and the web tools.
func (c *core) turnTaint() []string {
	return c.turnTaintBut(nil)
}

// turnTaintBut is turnTaint without the brain's calls still waiting for
// their answer (pending: how many per URL, or per catalog call: callKey): a
// call that has not run has read nothing yet, and calls asked together were
// chosen before any of them.
func (c *core) turnTaintBut(pending map[string]int) []string {
	pending = maps.Clone(pending)
	_, _, eventSeq, tainted := c.turn.snapshot()
	id := c.brainTaskID()
	if id == "" {
		return tainted
	}
	tainted, _ = c.eventsTaint(id, eventSeq, tainted, pending)
	return tainted
}

// eventsUnreadable taints what read a task whose events could not be read.
const eventsUnreadable = "events.unreadable"

// eventsTaint adds to tainted what a task's events after seq read: a
// catalog tool of a server not trusted, the web (but the page reads still
// pending). It returns the last event seen.
func (c *core) eventsTaint(id string, seq int, tainted []string, pending map[string]int) ([]string, int) {
	// Every event, page by page: a long turn has more than one.
	var events []EventRow
	for after := seq; ; {
		page, err := c.store.eventsAfter(id, after, 500)
		if err != nil {
			return append(tainted, eventsUnreadable), seq // not known: as if it read something
		}
		events = append(events, page...)
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].Seq
	}
	if len(events) > 0 {
		seq = events[len(events)-1].Seq
	}
	// The catalog calls of the turn refused at once (a card asks the user:
	// a yes runs them later, its result tainting as it comes): they never
	// ran, so they read nothing.
	asked, unrun := map[string]string{}, map[string]int{}
	for _, e := range events {
		switch e.Type {
		case "permission":
			if name, _ := e.Data["tool_name"].(string); strings.HasPrefix(name, "mcp__") {
				asked[fmt.Sprint(e.Data["id"])] = callKey(name, e.Data["input"])
			}
		case "permission_answer":
			if allow, _ := e.Data["allow"].(bool); !allow {
				if k, ok := asked[fmt.Sprint(e.Data["id"])]; ok {
					unrun[k]++
				}
			}
		}
	}
	trusted := c.trustedServers()
	for _, e := range events {
		if e.Type != "tool_use" {
			continue
		}
		name, _ := e.Data["name"].(string)
		if name == "" {
			name, _ = e.Data["tool_name"].(string)
		}
		switch {
		case strings.HasPrefix(name, "mcp__eriphe-core__"):
		case strings.HasPrefix(name, "mcp__"+nodesServer+"__"):
			// The graph: what a task reads there taints by its own record
			// (task_nodes.go).
		case providedCall(name):
			// Eriphe's own (the folders): what it reads taints by its own
			// record (folder_tools.go).
		case strings.HasPrefix(name, "mcp__"):
			if k := callKey(name, e.Data["input"]); pending[k] > 0 || unrun[k] > 0 {
				if pending[k] > 0 {
					pending[k]-- // waiting for its answer: not run yet
				} else {
					unrun[k]--
				}
				continue
			}
			server, _, _ := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
			if !trusted[server] && !containsStr(tainted, name) {
				tainted = append(tainted, name)
			}
		case name == "WebFetch" || name == "WebSearch":
			if u := webURL(e.Data["input"]); name == "WebFetch" && pending[u] > 0 {
				pending[u]--
				continue
			}
			if !containsStr(tainted, name) {
				tainted = append(tainted, name)
			}
		}
	}
	return tainted, seq
}

// markBrainMessage records that a message of the brain was written in a
// tainted turn (taintKey): the extraction reads it as external content.
func (c *core) markBrainMessage(seq int) {
	if tainted := c.turnTaint(); len(tainted) > 0 {
		c.store.setMsgTaint(seq, "brain:"+strings.Join(tainted, ","))
	}
}

// The taint also limits what the brain does, not only what it remembers: a
// tainted turn may have been talked into anything by what it read. It may
// not allow a permission (the request becomes mine to answer) nor lend a
// task anything but believed reads. My own message of the same turn does
// not lift this.

// ownerMessage reads a message I wrote, from a source_ref "msg:N" (or "#N"):
// never one that may not be my words (fromUntrusted: an untrusted
// channel's, a button of a block marked external).
func (c *core) ownerMessage(ref string) (*Message, bool) {
	seq, ok := parseMsgRef(ref)
	if !ok {
		return nil, false
	}
	msgs, err := c.store.messagesAfter(seq-1, 1)
	if err != nil || len(msgs) == 0 || msgs[0].Seq != seq || msgs[0].Role != roleUser || c.fromUntrusted(seq) {
		return nil, false
	}
	return &msgs[0], true
}

func parseMsgRef(ref string) (int, bool) {
	ref = strings.TrimSpace(ref)
	s, ok := strings.CutPrefix(ref, "msg:")
	if !ok {
		s, ok = strings.CutPrefix(ref, "#")
	}
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}
