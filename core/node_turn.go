package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The conversation per node (#632). One brain, one queue: each message of
// the conversation belongs to a node, or to the home (""), in its node
// column.
//
//   - A message of the user's belongs to the node the console talks to:
//     the page sends it with the message (node), checked when it is
//     written (nodeSaid). The brain reads it named (messageRef: « · nœud
//     <id> »).
//   - The brain's answer belongs to the node of what it answers, fixed at
//     delivery (talkOf): the last message of the user's, else the last
//     event of a node; the home for anything else. A failure of the turn
//     is shown there too.
//   - An event of a node (a task's end, a card of it decided) belongs to
//     it.
//
// The node a turn is about is another thing: the last node it named (a
// node tool called on it, the event of one delivered, the node the user
// talks to). What the turn raises for the user then (the card of one of
// its calls, a task's start) is that node's: linked to it (node_cards),
// and it waits for the user while one waits.

// about records the node the turn is about now.
func (t *brainTurn) about(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.node != id {
		t.node = id
		t.save()
	}
}

// aboutNode is the node the turn is about; "" if none.
func (t *brainTurn) aboutNode() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.node
}

// answersIn sets the node the turn's answer belongs to.
func (t *brainTurn) answersIn(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.talk != id {
		t.talk = id
		t.save()
	}
}

// talkNode is the node the turn's answer belongs to; "" for the home.
func (t *brainTurn) talkNode() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.talk
}

// wrote notes that the brain's answer of this turn has its place: it
// wrote a node, or proposed something on a card (node_net.go).
func (t *brainTurn) wrote() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.filed {
		t.filed = true
		t.save()
	}
}

// answers sets where the turn's answer starts: the brain's messages past
// seq, the last message when it was woken (node_net.go).
func (t *brainTurn) answers(seq int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.answer = seq
	t.save()
}

// takeAnswer gives where the turn's answer starts, once: at its end the
// answer is looked at, and a later end with nothing delivered since (a
// restarted task's first turn) has none. 0: no answer to file.
func (t *brainTurn) takeAnswer() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	after := t.answer
	if after != 0 {
		t.answer = 0
		t.save()
	}
	return after
}

// wroteNode tells whether the brain's answer of this turn has its place:
// a node it wrote, or a card it proposed.
func (t *brainTurn) wroteNode() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.filed
}

// nodesDelivered sets the node the turn is about from what it was given:
// the last message that belongs to a node; none otherwise. A new delivery
// never keeps the node of the turn before.
func (c *core) nodesDelivered(msgs []Message) {
	id := ""
	for _, m := range msgs {
		if m.Node != "" {
			id = m.Node
		}
	}
	c.turn.about(id)
}

// talkOf is the node the answer to a delivery belongs to: the last
// message of the user's, whatever its node ("": the home); without one,
// the last event of a node; else the home.
func talkOf(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == roleUser {
			return msgs[i].Node
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Node != "" {
			return msgs[i].Node
		}
	}
	return ""
}

// moveHomeTurn moves a turn that answers the user at the home into the
// node it opened for its subject: the user's messages of the turn and what
// the brain wrote since belong to that node, the rest of its answer goes
// there (its cards with it), and the user's screen opens the node, the
// console in it: the exchange goes on in the subject's page. A turn that
// answers in a node already, or no message of the user's (an event, a
// wake), moves nothing, and a node shown beside one the user talks to
// leaves the console where it is (node_show.go). True when it moved.
func (c *core) moveHomeTurn(id string) bool {
	if id == "" || c.turn.talkNode() != "" {
		return false
	}
	from, to := c.turn.span()
	if from == 0 {
		return false
	}
	res, err := c.store.db.Exec(`UPDATE messages SET node = ? WHERE seq >= ? AND seq <= ? AND node = '' AND role = ? AND via != ?`,
		id, from, to, roleUser, firstRunVia)
	if n, _ := rowsOf(res, err); n == 0 {
		return false
	}
	c.store.db.Exec(`UPDATE messages SET node = ? WHERE seq > ? AND node = '' AND role IN (?, ?)`, id, to, roleBrain, roleError)
	c.turn.answersIn(id)
	c.showNode(id, byBrain, true)
	return true
}

// turnAnswersUser tells a turn that answers a message of the user's, not
// an event or a wake only: what it opens is shown to them.
func (c *core) turnAnswersUser() bool {
	from, to := c.turn.span()
	if from == 0 {
		return false
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE seq >= ? AND seq <= ? AND role = ? AND via != ?`, from, to, roleUser, firstRunVia).Scan(&n)
	return n > 0
}

// turnNode notes that the brain's turn named a node, by a tool of its own
// on it; an id that is no node changes nothing.
func (c *core) turnNode(id string) {
	if id == "" || c.nodes == nil {
		return
	}
	if _, err := c.nodes.Get(id); err == nil {
		c.turn.about(id)
	}
}

// turnSource is who raises a card in the brain's turn: the node it is
// about (node:<id>), or "" when it is about none.
func (c *core) turnSource() string {
	if id := c.turn.aboutNode(); id != "" {
		return nodeRef(id)
	}
	return ""
}

// nodeSaid is the node the page names with a message the user writes:
// one of the graph's, not archived; "" for the home or anything else.
func (c *core) nodeSaid(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || c.nodes == nil || !nodefile.IDOK(id) {
		return ""
	}
	if sum, err := c.nodes.Get(id); err == nil && sum.Archived.IsZero() {
		return id
	}
	return ""
}

// nodeHead is how an event names a node: its id and, unless what it says
// may be someone else's words, its title.
func (c *core) nodeHead(id string) string {
	if c.nodes != nil {
		if sum, err := c.nodes.Get(id); err == nil && !sum.External {
			return fmt.Sprintf("[nœud %s « %s »]", id, sum.Title)
		}
	}
	return fmt.Sprintf("[nœud %s]", id)
}

// The cards of a node: those raised in a turn about it, and those of its
// tasks. The node waits for the user while one of them waits.

const nodeCardsSchema = `CREATE TABLE IF NOT EXISTS node_cards (
	node    TEXT NOT NULL,
	ref     TEXT NOT NULL,
	created INTEGER NOT NULL,
	PRIMARY KEY (node, ref)
);
CREATE INDEX IF NOT EXISTS node_cards_ref ON node_cards (ref);`

func init() { extraSchema = append(extraSchema, nodeCardsSchema) }

// cardRaisedFor links a card raised for a node to it (its source is
// node:<id>): the node waits for the user, unless it is at work.
func (c *core) cardRaisedFor(source, ref string) {
	id, ok := strings.CutPrefix(source, "node:")
	if !ok || c.nodes == nil {
		return
	}
	sum, err := c.nodes.Get(id)
	if err != nil || !sum.Archived.IsZero() {
		return
	}
	c.store.db.Exec(`INSERT OR IGNORE INTO node_cards (node, ref, created) VALUES (?, ?, ?)`, id, ref, time.Now().Unix())
	if sum.State == nodestore.Idle {
		c.nodes.SetState(id, nodestore.Waiting)
	}
}

// linkedCardMoved tells the nodes a card is linked to that it was
// decided: one that waited for it alone, and for no work of its steps, is
// idle again.
func (c *core) linkedCardMoved(ref string) {
	if c.nodes == nil {
		return
	}
	for _, id := range c.cardNodes(ref) {
		sum, err := c.nodes.Get(id)
		if err != nil || sum.State != nodestore.Waiting || c.nodeCardWaits(id) {
			continue
		}
		if _, ok := c.work(id); ok {
			continue // its steps' engine says where it stands (node_steps.go)
		}
		c.nodes.SetState(id, nodestore.Idle)
	}
}

// cardNodes are the nodes a card is linked to, the last linked first.
func (c *core) cardNodes(ref string) []string {
	rows, err := c.store.db.Query(`SELECT node FROM node_cards WHERE ref = ? ORDER BY created DESC, node`, ref)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// nodeCardWaits tells a node one of whose linked cards waits for the
// user.
func (c *core) nodeCardWaits(id string) bool {
	rows, err := c.store.db.Query(`SELECT ref FROM node_cards WHERE node = ?`, id)
	if err != nil {
		return false
	}
	var refs []string
	for rows.Next() {
		var ref string
		if rows.Scan(&ref) == nil {
			refs = append(refs, ref)
		}
	}
	rows.Close() // one connection: the cards are read after
	for _, ref := range refs {
		if c.cardState(ref) == cardPending {
			return true
		}
	}
	return false
}

// cardState is where a card stands: pending, approved, rejected or gone.
func (c *core) cardState(ref string) string {
	if xs, err := c.cardsWhere(`ref = ?`, ref); err == nil && len(xs) == 1 {
		x, p := c.liveCard(xs[0])
		if x.State == cardPending && !p.Pending {
			return cardGone
		}
		return x.State
	}
	kind, pid, ok := parseProposalRef(ref)
	if !ok {
		return cardGone
	}
	p := c.proposalOf(kind, pid)
	if p.Pending {
		return cardPending
	}
	return cmpOr(p.Outcome, cardGone)
}

// callToBrain tells the brain how a call kept on its card went (run on a
// yes, or refused), taint being what its words make the turn. A card
// linked to a node makes it an event of that node: the turn that reads it
// is about the node (nodesDelivered), and what it raises next is the
// node's. Written delivered, its marks set, then handed to the brain, as
// tellBrain does: it is never delivered before its marks.
func (c *core) callToBrain(a accessQuestion, text, taint string) {
	node := ""
	if ids := c.cardNodes(proposalRef(cardAccess, a.ID)); len(ids) > 0 {
		node = ids[0]
		text = c.nodeHead(node) + " " + text
	}
	seq, err := c.store.addNodeMessage(roleEvent, text, "", node, true)
	if err != nil {
		return
	}
	if taint != "" {
		c.store.set(taintKey(int(seq)), taint)
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// brainRunTask starts the task the brain asks for, in a node of its own
// (a child of node, when it names one): its start's card goes with the
// brain's message, it is tainted by the turn, and its end reaches the
// brain as an event pointing to its node (task_node.go). A turn about a
// node makes the task about it.
func (c *core) brainRunTask(ctx context.Context, in runRequest) (map[string]any, error) {
	if err := c.taintedResources(in); err != nil { // resources.go
		return nil, err
	}
	if in.About == "" {
		in.About = c.turnSource()
	}
	in.askStart, in.byBrain, in.taint = true, true, c.turnTaint()
	t, err := c.runTask(ctx, in)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"task": t.ID, "state": t.State, "start_state": c.startState(t)} // task_start.go
	if node := c.taskNode(t.ID); node != "" {
		out["node"] = node
	}
	return out, nil
}

// taskEventNode is the node an event of a task belongs to: its own node,
// live; "" for none.
func (c *core) taskEventNode(task string) string {
	id := c.taskNode(task)
	if id == "" || c.nodes == nil {
		return ""
	}
	if sum, err := c.nodes.Get(id); err != nil || !sum.Archived.IsZero() {
		if err != nil {
			log.Printf("task %s: node %s: %v", task, id, err)
		}
		return ""
	}
	return id
}
