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
// own. A card is bound by the code to exactly one proposal (an
// intention, a notification rule, a watch, a setting):
// its Oui and Non call that proposal's own approval, and nothing else does.
//
//   - A proposal the brain makes in a turn is attached to the message it
//     writes in that turn (attachTurnCards), or, if it writes nothing after
//     it, to its last message of the turn (cardsTurnEnded).
//   - Any other proposal (a turn that said nothing) is queued, and enters
//     the attention queue at its card's weight (cardWeight,
//     attendQueuedCards): it is delivered like anything else that goes to
//     the user, under a message of eriphe-core's, a few at a time
//     (attention.go).
//   - A card that stops a task (blocksATask) never waits for a good moment:
//     if its turn wrote nothing, as when the turn failed, the queue
//     delivers it at once, whatever the hour (its weight); one still queued
//     goes with the brain's next message (claimBlockingCards).
//   - A card's state is the one truth of what waits: its proposal moves it
//     the moment it moves, wherever it was answered (settleCards).
//   - The brain never approves: no tool of its decides a card. A plain
//     "oui" or "non" of the user's may, under strict rules (resolveByText).
//
// What a task asks of the user (what the grid asks, the release of a tool
// lent held back) is a card here too (access_card.go).

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
	about  TEXT NOT NULL DEFAULT ''  -- what it is about: node:<id> or an entity's key (about.go)
);
CREATE INDEX IF NOT EXISTS convo_cards_seq ON convo_cards (seq);`

func init() { extraSchema = append(extraSchema, convoCardsSchema) }

// The kinds of cards: one per kind of proposal.
const (
	cardIntention = "intention"
	cardRule      = "rule"
	cardSetting   = "setting"

	cardPending  = "pending"
	cardApproved = "approved"
	cardRejected = "rejected"
	cardGone     = "gone" // withdrawn, replaced, or decided where the card cannot tell

	cardTextAnswer = 30 * time.Minute // a plain yes or no answers a card this recent only
)

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
	By       string // web, a channel's id, text, page (the fallback page), or archive (withdrawCards)
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
	Kind    string
	ID      int64
	Pending bool   // still waits for the user
	Outcome string // when not pending: approved, rejected or gone
	Tainted string // why it may carry what someone else wrote, if it may
	Why     string
	Fields  []cardField // the parameters of a call to approve
	text    func(lang string) string
	fields  func(lang string) []cardField      // Fields in the user's language, when they have words of ours
	summary func(lang string) []string         // what a yes makes, in plain sentences of code's, before the fields
	context func(lang string) string           // where it acts, said quietly beside its text (an account: « compte Gmail »)
	origin  func(lang string) cardOrigin       // where it comes from and what its Oui does (card_origin.go)
	choices func(lang string) []proposalChoice // a calls card's calls the user may leave out (call_fold.go)
	acts    func(lang string) []string         // what its Oui says for 1, 2… items chosen
	verb    func(lang string) string           // what its yes button says, if not its kind's (card_verb.go)
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

// ContextIn is where it acts, in a few quiet words of lang: "" for most
// kinds.
func (p proposal) ContextIn(lang string) string {
	if p.context == nil {
		return ""
	}
	return p.context(lang)
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
	if k, ok := kindsOf[kind]; ok {
		return k.read(c, p)
	}
	return p
}

// externalWhy is why an intention may carry what someone else
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
	for _, kind := range proposalKinds {
		for _, id := range kindsOf[kind].waiting(c) {
			if p := c.proposalOf(kind, id); p.Pending {
				out = append(out, p)
			}
		}
	}
	return out
}

// decideProposal applies the user's answer to one proposal, by its kind.
// Only decideCardWith calls it (and withdrawProposal, for a no nobody
// said): every yes goes through a card (TestOneWayToSayYes). It refuses a
// proposal that no longer waits: an answer never touches what is already
// in force.
func (c *core) decideProposal(ctx context.Context, kind string, id int64, approve bool) error {
	k, ok := kindsOf[kind]
	if !ok {
		return fmt.Errorf("no proposal kind %q", kind)
	}
	if !c.proposalOf(kind, id).Pending {
		return errCardDecided
	}
	if err := k.decides(c, ctx, id, approve); err != nil {
		return err
	}
	c.settleRef(proposalRef(kind, id)) // its card, at once (decideCardWith then says who)
	return nil
}

var (
	errCardDecided = errors.New("this proposal no longer waits")
	errNoCard      = errors.New("no such card")
)

// The cards.

// cardProposed makes the card of a proposal the brain just made: it goes
// with the brain's next message of this turn. It gives the card's ref,
// which the brain's tool returns (card): a card block of the node's
// document places it (nodefile's card block).
func (c *core) cardProposed(kind string, id int64) string {
	inherit, source := c.turnAbout, c.turnSource() // made in the brain's turn
	c.turn.wrote()                                 // what the turn says is about its card: the net leaves it in the exchange (node_net.go)
	c.cardRaisedBy(proposalRef(kind, id), byBrain)
	c.cardProposedBy(kind, id, inherit, source)
	return proposalRef(kind, id)
}

// cardProposedBy is cardProposed for a proposal its maker's about
// inherits (nil: none) and source raised: a card born in a turn about a
// node is linked to it by the code, and the node waits for the user.
func (c *core) cardProposedBy(kind string, id int64, inherit func() string, source string) {
	c.cardRaisedFor(source, proposalRef(kind, id)) // linked to its node, if any (node_turn.go)
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
	if kind == cardCalls {
		c.itemCardMoved(id) // its item's row (item_cards.go)
	}
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
// queued, linked to the node it belongs to (one raised before it was
// linked waits there too). Only at start (reconcileCards): a proposal
// gets its card where it is made.
func (c *core) syncCards() {
	for _, p := range c.pendingProposals() {
		c.syncCard(p)
	}
}

// reconcileCards puts the cards in step with their proposals once, when
// eriphe-core starts: a proposal and its card are written one after the
// other, not in one transaction, so a stop between the two leaves them
// apart; and what was queued waits in the attention queue again. Past
// that, each proposal moves its card the moment it moves (settleCards).
func (c *core) reconcileCards() {
	c.syncCards()
	c.settleCards()
	c.attendQueuedCards()
}

// settleCards reads the waiting cards of kinds (of all kinds when none)
// again against their proposals: a proposal decided elsewhere, or gone,
// is said so on its card at once, it leaves the attention queue, and its
// nodes stop waiting on it (proposalMoved). Whatever moves a proposal
// other than its card calls it: a task's state, the intentions' store,
// a page. It takes no lock: decideCardWith calls it too, through a
// decider.
func (c *core) settleCards(kinds ...string) {
	where, args := `state = ?`, []any{cardPending}
	if len(kinds) > 0 {
		where += ` AND kind IN (?` + strings.Repeat(`, ?`, len(kinds)-1) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	xs, err := c.cardsWhere(where, args...)
	if err != nil {
		return
	}
	for _, x := range xs {
		c.settleCard(x)
	}
}

// settleRef settles the waiting card of one proposal.
func (c *core) settleRef(ref string) {
	xs, err := c.cardsWhere(`ref = ? AND state = ?`, ref, cardPending)
	if err != nil {
		return
	}
	for _, x := range xs {
		c.settleCard(x)
	}
}

// settleCard says on a waiting card what came of its proposal, if it no
// longer waits. A card of a kind that is no more (a memory's) is gone.
func (c *core) settleCard(x convoCard) {
	if x.State != cardPending {
		return
	}
	p := proposal{Outcome: cardGone}
	if kind, id, ok := parseProposalRef(x.Ref); ok {
		if p = c.proposalOf(kind, id); p.Pending {
			return
		}
	}
	res, err := c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ? WHERE id = ? AND state = ?`, p.Outcome, time.Now().Unix(), x.ID, cardPending)
	if n, _ := rowsOf(res, err); n > 0 {
		c.attentionDone(cardItemRef(x.ID))
		c.proposalMoved(x.Ref)
	}
}

// syncCard gives a waiting proposal a waiting card, if it has none.
func (c *core) syncCard(p proposal) {
	c.cardRaisedFor("", proposalRef(p.Kind, p.ID))
	c.cardAgain(p)
	res, err := c.store.db.Exec(`INSERT OR IGNORE INTO convo_cards (ref, kind, text, created) VALUES (?, ?, ?, ?)`,
		proposalRef(p.Kind, p.ID), p.Kind, p.Text(c.userLang()), time.Now().Unix())
	if n, _ := rowsOf(res, err); n > 0 {
		c.proposalMoved(proposalRef(p.Kind, p.ID))
	}
}

// attachTurnCards attaches the cards of this turn to the brain's message
// seq. Called with relayMu held, as the message is written.
func (c *core) attachTurnCards(seq int) {
	if seq <= 0 {
		return
	}
	raised, _ := c.cardsWhere(`seq = 0 AND turn = 1`) // by the turn itself, not a task's taken back
	c.showTurnCards(raised)                           // its node's page in front (node_turn.go)
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
// queue, which delivers them (attendQueuedCards). Called with relayMu
// held.
func (c *core) cardsTurnEnded() {
	if c.brainSeqOfTurn > 0 {
		c.attachTurnCards(c.brainSeqOfTurn)
	} else if res, err := c.store.db.Exec(`UPDATE convo_cards SET turn = 0 WHERE seq = 0 AND turn = 1`); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			c.attendQueuedCards()
		}
	}
	c.brainSeqOfTurn = 0
}

// liveCard is a card with its proposal, read now. The card's state is the
// one truth of whether it waits: its proposal moves it when it moves
// (settleCards).
func (c *core) liveCard(x convoCard) (convoCard, proposal) {
	kind, id, ok := parseProposalRef(x.Ref)
	if !ok {
		return x, proposal{}
	}
	return x, c.proposalOf(kind, id)
}

// decideCard is a card's Oui or ✕: its proposal's own approval.
func (c *core) decideCard(ctx context.Context, id int64, approve bool, by string) (convoCard, error) {
	return c.decideCardWith(ctx, id, approve, by, func(kind string, pid int64) error {
		return c.decideProposal(ctx, kind, pid, approve)
	})
}

// decideCardWith is the one way a proposal is answered, whoever answers:
// a card's Oui or ✕ on the workspace, a word in the bar or a channel
// (decideCard), a page's buttons (answerProposal), a calls card with the
// user's choice (decideCardChosen). Its card says so in the same call.
func (c *core) decideCardWith(ctx context.Context, id int64, approve bool, by string, decide func(kind string, pid int64) error) (convoCard, error) {
	cardMu.Lock()
	defer cardMu.Unlock()
	x, err := c.card(id)
	if err != nil {
		return x, errNoCard
	}
	x, p := c.liveCard(x)
	if x.State != cardPending {
		return x, errCardDecided
	}
	kind, pid, _ := parseProposalRef(x.Ref)
	if err := decide(kind, pid); err != nil {
		if errors.Is(err, errCardDecided) {
			c.settleCard(x) // moved where its card did not hear of it
		}
		return x, err
	}
	x.State, x.Decided, x.By = cardRejected, time.Now(), by
	if approve {
		x.State = cardApproved
	}
	c.attentionDone(cardItemRef(x.ID))
	c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ?, by = ? WHERE id = ?`, x.State, x.Decided.Unix(), by, id)
	c.callsDecided(x)      // its words as decided, what still waits on a new card (call_fold.go)
	c.proposalMoved(x.Ref) // and the nodes it is linked to
	if after := kindsOf[kind].decided; after != nil {
		after(c, pid, approve, by) // what else its kind moves (proposal_kinds.go)
	}
	c.cardOutcome(x, p, kind, pid, approve, by) // the brain is told what came of it (card_outcome.go)
	log.Printf("cards: %s %s by %s", x.Ref, x.State, by)
	return x, nil
}

// answerProposal is a page's yes or no on a proposal (Réglages, Intentions):
// the same as its card's, which it is given first if it has none.
func (c *core) answerProposal(ctx context.Context, kind string, id int64, approve bool) error {
	p := c.proposalOf(kind, id)
	if !p.Pending {
		return errCardDecided
	}
	c.syncCard(p)
	xs, err := c.cardsWhere(`ref = ? AND state = ?`, proposalRef(kind, id), cardPending)
	if err != nil || len(xs) == 0 {
		return errNoCard
	}
	_, err = c.decideCard(ctx, xs[0].ID, approve, "page")
	return err
}

// cardNote tells the brain where its proposal waits: on a card, with its message.
func cardNote(user string) string {
	return "A card with Oui / Non goes with your message in the conversation (and on the channel " + user + " writes from, if any), " +
		"and waits in the page of the node of this turn: place it in its document where it belongs, a line [its words](card:<the ref above>). " +
		"Say what you propose in one natural sentence, never as a list of pending items; " + user + " answers on the card, never you."
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

// cardAnswer is what a message of the user's decided, for the brain:
// state|ref|text; "" when it decided nothing.
func (s *Store) cardAnswer(seq int) string {
	var v string
	s.db.QueryRow(`SELECT card_answer FROM messages WHERE seq = ?`, seq).Scan(&v)
	return v
}

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
	if c.store.msgTaint(brain.Seq) != "" {
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
	x, err = c.decideCard(ctx, x.ID, yes, byText)
	if err != nil {
		return none, false
	}
	c.store.db.Exec(`UPDATE messages SET card_answer = ? WHERE seq = ?`, x.State+"|"+x.Ref+"|"+truncate(p.Text("fr"), 200), seq)
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
	raw := c.store.cardAnswer(seq)
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
	outcome := ""
	if s := c.store.get(cardOutcomeKey(parts[1])); s != "" {
		outcome = " Ce qui en est sorti : " + s // card_outcome.go
	}
	return fmt.Sprintf("\n(eriphe-core : cette réponse a %s la proposition %s, « %s ». C'est déjà fait : ne la refais pas, confirme en quelques mots.%s)",
		verb, parts[1], packQuote(parts[2]), outcome)
}

// Withdrawn with their subject.

// cardByArchive is who decided a card withdrawn with its node.
const cardByArchive = "archive"

// withdrawCards withdraws the cards that wait on nodes the user just
// archived (ids): archiving is setting the subject aside, so what it asked
// goes with it. A card is theirs when linked to one of them (node_cards)
// or about one (node:<id>); one a live node still holds stays. Each card
// is gone, decided by the archive, and its proposal no longer waits
// (withdrawProposal), without decideCard's words to the brain or a task. So no card comes
// back, not even when the node is reopened; the brain may propose again,
// and no longer reads it among what waits (pendingProposals). Called after
// the archive: the cards stay linked to their nodes, gone.
func (c *core) withdrawCards(ctx context.Context, ids []string) {
	refs := map[string]bool{}
	for _, id := range ids {
		for _, ref := range c.nodeCardRefs(id) {
			refs[ref] = true
		}
		for _, ref := range c.store.column(`SELECT ref FROM convo_cards WHERE about = ? AND state = 'pending'`, nodeRef(id)) {
			refs[ref] = true
		}
	}
	if len(refs) == 0 {
		return
	}
	cardMu.Lock()
	defer cardMu.Unlock()
	moved := false
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		if !c.cardHeld(ref) && c.withdrawRef(ctx, ref, cardByArchive) {
			moved = true
		}
	}
	if moved {
		c.bump()
	}
}

// cardByCode is who decided a card code withdrew: what it asked waits on
// nothing any more.
const cardByCode = "code"

// withdrawCard withdraws one card: its proposal waits no more, undecided
// by the user (withdrawRef).
func (c *core) withdrawCard(ctx context.Context, ref, by string) {
	cardMu.Lock()
	moved := c.withdrawRef(ctx, ref, by)
	cardMu.Unlock()
	if moved {
		c.bump()
	}
}

// withdrawRef makes a card gone, decided by by, and its proposal wait no
// more (withdrawProposal). It tells whether anything moved. cardMu held.
func (c *core) withdrawRef(ctx context.Context, ref, by string) bool {
	kind, pid, ok := parseProposalRef(ref)
	if !ok {
		return false
	}
	xs, _ := c.cardsWhere(`ref = ? AND state = ?`, ref, cardPending)
	for _, x := range xs {
		c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ?, by = ? WHERE id = ? AND state = ?`,
			cardGone, time.Now().Unix(), by, x.ID, cardPending)
		c.attentionDone(cardItemRef(x.ID))
	}
	pending := c.proposalOf(kind, pid).Pending
	if pending {
		if err := c.withdrawProposal(ctx, kind, pid); err != nil && !errors.Is(err, errCardDecided) {
			log.Printf("cards: %s: withdrawn (%s): %v", ref, by, err)
		}
	}
	if len(xs) == 0 && !pending {
		return false
	}
	c.proposalMoved(ref)
	log.Printf("cards: %s withdrawn (%s)", ref, by)
	return true
}

// withdrawProposal makes a proposal wait no more, undecided by the user:
// a view is dropped, an intention cancelled, the calls and access a card asks
// for withdrawn (withdrawAsk); another kind is answered no through its own
// approval, which only records it.
func (c *core) withdrawProposal(ctx context.Context, kind string, id int64) error {
	if k, ok := kindsOf[kind]; ok && k.withdraw != nil {
		return k.withdraw(c, ctx, id)
	}
	return c.decideProposal(ctx, kind, id, false)
}

// withdrawAsk takes back a call or an access a card asks for, its node
// archived: it waits no more, nothing runs, and nobody is told the user
// refused (no word to the brain, no trace on the archived node). A task
// that waits on the access is answered no, as on a no.
func (c *core) withdrawAsk(ctx context.Context, a accessQuestion) {
	res, err := c.store.db.Exec(`UPDATE access_asks SET state = ?, decided = ? WHERE id = ? AND state = ?`,
		startRejected, time.Now().Unix(), a.ID, startProposed)
	if n, _ := rowsOf(res, err); n != 1 {
		return
	}
	log.Printf("access ask %d: withdrawn", a.ID)
	if a.Call == "" {
		c.settleWaiting(ctx, a, false) // the task's calls that waited on it (an access, a release): refused
	}
}
