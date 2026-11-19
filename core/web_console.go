package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The workspace's view of the conversation (#455): not a second chat, the
// last exchange only, in the console. What the user says updates a
// node of the graph, so rereading the conversation is
// rare: the user's last message and what Eriphe said after it, the message
// being written, and whether Eriphe is at work. The workspace's stream
// (/events, web_sse.go) sends it as JSON, its "exchange" event, when
// it connects and whenever it changes: a reconnecting page gets it whole.
// The lines before it come on request, a few at a time
// (GET /api/earlier), back to the first.
//
// The conversation per node (#632): the exchange is the one of the node
// the console talks to (its lines belong to it, node_turn.go), named in
// the stream's query (node) and in the event, the home's when none is.
// Eriphe's draft and work show in the exchange its turn answers in; its
// latest answer brings the replies it suggested (brain_suggest.go), and
// the node code filed it in, when it did (filed, node_net.go).
//
// A card no node holds (one proposed in a turn about no node, from the
// home, or whose node is archived) would wait nowhere in the graph: every
// exchange carries the ones the user was shown (cards: their refs), and
// the console shows them with their Oui and Non, wherever it talks. A
// card linked to a node waits at the foot of its page.
//
// The brain's text is its own, shown as Markdown by the page's renderer,
// which makes no HTML of it, and marked when written in a turn that read
// external content (markBrainMessage); an event's line (an external
// source's), the first run's messages and an error's raw text are never
// sent.

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
	Filed    string    `json:"filed,omitempty"` // Eriphe's: the node code filed it in (node_net.go)
}

// exchangeEvent is the workspace's "exchange" event.
type exchangeEvent struct {
	Node        string         `json:"node,omitempty"`        // the node it belongs to; none: the home
	Title       string         `json:"title,omitempty"`       // that node's title, unless it may be someone else's words
	Lines       []exchangeLine `json:"lines"`                 // the last exchange
	Earlier     bool           `json:"earlier,omitempty"`     // lines come before it
	Draft       string         `json:"draft,omitempty"`       // Eriphe's message being written
	Thinking    bool           `json:"thinking,omitempty"`    // Eriphe is at work
	Suggestions []string       `json:"suggestions,omitempty"` // the replies its last line suggests
	Cards       []string       `json:"cards,omitempty"`       // the cards that wait and no node holds: their refs
}

// exchangeCardsMax is the most cards no node holds an exchange carries.
const exchangeCardsMax = 20

// unheldCards are the cards that wait for the user, shown already (with a
// message, or in the turn under way), and that no node holds: linked to
// none that is live. The oldest first.
func (c *core) unheldCards() []string {
	xs, err := c.cardsWhere(`state = ? AND kind != ? AND (seq > 0 OR turn = 1) ORDER BY id`, cardPending, cardDigest)
	if err != nil {
		return nil
	}
	var out []string
	for _, x := range xs {
		if x, _ = c.liveCard(x); x.State != cardPending || c.cardHeld(x.Ref) {
			continue
		}
		out = append(out, x.Ref)
	}
	if len(out) > exchangeCardsMax {
		out = out[len(out)-exchangeCardsMax:]
	}
	return out
}

// cardHeld tells a card linked to a live node: it waits on its page.
func (c *core) cardHeld(ref string) bool {
	if c.nodes == nil {
		return false
	}
	for _, id := range c.cardNodes(ref) {
		if sum, err := c.nodes.Get(id); err == nil && sum.Archived.IsZero() {
			return true
		}
	}
	return false
}

// exchangeFor is the workspace's exchange of a node ("": the home), with
// Eriphe's draft and work when its turn answers there: talk is the node it
// answers in.
func (w *web) exchangeFor(node, talk, draft string, thinking bool) (exchangeEvent, error) {
	lines, earlier, err := w.c.store.lastExchange(node)
	if err != nil {
		return exchangeEvent{}, err
	}
	x := exchangeEvent{Node: node, Lines: lines, Earlier: earlier}
	if node != "" {
		x.Title = w.c.cleanNodeTitle(node)
	}
	waiting := len(lines) > 0 && lines[len(lines)-1].Role == roleUser
	if talk == node {
		x.Draft = truncate(draft, exchangeTextMax)
	}
	x.Thinking = thinking && (talk == node || waiting)
	if n := len(lines); n > 0 && lines[n-1].Role == roleBrain && !lines[n-1].External && x.Draft == "" {
		x.Suggestions = w.c.store.suggestions(lines[n-1].Seq)
	}
	x.Cards = w.c.unheldCards()
	return x, nil
}

// exchangeLines is the filter of the lines the page gets, with its
// arguments (exchangeArgs): the user's (not the first run's), Eriphe's and
// errors, never an event's, of one node.
const exchangeLines = `node = ? AND (role IN (?, ?) OR (role = ? AND via != ?))`

func exchangeArgs(node string, args ...any) []any {
	return append(args, node, roleBrain, roleError, roleUser, firstRunVia)
}

// exchangeOf is a message as the page gets it.
func (s *Store) exchangeOf(m Message) exchangeLine {
	l := exchangeLine{Seq: m.Seq, Role: m.Role, Time: m.Time}
	switch m.Role {
	case roleUser:
		l.Text = truncate(m.Text, exchangeTextMax)
		l.Via = m.Via
	case roleBrain:
		l.Text = truncate(m.Text, exchangeTextMax)
		l.External = strings.HasPrefix(s.get(taintKey(m.Seq)), "brain:")
		l.Filed = m.Filed
	}
	return l
}

// lastExchange is a node's ("": the home's) last message of the user's
// and Eriphe's lines after it (the last exchangeReplies); with no message
// of the user's, Eriphe's last line. Earlier tells whether lines come
// before. Empty when the node has none.
func (s *Store) lastExchange(node string) (lines []exchangeLine, earlier bool, err error) {
	var from int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE node = ? AND role = ? AND via != ?`, node, roleUser, firstRunVia).Scan(&from)
	if from == 0 {
		s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE node = ? AND role IN (?, ?)`, node, roleBrain, roleError).Scan(&from)
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
	replies, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq >= ? AND node = ? AND role IN (?, ?) ORDER BY seq DESC LIMIT ?`,
		from, node, roleBrain, roleError, exchangeReplies)
	if err != nil {
		return nil, false, err
	}
	for i := len(replies) - 1; i >= 0; i-- {
		lines = append(lines, s.exchangeOf(replies[i]))
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM messages WHERE seq < ? AND `+exchangeLines+` LIMIT 1)`, exchangeArgs(node, lines[0].Seq)...).Scan(&n)
	return lines, n > 0, nil
}

// earlierLines are a node's ("": the home's) exchangeEarlier lines
// before seq, oldest first, and whether more come before them.
func (s *Store) earlierLines(before int, node string) ([]exchangeLine, bool, error) {
	msgs, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq < ? AND `+exchangeLines+` ORDER BY seq DESC LIMIT ?`,
		append(exchangeArgs(node, before), exchangeEarlier+1)...)
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

// earlierView is the answer of GET /api/earlier.
type earlierView struct {
	Lines []exchangeLine `json:"lines"` // oldest first
	More  bool           `json:"more"`  // lines come before them
}

// handleEarlier is GET /api/earlier?before=<seq>&node=<id>: the
// lines of a node's exchange (none: the home's) before that one, for the
// user who scrolls up the console.
func (w *web) handleEarlier(rw http.ResponseWriter, r *http.Request, _ *session) {
	before, err := strconv.Atoi(r.URL.Query().Get("before"))
	if err != nil || before <= 0 {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "before: a message's seq"})
		return
	}
	node := r.URL.Query().Get("node")
	if node != "" && !nodefile.IDOK(node) {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "node: a node's id"})
		return
	}
	lines, more, err := w.c.store.earlierLines(before, node)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": "the conversation could not be read"})
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, earlierView{Lines: lines, More: more})
}
