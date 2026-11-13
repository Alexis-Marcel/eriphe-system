package main

import (
	"time"
)

// The workspace's view of the conversation (#455): not a second chat, the
// last exchange only, near its input. The user's last message and what
// Eriphe said after it, the message being written, and whether Eriphe is at
// work; the conversation (convoPath) has the rest. The workspace's stream
// (/events?a2ui=1, web_sse.go) sends it as JSON, its "exchange" event, when
// it connects and whenever it changes: a reconnecting page gets it whole.
//
// The brain's text is its own, shown as Markdown by the page's renderer,
// which makes no HTML of it; an event's line (an external source's) and an
// error's raw text are never sent.

const (
	exchangeReplies = 3    // Eriphe's last lines after the user's message
	exchangeTextMax = 4000 // the page folds a long one; the conversation has it whole
)

// exchangeLine is a message of the exchange: the user's (with where they
// said it, when not here or in the conversation), Eriphe's, or an error,
// without its text: the page says Eriphe could not answer.
type exchangeLine struct {
	Seq  int       `json:"seq"`
	Role string    `json:"role"` // user, brain or error
	Text string    `json:"text,omitempty"`
	Via  string    `json:"via,omitempty"`
	Time time.Time `json:"time"`
}

// exchangeEvent is the workspace's "exchange" event.
type exchangeEvent struct {
	Lines    []exchangeLine `json:"lines"`
	Draft    string         `json:"draft,omitempty"`    // Eriphe's message being written
	Thinking bool           `json:"thinking,omitempty"` // Eriphe is at work
}

// lastExchange is the user's last message and Eriphe's lines after it
// (the last exchangeReplies); with no message of the user's, Eriphe's last
// line. Empty when the conversation is.
func (s *Store) lastExchange() ([]exchangeLine, error) {
	var from int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role = ? AND via != ?`, roleUser, firstRunVia).Scan(&from)
	if from == 0 {
		s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role IN (?, ?)`, roleBrain, roleError).Scan(&from)
	}
	if from == 0 {
		return []exchangeLine{}, nil
	}
	out := []exchangeLine{}
	mine, err := s.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE seq = ? AND role = ?`, from, roleUser)
	if err != nil {
		return nil, err
	}
	for _, m := range mine {
		l := exchangeLine{Seq: m.Seq, Role: m.Role, Text: truncate(m.Text, exchangeTextMax), Time: m.Time}
		if m.Via != viaWorkspaceAsk {
			l.Via = m.Via
		}
		out = append(out, l)
	}
	// The last few, however many events came between.
	replies, err := s.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE seq >= ? AND role IN (?, ?) ORDER BY seq DESC LIMIT ?`,
		from, roleBrain, roleError, exchangeReplies)
	if err != nil {
		return nil, err
	}
	for i := len(replies) - 1; i >= 0; i-- {
		m := replies[i]
		l := exchangeLine{Seq: m.Seq, Role: m.Role, Time: m.Time}
		if m.Role == roleBrain {
			l.Text = truncate(m.Text, exchangeTextMax)
		}
		out = append(out, l)
	}
	return out, nil
}
