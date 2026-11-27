package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// heldCall keeps a call of a task's on a card, as a write held for the
// user's yes.
func heldCall(t *testing.T, c *core, input string) int64 {
	t.Helper()
	id := c.newAccessAsk(accessQuestion{TaskID: "t-agenda", Need: accessNeed{Server: "agenda", Category: catChange},
		Call: "mcp__agenda__add_entry []", Input: input})
	if id == 0 {
		t.Fatal("no access ask")
	}
	return id
}

// Every way a proposal is decided says so to the workspace: a card's button, one found decided elsewhere; and a new card.
func TestProposalMoves(t *testing.T) {
	tw, _ := newIntentsWeb(t)
	c := tw.c
	_, seq, ch := c.watchProposals(0)
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures ?")
	ref := proposalRef(cardRule, r.ID)
	moved := func(what string) {
		t.Helper()
		select {
		case <-ch:
		default:
			t.Fatalf("%s: no move", what)
		}
		var ev proposalsEvent
		ev, seq, ch = c.watchProposals(seq)
		if !slices.Contains(ev.Refs, ref) || ev.All {
			t.Fatalf("%s: %+v", what, ev)
		}
	}
	moved("a new card")

	// Decided where no card is told (its own page): found when read.
	r2 := proposedRule(t, c, "banque", "")
	brainSays(c, "Et celle-là ?")
	ref = proposalRef(cardRule, r2.ID)
	moved("another card")
	if _, err := c.approveNotifyRule(r2.ID); err != nil {
		t.Fatal(err)
	}
	c.liveCard(cardOf(t, c, cardRule, r2.ID))
	moved("found decided")

	// A card linked to a node names it: its page reads it again.
	c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES ('n1', ?, 1)`, ref)
	c.proposalMoved(ref)
	if ev, _, _ := c.watchProposals(seq); !slices.Contains(ev.Refs, ref) || !slices.Equal(ev.Nodes, []string{"n1"}) {
		t.Errorf("a node's card: %+v", ev)
	}

	// A stream far behind reads everything again.
	for i := 0; i < proposalFeedMax+2; i++ {
		c.proposalMoved("rule:1")
	}
	if ev, _, _ := c.watchProposals(seq); !ev.All {
		t.Errorf("behind: %+v", ev)
	}
}

// The workspace's stream sends the refs that moved, for the blocks that
// show them to read them again.
func TestProposalsEvent(t *testing.T) {
	tw, csrf := newIntentsWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures ?")
	ref := proposalRef(cardRule, r.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan proposalsEvent, 10)
	ready := make(chan struct{})
	var snapshot sync.Once
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
				if event == "node" {
					snapshot.Do(func() { close(ready) })
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "proposals" {
				var ev proposalsEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					events <- ev
				}
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot")
	}
	// At first, everything: what the page shows may have been decided
	// while it was away.
	select {
	case ev := <-events:
		if !ev.All {
			t.Fatalf("first event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no first proposals event")
	}
	x := cardOf(t, c, cardRule, r.ID)
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/no", x.ID), url.Values{}, map[string]string{"X-CSRF-Token": csrf}); code != 200 {
		t.Fatalf("no: %d", code)
	}
	select {
	case ev := <-events:
		if !slices.Contains(ev.Refs, ref) {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no proposals event")
	}
}
