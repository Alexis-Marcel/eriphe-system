package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Eriphe's orb: the workspace shows Eriphe as a small blue orb that lives
// in the graph. It rests in the bar; while Eriphe works, it goes beside
// the subject it works on and shows it; when Eriphe has said something, it
// goes beside the subject its words belong to, or to a free spot of the
// home when they belong to none, and says them in a few lines; when a card
// waits for the user's yes, it goes beside the subject that holds it.
// After every turn of Eriphe's, the user sees where its answer is.
//
// The subject's document is the conversation: beside a subject the orb
// says only a few lines (its « voir plus » opens the page, where the rest
// is, written by the brain or filed by code, node_net.go). Only an answer
// given at the home, which no subject holds, is held whole, as Markdown,
// until the user moves on.
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
// page: the orb points at it (wait).

const (
	askedMax  = 500  // the user's message that waits, as the bar folds it
	ackRunes  = 140  // a line of Eriphe's
	wordLines = 3    // Eriphe's words beside a subject: at most these lines
	wordRunes = 280  // ... and these runes
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
	// it belongs; none once the user wrote again. Said are its last words
	// after it that answer no message of the user's (an event's, a
	// wake's): where they belong.
	Answer *orbWords `json:"answer,omitempty"`
	Said   *orbWords `json:"said,omitempty"`
	// Failed: Eriphe could not answer; its error's seq and node, never its
	// text.
	Failed *orbFailed `json:"failed,omitempty"`
	// Wait is the newest card that waits for the user's yes in a node.
	Wait *orbWait `json:"wait,omitempty"`
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
// few plain lines (More: there is more, in the subject's page); at the
// home, an answer to the user of more than a line, whole, as Markdown
// (Whole).
type orbWords struct {
	Seq      int    `json:"seq"`
	Node     string `json:"node,omitempty"`
	Text     string `json:"text"`
	More     bool   `json:"more,omitempty"`
	Whole    bool   `json:"whole,omitempty"`
	External bool   `json:"external,omitempty"` // written in a turn that read external content
}

// orbFailed is a turn that could not answer: its error message's seq, and
// the node it belongs to.
type orbFailed struct {
	Seq  int    `json:"seq"`
	Node string `json:"node,omitempty"`
}

// orbWait is a card that waits for the user's yes, and the node that holds it.
type orbWait struct {
	Card int64  `json:"card"`
	Node string `json:"node"`
}

// orbFor is the orb's event: Eriphe's last words, its work (thinking: the
// brain is at work; draft: what it is writing), the cards and the
// messages that wait.
func (w *web) orbFor(draft string, thinking bool) (orbEvent, error) {
	c, s := w.c, w.c.store
	from := s.lastAsked()
	x := orbEvent{Asked: from}
	msgs, err := s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq > ? AND role IN (?, ?) ORDER BY seq`, from, roleBrain, roleError)
	if err != nil {
		return orbEvent{}, err
	}
	if from == 0 && len(msgs) == 0 {
		// No message of the user's yet: Eriphe's last words, if any.
		if msgs, err = s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? ORDER BY seq DESC LIMIT 1`, roleBrain); err != nil {
			return orbEvent{}, err
		}
	}
	var answer, said []Message
	for _, m := range msgs {
		switch {
		case m.Role == roleError:
			x.Failed = &orbFailed{Seq: m.Seq, Node: m.Node}
			answer, said = nil, nil
		case from > 0 && s.get(answerKey(m.Seq)) != "":
			x.Failed = nil
			answer, said = append(answer, m), nil
		default:
			x.Failed = nil
			said = append(said, m)
		}
	}
	// The user wrote again (read or not): Eriphe's answer to the message
	// before is gone, and so is its failure.
	var since int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? AND via != ? AND seq > ?`, roleUser, firstRunVia, from).Scan(&since)
	if since > 0 {
		answer, x.Failed = nil, nil
	}
	x.Answer = s.orbWordsOf(answer, true)
	x.Said = s.orbWordsOf(said, false)
	if line, _ := ackLine(draft); thinking || line != "" {
		x.Work = &orbWork{Node: c.workNode(), Draft: line}
	}
	x.Wait = c.newestWait(from)
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

// placeOf is where a message of Eriphe's belongs: its node, else the node
// code filed it in (node_net.go), else the home.
func placeOf(m Message) string {
	if m.Node == "" {
		return m.Filed
	}
	return m.Node
}

// orbWordsOf are Eriphe's words of msgs, where the last one belongs: those
// of msgs that belong there, joined. At the home, an answer to the user
// (answer) of more than a line is held whole, as Markdown; anything else
// is a few plain lines. Nil when they say nothing.
func (s *Store) orbWordsOf(msgs []Message, answer bool) *orbWords {
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
		w.External = w.External || strings.HasPrefix(s.get(taintKey(m.Seq)), "brain:")
	}
	text := strings.Join(parts, "\n\n")
	if _, cut := ackLine(text); answer && place == "" && cut {
		w.Text, w.Whole = truncate(text, answerMax), true
		return &w
	}
	if w.Text, w.More = fewLines(text); w.Text == "" {
		return nil
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

// newestWait is the newest card that waits for the user's yes in a live
// node, shown since the user's last message read (after: its seq) or in
// the turn under way; nil when none does. An older card waits in its page,
// its bubble says so: the user moved on from it.
func (c *core) newestWait(after int) *orbWait {
	if c.nodes == nil {
		return nil
	}
	xs, err := c.cardsWhere(`state = ? AND (seq > ? OR turn = 1) ORDER BY id DESC LIMIT 20`, cardPending, after)
	if err != nil {
		return nil
	}
	for _, x := range xs {
		if x, _ = c.liveCard(x); x.State != cardPending {
			continue
		}
		for _, id := range c.cardNodes(x.Ref) {
			if sum, err := c.nodes.Get(id); err == nil && sum.Archived.IsZero() {
				return &orbWait{Card: x.ID, Node: id}
			}
		}
	}
	return nil
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
// wordRunes runes, a long line cut as ackLine cuts it; cut tells that they
// are not all of it.
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
		line, short := ackLine(t)
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

// answerKey marks a message of Eriphe's written in a turn that answers
// messages of the user's (markAnswer).
func answerKey(seq int) string { return fmt.Sprintf("answer_msg:%d", seq) }

// markAnswer marks a message the brain just wrote when its turn answers
// the user, not events or a wake only: the orb holds it as the answer, and
// an event's words never take its place.
func (c *core) markAnswer(seq int) {
	if c.turnAnswersUser() {
		c.store.set(answerKey(seq), "1")
	}
}
