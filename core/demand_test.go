package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// moreMails puts mails m1 to m<n> in the faked server, and the page
// references them, as its skill writes it.
func moreMails(t *testing.T, c *core, fm *fakeMonitor, node string, n int) {
	t.Helper()
	fm.mu.Lock()
	fm.got = nil
	for i := 1; i <= n; i++ {
		fm.got = append(fm.got, map[string]any{"id": fmt.Sprintf("m%d", i), "subject": fmt.Sprintf("Mail %d", i), "from": "news@example.org"})
	}
	fm.mu.Unlock()
	writeBody(t, c, node, mailsBody(n))
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
	cards := rowCards(c, node)
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
}

// A call on 6 mails and one on 2 others do the same: one card of 8.
func TestFoldSixAndTwo(t *testing.T) {
	c, node, _ := mailView(t)
	labels := func(ids ...string) []itemLabel {
		var out []itemLabel
		for _, id := range ids {
			out = append(out, itemLabel{ID: id, Title: "Mail " + id, Server: "monitor"})
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

// A call on no item waits in its node: its keeper is told how to place
// it in a line of the page (card:), and one it places is drawn there, not
// at the foot; one it does not place waits at the foot, code writing
// nothing in the page.
func TestDemandOnNoItemIsPlacedByALine(t *testing.T) {
	c, node, r := mailView(t)
	keep(c, r.Task, "p1", "mcp__monitor__rollback", map[string]any{"version": "1.0"})
	keep(c, r.Task, "p2", "mcp__monitor__rollback", map[string]any{"version": "2.0"})
	var placed, unplaced int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p1'`).Scan(&placed)
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p2'`).Scan(&unplaced)
	ref, other := proposalRef(cardCalls, placed), proposalRef(cardCalls, unplaced)
	items, _ := c.store.inbox(r.Task)
	if len(items) != 2 || !strings.Contains(fmt.Sprint(items[0].Data["message"]), "(card:"+ref+")") {
		t.Fatalf("the run is not told how to place it: %+v", items)
	}
	// The run places the first under its plan, before its turn ends.
	mustNodes(t, c, r.Task, "node_edit", map[string]any{"old": "Les mails reçus.", "new": "Les mails reçus.\n\n[Revenir à la 1.0](card:" + ref + ")"})
	before, _ := c.nodes.Read(node, 0)
	endTurn(t, c, r.Task, "Deux retours en arrière proposés.")
	if d, _ := c.nodes.Read(node, 0); d.Version.N != before.Version.N {
		t.Errorf("code wrote in the page: version %d after %d", d.Version.N, before.Version.N)
	}
	pg, _ := (&web{c: c}).nodePageOf(node, 0, "fr")
	if !slices.Equal(pg.Placed, []string{ref}) {
		t.Errorf("placed %v, want %s", pg.Placed, ref)
	}
	if slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("%s is at the foot too", ref)
	}
	if !slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == other }) {
		t.Errorf("%s is not at the foot: %+v", other, pg.Cards)
	}
}
