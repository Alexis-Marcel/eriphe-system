package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// A demand: Eriphe asking the user's yes for calls it may not make alone,
// the brain's in its turn or a skill's run. One way to keep them, one
// card kind (calls), always in a node.
//
//   - keepCall keeps the call as it came (access_asks), on a card of its
//     own (its batch), and answers nothing: its keeper says it was not
//     run. What differs between the brain and a run is only in keeper:
//     where it is answered, its node, its taint, its run.
//   - On items its node's page references (page_items.go), or a node's
//     sources list (by their ids in its input), it waits on their lines
//     (item_cards.go); calls that do the same thing to other items of the
//     node fold into one card (call_fold.go), whoever kept them, each item
//     once. On no item, its keeper may place it in a line of its node's
//     page where it reads best ([its words](card:ref)); one never placed
//     waits at the foot of the page. The same call waiting in the same
//     node is the same demand.
//   - Its items leave the source: they leave the demand; one left with no
//     item is withdrawn by code (staleDemands), never the user's no.
//   - Tasks in containers ask their access their own way (access_card.go):
//     a grant, a release, not a kept call.

// keeper is who keeps a call for the user's yes.
type keeper struct {
	task, pid string
	run       int64  // a skill's run; 0: the brain
	node      string // the node it works in: its turn's, its run's; "" for none
	taint     string // what someone else wrote that it read
	turn      bool   // the brain in its turn: the card goes with its message
}

// keepCall keeps a call for its card: the same call already waiting in
// the node, else a new one on a card of its own, folded with those that
// do the same to other items of the node. It gives the call as kept: its
// batch is its card.
func (c *core) keepCall(ctx context.Context, k keeper, tool string, input map[string]any) accessQuestion {
	raw, _ := json.Marshal(input)
	// Its node: the one whose sources list the items it names (its
	// keeper's first), else its keeper's.
	node, labels := c.callRows(ctx, k.node, tool, input)
	if node == "" {
		node = k.node
	}
	a, ok := c.sameCallWaiting(node, tool, string(raw))
	if !ok {
		a = c.newDemand(ctx, k, node, labels, tool, input, string(raw))
	}
	if a.ID == 0 {
		return a
	}
	if k.turn {
		c.foldCalls() // into the card of the node that does the same (call_fold.go)
		a, _ = c.accessAskOf(a.ID)
		inherit, source := c.turnAbout, c.turnSource()
		if a.Node != "" {
			inherit, source = func() string { return c.sourceAbout(nodeRef(a.Node)) }, nodeRef(a.Node)
		}
		c.cardProposedBy(cardCalls, a.Batch, inherit, source)
	}
	return a
}

// newDemand writes a kept call: on its items' rows when it names some
// (its labels), else its node's; on its own card, but a run's call on an
// item its other calls on that item are on.
func (c *core) newDemand(ctx context.Context, k keeper, node string, labels []itemLabel, tool string, input map[string]any, raw string) accessQuestion {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	fields, _ := json.Marshal(cardFields(c.callContext(cctx, tool, input), input))
	cancel()
	n := c.callNeed(tool, input, c.pool.facts(tool, input))
	item, ls := "", ""
	if len(labels) == 1 {
		item = labels[0].ID
	}
	if len(labels) > 0 {
		b, _ := json.Marshal(labels)
		ls = string(b)
	}
	res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, permission_id, server, category, account, call, input, tainted, created, run, node, item, labels)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.task, k.pid, n.Server, n.Category, n.Account, tool+" "+string(fields), raw,
		k.taint, time.Now().Unix(), k.run, node, item, ls)
	id, err := rowsID(res, err)
	if err != nil {
		log.Printf("demand: %s: %v", tool, err)
		return accessQuestion{}
	}
	batch := id
	if k.run != 0 && item != "" {
		if b := c.runItemBatch(k.run, node, n.Server, item); b != 0 {
			batch = b
		}
	}
	c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, batch, id)
	// Linked to its node now: its keeper may place its block before the
	// card shows (a run's, once the run is over).
	c.cardRaisedFor("", proposalRef(cardCalls, batch))
	a, _ := c.accessAskOf(id)
	log.Printf("demand: call %d kept on card %d (%s, node %q)", id, batch, tool, node)
	return a
}

// sameCallWaiting is the call already kept in the node with the same
// tool and input, still waiting: the same demand.
func (c *core) sameCallWaiting(node, tool, input string) (accessQuestion, bool) {
	var id int64
	if c.store.db.QueryRow(`SELECT id FROM access_asks WHERE node = ? AND call LIKE ? AND input = ? AND state = ? AND batch != 0 ORDER BY id LIMIT 1`,
		node, tool+" %", input, startProposed).Scan(&id) != nil {
		return accessQuestion{}, false
	}
	a, err := c.accessAskOf(id)
	return a, err == nil
}

// What a keeper is told of a call kept: not run, on which card, and
// where it waits.
const (
	keptByBrain = "Pas exécutée pour l'instant : la carte %s la demande à %s. Ne la relance pas et continue, tu restes disponible. S'il dit oui, eriphe-core l'exécute telle quelle et te donne le résultat."
	keptByRun   = "Pas exécutée maintenant : eriphe-core la garde telle quelle sur la carte %s, que %s verra à la fin de ton tour ; s'il dit oui, elle sera exécutée. N'insiste pas et continue ; dis-le dans ton message final."
	keptOnRows  = " Elle attend sur les lignes des éléments qu'elle touche."
	keptBlock   = " Pour la placer dans la page du nœud %s là où elle se lit le mieux (sur la ligne de l'élément qu'elle concerne, sous le plan qu'elle valide), écris-y un lien [ses mots](card:%s) ; sinon elle attend en bas de la page."
)

// keptSay is what a keeper is told of the call it kept.
func (c *core) keptSay(k keeper, a accessQuestion) string {
	ref := proposalRef(cardCalls, a.Batch)
	s := fmt.Sprintf(keptByBrain, ref, c.conf().User)
	if k.run != 0 {
		s = fmt.Sprintf(keptByRun, ref, c.conf().User)
	}
	switch {
	case len(askItems(a)) > 0:
		s += keptOnRows
	case a.Node != "":
		s += fmt.Sprintf(keptBlock, a.Node, ref)
	}
	return s
}

// askItems are the items a kept call acts on: those kept with it (its
// labels), and the item it is on.
func askItems(a accessQuestion) []itemLabel {
	var out []itemLabel
	add := func(l itemLabel) {
		if l.ID == "" || slices.ContainsFunc(out, func(x itemLabel) bool { return x.ID == l.ID }) {
			return
		}
		out = append(out, l)
	}
	for _, l := range a.Labels {
		add(l)
	}
	add(itemLabel{ID: a.Item, Server: a.Need.Server})
	return out
}

// narrowCall takes items out of a waiting call: the ids dropped leave
// the list of items its input names (callItems), its labels and the
// parameters its card shows; what it runs on a yes is what is left, as
// shown. empty: nothing would be left (or its items are not a list):
// it is the caller's to withdraw it.
func (c *core) narrowCall(a accessQuestion, drop map[string]bool) (empty bool) {
	var input map[string]any
	if json.Unmarshal([]byte(a.Input), &input) != nil {
		return true
	}
	key, _ := callItems(input, askItems(a))
	list, ok := input[key].([]any)
	if !ok {
		return true // one item, dropped
	}
	var kept []any
	for _, v := range list {
		if !drop[valueText(v)] {
			kept = append(kept, v)
		}
	}
	switch {
	case len(kept) == 0:
		return true
	case len(kept) == len(list):
		return false
	}
	input[key] = kept
	raw, _ := json.Marshal(input)
	name, f, _ := strings.Cut(a.Call, " ")
	var fields []cardField
	json.Unmarshal([]byte(f), &fields)
	for i := range fields {
		if fields[i].Name == key {
			v, _ := json.Marshal(kept)
			fields[i].Value = string(v)
		}
	}
	fs, _ := json.Marshal(fields)
	var labels []itemLabel
	for _, l := range a.Labels {
		if !drop[l.ID] {
			labels = append(labels, l)
		}
	}
	ls := ""
	if len(labels) > 0 {
		b, _ := json.Marshal(labels)
		ls = string(b)
	}
	item := a.Item
	if drop[item] {
		item = ""
	}
	if len(labels) == 1 {
		item = labels[0].ID
	}
	c.store.db.Exec(`UPDATE access_asks SET input = ?, call = ?, labels = ?, item = ? WHERE id = ? AND state = ?`,
		string(raw), name+" "+string(fs), ls, item, a.ID, startProposed)
	log.Printf("demand: call %d narrowed to %d item(s)", a.ID, len(kept))
	return false
}

// answerCall is a page's yes or no on one call of a card (a skill's
// page): yes runs it alone, as kept; no refuses it; the card's other
// calls wait on.
func (c *core) answerCall(ctx context.Context, id int64, approve bool, by string) error {
	a, err := c.accessAskOf(id)
	if err != nil || a.Batch == 0 {
		return errNoCard
	}
	p := c.proposalOf(cardCalls, a.Batch)
	if !p.Pending {
		return errCardDecided
	}
	c.syncCard(p)
	xs, err := c.cardsWhere(`ref = ? AND state = ?`, proposalRef(cardCalls, a.Batch), cardPending)
	if err != nil || len(xs) == 0 {
		return errNoCard
	}
	if approve {
		_, err = c.decideCardChosen(ctx, xs[0].ID, []int64{id}, nil, by)
		return err
	}
	return c.refuseCalls(ctx, xs[0].ID, []int64{id}, by)
}
