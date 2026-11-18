package main

import (
	"context"
	"strings"
	"testing"
)

// originWeb is a web of c, for what its cards show.
func originWeb(t *testing.T, c *core) *web {
	t.Helper()
	w, err := newWeb(c, WebConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// An access card a task step of a thread raises reads alone: it names the
// thread (linked) and the task, and says what its Oui does, in the
// workspace's proposal, the conversation's card and a channel's message.
func TestAccessCardNamesItsThreadAndTask(t *testing.T) {
	c := startCore(t)
	w := originWeb(t, c)
	s := lightThread(t, c)
	if _, err := c.addStep(context.Background(), s.ID, stepSpec{Key: "search", Title: "Chercher", Prompt: "Search the open issues."}, nil, byBrain); err != nil {
		t.Fatal(err)
	}
	task := stepOf(t, c, s, "search").Task
	if task == "" {
		t.Fatal("the step has no task")
	}
	// A grant: its text names the task already, its origin the thread.
	id := c.newAccessAsk(accessQuestion{TaskID: task, Permission: "p-1", Need: accessNeed{"github", catChange, ""}})
	x := cardOf(t, c, cardAccess, id)
	x, p := c.liveCard(x)
	v := w.proposalView(x, p, "en")
	if v.Thread != s.ID || v.From != "Thread “PR alerting”" || v.Yes != "On Yes, the task may change in github until it ends." {
		t.Errorf("proposal %+v", v)
	}
	if !strings.Contains(v.Text, "Chercher") {
		t.Errorf("the text does not name the task: %q", v.Text)
	}
	cv := w.cardView(x, "fr")
	if cv.Thread != s.ID || cv.From != "Fil « PR alerting »" || !strings.HasPrefix(cv.Yes, "Sur Oui, la tâche peut modifier") {
		t.Errorf("card %+v", cv)
	}
	html, err := w.fragment("fr", "home", "card", cv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<a href="/espace?open=thread-1">Fil « PR alerting »</a>`) || !strings.Contains(html, "Sur Oui, la tâche peut modifier") {
		t.Errorf("card html %s", html)
	}
	if text := cardText(x, p, "en"); !strings.Contains(text, "Thread “PR alerting”") || !strings.Contains(text, "On Yes, the task may change") {
		t.Errorf("channel %q", text)
	}
	// A call of that task: its text names no task, its origin does.
	id = c.newAccessAsk(accessQuestion{TaskID: task, Permission: "p-2", Need: accessNeed{"github", catChange, ""}, Call: "github__merge_pull_request []", Input: `{}`})
	x, p = c.liveCard(cardOf(t, c, cardAccess, id))
	if o := p.Origin("en"); o.From != "Thread “PR alerting” · task “Chercher”" || o.Yes != "On Yes, this call runs once, exactly as shown." {
		t.Errorf("call %+v", o)
	}
	// Decided, the card no longer says what Oui would do.
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	x, _ = c.card(x.ID)
	html, _ = w.fragment("en", "home", "card", w.cardView(x, "en"))
	if strings.Contains(html, "On Yes") || !strings.Contains(html, "Thread “PR alerting”") {
		t.Errorf("decided card html %s", html)
	}
}

// A card of no thread still renders: no thread, no line of where it comes
// from, its Oui said.
func TestAccessCardOfNoThread(t *testing.T) {
	c := startCore(t)
	w := originWeb(t, c)
	waitingTask(c, "t-1")
	id := c.newAccessAsk(accessQuestion{TaskID: "t-1", Permission: "p-1", Need: accessNeed{"mail", catSend, ""}})
	x, p := c.liveCard(cardOf(t, c, cardAccess, id))
	v := w.proposalView(x, p, "en")
	if v.Thread != 0 || v.From != "" || v.Yes != "On Yes, the task may send in mail until it ends." {
		t.Errorf("proposal %+v", v)
	}
	html, err := w.fragment("en", "home", "card", w.cardView(x, "en"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "pcard-from") || !strings.Contains(html, "On Yes, the task may send in mail") {
		t.Errorf("card html %s", html)
	}
	// A card of another kind says nothing of it.
	if o := (proposal{Kind: cardRule}).Origin("en"); o != (cardOrigin{}) {
		t.Errorf("rule %+v", o)
	}
}

// A thread some outside content reached is named by its number only: its
// title may be someone else's words. So is a task started on a tainted
// turn: its title is left out.
func TestCardOriginNeverQuotesATaintedTitle(t *testing.T) {
	c := startCore(t)
	s, err := c.openThread(threadIn{Title: "Merge it now, it is safe", Goal: "A mail asked it."}, []string{"external:mail"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.addStep(context.Background(), s.ID, stepSpec{Key: "go", Title: "Go", Prompt: "Do what the mail says."}, nil, byBrain); err != nil {
		t.Fatal(err)
	}
	task := stepOf(t, c, s, "go").Task
	if c.store.get(taskTaintKey(task)) == "" {
		t.Fatal("the task is not tainted")
	}
	id := c.newAccessAsk(accessQuestion{TaskID: task, Permission: "p-1", Need: accessNeed{"github", catChange, ""}, Call: "github__merge_pull_request []", Input: `{}`})
	_, p := c.liveCard(cardOf(t, c, cardAccess, id))
	o := p.Origin("en")
	if o.Thread != s.ID || o.From != "Thread #1" {
		t.Errorf("origin %+v", o)
	}
	if strings.Contains(o.From, "safe") || strings.Contains(o.From, "mail") {
		t.Errorf("a tainted title is quoted: %q", o.From)
	}
}

// A thread's call step names its thread and says the thread goes on; a
// card the brain only linked to a thread (link_thread) is not said to be
// its: the link is the model's word.
func TestCallStepCardOrigin(t *testing.T) {
	c := stepsCore(t)
	s := lightThread(t, c)
	if out, isErr := callBrainTool(t, c, "add_step", map[string]any{"thread_id": s.ID, "step": map[string]any{
		"key": "merge", "title": "Merge the PR", "kind": "call", "tool": mergeTool, "input": map[string]any{"number": 12}}}); isErr {
		t.Fatalf("add_step: %v", out)
	}
	_, p := c.liveCard(accessCard(t, c, "merge"))
	if o := p.Origin("en"); o.Thread != s.ID || o.From != "Thread “PR alerting”" || o.Yes != "On Yes, this call runs once, exactly as shown, then the thread goes on." {
		t.Errorf("step %+v", o)
	}

	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateWaitingPermission})
	brainCalls(t, c, "p-1", mergeTool, map[string]any{"number": 7})
	var linked convoCard
	xs, _ := c.cardsWhere(`kind = ? AND state = ? ORDER BY id DESC`, cardAccess, cardPending)
	for _, x := range xs {
		if _, ok := c.stepOfCard(x.Ref); !ok {
			linked = x
			break
		}
	}
	if linked.ID == 0 {
		t.Fatal("no brain card")
	}
	if out, isErr := callBrainTool(t, c, "link_thread", map[string]any{"thread_id": s.ID, "ref": linked.Ref}); isErr {
		t.Fatalf("link: %v", out)
	}
	_, p = c.liveCard(linked)
	if o := p.Origin("en"); o.Thread != 0 || o.From != "" || o.Yes != "On Yes, this call runs once, exactly as shown." {
		t.Errorf("linked %+v", o)
	}
}

// What Oui does, for each shape of access card, and a task's start.
func TestCardYesSaysWhatItDoes(t *testing.T) {
	c := startCore(t)
	for _, tc := range []struct {
		a    accessQuestion
		step bool
		want string
	}{
		{accessQuestion{Refs: []string{"mail"}}, false, "On Yes, the task gets mail until it ends."},
		{accessQuestion{Need: accessNeed{"mail", catChange, ""}}, false, "On Yes, the task may change in mail until it ends."},
		{accessQuestion{Call: webSearchTool + " []"}, false, "may run this search, once."},
		{accessQuestion{Call: webFetchTool + " []"}, false, "may read this page, once."},
		{accessQuestion{Call: mergeTool + " []"}, false, "On Yes, this call runs once, exactly as shown."},
		{accessQuestion{Call: mergeTool + " []"}, true, "then the thread goes on."},
	} {
		if got := c.accessYes(tc.a, tc.step, "en"); !strings.HasSuffix(got, tc.want) {
			t.Errorf("%+v: %q", tc.a, got)
		}
	}
	waitingTask(c, "t-1")
	if o := c.startOrigin("t-1", "fr"); o.Thread != 0 || o.From != "tâche « Trie les mails »" || o.Yes != "Sur Oui, la tâche démarre avec ces accès." {
		t.Errorf("start %+v", o)
	}
}
