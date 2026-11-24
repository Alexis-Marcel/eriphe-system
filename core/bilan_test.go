package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A view as a bilan (docs/node-format.md): rows that carry the card that
// waits on them, Lists with several filters, settled items that leave,
// decided cards that leave the page's foot, and the view's skill woken by
// the user's hand.

// Several filters on one List: an item shows when each keeps it.
func TestListFiltersAll(t *testing.T) {
	srcs := map[string]nodefile.Source{"mails": {Tool: "mail/search", Items: &nodefile.Items{Path: "messages", ID: "id"}}}
	name, fs, err := listBinding("/mails/messages[_group=À traiter][_state!=done]", srcs)
	if err != nil || name != "mails" || len(fs) != 2 {
		t.Fatalf("binding: %q %+v %v", name, fs, err)
	}
	for _, tc := range []struct {
		it   map[string]any
		keep bool
	}{
		{map[string]any{"_group": "À traiter", "_state": "act"}, true},
		{map[string]any{"_group": "À traiter", "_state": "done"}, false},
		{map[string]any{"_group": "À jeter", "_state": "none"}, false},
		{map[string]any{"_group": "À traiter"}, true}, // no state reads as empty
	} {
		if got := fs.keeps(tc.it); got != tc.keep {
			t.Errorf("%v: keeps %v", tc.it, got)
		}
	}
	if !itemFilters(nil).keeps(map[string]any{}) {
		t.Error("no filter drops an item")
	}
	for _, bad := range []string{"/mails/messages[_group]", "/mails/messages[_group=a]x", "/mails/messages[_group=a][", "/mails/messages[_group=a][_state]"} {
		if _, _, err := listBinding(bad, srcs); err == nil || !strings.Contains(err.Error(), "a filter is [field=value]") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// itemCardOf is the calls card that waits on an item of node's source.
func itemCardOf(c *core, node, source, item string) string { return c.itemCards(node, source)[item] }

// pageList is a node's page's List, by its block and component.
func pageListOf(t *testing.T, w *web, node, block, comp string) (pageList, map[string]any) {
	t.Helper()
	p, err := w.nodePageOf(node, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range p.Parts {
		if part.Block != nil && part.Block.Name == block {
			for _, l := range part.Block.Lists {
				if l.Component == comp {
					return l, part.Block.Data
				}
			}
			t.Fatalf("block %s has no List %s: %+v", block, comp, part.Block.Lists)
		}
	}
	t.Fatalf("no block %s", block)
	return pageList{}, nil
}

// itemOf is an item of a source in a block's data, by its id.
func itemOf(data map[string]any, source, id string) map[string]any {
	src, _ := data[source].(map[string]any)
	list, _ := src["messages"].([]any)
	for _, it := range list {
		if o, _ := it.(map[string]any); o["id"] == id {
			return o
		}
	}
	return nil
}

// A card on an item is on its row: each item carries its ref (_card,
// code's, over whatever the source said), the page's List tells it
// (items[].card), the page draws it on the row, and it is not at the
// page's foot too. Its words are its tool's title and its item; what it
// sends that is to read (a reply) shows, named, out of Détails.
func TestItemCardOnItsRow(t *testing.T) {
	c, fm, node, r := mailViewOf(t)
	w := &web{c: c}
	fm.mu.Lock()
	fm.got[1]["_card"] = "calls:999" // a source's own field never passes for code's
	fm.mu.Unlock()
	c.readNode(context.Background(), node, []string{"mails"}, time.Now())
	reply := "Bonjour,\n\nMerci pour le devis : il me va, et la date aussi. Peux-tu m'envoyer le bon à signer ?\n\nÀ bientôt"
	keep(c, r.Task, "p1", "mcp__monitor__send", map[string]any{"id": "m1", "body": reply})
	endTurn(t, c, r.Task, "Une réponse prête.")

	ref := itemCardOf(c, node, "mails", "m1")
	if !strings.HasPrefix(ref, "calls:") {
		t.Fatalf("no card on m1: %v", c.itemCards(node, "mails"))
	}
	l, data := pageListOf(t, w, node, "recus", "root")
	cards := map[string]string{}
	for _, it := range l.Items {
		cards[it.ID] = it.Card
	}
	if cards["m1"] != ref || cards["m2"] != "" {
		t.Errorf("the List tells %v", cards)
	}
	if itemOf(data, "mails", "m1")["_card"] != ref || itemOf(data, "mails", "m2")["_card"] != "" {
		t.Errorf("the data: %v / %v", itemOf(data, "mails", "m1"), itemOf(data, "mails", "m2"))
	}

	// Its words: the tool's title, the item; the reply to read, named.
	_, id, _ := parseProposalRef(ref)
	p := c.proposalOf(cardCalls, id)
	if got := p.Text("fr"); got != "Send a reply · « STAGE – Data Engineer » · Emplois Example ⚠" {
		t.Errorf("text %q", got)
	}
	fs := p.FieldsIn("fr")
	if len(fs) != 2 || fs[0].Name != "Texte" || fs[0].Value != reply || fs[0].Detail || !fs[1].Detail || strings.Contains(fs[1].Value, "devis") || !strings.Contains(fs[1].Value, "m1") {
		t.Errorf("fields %+v", fs)
	}

	// On its row, never at the page's foot too.
	pg, _ := w.nodePageOf(node, 0, "fr")
	if slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("the foot shows %s, on its row: %+v", ref, pg.Cards)
	}
}

// A card on an item, said yes: once its call ran, code notes the item
// done (its note and group kept), so that a List that keeps what is not
// done lets it go; its row's card is gone; the decided card is listed in
// the page's settled ones, never at its foot; and the view's skill is
// woken once by the user's gesture, to write its bilan again.
func TestItemCardSettlesItsItem(t *testing.T) {
	c, fm, node, r := mailViewOf(t)
	w := &web{c: c}
	mustNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m1", "note": "Répondre — brouillon prêt", "state": "act", "group": "À traiter"})
	keep(c, r.Task, "p1", "mcp__monitor__send", map[string]any{"id": "m1", "body": "Merci, ça me va."})
	endTurn(t, c, r.Task, "Une réponse prête.")
	ref := itemCardOf(c, node, "mails", "m1")
	xs, err := c.cardsWhere(`ref = ?`, ref)
	if err != nil || len(xs) != 1 {
		t.Fatalf("card %s: %v %v", ref, xs, err)
	}
	queued := func() int {
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ? AND key = ?`, evNodeGesture, nodeRef(node)).Scan(&n)
		return n
	}
	if _, err := c.decideCard(context.Background(), xs[0].ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	waitSent(t, fm, 1)
	for i := 0; i < 300 && c.itemNotes(node, "mails")["m1"].State != itemDone; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	n := c.itemNotes(node, "mails")["m1"]
	if n.State != itemDone || n.Note != "Répondre — brouillon prêt" || n.Group != "À traiter" || n.Author != nodeByCode {
		t.Fatalf("the item's note: %+v", n)
	}
	if got := itemCardOf(c, node, "mails", "m1"); got != "" {
		t.Errorf("the row still shows %s", got)
	}
	for i := 0; i < 300 && queued() == 0; i++ {
		time.Sleep(10 * time.Millisecond) // told just after the note
	}
	if q := queued(); q != 1 {
		t.Errorf("the skill was woken %d times", q)
	}
	c.itemCardMoved(func() int64 { _, id, _ := parseProposalRef(ref); return id }()) // moved again: settled once
	if q := queued(); q != 1 {
		t.Errorf("woken again: %d", q)
	}
	pg, _ := w.nodePageOf(node, 0, "fr")
	if slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) || !slices.ContainsFunc(pg.Settled, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("foot %+v, settled %+v", pg.Cards, pg.Settled)
	}
	// The gestures pause before the run: none now, one once quiet.
	if rs := runsOfSkill(t, c, "tri-mails"); len(rs) != 1 {
		t.Fatalf("runs %+v", rs)
	}
	c.store.db.Exec(`UPDATE skill_runs SET state = ? WHERE id = ?`, skillRunDone, r.ID)
	c.tickSkills(context.Background(), time.Now())
	if rs := runsOfSkill(t, c, "tri-mails"); len(rs) != 1 {
		t.Fatalf("a run before the gestures paused: %+v", rs)
	}
	c.tickSkills(context.Background(), time.Now().Add(gestureQuiet+time.Second))
	rs := runsOfSkill(t, c, "tri-mails")
	if len(rs) != 2 || rs[0].Key != nodeRef(node) {
		t.Fatalf("runs after the gesture %+v", rs)
	}
}

// Said no: the item stays as it was, its row without its Oui, and the
// skill is woken to note it again. A node with no skill is never woken.
func TestItemCardSaidNo(t *testing.T) {
	c, _, node, r := mailViewOf(t)
	keep(c, r.Task, "p1", "mcp__monitor__send", map[string]any{"id": "m1", "body": "Merci."})
	endTurn(t, c, r.Task, "Une réponse prête.")
	ref := itemCardOf(c, node, "mails", "m1")
	xs, _ := c.cardsWhere(`ref = ?`, ref)
	if _, err := c.decideCard(context.Background(), xs[0].ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if n := c.itemNotes(node, "mails")["m1"]; n.State == itemDone {
		t.Errorf("a no settled the item: %+v", n)
	}
	if got := itemCardOf(c, node, "mails", "m1"); got != "" {
		t.Errorf("the row still shows %s", got)
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ? AND key = ?`, evNodeGesture, nodeRef(node)).Scan(&n)
	if n != 1 {
		t.Errorf("%d gestures told", n)
	}
	other := mustNode(t, c, nodestore.New{Title: "Ailleurs", Body: "Rien."})
	c.wakeNodeSkills(other, "action")
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ?`, evNodeGesture).Scan(&n)
	if n != 1 {
		t.Errorf("a node with no skill was woken: %d", n)
	}
}

// A run puts on an item a call that acts on it without naming it (an
// event made from a mail): item_note's call, a call of that very run
// only, still waiting; its card then waits on the item's row, its words
// naming the item. The run is told the call's reference.
func TestItemNoteBindsACall(t *testing.T) {
	c, _, node, r := mailViewOf(t)
	keep(c, r.Task, "p1", "mcp__monitor__rollback", map[string]any{"version": "1.0"})
	var call int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE run = ? AND batch = id`, r.ID).Scan(&call)
	if call == 0 {
		t.Fatal("no call kept")
	}
	items, _ := c.store.inbox(r.Task)
	if len(items) == 0 || !strings.Contains(fmt.Sprint(items[len(items)-1].Data), fmt.Sprintf("sa référence %d", call)) {
		t.Errorf("the run is not told its call's reference: %+v", items)
	}
	if _, msg := callNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m9", "note": "x", "call": call}); !strings.Contains(msg, "not an item") {
		t.Errorf("an item not listed: %q", msg)
	}
	if _, msg := callNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m2", "note": "x", "call": call + 50}); !strings.Contains(msg, "not a call this run kept") {
		t.Errorf("another call: %q", msg)
	}
	mustNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m2", "note": "Revenir à la 1.0", "state": "act", "call": call})
	endTurn(t, c, r.Task, "Une proposition.")
	ref := itemCardOf(c, node, "mails", "m2")
	if ref != proposalRef(cardCalls, call) {
		t.Fatalf("the card on m2: %q (%v)", ref, c.itemCards(node, "mails"))
	}
	if p := c.proposalOf(cardCalls, call); !strings.Contains(p.Text("fr"), "« Votre lettre de la semaine »") {
		t.Errorf("its words: %q", p.Text("fr"))
	}
	if pg, _ := (&web{c: c}).nodePageOf(node, 0, "fr"); slices.ContainsFunc(pg.Parts, func(p pagePart) bool { return p.Block != nil && p.Block.Card == ref }) {
		t.Error("a call on an item was placed as a block of its own")
	}
}

// pendingAsks: what still asks something at a page's foot.
func TestPendingAsks(t *testing.T) {
	for _, tc := range []struct {
		v    proposalView
		asks bool
	}{
		{proposalView{State: cardPending}, true},
		{proposalView{State: cardApproved, Exec: callRunning}, true},
		{proposalView{State: cardApproved, Exec: callFailed}, false}, // decided: its ⋯ menu lists it
		{proposalView{State: cardGone}, false},
		{proposalView{State: cardApproved, Exec: callDone}, false},
		{proposalView{State: cardApproved}, false},
		{proposalView{State: cardRejected}, false},
	} {
		if got := pendingAsks(tc.v); got != tc.asks {
			t.Errorf("%+v: %v", tc.v, got)
		}
	}
	raw, _ := json.Marshal(nodePage{})
	if !strings.Contains(string(raw), `"settled"`) {
		t.Error("a page has no settled cards field")
	}
}

// A long text argument is lifted out of Détails, named by the tool's
// schema's title, else in words for a common name, else by its own; a
// short one stays.
func TestCallShownNames(t *testing.T) {
	c, _, _ := viewCore(t)
	long := strings.Repeat("Une phrase assez longue. ", 6)
	in := map[string]any{"id": "m1", "body": long, "note": "court"}
	a := accessQuestion{Call: "mcp__monitor__send " + mustJSON(callFields(in)), Input: mustJSON(in)}
	fs, names := c.callShown(a, "fr")
	if len(fs) != 1 || fs[0].Name != "Texte" || fs[0].Value != long || !slices.Equal(names, []string{"body"}) {
		t.Errorf("fr: %+v %v", fs, names)
	}
	if fs, _ := c.callShown(a, "en"); len(fs) != 1 || fs[0].Name != "Text" {
		t.Errorf("en: %+v", fs)
	}
	b := accessQuestion{Call: "mcp__monitor__send []", Input: mustJSON(map[string]any{"id": "m1", "poem": "un\ndeux"})}
	if fs, _ := c.callShown(b, "fr"); len(fs) != 1 || fs[0].Name != "poem" {
		t.Errorf("by its name: %+v", fs)
	}
	r := accessQuestion{Call: "mcp__monitor__reply []", Input: mustJSON(map[string]any{"id": "m1", "text": long})}
	if fs, _ := c.callShown(r, "fr"); len(fs) != 1 || fs[0].Name != "Ta réponse" {
		t.Errorf("by its schema's title: %+v", fs)
	}
	if d := callDetail(a, []string{"body"}); strings.Contains(d.Value, "phrase") || !strings.Contains(d.Value, "court") {
		t.Errorf("details %+v", d)
	}
}

// A block never holds a Proposal, tainted or not: a row's card is code's,
// drawn on its row from the List's rows (items[].card), never from the
// block's data an author may write.
func TestNoProposalInABlock(t *testing.T) {
	tw, _, _, id := rowsWeb(t)
	c := tw.c
	for _, tainted := range []bool{false, true} {
		block := "```eriphe:block inbox\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/received/messages", "componentId": "row"}},
  {"id": "row", "component": "Row", "children": ["t", "yes"]},
  {"id": "t", "component": "Text", "text": {"@path": "subject"}},
  {"id": "yes", "component": "Proposal", "inline": true, "proposalId": {"@path": "_card"}}],
 "sources": {"received": {"resource": "monitor", "uri": "monitor://received", "items": {"path": "messages", "id": "id", "title": "subject"}}}}` + "\n```\n"
		_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, tainted, c.nodeSourceOK(), func(n *nodefile.Node) error { return n.AuthorBody("Les mails.\n\n" + block) })
		if err == nil || !strings.Contains(err.Error(), "Proposal") {
			t.Errorf("tainted %v: %v", tainted, err)
		}
	}
}
