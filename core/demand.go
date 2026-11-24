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
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A demand: Eriphe asking the user's yes for calls it may not make alone,
// the brain's in its turn or a skill's run. One way to keep them, one
// card kind (calls), always in a node.
//
//   - keepCall keeps the call as it came (access_asks), on a card of its
//     own (its batch), and answers nothing: its keeper says it was not
//     run. What differs between the brain and a run is only in keeper:
//     where it is answered, its node, its taint, its run.
//   - On items a node's sources list (by their ids in its input), it
//     waits on their rows (item_cards.go); calls that do the same thing
//     to other items of the node fold into one card (call_fold.go),
//     whoever kept them, each item once. On no item, it is a block of its
//     node, {"card": ref}, placed by its keeper where it reads best; one
//     never placed is put at the end of the page by code (placeDemands).
//     The same call waiting in the same node is the same demand.
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
	node, labels := c.callRows(k.node, tool, input)
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
		c.turn.wrote()
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
	source, item, ls := "", "", ""
	for i, l := range labels {
		if i == 0 {
			source = l.Source
		} else if l.Source != source {
			source = ""
			break
		}
	}
	if len(labels) == 1 {
		item = labels[0].ID
	}
	if len(labels) > 0 {
		b, _ := json.Marshal(labels)
		ls = string(b)
	}
	res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, permission_id, server, category, account, call, input, tainted, created, run, node, source, item, labels)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.task, k.pid, n.Server, n.Category, n.Account, tool+" "+string(fields), raw,
		k.taint, time.Now().Unix(), k.run, node, source, item, ls)
	id, err := rowsID(res, err)
	if err != nil {
		log.Printf("demand: %s: %v", tool, err)
		return accessQuestion{}
	}
	batch := id
	if k.run != 0 && item != "" {
		if b := c.runItemBatch(k.run, node, source, item); b != 0 {
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
	keptBlock   = ` Place-la dans le document du nœud %s là où elle se lit le mieux (sous le plan qu'elle valide, par exemple) : node_edit avec un bloc {"card": "%s"} et rien d'autre ; sinon eriphe-core la met à la fin de la page.`
	keptItem    = " Si elle porte sur un élément de ton nœud sans le nommer (un événement à créer pour un mail), passe plutôt sa référence %d à item_note (call) sur cet élément : elle attendra sur sa ligne."
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
		if k.run != 0 {
			s += fmt.Sprintf(keptItem, a.ID)
		}
	}
	return s
}

// askItems are the items a kept call acts on, each with the source of
// its node that lists it: those kept with it (its labels), and the item
// it is on.
func askItems(a accessQuestion) []itemLabel {
	var out []itemLabel
	add := func(l itemLabel) {
		if l.ID == "" || slices.ContainsFunc(out, func(x itemLabel) bool { return x.ID == l.ID }) {
			return
		}
		if l.Source == "" {
			l.Source = a.Source
		}
		out = append(out, l)
	}
	for _, l := range a.Labels {
		add(l)
	}
	add(itemLabel{ID: a.Item, Source: a.Source})
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

// staleDemands takes out of the demands of a node the items a read of
// one of its sources no longer lists: a call left with none is withdrawn,
// and a card left with no call waiting, by code. Only the brain's and
// the runs' calls: those code keeps for itself (a tools review) follow
// their own source (tool_review.go).
func (c *core) staleDemands(ctx context.Context, node string, r nodeRead) {
	if r.Source.Items == nil || r.Source.Monitor != "" || r.Value == nil {
		return
	}
	listed := map[string]bool{}
	for _, it := range sourceItems(r.Name, r.Source, r.Value, 0) {
		if id, _ := it["id"].(string); id != "" {
			listed[id] = true
		}
	}
	cardMu.Lock()
	moved := map[int64]bool{}
	for _, a := range c.asksWhere(`node = ? AND state = ? AND batch != 0 AND task_id != '' AND (item != '' OR labels != '') ORDER BY id`, node, startProposed) {
		drop := map[string]bool{}
		for _, l := range askItems(a) {
			if l.Source == r.Name && !listed[l.ID] {
				drop[l.ID] = true
			}
		}
		if len(drop) == 0 {
			continue
		}
		if len(drop) == len(askItems(a)) || c.narrowCall(a, drop) {
			c.withdrawAsk(ctx, a)
		}
		moved[a.Batch] = true
	}
	var left []int64
	for _, batch := range slices.Sorted(maps.Keys(moved)) {
		ref := proposalRef(cardCalls, batch)
		if len(c.asksWhere(`batch = ? AND state = ?`, batch, startProposed)) == 0 {
			c.withdrawRef(ctx, ref, cardByCode) // nothing left to ask
			continue
		}
		left = append(left, batch)
		p := c.proposalOf(cardCalls, batch)
		c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE ref = ? AND state = ?`, p.Text(c.userLang()), ref, cardPending)
		c.proposalMoved(ref)
	}
	cardMu.Unlock()
	for _, batch := range left {
		c.itemCardMoved(batch)
	}
	if len(moved) > 0 {
		log.Printf("demand: node %s: %d card(s) lost items no longer in %s", node, len(moved), r.Name)
		c.bump()
	}
}

// placeDemands puts at the end of its node's page each card that waits
// on no item and that its keeper did not place: a block {"card": ref},
// written by code. Its keeper had its turn (the brain's, a run's) to put
// it where it reads best.
func (c *core) placeDemands() {
	if c.nodes == nil {
		return
	}
	for _, batch := range c.proposedBatches() {
		asks := c.batchAsks(batch)
		if len(asks) == 0 || asks[0].Node == "" || asks[0].TaskID == "" || !c.proposalOf(cardCalls, batch).Pending {
			continue
		}
		if slices.ContainsFunc(asks, func(a accessQuestion) bool { return len(askItems(a)) > 0 }) {
			continue // on its items' rows
		}
		c.placeCard(asks[0].Node, proposalRef(cardCalls, batch))
	}
}

// placeCard appends a card block to a node's document, unless one of its
// blocks places that card already.
func (c *core) placeCard(node, ref string) {
	if !c.nodeHasCard(node, ref) {
		return
	}
	for try := 0; try < 3; try++ {
		d, err := c.nodes.Read(node, 0)
		if err != nil || !d.Node.Meta.Archived.IsZero() {
			return
		}
		if slices.ContainsFunc(d.Node.Blocks(), func(b *nodefile.Block) bool { return b.Card == ref }) {
			return
		}
		name := "carte_" + strings.ReplaceAll(ref, ":", "_")
		_, err = c.nodes.Change(node, d.Version.Hash, nodeByCode, func(n *nodefile.Node) error {
			return n.PutAuthorBlock(name, []byte(`{"card": "`+ref+`"}`), "")
		})
		if err == nil {
			log.Printf("demand: %s placed at the end of node %s", ref, node)
			c.nodeMoved(node, false)
			return
		}
		if !errors.Is(err, nodestore.ErrConflict) {
			log.Printf("demand: %s: not placed in node %s: %v", ref, node, err)
			return
		}
	}
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

// migrateDemands puts the demands an older Eriphe kept in step with these
// rules, once at start, before the cards are synced: nothing runs and
// nothing waiting is lost. The brain's calls waiting on an access card and
// a run's on its run's card (runcalls) each get a card of its own (calls),
// the first of a run's taking its old card; then the cards fold, lose the
// items their sources no longer list, and those on no item are placed.
// Each step finds nothing the second time.
func (c *core) migrateDemands(ctx context.Context) {
	firstOfRun := map[int64]int64{}
	for _, a := range c.asksWhere(`state = ? AND batch = 0 AND call != '' AND refs = '' ORDER BY id`, startProposed) {
		if !catalogAsk(a) {
			continue // a page read the brain asks: its own card (access)
		}
		node := a.Node
		switch {
		case node != "":
		case a.Run != 0:
			if r := c.runByID(a.Run); r != nil {
				node = c.taskNode(r.Task)
			}
		default:
			if ids := c.cardNodes(proposalRef(cardAccess, a.ID)); len(ids) > 0 {
				node = ids[0]
			}
		}
		source, item, labels := a.Source, a.Item, ""
		if len(a.Labels) == 0 && node != "" {
			name, _, _ := strings.Cut(a.Call, " ")
			var input map[string]any
			json.Unmarshal([]byte(a.Input), &input)
			if ls := c.callItemLabels(node, name, input); len(ls) > 0 {
				b, _ := json.Marshal(ls)
				labels, source = string(b), ls[0].Source
				if len(ls) == 1 {
					item = ls[0].ID
				}
			}
		}
		if labels == "" && len(a.Labels) > 0 {
			b, _ := json.Marshal(a.Labels)
			labels = string(b)
		}
		c.store.db.Exec(`UPDATE access_asks SET batch = ?, node = ?, source = ?, item = ?, labels = ? WHERE id = ?`, a.ID, node, source, item, labels, a.ID)
		from, to := proposalRef(cardAccess, a.ID), proposalRef(cardCalls, a.ID)
		if a.Run != 0 {
			if _, ok := firstOfRun[a.Run]; ok {
				continue
			}
			firstOfRun[a.Run] = a.ID
			from = "runcalls:" + fmt.Sprint(a.Run)
		}
		c.store.db.Exec(`UPDATE convo_cards SET ref = ?, kind = ? WHERE ref = ?`, to, cardCalls, from)
		c.store.db.Exec(`UPDATE OR IGNORE node_cards SET ref = ? WHERE ref = ?`, to, from)
		log.Printf("demand: %s is now %s", from, to)
	}
	// A run's card left waiting with no call to take it: gone.
	if xs, err := c.cardsWhere(`kind = 'runcalls' AND state = ?`, cardPending); err == nil {
		for _, x := range xs {
			c.store.db.Exec(`UPDATE convo_cards SET state = ?, decided = ?, by = ? WHERE id = ?`, cardGone, time.Now().Unix(), cardByCode, x.ID)
			c.attentionDone(cardItemRef(x.ID))
		}
	}
	c.store.db.Exec(`DELETE FROM node_cards WHERE ref LIKE 'runcalls:%'`)
	for _, batch := range c.proposedBatches() {
		c.cardRaisedFor("", proposalRef(cardCalls, batch))
	}
	c.foldCalls()
	nodes := c.store.column(`SELECT DISTINCT node FROM access_asks WHERE state = ? AND batch != 0 AND task_id != '' AND node != ''`, startProposed)
	for _, node := range nodes {
		if c.nodes == nil {
			break
		}
		for _, r := range c.nodeReads(node) {
			c.staleDemands(ctx, node, r)
		}
	}
	c.placeDemands()
}
