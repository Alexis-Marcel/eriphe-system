package main

import (
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// pushCard pushes a card as the code does, at a place.
func pushCard(t *testing.T, c *core, key, place string, turn int, who string) {
	t.Helper()
	x, ok := c.compileCard(key, aboutCardChars)
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
	site := siteNode(t, c)
	siteKey := nodeRef(site)
	mustIntend(t, c.mem, &Intention{Text: "Vérifier le blog sur le NAS", About: "project:blog", DueAt: time.Now().Add(time.Hour)})
	mustIntend(t, c.mem, &Intention{Text: "Vendre des tasses", About: "project:shop", DueAt: time.Now().Add(time.Hour)})

	// A turn's card, then the brain writes an intention of the subject: wrote.
	turn := ownerSays(c, "Rappelle-moi de passer le site sur Astro")
	pushCard(t, c, siteKey, placeTurn, c.turnSeq(), "")
	if _, err := c.brainIntend(intendIn{Text: "Passer le site sur Astro", About: siteKey, SourceRef: ref(turn)}); err != nil {
		t.Fatal(err)
	}
	// A turn's card the brain answers with, asking nothing: answered.
	ownerSays(c, "Le blog marche ?")
	pushCard(t, c, "project:blog", placeTurn, c.turnSeq(), "")
	c.store.addMessage(roleBrain, "Oui, il tourne.", true)
	// A turn's card the brain does not answer with: no.
	ownerSays(c, "Et la boutique ?")
	pushCard(t, c, "project:shop", placeTurn, c.turnSeq(), "")
	// A task's card, and its write of the node: wrote; another task's
	// that writes nothing: no.
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Device: "nas", Runtime: "claude-code", State: stateRunning, About: siteKey})
	pushCard(t, c, siteKey, placeTask, 0, "t-1")
	d, _ := c.nodes.Read(site, 0)
	if _, err := nodeWrite(c, site, "Le site est sur Astro.", d.Version.Hash, "task:t-1"); err != nil {
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
	if u := usesOf(c, siteKey)[placeTurn]; u.Pushed != 1 || u.Wrote != 1 {
		t.Errorf("the site's: %s", u)
	}
	// The tasks' panel says how their cards were used.
	if st := c.store.stats(time.Now().Add(-time.Hour)); st.Cards.Pushed != 2 || st.Cards.Used() != 1 || st.Cards.Judged() != 2 {
		t.Errorf("stats: %+v", st.Cards)
	}
	// node_read is asking again too.
	pd, err := c.nodes.Create(nodestore.New{Title: "Photos", Author: byUser}) // its opening is no use of the card
	if err != nil {
		t.Fatal(err)
	}
	photos := pd.Node.Meta.ID
	ownerSays(c, "Les photos ?")
	pushCard(t, c, nodeRef(photos), placeTurn, c.turnSeq(), "")
	if _, isErr := callBrainTool(t, c, "node_read", map[string]any{"id": photos}); isErr {
		t.Fatal("node_read")
	}
	c.store.addMessage(roleBrain, "Sur le NAS.", true)
	c.judgeCardPushes(time.Now().Add(cardUseWindow + time.Minute))
	if u := usesOf(c, nodeRef(photos))[placeTurn]; u.Pushed != 1 || u.Unused != 1 {
		t.Errorf("node_read: %s", u)
	}
}

// A pack delivered with a turn: only the card of what the turn is about
// is the turn's; one answer counts for it alone.
func TestPackCardsOfTurn(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := nodeRef(siteNode(t, c))
	mustIntend(t, c.mem, &Intention{Text: "Vérifier le blog sur le NAS", About: "project:blog", DueAt: c.mem.now().Add(time.Hour)})
	site, _ := c.compileCard(th, aboutCardChars)
	blog, _ := c.compileCard("project:blog", aboutCardChars)
	c.packedCards([]pushedCard{site, blog}, 7, th)
	if rows, want := pushRows(t, c), th+" pack 7 \nproject:blog pack 0 "; rows != want {
		t.Fatalf("counted: %q, want %q", rows, want)
	}
}
