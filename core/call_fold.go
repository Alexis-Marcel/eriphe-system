package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
)

// Same-kind proposals wait as one card, and the user unticks what stays.
//
// A skill woken once per item (a monitor's new mail) proposes the same
// action on each: a calls card each (item_cards.go), a pile of identical
// cards. Calls cards that wait in the same node, untouched, and whose
// calls do the same thing to other items fold into one: the oldest.
//
//   - The same thing: the same server and tool, the same arguments but
//     one, a list whose values are ids of items its node's sources list
//     (kept with the call: its labels). Nothing of a tool's own: the
//     source's items descriptor says what an id is. A different account
//     or destination is another argument: another card.
//   - The card keeps each call as it was kept, its own row: Oui runs each
//     exactly as shown, each in the decisions, never a call made up of
//     their union. A card decided, or one of whose calls runs or waits
//     for its run to end, takes no more.
//   - Each call is a line with a box (on a batch, its item's row), all
//     ticked: the user unticks what should not go. Oui sends the ticked calls (only) and those unticked
//     (left): code checks both are calls of the card that wait, runs the
//     first and refuses the others. A call folded in after the page drew
//     the card is in neither: it waits on, its card comes again. Non
//     refuses every call.
//   - Folded when a run ends, before its cards are shown, and when the
//     cards are synced: cards made before this fold at the next sync.

// errNotOnCard is a choice naming a call the card does not keep waiting.
var errNotOnCard = errors.New("a chosen call is not one this card keeps waiting")

// foldKey is what a kept call does, but for the items it acts on: its
// tool and its other arguments, and the name of its list of items; ""
// when it has no such list, or more than one. Its items are those kept
// with it (its labels) and the item it is on (a call kept before labels
// were has only that).
func foldKey(a accessQuestion) (key, list string) {
	ids := map[string]bool{}
	for _, l := range a.Labels {
		ids[l.ID] = true
	}
	if a.Item != "" {
		ids[a.Item] = true
	}
	if len(ids) == 0 {
		return "", ""
	}
	name, _, _ := strings.Cut(a.Call, " ")
	var input map[string]any
	if json.Unmarshal([]byte(a.Input), &input) != nil {
		return "", ""
	}
	for k, v := range input {
		values, ok := v.([]any)
		if !ok || len(values) == 0 || !allItems(values, ids) {
			continue
		}
		if list != "" {
			return "", "" // two lists of items: which one says "other items" is not clear
		}
		list = k
	}
	if list == "" {
		return "", ""
	}
	rest := maps.Clone(input)
	delete(rest, list)
	raw, _ := json.Marshal(rest) // its keys sorted
	return name + " " + list + " " + string(raw), list
}

// allItems tells whether every value is one of the items' ids.
func allItems(values []any, ids map[string]bool) bool {
	for _, v := range values {
		s, ok := v.(string)
		if !ok || !ids[s] {
			return false
		}
	}
	return true
}

// itemCount is how many items a call acts on: its list's length, 1 when
// it has none.
func itemCount(a accessQuestion) int {
	_, list := foldKey(a)
	if list == "" {
		return 1
	}
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	values, _ := input[list].([]any)
	return max(1, len(values))
}

// batchKind is the one thing every call of a card does, "" when they do
// not all do the same.
func batchKind(asks []accessQuestion) string {
	kind := ""
	for i, a := range asks {
		k, _ := foldKey(a)
		if k == "" || (i > 0 && k != kind) {
			return ""
		}
		kind = k
	}
	return kind
}

// foldCalls folds the calls cards that wait untouched into the oldest of
// those of their node that do the same thing. It gives each folded card's
// batch the one it went to.
func (c *core) foldCalls() map[int64]int64 {
	cardMu.Lock() // not while a card is decided
	defer cardMu.Unlock()
	into := map[string]int64{} // node and kind: the oldest card
	folded := map[int64]int64{}
	for _, batch := range c.proposedBatches() {
		asks := c.batchAsks(batch)
		kind := batchKind(asks)
		if kind == "" || !c.untouched(asks) {
			continue
		}
		at := asks[0].Node + "\x00" + kind
		target, ok := into[at]
		if !ok {
			into[at] = batch
			continue
		}
		c.foldInto(batch, target)
		folded[batch] = target
	}
	return folded
}

// untouched tells whether every call of a card waits, none running.
func (c *core) untouched(asks []accessQuestion) bool {
	for _, a := range asks {
		if !c.askWaits(a) {
			return false
		}
		if s, _ := c.callState(a.ID); s != "" {
			return false
		}
	}
	return len(asks) > 0
}

// foldInto moves a card's calls to another: its own card goes, unlinked
// from its node and out of the attention queue.
func (c *core) foldInto(batch, target int64) {
	c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE batch = ?`, target, batch)
	ref := proposalRef(cardCalls, batch)
	if xs, err := c.cardsWhere(`ref = ?`, ref); err == nil {
		for _, x := range xs {
			c.attentionDone(cardItemRef(x.ID))
		}
	}
	linked := c.cardNodes(ref)
	c.store.db.Exec(`DELETE FROM convo_cards WHERE ref = ?`, ref)
	c.store.db.Exec(`DELETE FROM node_cards WHERE ref = ?`, ref)
	to := proposalRef(cardCalls, target)
	p := c.proposalOf(cardCalls, target)
	c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE ref = ? AND state = ?`, p.Text(c.userLang()), to, cardPending)
	c.proposalMoved(ref)
	c.proposalMoved(to)
	for _, id := range linked {
		c.nodeMoved(id, false)
	}
	log.Printf("cards: %s folded into %s", ref, to)
}

// proposalChoice is one call of a card the user may leave out: what it
// acts on, in words; how many items; where it stands.
type proposalChoice struct {
	ID    int64    `json:"id"`
	Text  string   `json:"text"`
	N     int      `json:"n"`
	State string   `json:"state"`           // proposed, approved, rejected
	Items []string `json:"items,omitempty"` // the items it acts on, by id: a batch's rows unticked leave it out
}

// choiceItems are the ids of the items a kept call acts on: those kept
// with it (its labels) and the item it is on, in that order, once each.
func choiceItems(a accessQuestion) []string {
	var out []string
	for _, l := range a.Labels {
		if l.ID != "" && !slices.Contains(out, l.ID) {
			out = append(out, l.ID)
		}
	}
	if a.Item != "" && !slices.Contains(out, a.Item) {
		out = append(out, a.Item)
	}
	return out
}

// choiceText is a call's line on a card of calls that do the same thing:
// the items it acts on; its sentence when they are not known.
func (c *core) choiceText(a accessQuestion, lang string) string {
	var items []string
	for _, l := range c.labelsOf(a) {
		if s := labelLine(l, lang); s != "" {
			items = append(items, s)
		}
	}
	if len(items) == 0 {
		return c.callText(a, lang)
	}
	return strings.Join(items, ", ")
}

// kindSentence says what calls that do the same thing do to n items, in
// one sentence (callPhrase): the action, for how many.
func (c *core) kindSentence(a accessQuestion, n int, lang string) string {
	return c.callTextN(a, n, lang)
}

// decideCallsChosen is Oui on a calls card with the user's choice: the
// calls ticked (only) run, those unticked (left) are refused; both must
// be calls of the card that wait, or nothing is decided. A call in
// neither, folded in after the card was drawn, waits on.
func (c *core) decideCallsChosen(ctx context.Context, batch int64, only, left []int64) error {
	waiting := map[int64]bool{}
	for _, a := range c.batchAsks(batch) {
		if c.askWaits(a) {
			waiting[a.ID] = true
		}
	}
	if len(only) == 0 {
		return errNotOnCard
	}
	for _, id := range slices.Concat(only, left) {
		if !waiting[id] {
			return errNotOnCard
		}
	}
	for _, id := range only {
		if slices.Contains(left, id) {
			return errNotOnCard
		}
	}
	for _, id := range left {
		if err := c.decideAccess(ctx, id, false); err != nil && err != errCardDecided {
			return err
		}
	}
	for _, id := range only {
		if err := c.decideAccess(ctx, id, true); err != nil && err != errCardDecided {
			return err
		}
	}
	return nil
}

// decideCardChosen is a card's Oui with the user's choice of its calls
// (decideCallsChosen): a calls card's only.
func (c *core) decideCardChosen(ctx context.Context, id int64, only, left []int64, by string) (convoCard, error) {
	return c.decideCardWith(ctx, id, true, by, func(kind string, pid int64) error {
		if kind != cardCalls {
			return errNotOnCard
		}
		if !c.proposalOf(kind, pid).Pending {
			return errCardDecided
		}
		return c.decideCallsChosen(ctx, pid, only, left)
	})
}

// callsDecided keeps a decided calls card's words, those of its calls
// decided: what it shows once set aside. A call folded in after the page
// drew it, still waiting, gets a new card at once (syncCard).
func (c *core) callsDecided(x convoCard) {
	kind, id, _ := parseProposalRef(x.Ref)
	if x.Kind != cardCalls || kind != cardCalls {
		return
	}
	var decided []accessQuestion
	for _, a := range c.batchAsks(id) {
		if a.State != startProposed {
			decided = append(decided, a)
		}
	}
	if len(decided) == 0 {
		return
	}
	lang := c.userLang()
	text, _ := c.callsWords(decided, lang)
	if len(decided) > 1 && batchKind(decided) != "" {
		text = c.callsDecidedText(decided, lang, false)
	}
	c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE id = ?`, text, x.ID)
	if p := c.proposalOf(kind, id); p.Pending {
		c.syncCard(p)
	}
}

// callsDecidedText says a decided card of calls that do the same thing:
// what went, and how many were left. Live (read now), once its calls ran,
// as a list action says it is done; kept on the card, what it decided.
func (c *core) callsDecidedText(asks []accessQuestion, lang string, live bool) string {
	done, left := 0, 0
	for _, a := range asks {
		switch a.State {
		case startApproved:
			done += itemCount(a)
		case startRejected:
			left += itemCount(a)
		}
	}
	if done == 0 {
		return c.kindSentence(asks[0], left, lang)
	}
	// Done, as a list action says it (doneText); until then, what it does.
	s := c.kindSentence(asks[0], done, lang)
	if state, _, _, _ := c.asksExec(c.batchAsks(asks[0].Batch)); live && state == callDone {
		s = doneText(callTool(asks[0].Call), done, lang)
	}
	if left > 0 {
		key := "card.calls.left"
		if left > 1 {
			key = "card.calls.leftn"
		}
		s += ", " + fmt.Sprintf(tr(lang, key), left)
	}
	return s
}
