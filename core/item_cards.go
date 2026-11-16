package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// Cards bound to a thread's items (#575): an action on one item a source
// of the thread lists (archive this mail, close that alert) is a card on
// that item's row, its Oui running exactly the call it keeps, on that
// item; an action on several items at once is one card that keeps their N
// exact calls.
//
//   - A kept call is an access_asks row; with an item, it carries the
//     thread, the source and the item's id (code's, never given: matched
//     in its input, or filled in by code), and the card it is on, kind
//     calls, by its first call's id (batch).
//   - A triggered skill's run never holds on a call (skill_cards.go): a
//     call it keeps whose input names exactly one item its thread's
//     sources list, of the call's own server, is that item's. When the run
//     ends, each item's calls are one card, for that item; its other calls
//     are the run's card, as before. The same call kept again on the same
//     item, by another run, is the card already there.
//   - While it waits, the card is the user's: shown once, with what waits
//     for them on the thread's surface (« À toi », threadYours); an ended
//     thread's goes back to the attention queue. Decided, it stays a day
//     in its item's data, on its row (itemsData). Oui runs each call as kept, with
//     the policy read again (catalogCall); Non runs none. A run's results
//     go to its note, the others' to the thread's log: the brain is not
//     told.
//   - Its words are code's: the action by its tool (callVerb), the item by
//     its source and its id, its parameters as kept. A call kept after
//     reading external content keeps that taint: « extérieur », buttons
//     only (resolveByText).

const (
	cardCalls     = "calls"
	itemCardsKeep = 24 * time.Hour // a decided card stays on its row, folded, that long
)

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
	items := asksItems(asks)
	p.text = func(lang string) string {
		verb := callVerb(first.Call, lang)
		if len(items) > 1 {
			return fmt.Sprintf(tr(lang, "card.calls.items"), verb, len(items))
		}
		return verb
	}
	p.fields = func(lang string) []cardField {
		var fs []cardField
		for _, a := range asks {
			var lines []string
			for _, f := range a.fields() {
				lines = append(lines, f.Name+" : "+f.Value)
			}
			fs = append(fs, cardField{Name: callVerb(a.Call, lang), Value: truncate(strings.Join(lines, "\n"), cardValueMax)})
		}
		return fs
	}
	p.Fields = p.fields("fr")
	p.origin = func(lang string) cardOrigin { return c.callsOrigin(first, items, len(asks), lang) }
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

// callsOrigin is a calls card's origin: its thread, its item (by its
// source and its id) or how many, and what Oui does.
func (c *core) callsOrigin(first accessQuestion, items []string, calls int, lang string) cardOrigin {
	o := originParts{thread: first.Thread}
	if o.thread > 0 {
		o.title = c.cleanThreadTitle(o.thread)
	}
	from := o.from(lang)
	// The item by its id and its source only when neither may be someone
	// else's words: a source of a trusted server, calls kept clean; else
	// code's words alone.
	named := first.Tainted == "" && c.trustedItemSource(first.Thread, first.Source)
	var item string
	switch {
	case len(items) > 1 && named:
		item = fmt.Sprintf(tr(lang, "card.from.items"), len(items), truncate(first.Source, 60))
	case len(items) > 1:
		item = fmt.Sprintf(tr(lang, "card.from.itemsn"), len(items))
	case named:
		item = fmt.Sprintf(tr(lang, "card.from.item"), truncate(oneLine(first.Item), 80), truncate(first.Source, 60))
	default:
		item = tr(lang, "card.from.itemn")
	}
	if from != "" {
		from += " · "
	}
	yes := tr(lang, "card.yes.call")
	if calls > 1 {
		yes = fmt.Sprintf(tr(lang, "card.yes.calls"), calls)
	}
	return cardOrigin{Thread: first.Thread, From: from + item, Yes: yes}
}

// trustedItemSource tells whether a thread's source is read from a server
// the user trusts: its items' ids are not someone else's words.
func (c *core) trustedItemSource(thread int64, name string) bool {
	for _, src := range c.threadSources(thread) {
		if src.Name == name {
			return c.trustsServer(src.surface().spec().Server) // its name is a slug (threadSource.check)
		}
	}
	return false
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

// itemCallDone keeps how a call on an item went in its thread's log, once
// decided: not a run's (its note keeps it), and the brain is not told.
func (c *core) itemCallDone(a accessQuestion, text string) {
	c.logThread(a.Thread, "user", truncate(text, 400))
	log.Printf("item card %d: call %d: %s", a.Batch, a.ID, truncate(text, 120))
}

// callItem is the item a call acts on: the one, among those the thread's
// sources of the call's own server list, whose id is exactly a value of
// its input; ok false when none or more than one is.
func (c *core) callItem(thread int64, tool string, input map[string]any) (source, item string, ok bool) {
	server, _, okName := splitToolName(tool)
	if thread == 0 || !okName {
		return "", "", false
	}
	values := map[string]bool{}
	inputValues(input, values)
	n := 0
	for _, src := range c.threadSources(thread) {
		if src.Items == nil || src.surface().spec().Server != server {
			continue
		}
		for _, it := range threadSourceItems(src, 0) {
			if id := it["id"].(string); values[id] {
				source, item = src.Name, id
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
func (c *core) itemAskWaiting(thread int64, source, item, tool, input string) int64 {
	var id int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE thread = ? AND source = ? AND item = ? AND call LIKE ? AND input = ? AND state = ? ORDER BY id LIMIT 1`,
		thread, source, item, tool+" %", input, startProposed).Scan(&id)
	return id
}

// runItemBatch is the card a run's call on an item goes on: the one its
// other calls on that item are on, else its own.
func (c *core) runItemBatch(run, thread int64, source, item string) int64 {
	var batch int64
	c.store.db.QueryRow(`SELECT batch FROM access_asks WHERE run = ? AND thread = ? AND source = ? AND item = ? AND batch != 0 ORDER BY id LIMIT 1`,
		run, thread, source, item).Scan(&batch)
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

// itemCard is a calls card decided on a thread's items: on its item's row
// when it acts on one, else under its source (itemsData).
type itemCard struct {
	Ref    string
	Source string
	Item   string // "" for a card on several items
}

// threadItemCards are a thread's calls cards decided within itemCardsKeep
// (folded on their row), oldest first. One that waits is the user's, with
// what waits for them (threadYours), never on a row.
func (c *core) threadItemCards(thread int64, now time.Time) []itemCard {
	rows, err := c.store.db.Query(`SELECT batch, MIN(source), MIN(item), MAX(item), MAX(state = ?), MAX(decided) FROM access_asks
		WHERE thread = ? AND batch != 0 GROUP BY batch ORDER BY batch`, startProposed, thread)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []itemCard
	for rows.Next() {
		var batch, decided int64
		var source, lo, hi string
		var waits bool
		if rows.Scan(&batch, &source, &lo, &hi, &waits, &decided) != nil || waits || now.Sub(time.Unix(decided, 0)) > itemCardsKeep {
			continue // it waits still, or was decided long ago
		}
		x := itemCard{Ref: proposalRef(cardCalls, batch), Source: source}
		if lo == hi {
			x.Item = lo
		}
		out = append(out, x)
	}
	return out
}
