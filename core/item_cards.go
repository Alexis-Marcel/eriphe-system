package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"slices"
	"time"
)

// Cards bound to a node's items (#575, #631, #782): an action on items a
// node's page references (page_items.go), or a source of a node lists
// (archive these mails, answer that invitation), is a card on their rows,
// its Oui running exactly the calls it keeps.
//
//   - A kept call is an access_asks row, on a card (its batch): the brain's
//     and the runs' (keepCall, demand.go), code's own (a tools review). It
//     carries the node it belongs to and the items it acts on (its labels,
//     each with the server whose item the page references, or the source
//     that lists it; item when it is one): code's,
//     never given: matched in its input.
//   - A run's calls on one item are one card. Calls that do the same thing
//     to other items of the node are one card (call_fold.go).
//   - Oui runs each call as kept, with the policy read again
//     (catalogCall); Non runs none. A run's results go to its note, the
//     brain's to the brain, code's to the node's trace; once all ran, the
//     brain is told how the yes went, the orb's words (card_outcome.go).
//   - Its words are code's: the action in a sentence, the items by their
//     title and detail as their server read them, or their source lists
//     them (call_words.go), its
//     exact calls, as kept, under Détails. It waits in its node
//     (cardNodeOf), about it. A call kept after
//     reading external content keeps that taint: « extérieur », buttons
//     only (resolveByText).

const cardCalls = "calls"

// batchAsks are the calls a calls card keeps, in order.
func (c *core) batchAsks(batch int64) []accessQuestion {
	return c.asksWhere(`batch = ? ORDER BY id`, batch)
}

// asksWhere are the access_asks rows a condition selects.
func (c *core) asksWhere(where string, args ...any) []accessQuestion {
	rows, err := c.store.db.Query(`SELECT id FROM access_asks WHERE `+where, args...)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close() // one connection: each is read after
	var out []accessQuestion
	for _, id := range ids {
		if a, err := c.accessAskOf(id); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// askWaits tells whether a kept call still waits for the user: proposed,
// and a run's only once the run is over (its card comes then).
func (c *core) askWaits(a accessQuestion) bool {
	if a.State != startProposed {
		return false
	}
	if a.Run != 0 {
		r := c.runByID(a.Run)
		return r != nil && r.State != skillRunOpen
	}
	return true
}

// callsProposal reads a calls card as a proposal.
func (c *core) callsProposal(p proposal) proposal {
	asks := c.batchAsks(p.ID)
	if len(asks) == 0 {
		return p
	}
	approved := false
	for _, a := range asks {
		if c.askWaits(a) {
			p.Pending = true
		}
		if a.State == startApproved {
			approved = true
		}
		if p.Tainted == "" {
			p.Tainted = a.Tainted
		}
	}
	p.Outcome = cardRejected
	if approved {
		p.Outcome = cardApproved
	}
	first := asks[0]
	if first.Run != 0 {
		if r := c.runByID(first.Run); r != nil {
			p.Why = "Skill « " + r.Skill + " »"
		}
	}
	// While it waits, the calls that wait: one decided before them (its
	// card came again) is on its old card.
	if p.Pending {
		var waiting []accessQuestion
		for _, a := range asks {
			if c.askWaits(a) {
				waiting = append(waiting, a)
			}
		}
		asks = waiting
	}
	items := asksItems(asks)
	// What each call sends that is to read (a reply, a message) shows on
	// the card, named as its tool names it; its exact call, the rest,
	// under Détails. A name met twice says which call it is of.
	var shown, details []cardField
	for i, a := range asks {
		fs, names := c.callShown(a, c.userLang())
		for _, f := range append(callRead(a), fs...) {
			if slices.ContainsFunc(shown, func(g cardField) bool { return g.Name == f.Name }) {
				f.Name = fmt.Sprintf("%s (%d)", f.Name, i+1)
			}
			shown = append(shown, f)
		}
		if len(asks) == 1 {
			details = c.callDetails(a, names, c.userLang()) // its parameters by meaning, then its exact call
		} else {
			details = append(details, callDetail(a, names))
		}
	}
	p.Fields = append(shown, details...)
	p.origin = func(lang string) cardOrigin { return c.callsOrigin(first, items, lang) }
	// The account or folder they act in, quietly beside the words, when
	// it is one; on items, its server alone says nothing their rows do not.
	p.context = func(lang string) string {
		out := ""
		for i, a := range asks {
			w := c.callAccountWords(a, lang)
			if len(items) > 0 && w == c.serverWords(a) || i > 0 && w != out {
				return ""
			}
			out = w
		}
		return out
	}
	p.verb = func(lang string) string { return c.callVerb(first, itemCount(first), lang) }
	if len(asks) > 1 {
		return c.chosenCalls(p, asks)
	}
	// One call: no box to untick, but its id all the same, so that Oui
	// names it, and never a call folded in after the card was drawn
	// (call_fold.go).
	p.choices = func(lang string) []proposalChoice {
		a := asks[0]
		return []proposalChoice{{ID: a.ID, Text: c.choiceText(a, lang), N: itemCount(a), State: a.State, Items: choiceItems(a)}}
	}
	// Its words read plainly (call_words.go); the exact calls under
	// Détails.
	p.text = func(lang string) string {
		text, _ := c.callsWords(asks, lang)
		return text
	}
	p.summary = func(lang string) []string {
		_, lines := c.callsWords(asks, lang)
		return lines
	}
	return p
}

// chosenCalls are the words of a card of several calls, each a line the
// user may leave out (call_fold.go): for calls that do the same thing to
// other items, the action once, for how many, and each call's items; for
// others, each call's sentence.
func (c *core) chosenCalls(p proposal, asks []accessQuestion) proposal {
	same := batchKind(asks) != ""
	total := 0
	for _, a := range asks {
		total += itemCount(a)
	}
	p.choices = func(lang string) []proposalChoice {
		_, lines := c.callsWords(asks, lang)
		var out []proposalChoice
		for i, a := range asks {
			text := lines[i]
			if same {
				text = c.choiceText(a, lang)
			}
			out = append(out, proposalChoice{ID: a.ID, Text: text, N: itemCount(a), State: a.State, Items: choiceItems(a)})
		}
		return out
	}
	if !same {
		p.text = func(lang string) string {
			text, _ := c.callsWords(asks, lang)
			return text
		}
		p.verb = func(lang string) string { return fmt.Sprintf(tr(lang, "card.verb.all"), len(asks)) }
		return p
	}
	p.verb = func(lang string) string { return c.callVerb(asks[0], total, lang) }
	p.text = func(lang string) string {
		if p.Pending {
			return c.kindSentence(asks[0], total, lang)
		}
		return c.callsDecidedText(asks, lang, true)
	}
	p.acts = func(lang string) []string {
		out := make([]string, total)
		for k := range out {
			out[k] = c.kindSentence(asks[0], k+1, lang)
		}
		return out
	}
	return p
}

// asksItems are the distinct items the calls act on, in order, each
// with the server that holds it.
func asksItems(asks []accessQuestion) []itemLabel {
	var out []itemLabel
	for _, a := range asks {
		for _, l := range askItems(a) {
			if !slices.ContainsFunc(out, func(x itemLabel) bool { return x.ID == l.ID }) {
				out = append(out, l)
			}
		}
	}
	return out
}

// callsOrigin is a calls card's origin: its node, its item (by its
// server and its id) or how many, and what Oui does.
func (c *core) callsOrigin(first accessQuestion, items []itemLabel, lang string) cardOrigin {
	o := originParts{node: first.Node}
	if o.node != "" {
		o.title, o.titleExt = c.titleOfNode(o.node)
	}
	from, ext := o.from(lang)
	// The item by its id and where it is from (the server a page's
	// reference names), always: marked when either may be someone else's
	// words (not a trusted server's, or calls kept on a tainted turn).
	source := ""
	for i, l := range items {
		if i == 0 {
			source = l.Server
		} else if l.Server != source {
			source = ""
		}
	}
	itemExt := first.Tainted != "" || source != "" && !c.trustsServer(source)
	var item string
	switch {
	case len(items) > 1 && source != "":
		item = fmt.Sprintf(tr(lang, "card.from.items"), len(items), markedWords(source, itemExt, lang))
	case len(items) > 1:
		item = fmt.Sprintf(tr(lang, "card.from.itemsn"), len(items))
	case len(items) == 1 && source != "":
		// By its title, never its id: « Picnic » de invitations.
		name := tr(lang, "card.from.itemnamed")
		for _, l := range c.labelsOf(first) {
			if l.ID == items[0].ID && l.Title != "" {
				name = fmt.Sprintf(tr(lang, "card.call.itemt"), truncate(oneLine(l.Title), 80))
			}
		}
		item = fmt.Sprintf(tr(lang, "card.from.item"), name, markedWords(source, itemExt, lang))
	default:
		item = tr(lang, "card.from.itemn")
	}
	ext = ext || itemExt && source != ""
	if from != "" {
		from += " · "
	}
	// What Oui does is the card's own sentence: its exact calls are under
	// its Détails.
	return cardOrigin{Node: first.Node, From: from + item, External: ext}
}

// decideCalls answers every call of the card still waiting.
func (c *core) decideCalls(ctx context.Context, batch int64, approve bool) error {
	for _, a := range c.batchAsks(batch) {
		if !c.askWaits(a) {
			continue
		}
		if err := c.decideAccess(ctx, a.ID, approve); err != nil && err != errCardDecided {
			return err
		}
	}
	return nil
}

// callsAsk tells whether a calls card still asks something of the user:
// one of its calls waits for a yes, or one it said yes to is about to
// run or running. Done or failed, it is decided: it leaves its rows
// (card_outcome.go).
func (c *core) callsAsk(batch int64) bool {
	for _, a := range c.batchAsks(batch) {
		if c.askWaits(a) {
			return true
		}
		if a.State == startApproved && !callEnded(c.callState(a.ID)) {
			return true
		}
	}
	return false
}

// callEnded tells a call's state (callState) that is over: done or failed.
func callEnded(state, _ string) bool { return state == callDone || state == callFailed }

// itemCardsKept is how long after its yes a card on an item may still be
// about to run or running on its row: an older one asks nothing more
// there.
const itemCardsKept = 7 * 24 * time.Hour

// itemCardMoved is a calls card on items that was decided, or whose yes
// ran: once it asks nothing more, its items are settled, once, and the
// node's skill is woken (wakeNodeSkills): it writes its page again.
func (c *core) itemCardMoved(batch int64) {
	asks := c.batchAsks(batch)
	items := asksItems(asks)
	if len(items) == 0 || c.nodes == nil || asks[0].Node == "" {
		return
	}
	node := asks[0].Node
	c.cardsMovedOn(node)
	if c.callsAsk(batch) {
		return
	}
	for _, x := range asks {
		if x.State == startProposed {
			return // not decided: one folded away, or a run still open
		}
	}
	// Once: its decision and its call's end may both come here. The mark
	// is its first call's (the batch's own row), gone with it.
	res, err := c.store.db.Exec(`UPDATE access_asks SET settled = 1 WHERE id = ? AND settled = 0`, batch)
	if n, _ := rowsOf(res, err); n != 1 {
		return
	}
	// A page's references are its skill's to write again: code writes no
	// line of what a yes did (page_items.go).
	c.wakeNodeSkills(node, "card")
}

// itemCallDone keeps how a call on an item went in its node's trace,
// once decided: not a run's (its note keeps it); one that failed is
// traced with why already (traceFailedCall). server is the one whose
// words text carries ("" for none): one not trusted marks the node
// external first, as any reader of its trace (node_read) must know.
func (c *core) itemCallDone(a accessQuestion, text, server string) {
	if s, _ := c.callState(a.ID); s != callFailed && c.nodes != nil && a.Node != "" {
		if server != "" && !c.trustsServer(server) {
			c.markNodeExternal(a.Node, nodeByCode) // the tool's words: never without the mark
		}
		if err := c.nodes.AppendTrace(a.Node, "user", truncate(text, 400)); err != nil {
			log.Printf("item card %d: node %s: %v", a.Batch, a.Node, err)
		}
	}
	log.Printf("item card %d: call %d: %s", a.Batch, a.ID, truncate(text, 120))
}

// rowsID is the id of the row an insert made.
func rowsID(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// inputValues collects the scalar values of an input, as text.
func inputValues(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			inputValues(e, out)
		}
	case []any:
		for _, e := range x {
			inputValues(e, out)
		}
	case nil:
	default:
		if s := valueText(x); s != "" {
			out[s] = true
		}
	}
}

// runItemBatch is the card a run's call on an item goes on: the one its
// other calls on that item are on, else its own. An item is its server's:
// another server's of the same id is another item.
func (c *core) runItemBatch(run int64, node, server, item string) int64 {
	var batch int64
	c.store.db.QueryRow(`SELECT batch FROM access_asks WHERE run = ? AND node = ? AND item = ? AND server = ? AND batch != 0 ORDER BY id LIMIT 1`,
		run, node, item, server).Scan(&batch)
	return batch
}

// proposedBatches are the calls cards that may wait.
func (c *core) proposedBatches() []int64 {
	rows, err := c.store.db.Query(`SELECT DISTINCT batch FROM access_asks WHERE batch != 0 AND state = ? ORDER BY batch`, startProposed)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var b int64
		if rows.Scan(&b) == nil {
			out = append(out, b)
		}
	}
	return out
}
