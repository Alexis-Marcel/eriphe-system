package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// talkTurn plays a turn the user starts with a message written over a
// surface (about, "" for none): the brain given it, as brainLoop does.
func talkTurnOf(t *testing.T, c *core, text, about string) int {
	t.Helper()
	c.store.markDelivered(c.store.lastMessageSeq())
	seq, _ := c.store.addMessage(roleUser, text, false)
	if about != "" {
		c.noteAbout(seq, about)
	}
	c.turn.start(int(seq), int(seq), 0)
	msgs, _ := c.store.undelivered()
	c.threadsDelivered(msgs)
	return int(seq)
}

// reply is a line of Eriphe's in the turn, as brain.go writes it.
func reply(c *core, text string) int {
	seq, _ := c.store.addMessage(roleBrain, text, true)
	c.markBrainMessage(int(seq))
	c.fileReply(int(seq))
	return int(seq)
}

// talkLines are a thread's exchanges as the page gets them: role:text,
// an exchange per element.
func talkLines(t *testing.T, c *core, id int64) []string {
	t.Helper()
	all, err := c.threadTalks()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range all[threadSurfaceID(id)] {
		var parts []string
		for _, l := range x.Lines {
			if l.Thread != threadSurfaceID(id) {
				t.Errorf("line %d filed under %q", l.Seq, l.Thread)
			}
			parts = append(parts, l.Role+":"+l.Text)
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

// A message written over a thread's surface and Eriphe's replies of its
// turn are filed under the thread; the visit's lines say so.
func TestThreadTalkFiled(t *testing.T) {
	c := stepsCore(t)
	pr := lightThread(t, c)
	user := talkTurnOf(t, c, "mets plutôt un formulaire Formspree", "surface:"+threadSurfaceID(pr.ID))
	if c.talkThread() != pr.ID {
		t.Fatalf("the turn talks about %d", c.talkThread())
	}
	r1 := reply(c, "D'accord : Formspree.")
	reply(c, "J'ai relancé l'assistant.")
	if got := talkLines(t, c, pr.ID); len(got) != 1 || got[0] != "user:mets plutôt un formulaire Formspree brain:D'accord : Formspree. brain:J'ai relancé l'assistant." {
		t.Fatalf("the thread's exchanges: %q", got)
	}
	lines, threads, err := c.workspaceExchange(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	filed := map[int]string{}
	for _, l := range lines {
		filed[l.Seq] = l.Thread
	}
	if filed[user] != threadSurfaceID(pr.ID) || filed[r1] != threadSurfaceID(pr.ID) {
		t.Errorf("the visit's lines are not filed: %v", filed)
	}
	if len(threads[threadSurfaceID(pr.ID)]) != 1 {
		t.Errorf("the event's threads: %+v", threads)
	}

	// A thread tool naming another thread ends the talk: its replies stay
	// in the flow.
	other, err := c.openThread(threadIn{Title: "Factures", Goal: "Les factures sont envoyées.", Subject: "project:factures"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.turnThread(other.ID)
	r3 := reply(c, "Et les factures sont prêtes.")
	if f := c.talkOf([]int{r3}); f[r3] != "" {
		t.Errorf("a reply about another thread filed under %q", f[r3])
	}

	// The next message, about nothing: not filed, nor its replies; the
	// thread keeps its exchange.
	talkTurnOf(t, c, "merci", "")
	r4 := reply(c, "Avec plaisir.")
	if f := c.talkOf([]int{r4}); f[r4] != "" {
		t.Errorf("a reply about nothing filed under %q", f[r4])
	}
	if got := talkLines(t, c, pr.ID); len(got) != 1 {
		t.Errorf("the thread's exchanges after: %q", got)
	}

	// A reply of a tainted turn is marked so.
	talkTurnOf(t, c, "et le formulaire ?", "surface:"+threadSurfaceID(pr.ID))
	c.turn.taint("channel:mail")
	seq := reply(c, "Le message de test est arrivé.")
	all, _ := c.threadTalks()
	talks := all[threadSurfaceID(pr.ID)]
	last := talks[len(talks)-1].Lines
	if len(talks) != 2 || last[len(last)-1].Seq != seq || !last[len(last)-1].External || last[0].External {
		t.Errorf("a tainted turn's reply: %+v", talks)
	}
}

// off_thread unfiles the turn whole: the user's message was about
// something else, said over the thread's surface (« J'ai quoi de prévu
// aujourd'hui ? »).
func TestThreadTalkOffThread(t *testing.T) {
	c := stepsCore(t)
	pr := lightThread(t, c)
	user := talkTurnOf(t, c, "J'ai quoi de prévu aujourd'hui ?", "surface:"+threadSurfaceID(pr.ID))
	if f := c.talkOf([]int{user}); f[user] != threadSurfaceID(pr.ID) {
		t.Fatalf("the message is not filed: %v", f)
	}
	if out, isErr := callBrainTool(t, c, "off_thread", map[string]any{}); isErr || out["result"] != "about no thread" {
		t.Fatalf("off_thread: %v", out)
	}
	r := reply(c, "Un appel à 10 h et une échéance.")
	if f := c.talkOf([]int{user, r}); len(f) != 0 {
		t.Errorf("filed after off_thread: %v", f)
	}
	if got := talkLines(t, c, pr.ID); len(got) != 0 {
		t.Errorf("the thread's exchanges: %q", got)
	}
}

// A turn about a thread by its event alone, with no message of the
// user's about it, files nothing: it is no exchange with the user.
func TestThreadTalkEventFilesNothing(t *testing.T) {
	c := stepsCore(t)
	pr := lightThread(t, c)
	c.store.markDelivered(c.store.lastMessageSeq())
	seq, _ := c.store.addMessage(roleEvent, "[thread] l'étape a avancé", false)
	c.store.set(threadEventKey(int(seq)), strconv.FormatInt(pr.ID, 10))
	c.turn.start(int(seq), int(seq), int(seq))
	msgs, _ := c.store.undelivered()
	c.threadsDelivered(msgs)
	if c.turn.aboutThread() != pr.ID || c.talkThread() != 0 {
		t.Fatalf("turn about %d, talking about %d", c.turn.aboutThread(), c.talkThread())
	}
	r := reply(c, "L'étape a avancé.")
	if f := c.talkOf([]int{r}); len(f) != 0 {
		t.Errorf("filed: %v", f)
	}
}
