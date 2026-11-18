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
// answer lives in the conversation alone. At its turn's end, the turn's
// answer (its texts joined), past netMinRunes, when the brain wrote no
// node in the turn (node_open, node_write, node_edit), goes:
//
//   - into the node the turn answers in (talkNode): whole in its trace,
//     and as its doc when it has none (never a standing node's);
//   - else into a new work node, titled from the user's request (its
//     first line), the answer as its doc.
//
// The answer links to it (messages.filed). Written in a tainted turn, the
// node is external. Code opens no other node: « Ma journée » is a
// standing node the brain opens like any other. A refusal (a secret, the
// size) never loses the answer: it stays in the conversation, unfiled.

// netMinRunes is the length past which an answer is filed: under it, an
// answer is a line or two, an action done, a short answer.
const netMinRunes = 600

// netTitleMax is the most runes of a title made from the request.
const netTitleMax = 80

// fileTurnAnswer is the safety net, at the end of the brain's turn.
func (c *core) fileTurnAnswer() {
	if c.nodes == nil || c.turn.wroteNode() {
		return
	}
	from, to := c.turn.span()
	talk := c.turn.talkNode()
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND seq > ? AND node = ? AND filed = '' ORDER BY seq`,
		roleBrain, to, talk)
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
	if runeLen(answer) < netMinRunes {
		return
	}
	tainted := len(c.turnTaint()) > 0
	id := c.fileInTalked(talk, answer, tainted)
	if id == "" {
		id = c.fileInNew(c.netTitle(from, to), answer, tainted)
	}
	if id == "" {
		return
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(seqs)), ", ")
	c.store.db.Exec(`UPDATE messages SET filed = ? WHERE seq IN (`+marks+`)`, append([]any{id}, seqs...)...)
}

// fileInTalked files an answer into the node the turn answers in: its
// trace, and its doc when it has none. It gives the node, "" when there
// is none to file in or it refused.
func (c *core) fileInTalked(id, answer string, tainted bool) string {
	if id == "" {
		return ""
	}
	sum, err := c.nodes.Get(id)
	if err != nil || !sum.Archived.IsZero() {
		return ""
	}
	for _, part := range traceParts(answer, nodestore.MaxTrace) {
		if err := c.nodes.AppendTrace(id, byBrain, part); err != nil {
			log.Printf("node %s: the brain's answer not filed in its trace: %v", id, err)
			return ""
		}
	}
	if tainted {
		c.markNodeExternal(id, byBrain)
	}
	d, err := c.nodes.Read(id, 0)
	if err == nil && strings.TrimSpace(d.Node.Text()) == "" && len(d.Node.Blocks()) == 0 && d.Node.Meta.Lifetime != nodefile.Standing {
		if _, err := c.authorWrite(id, d.Version.Hash, byBrain, tainted, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(answer) }); err != nil {
			log.Printf("node %s: the brain's answer kept in its trace, not as its doc: %v", id, err)
		}
	}
	return id
}

// fileInNew files an answer into a new work node at the top: the answer
// as its doc (in its trace when the doc refuses it). It gives the node,
// "" when the store refused it.
func (c *core) fileInNew(title, answer string, tainted bool) string {
	nw := nodestore.New{Title: title, Body: answer, External: tainted, Author: byBrain}
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

// netTitle is a filed answer's node's title: the first line of the user's
// request of the turn (its messages from..to), never of the answer nor of
// an event; a neutral one when there is none, or when it may not be the
// user's words.
func (c *core) netTitle(from, to int) string {
	neutral := fmt.Sprintf(tr(c.userLang(), "node.net.title"), time.Now().Format("2006-01-02 15:04"))
	if from == 0 {
		return neutral
	}
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND seq >= ? AND seq <= ? ORDER BY seq LIMIT 1`, roleUser, from, to)
	if err != nil || len(msgs) == 0 || c.fromUntrusted(msgs[0].Seq) {
		return neutral
	}
	title := firstLine(msgs[0].Text)
	if title == "" || scanSecrets(title) != "" {
		return neutral
	}
	return truncate(title, netTitleMax)
}
