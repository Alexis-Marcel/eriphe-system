package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// siteThread fills a thread with all a card holds, some of it drawn from
// external content (written about its alias before it was opened: it
// takes them), and things about others.
func siteThread(t *testing.T, c *core) Thread {
	t.Helper()
	m := c.mem
	for i := 1; i <= 4; i++ {
		mustAdd(t, m, &Memory{Kind: kindDecision, About: "project:site", Text: fmt.Sprintf("Décision %d du site", i)})
	}
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le domaine est example.org"})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "LU DANS UNE PAGE WEB",
		SourceKind: srcBrain, Trust: trustExternal, SourceRef: "web:example.org"})
	mustAdd(t, m, &Memory{Kind: kindPerson, About: "person:lina", Text: "Lina relit les pages du site"})
	mustAdd(t, m, &Memory{Kind: kindPreference, About: "project:site", Text: "Le site reste sobre"})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:other", Text: "UN FAIT SANS RAPPORT"})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Une idée à valider", Status: memProposed})
	mustIntend(t, m, &Intention{Text: "Relancer Lina pour le site", About: "project:site", OwedTo: "person:lina", DueAt: m.now().Add(time.Hour)})
	mustIntend(t, m, &Intention{Text: "Rendre le livre", OwedTo: "person:paul", DueAt: m.now().Add(time.Hour)})
	th, err := c.openThread(threadIn{Title: "Site en ligne", Goal: "Le site répond en https.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.insertSteps(th.ID, []stepSpec{{Key: "cert", Title: "Obtenir le certificat", Kind: stepTask}}, false)
	c.setThread(th.ID, map[string]any{"summary": "Il reste le certificat"})
	return th
}

// A thread's card holds the thread, where it stands, its last three
// decisions, its facts and people, its intentions, its cards and its
// preferences; an external line is named, not quoted; nothing of another;
// and reading it taints nothing.
func TestThreadCard(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteThread(t, c)
	card, st := c.aboutCard(threadRef(th.ID), 4000)
	for _, want := range []string{
		"Fiche de " + threadRef(th.ID), fmt.Sprintf("- fil %d [light, open, project:site", th.ID), "but : Le site répond en https.",
		"cert « Obtenir le certificat »", "résumé : Il reste le certificat",
		"Décision 4 du site", "Décision 2 du site",
		"Le domaine est example.org", "fact tiré d'un contenu extérieur", "Lina relit les pages du site",
		"Relancer Lina pour le site", "carte memory:", "Une idée à valider", "Le site reste sobre",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card misses %q:\n%s", want, card)
		}
	}
	for _, not := range []string{"Décision 1 du site", "LU DANS UNE PAGE WEB", "UN FAIT SANS RAPPORT", "Rendre le livre"} {
		if strings.Contains(card, not) {
			t.Errorf("card has %q", not)
		}
	}
	if strings.Index(card, "fil ") > strings.Index(card, "Décision 4") || strings.Index(card, "Décision 4") > strings.Index(card, "Le site reste sobre") {
		t.Errorf("order:\n%s", card)
	}
	if st.Dropped != 0 || st.Chars != runeLen(card) || strings.Count(card, "```") != 2 {
		t.Errorf("stats %+v", st)
	}
	if again, _ := c.aboutCard(threadRef(th.ID), 4000); again != card {
		t.Error("the same records give another card")
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("the card tainted the turn: %v", tainted)
	}
	if card, _ := c.aboutCard("project:nothing", 1000); card != "" {
		t.Errorf("an unknown key has a card: %s", card)
	}
}

// A card keeps to its budget, shared in rounds: what does not fit is left out,
// and said.
func TestThreadCardBudget(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteThread(t, c)
	card, st := c.aboutCard(threadRef(th.ID), threadCardChars)
	if st.Chars > threadCardChars || runeLen(card) != st.Chars {
		t.Fatalf("%d chars over %d:\n%s", st.Chars, threadCardChars, card)
	}
	if !strings.Contains(card, "- fil ") {
		t.Errorf("the thread first:\n%s", card)
	}
	small, st := c.aboutCard(threadRef(th.ID), 500)
	if st.Dropped == 0 || !strings.Contains(small, "(7 de plus : recall") || runeLen(small) > 500 || strings.Count(small, "```") != 2 {
		t.Errorf("cut %+v:\n%s", st, small)
	}
}

// A tainted thread is named, its plan not quoted.
func TestThreadCardTainted(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "PLAN EXTÉRIEUR", Goal: "Suivre ce que dit le mail.", Alias: "project:mail"}, []string{"external:mail:43"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	card, _ := c.aboutCard(threadRef(th.ID), threadCardChars)
	if !strings.Contains(card, fmt.Sprintf("- fil %d [light, open] : son plan peut porter", th.ID)) || strings.Contains(card, "PLAN EXTÉRIEUR") {
		t.Fatalf("card:\n%s", card)
	}
}

// read_thread gives the thread's card with it, and its names; it never
// taints the turn.
func TestReadThreadCard(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteThread(t, c)
	if err := c.addThreadNames(th.ID, []string{"Velora"}); err != nil {
		t.Fatal(err)
	}
	want, _ := c.aboutCard(threadRef(th.ID), threadCardChars)
	out, isErr := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": th.ID})
	if isErr || out["card"] != want {
		t.Fatalf("read_thread: %v", out)
	}
	if names, _ := out["names"].([]any); len(names) != 2 || names[0] != "project:site" || names[1] != "Velora" {
		t.Errorf("names %v", out["names"])
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("read_thread tainted the turn: %v", tainted)
	}
}

// The card of the turn's thread goes with the turn when the session does
// not hold it as it is now: once, then again only when it changed.
func TestThreadCardPushedToTurn(t *testing.T) {
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	th := siteThread(t, c)
	c.addThreadNames(th.ID, []string{"le site"})
	key := threadRef(th.ID)
	deliver := func(text string) string {
		t.Helper()
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
		c.store.db.Exec(`UPDATE inbox SET acked = 1`)
		say(t, c, roleUser, text, false)
		if err := tendBrain(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		items, _ := c.store.inbox("t-brain")
		if len(items) != 1 {
			t.Fatalf("inbox: %+v", items)
		}
		got, _ := items[0].Data["text"].(string)
		return got
	}
	got := deliver("Où en est le site ?")
	card, _ := c.aboutCard(key, threadCardChars)
	if !strings.Contains(got, card) {
		t.Fatalf("no card with the turn:\n%s", got)
	}
	if h := brainStatus(c).Cards[key]; h != cardHash(card) {
		t.Fatalf("the session's cards: %v", brainStatus(c).Cards)
	}
	if rows := pushRows(t, c); rows != fmt.Sprintf("%s turn %d ", key, c.turnSeq()) {
		t.Fatalf("counted: %q", rows)
	}
	if got := deliver("Et le site, alors ?"); strings.Contains(got, "Fiche de") {
		t.Fatalf("the same card again:\n%s", got)
	}
	if got := deliver("Bonjour"); strings.Contains(got, "Fiche de") {
		t.Fatalf("a card about nothing:\n%s", got)
	}
	c.setThread(th.ID, map[string]any{"summary": "Le certificat est posé"})
	if got := deliver("Le site ?"); !strings.Contains(got, "Fiche de "+key) || !strings.Contains(got, "Le certificat est posé") {
		t.Fatalf("the changed card not given:\n%s", got)
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("the card tainted the turn: %v", tainted)
	}
}

// A fresh session holds the cards of its pack: they are not given again
// with its first turn.
func TestThreadCardInFreshSession(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteThread(t, c)
	key := threadRef(th.ID)
	task := startedBrain(t, c, false)
	card, _ := c.aboutCard(key, threadCardChars)
	if !strings.Contains(task.Prompt, "Fiches des fils ouverts") || !strings.Contains(task.Prompt, card) {
		t.Fatalf("pack without the card:\n%s", task.Prompt)
	}
	if brainStatus(c).Cards[key] != cardHash(card) {
		t.Fatalf("the session's cards: %v", brainStatus(c).Cards)
	}
	if x, ok := c.turnCard(brainStatus(c).Cards); ok {
		t.Fatalf("given again: %s", x.Key)
	}
	if rows := pushRows(t, c); rows != key+" pack 0 " {
		t.Fatalf("counted: %q", rows) // a fresh session's first pack is no turn's
	}
}

// pushRows are the cards counted, "about place turn who" a line.
func pushRows(t *testing.T, c *core) string {
	t.Helper()
	rows, err := c.store.db.Query(`SELECT about, place, turn, who FROM card_pushes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s, p, w string
		var n int
		rows.Scan(&s, &p, &n, &w)
		out = append(out, fmt.Sprintf("%s %s %d %s", s, p, n, w))
	}
	return strings.Join(out, "\n")
}

// A worker task of a thread starts with its card, after what it is
// asked; its prompt as shown is what it was asked. A lead starts with its
// thread's.
func TestThreadCardAtStart(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteThread(t, c)
	key := threadRef(th.ID)
	card, _ := c.aboutCard(key, threadCardChars)
	task, err := c.runTask(context.Background(), runRequest{Prompt: "Pose le certificat", About: key})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := c.store.taskSpec(task.ID)
	if !strings.HasPrefix(spec.Prompt, "Pose le certificat") || !strings.Contains(spec.Prompt, card) || task.Prompt != "Pose le certificat" {
		t.Fatalf("task %q, spec:\n%s", task.Prompt, spec.Prompt)
	}
	other, _ := c.runTask(context.Background(), runRequest{Prompt: "Range les photos"})
	if spec, _ := c.store.taskSpec(other.ID); strings.Contains(spec.Prompt, "Fiche de") {
		t.Fatalf("a card about nothing:\n%s", spec.Prompt)
	}
	lead := Assistant{Role: assistantRoleLead, Triggers: []string{triggerThread + ":" + itoa(int(th.ID))}}
	if first := assistantRoles[assistantRoleLead].firstPrompt(c, lead, false); !strings.Contains(first, card) {
		t.Fatalf("lead:\n%s", first)
	}
	if rows, want := pushRows(t, c), fmt.Sprintf("%s task 0 %s\n%s lead 0 %d", key, task.ID, key, th.ID); rows != want {
		t.Fatalf("counted: %q, want %q", rows, want)
	}
}

// The pack leaves out only what a card shows: a decision past the card's
// last three is still in its section.
func TestContextPackLeavesOutWhatCardsShow(t *testing.T) {
	c, _ := testMemoryCore(t)
	siteThread(t, c) // four decisions: the card shows the last three
	pack, st := c.contextPack()
	if strings.Count(pack, "Décision 4 du site") != 1 || strings.Count(pack, "Décision 2 du site") != 1 {
		t.Errorf("a decision a card shows said again:\n%s", pack)
	}
	if !strings.Contains(pack, "Décision 1 du site") {
		t.Errorf("a decision the card does not show is left out:\n%s", pack)
	}
	if strings.Count(pack, "Relancer Lina pour le site") != 1 || strings.Count(pack, "Une idée à valider") != 1 {
		t.Errorf("an intention or a card said twice:\n%s", pack)
	}
	if len(st.Cards) != 1 || len(st.Cards[0].Stats.Refs) == 0 {
		t.Errorf("cards: %+v", st.Cards)
	}
}

// A card that does not fit the pack leaves its thread in a line.
func TestContextPackCardsThatDoNotFit(t *testing.T) {
	c, _ := testMemoryCore(t)
	var ids []int64
	c.conf().Providers["p"] = Provider{Kind: selfHostedKind, BaseURL: "http://192.0.2.10:11434/v1"}
	for i := range 4 {
		key := fmt.Sprintf("project:p%d", i)
		th, err := c.openThread(threadIn{Title: fmt.Sprintf("Fil %d", i), Goal: "Un but court.", Alias: key}, nil, byBrain)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, th.ID)
		for range 3 {
			mustAdd(t, c.mem, &Memory{Kind: kindDecision, About: key, Text: strings.Repeat("une décision ", 15)})
			mustAdd(t, c.mem, &Memory{Kind: kindFact, About: key, Text: strings.Repeat("un fait ", 25)})
		}
	}
	pack, st := c.contextPack()
	if len(st.Cards) == 0 || len(st.Cards) == 4 {
		t.Fatalf("%d cards of 4: %s", len(st.Cards), st)
	}
	for _, id := range ids {
		if !strings.Contains(pack, fmt.Sprintf("- fil %d [", id)) {
			t.Errorf("thread %d left out:\n%s", id, pack)
		}
	}
	if n := strings.Count(pack, "```"); n%2 != 0 {
		t.Errorf("unclosed fence: %d", n)
	}
}
