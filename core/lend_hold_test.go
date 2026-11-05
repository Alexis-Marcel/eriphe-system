package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// heldTask is a worker lent the mail's search outright and its move held
// back, started in the turn of my message.
func heldTask(t *testing.T, c *core, id string) {
	t.Helper()
	c.store.set(lentByUserKey(id), "1")
	c.store.insertTask(&TaskRow{ID: id, Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "Trie les mails",
		State: stateIdle, Tools: "mail.search_messages,mail.move", Resources: []ResourceRef{
			{Kind: resTool, Ref: "mail.search_messages", Access: accessRead},
			{Kind: resTool, Ref: "mail.move", Access: accessWrite, Hold: true},
		}})
}

var requestSeq int

// request makes the task ask for a tool once, and returns what the policy
// did with it: allowed, the user's, or left to the brain.
func request(t *testing.T, c *core, id, pid, tool string) string {
	t.Helper()
	requestSeq++
	ev := []EventRow{{Seq: requestSeq, Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": pid, "tool_name": "mcp__mail__" + tool, "input": map[string]any{}}}}
	c.store.addEvents(id, ev)
	c.applyPolicy(id, ev)
	items, _ := c.store.inbox(id)
	for _, it := range items {
		if it.Data["id"] == pid && it.Data["allow"] == true {
			return "allowed"
		}
	}
	if c.store.userOnly(id, pid) != "" {
		return "user"
	}
	return "brain"
}

// userTurn starts a turn of the brain on a message of mine.
func userTurn(t *testing.T, c *core, text string) {
	t.Helper()
	seq, _ := c.store.addMessage(roleUser, text, true)
	c.turn.start(int(seq), int(seq), 0)
}

// A tool lent held back waits for the user's yes on a card: answer_task
// only asks. Then it is lent as any other.
func TestHeldToolWaitsForRelease(t *testing.T) {
	c := newMailCore(t, accessFree) // Free: only the hold stops it
	heldTask(t, c, "t-1")
	if got := request(t, c, "t-1", "p-1", "search_messages"); got != "allowed" {
		t.Errorf("a read lent outright: %s", got)
	}
	if got := request(t, c, "t-1", "p-2", "move"); got != "user" {
		t.Errorf("a held tool before its release: %s, want the user's", got)
	}
	userTurn(t, c, "Ok, mets les alertes à la corbeille")
	res, err := c.releaseTools("t-1", []string{"mail.move"})
	if err != nil || !strings.Contains(res, "card") {
		t.Fatalf("release: %q, %v", res, err)
	}
	if tk, _ := c.store.task("t-1"); !tk.Resources[1].Hold {
		t.Fatal("released before the user's yes")
	}
	x := accessCard(t, c, "mail.move")
	if _, err := c.decideCard(context.Background(), x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if tk, _ := c.store.task("t-1"); tk.Resources[1].Hold {
		t.Error("the release is not kept with the task")
	}
	if got := request(t, c, "t-1", "p-3", "move"); got != "allowed" {
		t.Errorf("a released tool: %s", got)
	}
	if _, err := c.releaseTools("t-1", []string{"mail.move"}); err == nil {
		t.Error("a tool released twice")
	}
}

// Only what was lent held back is released, and a no releases nothing.
func TestReleaseRefused(t *testing.T) {
	c := newMailCore(t, "")
	heldTask(t, c, "t-1")
	for _, ref := range []string{"mail.send", "mail.search_messages", "mail"} {
		if _, err := c.releaseTools("t-1", []string{ref}); err == nil {
			t.Errorf("%s released, though it was not lent held back", ref)
		}
	}
	if _, err := c.releaseTools("t-1", []string{"mail.move"}); err != nil {
		t.Fatal(err)
	}
	x := accessCard(t, c, "mail.move")
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if tk, _ := c.store.task("t-1"); !tk.Resources[1].Hold {
		t.Error("a no released the tool")
	}
}

// A tainted turn may lend tools, held or not: what they may do is asked on
// a card. What is lent outright beside a held tool wins.
func TestHeldResources(t *testing.T) {
	c := newMailCore(t, "")
	userTurn(t, c, "Fais le point de mes mails")
	c.turn.taint("mail")
	held := runRequest{Resources: []ResourceRef{{Kind: resTool, Ref: "mail.search_messages"}, {Kind: resTool, Ref: "mail.move", Hold: true}}}
	if err := c.taintedResources(held); err != nil {
		t.Errorf("a held tool on a tainted turn: %v", err)
	}
	if _, err := normalizeResources(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients", Hold: true}}}); err == nil {
		t.Error("a folder held back")
	}
	lent, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resTool, Ref: "mail.move", Hold: true}, {Kind: resTool, Ref: "mail.move"}}})
	if err != nil || len(lent.List) != 1 || lent.List[0].Hold {
		t.Errorf("held and outright: %+v, %v", lent, err)
	}
	if !heldTool([]ResourceRef{{Kind: resTool, Ref: "mail.move", Hold: true}}, "mail", "move") ||
		heldTool([]ResourceRef{{Kind: resTool, Ref: "mail.move", Hold: true}, {Kind: resTool, Ref: "mail"}}, "mail", "move") {
		t.Error("heldTool")
	}
}
