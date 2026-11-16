package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The workspace's view of the conversation (#455): not a second chat, the
// last exchange only, in the left column. What the user says updates a
// thread or a surface at the centre, so rereading the conversation is
// rare: the user's last message and what Eriphe said after it, the message
// being written, and whether Eriphe is at work. The workspace's stream
// (/events?a2ui=1, web_sse.go) sends it as JSON, its "exchange" event, when
// it connects and whenever it changes: a reconnecting page gets it whole.
// The lines before it come on request, a few at a time
// (GET /api/espace/earlier); the conversation (convoPath) has them all.
//
// The conversation leads (#613): the exchange is the one of the thread
// open on the workspace (its lines belong to it, thread_talk.go), named in
// the event, the home's when none is. Eriphe's draft and work show in the
// exchange its turn answers in; its latest answer brings the replies it
// suggested (brain_suggest.go).
//
// The brain's text is its own, shown as Markdown by the page's renderer,
// which makes no HTML of it, and marked when written in a turn that read
// external content (markBrainMessage); an event's line (an external
// source's), the first run's messages and an error's raw text are never
// sent.

// The exchanges were filed per thread and shown in its card (#556) before
// the column kept to the last exchange.
const threadTalkGone = `DROP TABLE IF EXISTS thread_talk;
DELETE FROM kv WHERE key = 'thread_talk_turn';`

func init() { extraSchema = append(extraSchema, threadTalkGone) }

const (
	exchangeReplies = 12   // Eriphe's last lines after the user's message
	exchangeTextMax = 4000 // the page folds a long one; the conversation has it whole
	exchangeEarlier = 10   // the lines before, per request
)

// exchangeLine is a message: the user's (with where they said it, when not
// here or in the conversation), Eriphe's (External: written in a tainted
// turn), or an error, without its text: the page says Eriphe could not
// answer.
type exchangeLine struct {
	Seq      int       `json:"seq"`
	Role     string    `json:"role"` // user, brain or error
	Text     string    `json:"text,omitempty"`
	Via      string    `json:"via,omitempty"`
	Time     time.Time `json:"time"`
	External bool      `json:"external,omitempty"`
}

// exchangeEvent is the workspace's "exchange" event.
type exchangeEvent struct {
	Thread      int64          `json:"thread,omitempty"`      // the thread it belongs to; none: the home
	Title       string         `json:"title,omitempty"`       // that thread's title
	Lines       []exchangeLine `json:"lines"`                 // the last exchange
	Earlier     bool           `json:"earlier,omitempty"`     // lines come before it
	Draft       string         `json:"draft,omitempty"`       // Eriphe's message being written
	Thinking    bool           `json:"thinking,omitempty"`    // Eriphe is at work
	Suggestions []string       `json:"suggestions,omitempty"` // the replies its last line suggests
}

// exchangeFor is the workspace's exchange of a thread (0: the home), with
// Eriphe's draft and work when its turn answers there: talk is the thread
// it answers in.
func (w *web) exchangeFor(thread, talk int64, draft string, thinking bool) (exchangeEvent, error) {
	lines, earlier, err := w.c.store.lastExchange(thread)
	if err != nil {
		return exchangeEvent{}, err
	}
	x := exchangeEvent{Thread: thread, Lines: lines, Earlier: earlier}
	if s, err := w.c.thread(thread); thread > 0 && err == nil {
		x.Title = s.Title
	}
	waiting := len(lines) > 0 && lines[len(lines)-1].Role == roleUser
	if talk == thread {
		x.Draft = truncate(draft, exchangeTextMax)
	}
	x.Thinking = thinking && (talk == thread || waiting)
	if n := len(lines); n > 0 && lines[n-1].Role == roleBrain && !lines[n-1].External && x.Draft == "" {
		x.Suggestions = w.c.store.suggestions(lines[n-1].Seq)
	}
	return x, nil
}

// exchangeLines is the filter of the lines the page gets, with its
// arguments (exchangeArgs): the user's (not the first run's), Eriphe's and
// errors, never an event's, of one thread.
const exchangeLines = `thread = ? AND (role IN (?, ?) OR (role = ? AND via != ?))`

func exchangeArgs(thread int64, args ...any) []any {
	return append(args, thread, roleBrain, roleError, roleUser, firstRunVia)
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

// lastExchange is a thread's (0: the home's) last message of the user's
// and Eriphe's lines after it (the last exchangeReplies); with no message
// of the user's, Eriphe's last line. Earlier tells whether lines come
// before. Empty when the thread has none.
func (s *Store) lastExchange(thread int64) (lines []exchangeLine, earlier bool, err error) {
	var from int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE thread = ? AND role = ? AND via != ?`, thread, roleUser, firstRunVia).Scan(&from)
	if from == 0 {
		s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE thread = ? AND role IN (?, ?)`, thread, roleBrain, roleError).Scan(&from)
	}
	if from == 0 {
		return []exchangeLine{}, false, nil
	}
	lines = []exchangeLine{}
	mine, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq = ? AND role = ?`, from, roleUser)
	if err != nil {
		return nil, false, err
	}
	for _, m := range mine {
		lines = append(lines, s.exchangeOf(m))
	}
	// The last few, however many events came between.
	replies, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq >= ? AND thread = ? AND role IN (?, ?) ORDER BY seq DESC LIMIT ?`,
		from, thread, roleBrain, roleError, exchangeReplies)
	if err != nil {
		return nil, false, err
	}
	for i := len(replies) - 1; i >= 0; i-- {
		lines = append(lines, s.exchangeOf(replies[i]))
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM messages WHERE seq < ? AND `+exchangeLines+` LIMIT 1)`, exchangeArgs(thread, lines[0].Seq)...).Scan(&n)
	return lines, n > 0, nil
}

// earlierLines are a thread's (0: the home's) exchangeEarlier lines
// before seq, oldest first, and whether more come before them.
func (s *Store) earlierLines(before int, thread int64) ([]exchangeLine, bool, error) {
	msgs, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq < ? AND `+exchangeLines+` ORDER BY seq DESC LIMIT ?`,
		append(exchangeArgs(thread, before), exchangeEarlier+1)...)
	if err != nil {
		return nil, false, err
	}
	more := len(msgs) > exchangeEarlier
	if more {
		msgs = msgs[:exchangeEarlier]
	}
	out := make([]exchangeLine, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		out = append(out, s.exchangeOf(msgs[i]))
	}
	return out, more, nil
}

// earlierView is the answer of GET /api/espace/earlier.
type earlierView struct {
	Lines []exchangeLine `json:"lines"` // oldest first
	More  bool           `json:"more"`  // lines come before them
}

// handleEarlier is GET /api/espace/earlier?before=<seq>&thread=<id>: the
// lines of a thread's exchange (none: the home's) before that one, for the
// user who scrolls up the left column.
func (w *web) handleEarlier(rw http.ResponseWriter, r *http.Request, _ *session) {
	before, err := strconv.Atoi(r.URL.Query().Get("before"))
	if err != nil || before <= 0 {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "before: a message's seq"})
		return
	}
	var thread int64
	if v := r.URL.Query().Get("thread"); v != "" {
		if thread, err = strconv.ParseInt(v, 10, 64); err != nil || thread < 0 {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "thread: a thread's id"})
			return
		}
	}
	lines, more, err := w.c.store.earlierLines(before, thread)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": "the conversation could not be read"})
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, earlierView{Lines: lines, More: more})
}
