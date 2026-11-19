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

// Same-kind proposals wait as one card, and the user picks what goes.
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
//   - Each call is a line with a box, all ticked: the user unticks what
//     should not go. Oui sends the ticked calls (only) and those unticked
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
// when it has no such list, or more than one.
func foldKey(a accessQuestion) (key, list string) {
	if len(a.Labels) == 0 {
		return "", ""
	}
	name, _, _ := strings.Cut(a.Call, " ")
	var input map[string]any
	if json.Unmarshal([]byte(a.Input), &input) != nil {
		return "", ""
	}
	ids := map[string]bool{}
	for _, l := range a.Labels {
		ids[l.ID] = true
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
	c.store.db.Exec(`DELETE FROM convo_cards WHERE ref = ?`, ref)
	c.store.db.Exec(`DELETE FROM node_cards WHERE ref = ?`, ref)
	to := proposalRef(cardCalls, target)
	p := c.proposalOf(cardCalls, target)
	c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE ref = ? AND state = ?`, p.Text(c.userLang()), to, cardPending)
	c.proposalMoved(ref)
	c.proposalMoved(to)
	c.linkedCardMoved(ref)
	log.Printf("cards: %s folded into %s", ref, to)
}

// proposalChoice is one call of a card the user may leave out: what it
// acts on, in words; how many items; where it stands.
type proposalChoice struct {
	ID    int64  `json:"id"`
	Text  string `json:"text"`
	N     int    `json:"n"`
	State string `json:"state"` // proposed, approved, rejected
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
		return callSentence(a.Call, a.Input, lang)
	}
	return strings.Join(items, ", ")
}

// kindSentence says what a call does to n items: its sentence with n
// items in its list; with how many, when the sentence does not say it.
func kindSentence(a accessQuestion, n int, lang string) string {
	_, list := foldKey(a)
	if list == "" {
		return callSentence(a.Call, a.Input, lang)
	}
	with := func(k int) string {
		var input map[string]any
		json.Unmarshal([]byte(a.Input), &input)
		values := make([]any, k)
		for i := range values {
			values[i] = fmt.Sprint(i)
		}
		input[list] = values
		raw, _ := json.Marshal(input)
		return callSentence(a.Call, string(raw), lang)
	}
	s := with(n)
	if with(1) == with(2) { // a sentence that does not count
		if n == 1 {
			return s
		}
		return fmt.Sprintf(tr(lang, "card.calls.items"), s, n)
	}
	return s
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

// keepCardText keeps a decided calls card's words as they are now: shown
// once its calls wait again on a new card.
func (c *core) keepCardText(x convoCard) {
	if x.Kind != cardCalls {
		return
	}
	kind, id, _ := parseProposalRef(x.Ref)
	c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE id = ?`, c.proposalOf(kind, id).Text(c.userLang()), x.ID)
}

// callsDecidedText says a decided card of calls that do the same thing:
// what went, and how many were left.
func (c *core) callsDecidedText(asks []accessQuestion, lang string) string {
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
		return kindSentence(asks[0], left, lang)
	}
	s := kindSentence(asks[0], done, lang)
	if left > 0 {
		key := "card.calls.left"
		if left > 1 {
			key = "card.calls.leftn"
		}
		s += " · " + fmt.Sprintf(tr(lang, key), left)
	}
	return s
}
