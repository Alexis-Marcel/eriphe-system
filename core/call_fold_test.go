package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// moveMail is the fake server's move of mails, as a run keeps it.
func moveMail(account, to string, ids ...string) map[string]any {
	list := []any{}
	for _, id := range ids {
		list = append(list, id)
	}
	return map[string]any{"account": account, "ids": list, "to": to}
}

// askOn keeps a call on its node as a card of its own, as a run's call on
// an item is kept (item_cards.go): proposed, its batch itself, the
// items it acts on its labels. It gives its id.
func askOn(t *testing.T, c *core, node, tool string, input map[string]any, labels ...itemLabel) int64 {
	t.Helper()
	raw, _ := json.Marshal(input)
	ls, _ := json.Marshal(labels)
	res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, server, category, call, input, created, node, item, labels)
		VALUES ('t-x', 'monitor', 'change', ?, ?, ?, ?, ?, ?)`, tool+" []", string(raw), time.Now().Unix(), node, labels[0].ID, string(ls))
	id, err := rowsID(res, err)
	if err != nil {
		t.Fatal(err)
	}
	c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, id, id)
	return id
}

// moveWords is what a folded card of moves to the archive says, in plain
// words (callPhrase): for how many.
func moveWords(n string) string {
	return "Archiver " + n
}

// waitTraced waits until the node's trace tells n decided moves: a call
// run on a yes is traced after its card says it is done.
func waitTraced(t *testing.T, c *core, node string, n int) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if raw, _ := c.nodes.Trace(node); strings.Count(string(raw), "mcp__monitor__move") >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the node's trace lacks %d moves", n)
}

// batchOf is the card a call is on.
func batchOf(c *core, id int64) int64 {
	a, _ := c.accessAskOf(id)
	return a.Batch
}

// The skill, woken once per mail, proposes to archive each: one card
// « Archiver 2 mails » in the view, each mail a line the user may leave
// out, Oui saying how many go.
func TestSameKindCallsWaitAsOneCard(t *testing.T) {
	c, node, r := mailView(t)
	keep(c, r.Task, "p1", "mcp__monitor__move", moveMail("sam@example.org", "archive", "m1"))
	keep(c, r.Task, "p2", "mcp__monitor__move", moveMail("sam@example.org", "archive", "m2"))
	endTurn(t, c, r.Task, "Deux mails à archiver.")

	calls := cardsOf(c, cardCalls)
	if len(calls) != 1 {
		t.Fatalf("cards %+v", calls)
	}
	p := calls[0]
	if got := p.Text("fr"); got != moveWords("2 mails") {
		t.Errorf("text %q", got)
	}
	var lines []string
	for _, ch := range p.choices("fr") {
		lines = append(lines, ch.Text)
		if ch.N != 1 || ch.State != startProposed {
			t.Errorf("choice %+v", ch)
		}
	}
	if !slices.Equal(lines, []string{"« STAGE – Data Engineer » · Emplois Example ⚠", "« Votre lettre de la semaine » · news@example.org ⚠"}) {
		t.Errorf("choices %q", lines)
	}
	if got := p.acts("fr"); !slices.Equal(got, []string{moveWords("1 mail"), moveWords("2 mails")}) {
		t.Errorf("acts %q", got)
	}
	if len(p.Fields) != 2 || !p.Fields[0].Detail || !p.Fields[1].Detail {
		t.Errorf("each exact call is under Détails: %+v", p.Fields)
	}
	ref := proposalRef(cardCalls, p.ID)
	if !slices.Contains(c.cardNodes(ref), node) {
		t.Errorf("the card is not the view's: %v", c.cardNodes(ref))
	}
	if xs, _ := c.cardsWhere(`kind = ?`, cardCalls); len(xs) != 1 {
		t.Errorf("calls cards %+v", xs)
	}
}

// Only what does the same thing folds: not another account or
// destination, another tool, nor a card decided. Cards made before (an
// install updated) fold at the next sync: the folded ones go, from the
// node too.
func TestFoldKeepsApartWhatDiffers(t *testing.T) {
	c, node, _ := mailView(t)
	l := func(id string) itemLabel { return itemLabel{ID: id, Title: "Mail " + id, Detail: "news@example.org"} }
	a := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m1"), l("m1"))
	other := askOn(t, c, node, "mcp__monitor__move", moveMail("lou@example.org", "archive", "m2"), l("m2"))
	folder := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "Factures", "m3"), l("m3"))
	decided := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m4"), l("m4"))
	c.store.db.Exec(`UPDATE access_asks SET state = ? WHERE id = ?`, startApproved, decided)
	b := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m5", "m6"), l("m5"), l("m6"))
	tool := askOn(t, c, node, "mcp__monitor__send", map[string]any{"id": "m7", "body": "Merci"}, l("m7"))
	// Kept before labels were: only the item it is on.
	bare := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m8"), l("m8"))
	c.store.db.Exec(`UPDATE access_asks SET labels = '' WHERE id = ?`, bare)
	// Their cards, as an install made them before.
	for _, id := range []int64{a, other, folder, b, tool, bare} {
		c.cardNow(cardCalls, id, "", refNode+":"+node)
	}
	c.syncCards()

	if batchOf(c, b) != a || batchOf(c, bare) != a {
		t.Errorf("the same move did not fold: %d on %d, %d on %d", b, batchOf(c, b), bare, batchOf(c, bare))
	}
	for _, id := range []int64{other, folder, decided, tool} {
		if batchOf(c, id) != id {
			t.Errorf("call %d folded into %d", id, batchOf(c, id))
		}
	}
	ref := proposalRef(cardCalls, b)
	if xs, _ := c.cardsWhere(`ref = ?`, ref); len(xs) != 0 {
		t.Errorf("the folded card stays: %+v", xs)
	}
	if ns := c.cardNodes(ref); len(ns) != 0 {
		t.Errorf("the folded card is still linked: %v", ns)
	}
	p := c.proposalOf(cardCalls, a)
	if got := p.Text("fr"); got != moveWords("4 éléments") {
		t.Errorf("text %q", got)
	}
	if got := p.acts("fr"); len(got) != 4 || got[3] != moveWords("4 éléments") {
		t.Errorf("acts %q", got)
	}
	if xs, _ := c.cardsWhere(`ref = ?`, proposalRef(cardCalls, a)); len(xs) != 1 || xs[0].Text != moveWords("4 éléments") {
		t.Errorf("the card kept %+v", xs)
	}
	// Folded once: the next sync changes nothing.
	c.syncCards()
	if batchOf(c, b) != a || batchOf(c, other) != other {
		t.Error("a second sync moved a call")
	}
}

// The yes runs exactly the calls ticked, each as kept; those unticked
// are refused, and the card says how many were left. A choice naming a
// call that is not the card's, or not waiting, decides nothing.
func TestChosenCallsRunAlone(t *testing.T) {
	c, fm, node, _ := mailViewOf(t)
	l := func(id string) itemLabel { return itemLabel{ID: id, Title: "Mail " + id} }
	a := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m1"), l("m1"))
	b := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m2"), l("m2"))
	d := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m3"), l("m3"))
	stranger := askOn(t, c, node, "mcp__monitor__move", moveMail("lou@example.org", "archive", "m4"), l("m4"))
	c.cardNow(cardCalls, a, "", refNode+":"+node)
	c.syncCards()
	xs, _ := c.cardsWhere(`ref = ?`, proposalRef(cardCalls, a))
	if len(xs) != 1 {
		t.Fatalf("cards %+v", xs)
	}
	card := xs[0].ID
	ctx := context.Background()
	for _, bad := range []struct{ only, left []int64 }{
		{[]int64{a, stranger}, nil}, // not the card's
		{[]int64{a}, []int64{a}},    // both ticked and not
		{nil, []int64{a}},           // nothing ticked: that is Non
	} {
		if _, err := c.decideCardChosen(ctx, card, bad.only, bad.left, "web"); !errors.Is(err, errNotOnCard) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	for _, id := range []int64{a, b, d, stranger} {
		if x, _ := c.accessAskOf(id); x.State != startProposed {
			t.Fatalf("a refused choice decided call %d: %s", id, x.State)
		}
	}
	// Through the workspace's route: two ticked, one left.
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	yes := func(form url.Values) (int, string) {
		code, body, _ := tw.do("POST", fmt.Sprintf("/cards/%d/yes", card), form, map[string]string{"X-CSRF-Token": csrf})
		return code, body
	}
	if code, body := yes(url.Values{"only": {fmt.Sprintf("%d,%d", a, d)}, "left": {fmt.Sprint(b)}}); code != http.StatusOK {
		t.Fatalf("yes: %d %s", code, body)
	}
	want := map[int64]string{a: startApproved, d: startApproved, b: startRejected, stranger: startProposed}
	for id, state := range want {
		if x, _ := c.accessAskOf(id); x.State != state {
			t.Errorf("call %d is %s, want %s", id, x.State, state)
		}
	}
	for i := 0; i < 200 && func() bool { s, _ := c.callsExec(a); return s != callDone }(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if s, _ := c.callsExec(a); s != callDone {
		t.Fatalf("the chosen calls: %s", s)
	}
	moved := movedIDs(fm)
	slices.Sort(moved)
	if !slices.Equal(moved, []string{"m1", "m3"}) {
		t.Errorf("moved %v, want m1 and m3 only", moved)
	}
	p := c.proposalOf(cardCalls, a)
	if p.Pending || p.Outcome != cardApproved {
		t.Errorf("the card %+v", p)
	}
	if got := p.Text("fr"); got != "2 déplacés, 1 laissé" {
		t.Errorf("decided text %q", got)
	}
	if x, _ := c.card(card); x.State != cardApproved || x.Text != moveWords("2 éléments")+", 1 laissé" {
		t.Errorf("the card %+v", x)
	}
	if code, _ := yes(url.Values{"only": {"x"}}); code != http.StatusBadRequest {
		t.Errorf("a bad id: %d", code)
	}
	waitTraced(t, c, node, 3)
}

// movedIDs are the mails the fake server moved.
func movedIDs(fm *fakeMonitor) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	var out []string
	for _, s := range fm.sent {
		if s["tool"] == "move" {
			out = append(out, s["ids"].([]string)...)
		}
	}
	return out
}

// A call folded in after the page drew the card is neither ticked nor
// left: the yes decides the others, the card keeps the words of what it
// decided, and the call waits on a new card of its own.
func TestCallFoldedInAfterDrawnWaitsOn(t *testing.T) {
	c, node, _ := mailView(t)
	l := func(id string) itemLabel { return itemLabel{ID: id, Title: "Mail " + id} }
	a := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m1"), l("m1"))
	b := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m2"), l("m2"))
	c.cardNow(cardCalls, a, "", refNode+":"+node)
	c.syncCards()
	xs, _ := c.cardsWhere(`ref = ?`, proposalRef(cardCalls, a))
	if len(xs) != 1 {
		t.Fatalf("cards %+v", xs)
	}
	card := xs[0].ID
	// Drawn with a and b; then another mail's call folds in.
	late := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m3"), l("m3"))
	c.syncCards()
	if batchOf(c, late) != a {
		t.Fatalf("the late call did not fold: %d", batchOf(c, late))
	}
	if _, err := c.decideCardChosen(context.Background(), card, []int64{a}, []int64{b}, "web"); err != nil {
		t.Fatal(err)
	}
	if x, _ := c.accessAskOf(late); x.State != startProposed {
		t.Errorf("the late call is %s", x.State)
	}
	old, err := c.card(card)
	if err != nil || old.State != cardApproved || old.Text != moveWords("1 élément")+", 1 laissé" || old.Ref == proposalRef(cardCalls, a) {
		t.Errorf("the decided card %+v, %v", old, err)
	}
	xs, _ = c.cardsWhere(`ref = ? AND state = ?`, proposalRef(cardCalls, a), cardPending)
	if len(xs) != 1 || xs[0].ID == card {
		t.Fatalf("no new card for the late call: %+v", xs)
	}
	x, p := c.liveCard(xs[0])
	if x.State != cardPending || p.Text("fr") != "Archiver 1 élément" {
		t.Errorf("the new card %+v: %q", x, p.Text("fr"))
	}
	if ch := p.choices("fr"); len(ch) != 1 || ch[0].ID != late {
		t.Errorf("the new card's calls %+v", ch)
	}
	if !slices.Contains(c.cardNodes(x.Ref), node) {
		t.Errorf("the new card is not the view's: %v", c.cardNodes(x.Ref))
	}
	waitTraced(t, c, node, 2)
}
