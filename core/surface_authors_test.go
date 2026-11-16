package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func oneText(text string) []map[string]any {
	return []map[string]any{{"id": "root", "component": "Text", "text": text}}
}

// An attention item names a thread, never a surface: what a surface
// shows is its thread's, and a thread's tile rises with thread:<id>.
func TestNoSurfaceAttentionRef(t *testing.T) {
	if validAttentionRef("surface:revue-du-jour") {
		t.Fatal("a surface ref is an attention ref")
	}
	if kept, dropped := decodeSignals([]byte(`[{"refs": ["surface:revue-du-jour"], "weight": 2, "why": "La revue est prête"}]`)); len(kept) != 1 || len(kept[0].Refs) != 0 || len(dropped) != 1 {
		t.Fatalf("signal %v %v", kept, dropped)
	}
}

// At start, what no code wrote goes: a surface an assistant, a task or a
// skill wrote before only threads were shown; a thread's and the queue's
// stay.
func TestDropFreeSurfaces(t *testing.T) {
	c, _ := surfaceCore(t)
	comps := []byte(`[{"id":"root","component":"Text","text":"x"}]`)
	for _, w := range []struct{ author, id string }{
		{"brain", "revue"}, {"skill:arrivee", "arrivee"}, {"task:t-1", "page-site"}, {"lead:3", "suivi"},
		{threadAuthor(1), threadSurfaceID(1)}, {surfaceAuthorCore, "q-decide"},
	} {
		if _, _, err := c.fixtureSurface(w.author, surfaceIn{ID: w.id, Title: "x", Components: comps}); err != nil {
			t.Fatal(w, err)
		}
	}
	c.dropFreeSurfaces()
	all, _ := c.surfaces()
	var left []string
	for _, s := range all {
		left = append(left, s.ID)
	}
	slices.Sort(left)
	if !slices.Equal(left, []string{"q-decide", threadSurfaceID(1)}) {
		t.Fatalf("left %v", left)
	}
}

// Proposals waiting without a card in front of the user enter the queue at
// their card's weight: the brain's turn lists them, no answer nor thread
// breakpoint delivers the light ones, and the user's return does, a few
// cards at a time, under one message.
func TestQueuedProposalsInTheQueue(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	for i := range 4 {
		mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: fmt.Sprintf("Sam aime le thé numéro %d", i), Status: memProposed, SourceKind: srcTask})
	}
	mustIntend(t, c.mem, &Intention{Text: "Appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	c.attendQueuedCards()
	items := c.attentionOpen()
	if len(items) != 5 {
		t.Fatalf("queue %+v", items)
	}
	weights := map[string]int{}
	for _, it := range items {
		if !it.Held {
			t.Errorf("item %+v", it)
		}
		weights[strings.SplitN(it.Why, " : ", 2)[1]] = it.Weight
	}
	if weights["Sam aime le thé numéro 0"] != cardWeight(cardMemory) || weights["Appeler le garage"] != weightBrief {
		t.Fatalf("weights %v", weights)
	}
	// The workspace shows them, as any item of the queue: a Proposal each,
	// in its group or alone.
	c.syncQueueSurfaces()
	for _, e := range c.queuePlan(time.Now()) {
		if sf, err := c.surface(e.surface); err != nil || !strings.Contains(string(sf.Components), "Proposal") {
			t.Errorf("no surface for %s: %v", e.it.Ref, err)
		}
	}
	c.attendQueuedCards() // again: nothing doubles
	if n := len(c.attentionOpen()); n != 5 {
		t.Fatalf("%d items", n)
	}
	if b := c.attentionBlock(); !strings.Contains(b, "Appeler le garage") {
		t.Fatalf("the brain's turn does not list them: %q", b)
	}
	now := c.notifyTime()
	for _, at := range []string{breakAnswer, breakThread, breakCap, breakNow} {
		if n := c.attentionBreak(at, now); n != 0 {
			t.Fatalf("%s delivered %d", at, n)
		}
	}
	if n := c.attentionBreak(breakReturn, now); n != attentionCardsPerBreak {
		t.Fatalf("the return delivered %d", n)
	}
	if xs, _ := c.cardsWhere(`seq > 0`); len(xs) != attentionCardsPerBreak {
		t.Fatalf("cards under the brief: %+v", xs)
	}
	if n := c.attentionBreak(breakReturn, now.Add(time.Hour)); n != 2 {
		t.Fatalf("the next return delivered %d", n)
	}
}
