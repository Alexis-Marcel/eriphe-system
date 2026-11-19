package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Cards in the conversation, as decided in the study "Une conversation
// plutôt qu'un tableau de bord" (2026-11-01): what waits for the user's
// approval is asked where they talk to the assistant, not on a page of its
// own. A card is bound by the code to exactly one proposal (a memory, an
// intention, a notification rule, a watch, a setting):
// its Oui and Non call that proposal's own approval, and nothing else does.
//
//   - A proposal the brain makes in a turn is attached to the message it
//     writes in that turn (attachTurnCards), or, if it writes nothing after
//     it, to its last message of the turn (cardsTurnEnded).
//   - Any other proposal (the memory's extraction, a turn that said
//     nothing) is queued, and enters the attention queue at its card's
//     weight (cardWeight, attendQueuedCards): it is delivered like
//     anything else that goes to the user, under a message of eriphe-core's,
//     a few at a time (attention.go).
//   - A card that stops a task (blocksATask) never waits for a good moment:
//     if its turn wrote nothing, as when the turn failed, it goes with the
//     brain's next message, whatever the hour.
//   - The brain never approves: no tool of its decides a card. A plain
//     "oui" or "non" of the user's may, under strict rules (resolveByText).
//
// A task's raw permissions (a held tool's) keep their own cards,
// at the end of the conversation (pending); what the grid asks is a card
// here (access_card.go).

const convoCardsSchema = `CREATE TABLE IF NOT EXISTS convo_cards (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	ref      TEXT NOT NULL UNIQUE,
	kind     TEXT NOT NULL,
	seq      INTEGER NOT NULL DEFAULT 0,
	turn     INTEGER NOT NULL DEFAULT 0,
	state    TEXT NOT NULL DEFAULT 'pending',
	text     TEXT NOT NULL DEFAULT '',
	created  INTEGER NOT NULL,
	attached INTEGER NOT NULL DEFAULT 0,
	decided  INTEGER NOT NULL DEFAULT 0,
	by       TEXT NOT NULL DEFAULT '',
	about  TEXT NOT NULL DEFAULT ''   -- what it is about: node:<id> or an entity's key (about.go)
);
CREATE INDEX IF NOT EXISTS convo_cards_seq ON convo_cards (seq);`

func init() { extraSchema = append(extraSchema, convoCardsSchema) }

// The kinds of cards: one per kind of proposal.
const (
	cardMemory    = "memory"
	cardIntention = "intention"
	cardRule      = "rule"
	cardSetting   = "setting"

	cardPending  = "pending"
	cardApproved = "approved"
	cardRejected = "rejected"
	cardGone     = "gone" // withdrawn, replaced, or decided where the card cannot tell

	cardTextAnswer = 30 * time.Minute // a plain yes or no answers a card this recent only
)

var proposalKinds = []string{cardMemory, cardIntention, cardRule, cardSetting, cardTaskStart, cardUnlearn, cardAccess, cardSkill, cardRunCalls, cardCalls, cardMonitor, cardViewKind}

// cardUnlearn is an undo of the learning journal the brain proposed on a
// tainted turn (learning_tools.go).
const cardUnlearn = "unlearn"

// cardMu makes deciding a card one thing at a time: a click and a "oui"
// at the same moment decide it once.
var cardMu sync.Mutex

// convoCard is a card as the store keeps it.
type convoCard struct {
	ID       int64
	Ref      string // kind:id
	Kind     string
	Seq      int  // the message it is shown with; 0 while queued
	Turn     bool // to be attached to the brain's next message
	State    string
	Text     string // what it said when made, in the user's language (userLang): shown once its proposal is gone
	Created  time.Time
	Attached time.Time
	Decided  time.Time
	By       string // web, a channel's id, text, or page (the fallback page)
	About    string // what it is about: its proposal's, else its maker's (cardAbout)
}

func proposalRef(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }

func parseProposalRef(ref string) (string, int64, bool) {
	kind, raw, ok := strings.Cut(ref, ":")
	id, err := strconv.ParseInt(raw, 10, 64)
	return kind, id, ok && err == nil && id > 0 && slices.Contains(proposalKinds, kind)
}

const cardColumns = `id, ref, kind, seq, turn, state, text, created, attached, decided, by, about`

func scanCard(row interface{ Scan(...any) error }) (convoCard, error) {
	var x convoCard
	var created, attached, decided int64
	err := row.Scan(&x.ID, &x.Ref, &x.Kind, &x.Seq, &x.Turn, &x.State, &x.Text, &created, &attached, &decided, &x.By, &x.About)
	x.Created, x.Attached, x.Decided = unixOrZero(created), unixOrZero(attached), unixOrZero(decided)
	return x, err
}

func unixOrZero(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func (c *core) cardsWhere(where string, args ...any) ([]convoCard, error) {
	rows, err := c.store.db.Query(`SELECT `+cardColumns+` FROM convo_cards WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []convoCard
	for rows.Next() {
		x, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (c *core) card(id int64) (convoCard, error) {
	return scanCard(c.store.db.QueryRow(`SELECT `+cardColumns+` FROM convo_cards WHERE id = ?`, id))
}

// The proposals, as cards read them.

// proposal is one proposal of any kind, read now.
type proposal struct {
	Kind      string
	ID        int64
	Pending   bool   // still waits for the user
	Outcome   string // when not pending: approved, rejected or gone
	Tainted   string // why it may carry what someone else wrote, if it may
	Why       string
	Fields    []cardField // the parameters of a call to approve
	Extracted bool        // from the memory's extraction, not from a turn of the brain's
	text      func(lang string) string
	fields    func(lang string) []cardField // Fields in the user's language, when they have words of ours
	summary   func(lang string) []string    // what a yes makes, in plain sentences of code's, before the fields
	origin    func(lang string) cardOrigin  // where it comes from and what its Oui does (card_origin.go)
}

// Origin is where it comes from and what its Oui does, with the words of
// lang: read only when its card is shown.
func (p proposal) Origin(lang string) cardOrigin {
	if p.origin == nil {
		return cardOrigin{}
	}
	return p.origin(lang)
}

// FieldsIn are its fields, with the words of lang.
func (p proposal) FieldsIn(lang string) []cardField {
	if p.fields != nil {
		return p.fields(lang)
	}
	return p.Fields
}

// SummaryIn is what a yes makes, in plain sentences, with the words of
// lang: none for most kinds, whose text and fields say it.
func (p proposal) SummaryIn(lang string) []string {
	if p.summary == nil {
		return nil
	}
	return p.summary(lang)
}

// Text is what it proposes, in a line, with the words of lang.
func (p proposal) Text(lang string) string {
	if p.text == nil {
		return ""
	}
	return p.text(lang)
}

// proposalOf reads a proposal; a missing one is gone.
func (c *core) proposalOf(kind string, id int64) proposal {
	p := proposal{Kind: kind, ID: id, Outcome: cardGone}
	switch kind {
	case cardMemory:
		if c.mem == nil {
			return p
		}
		x, err := c.mem.memory(id)
		if err != nil {
			return p
		}
		p.Pending, p.Extracted = x.Status == memProposed, x.SourceKind == srcExtraction
		p.Tainted = externalWhy(x.Trust, x.Flags, x.SourceRef)
		switch x.Status {
		case memDeleted:
			p.Outcome = cardRejected
		case memActive, memArchived:
			p.Outcome = cardApproved
		}
		p.text = func(string) string { return x.Text }
	case cardIntention:
		if c.mem == nil {
			return p
		}
		x, err := c.mem.intention(id)
		if err != nil {
			return p
		}
		p.Pending, p.Extracted = x.State == intentProposed, x.SourceKind == srcExtraction
		p.Tainted = externalWhy(x.Trust, x.Flags, x.SourceRef)
		p.Outcome = cardApproved
		if x.State == intentCancelled {
			p.Outcome = cardRejected
		}
		p.text = func(lang string) string {
			if x.DueAt.IsZero() {
				return x.Text
			}
			return x.Text + " · " + x.DueAt.In(c.notifyZone()).Format("2006-01-02 15:04")
		}
	case cardRule:
		r, err := c.notifyRule(id)
		if err != nil {
			return p
		}
		p.Pending, p.Tainted, p.Why, p.Outcome = r.State == ruleProposed, r.Tainted, r.Why, cardApproved
		p.text = func(lang string) string { return ruleText(r, lang) }
	case cardSetting:
		var name, value, why, tainted string
		err := c.store.db.QueryRow(`SELECT name, value, why, tainted FROM setting_proposals WHERE id = ?`, id).Scan(&name, &value, &why, &tainted)
		if err != nil {
			return p
		}
		p.Pending, p.Tainted, p.Why = true, tainted, why
		p.text = func(lang string) string { return tr(lang, "setting.name."+name) + " : " + value }
	case cardTaskStart:
		return c.taskStartProposal(p) // task_start.go
	case cardAccess:
		return c.accessProposal(p) // access_card.go
	case cardSkill:
		return c.skillProposal(p) // skill_write.go
	case cardRunCalls:
		return c.runCallsProposal(p) // skill_cards.go
	case cardCalls:
		return c.callsProposal(p) // item_cards.go
	case cardMonitor:
		return c.monitorProposal(p) // monitors.go
	case cardViewKind:
		return c.viewProposalOf(p) // view_card.go
	case cardUnlearn:
		if c.mem == nil {
			return p
		}
		e, err := c.mem.learning(id)
		if err != nil {
			return p
		}
		p.Pending, p.Why = e.Undo == undoProposed && e.Status != learnReverted, e.UndoWhy
		p.Outcome = cardApproved
		if e.Undo == undoRejected {
			p.Outcome = cardRejected
		}
		if e.Trust == trustExternal {
			p.Tainted = "external:" + e.Target()
		}
		p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "card.unlearn"), learnedSummary(e, lang)) }
	}
	return p
}

// externalWhy is why a memory or an intention may carry what someone else
// wrote: drawn from external content, or flagged by the scanner.
func externalWhy(trust, flags, ref string) string {
	switch {
	case trust == trustExternal:
		return "external:" + ref
	case flags != "":
		return "flags:" + flags
	}
	return ""
}

// ruleText is a notification rule in a line.
func ruleText(r notifyRule, lang string) string {
	if r.Kind == ruleQuiet {
		return fmt.Sprintf(tr(lang, "rule.quietline"), tr(lang, "rule.days."+r.Days), r.Window())
	}
	s := aboutLabel(r.Match) + " → " + ruleBoundsText(r, lang)
	if r.Night {
		s += " · " + tr(lang, "rule.night")
	}
	return s
}

// ruleBoundsText says a topic rule's bounds of weight in the user's words.
func ruleBoundsText(r notifyRule, lang string) string {
	switch {
	case r.Ceiling == 0:
		return tr(lang, "rule.out.never")
	case r.Floor > 0 && r.Ceiling < weightInterrupts:
		return fmt.Sprintf(tr(lang, "rule.between"), r.Floor, r.Ceiling)
	case r.Floor > 0:
		return fmt.Sprintf(tr(lang, "rule.floor"), r.Floor)
	}
	return fmt.Sprintf(tr(lang, "rule.ceiling"), r.Ceiling)
}

// pendingProposals lists every proposal that waits, oldest first by kind.
func (c *core) pendingProposals() []proposal {
	var out []proposal
	add := func(kind string, id int64) {
		if p := c.proposalOf(kind, id); p.Pending {
			out = append(out, p)
		}
	}
	if m := c.mem; m != nil {
		// The extraction's proposals get no card: they wait in À valider
		// and expire undecided (memory_extract.go).
		if xs, err := m.list(`status = 'proposed' AND source_kind != ?`, []any{srcExtraction}, `recorded_at, id`, 200); err == nil {
			for _, x := range xs {
				add(cardMemory, x.ID)
			}
		}
		if xs, err := m.intentions(intentProposed); err == nil {
			for _, x := range xs {
				add(cardIntention, x.ID)
			}
		}
	}
	if rs, err := c.notifyRules(ruleProposed); err == nil {
		for _, r := range rs {
			add(cardRule, r.ID)
		}
	}
	if ps, err := c.settingProposals(); err == nil {
		for _, s := range ps {
			add(cardSetting, s.ID)
		}
	}
	for _, id := range c.proposedStarts() { // task_start.go
		add(cardTaskStart, id)
	}
	for _, id := range c.proposedAccess() {
		add(cardAccess, id)
	}
	if rows, err := c.store.db.Query(`SELECT rowid FROM skills WHERE pending != '' AND source = ?`, skillFromEriphe); err == nil {
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			add(cardSkill, id)
		}
	}
	for _, id := range c.runsWithCalls() {
		add(cardRunCalls, id)
	}
	for _, id := range c.proposedBatches() {
		add(cardCalls, id)
	}
	for _, id := range c.proposedMonitors() {
		add(cardMonitor, id)
	}
	for _, id := range c.proposedViews() {
		add(cardViewKind, id)
	}
	if m := c.mem; m != nil {
		rows, err := m.db.Query(`SELECT id FROM learning_log WHERE undo = ? AND status != ? ORDER BY id`, undoProposed, learnReverted)
		if err == nil {
			var ids []int64
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				add(cardUnlearn, id)
			}
		}
	}
	return out
}

// decideProposal applies the user's answer to one proposal, through the
// same code as the pages' buttons. It refuses a proposal that no longer
// waits: an answer never touches what is already in force.
func (c *core) decideProposal(ctx context.Context, kind string, id int64, approve bool) error {
	p := c.proposalOf(kind, id)
	if !p.Pending {
		return errCardDecided
	}
	switch kind {
	case cardMemory:
		if approve {
			_, err := c.mem.approve(id, "owner")
			return err
		}
		return c.mem.forget(id, "owner", "rejected")
	case cardIntention:
		ch, action := intentionChange{State: intentOpen}, "approve"
		if !approve {
			ch, action = intentionChange{State: intentCancelled}, "reject"
		}
		_, err := c.mem.changeIntention(id, ch, "owner", action)
		return err
	case cardRule:
		if approve {
			_, err := c.approveNotifyRule(id)
			return err
		}
		return c.deleteNotifyRule(id)
	case cardSetting:
		return c.decideSetting(id, approve)
	case cardTaskStart:
		return c.decideTaskStart(id, approve)
	case cardAccess:
		return c.decideAccess(ctx, id, approve)
	case cardSkill:
		return c.decideSkill(id, approve)
	case cardRunCalls:
		return c.decideRunCalls(ctx, id, approve)
	case cardCalls:
		return c.decideCalls(ctx, id, approve)
	case cardMonitor:
		return c.decideMonitor(id, approve)
	case cardViewKind:
		return c.decideView(id, approve)
	case cardUnlearn:
		if approve {
			_, err := c.unlearn(id, "owner", p.Why, nil)
			return err
		}
		return c.mem.setUndo(id, undoRejected, p.Why)
	}
	return fmt.Errorf("no proposal kind %q", kind)
}

var (
	errCardDecided = errors.New("this proposal no longer waits")
	errNoCard      = errors.New("no such card")
)

// The cards.

// cardProposed makes the card of a proposal the brain just made: it goes
// with the brain's next message of this turn.
func (c *core) cardProposed(kind string, id int64) {
	inherit, source := c.turnAbout, c.turnSource() // made in the brain's turn
	switch kind {
	case cardRunCalls:
		inherit, source = nil, "" // a skill's run: its own (cardAbout)
	default:
		c.turn.wrote() // what the turn says is about its card: the net leaves it in the exchange (node_net.go)
	}
	c.cardProposedBy(kind, id, inherit, source)
}

// cardProposedBy is cardProposed for a proposal its maker's about
// inherits (nil: none) and source raised: a card born in a turn about a
// node is linked to it by the code, and the node waits for the user.
func (c *core) cardProposedBy(kind string, id int64, inherit func() string, source string) {
	if source != "" {
		c.cardRaisedFor(source, proposalRef(kind, id)) // linked to its node (node_turn.go)
	}
	p := c.proposalOf(kind, id)
	if !p.Pending {
		return
	}
	c.cardAgain(p)
	c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, turn, text, created) VALUES (?, ?, 1, ?, ?)
		ON CONFLICT (ref) DO UPDATE SET turn = 1 WHERE seq = 0`, proposalRef(kind, id), kind, p.Text(c.userLang()), time.Now().Unix())
	c.cardAbout(kind, id, inherit)
	c.proposalMoved(proposalRef(kind, id))
}

// cardNow puts the card of a proposal made outside a brain turn in the
// attention queue (attention.go): no message of the brain's will carry it,
// so the queue delivers it under a message of its own, text (as the
// brain's, written by eriphe-core): at once when a task or a node waits
// on it (weight 4), else at its breakpoint. source is who made it
// (task:<id>, node:<id>, skill:<name>). A card already shown and still
// waiting is not shown twice. The brain is not woken: it reads the open
// items at its next turn, and nothing it does waits on the card.
func (c *core) cardNow(kind string, id int64, text, source string) {
	p := c.proposalOf(kind, id)
	if !p.Pending {
		return
	}
	ref := proposalRef(kind, id)
	c.cardRaisedFor(source, ref) // a node's: linked to it (node_turn.go)
	var shown int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM convo_cards WHERE ref = ? AND seq > 0 AND state = ?`, ref, cardPending).Scan(&shown)
	if shown > 0 {
		return
	}
	c.cardAgain(p)
	c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES (?, ?, ?, ?)
		ON CONFLICT (ref) DO UPDATE SET turn = 0 WHERE seq = 0`, ref, kind, p.Text(c.userLang()), time.Now().Unix())
	c.proposalMoved(ref)
	c.cardAbout(kind, id, func() string { return c.sourceAbout(source) })
	if x, err := c.cardsWhere(`ref = ?`, ref); err == nil && len(x) == 1 {
		c.attendCard(x[0], p, text, source)
	}
	c.bump()
}

// cardAgain: the same proposal waits again after its card was decided (a
// change of a skill or a monitor already approved). It gets a new card:
// the decided one stays under its message as it was, its ref set aside
// (ref@card id, read as no proposal), so a page never shows two cards of
// one id, the Oui of the new one landing on the old.
func (c *core) cardAgain(p proposal) {
	c.store.db.Exec(`UPDATE convo_cards SET ref = ref || '@' || id WHERE ref = ? AND state != ?`,
		proposalRef(p.Kind, p.ID), cardPending)
}

// syncCards gives every waiting proposal without a waiting card one,
// queued.
func (c *core) syncCards() {
	for _, p := range c.pendingProposals() {
		c.cardAgain(p)
		res, err := c.store.db.Exec(`INSERT OR IGNORE INTO convo_cards (ref, kind, text, created) VALUES (?, ?, ?, ?)`,
			proposalRef(p.Kind, p.ID), p.Kind, p.Text(c.userLang()), time.Now().Unix())
		if n, _ := rowsOf(res, err); n > 0 {
			c.proposalMoved(proposalRef(p.Kind, p.ID))
		}
	}
}

// attachTurnCards attaches the cards of this turn to the brain's message
// seq. Called with relayMu held, as the message is written.
func (c *core) attachTurnCards(seq int) {
	if seq <= 0 {
		return
	}
	c.claimBlockingCards()
	xs, _ := c.cardsWhere(`seq = 0 AND turn = 1`)
	c.store.db.Exec(`UPDATE convo_cards SET seq = ?, turn = 0, attached = ? WHERE seq = 0 AND turn = 1`, seq, time.Now().Unix())
	c.brainSeqOfTurn = seq
	for _, x := range xs {
		x.Seq = seq
		if x, p := c.liveCard(x); x.State == cardPending {
			c.attendCard(x, p, "", byBrain)
		}
	}
}

// claimBlockingCards takes the cards back from the queue that stop a task:
// the task waits on them, so the offer's quiet hours are not for them. One
// whose task stopped meanwhile stays out of the conversation.
func (c *core) claimBlockingCards() {
	xs, err := c.cardsWhere(`seq = 0 AND turn = 0 AND state = ? AND kind IN (?, ?)`, cardPending, cardTaskStart, cardAccess)
	if err != nil {
		return
	}
	for _, x := range xs {
		if x, _ = c.liveCard(x); x.State == cardPending {
			c.store.db.Exec(`UPDATE convo_cards SET turn = 1 WHERE id = ? AND seq = 0`, x.ID)
		}
	}
}

// cardsTurnEnded attaches what the brain proposed after its last message
// to that message; without one in the turn, its cards go back to the
// queue. Called with relayMu held.
func (c *core) cardsTurnEnded() {
	if c.brainSeqOfTurn > 0 {
		c.attachTurnCards(c.brainSeqOfTurn)
	} else {
		c.store.db.Exec(`UPDATE convo_cards SET turn = 0 WHERE seq = 0 AND turn = 1`)
	}
	c.brainSeqOfTurn = 0
}

// liveCard reads a card's proposal and keeps its state: a proposal decided
// elsewhere, or gone, is said so on its card.
func (c *core) liveCard(x convoCard) (convoCard, proposal) {
	kind, id, ok := parseProposalRef(x.Ref)
	if !ok {
		return x, proposal{}
	}
	p := c.proposalOf(kind, id)
	if x.State == cardPending && !p.Pending {
		x.State = p.Outcome
		res, err := c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ? WHERE id = ? AND state = ?`, x.State, time.Now().Unix(), x.ID, cardPending)
		if n, _ := rowsOf(res, err); n > 0 {
			c.proposalMoved(x.Ref)
		}
	}
	return x, p
}

// decideCard is a card's Oui or Non: its proposal's own approval.
func (c *core) decideCard(ctx context.Context, id int64, approve bool, by string) (convoCard, error) {
	cardMu.Lock()
	defer cardMu.Unlock()
	x, err := c.card(id)
	if err != nil {
		return x, errNoCard
	}
	if x, _ = c.liveCard(x); x.State != cardPending {
		return x, errCardDecided
	}
	kind, pid, _ := parseProposalRef(x.Ref)
	if err := c.decideProposal(ctx, kind, pid, approve); err != nil {
		return x, err
	}
	if kind == cardTaskStart && !approve && by != "text" {
		c.taskStartDeclined(pid) // a "non" in words is told with the message
	}
	if kind == cardAccess && by != "text" {
		c.accessDecidedTold(pid, approve)
	}
	x.State, x.Decided, x.By = cardRejected, time.Now(), by
	if approve {
		x.State = cardApproved
	}
	c.attentionDone(cardItemRef(x.ID))
	c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ?, by = ? WHERE id = ?`, x.State, x.Decided.Unix(), by, id)
	c.proposalMoved(x.Ref)
	c.linkedCardMoved(x.Ref) // the nodes it is linked to (node_turn.go)
	log.Printf("cards: %s %s by %s", x.Ref, x.State, by)
	return x, nil
}

// proposalDecided keeps a card in step with a decision made on a page.
func (c *core) proposalDecided(kind string, id int64, approved bool) {
	state := cardRejected
	if approved {
		state = cardApproved
	}
	c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ?, by = 'page' WHERE ref = ? AND state = ?`,
		state, time.Now().Unix(), proposalRef(kind, id), cardPending)
	c.proposalMoved(proposalRef(kind, id))
	c.linkedCardMoved(proposalRef(kind, id))
}

// cardNote tells the brain where its proposal waits: on a card, with its message.
func cardNote(user string) string {
	return "A card with Oui / Non goes with your message in the conversation (and on the channel " + user + " writes from, if any): " +
		"say what you propose in one natural sentence, never as a list of pending items; " + user + " answers on the card, never you."
}

// Free text: a plain "oui" or "non".

var (
	yesWords = []string{"oui", "ouais", "ok", "okay", "d'accord", "dac", "vas-y", "vas y", "go", "oui merci", "oui vas-y", "oui stp",
		"oui s'il te plaît", "parfait", "ça marche", "yes", "yep", "sure", "ok merci", "c'est bon", "valide", "je valide", "approuve"}
	noWords = []string{"non", "nan", "non merci", "pas besoin", "laisse tomber", "no", "nope", "non stp", "refuse", "je refuse", "surtout pas"}
)

// plainAnswer reads a message that is only a yes or a no: anything more
// ("non, plutôt 8 h") is not one, and the brain reads it.
func plainAnswer(text string) (yes, ok bool) {
	t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(text), "’", "'"))
	t = strings.TrimFunc(t, func(r rune) bool { return unicode.IsPunct(r) && r != '\'' || unicode.IsSpace(r) || unicode.IsSymbol(r) })
	t = strings.Join(strings.Fields(t), " ")
	switch {
	case slices.Contains(yesWords, t):
		return true, true
	case slices.Contains(noWords, t):
		return false, true
	}
	return false, false
}

// cardAnswerKey keeps, for the brain, what a message of the user's decided.
func cardAnswerKey(seq int) string { return fmt.Sprintf("card_answer:%d", seq) }

// resolveByText lets a message of the user's answer a card in words. Only
// when all of this holds, else the brain reads it and asks, or the user
// clicks: the message is the user's own (not an event), through a channel
// that guarantees it is them (not one the user did not trust, where
// anyone may write), a plain yes or no and nothing else, without a file; it comes
// right after a message of the brain's, recent, written in a clean turn,
// that shows exactly one card, still waiting; and that proposal does not
// come from external content.
func (c *core) resolveByText(ctx context.Context, seq int) (convoCard, bool) {
	var none convoCard
	// In the message's own node: the card above it in its exchange (node_turn.go).
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq <= ? AND role IN ('user', 'brain')
		AND node = (SELECT node FROM messages WHERE seq = ?) ORDER BY seq DESC LIMIT 2`, seq, seq)
	if err != nil || len(msgs) != 2 {
		return none, false
	}
	me, brain := msgs[0], msgs[1]
	if me.Seq != seq || me.Role != roleUser || brain.Role != roleBrain || c.fromUntrusted(me.Seq) {
		return none, false
	}
	if len(me.Attachments) > 0 {
		return none, false
	}
	yes, ok := plainAnswer(me.Text)
	if !ok || me.Time.Sub(brain.Time) > cardTextAnswer {
		return none, false
	}
	if c.store.get(taintKey(brain.Seq)) != "" {
		return none, false // written in a tainted turn: the buttons only
	}
	cards, err := c.cardsWhere(`seq = ?`, brain.Seq)
	if err != nil || len(cards) != 1 {
		return none, false
	}
	x, p := c.liveCard(cards[0])
	if x.State != cardPending || p.Tainted != "" || x.Kind == cardAccess || x.Kind == cardCalls {
		return none, false // an access card comes at any moment, an item's is answered on its row: a yes may be for something else, so their buttons only
	}
	x, err = c.decideCard(ctx, x.ID, yes, "text")
	if err != nil {
		return none, false
	}
	c.store.set(cardAnswerKey(seq), x.State+"|"+x.Ref+"|"+truncate(p.Text("fr"), 200))
	return x, true
}

// channelCardConfirm is what a channel not trusted says when the brain's
// answer to message seq left a card: anyone may have written through it,
// so a card is never answered there; the user confirms it on their phone
// or in the workspace, where it is sent (channelCardNote). "" without one.
func (c *core) channelCardConfirm(seq int, lang string) (convoCard, string) {
	xs, err := c.cardsWhere(`seq > ? AND state = ?`, seq, cardPending)
	if err != nil || len(xs) == 0 {
		return convoCard{}, ""
	}
	return xs[0], tr(lang, "card.channel.confirm")
}

// channelCardNote tells the user, on their phone or computer, that what
// came through a channel not trusted waits for their yes: the code's words,
// never the card's.
func (c *core) channelCardNote(x convoCard) pushNote {
	url := nodeAt("")
	if x.Seq > 0 {
		url = c.messageAt(x.Seq)
	}
	n := waitingNote(tr("fr", "card.channel.note"), url, fmt.Sprintf("voice-card-%d", x.ID))
	n.Body["en"] = assistantName() + " is waiting for your answer: " + tr("en", "card.channel.note")
	return n
}

// cardAnswerLine tells the brain what a message of the user's decided.
func (c *core) cardAnswerLine(seq int) string {
	raw := c.store.get(cardAnswerKey(seq))
	if raw == "" {
		return ""
	}
	parts := strings.SplitN(raw, "|", 3)
	if len(parts) != 3 {
		return ""
	}
	verb := "validé"
	if parts[0] == cardRejected {
		verb = "refusé"
	}
	return fmt.Sprintf("\n(eriphe-core : cette réponse a %s la proposition %s, « %s ». C'est déjà fait : ne la refais pas, confirme en quelques mots.)",
		verb, parts[1], packQuote(parts[2]))
}

// Withdrawn with their subject.

// cardByArchive is who decided a card withdrawn with its node.
const cardByArchive = "archive"

// withdrawCards withdraws the cards that wait on nodes the user just
// archived (ids): archiving is setting the subject aside, so what it asked
// goes with it. A card is theirs when linked to one of them (node_cards)
// or about one (node:<id>); one a live node still holds stays. Each card
// is gone, decided by the archive, and its proposal no longer waits: a
// view is dropped, any other is answered no through its own approval,
// without decideCard's words to the brain or a task. So no card comes
// back, not even when the node is reopened; the brain may propose again,
// and no longer reads it among what waits (pendingProposals). Called after
// the archive, before the nodes are unlinked from their cards
// (linkedNodeGone).
func (c *core) withdrawCards(ctx context.Context, ids []string) {
	refs := map[string]bool{}
	for _, id := range ids {
		for _, q := range []struct{ query, arg string }{
			{`SELECT ref FROM node_cards WHERE node = ?`, id},
			{`SELECT ref FROM convo_cards WHERE about = ? AND state = 'pending'`, refNode + ":" + id},
		} {
			rows, err := c.store.db.Query(q.query, q.arg)
			if err != nil {
				continue
			}
			for rows.Next() {
				var ref string
				if rows.Scan(&ref) == nil {
					refs[ref] = true
				}
			}
			rows.Close()
		}
	}
	if len(refs) == 0 {
		return
	}
	cardMu.Lock()
	defer cardMu.Unlock()
	moved := false
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		kind, pid, ok := parseProposalRef(ref)
		if !ok || c.cardHeld(ref) {
			continue
		}
		xs, _ := c.cardsWhere(`ref = ? AND state = ?`, ref, cardPending)
		for _, x := range xs {
			c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ?, by = ? WHERE id = ? AND state = ?`,
				cardGone, time.Now().Unix(), cardByArchive, x.ID, cardPending)
			c.attentionDone(cardItemRef(x.ID))
		}
		pending := c.proposalOf(kind, pid).Pending
		if pending {
			if err := c.withdrawProposal(ctx, kind, pid); err != nil && !errors.Is(err, errCardDecided) {
				log.Printf("cards: %s: withdrawn with its node: %v", ref, err)
			}
		}
		if len(xs) > 0 || pending {
			c.proposalMoved(ref)
			log.Printf("cards: %s gone with its archived node", ref)
			moved = true
		}
	}
	if moved {
		c.bump()
	}
}

// withdrawProposal makes a proposal wait no more, undecided by the user:
// a view is dropped, a memory forgotten; another kind is answered no
// through its own approval, which records it.
func (c *core) withdrawProposal(ctx context.Context, kind string, id int64) error {
	switch kind {
	case cardViewKind:
		_, err := c.store.db.Exec(`UPDATE view_proposals SET state = ? WHERE id = ? AND state = ?`, viewDropped, id, viewProposed)
		return err
	case cardMemory:
		return c.mem.forget(id, "owner", "withdrawn: its node was archived")
	}
	return c.decideProposal(ctx, kind, id, false)
}
