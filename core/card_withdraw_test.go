package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Eriphe withdraws a card of his that the work made obsolete (#750): it
// is gone, by Eriphe, nothing of it runs, nobody is told, and its node no
// longer waits. It decides nothing for the user: what a task asks for
// itself and a task's start are refused, and a card no longer waiting too.
func TestBrainWithdrawsHisCard(t *testing.T) {
	c, node := outcomeCore(t)
	x := mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Le garage ferme lundi", Status: memProposed, SourceKind: srcTask})
	ref := proposalRef(cardMemory, x.ID)
	c.cardNow(cardMemory, x.ID, "", nodeRef(node))
	before := c.store.lastMessageSeq()

	out, isErr := callBrainTool(t, c, "card_withdraw", map[string]any{"ref": ref})
	if isErr || out["withdrawn"] != ref {
		t.Fatalf("card_withdraw: %v", out)
	}
	if y := cardOfRef(t, c, ref); y.State != cardGone || y.By != cardByEriphe {
		t.Errorf("the card: %s by %q", y.State, y.By)
	}
	if c.proposalOf(cardMemory, x.ID).Pending {
		t.Error("the memory still waits")
	}
	if got, _ := c.mem.memory(x.ID); got.Status == memActive {
		t.Error("withdrawing approved it")
	}
	if c.store.lastMessageSeq() != before {
		t.Error("withdrawing told someone")
	}
	if s := nodeStatusNow(t, c, node); s == nodeWaits {
		t.Error("its node still waits")
	}
	if _, isErr := callBrainTool(t, c, "card_withdraw", map[string]any{"ref": ref}); !isErr {
		t.Error("a card no longer waiting withdrawn again")
	}
	if _, isErr := callBrainTool(t, c, "card_withdraw", map[string]any{"ref": "nothing:1"}); !isErr {
		t.Error("no card's ref taken")
	}
}

// Only Eriphe's own: a task's grant, its start are the user's to answer.
func TestBrainWithdrawsOnlyHisOwn(t *testing.T) {
	for _, kind := range []string{cardAccess, cardTaskStart} {
		c, ref := outcomeMakers[kind](t)
		if _, err := c.brainWithdrawCard(context.Background(), ref); !errors.Is(err, errNotEriphes) {
			t.Errorf("%s: %v", kind, err)
		}
		if c.cardState(ref) != cardPending {
			t.Errorf("%s: no longer waits", kind)
		}
	}
}

// A skill's batch the work made obsolete: withdrawn, none of its calls
// runs, and the brain is not told the user refused.
func TestBrainWithdrawsAStaleBatch(t *testing.T) {
	c, ref := outcomeMakers[cardCalls](t)
	before := c.store.lastMessageSeq()
	if _, err := c.brainWithdrawCard(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	_, id, _ := parseProposalRef(ref)
	for _, a := range c.batchAsks(id) {
		if a.State != startRejected {
			t.Errorf("call %d: %s", a.ID, a.State)
		}
		if s, _ := c.callState(a.ID); s != "" {
			t.Errorf("call %d ran: %s", a.ID, s)
		}
	}
	if told := toldSince(t, c, before); len(told) != 0 {
		t.Errorf("told %+v", told)
	}
}

// A call of the brain's own, kept on a card: Eriphe's to withdraw.
func TestBrainWithdrawsHisCall(t *testing.T) {
	c := newMailCore(t, "")
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateWaitingPermission})
	brainCalls(t, c, "p-1", "mcp__mail__publish_page", map[string]any{"repo": "portfolio"})
	ps := cardsOf(c, cardAccess)
	if len(ps) != 1 {
		t.Fatalf("cards %+v", ps)
	}
	ref := proposalRef(cardAccess, ps[0].ID)
	out, err := c.brainWithdrawCard(context.Background(), ref)
	if err != nil || !strings.Contains(out["note"].(string), "nothing of it runs") {
		t.Fatalf("%v %v", out, err)
	}
	if a, _ := c.accessAskOf(ps[0].ID); a.State != startRejected {
		t.Errorf("the call: %s", a.State)
	}
}
