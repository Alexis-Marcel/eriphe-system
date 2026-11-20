package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The workspace's bar (#455, #663): not a chat. The subject's document is
// the conversation; the bar under it is only a bar: the input, the user's
// last message in grey, and one line of Eriphe's, the first line of its
// last answer (its acknowledgement: the rest, if any, is in the subject,
// written by the brain or filed by code, node_net.go), or of what it is
// writing. No history, no buttons: Eriphe's questions and the buttons
// that answer them are in the subject's document (a block's ask). The
// workspace's stream (/events, web_sse.go) sends it as JSON, its
// "exchange" event, when it connects and whenever it changes.
//
// The bar per node (#632): the exchange is the one of the node the bar
// talks to (its lines belong to it, node_turn.go), named in the stream's
// query (node) and in the event, the home's when none is. Eriphe's draft
// and work show in the exchange its turn answers in.
//
// A card no node holds (one proposed in a turn about no node, from the
// home) would wait nowhere in the graph: every exchange carries the ones
// the user was shown (cards: their refs), and the bar shows them above
// it, with their Oui and Non, wherever it talks: the one exception to a
// bar without buttons. A card linked to a node waits in its page.
//
// Eriphe's line is plain text, cut here (ackLine): never its Markdown,
// never more than a line; marked when written in a turn that read
// external content (markBrainMessage). An event's line, the first run's
// messages and an error's raw text are never sent.
//
// An answer given at the home is the one exception to the line: no
// subject holds it, so the bar holds it whole (answer), as Markdown,
// until the user moves on. Only the answer to the user's own message
// (markAnswer: written in a turn that read one) is: what an event or a
// wake made Eriphe say later is a line (said) that never takes its place.
// An answer of one line is its line, and goes as any.

const (
	exchangeReplies = 12   // Eriphe's last lines after the user's message, read to find its last
	askedMax        = 500  // the user's last message, as the bar folds it
	ackRunes        = 140  // Eriphe's line in the bar
	answerMax       = 2000 // an answer given at the home, as the bar holds it (node_net.go files one of netMinRunes)
)

// exchangeLine is a message as the store reads it for the bar: the
// user's (with where they said it), Eriphe's (External: written in a
// tainted turn), or an error, without its text.
type exchangeLine struct {
	Seq      int
	Role     string // user, brain or error
	Text     string
	Via      string
	External bool
	Filed    string // Eriphe's: the node code filed it in (node_net.go)
}

// barLine is a line of the bar: its message's seq and its text.
type barLine struct {
	Seq      int    `json:"seq"`
	Text     string `json:"text"`
	External bool   `json:"external,omitempty"` // Eriphe's, written in a turn that read external content
}

// exchangeEvent is the workspace's "exchange" event: what the bar shows.
type exchangeEvent struct {
	Node     string   `json:"node,omitempty"`     // the node it belongs to; none: the home
	Title    string   `json:"title,omitempty"`    // that node's title, unless it may be someone else's words
	Asked    *barLine `json:"asked,omitempty"`    // the user's last message
	Said     *barLine `json:"said,omitempty"`     // Eriphe's line: the first line of its last answer after it
	Failed   bool     `json:"failed,omitempty"`   // Eriphe could not answer: its last line is an error
	Answer   *barLine `json:"answer,omitempty"`   // at the home, Eriphe's whole answer to the user's last message, Markdown, when more than a line
	Draft    string   `json:"draft,omitempty"`    // the first line of what Eriphe is writing
	Thinking bool     `json:"thinking,omitempty"` // Eriphe is at work
	Cards    []string `json:"cards,omitempty"`    // the cards that wait and no node holds: their refs
	// Waiting are the user's messages that wait for Eriphe, whatever node
	// they were said to, oldest first (messages_waiting.go).
	Waiting []waitingLine `json:"waiting,omitempty"`
}

// exchangeCardsMax is the most cards no node holds an exchange carries.
const exchangeCardsMax = 20

// unheldCards are the cards that wait for the user, shown already (with a
// message, or in the turn under way), and that no node holds: linked to
// none that is live. The oldest first.
func (c *core) unheldCards() []string {
	xs, err := c.cardsWhere(`state = ? AND (seq > 0 OR turn = 1) ORDER BY id`, cardPending)
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
	lines, err := w.c.store.lastExchange(node)
	if err != nil {
		return exchangeEvent{}, err
	}
	x := exchangeEvent{Node: node}
	if node != "" {
		x.Title = w.c.cleanNodeTitle(node)
	}
	for _, l := range lines {
		if l.Role == roleUser {
			x.Asked = &barLine{Seq: l.Seq, Text: truncate(l.Text, askedMax)}
		}
	}
	if n := len(lines); n > 0 {
		switch last := lines[n-1]; last.Role {
		case roleBrain:
			if ack, _ := ackLine(last.Text); ack != "" {
				x.Said = &barLine{Seq: last.Seq, Text: ack, External: last.External}
			}
		case roleError:
			x.Failed = true
		}
	}
	if node == "" {
		if x.Answer, err = w.c.store.homeAnswer(); err != nil {
			return exchangeEvent{}, err
		}
		if x.Answer != nil && x.Said != nil && x.Said.Seq <= x.Answer.Seq {
			x.Said = nil // its line is the answer, held whole
		}
	}
	waiting := len(lines) > 0 && lines[len(lines)-1].Role == roleUser
	if talk == node {
		x.Draft, _ = ackLine(draft)
	}
	x.Thinking = thinking && (talk == node || waiting)
	x.Cards = w.c.unheldCards()
	x.Waiting = w.c.waiting(thinking)
	return x, nil
}

// ackLine is Eriphe's line in the bar: the first line of what it wrote
// that says something, as plain text (no Markdown marks, a link's words
// only), at most ackRunes: its first sentence when that fits, else cut at
// a word with « … ». cut tells that the bar does not show all of it: more
// lines, or a line cut.
func ackLine(text string) (line string, cut bool) {
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if line != "" {
			cut = true
			break
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "|") || strings.Trim(t, "-*_= ") == "" {
			cut = true // a fence, a table, a rule: not a line to say
			continue
		}
		line = plainLine(t)
	}
	if len([]rune(line)) > ackRunes {
		return shortLine(line), true
	}
	return line, cut
}

var (
	ackLinkRe   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	ackLeadRe   = regexp.MustCompile(`^(?:[-*+>]|\d+[.)]|#{1,6})\s+`)
	ackMarkRe   = regexp.MustCompile("\\*\\*|__|`|~~")
	ackSpacesRe = regexp.MustCompile(`\s+`)
)

// plainLine is a line of Markdown as plain text.
func plainLine(t string) string {
	for {
		u := ackLeadRe.ReplaceAllString(t, "")
		if u == t {
			break
		}
		t = u
	}
	t = ackLinkRe.ReplaceAllString(t, "$1")
	t = ackMarkRe.ReplaceAllString(t, "")
	return strings.TrimSpace(ackSpacesRe.ReplaceAllString(t, " "))
}

// shortLine is a line longer than ackRunes, made short: its first
// sentence when that fits, else cut at a word.
func shortLine(line string) string {
	r := []rune(line)
	for i := 20; i < ackRunes; i++ {
		if (r[i] == '.' || r[i] == '?' || r[i] == '!') && r[i+1] == ' ' {
			return string(r[:i+1])
		}
	}
	head := string(r[:ackRunes])
	if i := strings.LastIndex(head, " "); i > len(head)/2 {
		head = head[:i]
	}
	return strings.TrimRight(head, " ,;:") + "…"
}

// exchangeOf is a message as the bar reads it.
func (s *Store) exchangeOf(m Message) exchangeLine {
	l := exchangeLine{Seq: m.Seq, Role: m.Role}
	switch m.Role {
	case roleUser:
		l.Text, l.Via = m.Text, m.Via
	case roleBrain:
		l.Text, l.Filed = m.Text, m.Filed
		l.External = strings.HasPrefix(s.get(taintKey(m.Seq)), "brain:")
	}
	return l
}

// lastExchange is a node's ("": the home's) last message of the user's
// that Eriphe was given and Eriphe's lines after it (the last
// exchangeReplies); with no such message of the user's, Eriphe's last
// line. Empty when the node has none. A message that waits for Eriphe is
// not in it: the bar shows it waiting (messages_waiting.go), and what
// Eriphe answers meanwhile is to the message before.
func (s *Store) lastExchange(node string) ([]exchangeLine, error) {
	var from int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE node = ? AND role = ? AND via != ? AND delivered = 1`, node, roleUser, firstRunVia).Scan(&from)
	if from == 0 {
		s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE node = ? AND role IN (?, ?)`, node, roleBrain, roleError).Scan(&from)
	}
	lines := []exchangeLine{}
	if from == 0 {
		return lines, nil
	}
	mine, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq = ? AND role = ?`, from, roleUser)
	if err != nil {
		return nil, err
	}
	for _, m := range mine {
		lines = append(lines, s.exchangeOf(m))
	}
	// The last few, however many events came between.
	replies, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq >= ? AND node = ? AND role IN (?, ?) ORDER BY seq DESC LIMIT ?`,
		from, node, roleBrain, roleError, exchangeReplies)
	if err != nil {
		return nil, err
	}
	for i := len(replies) - 1; i >= 0; i-- {
		lines = append(lines, s.exchangeOf(replies[i]))
	}
	return lines, nil
}

// answerKey marks a message of Eriphe's written in a turn that answers
// messages of the user's (markAnswer).
func answerKey(seq int) string { return fmt.Sprintf("answer_msg:%d", seq) }

// markAnswer marks a message the brain just wrote when its turn answers
// the user, not events or a wake only: the bar holds it at the home.
func (c *core) markAnswer(seq int) {
	if c.turnAnswersUser() {
		c.store.set(answerKey(seq), "1")
	}
}

// homeAnswer is Eriphe's whole answer to the user's last message read at
// the home: its messages written in a turn that answered it (markAnswer),
// filed in no node, joined, as Markdown. Nil when the bar's line says all
// of it (a line: it goes as any), when there is none, or when the user
// wrote since, wherever and read or not: a new message moves on from it.
func (s *Store) homeAnswer() (*barLine, error) {
	var from, since int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE node = '' AND role = ? AND via != ? AND delivered = 1`, roleUser, firstRunVia).Scan(&from)
	if from == 0 {
		return nil, nil
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? AND seq > ?`, roleUser, from).Scan(&since)
	if since > 0 {
		return nil, nil
	}
	msgs, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq > ? AND node = '' AND role = ? AND filed = '' ORDER BY seq`, from, roleBrain)
	if err != nil {
		return nil, err
	}
	var parts []string
	var a barLine
	for _, m := range msgs {
		if s.get(answerKey(m.Seq)) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(m.Text))
		a.Seq = m.Seq
		a.External = a.External || s.exchangeOf(m).External
	}
	a.Text = strings.Join(parts, "\n\n")
	if _, cut := ackLine(a.Text); !cut {
		return nil, nil
	}
	a.Text = truncate(a.Text, answerMax)
	return &a, nil
}
