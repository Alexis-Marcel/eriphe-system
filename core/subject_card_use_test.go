package main

import (
	"strings"
	"testing"
	"time"
)

// pushCard pushes a subject's card as the code does, at a place.
func pushCard(t *testing.T, c *core, key, place string, turn int, who string) {
	t.Helper()
	x, ok := c.compileCard(key, subjectCardChars)
	if !ok {
		t.Fatalf("no card for %s", key)
	}
	c.cardPushed(x, place, turn, who)
}

func usesOf(c *core, subject string) map[string]cardUse {
	out := map[string]cardUse{}
	for _, u := range c.store.cardUses(time.Now().Add(-time.Hour), subject) {
		out[u.Place] = u
	}
	return out
}

// Each card pushed is counted, and judged once its window is over: wrote
// when the model it went to wrote a record of its subject, answered when
// the brain answered in its turn without asking for the subject again, no
// otherwise.
func TestCardUses(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.mem.now = time.Now
	siteSubject(t, c)
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Subject: "project:blog", Text: "Le blog tourne sur le NAS"})
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Subject: "project:shop", Text: "La boutique vend des tasses"})

	// A turn's card, then the brain writes a decision of the subject: wrote.
	turn := ownerSays(c, "On garde Astro pour le site")
	pushCard(t, c, "project:site", placeTurn, c.turnSeq(), "")
	if _, err := c.brainRemember(rememberIn{Kind: "decision", Subject: "project:site", Text: "Astro plutôt que WordPress", SourceRef: ref(turn)}); err != nil {
		t.Fatal(err)
	}
	// A turn's card the brain answers with, asking nothing: answered.
	ownerSays(c, "Le blog marche ?")
	pushCard(t, c, "project:blog", placeTurn, c.turnSeq(), "")
	c.store.addMessage(roleBrain, "Oui, il tourne.", true)
	// A turn's card the brain looks up again before answering: no.
	ownerSays(c, "Et la boutique ?")
	pushCard(t, c, "project:shop", placeTurn, c.turnSeq(), "")
	if _, err := c.brainRecall(recallIn{Query: "boutique tasses", Subject: "project:shop"}); err != nil {
		t.Fatal(err)
	}
	c.store.addMessage(roleBrain, "Elle vend des tasses.", true)
	// A task's card, and its remember of the subject: wrote; another
	// task's that writes nothing: no.
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "nas", Runtime: "claude-code", State: stateRunning, Subject: "project:blog"})
	pushCard(t, c, "project:blog", placeTask, 0, "t-1")
	if _, err := c.taskRemember("t-1", taskRememberIn{Kind: "fact", Subject: "project:blog", Text: "Le blog a un flux RSS"}); err != nil {
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
	if u := usesOf(c, "project:site")[placeTurn]; u.Pushed != 1 || u.Wrote != 1 {
		t.Errorf("the site's: %s", u)
	}
	// The tasks' panel says how their cards were used.
	if st := c.store.stats(time.Now().Add(-time.Hour)); st.Cards.Pushed != 2 || st.Cards.Used() != 1 || st.Cards.Judged() != 2 {
		t.Errorf("stats: %+v", st.Cards)
	}
	// read_subject is asking again too.
	ownerSays(c, "Le blog ?")
	pushCard(t, c, "project:blog", placeTurn, c.turnSeq(), "")
	c.readSubject(readSubjectIn{Subject: "project:blog"})
	c.store.addMessage(roleBrain, "Il tourne.", true)
	c.judgeCardPushes(time.Now().Add(cardUseWindow + time.Minute))
	if u := usesOf(c, "project:blog")[placeTurn]; u.Pushed != 2 || u.Answered != 1 || u.Unused != 1 {
		t.Errorf("read_subject: %s", u)
	}
}

// A pack delivered with a turn: only the card of the turn's subject is
// the turn's; one answer counts for it alone.
func TestPackCardsOfTurn(t *testing.T) {
	c, _ := testMemoryCore(t)
	siteSubject(t, c)
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Subject: "project:blog", Text: "Le blog tourne sur le NAS"})
	site, _ := c.compileCard("project:site", subjectCardChars)
	blog, _ := c.compileCard("project:blog", subjectCardChars)
	c.packedCards([]pushedCard{site, blog}, 7, "project:site")
	if rows := pushRows(t, c); rows != "project:site pack 7 \nproject:blog pack 0 " {
		t.Fatalf("counted: %q", rows)
	}
}

// Réglages → Sujets shows the counts, all and the picked subject's.
func TestSubjectsPageCardUses(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	siteSubject(t, tw.c)
	pushCard(t, tw.c, "project:site", placeTask, 0, "t-1")
	_, body, _ := tw.do("GET", "/settings/subjects?s=project:site", nil, nil)
	if !strings.Contains(body, "Fiches poussées · 30 jours") || strings.Count(body, `<th scope="row">Tâche</th>`) != 2 {
		t.Fatalf("counts:\n%s", body)
	}
	_, body, _ = tw.do("GET", "/settings/subjects", nil, map[string]string{"Cookie": "lang=en"})
	if !strings.Contains(body, "Cards pushed · 30 days") || !strings.Contains(body, `<th scope="row">Task</th>`) {
		t.Fatalf("counts in English:\n%s", body)
	}
}
