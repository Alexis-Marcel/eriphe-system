package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// moreMails puts mails m1 to m<n> in the faked server, and reads the
// node's source again: what its list shows now.
func moreMails(t *testing.T, c *core, fm *fakeMonitor, node string, n int) {
	t.Helper()
	fm.mu.Lock()
	fm.got = nil
	for i := 1; i <= n; i++ {
		fm.got = append(fm.got, map[string]any{"id": fmt.Sprintf("m%d", i), "subject": fmt.Sprintf("Mail %d", i), "from": "news@example.org"})
	}
	fm.mu.Unlock()
	c.readNode(context.Background(), node, []string{"mails"}, time.Now())
}

// otherRun is another run of the node's skill, over: what a monitor's
// next mail starts.
func otherRun(t *testing.T, c *core, r *skillRun) int64 {
	t.Helper()
	res, err := c.store.db.Exec(`INSERT INTO skill_runs (skill, sha, task, state, created, ended) VALUES (?, ?, ?, ?, ?, ?)`,
		r.Skill, r.SHA, r.Task, skillRunDone, time.Now().Unix(), time.Now().Unix())
	id, err := rowsID(res, err)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// waitingCalls are the calls cards that wait, and each one's items.
func waitingCalls(c *core) map[int64][]string {
	out := map[int64][]string{}
	for _, p := range cardsOf(c, cardCalls) {
		for _, a := range c.batchAsks(p.ID) {
			if a.State == startProposed {
				out[p.ID] = append(out[p.ID], choiceItems(a)...)
			}
		}
		slices.Sort(out[p.ID])
	}
	return out
}

// Three runs of the node's skill and the brain each propose to archive
// mails of « Mes mails », some the same: one demand, on the rows of all
// the mails, each once; its Oui moves each mail once.
func TestOneDemandForRunsAndBrain(t *testing.T) {
	c, fm, node, r := mailViewOf(t)
	moreMails(t, c, fm, node, 5)
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateWaitingPermission})
	move := func(ids ...string) map[string]any { return moveMail("sam@example.org", "archive", ids...) }

	keep(c, r.Task, "p1", "mcp__monitor__move", move("m1"))
	items, _ := c.store.inbox(r.Task)
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0].Data["message"]), "sur les lignes") {
		t.Fatalf("the run is not told its call waits on its rows: %+v", items)
	}
	endTurn(t, c, r.Task, "Un mail à archiver.")
	for _, ids := range [][]string{{"m2", "m3"}, {"m3", "m4"}} {
		run := otherRun(t, c, r)
		c.keepCall(context.Background(), keeper{task: r.Task, pid: fmt.Sprint("p", run), run: run, node: node}, "mcp__monitor__move", move(ids...))
		c.foldCalls() // as its end does (skill_runs.go)
	}
	c.turn.about(node)
	brainCallOn(t, c, "p-b", "mcp__monitor__move", move("m4", "m5", "m1"))

	got := waitingCalls(c)
	if len(got) != 1 {
		t.Fatalf("demands %v", got)
	}
	var batch int64
	for b, ids := range got {
		batch = b
		if !slices.Equal(ids, []string{"m1", "m2", "m3", "m4", "m5"}) {
			t.Errorf("its items %v", ids)
		}
	}
	ref := proposalRef(cardCalls, batch)
	cards := c.itemCards(node, "mails")
	for i := 1; i <= 5; i++ {
		if cards[fmt.Sprintf("m%d", i)] != ref {
			t.Errorf("rows %v, want %s on each", cards, ref)
		}
	}
	if got := c.proposalOf(cardCalls, batch).Text("fr"); got != moveWords("5 mails") {
		t.Errorf("its words %q", got)
	}
	// The brain is told where its call waits: the card it is on.
	brain, _ := c.store.inbox("b-1")
	if len(brain) == 0 || !strings.Contains(fmt.Sprint(brain[len(brain)-1].Data["message"]), ref) {
		t.Errorf("the brain is not told its card: %+v", brain)
	}
	xs, _ := c.cardsWhere(`ref = ? AND state = ?`, ref, cardPending)
	if len(xs) != 1 {
		t.Fatalf("cards %+v", xs)
	}
	if _, err := c.decideCard(context.Background(), xs[0].ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait()
	moved := movedIDs(fm)
	slices.Sort(moved)
	if !slices.Equal(moved, []string{"m1", "m2", "m3", "m4", "m5"}) {
		t.Errorf("moved %v: each mail once", moved)
	}
	c.live.wg.Wait()
}

// A call on 6 mails and one on 2 others do the same: one card of 8.
func TestFoldSixAndTwo(t *testing.T) {
	c, node, _ := mailView(t)
	labels := func(ids ...string) []itemLabel {
		var out []itemLabel
		for _, id := range ids {
			out = append(out, itemLabel{ID: id, Title: "Mail " + id, Source: "mails"})
		}
		return out
	}
	six := []string{"m1", "m2", "m3", "m4", "m5", "m6"}
	a := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", six...), labels(six...)...)
	b := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m7", "m8"), labels("m7", "m8")...)
	c.foldCalls()
	if batchOf(c, b) != a {
		t.Fatalf("the call on 2 did not fold: %d on %d", b, batchOf(c, b))
	}
	if got := c.proposalOf(cardCalls, a).Text("fr"); got != moveWords("8 mails") {
		t.Errorf("text %q", got)
	}
}

// A mail gone from the source leaves the demand on it; a demand left with
// no mail is withdrawn, by code: nothing runs, nobody is told a no.
func TestStaleItemsLeaveTheirDemand(t *testing.T) {
	c, fm, node, _ := mailViewOf(t)
	moreMails(t, c, fm, node, 3)
	l := func(id string) itemLabel { return itemLabel{ID: id, Title: "Mail " + id, Source: "mails"} }
	two := askOn(t, c, node, "mcp__monitor__move", moveMail("sam@example.org", "archive", "m1", "m2"), l("m1"), l("m2"))
	one := askOn(t, c, node, "mcp__monitor__send", map[string]any{"id": "m3", "body": "Merci"}, l("m3"))
	c.store.db.Exec(`UPDATE access_asks SET call = ? WHERE id = ?`, `mcp__monitor__move [{"name":"ids","value":"[\"m1\",\"m2\"]"}]`, two) // its parameters as its card shows them
	for _, id := range []int64{two, one} {
		c.cardNow(cardCalls, id, "", nodeRef(node))
	}
	before := c.store.lastMessageSeq()

	moreMails(t, c, fm, node, 1) // m2 and m3 left the list

	a, _ := c.accessAskOf(two)
	if a.State != startProposed || !slices.Equal(choiceItems(a), []string{"m1"}) || a.Input != `{"account":"sam@example.org","ids":["m1"],"to":"archive"}` ||
		!strings.Contains(a.Call, `[\"m1\"]`) || strings.Contains(a.Call, "m2") {
		t.Errorf("the demand on m1 and m2: %+v", a)
	}
	if got := c.proposalOf(cardCalls, two).Text("fr"); got != moveWords("1 mail") {
		t.Errorf("its words %q", got)
	}
	if x, _ := c.accessAskOf(one); x.State != startRejected {
		t.Errorf("the demand on m3 alone: %s", x.State)
	}
	if got := c.cardState(proposalRef(cardCalls, one)); got != cardGone {
		t.Errorf("its card: %s", got)
	}
	if s, _ := c.callState(one); s != "" {
		t.Errorf("it ran: %s", s)
	}
	if told := toldSince(t, c, before); len(told) != 0 {
		t.Errorf("told %+v", told)
	}
	if cards := c.itemCards(node, "mails"); cards["m1"] != proposalRef(cardCalls, two) || len(cards) != 1 {
		t.Errorf("rows %v", cards)
	}
}

// A run's call on no item of its node: its card is a block of the node,
// where the run places it; one it does not place, code puts at the end
// of the page. Placed or not, it is not at the page's foot.
func TestDemandOnNoItemIsABlock(t *testing.T) {
	c, node, r := mailView(t)
	keep(c, r.Task, "p1", "mcp__monitor__rollback", map[string]any{"version": "1.0"})
	keep(c, r.Task, "p2", "mcp__monitor__rollback", map[string]any{"version": "2.0"})
	var placed, unplaced int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p1'`).Scan(&placed)
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p2'`).Scan(&unplaced)
	ref, other := proposalRef(cardCalls, placed), proposalRef(cardCalls, unplaced)
	items, _ := c.store.inbox(r.Task)
	if len(items) != 2 || !strings.Contains(fmt.Sprint(items[0].Data["message"]), `{"card": "`+ref+`"}`) {
		t.Fatalf("the run is not told how to place it: %+v", items)
	}
	// The run places the first under its plan, before its turn ends.
	mustNodes(t, c, r.Task, "node_edit", map[string]any{"expect": lastHash(t, c, node), "block": "revenir", "value": map[string]any{"card": ref}})
	endTurn(t, c, r.Task, "Deux retours en arrière proposés.")

	d, _ := c.nodes.Read(node, 0)
	var cards []string
	for _, b := range d.Node.Blocks() {
		if b.Card != "" {
			cards = append(cards, b.Name+"="+b.Card)
		}
	}
	if !slices.Equal(cards, []string{"revenir=" + ref, "carte_calls_" + fmt.Sprint(unplaced) + "=" + other}) {
		t.Errorf("the node's card blocks %v", cards)
	}
	pg, _ := (&web{c: c}).nodePageOf(node, 0, "fr")
	for _, want := range []string{ref, other} {
		if !slices.ContainsFunc(pg.Parts, func(p pagePart) bool { return p.Block != nil && p.Block.Proposal != nil && p.Block.Proposal.Ref == want }) {
			t.Errorf("%s is not drawn in the page", want)
		}
		if slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == want }) {
			t.Errorf("%s is at the foot", want)
		}
	}
	// Placed once: its next pass changes nothing.
	v := d.Version.N
	c.placeDemands()
	if d, _ := c.nodes.Read(node, 0); d.Version.N != v {
		t.Errorf("placed again: version %d after %d", d.Version.N, v)
	}
}

// At start, the demands an older Eriphe kept fold under these rules:
// the brain's access card and a run's card of calls become calls cards,
// those that do the same fold, each item once; one whose mail is gone is
// withdrawn; nothing runs, and a second start changes nothing.
func TestMigrateDemands(t *testing.T) {
	c, fm, node, r := mailViewOf(t)
	moreMails(t, c, fm, node, 3)
	endTurn(t, c, r.Task, "Rien.")
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateIdle})
	old := func(task string, run int64, input map[string]any, labels ...itemLabel) int64 {
		t.Helper()
		raw, _ := json.Marshal(input)
		ls := ""
		if len(labels) > 0 {
			b, _ := json.Marshal(labels)
			ls = string(b)
		}
		res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, server, category, call, input, created, run, node, source, labels)
			VALUES (?, 'monitor', 'change', ?, ?, ?, ?, ?, ?, ?)`, task, `mcp__monitor__move [{"name":"ids","value":"x"}]`, string(raw), time.Now().Unix(), run, "", "", ls)
		id, err := rowsID(res, err)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	l := func(id string) itemLabel { return itemLabel{ID: id, Title: "Mail " + id, Source: "mails"} }
	move := func(ids ...string) map[string]any { return moveMail("sam@example.org", "archive", ids...) }
	// The brain's, on m1, its access card in the node.
	brain := old("b-1", 0, move("m1"), l("m1"))
	c.store.db.Exec(`UPDATE access_asks SET node = ?, source = 'mails' WHERE id = ?`, node, brain)
	c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES (?, 'access', 'Déplacer', 1)`, proposalRef(cardAccess, brain))
	c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, ?, 1)`, node, proposalRef(cardAccess, brain))
	// The run's two, on m1 and m2, then m3: one runcalls card.
	first := old(r.Task, r.ID, move("m1", "m2"))
	second := old(r.Task, r.ID, move("m3"))
	c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES (?, 'runcalls', 'Le skill propose 2 action(s)', 1)`, fmt.Sprint("runcalls:", r.ID))
	c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, ?, 1)`, node, fmt.Sprint("runcalls:", r.ID))
	// Yesterday's, on a mail no longer listed.
	gone := old("b-1", 0, move("m9"), l("m9"))
	c.store.db.Exec(`UPDATE access_asks SET node = ?, source = 'mails' WHERE id = ?`, node, gone)

	c.reconcileCards()

	got := waitingCalls(c)
	if len(got) != 1 {
		t.Fatalf("demands %v", got)
	}
	for batch, ids := range got {
		if !slices.Equal(ids, []string{"m1", "m2", "m3"}) {
			t.Errorf("its items %v", ids)
		}
		if batch != brain {
			t.Errorf("the oldest card is %d, not %d", batch, brain)
		}
	}
	if xs, _ := c.cardsWhere(`ref = ? AND state = ?`, proposalRef(cardCalls, brain), cardPending); len(xs) != 1 {
		t.Errorf("the brain's card did not become calls: %+v", xs)
	}
	if xs, _ := c.cardsWhere(`kind = 'runcalls' AND state = ?`, cardPending); len(xs) != 0 {
		t.Errorf("a runcalls card waits: %+v", xs)
	}
	if xs, _ := c.cardsWhere(`kind = ? AND state = ?`, cardAccess, cardPending); len(xs) != 0 {
		t.Errorf("an access card waits: %+v", xs)
	}
	for _, id := range []int64{first, second} {
		if a, _ := c.accessAskOf(id); a.Node != node {
			t.Errorf("call %d is not the node's: %+v", id, a)
		}
	}
	if a, _ := c.accessAskOf(gone); a.State != startRejected {
		t.Errorf("the call on a gone mail: %s", a.State)
	}
	if moved := movedIDs(fm); len(moved) != 0 {
		t.Errorf("ran %v", moved)
	}
	if cards := c.itemCards(node, "mails"); cards["m1"] != proposalRef(cardCalls, brain) || cards["m3"] != proposalRef(cardCalls, brain) {
		t.Errorf("rows %v", cards)
	}
	// A second start: the same.
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM convo_cards`).Scan(&n)
	c.reconcileCards()
	if again := waitingCalls(c); fmt.Sprint(again) != fmt.Sprint(got) {
		t.Errorf("a second start: %v, was %v", again, got)
	}
	var m int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM convo_cards`).Scan(&m)
	if m != n {
		t.Errorf("cards %d, were %d", m, n)
	}
}
