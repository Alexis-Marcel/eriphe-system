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
// action on each, and the brain asks it too: calls cards that wait in the
// same node, untouched, and whose calls do the same thing to other items
// fold into one: the oldest, whoever kept them (a run, the brain).
//
//   - The same thing: the same server and tool, the same arguments but
//     the one that names items its node's sources list (kept with the
//     call: its labels), a list of their ids or one id. Nothing of a
//     tool's own: the source's items descriptor says what an id is. A
//     different account or destination is another argument: another
//     card. A call on no item folds with nothing: the same call waiting
//     in the node is the same demand (keepCall, demand.go).
//   - Each item once: a call folded in loses the items the card already
//     acts on (narrowCall), and one left with none is withdrawn.
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
//   - Folded when a call is kept in the brain's turn, when a run ends,
//     before its cards are shown, and once at start (migrateDemands).

// errNotOnCard is a choice naming a call the card does not keep waiting.
var errNotOnCard = errors.New("a chosen call is not one this card keeps waiting")

// foldKey is what a kept call does, but for the items it acts on: its
// tool and its other arguments, and the name of the argument that names
// its items (a list of their ids, or one id); "" when it names none, or
// in more than one argument. Its items are those kept with it (its labels)
// and the item it is on (a call kept before labels were has only that).
func foldKey(a accessQuestion) (key, arg string) {
	ids := map[string]bool{}
	for _, id := range choiceItems(a) {
		ids[id] = true
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
		switch x := v.(type) {
		case []any:
			if len(x) == 0 || !allItems(x, ids) {
				continue
			}
		case string:
			if !ids[x] {
				continue
			}
		default:
			continue
		}
		if arg != "" {
			return "", "" // two arguments of items: which one says "other items" is not clear
		}
		arg = k
	}
	if arg == "" {
		return "", ""
	}
	rest := maps.Clone(input)
	delete(rest, arg)
	raw, _ := json.Marshal(rest) // its keys sorted
	return name + " " + arg + " " + string(raw), arg
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
// it names one or none.
func itemCount(a accessQuestion) int {
	_, arg := foldKey(a)
	if arg == "" {
		return 1
	}
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	values, _ := input[arg].([]any)
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

// untouched tells whether every call of a card waits, none running, but
// those a row's ✕ refused (refuseCalls): left out, the card still waits
// on the others.
func (c *core) untouched(asks []accessQuestion) bool {
	waits := false
	for _, a := range asks {
		if a.State == startRejected {
			continue
		}
		waits = true
		if !c.askWaits(a) {
			return false
		}
		if s, _ := c.callState(a.ID); s != "" {
			return false
		}
	}
	return waits
}

// foldInto moves a card's calls that wait to another, each losing the
// items the other acts on already (one left with none is withdrawn): its
// own card goes, unlinked from its node and out of the attention queue.
func (c *core) foldInto(batch, target int64) {
	covered := map[string]bool{}
	for _, a := range c.batchAsks(target) {
		if a.State == startProposed {
			for _, id := range choiceItems(a) {
				covered[id] = true
			}
		}
	}
	for _, a := range c.batchAsks(batch) {
		if a.State != startProposed {
			continue
		}
		drop := map[string]bool{}
		for _, id := range choiceItems(a) {
			if covered[id] {
				drop[id] = true
			}
		}
		if len(drop) > 0 && (len(drop) == len(choiceItems(a)) || c.narrowCall(a, drop)) {
			c.withdrawAsk(context.Background(), a) // all it does, the card does already
			continue
		}
		for _, id := range choiceItems(a) {
			covered[id] = true
		}
		c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, target, a.ID)
	}
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

// refuseCalls is a row's ✕ on a calls card its List's rows share (the
// card's Oui at the List's head): the calls named are refused, each as a
// card's Non refuses it, and the card waits on with the others, its words
// said again. Each must be a call of the card that waits, or nothing is
// refused; naming them all is the card's Non. The rows' source is read
// again: what it lists now.
func (c *core) refuseCalls(ctx context.Context, id int64, left []int64, by string) error {
	if len(left) == 0 {
		return errNotOnCard
	}
	cardMu.Lock()
	x, err := c.card(id)
	if err != nil {
		cardMu.Unlock()
		return errNoCard
	}
	kind, pid, _ := parseProposalRef(x.Ref)
	if x.State != cardPending || kind != cardCalls || x.Kind != cardCalls {
		cardMu.Unlock()
		if x.State != cardPending {
			return errCardDecided
		}
		return errNotOnCard
	}
	asks := c.batchAsks(pid)
	waiting := map[int64]bool{}
	for _, a := range asks {
		if c.askWaits(a) {
			waiting[a.ID] = true
		}
	}
	named := map[int64]bool{}
	for _, aid := range left {
		if !waiting[aid] {
			cardMu.Unlock()
			return errNotOnCard
		}
		named[aid] = true
	}
	if len(named) == len(waiting) {
		cardMu.Unlock()
		_, err := c.decideCard(ctx, id, false, by)
		return err
	}
	for aid := range named {
		if err := c.decideAccess(ctx, aid, false); err != nil && err != errCardDecided {
			cardMu.Unlock()
			return err
		}
	}
	p := c.proposalOf(cardCalls, pid)
	c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE id = ? AND state = ?`, p.Text(c.userLang()), id, cardPending)
	cardMu.Unlock()
	c.proposalMoved(x.Ref)
	log.Printf("cards: %s: %d of its calls refused by %s", x.Ref, len(named), by)
	for _, a := range asks {
		if a.Node != "" && a.Source != "" {
			c.readNodeLater(context.Background(), a.Node, []string{a.Source})
			break
		}
	}
	return nil
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
		s = c.doneText(asks[0].Call, done, lang)
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
