package main

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// Eriphe's orb: the workspace shows Eriphe as a small blue orb that lives
// in the graph. It rests in the bar, and leaves it only in reply to what
// the user said to it (a message, or their Oui or Non on a card: what came
// of it is told to Eriphe as an event that answers the user, nodeEvent):
// while Eriphe works on their message, it goes beside
// the subject it works on and shows it; when Eriphe has answered, it goes
// beside the subject its answer belongs to, or to a free spot of the home
// when it belongs to none, and says it in a few lines, whether or not
// that turn raised a card; when that turn failed, it says so. It only
// answers: what waits for the user's yes is the graph's to show (the
// node's ring, the card in its page), never the orb's; and what Eriphe
// does or says unasked (an event's words, a subject that began to wait)
// never moves it either.
//
// The subject's document is the conversation: beside a subject the orb
// says its first lines; its « voir plus » unfolds the whole answer in
// place, as Markdown (Full), the page holding what the brain or code wrote
// there (node_net.go). Only an answer given at the home, which no subject
// holds, is held whole at once, as Markdown, until the user moves on.
//
// The workspace's stream (/events, web_sse.go) sends what the orb needs as
// JSON, its "orb" event, whatever page the bar talks to: a snapshot keyed
// by the messages' seqs, when the stream opens and whenever it changes.
// The page decides what the orb shows of it and remembers what it showed
// already (web/espace/src/orb).
//
// Eriphe's words beside a subject are plain text lines, cut here
// (fewLines): never its Markdown, but the home's answer; marked when
// written in a turn that read external content (markBrainMessage), never
// replaced. The first run's messages and an error's raw text are never
// sent.
//
// The event also carries what waits above the bar: the user's messages
// that wait for Eriphe (messages_waiting.go), and the cards no node holds
// (one proposed in a turn about no node, from the home): those show above
// the bar, with their Oui and Non. A card linked to a node waits in its
// page, the node's ring says so.

const (
	askedMax  = 500  // the user's message that waits, as the bar folds it
	ackRunes  = 140  // a line of Eriphe's
	wordLines = 6    // Eriphe's words beside a subject: at most these lines
	wordRunes = 600  // ... and these runes
	lineRunes = 280  // ... a line of them at most these
	answerMax = 2000 // an answer given at the home, as the orb holds it (node_net.go files one of netMinRunes)
)

// orbEvent is the workspace's "orb" event.
type orbEvent struct {
	// Asked is the seq of the user's last message Eriphe read: a message
	// the page sent has landed once one past it is read, or waits.
	Asked int `json:"asked,omitempty"`
	// Work: Eriphe is at work, on that node ("": none, the home).
	Work *orbWork `json:"work,omitempty"`
	// Answer is Eriphe's answer to the user's last message it read, where
	// it belongs; none once the user wrote again.
	Answer *orbWords `json:"answer,omitempty"`
	// Failed: Eriphe could not answer the user's message; its error's seq
	// and node, never its text.
	Failed *orbFailed `json:"failed,omitempty"`
	// Cards are the cards that wait and no node holds: their refs.
	Cards []string `json:"cards,omitempty"`
	// Waiting are the user's messages that wait for Eriphe, whatever node
	// they were said to, oldest first (messages_waiting.go).
	Waiting []waitingLine `json:"waiting,omitempty"`
}

// orbWork is where Eriphe works, and the first line of what it is writing.
type orbWork struct {
	Node  string `json:"node,omitempty"`
	Draft string `json:"draft,omitempty"`
}

// orbWords are Eriphe's words where they belong (Node; "" for the home): a
// few plain lines (More: there is more, Full: all of it, as Markdown); at
// the home, an answer to the user of more than a line, whole, as Markdown
// (Whole).
type orbWords struct {
	Seq      int    `json:"seq"`
	Node     string `json:"node,omitempty"`
	Text     string `json:"text"`
	More     bool   `json:"more,omitempty"`
	Full     string `json:"full,omitempty"` // with More: the whole answer, as Markdown
	Whole    bool   `json:"whole,omitempty"`
	External bool   `json:"external,omitempty"` // written in a turn that read external content
}

// orbFailed is a turn that could not answer: its error message's seq, and
// the node it belongs to.
type orbFailed struct {
	Seq  int    `json:"seq"`
	Node string `json:"node,omitempty"`
}

// orbFor is the orb's event: Eriphe's answer to the user, its work on
// their message (thinking: the brain is at work; draft: what it is
// writing), the cards and the messages that wait. Its words and errors in
// a turn that answers no act of the user's (a message, or a card's Oui or
// Non: nodeEvent) are not the orb's. A card's decision moves nothing by
// itself: only what Eriphe says of it replaces what the orb says.
func (w *web) orbFor(draft string, thinking bool) (orbEvent, error) {
	c, s := w.c, w.c.store
	from := s.lastAsked()
	x := orbEvent{Asked: from}
	if from == 0 {
		x.Cards, x.Waiting = c.unheldCards(), c.waiting(thinking)
		return x, nil
	}
	msgs, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq > ? AND role IN (?, ?) ORDER BY seq`, from, roleBrain, roleError)
	if err != nil {
		return orbEvent{}, err
	}
	var answer []Message
	turn := 0 // the act the answer collected replies to
	for _, m := range msgs {
		mark := m.AnswerTo
		if mark < from {
			// An event's words, a failure of a turn that answered none,
			// or the answer to an earlier message of the user's (written
			// after this one was sent, while it waited): not the reply to
			// the user's last message, nor to an act of theirs since.
			continue
		}
		if mark > turn {
			answer, x.Failed, turn = nil, nil, mark // the reply to a later act (a card's Oui) replaces it
		}
		if m.Role == roleError {
			x.Failed = &orbFailed{Seq: m.Seq, Node: m.Node}
			answer = nil
		} else {
			x.Failed = nil
			answer = append(answer, m)
		}
	}
	// The user wrote again (read or not): Eriphe's answer to the message
	// before is gone, and so is its failure.
	var since int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? AND via != ? AND seq > ?`, roleUser, firstRunVia, from).Scan(&since)
	if since > 0 {
		answer, x.Failed = nil, nil
	}
	x.Answer = s.orbWordsOf(answer)
	// At work on the user's message: as soon as it thinks. On a card's
	// decision only once it writes words: Eriphe may have nothing to say
	// of it (quietMark), and what the orb says stays.
	line, _ := ackLine(draft)
	working := thinking
	if s.lastActed() > from {
		working = false
		if strings.HasPrefix(quietMark, line) {
			line = ""
		}
	}
	if (working || line != "") && c.turnAnswersUser() {
		x.Work = &orbWork{Node: c.workNode(), Draft: line}
	}
	x.Cards = c.unheldCards()
	x.Waiting = c.waiting(thinking)
	return x, nil
}

// lastAsked is the seq of the user's last message Eriphe read (the first
// run's aside); 0 if none.
func (s *Store) lastAsked() int {
	var from int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role = ? AND via != ? AND delivered = 1`, roleUser, firstRunVia).Scan(&from)
	return from
}

// userActs are the user's own acts among the messages, as SQL: their
// messages (not the first run's aside), and the events that tell the
// brain what came of their Oui or Non on a card (nodeEvent). Its one
// parameter is firstRunVia.
const userActs = `(role = 'user' AND via != ? OR role = 'event' AND user_act = 1)`

// lastActed is the seq of the user's last act Eriphe read: their message,
// or the event of their Oui or Non on a card; 0 if none. Past lastAsked,
// the last turn answers a card's decision.
func (s *Store) lastActed() int {
	var from int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE delivered = 1 AND `+userActs, firstRunVia).Scan(&from)
	return from
}

// isUserAct tells an event that tells the brain what came of the user's
// own act, their Oui or Non on a card (nodeEvent): the turn that reads
// it answers the user (deliverConversation), and what it says, or its
// failure, is the orb's, as a reply to a message is, and replaces what
// the orb said before; the decision alone replaces nothing.
func (s *Store) isUserAct(m Message) bool { return m.Role == roleEvent && m.UserAct }

// nodeEvent tells the brain something as an event of node ("" for the
// home): what came of the user's Oui or Non on a card (userAct: what the
// brain says of it answers them), an exception of a work cut in steps.
// An event of a node names it first (nodeHead), and the turn that reads
// it is about that node (nodesDelivered); taint is what its words make
// that turn, with the node's title when it may be someone else's words.
// Written delivered, its marks set, then handed to the brain: it is never
// delivered before its marks.
func (c *core) nodeEvent(node, text string, userAct bool, taint ...string) {
	taint = slices.Clone(taint)
	if node != "" {
		head, t := c.nodeHead(node)
		text = head + " " + text
		taint = append(taint, t)
	}
	var why []string
	for _, t := range taint {
		if t != "" && !slices.Contains(why, t) {
			why = append(why, t)
		}
	}
	res, err := c.store.db.Exec(`INSERT INTO messages (time, role, text, delivered, node, user_act) VALUES (?, ?, ?, 1, ?, ?)`,
		time.Now().Unix(), roleEvent, text, node, userAct)
	if err != nil {
		return
	}
	seq, _ := res.LastInsertId()
	if len(why) > 0 {
		c.store.setMsgTaint(int(seq), strings.Join(why, ", "))
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// placeOf is where a message of Eriphe's belongs: its node, else the node
// code filed it in (node_net.go), else the home.
func placeOf(m Message) string {
	if m.Node == "" {
		return m.Filed
	}
	return m.Node
}

// orbWordsOf are Eriphe's answer of msgs, where the last one belongs: those
// of msgs that belong there, joined. At the home, an answer of more than a
// line is held whole, as Markdown; beside a subject, a few plain lines.
// Nil when they say nothing.
func (s *Store) orbWordsOf(msgs []Message) *orbWords {
	if len(msgs) == 0 {
		return nil
	}
	place := placeOf(msgs[len(msgs)-1])
	w := orbWords{Node: place}
	var parts []string
	for _, m := range msgs {
		if placeOf(m) != place {
			continue
		}
		parts = append(parts, strings.TrimSpace(m.Text))
		w.Seq = m.Seq
		w.External = w.External || strings.HasPrefix(s.msgTaint(m.Seq), "brain:")
	}
	text := strings.Join(parts, "\n\n")
	if _, cut := ackLine(text); place == "" && cut {
		w.Text, w.Whole = truncate(text, answerMax), true
		return &w
	}
	if w.Text, w.More = fewLines(text); w.Text == "" {
		return nil
	}
	if w.More {
		w.Full = truncate(text, answerMax)
	}
	return &w
}

// workNode is the node Eriphe works on: the one its turn is about, else
// the one its answer belongs to; none ("") for a turn about the home.
func (c *core) workNode() string {
	if c.turn.answersAtHome() {
		return ""
	}
	if id := c.turn.aboutNode(); id != "" {
		return id
	}
	return c.turn.talkNode()
}

// unheldCardsMax is the most cards no node holds the event carries.
const unheldCardsMax = 20

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
	if len(out) > unheldCardsMax {
		out = out[len(out)-unheldCardsMax:]
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

// fewLines are Eriphe's words beside a subject: the first lines of what it
// wrote that say something, as plain text, at most wordLines lines and
// wordRunes runes, a line past lineRunes cut as ackLine cuts one; cut
// tells that they are not all of it.
func fewLines(text string) (lines string, cut bool) {
	var out []string
	n, fenced := 0, false
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			fenced, cut = !fenced, true // code: not words to say
			continue
		}
		if t == "" || fenced {
			continue
		}
		if strings.HasPrefix(t, "|") || strings.Trim(t, "-*_= ") == "" {
			cut = true // a table, a rule: not words to say
			continue
		}
		line, short := sayLine(t, lineRunes)
		if len(out) == wordLines || (len(out) > 0 && n+len([]rune(line)) > wordRunes) {
			cut = true
			break
		}
		out = append(out, line)
		n += len([]rune(line))
		if short {
			cut = true
			break
		}
	}
	return strings.Join(out, "\n"), cut
}

// ackLine is a line of Eriphe's: the first line of what it wrote that
// says something, as plain text (no Markdown marks, a link's words only),
// at most ackRunes: its first sentence when that fits, else cut at a word
// with « … ». cut tells that the line is not all of it: more lines, or a
// line cut.
func ackLine(text string) (line string, cut bool) { return sayLine(text, ackRunes) }

// sayLine is ackLine with limit runes at most.
func sayLine(text string, limit int) (line string, cut bool) {
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
	if len([]rune(line)) > limit {
		return shortLine(line, limit), true
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

// shortLine is a line longer than limit runes, made short: its first
// sentence when that fits, else cut at a word.
func shortLine(line string, limit int) string {
	r := []rune(line)
	for i := 20; i < limit; i++ {
		if (r[i] == '.' || r[i] == '?' || r[i] == '!') && r[i+1] == ' ' {
			return string(r[:i+1])
		}
	}
	head := string(r[:limit])
	if i := strings.LastIndex(head, " "); i > len(head)/2 {
		head = head[:i]
	}
	return strings.TrimRight(head, " ,;:") + "…"
}

// markAnswer marks a message the brain just wrote, or its error, when its
// turn answers the user, not events or a wake only: the orb says it; an
// event's words never. The mark is the last message the turn read: a
// message of the user's sent while it ran waits for the next turn, whose
// answer alone replies to it.
func (c *core) markAnswer(seq int) {
	c.turn.mu.Lock()
	asked, to := c.turn.asked, c.turn.to
	c.turn.mu.Unlock()
	if asked {
		c.store.db.Exec(`UPDATE messages SET answer_to = ? WHERE seq = ?`, max(to, 1), seq)
	}
}
