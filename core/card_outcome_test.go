package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// A yes whose call fails: the card is decided, so it leaves the page's
// foot and its row, and its ⋯ menu lists it, failed, with the tool's own
// words (marked: its server is not trusted); the item stays as it was;
// the node's trace keeps why; and the brain is told once, as the user's
// act (the orb says what came of it), with why and what to do: say it
// plainly, propose it again if it makes sense. A yes that works is told
// too: « C'est fait ».
func TestFailedYesLeavesThePage(t *testing.T) {
	c, fm, node, r := mailViewOf(t)
	w := &web{c: c}
	keep(c, r.Task, "p1", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m2"}, "to": "archive"})
	endTurn(t, c, r.Task, "Un mail à archiver.")
	calls := cardsOf(c, cardCalls)
	if len(calls) != 1 {
		t.Fatalf("cards %+v", c.pendingProposals())
	}
	ref := proposalRef(cardCalls, calls[0].ID)
	if c.itemCards(node, "mails")["m2"] != ref {
		t.Fatalf("the card is not on its row: %v", c.itemCards(node, "mails"))
	}

	fm.mu.Lock()
	fm.moveFails = "folder archive: not allowed (403 Forbidden)"
	fm.mu.Unlock()
	if err := c.decideProposal(context.Background(), cardCalls, calls[0].ID, true); err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait()

	p, err := w.nodePageOf(node, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(p.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Error("the failed card stays at the page's foot")
	}
	i := slices.IndexFunc(p.Settled, func(v proposalView) bool { return v.Ref == ref })
	if i < 0 {
		t.Fatalf("the ⋯ menu does not list it: %+v", p.Settled)
	}
	if v := p.Settled[i]; v.Exec != callFailed || v.ExecWhy != callWhyError || !strings.Contains(v.ExecError, "403 Forbidden") || !v.ExecErrorExt {
		t.Errorf("settled %+v", v)
	}
	if card, ok := c.itemCards(node, "mails")["m2"]; ok {
		t.Errorf("the failed card stays on its row: %s", card)
	}
	if n := c.itemNotes(node, "mails")["m2"]; n.State == itemDone {
		t.Error("a failed call marked its item done")
	}
	if raw, _ := c.nodes.Trace(node); !strings.Contains(string(raw), "Échec") || !strings.Contains(string(raw), "403 Forbidden") {
		t.Errorf("the trace does not keep why:\n%s", raw)
	}
	if d, err := c.nodes.Read(node, 0); err != nil || !d.Node.Meta.External {
		t.Error("an untrusted tool's words in the trace, its node not marked external")
	}
	if why := c.store.msgTaint(lastUserAct(t, c)); why == "" {
		t.Error("an untrusted tool's words reach the brain untainted")
	}
	told := yesTold(t, c)
	if len(told) != 1 || !strings.Contains(told[0], "n'a pas marché") || !strings.Contains(told[0], "403 Forbidden") ||
		!strings.Contains(told[0], "propose-la à nouveau") || !strings.Contains(told[0], "Déplacer") {
		t.Fatalf("the brain was told %q", told)
	}
	// Told once, however often the card moves again.
	a := c.batchAsks(calls[0].ID)[0]
	c.callsCardRan(a)
	if n := len(yesTold(t, c)); n != 1 {
		t.Errorf("told %d times", n)
	}

	// A yes that works: « C'est fait », and its item done.
	fm.mu.Lock()
	fm.moveFails = ""
	fm.mu.Unlock()
	keep(c, r.Task, "p2", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m1"}, "to": "archive"})
	r = endTurn(t, c, r.Task, "Un autre.")
	calls = cardsOf(c, cardCalls)
	if len(calls) != 1 {
		t.Fatalf("cards %+v", c.pendingProposals())
	}
	if err := c.decideProposal(context.Background(), cardCalls, calls[0].ID, true); err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait()
	if told := yesTold(t, c); len(told) != 2 || !strings.Contains(told[1], "C'est fait") {
		t.Fatalf("the brain was told %q", told)
	}
}

// yesTold are the events that told the brain what came of the user's
// yes or no, oldest first.
// lastUserAct is the seq of the last event that told the brain what came
// of a yes.
func lastUserAct(t *testing.T, c *core) int {
	t.Helper()
	var seq int
	if err := c.store.db.QueryRow(`SELECT MAX(seq) FROM messages WHERE user_act = 1`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

func yesTold(t *testing.T, c *core) []string {
	t.Helper()
	rows, err := c.store.db.Query(`SELECT text FROM messages WHERE user_act = 1 ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}
