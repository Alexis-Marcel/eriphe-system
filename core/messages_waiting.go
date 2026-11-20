package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The messages that wait for Eriphe. A message the user writes
// while the brain is at work is kept, undelivered, and the brain's next
// idle hands it everything that waits, in one turn (deliverConversation,
// brain.go). Until then it is the user's still: the workspace's bar shows
// each above it, with a clock (exchangeEvent's waiting), and the user may
// withdraw one. A message withdrawn is deleted, its attachments with it:
// the brain never reads it, and the conversation it sees keeps no trace of
// it; the log keeps a line.
//
// Withdrawing races the delivery: both hold c.delivering, so a message is
// either in the turn the brain is given or withdrawn, never both. A
// message whose plain yes or no already decided a card (resolveByText,
// convo_cards.go) has acted: it cannot be withdrawn.

var (
	errMessageTaken = errors.New("Eriphe already has this message")
	errNoMessage    = errors.New("no such message of yours")
)

// waitingFresh is how long a message waits before the bar shows it as
// waiting while Eriphe is not at work: an idle brain takes it at once,
// and a clock that comes and goes would only blink.
const waitingFresh = 2 * time.Second

// waitingLine is a message of the user's that waits for Eriphe, as the
// bar shows it: its seq, its text, and the node it was said to, with that
// node's title.
type waitingLine struct {
	Seq   int    `json:"seq"`
	Text  string `json:"text"`
	Node  string `json:"node,omitempty"`
	Title string `json:"title,omitempty"`
}

// waitingMessages are the user's messages not yet delivered to the brain,
// oldest first (the first run's own excepted).
func (s *Store) waitingMessages() ([]Message, error) {
	return s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE delivered = 0 AND role = ? AND via != ? ORDER BY seq`, roleUser, firstRunVia)
}

// waiting is what the bar shows waiting: the user's messages not yet
// delivered, all of them while Eriphe is at work (thinking), else those
// older than waitingFresh.
func (c *core) waiting(thinking bool) []waitingLine {
	msgs, err := c.store.waitingMessages()
	if err != nil {
		return nil
	}
	out := []waitingLine{}
	for _, m := range msgs {
		if !thinking && time.Since(m.Time) < waitingFresh {
			continue
		}
		l := waitingLine{Seq: m.Seq, Text: truncate(m.Text, askedMax), Node: m.Node}
		if l.Text == "" && len(m.Attachments) > 0 {
			l.Text = m.Attachments[0].Name
		}
		if m.Node != "" {
			l.Title = c.shownNodeTitle(m.Node)
		}
		out = append(out, l)
	}
	return out
}

// waitingKey tells what the bar shows waiting, cheaply, for the stream to
// know it changed (web_sse.go): a delivery or a withdrawal adds no message.
func (c *core) waitingKey(thinking bool) string {
	rows, err := c.store.db.Query(`SELECT seq, time FROM messages WHERE delivered = 0 AND role = ? AND via != ? ORDER BY seq`, roleUser, firstRunVia)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var seq int
		var at int64
		if rows.Scan(&seq, &at) != nil || (!thinking && time.Since(time.Unix(at, 0)) < waitingFresh) {
			continue
		}
		b.WriteString(strconv.Itoa(seq))
		b.WriteByte(',')
	}
	return b.String()
}

// withdrawMessage deletes a message of the user's that the brain has not
// been given: errMessageTaken once it has (or once it decided a card),
// errNoMessage when there is none.
func (c *core) withdrawMessage(seq int) error {
	c.delivering.Lock()
	defer c.delivering.Unlock()
	if c.store.get(cardAnswerKey(seq)) != "" {
		return errMessageTaken // its yes or no decided a card already
	}
	atts, err := c.store.withdrawMessage(seq)
	if err != nil {
		return err
	}
	c.store.removeAttachments(atts)
	log.Printf("message #%d withdrawn by the user before Eriphe read it", seq)
	return nil
}

// withdrawMessage deletes message seq, the user's and undelivered, with
// its attachments' rows and its taint, at once: the attachments it had,
// for their files.
func (s *Store) withdrawMessage(seq int) ([]Attachment, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var delivered bool
	switch err := tx.QueryRow(`SELECT delivered FROM messages WHERE seq = ? AND role = ?`, seq, roleUser).Scan(&delivered); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, errNoMessage
	case err != nil:
		return nil, err
	case delivered:
		return nil, errMessageTaken
	}
	rows, err := tx.Query(`SELECT id, seq, name, type, size FROM attachments WHERE seq = ?`, seq)
	if err != nil {
		return nil, err
	}
	var atts []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.Seq, &a.Name, &a.Type, &a.Size); err != nil {
			rows.Close()
			return nil, err
		}
		atts = append(atts, a)
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM attachments WHERE seq = ?`, seq); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE seq = ? AND role = ? AND delivered = 0`, seq, roleUser); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM kv WHERE key = ?`, taintKey(seq)); err != nil {
		return nil, err
	}
	return atts, tx.Commit()
}

// handleWithdraw is DELETE /messages/{seq}: the bar's ✕ on a message that
// waits. 409 once Eriphe has it.
func (w *web) handleWithdraw(rw http.ResponseWriter, r *http.Request, s *session) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq <= 0 {
		http.Error(rw, "bad message", http.StatusBadRequest)
		return
	}
	switch err := w.c.withdrawMessage(seq); {
	case errors.Is(err, errMessageTaken):
		http.Error(rw, err.Error(), http.StatusConflict)
	case errors.Is(err, errNoMessage):
		http.Error(rw, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	default:
		w.notify()
		rw.WriteHeader(http.StatusNoContent)
	}
}
