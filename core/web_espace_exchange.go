package main

import (
	"strings"
	"time"
)

// The workspace's view of the conversation (#455, #556): not a second
// chat, the present of the visit, in the left column. The visit's lines
// (the user's messages and Eriphe's since the user came back, the last
// few), the message being written, whether Eriphe is at work; and, for
// each thread the user talked with Eriphe about, its last exchanges
// (thread_talk.go), which the workspace shows in the thread's card. The
// conversation (convoPath) has the rest. The workspace's stream
// (/events?a2ui=1, web_sse.go) sends it as JSON, its "exchange" event, when
// it connects and whenever it changes: a reconnecting page gets it whole.
//
// The brain's text is its own, shown as Markdown by the page's renderer,
// which makes no HTML of it, and marked when written in a turn that read
// external content (markBrainMessage); an event's line (an external
// source's) and an error's raw text are never sent.

const (
	exchangeReplies     = 3    // Eriphe's last lines in a thread's exchange
	exchangeTextMax     = 4000 // the page folds a long one; the conversation has it whole
	exchangeVisitMax    = 12   // the visit's last lines
	exchangeTalks       = 3    // a thread's last exchanges
	exchangeTalkThreads = 12   // the threads talked about last
)

// exchangeLine is a message: the user's (with where they said it, when not
// here or in the conversation), Eriphe's, or an error, without its text:
// the page says Eriphe could not answer. Thread is the surface of the
// thread it is filed under; External, a line of Eriphe's written in a
// tainted turn.
type exchangeLine struct {
	Seq      int       `json:"seq"`
	Role     string    `json:"role"` // user, brain or error
	Text     string    `json:"text,omitempty"`
	Via      string    `json:"via,omitempty"`
	Time     time.Time `json:"time"`
	Thread   string    `json:"thread,omitempty"`
	External bool      `json:"external,omitempty"`
}

// exchangeTalk is an exchange about a thread: the user's message and
// Eriphe's lines of its turn.
type exchangeTalk struct {
	Lines []exchangeLine `json:"lines"`
}

// exchangeEvent is the workspace's "exchange" event.
type exchangeEvent struct {
	Lines       []exchangeLine            `json:"lines"`                 // the visit's
	Threads     map[string][]exchangeTalk `json:"threads,omitempty"`     // by thread surface, oldest first
	Draft       string                    `json:"draft,omitempty"`       // Eriphe's message being written
	DraftThread string                    `json:"draftThread,omitempty"` // the thread surface the draft answers about
	Thinking    bool                      `json:"thinking,omitempty"`    // Eriphe is at work
}

// exchangeOf is a message as the page gets it.
func (s *Store) exchangeOf(m Message) exchangeLine {
	l := exchangeLine{Seq: m.Seq, Role: m.Role, Time: m.Time}
	switch m.Role {
	case roleUser:
		l.Text = truncate(m.Text, exchangeTextMax)
		if m.Via != viaWorkspaceAsk {
			l.Via = m.Via
		}
	case roleBrain:
		l.Text = truncate(m.Text, exchangeTextMax)
		l.External = strings.HasPrefix(s.get(taintKey(m.Seq)), "brain:")
	}
	return l
}

// visitLines are the conversation's lines since the visit began, the
// last exchangeVisitMax: the user's (not the first run's), Eriphe's and
// errors, never an event's.
func (s *Store) visitLines(since time.Time) ([]exchangeLine, error) {
	var from int64
	if !since.IsZero() {
		from = since.Unix()
	}
	msgs, err := s.queryMessages(`SELECT seq, time, role, text, via FROM messages
		WHERE time >= ? AND (role IN (?, ?) OR (role = ? AND via != ?)) ORDER BY seq DESC LIMIT ?`,
		from, roleBrain, roleError, roleUser, firstRunVia, exchangeVisitMax)
	if err != nil {
		return nil, err
	}
	out := make([]exchangeLine, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		out = append(out, s.exchangeOf(msgs[i]))
	}
	return out, nil
}

// visitStart is when the user's visit began: back from their last
// absence; now while they are away (the page shows what was composed for
// their return); the beginning when they were never away.
func (c *core) visitStart(now time.Time) time.Time {
	a, ok := c.lastAbsence(now)
	switch {
	case !ok:
		return time.Time{}
	case a.Back.IsZero():
		return now
	}
	return a.Back
}

// workspaceExchange is the exchange event's visit and threads at now.
func (c *core) workspaceExchange(now time.Time) ([]exchangeLine, map[string][]exchangeTalk, error) {
	lines, err := c.store.visitLines(c.visitStart(now))
	if err != nil {
		return nil, nil, err
	}
	seqs := make([]int, len(lines))
	for i, l := range lines {
		seqs[i] = l.Seq
	}
	filed := c.talkOf(seqs)
	for i := range lines {
		lines[i].Thread = filed[lines[i].Seq]
	}
	threads, err := c.threadTalks()
	return lines, threads, err
}

// threadTalks are the last exchanges of the threads talked about last,
// by their surface, oldest first.
func (c *core) threadTalks() (map[string][]exchangeTalk, error) {
	rows, err := c.store.db.Query(`SELECT thread, turn FROM thread_talk GROUP BY thread, turn ORDER BY turn DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	type key struct {
		thread int64
		turn   int
	}
	var turns []key
	count := map[int64]int{}
	for rows.Next() {
		var k key
		if rows.Scan(&k.thread, &k.turn) != nil {
			continue
		}
		n, seen := count[k.thread]
		if (!seen && len(count) >= exchangeTalkThreads) || n >= exchangeTalks {
			continue
		}
		count[k.thread] = n + 1
		turns = append(turns, k)
	}
	rows.Close() // one connection: the messages are read after
	out := map[string][]exchangeTalk{}
	for i := len(turns) - 1; i >= 0; i-- {
		k := turns[i]
		msgs, err := c.store.queryMessages(`SELECT m.seq, m.time, m.role, m.text, m.via FROM thread_talk t JOIN messages m ON m.seq = t.seq
			WHERE t.turn = ? ORDER BY m.seq`, k.turn)
		if err != nil {
			return nil, err
		}
		sid := threadSurfaceID(k.thread)
		var talk exchangeTalk
		var replies []exchangeLine
		for _, m := range msgs {
			l := c.store.exchangeOf(m)
			l.Thread = sid
			switch m.Role {
			case roleUser:
				talk.Lines = append(talk.Lines, l)
			case roleBrain, roleError:
				replies = append(replies, l)
			}
		}
		if len(talk.Lines) == 0 {
			continue // the user's message is gone
		}
		if n := len(replies) - exchangeReplies; n > 0 {
			replies = replies[n:]
		}
		talk.Lines = append(talk.Lines, replies...)
		out[sid] = append(out[sid], talk)
	}
	return out, nil
}
