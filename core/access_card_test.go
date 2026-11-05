package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// accessCard is the one card waiting whose text says what.
func accessCard(t *testing.T, c *core, what string) convoCard {
	t.Helper()
	xs, err := c.cardsWhere(`kind = ? AND state = ? ORDER BY id`, cardAccess, cardPending)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		if _, p := c.liveCard(x); strings.Contains(p.Text("en"), what) {
			return x
		}
	}
	t.Fatalf("no card says %q in %+v", what, xs)
	return convoCard{}
}

func accessCards(t *testing.T, c *core) int {
	t.Helper()
	xs, err := c.cardsWhere(`kind = ? AND state = ?`, cardAccess, cardPending)
	if err != nil {
		t.Fatal(err)
	}
	return len(xs)
}

// waitingTask is a worker lent the whole mail, started cleanly.
func waitingTask(c *core, id string) {
	c.store.set(lentByUserKey(id), "1")
	c.store.insertTask(&TaskRow{ID: id, Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "Trie les mails",
		State: stateWaitingPermission, Tools: "mail", Resources: []ResourceRef{{Kind: resTool, Ref: "mail", Access: accessWrite}}})
}

func answeredAllow(t *testing.T, c *core, id, pid string) (bool, bool) {
	t.Helper()
	items, _ := c.store.inbox(id)
	for _, it := range items {
		if it.Data["id"] == pid {
			return true, it.Data["allow"] == true
		}
	}
	return false, false
}

// One card per task and category: the calls that wait on it are answered
// with it, and the next ones do not ask.
func TestFirstCallCardGrantsTheCategory(t *testing.T) {
	c := newMailCore(t, "")
	ctx := context.Background()
	waitingTask(c, "t-1")
	if got := request(t, c, "t-1", "p-1", "move"); got != "user" {
		t.Fatalf("first call: %s", got)
	}
	if got := request(t, c, "t-1", "p-2", "move"); got != "user" {
		t.Fatalf("second call: %s", got)
	}
	if n := accessCards(t, c); n != 1 {
		t.Fatalf("%d cards for one category", n)
	}
	x := accessCard(t, c, "change in mail")
	if x.Seq == 0 {
		t.Fatal("the card waits for a message that will not come")
	}
	if _, err := c.decideCard(ctx, x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []string{"p-1", "p-2"} {
		if told, allow := answeredAllow(t, c, "t-1", pid); !told || !allow {
			t.Errorf("%s: told %v, allow %v", pid, told, allow)
		}
	}
	if got := request(t, c, "t-1", "p-3", "move"); got != "allowed" {
		t.Errorf("a call after the yes: %s", got)
	}
	// Another category asks on its own card, and another task starts over.
	if got := request(t, c, "t-1", "p-4", "send"); got != "user" {
		t.Errorf("another category: %s", got)
	}
	waitingTask(c, "t-2")
	if got := request(t, c, "t-2", "p-5", "move"); got != "user" {
		t.Errorf("another task: %s", got)
	}
	// It is kept and shown.
	if g := c.store.accessGrants("t-1"); len(g) != 1 || g[0].Category != catChange {
		t.Errorf("grants = %+v", g)
	}
	var by string
	c.store.db.QueryRow(`SELECT by FROM decisions WHERE task_id = 't-1' AND tool = 'access mail.change'`).Scan(&by)
	if by != byUser {
		t.Errorf("the approval is not on record: %q", by)
	}
}

func TestCardNoRefusesWhatWaits(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	request(t, c, "t-1", "p-1", "move")
	if _, err := c.decideCard(context.Background(), accessCard(t, c, "change").ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if told, allow := answeredAllow(t, c, "t-1", "p-1"); !told || allow {
		t.Errorf("told %v, allow %v", told, allow)
	}
	if len(c.store.accessGrants("t-1")) != 0 {
		t.Error("a no granted the category")
	}
}

// The brain cannot answer what a card asks.
func TestBrainCannotAnswerAccess(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	request(t, c, "t-1", "p-1", "move")
	yes := true
	if _, err := c.answer(context.Background(), "t-1", "p-1", "", &yes, byBrain); err == nil {
		t.Fatal("the brain answered a card's request")
	}
}

// The raw request is not shown beside its card, and the brain is told not
// to relay it.
func TestCardReplacesTheRawRequest(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	request(t, c, "t-1", "p-1", "move")
	if !c.accessAsked("t-1", "p-1") {
		t.Error("the request is not known to wait on a card")
	}
	if c.accessAsked("t-1", "p-other") {
		t.Error("another request waits on the card")
	}
}

// A plain yes answers the card just above; on a tainted one, buttons only.
func TestAccessCardAnsweredInWords(t *testing.T) {
	c := newMailCore(t, "")
	ctx := context.Background()
	waitingTask(c, "t-1")
	request(t, c, "t-1", "p-1", "move")
	seq, _ := c.store.addMessage(roleUser, "oui", true)
	if x, ok := c.resolveByText(ctx, int(seq)); !ok || x.State != cardApproved {
		t.Fatalf("a plain yes: %+v, %v", x, ok)
	}
	waitingTask(c, "t-2")
	c.store.set(taskTaintKey("t-2"), "started on a tainted turn: mail.read")
	request(t, c, "t-2", "p-2", "move")
	seq, _ = c.store.addMessage(roleUser, "oui", true)
	if _, ok := c.resolveByText(ctx, int(seq)); ok {
		t.Fatal("a plain yes answered a tainted card")
	}
}

// What stops a task goes to Telegram, though no message of its went there;
// another card waits for its message. "Alerts only" sends none.
func TestBlockingCardsGoToTelegram(t *testing.T) {
	c, _, f := telegramCore(t)
	linked(c)
	c.tg.restart()
	waitingTask(c, "t-1")
	c.newAccessAsk(accessQuestion{TaskID: "t-1", Permission: "p-1", Need: accessNeed{"mail", catChange, ""}})
	seq, _ := c.store.addMessage(roleBrain, "Je lance la tâche.", true)
	c.store.updateTask("t-1", map[string]any{"state": statePending, "desired": desiredHeld})
	if err := c.holdTask(&TaskRow{ID: "t-1"}, []startNeed{{Server: "mail", Category: catSend}}, nil); err != nil {
		t.Fatal(err)
	}
	c.attachTurnCards(int(seq))
	c.store.set(tgDetailKey, detailAlert)
	c.telegramCards()
	c.tg.pending.Wait()
	if sent := f.sentMessages(); len(sent) != 0 {
		t.Fatalf("alerts only sent %v", sent)
	}
	c.store.set(tgDetailKey, detailFull)
	c.telegramCards()
	c.tg.pending.Wait()
	sent := f.sentMessages()
	if len(sent) != 2 || !strings.Contains(fmt.Sprint(sent[0]["reply_markup"]), "pc:") {
		t.Fatalf("sent %v", sent)
	}
}

// At launch: a lend of On approval categories holds the task on its start's
// card, and a yes grants them. A read asks nothing.
func TestLaunchAsksWhatIsLent(t *testing.T) {
	c := newMailCore(t, "")
	needs := c.accessStartNeeds([]ResourceRef{{Kind: resTool, Ref: "mail"}}, true)
	var got []string
	for _, n := range needs {
		got = append(got, n.Category)
	}
	if s := strings.Join(got, ","); s != "change,send" {
		t.Fatalf("needs = %s", s)
	}
	if n := c.accessStartNeeds([]ResourceRef{{Kind: resTool, Ref: "mail.search_messages"}}, true); len(n) != 0 {
		t.Errorf("a read: %+v", n)
	}
	if n := c.accessStartNeeds([]ResourceRef{{Kind: resTool, Ref: "mail.move", Hold: true}}, true); len(n) != 0 {
		t.Errorf("a held tool: %+v", n)
	}
	if s := startText(needs, "en"); !strings.Contains(s, "change, send in mail") {
		t.Errorf("text = %s", s)
	}
	waitingTask(c, "t-1")
	c.store.updateTask("t-1", map[string]any{"state": statePending, "desired": desiredHeld})
	if err := c.holdTask(&TaskRow{ID: "t-1"}, needs, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.decideTaskStart(1, true); err != nil {
		t.Fatal(err)
	}
	c.store.updateTask("t-1", map[string]any{"state": stateWaitingPermission})
	if got := request(t, c, "t-1", "p-1", "move"); got != "allowed" {
		t.Errorf("a category granted at launch: %s", got)
	}
}

// Free above Read needs a clean start: otherwise it is asked at launch too.
func TestLaunchOfFreeFollowsTheStart(t *testing.T) {
	c := newMailCore(t, accessFree)
	lent := []ResourceRef{{Kind: resTool, Ref: "mail"}}
	if n := c.accessStartNeeds(lent, true); len(n) != 0 {
		t.Errorf("a clean start: %+v", n)
	}
	if n := c.accessStartNeeds(lent, false); len(n) == 0 {
		t.Error("a tainted start asks nothing")
	}
}

// The brain: one card per call, its yes for that call alone.
func TestBrainCardIsForOneCall(t *testing.T) {
	c := newMailCore(t, "")
	ctx := context.Background()
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "box", Runtime: "claude-code", State: stateWaitingPermission})
	in := map[string]any{"id": "42"}
	c.store.addEvents("b-1", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": "p-1", "tool_name": "mcp__mail__move", "input": in}}})
	c.askBrainAccess(&TaskRow{ID: "b-1"}, "p-1", "mcp__mail__move", in, approvalReason, nil)
	x := accessCard(t, c, "mcp__mail__move")
	if _, err := c.decideCard(ctx, x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if told, allow := answeredAllow(t, c, "b-1", "p-1"); !told || !allow {
		t.Errorf("told %v, allow %v", told, allow)
	}
	if !c.store.useTicket("b-1", "mcp__mail__move", in) {
		t.Error("the yes gave the call no ticket")
	}
	if got, _ := c.decideBrainCall("mcp__mail__move", in, nil); got != decideAsk {
		t.Errorf("the next call: %q", got)
	}
}

// A run_task on a watch turn the user authorised needs no card; this is
// the grant itself.
func TestApprovedInAdvanceGrantsTheLend(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	tk, _ := c.store.task("t-1")
	c.grantLent(tk)
	if got := request(t, c, "t-1", "p-1", "move"); got != "allowed" {
		t.Errorf("what was approved in advance: %s", got)
	}
}

// The drawer says what was granted.
func TestDrawerShowsGrants(t *testing.T) {
	c := newCatalogCore(t)
	c.conf().User = "Sam"
	tw := newTestWebFor(t, c)
	tw.setup()
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x", State: stateRunning})
	c.store.grantAccess("t-1", accessNeed{"mail", catDelete, "sam@example.org"})
	if _, body, _ := tw.do("GET", "/tasks/t-1", nil, nil); !strings.Contains(body, "supprimer, sam@example.org") {
		t.Errorf("no grant in the drawer: %s", body)
	}
}
