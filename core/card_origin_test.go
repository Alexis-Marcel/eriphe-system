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

// An access card a task raises reads alone: it names the task and says
// what its Oui does, in the workspace's proposal, the conversation's card
// and a channel's message.
func TestAccessCardNamesItsTask(t *testing.T) {
	c := startCore(t)
	w := originWeb(t, c)
	waitingTask(c, "t-1")
	// A grant: its text names the task already.
	id := c.newAccessAsk(accessQuestion{TaskID: "t-1", Permission: "p-1", Need: accessNeed{"github", catChange, ""}})
	x := cardOf(t, c, cardAccess, id)
	x, p := c.liveCard(x)
	v := w.proposalView(x, p, "en")
	if v.Node != "" || v.From != "" || v.Yes != "On Yes, the task may change in github until it ends." {
		t.Errorf("proposal %+v", v)
	}
	if !strings.Contains(v.Text, "Trie les mails") {
		t.Errorf("the text does not name the task: %q", v.Text)
	}
	if fr := w.proposalView(x, p, "fr"); !strings.HasPrefix(fr.Yes, "Sur Oui, la tâche peut modifier") {
		t.Errorf("card %+v", fr)
	}
	if text := cardText(x, p, "en"); !strings.Contains(text, "On Yes, the task may change") {
		t.Errorf("channel %q", text)
	}
	// A call of that task: its text names no task, its origin does.
	id = c.newAccessAsk(accessQuestion{TaskID: "t-1", Permission: "p-2", Need: accessNeed{"github", catChange, ""}, Call: "github__merge_pull_request []", Input: `{}`})
	x, p = c.liveCard(cardOf(t, c, cardAccess, id))
	if o := p.Origin("en"); o.From != "task “Trie les mails”" || o.Yes != "On Yes, this call runs once, exactly as shown." {
		t.Errorf("call %+v", o)
	}
	// Decided, the card still says where it came from.
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	x, _ = c.card(x.ID)
	x, p = c.liveCard(x)
	if v := w.proposalView(x, p, "en"); v.State != cardRejected || v.From != "task “Trie les mails”" {
		t.Errorf("decided card %+v", v)
	}
}

// A card of no node still renders: no node, no line of where it comes
// from, its Oui said.
func TestAccessCardOfNoNode(t *testing.T) {
	c := startCore(t)
	w := originWeb(t, c)
	waitingTask(c, "t-1")
	id := c.newAccessAsk(accessQuestion{TaskID: "t-1", Permission: "p-1", Need: accessNeed{"mail", catSend, ""}})
	x, p := c.liveCard(cardOf(t, c, cardAccess, id))
	v := w.proposalView(x, p, "en")
	if v.Node != "" || v.From != "" || v.Yes != "On Yes, the task may send in mail until it ends." {
		t.Errorf("proposal %+v", v)
	}
	// A card of another kind says nothing of it.
	if o := (proposal{Kind: cardRule}).Origin("en"); o != (cardOrigin{}) {
		t.Errorf("rule %+v", o)
	}
}

// A task started on a tainted turn is named, marked: its title may be
// someone else's words, never passing for a trusted one.
func TestCardOriginMarksATaintedTitle(t *testing.T) {
	c := startCore(t)
	waitingTask(c, "t-1")
	c.store.set(taskTaintKey("t-1"), "external:mail")
	id := c.newAccessAsk(accessQuestion{TaskID: "t-1", Permission: "p-1", Need: accessNeed{"github", catChange, ""}, Call: "github__merge_pull_request []", Input: `{}`})
	_, p := c.liveCard(cardOf(t, c, cardAccess, id))
	if o := p.Origin("en"); !o.External || !strings.HasPrefix(o.From, "task “") || !strings.HasSuffix(o.From, " ⚠ (written after an external read)”") {
		t.Errorf("a tainted title is not marked: %+v", o)
	}
	c.store.set(taskTaintKey("t-1"), "")
	if o := p.Origin("en"); o.External || strings.Contains(o.From, "⚠") || o.From == "" {
		t.Errorf("a clean title is marked: %+v", o)
	}
}

// What Oui does, for each shape of access card, and a task's start.
func TestCardYesSaysWhatItDoes(t *testing.T) {
	c := startCore(t)
	for _, tc := range []struct {
		a    accessQuestion
		want string
	}{
		{accessQuestion{Refs: []string{"mail"}}, "On Yes, the task gets mail until it ends."},
		{accessQuestion{Need: accessNeed{"mail", catChange, ""}}, "On Yes, the task may change in mail until it ends."},
		{accessQuestion{Call: webSearchTool + " []"}, "may run this search, once."},
		{accessQuestion{Call: webFetchTool + " []"}, "may read this page, once."},
		{accessQuestion{Call: "mcp__code__merge []"}, "On Yes, this call runs once, exactly as shown."},
	} {
		if got := c.accessYes(tc.a, "en"); !strings.HasSuffix(got, tc.want) {
			t.Errorf("%+v: %q", tc.a, got)
		}
	}
	waitingTask(c, "t-1")
	if o := c.startOrigin("t-1", "fr"); o.Node != "" || o.From != "tâche « Trie les mails »" || o.Yes != "Sur Oui, la tâche démarre avec ces accès." {
		t.Errorf("start %+v", o)
	}
}
