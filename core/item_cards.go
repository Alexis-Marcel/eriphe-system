package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"slices"
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
	var fs []cardField
	for _, a := range asks {
		fs = append(fs, callDetail(a))
	}
	p.Fields = fs
	p.origin = func(lang string) cardOrigin { return c.callsOrigin(first, items, len(asks), lang) }
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
		if len(items) > 1 {
			return fmt.Sprintf(tr(lang, "card.calls.items"), text, len(items))
		}
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
	p.summary = func(lang string) []string {
		if f := callFolder(asks[0].Call, asks[0].Input, lang); f != "" {
			return []string{f}
		}
		return nil
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
func (c *core) callsOrigin(first accessQuestion, items []string, calls int, lang string) cardOrigin {
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
	yes := tr(lang, "card.yes.call")
	if calls > 1 {
		yes = fmt.Sprintf(tr(lang, "card.yes.calls"), calls)
	}
	return cardOrigin{Node: first.Node, From: from + item, Yes: yes, External: ext}
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
