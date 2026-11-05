package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// An access card comes at any moment: a plain yes may be for the brain's
// question just before it, so only its buttons answer.
func TestStrayYesDoesNotAnswerAccess(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	c.store.addMessage(roleBrain, "Je l'archive ?", true)
	request(t, c, "t-1", "p-1", "move")
	seq, _ := c.store.addMessage(roleUser, "oui", true)
	if _, ok := c.resolveByText(context.Background(), int(seq)); ok {
		t.Fatal("a stray yes answered an access card")
	}
	if len(c.store.accessGrants("t-1")) != 0 {
		t.Fatal("a stray yes granted the category")
	}
}

// The card's message is a question: it notifies as any question does, also
// in alerts only.
func TestAccessCardNotifies(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	request(t, c, "t-1", "p-1", "move")
	msgs, _ := c.store.messagesAfter(0, 10)
	if len(msgs) != 1 {
		t.Fatalf("messages: %+v", msgs)
	}
	if _, ok := asksUser(msgs[0].Text); !ok {
		t.Errorf("not a question: %q", msgs[0].Text)
	}
	x := accessQuestion{TaskID: "b", Need: accessNeed{"mail", catSend, ""}, Call: "mcp__mail__send {}"}
	if _, ok := asksUser(c.accessText(x, "fr")); !ok {
		t.Errorf("the brain's is not a question: %q", c.accessText(x, "fr"))
	}
}

// A hold is lifted by its release alone: not by a grant of its category,
// nor because it reads.
func TestHoldIsNotAGrant(t *testing.T) {
	c := newMailCore(t, accessReads)
	c.store.set(lentByUserKey("t-1"), "1")
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
		State: stateWaitingPermission, Tools: "mail", Resources: []ResourceRef{
			{Kind: resTool, Ref: "mail.move", Access: accessWrite, Hold: true},
			{Kind: resTool, Ref: "mail.search_messages", Access: accessRead, Hold: true},
		}})
	c.store.grantAccess("t-1", accessNeed{"mail", catChange, ""})
	for _, tool := range []string{"move", "search_messages"} {
		if got := request(t, c, "t-1", "p-"+tool, tool); got != "user" {
			t.Errorf("%s held: %s", tool, got)
		}
	}
	// Its release answers what waited on it.
	if _, err := c.releaseTools("t-1", []string{"mail.move"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.decideCard(context.Background(), accessCard(t, c, "mail.move").ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if told, allow := answeredAllow(t, c, "t-1", "p-move"); !told || !allow {
		t.Errorf("what waited on the release: told %v, allow %v", told, allow)
	}
	if told, _ := answeredAllow(t, c, "t-1", "p-search_messages"); told {
		t.Error("the release answered a tool still held")
	}
}

// A ref names a tool as its server does, Claude Code's name differs.
func TestHoldOfAToolWithADot(t *testing.T) {
	c := newMailCore(t, accessFree)
	u := c.pool.get("mail")
	u.mu.Lock()
	u.tools = append(u.tools, &mcp.Tool{Name: "issues.create"})
	u.mu.Unlock()
	task := &TaskRow{ID: "t-1", Resources: []ResourceRef{{Kind: resTool, Ref: "mail.issues.create", Hold: true}}}
	c.store.set(lentByUserKey("t-1"), "1")
	if got, why := c.decideTask(task, claudeToolName("mail", "issues.create"), nil); got != decideAsk || why != heldReason {
		t.Errorf("held = %q (%s)", got, why)
	}
}

// No rule lifts a Never: the proxy would refuse the call anyway.
func TestRuleDoesNotLiftNever(t *testing.T) {
	c := newMailCore(t, accessReadOnly)
	c.conf().Policy = &Policy{Rules: []Rule{{Tool: "mcp__mail__*", Decision: decideAllow}}}
	if err := c.conf().Policy.compile(); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decide("mcp__mail__send", nil); got != decideDeny {
		t.Errorf("a rule on Never = %q", got)
	}
}

// A request whose card is decided and that nothing covers shows again as a
// permission, rather than stay hidden behind a card that is gone.
func TestRequestLeftByACardShowsAgain(t *testing.T) {
	c := newMailCore(t, "")
	waitingTask(c, "t-1")
	request(t, c, "t-1", "p-1", "move")
	x := accessCard(t, c, "change")
	c.conf().Policy = &Policy{Rules: []Rule{{Tool: "mcp__mail__move", Decision: decideAsk, Reason: "a rule now"}}}
	if err := c.conf().Policy.compile(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.decideCard(context.Background(), x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if c.accessAsked("t-1", "p-1") {
		t.Error("still hidden behind a decided card")
	}
}

// Two services of one account stay apart on a card.
func TestStartCardNamesEachService(t *testing.T) {
	needs := []startNeed{{Server: "mail", Category: catChange, Account: "sam@example.org"},
		{Server: "agenda", Category: catChange, Account: "sam@example.org"}}
	got := strings.Join(accessStartText(needs, "en"), " ")
	if !strings.Contains(got, "in mail sam@example.org") || !strings.Contains(got, "in agenda sam@example.org") {
		t.Errorf("text = %s", got)
	}
}

// A level moved from Free to approval changes what is allowed in advance:
// the brain must restart, though its grant is the same.
func TestFreeReadsChangeTheToolsHash(t *testing.T) {
	c := newCatalogCore(t)
	setBrain(c, &BrainConfig{})
	c.started = time.Now().Add(-time.Hour)
	waitStatus(t, c.pool.get("notes"), statusReady)
	a := *c.conf().brain()
	before := c.assistantToolsHash(a)
	setLevel(t, c, "github", accessAsk)
	if after := c.assistantToolsHash(a); before == "" || after == "" || after == before {
		t.Errorf("hash %q then %q", before, after)
	}
}

// What the workspace does with accounts holds for an account with a grid.
func TestAccountGridFollowsTheAccount(t *testing.T) {
	grids := map[string]accountGrid{"Sam@example.org": {}, "pro@example.org": {}}
	if got := withoutAccount(grids, "sam@example.org"); len(got) != 1 {
		t.Errorf("left %v", got)
	}
	if len(grids) != 2 {
		t.Error("the server's own map was changed")
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
	if _, body, _ := tw.do("GET", "/tasks/t-1", nil, nil); !strings.Contains(body, "supprimer, mail sam@example.org") {
		t.Errorf("no grant in the drawer: %s", body)
	}
}
