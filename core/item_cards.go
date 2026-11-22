package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"
)

// Cards bound to a node's items (#575, #631): an action on one item a
// source of a node lists (archive this mail, answer that invitation) is a
// card on that item, its Oui running exactly the call it keeps, on that
// item; several calls on one item are one card that keeps them all.
//
//   - A kept call is an access_asks row; it carries the node it belongs
//     to, and on an item the source and the item's id (code's, never
//     given: matched in its input).
//   - A triggered skill's run never holds on a call (skill_cards.go): a
//     call it keeps whose input names exactly one item its node's sources
//     list, of the call's own server, is that item's: the item's card,
//     kind calls, by its first call's id (batch). Its other calls are its
//     node's, on the run's card. The same call kept again on the same
//     item, by another run, is the card already there.
//   - Oui runs each call as kept, with the policy read again
//     (catalogCall); Non runs none. A run's results go to its note, the
//     others' to the node's trace: the brain is not told.
//   - Its words are code's: the action in a sentence, the items by their
//     title and detail as their source lists them (call_words.go), its
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
		details = append(details, callDetail(a, names))
	}
	p.Fields = append(shown, details...)
	p.origin = func(lang string) cardOrigin { return c.callsOrigin(first, items, lang) }
	if len(asks) > 1 {
		return c.chosenCalls(p, asks)
	}
	// One call: no box to untick, but its id all the same, so that Oui
	// names it, and never a call folded in after the card was drawn
	// (call_fold.go).
	p.choices = func(lang string) []proposalChoice {
		a := asks[0]
		return []proposalChoice{{ID: a.ID, Text: c.choiceText(a, lang), N: itemCount(a), State: a.State}}
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
			out = append(out, proposalChoice{ID: a.ID, Text: text, N: itemCount(a), State: a.State})
		}
		return out
	}
	if !same {
		p.text = func(lang string) string {
			text, _ := c.callsWords(asks, lang)
			return text
		}
		return p
	}
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

// asksItems are the distinct items the calls act on, in order.
func asksItems(asks []accessQuestion) []string {
	var out []string
	for _, a := range asks {
		if a.Item != "" && !slices.Contains(out, a.Item) {
			out = append(out, a.Item)
		}
	}
	return out
}

// callsOrigin is a calls card's origin: its node, its item (by its
// source and its id) or how many, and what Oui does.
func (c *core) callsOrigin(first accessQuestion, items []string, lang string) cardOrigin {
	o := originParts{node: first.Node}
	if o.node != "" {
		o.title, o.titleExt = c.titleOfNode(o.node)
	}
	from, ext := o.from(lang)
	// The item by its id and its source, always: marked when either may
	// be someone else's words (not a source of a trusted server, or calls
	// kept on a tainted turn).
	itemExt := first.Tainted != "" || !c.trustedItemSource(first.Node, first.Source)
	var item string
	switch {
	case len(items) > 1 && first.Source != "":
		item = fmt.Sprintf(tr(lang, "card.from.items"), len(items), markedWords(first.Source, itemExt, lang))
	case len(items) > 1:
		item = fmt.Sprintf(tr(lang, "card.from.itemsn"), len(items))
	case first.Item != "" && first.Source != "":
		item = fmt.Sprintf(tr(lang, "card.from.item"), truncate(oneLine(first.Item), 80), markedWords(first.Source, itemExt, lang))
	default:
		item = tr(lang, "card.from.itemn")
	}
	ext = ext || itemExt && first.Source != ""
	if from != "" {
		from += " · "
	}
	// What Oui does is the card's own sentence: its exact calls are under
	// its Détails.
	return cardOrigin{Node: first.Node, From: from + item, External: ext}
}

// trustedItemSource tells whether a node's source is read from a server
// the user trusts: its items' ids are not someone else's words.
func (c *core) trustedItemSource(node, name string) bool {
	r, ok := c.nodeValue(node, name)
	return ok && c.trustsServer(c.sourceServer(r.Source)) // its name is a slug (nodefile.SourceRe)
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

// callsExec is what a calls card's approved calls are doing, as its
// Proposal shows it: running while one runs, failed if one failed, done
// once all are.
func (c *core) callsExec(batch int64) (string, string) {
	state, why, all := "", "", true
	for _, a := range c.batchAsks(batch) {
		if a.State != startApproved {
			continue
		}
		s, w := c.callState(a.ID)
		switch {
		case s == callRunning:
			return callRunning, ""
		case s == callFailed && state != callFailed:
			state, why = callFailed, w
		case s != callDone:
			all = false
		}
	}
	if state == "" && all {
		state = callDone
	}
	return state, why
}

// callsAsk tells whether a calls card still asks something of the user:
// one of its calls waits for a yes, or one it said yes to is not done
// (about to run, running, or failed).
func (c *core) callsAsk(batch int64) bool {
	for _, a := range c.batchAsks(batch) {
		if c.askWaits(a) {
			return true
		}
		if s, _ := c.callState(a.ID); a.State == startApproved && s != callDone {
			return true
		}
	}
	return false
}

// itemCardsKept is how long after its yes a card on an item may still be
// running or failed on its row: an older one asks nothing more there.
const itemCardsKept = 7 * 24 * time.Hour

// itemCards are the cards on a node's items of one of its sources that
// still ask something (callsAsk), by item: its newest. Each item of the
// source carries it (_card, list_sections.go) and the page's List tells
// it (pageList's items' card): code's, never the block's data.
func (c *core) itemCards(node, source string) map[string]string {
	out := map[string]string{}
	if node == "" || source == "" {
		return out
	}
	rows, err := c.store.db.Query(`SELECT item, batch FROM access_asks WHERE node = ? AND source = ? AND item != '' AND batch != 0
		AND (state = ? OR state = ? AND decided >= ?) ORDER BY batch DESC`, node, source, startProposed, startApproved, time.Now().Add(-itemCardsKept).Unix())
	if err != nil {
		return out
	}
	type pair struct {
		item  string
		batch int64
	}
	var ps []pair
	for rows.Next() {
		var p pair
		if rows.Scan(&p.item, &p.batch) == nil {
			ps = append(ps, p)
		}
	}
	rows.Close() // one connection: each is read after
	seen := map[int64]bool{}
	for _, p := range ps {
		if _, ok := out[p.item]; ok || seen[p.batch] {
			continue
		}
		seen[p.batch] = true
		if c.callsAsk(p.batch) {
			out[p.item] = proposalRef(cardCalls, p.batch)
		}
	}
	// The brain's calls on items of the node (askBrainAccess): one card
	// on each of the rows it names, shared; the newest on a row it shares
	// with another card, what the user just asked.
	for _, a := range c.asksWhere(`node = ? AND batch = 0 AND run = 0 AND labels != '' AND (state = ? OR state = ? AND decided >= ?) ORDER BY id`,
		node, startProposed, startApproved, time.Now().Add(-itemCardsKept).Unix()) {
		if !c.accessAsks(a) {
			continue
		}
		for _, l := range a.Labels {
			if l.Source == source {
				out[l.ID] = proposalRef(cardAccess, a.ID)
			}
		}
	}
	return out
}

// accessAsks tells whether an access card still asks something of the
// user: it waits for a yes, or its yes's call is not done (about to run,
// running, or failed).
func (c *core) accessAsks(a accessQuestion) bool {
	if a.State == startProposed {
		return true
	}
	s, _ := c.callState(a.ID)
	return a.State == startApproved && s != callDone
}

// accessCardMoved is the brain's call on items of a node that was made,
// decided, or whose yes ran: the page is told its items' sources again
// (their _card). Once its call ran, catalogCall's sourcesChanged
// (source_fresh.go) shows its ids done at once and reads the sources again.
func (c *core) accessCardMoved(a accessQuestion) {
	if a.Node == "" || len(a.Labels) == 0 || c.nodes == nil {
		return
	}
	seen := map[string]bool{}
	for _, l := range a.Labels {
		if !seen[l.Source] {
			seen[l.Source] = true
			c.graphSaid(graphEvent{Kind: graphEvSource, Node: a.Node, Name: l.Source})
		}
	}
}

// itemCardMoved is a calls card on an item that was made, decided, or
// whose yes ran: the page is told its item's source again (its _card),
// and once it asks nothing more, the item is settled, once. Its yes ran:
// code notes the item done, its note and group kept (code's state, until
// a skill notes it again), so that a List that keeps what is not done
// lets it go. Either way, the view's skill is woken (wakeNodeSkills): it
// writes its bilan again.
func (c *core) itemCardMoved(batch int64) {
	asks := c.batchAsks(batch)
	i := slices.IndexFunc(asks, func(a accessQuestion) bool { return a.Item != "" && a.Node != "" })
	if i < 0 || c.nodes == nil {
		return
	}
	a := asks[i]
	c.graphSaid(graphEvent{Kind: graphEvSource, Node: a.Node, Name: a.Source})
	if c.callsAsk(batch) {
		return
	}
	for _, x := range asks {
		if x.State == startProposed {
			return // not decided: one folded away, or a run still open
		}
	}
	key := fmt.Sprintf("item_card_settled:%d", batch)
	itemSettleMu.Lock()
	settled := c.store.get(key) != ""
	if !settled {
		c.store.set(key, "1")
	}
	itemSettleMu.Unlock()
	if settled {
		return // its decision and its call's end may both come here
	}
	if slices.ContainsFunc(asks, func(x accessQuestion) bool { return x.State == startApproved }) {
		n := c.itemNotes(a.Node, a.Source)[a.Item]
		if n.State != itemDone {
			n.Source, n.Item, n.State, n.Author = a.Source, a.Item, itemDone, nodeByCode
			if err := c.noteItem(a.Node, n); err != nil {
				log.Printf("item card %d: %s: %v", batch, a.Node, err)
			}
		}
	}
	c.wakeNodeSkills(a.Node, "card")
}

// itemDone is the state of an item whose matter is over (item_note's done).
const itemDone = "done"

// itemSettleMu makes an item's card settle once.
var itemSettleMu sync.Mutex

// itemCallDone keeps how a call on an item went in its node's trace,
// once decided: not a run's (its note keeps it), and the brain is not
// told.
func (c *core) itemCallDone(a accessQuestion, text string) {
	if c.nodes != nil && a.Node != "" {
		if err := c.nodes.AppendTrace(a.Node, "user", truncate(text, 400)); err != nil {
			log.Printf("item card %d: node %s: %v", a.Batch, a.Node, err)
		}
	}
	log.Printf("item card %d: call %d: %s", a.Batch, a.ID, truncate(text, 120))
}

// callItem is the item a call acts on: the one, among those the node's
// sources of the call's own server list, whose id is exactly a value of
// its input; ok false when none or more than one is.
func (c *core) callItem(node, tool string, input map[string]any) (source, item string, ok bool) {
	server, _, okName := splitToolName(tool)
	if node == "" || c.nodes == nil || !okName {
		return "", "", false
	}
	values := map[string]bool{}
	inputValues(input, values)
	n := 0
	for _, r := range c.nodeReads(node) {
		if r.Source.Items == nil || r.Source.Monitor != "" || c.sourceServer(r.Source) != server {
			continue
		}
		for _, it := range sourceItems(r.Name, r.Source, r.Value, 0) {
			if id := it["id"].(string); values[id] {
				source, item = r.Name, id
				n++
			}
		}
	}
	if n != 1 {
		return "", "", false
	}
	return source, item, true
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

// itemAskWaiting is the id of the call already kept on that item with
// that exact input, still waiting, 0 when none.
func (c *core) itemAskWaiting(node, source, item, tool, input string) int64 {
	var id int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE node = ? AND source = ? AND item = ? AND call LIKE ? AND input = ? AND state = ? ORDER BY id LIMIT 1`,
		node, source, item, tool+" %", input, startProposed).Scan(&id)
	return id
}

// runItemBatch is the card a run's call on an item goes on: the one its
// other calls on that item are on, else its own.
func (c *core) runItemBatch(run int64, node, source, item string) int64 {
	var batch int64
	c.store.db.QueryRow(`SELECT batch FROM access_asks WHERE run = ? AND node = ? AND source = ? AND item = ? AND batch != 0 ORDER BY id LIMIT 1`,
		run, node, source, item).Scan(&batch)
	return batch
}

// runBatches are the cards of a run's calls on items that wait.
func (c *core) runBatches(run int64) []int64 {
	rows, err := c.store.db.Query(`SELECT DISTINCT batch FROM access_asks WHERE run = ? AND batch != 0 AND state = ? ORDER BY batch`, run, startProposed)
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
