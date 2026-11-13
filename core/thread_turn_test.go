package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The brain's part is played here as its prompt asks it (brain-prompt.md,
// « Les threads »): the tools it calls, in order, and what eriphe-core
// makes of them.

// threadSurface is the thread's own surface, thread-<id>, that code wrote
// at its opening (thread_surface.go).
func threadSurface(t *testing.T, c *core, id int64) string {
	t.Helper()
	sid := fmt.Sprintf("thread-%d", id)
	if sid != threadSurfaceID(id) {
		t.Fatalf("the thread's surface is %s", threadSurfaceID(id))
	}
	if _, err := c.surface(sid); err != nil {
		t.Fatalf("thread %d has no surface: %v", id, err)
	}
	return sid
}

// « Où en est la PR de l'alerte ? » from the workspace: no thread yet, so
// one opens, light, and its surface takes the stage; asked again, the same
// thread is found by its subject, never a second one.
func TestStatusQuestionOpensThreadOnStage(t *testing.T) {
	c := focusCore(t)
	const subject = "pr:example/infra#12"
	c.front.begin(true) // Sam asks from the workspace
	out, isErr := callBrainTool(t, c, "find_thread", map[string]any{"subject": subject})
	if isErr || len(foundThreads(out)) != 0 {
		t.Fatalf("find: %v", out)
	}
	out, isErr = callBrainTool(t, c, "open_thread", map[string]any{"title": "PR de l'alerte", "goal": "La PR est fusionnée et déployée.", "subject": subject})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	th := out["thread"].(map[string]any)
	id := int64(th["id"].(float64))
	if th["mode"] != modeLight || th["state"] != threadOpen {
		t.Fatalf("not light and open: %v", th)
	}
	if c.turn.aboutThread() != id {
		t.Errorf("the turn is about %d, not %d", c.turn.aboutThread(), id)
	}
	if asks, _ := c.cardsWhere(`kind = ?`, cardThread); len(asks) != 0 {
		t.Errorf("a light thread asked a card: %+v", asks)
	}
	sid := threadSurface(t, c, id)
	if out, isErr := callBrainTool(t, c, "workspace_focus", map[string]any{"id": sid}); isErr || out["result"] != "in front" {
		t.Fatalf("focus: %v", out)
	}
	if l := c.currentLayout(); l.Focus != sid || l.FocusBy != focusByBrain {
		t.Fatalf("stage: %q by %q", l.Focus, l.FocusBy)
	}

	// Asked again later, in other words and case: the same thread.
	c.front.begin(true)
	out, _ = callBrainTool(t, c, "find_thread", map[string]any{"subject": strings.ToUpper(subject)})
	if got := foundThreads(out); len(got) != 1 || int64(got[0].(map[string]any)["id"].(float64)) != id {
		t.Fatalf("resume by subject: %v", out)
	}
	// Closed on its goal, then back: reopened, not opened anew.
	if out, isErr := callBrainTool(t, c, "close_thread", map[string]any{"thread_id": id, "summary": "Fusionnée et déployée."}); isErr {
		t.Fatalf("close: %v", out)
	}
	out, _ = callBrainTool(t, c, "find_thread", map[string]any{"subject": subject})
	if got := foundThreads(out); len(got) != 1 || got[0].(map[string]any)["state"] != threadDone {
		t.Fatalf("an ended thread is found again: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "reopen_thread", map[string]any{"thread_id": id, "why": "L'alerte revient."}); isErr || out["state"] != threadOpen {
		t.Fatalf("reopen: %v", out)
	}
	if all, _ := c.threads(false, 10); len(all) != 1 {
		t.Errorf("%d threads for one matter", len(all))
	}
}

// « Pendant que je relis la PR, cherche un nom de marque » while the PR's
// thread is on stage: a second thread opens on the side; the stage stays
// the PR's, and the turn is now about the research.
func TestLaunchOpensThreadOnTheSide(t *testing.T) {
	c := focusCore(t)
	pr, err := c.openThread(threadIn{Title: "PR de l'alerte", Goal: "La PR est fusionnée.", Subject: "pr:example/infra#12"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.front.begin(true)
	sid := threadSurface(t, c, pr.ID)
	if _, err := c.focusFromBrain(workspaceFocusIn{ID: sid}, time.Now()); err != nil {
		t.Fatal(err)
	}

	c.front.begin(true) // the next message, from the workspace too
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Nom de marque", "goal": "Sam a choisi un nom libre.", "subject": "project:marque"})
	if isErr {
		t.Fatalf("open: %v", out)
	}
	research := int64(out["thread"].(map[string]any)["id"].(float64))
	threadSurface(t, c, research)
	if l := c.currentLayout(); l.Focus != sid {
		t.Errorf("the research took the stage: %q", l.Focus)
	}
	if c.turn.aboutThread() != research {
		t.Errorf("the turn is about %d, not the research %d", c.turn.aboutThread(), research)
	}
}

// The brain's own calls in a turn about a thread raise cards that land in
// its timeline: the thread last named in the turn; a new delivery about
// nothing links nothing.
func TestCardsOfThreadTurnLinked(t *testing.T) {
	c := stepsCore(t)
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "box", Runtime: "claude-code", State: stateWaitingPermission})
	pr := lightThread(t, c)
	if _, isErr := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": pr.ID}); isErr {
		t.Fatal("read_thread")
	}
	brainCalls(t, c, "p-1", mergeTool, map[string]any{"number": 12})
	merge := accessCard(t, c, "merge").Ref
	if refs := timelineCards(t, c, pr.ID); refs != merge {
		t.Fatalf("the PR's cards: %q, want %q", refs, merge)
	}
	// It waits on the thread's surface, answered there.
	if s, _, _ := threadSurfaceOf(t, c, pr.ID); !strings.Contains(string(s.Components), `"proposalId":"`+merge+`"`) {
		t.Errorf("the card is not on the thread's surface: %s", s.Components)
	}
	// The same call asked again: the same card, still linked once.
	brainCalls(t, c, "p-2", mergeTool, map[string]any{"number": 12})
	if refs := timelineCards(t, c, pr.ID); refs != merge {
		t.Errorf("asked again: %q", refs)
	}
	// A thread tool on an id that is no thread changes nothing.
	callBrainTool(t, c, "read_thread", map[string]any{"thread_id": 999})
	if c.turn.aboutThread() != pr.ID {
		t.Errorf("an unknown thread retargeted the turn: %d", c.turn.aboutThread())
	}

	// A second thread named: what follows is its own.
	out, _ := callBrainTool(t, c, "open_thread", map[string]any{"title": "Nom de marque", "goal": "Un nom libre.", "subject": "project:marque"})
	brand := int64(out["thread"].(map[string]any)["id"].(float64))
	brainCalls(t, c, "p-3", mergeTool, map[string]any{"number": 13})
	if refs := timelineCards(t, c, brand); refs == "" || strings.Contains(refs, merge) {
		t.Errorf("the brand's cards: %q", refs)
	}
	if refs := timelineCards(t, c, pr.ID); refs != merge {
		t.Errorf("the PR's cards after: %q", refs)
	}
	// Decided, it waits there no more.
	xs, _ := c.cardsWhere(`ref = ?`, merge)
	if len(xs) != 1 {
		t.Fatalf("cards of %s: %+v", merge, xs)
	}
	if _, err := c.decideCard(context.Background(), xs[0].ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := threadSurfaceOf(t, c, pr.ID); strings.Contains(string(s.Components), `"proposalId":"`+merge+`"`) {
		t.Errorf("a decided card still waits on the surface: %s", s.Components)
	}

	// A task started by the brain in that turn: its start card too.
	c.store.insertTask(&TaskRow{ID: "t-9", Kind: "worker", Node: "box", Runtime: "claude-code", Prompt: "Cherche", State: statePending})
	if err := c.holdTask(&TaskRow{ID: "t-9", Title: "Chercher des noms"}, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if refs := timelineCards(t, c, brand); !strings.Contains(refs, cardTaskStart+":") {
		t.Errorf("the start card is not the brand's: %q", refs)
	}

	// A new delivery about nothing: the next card is no thread's. (The no
	// above was told as the PR's event, read in a turn of its own.)
	c.store.markDelivered(c.store.lastMessageSeq())
	seq, _ := c.store.addMessage(roleUser, "Et la météo ?", true)
	c.turn.start(int(seq), int(seq), 0)
	msgs, _ := c.store.undelivered()
	c.threadsDelivered(msgs)
	if c.turn.aboutThread() != 0 {
		t.Fatalf("a new turn kept thread %d", c.turn.aboutThread())
	}
	before := timelineCards(t, c, pr.ID) + timelineCards(t, c, brand)
	brainCalls(t, c, "p-4", mergeTool, map[string]any{"number": 14})
	if after := timelineCards(t, c, pr.ID) + timelineCards(t, c, brand); after != before {
		t.Errorf("a card of no thread was linked: %q", after)
	}
}

// timelineCards are the refs of the cards linked to a thread.
func timelineCards(t *testing.T, c *core, id int64) string {
	t.Helper()
	tl, err := c.threadTimeline(id)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, x := range tl.Cards {
		refs = append(refs, x.Ref)
	}
	return strings.Join(refs, " ")
}

// A step that moves wakes the brain about its thread: the delivered event
// makes the turn the thread's, and the brain updates the thread there
// (its summary, its next step) without anything asked of the user.
func TestWakeOnStepUpdatesThread(t *testing.T) {
	c := stepsCore(t)
	ctx := context.Background()
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "box", Runtime: "claude-code", State: stateWaitingPermission})
	s := lightThread(t, c)
	if _, err := c.addStep(ctx, s.ID, stepSpec{Key: "deploy", Title: "Déployer", Kind: stepUser, Question: "Déploie la nouvelle version"}, nil, "brain"); err != nil {
		t.Fatal(err)
	}
	c.store.markDelivered(c.store.lastMessageSeq())
	if _, err := c.userStepDone(ctx, s.ID, "deploy", "user"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := c.store.undelivered()
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, fmt.Sprintf("[thread %d « PR alerting »] étape « Déployer » (deploy, user) → accepted", s.ID)) {
		t.Fatalf("the wake: %+v", msgs)
	}
	c.turn.goOn(msgs[0].Seq, msgs[0].Seq, 0, nil)
	c.threadsDelivered(msgs)
	if c.turn.aboutThread() != s.ID {
		t.Fatalf("the wake turn is about %d", c.turn.aboutThread())
	}
	if c.turnFromUser() {
		t.Fatal("a wake turn reads as the user's")
	}
	// It updates the thread: its summary, the check that follows.
	if out, isErr := callBrainTool(t, c, "link_thread", map[string]any{"thread_id": s.ID, "summary": "Fusionnée, déployée par Sam ; reste à vérifier l'alerte."}); isErr {
		t.Fatalf("summary: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "add_step", map[string]any{"thread_id": s.ID, "step": map[string]any{
		"key": "verify", "title": "Vérifier l'alerte", "kind": "check", "tool": readTool, "input": map[string]any{"number": 12}, "expect": "open"}}); isErr {
		t.Fatalf("check: %v", out)
	}
	waitStep(t, c, s, "verify", stepAccepted)
	if got, _ := c.thread(s.ID); !strings.Contains(got.Summary, "reste à vérifier") || !got.active() {
		t.Errorf("thread %+v", got)
	}
	// A choice is recorded only in the turn of the user's answer.
	if out, isErr := callBrainTool(t, c, "choose_step", map[string]any{"thread_id": s.ID, "step": "verify", "option": "x"}); !isErr {
		t.Errorf("a choice recorded in a wake turn: %v", out)
	}
}

// What a step produced is handed to the task after it with its taint: the
// next task starts tainted, in a light thread as in an approved plan.
func TestStepTaintHandedOn(t *testing.T) {
	t.Run("light", func(t *testing.T) {
		c := startCore(t)
		ctx := context.Background()
		s := lightThread(t, c)
		if _, err := c.addStep(ctx, s.ID, stepSpec{Key: "search", Title: "Chercher", Prompt: "Search the web."}, nil, "brain"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.addStep(ctx, s.ID, stepSpec{Key: "write", Title: "Écrire", Prompt: "Write it up.", After: []string{"search"}}, nil, "brain"); err != nil {
			t.Fatal(err)
		}
		search := stepOf(t, c, s, "search")
		// A card its task raises is the thread's too.
		id := c.newAccessAsk(accessQuestion{TaskID: search.Task, Permission: "p-1", Need: accessNeed{"github", catChange, ""}})
		if refs := timelineCards(t, c, s.ID); !strings.Contains(refs, proposalRef(cardAccess, id)) {
			t.Errorf("the step task's card is not the thread's: %q", refs)
		}
		c.taskRead(search.Task, "web")
		reports(t, c, search, receiptOf("Found three.", "found"))
		if _, err := c.settleStep(ctx, s.ID, "search", verdictAccept, "", "Three names.", "brain"); err != nil {
			t.Fatal(err)
		}
		write := stepOf(t, c, s, "write")
		if write.Task == "" {
			t.Fatalf("not started: %+v", write)
		}
		if why := c.store.get(taskTaintKey(write.Task)); !strings.Contains(why, taskTaintPrefix+search.Task) {
			t.Errorf("the next task is not tainted: %q", why)
		}
		if c.taskResultTaint(write.Task) == "" {
			t.Error("its result reads as clean")
		}
	})
	t.Run("clean", func(t *testing.T) {
		c := startCore(t)
		ctx := context.Background()
		s := lightThread(t, c)
		c.addStep(ctx, s.ID, stepSpec{Key: "a", Title: "A", Prompt: "Do A."}, nil, "brain")
		c.addStep(ctx, s.ID, stepSpec{Key: "b", Title: "B", Prompt: "Do B.", After: []string{"a"}}, nil, "brain")
		reports(t, c, stepOf(t, c, s, "a"), receiptOf("Done.", "done"))
		c.settleStep(ctx, s.ID, "a", verdictAccept, "", "", "brain")
		if b := stepOf(t, c, s, "b"); b.Task == "" || c.store.get(taskTaintKey(b.Task)) != "" {
			t.Errorf("a clean output tainted the next: %+v %q", b, c.store.get(taskTaintKey(b.Task)))
		}
	})
	t.Run("planned", func(t *testing.T) {
		c, s := threadCore(t)
		ctx := context.Background()
		c.decideCard(ctx, threadCard(t, c, askPlan).ID, true, "web")
		names, market := stepOf(t, c, s, "names"), stepOf(t, c, s, "market")
		c.taskRead(names.Task, "web")
		reports(t, c, names, receiptOf("Ten names.", "names"))
		reports(t, c, market, receiptOf("Five shops.", "shops"))
		for _, k := range []string{"names", "market"} {
			if _, err := c.settleStep(ctx, s.ID, k, verdictAccept, "", "", "lead"); err != nil {
				t.Fatal(err)
			}
		}
		check := stepOf(t, c, s, "check")
		if check.Task == "" {
			t.Fatalf("not started: %+v", check)
		}
		if why := c.store.get(taskTaintKey(check.Task)); !strings.Contains(why, taskTaintPrefix+names.Task) {
			t.Errorf("the planned step is not tainted: %q", why)
		}
		if c.store.get(lentByUserKey(check.Task)) != "" {
			t.Error("a tainted start counts as the user's clean one")
		}
		// The final check reads what every step said: tainted too.
		reports(t, c, check, receiptOf("Two are free.", "checked"))
		c.settleStep(ctx, s.ID, "check", verdictAccept, "", "", "lead")
		c.decideCard(ctx, threadCard(t, c, askApproval).ID, true, "web")
		if _, err := c.finishThread(ctx, s.ID, "Sam picked a name.", "lead"); err != nil {
			t.Fatal(err)
		}
		final := stepOf(t, c, s, finalStep)
		if final.Task == "" || !strings.Contains(c.store.get(taskTaintKey(final.Task)), taskTaintPrefix+names.Task) {
			t.Errorf("the final check is not tainted: %+v %q", final, c.store.get(taskTaintKey(final.Task)))
		}
	})
}

// The limit counts what still waits or runs: a thread that went through
// many steps takes more; past the bound of its life, it is refused.
func TestStepLimitCountsUnfinished(t *testing.T) {
	steps := func(n int, status string) []Step {
		var out []Step
		for i := range n {
			out = append(out, Step{stepSpec: stepSpec{Key: fmt.Sprintf("s%d", i)}, Status: status})
		}
		return out
	}
	add := []stepSpec{{Key: "next", Title: "Next", Kind: stepUser, Question: "Do it"}}
	if err := checkPlan(steps(maxSteps, stepAccepted), add); err != nil {
		t.Errorf("finished steps counted: %v", err)
	}
	if err := checkPlan(steps(maxSteps, stepWaiting), add); err == nil || !strings.Contains(err.Error(), "unfinished") {
		t.Errorf("unfinished past the limit: %v", err)
	}
	if err := checkPlan(steps(maxStepsEver, stepRejected), add); err == nil {
		t.Error("past the bound of a thread's life")
	}
}

// The prompt says when to open a thread, when its surface takes the stage,
// and how a wake turn goes.
func TestBrainPromptThreads(t *testing.T) {
	raw, _ := os.ReadFile("brain-prompt.md")
	prompt := string(raw)
	start := strings.Index(prompt, "## Les threads")
	end := strings.Index(prompt, "## Ta mémoire")
	if start < 0 || end < start {
		t.Fatal("no « Les threads »")
	}
	section := prompt[start:end]
	for _, want := range []string{
		"**Quand en ouvrir un.**", "`find_thread` par son sujet d'abord", "reste du texte, sans thread",
		"une étape `call` de fusion", "une étape `user`", "propose seulement le `check`",
		"`workspace_focus` avec `thread-<id>`", "**sans** `workspace_focus`",
		"aucune question de toi-même", "`[rien]`", "son **but** est atteint",
		"se range seul dans sa chronologie",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("« Les threads » lacks %q", want)
		}
	}
}

// foundThreads are the threads find_thread gave.
func foundThreads(out map[string]any) []any {
	xs, _ := out["threads"].([]any)
	return xs
}

// cardItem is a card's item in the attention queue.
func cardItem(x convoCard) string { return "card:" + strconv.FormatInt(x.ID, 10) }

// A card raised for a thread is answered on its surface, not again in the
// attention queue: the thread's surface takes its weight. Once the thread
// ended, the card still waiting comes back to the queue.
func TestThreadCardsLeaveTheQueue(t *testing.T) {
	c := stepsCore(t)
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "box", Runtime: "claude-code", State: stateWaitingPermission})
	pr := lightThread(t, c)
	callBrainTool(t, c, "read_thread", map[string]any{"thread_id": pr.ID})
	brainCalls(t, c, "p-1", mergeTool, map[string]any{"number": 12})
	merge := accessCard(t, c, "merge")
	// A card of no thread, for comparison: it keeps its own surface.
	c.turn.about(0)
	brainCalls(t, c, "p-2", mergeTool, map[string]any{"number": 99})
	var alone convoCard
	xs, _ := c.cardsWhere(`kind = ? AND state = ? ORDER BY id`, cardAccess, cardPending)
	for _, x := range xs {
		if x.ID != merge.ID {
			alone = x
		}
	}

	c.syncQueueSurfaces()
	if _, ok := surfaceByID(t, c, queueSurfaceID(cardItem(merge))); ok {
		t.Error("the thread's card has a surface of its own in the queue")
	}
	if g, ok := surfaceByID(t, c, queueDecide); ok && slices.Contains(groupRefs(t, g), merge.Ref) {
		t.Error("the thread's card is listed in the queue's group")
	}
	if _, ok := surfaceByID(t, c, queueSurfaceID(cardItem(alone))); !ok {
		t.Error("a card of no thread left the queue")
	}
	s, _, _ := threadSurfaceOf(t, c, pr.ID)
	if !strings.Contains(string(s.Components), `"proposalId":"`+merge.Ref+`"`) {
		t.Fatalf("the card is not on the thread's surface: %s", s.Components)
	}
	if want := queueSurfaceWeight(weightBlocksATask); s.Weight != want {
		t.Errorf("the thread's surface weighs %d, not its card's %d", s.Weight, want)
	}
	for _, e := range c.queuePlan(time.Now()) {
		if e.it.Ref == cardItem(merge) && e.surface != threadSurfaceID(pr.ID) {
			t.Errorf("the composer reads the card on %q", e.surface)
		}
	}

	// The thread stopped: the card still waits, back in the queue.
	if err := c.stopThread(context.Background(), pr.ID, "brain"); err != nil {
		t.Fatal(err)
	}
	c.syncQueueSurfaces()
	if _, ok := surfaceByID(t, c, queueSurfaceID(cardItem(merge))); !ok {
		t.Error("a card of an ended thread shows nowhere")
	}
}

// The result of a call approved on a card linked to a thread comes back as
// that thread's event: the turn that reads it is about the thread. A card
// of no thread gives a turn about none.
func TestLinkedCallResultAboutThread(t *testing.T) {
	c := stepsCore(t)
	ctx := context.Background()
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "box", Runtime: "claude-code", State: stateWaitingPermission})
	pr := lightThread(t, c)
	callBrainTool(t, c, "read_thread", map[string]any{"thread_id": pr.ID})
	brainCalls(t, c, "p-1", mergeTool, map[string]any{"number": 12})
	merge := accessCard(t, c, "merge")
	c.turn.about(0)
	brainCalls(t, c, "p-2", mergeTool, map[string]any{"number": 99})
	var alone convoCard
	xs, _ := c.cardsWhere(`kind = ? AND state = ? ORDER BY id`, cardAccess, cardPending)
	for _, x := range xs {
		if x.ID != merge.ID {
			alone = x
		}
	}

	deliver := func(x convoCard, approve bool, what string) string {
		t.Helper()
		c.store.markDelivered(c.store.lastMessageSeq())
		c.turn.about(0)
		if _, err := c.decideCard(ctx, x.ID, approve, "web"); err != nil {
			t.Fatal(err)
		}
		var msgs []Message
		waitFor(t, func() bool {
			msgs, _ = c.store.undelivered()
			return len(msgs) == 1 && strings.Contains(msgs[0].Text, what)
		})
		c.threadsDelivered(msgs)
		return msgs[0].Text
	}
	text := deliver(merge, true, "est exécutée")
	if c.turn.aboutThread() != pr.ID {
		t.Errorf("the result's turn is about %d, not the thread %d", c.turn.aboutThread(), pr.ID)
	}
	if !strings.HasPrefix(text, fmt.Sprintf("[thread %d « PR alerting »] ", pr.ID)) {
		t.Errorf("the result does not name its thread: %q", text)
	}
	if text := deliver(alone, false, "a refusé sur la carte"); strings.HasPrefix(text, "[thread") || c.turn.aboutThread() != 0 {
		t.Errorf("a card of no thread made the turn about %d: %q", c.turn.aboutThread(), text)
	}
}
