package main

import (
	"testing"
	"time"
)

// pushCard pushes a card as the code does, at a place.
func pushCard(t *testing.T, c *core, key, place string, turn int, who string) {
	t.Helper()
	x, ok := c.compileCard(key, threadCardChars)
	if !ok {
		t.Fatalf("no card for %s", key)
	}
	c.cardPushed(x, place, turn, who)
}

func usesOf(c *core, about string) map[string]cardUse {
	out := map[string]cardUse{}
	for _, u := range c.store.cardUses(time.Now().Add(-time.Hour), about) {
		out[u.Place] = u
	}
	return out
}

// Each card pushed is counted, and judged once its window is over: wrote
// when the model it went to wrote a record about it, answered when the
// brain answered in its turn without asking for it again, no otherwise.
func TestCardUses(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.mem.now = time.Now
	site := siteThread(t, c)
	mustAdd(t, c.mem, &Memory{Kind: kindFact, About: "project:blog", Text: "Le blog tourne sur le NAS"})
	mustAdd(t, c.mem, &Memory{Kind: kindFact, About: "project:shop", Text: "La boutique vend des tasses"})

	// A turn's card, then the brain writes a decision of the subject: wrote.
	turn := ownerSays(c, "On garde Astro pour le site")
	pushCard(t, c, "project:site", placeTurn, c.turnSeq(), "")
	if _, err := c.brainRemember(rememberIn{Kind: "decision", About: "project:site", Text: "Astro plutôt que WordPress", SourceRef: ref(turn)}); err != nil {
		t.Fatal(err)
	}
	// A turn's card the brain answers with, asking nothing: answered.
	ownerSays(c, "Le blog marche ?")
	pushCard(t, c, "project:blog", placeTurn, c.turnSeq(), "")
	c.store.addMessage(roleBrain, "Oui, il tourne.", true)
	// A turn's card the brain looks up again before answering: no.
	ownerSays(c, "Et la boutique ?")
	pushCard(t, c, "project:shop", placeTurn, c.turnSeq(), "")
	if _, err := c.brainRecall(recallIn{Query: "boutique tasses", About: "project:shop"}); err != nil {
		t.Fatal(err)
	}
	c.store.addMessage(roleBrain, "Elle vend des tasses.", true)
	// A task's card, and its remember of the subject: wrote; another
	// task's that writes nothing: no.
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "nas", Runtime: "claude-code", State: stateRunning, About: "project:blog"})
	pushCard(t, c, "project:blog", placeTask, 0, "t-1")
	if _, err := c.taskRemember("t-1", taskRememberIn{Kind: "fact", About: "project:blog", Text: "Le blog a un flux RSS"}); err != nil {
		t.Fatal(err)
	}
	pushCard(t, c, "project:shop", placeTask, 0, "t-2")

	// Before their window is over, they wait.
	c.judgeCardPushes(time.Now())
	if u := usesOf(c, "")[placeTurn]; u.Pushed != 3 || u.Waiting != 3 {
		t.Fatalf("waiting: %s", u)
	}
	// The brain's are judged after half an hour; the tasks' wait longer.
	c.judgeCardPushes(time.Now().Add(cardUseWindow + time.Minute))
	all := usesOf(c, "")
	if u := all[placeTurn]; u.Wrote != 1 || u.Answered != 1 || u.Unused != 1 || u.Waiting != 0 || u.Used() != 2 {
		t.Errorf("turn: %s", u)
	}
	if u := all[placeTask]; u.Pushed != 2 || u.Waiting != 2 {
		t.Errorf("task before its window: %s", u)
	}
	c.judgeCardPushes(time.Now().Add(cardUseLongWindow + time.Minute))
	if u := usesOf(c, "")[placeTask]; u.Wrote != 1 || u.Unused != 1 {
		t.Errorf("task: %s", u)
	}
	if u := usesOf(c, threadRef(site.ID))[placeTurn]; u.Pushed != 1 || u.Wrote != 1 {
		t.Errorf("the site's: %s", u)
	}
	// The tasks' panel says how their cards were used.
	if st := c.store.stats(time.Now().Add(-time.Hour)); st.Cards.Pushed != 2 || st.Cards.Used() != 1 || st.Cards.Judged() != 2 {
		t.Errorf("stats: %+v", st.Cards)
	}
	// read_thread is asking again too.
	photos, err := c.openThread(threadIn{Title: "Photos", Goal: "Rangées."}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c.mem, &Memory{Kind: kindFact, About: threadRef(photos.ID), Text: "Les photos sont sur le NAS"})
	ownerSays(c, "Les photos ?")
	c.store.db.Exec(`UPDATE thread_log SET at = at - 3600 WHERE thread = ?`, photos.ID) // its opening is no use of the card
	pushCard(t, c, threadRef(photos.ID), placeTurn, c.turnSeq(), "")
	if _, isErr := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": photos.ID}); isErr {
		t.Fatal("read_thread")
	}
	c.store.addMessage(roleBrain, "Sur le NAS.", true)
	c.judgeCardPushes(time.Now().Add(cardUseWindow + time.Minute))
	if u := usesOf(c, threadRef(photos.ID))[placeTurn]; u.Pushed != 1 || u.Unused != 1 {
		t.Errorf("read_thread: %s", u)
	}
}

// A pack delivered with a turn: only the card of what the turn is about
// is the turn's; one answer counts for it alone.
func TestPackCardsOfTurn(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteThread(t, c)
	mustAdd(t, c.mem, &Memory{Kind: kindFact, About: "project:blog", Text: "Le blog tourne sur le NAS"})
	site, _ := c.compileCard("project:site", threadCardChars) // its alias: the thread's card
	blog, _ := c.compileCard("project:blog", threadCardChars)
	c.packedCards([]pushedCard{site, blog}, 7, threadRef(th.ID))
	if rows, want := pushRows(t, c), threadRef(th.ID)+" pack 7 \nproject:blog pack 0 "; rows != want {
		t.Fatalf("counted: %q, want %q", rows, want)
	}
}
