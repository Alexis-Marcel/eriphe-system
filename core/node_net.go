package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Code's safety net (#632, contract 5): a long answer of the brain's that
// nothing of its turn filed into a node is filed by code, so that no long
// answer lives in the conversation alone: beside a subject, the
// workspace's orb says only its first lines (fewLines, web_orb.go). At
// its turn's end, the turn's answer (its texts joined), past netMinRunes,
// or said in a node any answer longer than its first line (ackLine,
// #663), or any answer at all of a turn that answers no act of the
// user's (an event's, a wake's: the orb never says it, web_orb.go), when
// the brain wrote no
// node in the turn (node_open, node_write, node_edit), launched no task on
// one (run_task: the task writes it, #770) and proposed nothing on a card
// (what it says is then about the card, which waits under it),
// goes:
//
//   - into the node the turn answers in (talkNode): whole in its trace,
//     and as its doc when it has none;
//   - else into a new work node, titled from the user's request (its
//     first line), the answer as its doc; a turn that answers the user at
//     the home then moves into it, as one whose subject the brain opened
//     does (moveHomeTurn): the node opens in front of the user, the
//     bar in it.
//
// The turn's answer is only what the brain wrote since it was given
// something (a message, events, a due intention: brainTurn.answer), and it
// is looked at once: a turn's end with nothing delivered, such as a
// restarted task's first, files nothing, and no older message ever counts.
// The answer links to it (messages.filed). Written in a tainted turn, the
// node is external. Code opens no other node: « Ma journée » is a node
// like any other, written by the brain and maintained by a skill. A
// refusal (a secret, the size) never loses the answer: it stays in the
// conversation, unfiled.

// netMinRunes is the length past which an answer is filed: under it, an
// answer is a line or two, an action done, a short answer.
const netMinRunes = 600

// netTitleMax is the most runes of a title made from the request.
const netTitleMax = 80

// fileTurnAnswer is the safety net, at the end of the brain's turn.
func (c *core) fileTurnAnswer() {
	after := c.turn.takeAnswer()
	if c.nodes == nil || after == 0 || c.turn.wroteNode() {
		return
	}
	from, to := c.turn.span()
	talk := c.turn.talkNode()
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND seq > ? AND node = ? AND filed = '' ORDER BY seq`,
		roleBrain, after, talk)
	if err != nil || len(msgs) == 0 {
		return
	}
	var parts []string
	seqs := make([]any, 0, len(msgs))
	for _, m := range msgs {
		parts = append(parts, strings.TrimSpace(m.Text))
		seqs = append(seqs, m.Seq)
	}
	answer := strings.Join(parts, "\n\n")
	long := runeLen(answer) >= netMinRunes
	home := c.turn.answersAtHome()  // said in a node, about something else (turn_home): as at the home
	unasked := !c.turnAnswersUser() // an event's, a wake's: the orb never says it, its node keeps it
	_, cut := ackLine(answer)
	if !long && (talk == "" || home || !cut && !unasked) {
		return
	}
	tainted := len(c.turnTaint()) > 0
	id := ""
	if !home {
		id = c.fileInTalked(talk, answer, tainted, long || cut) // an unasked line: its trace only, never its doc
	}
	if id == "" && long {
		title, trusted := c.netTitle(from, to)
		id = c.fileInNew(title, trusted, answer, tainted)
		if !c.moveHomeTurn(id) && home && id != "" { // from the home: the exchange goes on in it (node_turn.go)
			c.showNode(id, byBrain, false)
		}
	}
	if id == "" {
		return
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(seqs)), ", ")
	c.store.db.Exec(`UPDATE messages SET filed = ? WHERE seq IN (`+marks+`)`, append([]any{id}, seqs...)...)
}

// fileInTalked files an answer into the node the turn answers in: its
// trace, and its doc when it has none and doc says so (not for a line
// said unasked). It gives the node, "" when there is none to file in or
// it refused.
func (c *core) fileInTalked(id, answer string, tainted, doc bool) string {
	if id == "" {
		return ""
	}
	sum, err := c.nodes.Get(id)
	if err != nil || !sum.Archived.IsZero() {
		return ""
	}
	traceErr, docErr := c.fileText(id, byBrain, answer, tainted, doc, c.nodeSourceOK())
	if traceErr != nil {
		log.Printf("node %s: the brain's answer not filed in its trace: %v", id, traceErr)
		return ""
	}
	if docErr != nil {
		log.Printf("node %s: the brain's answer kept in its trace, not as its doc: %v", id, docErr)
	}
	return id
}

// fileText files an author's long text (the brain's answer, a task's
// report) in node id, whole: in its trace, in as many entries as it
// takes, and as its doc when doc says so and it has none. Tainted,
// the node is marked external first: never words without the mark. It
// tells why the trace, or the doc, refused it.
func (c *core) fileText(id, author, text string, tainted, doc bool, sourceOK func(nodefile.Source) error) (traceErr, docErr error) {
	if tainted {
		c.markNodeExternal(id, author)
	}
	for _, part := range traceParts(text, nodestore.MaxTrace) {
		if err := c.nodes.AppendTrace(id, author, part); err != nil {
			return err, nil
		}
	}
	if !doc {
		return nil, nil
	}
	d, err := c.nodes.Read(id, 0)
	if err != nil || strings.TrimSpace(d.Node.Text()) != "" || len(d.Node.Blocks()) > 0 {
		return nil, nil
	}
	_, err = c.authorWrite(id, d.Version.Hash, author, tainted, sourceOK, func(n *nodefile.Node) error { return n.AuthorBody(text) })
	return nil, err
}

// fileInNew files an answer into a new work node at the top: the answer
// as its doc (in its trace when the doc refuses it); trusted, its title
// is the user's words, never marked external. It gives the node, "" when
// the store refused it.
func (c *core) fileInNew(title string, trusted bool, answer string, tainted bool) string {
	nw := nodestore.New{Title: title, Goal: tr(c.userLang(), "net.goal"), Body: answer, External: tainted, TitleTrusted: trusted, OpenedBy: c.turnOrigin(), Author: byBrain}
	d, err := c.nodes.Create(nw)
	if err != nil {
		nw.Body = "" // a block that does not hold, the size: the answer goes in its trace
		d, err = c.nodes.Create(nw)
		if err == nil {
			for _, part := range traceParts(answer, nodestore.MaxTrace) {
				if c.nodes.AppendTrace(d.Node.Meta.ID, byBrain, part) != nil {
					break
				}
			}
		}
	}
	if err != nil {
		log.Printf("nodes: the brain's answer not filed: %v", err)
		return ""
	}
	if err := c.externalSources(d, byBrain); err != nil {
		log.Printf("node %s: %v", d.Node.Meta.ID, err)
	}
	return d.Node.Meta.ID
}

// netTitle is a filed answer's node's title: the user's request
// (requestTitle), else a neutral one; trusted tells it is the user's
// words.
func (c *core) netTitle(from, to int) (title string, trusted bool) {
	if title, ok := c.requestTitle(from, to); ok {
		return title, true
	}
	return fmt.Sprintf(tr(c.userLang(), "node.net.title"), time.Now().Format("2006-01-02 15:04")), false
}

// requestTitle is the first line of the user's request of the turn (its
// messages from..to), never of an answer nor of an event; false when
// there is none, or when it may not be the user's words (an untrusted
// channel, a secret).
func (c *core) requestTitle(from, to int) (string, bool) {
	if from == 0 {
		return "", false
	}
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND seq >= ? AND seq <= ? ORDER BY seq LIMIT 1`, roleUser, from, to)
	if err != nil || len(msgs) == 0 || c.fromUntrusted(msgs[0].Seq) {
		return "", false
	}
	title := firstLine(msgs[0].Text)
	if title == "" || scanSecrets(title) != "" {
		return "", false
	}
	return truncate(title, netTitleMax), true
}
