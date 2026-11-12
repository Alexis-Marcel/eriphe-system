package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// shownAtOnce checks that a card made outside a brain turn is in the
// conversation already, with no user message: under a message of its own
// written as the brain's, among the cards the event stream keeps up to
// date, and in the attention queue with its weight.
func shownAtOnce(t *testing.T, c *core, x convoCard, weight int) {
	t.Helper()
	if x.Seq <= 0 || x.State != cardPending {
		t.Fatalf("card not shown: %+v", x)
	}
	msgs, err := c.store.messagesAfter(x.Seq-1, 1)
	if err != nil || len(msgs) != 1 || msgs[0].Seq != x.Seq || msgs[0].Role != roleBrain || strings.TrimSpace(msgs[0].Text) == "" {
		t.Fatalf("no message of its own: %+v %v", msgs, err)
	}
	if msgs, _ := c.store.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE role = ?`, roleUser); len(msgs) != 0 {
		t.Fatalf("a user message: %+v", msgs)
	}
	w, err := newWeb(c, WebConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(w.liveCards(), func(y convoCard) bool { return y.ID == x.ID }) {
		t.Fatalf("card %d not among the live cards", x.ID)
	}
	if got := attentionOf(c, cardItemRef(x.ID)); got == nil || got.Weight != weight {
		t.Fatalf("attention item %+v, want weight %d", got, weight)
	}
}

func attentionOf(c *core, ref string) *attentionItem {
	for _, it := range c.attentionOpen() {
		if it.Ref == ref {
			return &it
		}
	}
	return nil
}

// Items about the same ref merge: the highest weight, the latest why, a say
// kept unless a new one comes; the heaviest come first.
func TestAttentionMergeByRef(t *testing.T) {
	c := startCore(t)
	at := time.Now().Add(-time.Minute)
	c.attend(attentionItem{Ref: "mail:1", Weight: 4, Why: "a bill due today", Say: "Ta facture est due.", Source: "skill:mail", Time: at})
	c.attend(attentionItem{Ref: "mail:1", Weight: 2, Why: "the bill, paid?", Source: "skill:mail"})
	c.attend(attentionItem{Ref: "session:3", Weight: 9, Why: "the plan waits", Source: "session:3"})
	c.attend(attentionItem{Ref: "agenda:2026-11-12", Weight: 1, Why: "a quiet day", Source: "skill:brief"})
	items := c.attentionOpen()
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Ref != "session:3" || items[0].Weight != 5 {
		t.Errorf("a weight is 1 to 5, the heaviest first: %+v", items[0])
	}
	m := attentionOf(c, "mail:1")
	if m.Weight != 4 || m.Why != "the bill, paid?" || m.Say != "Ta facture est due." || !m.Time.After(at) {
		t.Errorf("merged %+v", m)
	}
	c.attentionDone("mail:1")
	if attentionOf(c, "mail:1") != nil {
		t.Error("an item done stays")
	}
}

// A card enters the queue with a weight by its kind: one a task or a
// session waits on, 4; any other proposal, 3. Decided, it leaves.
func TestAttentionWeightByKind(t *testing.T) {
	for kind, want := range map[string]int{cardTaskStart: 4, cardAccess: 4, cardSession: 4, cardRule: 3, cardMemory: 3, cardRunCalls: 3} {
		if got := cardWeight(kind); got != want {
			t.Errorf("%s: weight %d, want %d", kind, got, want)
		}
	}
	tw, _ := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	x := cardOf(t, c, cardRule, r.ID)
	it := attentionOf(c, cardItemRef(x.ID))
	if it == nil || it.Weight != 3 || it.Source != byBrain || !strings.Contains(it.Why, "facture") {
		t.Fatalf("item %+v", it)
	}
	if !strings.Contains(c.attentionBlock(), cardItemRef(x.ID)) {
		t.Errorf("the brain does not read it: %q", c.attentionBlock())
	}
	if err := c.decideProposal(t.Context(), cardRule, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if attentionOf(c, cardItemRef(x.ID)) != nil || c.attentionBlock() != "" {
		t.Error("a card decided on its page stays in the queue")
	}
}
