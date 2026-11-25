package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The conversation per node (#632). One brain, one queue: each message of
// the conversation belongs to a node, or to the home (""), in its node
// column.
//
//   - A message of the user's belongs to the node the bar talks to:
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

// moveHomeTurn moves a turn that answers the user at the home, typed in the
// workspace, into the node it opened or showed for its subject: the user's
// messages of the turn and what the brain wrote since belong to that node, the rest of its answer goes
// there (its cards with it), and the user's screen opens the node, the
// bar in it: the exchange goes on in the subject's page. A turn that
// answers in a node already, or no message of the user's (an event, a
// wake), or one said on a channel (its exchange is the home's, where the
// channel's next words land), moves nothing, and a node shown beside one
// the user talks to leaves the bar where it is (node_show.go). True
// when it moved.
func (c *core) moveHomeTurn(id string) bool {
	if id == "" || c.turn.talkNode() != "" || !c.turnAnswersUser() {
		return false
	}
	from, to := c.turn.span()
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE seq >= ? AND seq <= ? AND node = '' AND role = ? AND via = ''`, from, to, roleUser).Scan(&n)
	if n == 0 {
		return false
	}
	c.turn.answersIn(id) // first: what the brain writes from now on is the node's
	c.store.db.Exec(`UPDATE messages SET node = ? WHERE seq >= ? AND seq <= ? AND node = '' AND role = ? AND via = ''`, id, from, to, roleUser)
	c.store.db.Exec(`UPDATE messages SET node = ? WHERE seq > ? AND node = '' AND role IN (?, ?)`, id, to, roleBrain, roleError)
	c.showNode(id, byBrain, true)
	return true
}

// atHome makes the turn about no node, its answer as at the home's
// (turn_home): the message said in a node is about something else.
func (t *brainTurn) atHome() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.node, t.home = "", true
	t.save()
}

// answersAtHome tells a turn turn_home made about no node.
func (t *brainTurn) answersAtHome() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.home
}

// turnHome is turn_home: the user's message, said in a node's bar, is
// not about that node. The turn is about none from now: what it raises
// waits above the bar (unheldCards), a card it raised already for that
// node is no longer its, and nothing of it is filed there (node_net.go).
// Its lines stay in that node's bar, where the user reads them.
func (c *core) turnHome() (map[string]any, error) {
	talk := c.turn.talkNode()
	if !c.turnAnswersUser() || talk == "" {
		return map[string]any{"note": "Nothing to do: this turn is not about a node the user wrote in."}, nil
	}
	c.turn.atHome()
	xs, _ := c.cardsWhere(`seq = 0 AND turn = 1`)
	for _, x := range xs {
		c.store.db.Exec(`DELETE FROM node_cards WHERE node = ? AND ref = ?`, talk, x.Ref)
	}
	c.nodeMoved(talk, false)
	return map[string]any{"note": "This turn is about no node now, as at the home."}, nil
}

// showTurnCards opens the page of the node a card raised in a turn that
// answers the user waits in, beside what is open: what the user must
// answer is in front of them at once, never in a page they do not see.
// A card no node holds waits above the bar already.
func (c *core) showTurnCards(xs []convoCard) {
	if !c.turnAnswersUser() || c.nodes == nil {
		return
	}
	shown := map[string]bool{}
	for _, x := range xs {
		if x.State != cardPending {
			continue
		}
		for _, id := range c.cardNodes(x.Ref) {
			if sum, err := c.nodes.Get(id); err == nil && sum.Archived.IsZero() {
				if !shown[id] {
					shown[id] = true
					c.showNode(id, byBrain, false)
				}
				break
			}
		}
	}
}

// turnAnswersUser tells a turn that answers messages of the user's, not
// events or a wake only: what it opens is shown to them.
func (c *core) turnAnswersUser() bool {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	return c.turn.asked
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

// nodeHead is how an event names a node: its id and its title, marked
// « titre extérieur » when it may be someone else's words; taint is then
// what quoting it makes the turn that reads the event (nodeRef), as
// node_read does.
func (c *core) nodeHead(id string) (head, taint string) {
	if c.nodes != nil {
		if sum, err := c.nodes.Get(id); err == nil {
			if sum.TitleExternal() {
				return fmt.Sprintf("[nœud %s, titre extérieur « %s »]", id, packQuote(sum.Title)), nodeRef(id)
			}
			return fmt.Sprintf("[nœud %s « %s »]", id, sum.Title), ""
		}
	}
	return fmt.Sprintf("[nœud %s]", id), ""
}

// The cards of a node: those raised in a turn about it, and those of its
// tasks. The node waits for the user while one of them waits
// (node_status.go). A decided card stays linked: a card block of the
// node's document still places it, and its page lists it as settled.

const nodeCardsSchema = `CREATE TABLE IF NOT EXISTS node_cards (
	node    TEXT NOT NULL,
	ref     TEXT NOT NULL,
	created INTEGER NOT NULL,
	PRIMARY KEY (node, ref)
);
CREATE INDEX IF NOT EXISTS node_cards_ref ON node_cards (ref);`

func init() { extraSchema = append(extraSchema, nodeCardsSchema) }

// cardRaisedFor links a card raised for a node to it (cardNodeOf): the
// node waits for the user while it waits.
func (c *core) cardRaisedFor(source, ref string) {
	id := c.cardNodeOf(source, ref)
	if id == "" || c.nodes == nil {
		return
	}
	sum, err := c.nodes.Get(id)
	if err != nil || !sum.Archived.IsZero() {
		return
	}
	c.store.db.Exec(`INSERT OR IGNORE INTO node_cards (node, ref, created) VALUES (?, ?, ?)`, id, ref, time.Now().Unix())
	c.nodeMoved(id, false)
}

// cardNodeOf is the node a card is raised for: the one its proposal
// belongs to (proposalNode: its calls', its run's, its task's), else its
// source's, node:<id> or a task's (task:<id>) that works in a node; ""
// for none.
func (c *core) cardNodeOf(source, ref string) string {
	if kind, n, ok := parseProposalRef(ref); ok {
		if id := c.proposalNode(kind, n); id != "" {
			return id
		}
	}
	kind, id, _ := strings.Cut(source, ":")
	switch kind {
	case refNode:
		return id
	case "task":
		return c.taskNode(id)
	}
	return ""
}

// proposalNode is the node a proposal belongs to by its records, whoever
// raised it (its kind's node, proposal_kinds.go): a task's access, a
// run's calls, an item's; "" for any other.
func (c *core) proposalNode(kind string, id int64) string {
	if k, ok := kindsOf[kind]; ok && k.node != nil {
		return k.node(c, id)
	}
	return ""
}

// cardNodes are the nodes a card is linked to, the last linked first.
func (c *core) cardNodes(ref string) []string {
	return c.store.column(`SELECT node FROM node_cards WHERE ref = ? ORDER BY created DESC, node`, ref)
}

// nodeCardRefs are the cards linked to a node, waiting or not, the last
// linked first.
func (c *core) nodeCardRefs(id string) []string {
	return c.store.column(`SELECT ref FROM node_cards WHERE node = ? ORDER BY created DESC, ref`, id)
}

// column is the one column a query gives, row by row; nil when it fails.
func (s *Store) column(query string, args ...any) []string {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// nodeHasCard tells a card linked to node id, waiting or not: one raised
// for it, which a card block of its document may place (nodeGuard).
func (c *core) nodeHasCard(id, ref string) bool {
	if id == "" {
		return false
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM node_cards WHERE node = ? AND ref = ?`, id, ref).Scan(&n)
	return n > 0
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
// node's. It is the user's act (nodeEvent): what the brain says of it
// answers them.
func (c *core) callToBrain(a accessQuestion, text, taint string) {
	node := a.Node
	if ids := c.cardNodes(askRef(a)); node == "" && len(ids) > 0 {
		node = ids[0]
	}
	c.nodeEvent(node, text, true, taint)
}

// brainRunTask starts the task the brain asks for, in a node of its own,
// a child of the node it names, else of the turn's node (at the top when
// the turn is about none, or its node is archived): what it does is
// written in the graph, under what it is for. Its start's card goes with
// the brain's message, it is tainted by the turn, it is about the node it
// works under, and its end reaches the brain as an event pointing to its
// node (task_node.go). Its prompt is built from the launch: the ask and
// the skill's method (task_launch.go).
func (c *core) brainRunTask(ctx context.Context, launch launchRequest) (map[string]any, error) {
	in, err := c.launchOf(launch)
	if err != nil {
		return nil, err
	}
	if err := c.taintedResources(in); err != nil { // resources.go, the skill's defaults included
		return nil, err
	}
	in.Node = strings.TrimSpace(in.Node)
	if in.Node == "" {
		if id := c.turn.aboutNode(); c.nodeParent(id) == nil {
			in.Node = id
		}
	}
	if in.Node != "" {
		in.about = nodeRef(in.Node)
	}
	in.askStart, in.byBrain, in.taint = true, true, c.turnTaint()
	t, err := c.runTask(ctx, in)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"task": t.ID, "state": t.State, "start_state": c.startState(t)} // task_start.go
	if node := c.taskNode(t.ID); node != "" {
		out["node"] = node
		// The turn's answer has its place: the node the task writes, as a
		// node the brain wrote does; its ack is a line (node_net.go).
		c.turn.wrote()
	}
	if launch.Skill != "" {
		// What it is lent, the skill's defaults or the call's (task_launch.go).
		lent := []string{}
		for _, e := range t.Resources {
			lent = append(lent, e.String())
		}
		out["lent"] = lent
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
