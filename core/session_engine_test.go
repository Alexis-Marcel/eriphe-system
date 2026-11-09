package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A plan holds together: keys unique, what a step comes after exists, no
// cycle, and each kind with what it needs.
func TestSessionPlanChecked(t *testing.T) {
	task := func(key string, after ...string) stepSpec {
		return stepSpec{Key: key, Title: "Step " + key, Prompt: "do " + key, After: after}
	}
	for name, steps := range map[string][]stepSpec{
		"duplicate":   {task("a"), task("a")},
		"unknown":     {task("a", "z")},
		"self":        {task("a", "a")},
		"cycle":       {task("a", "c"), task("b", "a"), task("c", "b")},
		"no prompt":   {{Key: "a", Title: "A"}},
		"bad key":     {{Key: "A B", Title: "A", Prompt: "x"}},
		"one option":  {{Key: "a", Title: "A", Kind: stepChoice, Question: "Which?", Options: []string{"x"}}},
		"approval op": {{Key: "a", Title: "A", Kind: stepApproval, Question: "OK?", Options: []string{"x", "y"}}},
		"choice task": {{Key: "a", Title: "A", Kind: stepChoice, Question: "Which?", Options: []string{"x", "y"}, Prompt: "x"}},
	} {
		if err := checkPlan(nil, steps); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := checkPlan(nil, []stepSpec{task("a"), task("b"), task("c", "a", "b")}); err != nil {
		t.Errorf("a diamond: %v", err)
	}
}

// sessionCore is a core with a node, and a session of three steps: two in
// parallel, then a review the user approves.
func sessionCore(t *testing.T) (*core, Session) {
	t.Helper()
	c := startCore(t)
	s, err := c.openSession(sessionIn{Title: "Brand name", Goal: "Find a name for the shop.", DoneWhen: "Sam picked a name that is free",
		Steps: []stepSpec{
			{Key: "names", Title: "Find names", Prompt: "Propose ten names."},
			{Key: "market", Title: "Study the market", Prompt: "List the shops nearby."},
			{Key: "check", Title: "Check the names", Prompt: "Check which names are free.", After: []string{"names", "market"}},
			{Key: "ok", Title: "Sam's pick", Kind: stepApproval, Question: "Keep the first free name?", After: []string{"check"}},
		}}, nil, "brain")
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

// sessionCard is the card a session asks, of a kind.
func sessionCard(t *testing.T, c *core, kind string) convoCard {
	t.Helper()
	xs, _ := c.cardsWhere(`kind = ? ORDER BY id`, cardSession)
	for _, x := range xs {
		_, id, _ := parseProposalRef(x.Ref)
		if a, err := c.sessionAsk(id); err == nil && a.Kind == kind && x.State == cardPending {
			return x
		}
	}
	t.Fatalf("no %s card in %+v", kind, xs)
	return convoCard{}
}

func stepOf(t *testing.T, c *core, s Session, key string) Step {
	t.Helper()
	st, err := c.step(s.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// reports ends a step's task turn with this message.
func reports(t *testing.T, c *core, st Step, text string, extra ...EventRow) {
	t.Helper()
	task, _ := c.store.task(st.Task)
	seq := task.LastSeq
	events := []EventRow{ev(seq+1, "text", map[string]any{"text": text})}
	for i, e := range extra {
		e.Seq = seq + 2 + i
		events = append(events, e)
	}
	events = append(events, ev(seq+len(events)+1, "turn_end", map[string]any{"subtype": "success"}))
	c.store.addEvents(st.Task, events)
	c.store.updateTask(st.Task, map[string]any{"state": stateIdle})
	c.notifyBrain(st.Task, stateIdle)
}

func receiptOf(text, summary string) string {
	return text + "\n\n```json\n" + `{"receipt": {"status": "done", "summary": "` + summary + `"}}` + "\n```"
}

// The plan waits for the user's card; then the engine runs the steps in
// order, in parallel when it may, passes each its predecessors' outputs,
// asks the approval on a card, and tells the lead when all is accepted.
func TestSessionRuns(t *testing.T) {
	c, s := sessionCore(t)
	ctx := context.Background()
	if st := stepOf(t, c, s, "names"); st.Status != stepWaiting || st.Task != "" {
		t.Fatalf("a step started before the plan was approved: %+v", st)
	}
	plan := sessionCard(t, c, askPlan)
	_, p := c.liveCard(plan)
	if len(p.Fields) != 4 || !strings.Contains(p.Text("fr"), "Brand name") {
		t.Errorf("plan card %q %+v", p.Text("fr"), p.Fields)
	}
	if _, err := c.decideCard(ctx, plan.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	names, market := stepOf(t, c, s, "names"), stepOf(t, c, s, "market")
	if names.Status != stepRunning || market.Status != stepRunning || names.Task == "" || market.Task == "" {
		t.Fatalf("the two first steps run in parallel: %+v %+v", names, market)
	}
	if c.heldTask(names.Task) {
		t.Error("a planned step asked its start again")
	}
	if st := stepOf(t, c, s, "check"); st.Status != stepWaiting {
		t.Fatalf("check started before its predecessors: %s", st.Status)
	}

	reports(t, c, names, receiptOf("Names: Lumen, Rivet, Ocre.", "ten names"))
	if st := stepOf(t, c, s, "names"); st.Status != stepReported || st.Output != "Names: Lumen, Rivet, Ocre." {
		t.Fatalf("names after its turn: %+v", st)
	}
	if msg := lastEvent(t, c); !strings.Contains(msg, "[session") || !strings.Contains(msg, "settle_step") {
		t.Errorf("the lead heard %q", msg)
	}
	if _, err := c.settleStep(ctx, s.ID, "names", verdictAccept, "", "", "brain"); err != nil {
		t.Fatal(err)
	}
	if st := stepOf(t, c, s, "check"); st.Status != stepWaiting {
		t.Fatalf("check started with market still running: %s", st.Status)
	}
	reports(t, c, market, receiptOf("Two shops nearby.", "the market"))
	if _, err := c.settleStep(ctx, s.ID, "market", verdictAccept, "", "Two shops: Atelier, Forge.", "brain"); err != nil {
		t.Fatal(err)
	}
	check := stepOf(t, c, s, "check")
	if check.Status != stepRunning {
		t.Fatalf("check: %s", check.Status)
	}
	if task, _ := c.store.task(check.Task); !strings.Contains(task.Prompt, "Lumen, Rivet") || !strings.Contains(task.Prompt, "Atelier, Forge") ||
		!strings.Contains(task.Prompt, "Find a name for the shop") {
		t.Errorf("check was not given what came before:\n%s", task.Prompt)
	}

	reports(t, c, check, receiptOf("Lumen is free.", "checked"))
	if _, err := c.settleStep(ctx, s.ID, "check", verdictAccept, "", "", "brain"); err != nil {
		t.Fatal(err)
	}
	ok := sessionCard(t, c, askApproval)
	if _, p := c.liveCard(ok); !strings.Contains(p.Text("fr"), "Keep the first free name?") {
		t.Errorf("approval card %q", p.Text("fr"))
	}
	if err := c.endSession(s.ID, "done", "brain"); err == nil {
		t.Error("a session closed with a step waiting for the user")
	}
	if _, err := c.decideCard(ctx, ok.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if msg := lastEvent(t, c); !strings.Contains(msg, "Toutes les étapes sont acceptées") {
		t.Errorf("the lead heard %q", msg)
	}
	if err := c.endSession(s.ID, "Lumen it is.", "brain"); err != nil {
		t.Fatal(err)
	}
	if s, _ = c.session(s.ID); s.State != sessDone || s.Summary != "Lumen it is." {
		t.Errorf("session %+v", s)
	}
}

// A report is only a claim: a step whose checks failed cannot be accepted;
// it is retried, twice at most.
func TestSessionChecksHold(t *testing.T) {
	c, s := sessionCore(t)
	ctx := context.Background()
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	names := stepOf(t, c, s, "names")
	reports(t, c, names, receiptOf("Done.", "names"), ev(0, "check", map[string]any{"name": "list", "ok": false, "run": "test -s names.txt", "exit_code": 1}))
	st := stepOf(t, c, s, "names")
	if st.Status != stepReported || !st.Failed {
		t.Fatalf("a failed check: %+v", st)
	}
	if _, err := c.settleStep(ctx, s.ID, "names", verdictAccept, "", "", "brain"); err == nil || !strings.Contains(err.Error(), "checks failed") {
		t.Fatalf("accepted against its checks: %v", err)
	}
	if _, err := c.settleStep(ctx, s.ID, "names", verdictRetry, "", "", "brain"); err == nil {
		t.Error("a retry without saying what to fix")
	}
	for i := range stepRetries {
		if _, err := c.settleStep(ctx, s.ID, "names", verdictRetry, "write names.txt", "", "brain"); err != nil {
			t.Fatalf("retry %d: %v", i+1, err)
		}
		st = stepOf(t, c, s, "names")
		if st.Status != stepRunning || st.Tries != i+1 || st.Failed {
			t.Fatalf("after retry %d: %+v", i+1, st)
		}
		items, _ := c.store.inbox(st.Task)
		if len(items) == 0 || !strings.Contains(items[len(items)-1].Data["text"].(string), "write names.txt") {
			t.Fatalf("the agent was not told what to fix: %+v", items)
		}
		for _, it := range items {
			c.store.ackInbox(st.Task, it.Seq)
		}
		reports(t, c, st, receiptOf("Done again.", "names"), ev(0, "check", map[string]any{"name": "list", "ok": false, "run": "test -s names.txt", "exit_code": 1}))
	}
	if _, err := c.settleStep(ctx, s.ID, "names", verdictRetry, "again", "", "brain"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("a third retry: %v", err)
	}
	if _, err := c.settleStep(ctx, s.ID, "names", verdictReject, "no list", "", "brain"); err != nil {
		t.Fatal(err)
	}
	reports(t, c, stepOf(t, c, s, "market"), receiptOf("Shops.", "market"))
	c.settleStep(ctx, s.ID, "market", verdictAccept, "", "", "brain")
	if msg := lastEvent(t, c); !strings.Contains(msg, "Plus rien ne peut avancer") || !strings.Contains(msg, "names") {
		t.Errorf("a stuck session was not told: %q", msg)
	}
}

// Two steps writing the same branch never run at once.
func TestSessionOneWriterPerScope(t *testing.T) {
	c := startCore(t)
	repo := []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/x"}}
	s, err := c.openSession(sessionIn{Title: "Rebuild", Goal: "Rebuild the photos service.", Steps: []stepSpec{
		{Key: "api", Title: "API", Prompt: "Write the API.", Resources: repo},
		{Key: "ui", Title: "UI", Prompt: "Write the UI.", Resources: repo},
		{Key: "docs", Title: "Docs", Prompt: "Write the docs.", Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/docs"}}},
	}}, nil, "brain")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	api, ui, docs := stepOf(t, c, s, "api"), stepOf(t, c, s, "ui"), stepOf(t, c, s, "docs")
	if api.Status != stepRunning || ui.Status != stepWaiting || docs.Status != stepRunning {
		t.Fatalf("api %s, ui %s, docs %s: one writer per branch", api.Status, ui.Status, docs.Status)
	}
	if c.heldTask(api.Task) {
		t.Error("a planned write asked its start again")
	}
	reports(t, c, api, receiptOf("API done.", "api"))
	c.settleStep(ctx, s.ID, "api", verdictAccept, "", "", "brain")
	if st := stepOf(t, c, s, "ui"); st.Status != stepRunning {
		t.Errorf("ui after api: %s", st.Status)
	}
}

// Past its budget a session pauses and asks the user for more time; the
// plan changes twice without asking, then on a card; a step added asks its
// own start card.
func TestSessionLimits(t *testing.T) {
	c, s := sessionCore(t)
	ctx := context.Background()
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")

	for i := range planAmends {
		key := string(rune('x' + i))
		if _, err := c.amendSession(ctx, s.ID, amendIn{Add: []stepSpec{{Key: key, Title: "Extra " + key, Prompt: "more",
			Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/" + key}}}}}, "more work", "brain"); err != nil {
			t.Fatal(err)
		}
	}
	x := stepOf(t, c, s, "x")
	if x.Planned || x.Status != stepRunning {
		t.Fatalf("an added step: %+v", x)
	}
	if !c.heldTask(x.Task) {
		t.Error("an added step that writes started without its card")
	}
	note, err := c.amendSession(ctx, s.ID, amendIn{Drop: []string{"ok"}}, "no need", "brain")
	if err != nil || !strings.Contains(note, "card") {
		t.Fatalf("a third change: %q %v", note, err)
	}
	if st := stepOf(t, c, s, "ok"); st.Status != stepWaiting {
		t.Fatalf("dropped before the user said yes: %s", st.Status)
	}
	c.decideCard(ctx, sessionCard(t, c, askAmend).ID, true, "web")
	if st := stepOf(t, c, s, "ok"); st.Status != stepDropped {
		t.Errorf("dropped after the user's yes: %s", st.Status)
	}

	// Its time runs out.
	c.setSession(s.ID, map[string]any{"since": time.Now().Add(-2 * time.Hour).Unix()})
	c.sessionsTick(time.Now())
	if s, _ = c.session(s.ID); s.State != sessPaused || s.Held != heldBudget {
		t.Fatalf("past its budget: %+v", s)
	}
	if err := c.resumeSession(ctx, s.ID, "brain"); err == nil {
		t.Error("the brain gave itself more time")
	}
	c.decideCard(ctx, sessionCard(t, c, askBudget).ID, true, "web")
	if s, _ = c.session(s.ID); s.State != sessRunning || s.Budget != 120+budgetMore(Session{Budget: defaultBudget}) {
		t.Errorf("after more time: %+v", s)
	}
}

// A choice is the user's, recorded in the turn of their answer; stopping a
// session stops its tasks and withdraws its cards.
func TestSessionChoiceAndStop(t *testing.T) {
	c := startCore(t)
	ctx := context.Background()
	s, err := c.openSession(sessionIn{Title: "Video", Goal: "Make a short video.", Steps: []stepSpec{
		{Key: "style", Title: "Style", Kind: stepChoice, Question: "Which style?", Options: []string{"Calm", "Fast"}},
		{Key: "cut", Title: "Cut", Prompt: "Cut the video.", After: []string{"style"}},
	}}, nil, "brain")
	if err != nil {
		t.Fatal(err)
	}
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	if msg := lastEvent(t, c); !strings.Contains(msg, "Which style?") || !strings.Contains(msg, "choose_step") {
		t.Errorf("the lead heard %q", msg)
	}
	if _, err := c.chooseStep(ctx, s.ID, "style", "Slow", "user"); err == nil {
		t.Error("an option not offered was taken")
	}
	if _, err := c.chooseStep(ctx, s.ID, "style", "fast", "user"); err != nil {
		t.Fatal(err)
	}
	cut := stepOf(t, c, s, "cut")
	if task, _ := c.store.task(cut.Task); task == nil || !strings.Contains(task.Prompt, "Fast") {
		t.Fatalf("cut was not given the choice: %+v", cut)
	}
	if err := c.stopSession(ctx, s.ID, "brain"); err != nil {
		t.Fatal(err)
	}
	if task, _ := c.store.task(cut.Task); task.State != stateStopped {
		t.Errorf("its task goes on: %s", task.State)
	}
	if st := stepOf(t, c, s, "cut"); st.Status != stepBlocked {
		t.Errorf("cut after the stop: %s", st.Status)
	}
	// Its end is not told again.
	c.store.updateTask(cut.Task, map[string]any{"state": stateStopped})
	before := lastEvent(t, c)
	c.notifyBrain(cut.Task, stateStopped)
	if lastEvent(t, c) != before {
		t.Errorf("a stopped session's task told its end: %q", lastEvent(t, c))
	}
}

// The plan the user declines closes the session; the brain hears it.
func TestSessionDeclined(t *testing.T) {
	c, s := sessionCore(t)
	c.decideCard(context.Background(), sessionCard(t, c, askPlan).ID, false, "web")
	if s, _ = c.session(s.ID); s.State != sessDeclined {
		t.Errorf("state %s", s.State)
	}
	if st := stepOf(t, c, s, "names"); st.Task != "" {
		t.Error("a step of a declined plan started")
	}
}
