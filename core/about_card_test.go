package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// siteNode is a node at work with all a card holds, some of it drawn from
// external content, and things about others. It opens the graph when c
// has none.
func siteNode(t *testing.T, c *core) string {
	t.Helper()
	if c.nodes == nil {
		openGraph(t, c)
	}
	d, err := c.nodes.Create(nodestore.New{Title: "Site en ligne", Goal: "Le site répond en https.", Body: "Il reste le certificat.\n\nLe reste du doc.", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	c.nodes.SetState(id, nodestore.Running)
	key, m := nodeRef(id), c.mem
	for i := 1; i <= 4; i++ {
		mustAdd(t, m, &Memory{Kind: kindDecision, About: key, Text: fmt.Sprintf("Décision %d du site", i)})
	}
	mustAdd(t, m, &Memory{Kind: kindFact, About: key, Text: "Le domaine est example.org"})
	mustAdd(t, m, &Memory{Kind: kindFact, About: key, Text: "LU DANS UNE PAGE WEB",
		SourceKind: srcBrain, Trust: trustExternal, SourceRef: "web:example.org"})
	mustAdd(t, m, &Memory{Kind: kindPerson, About: "person:lina", Text: "Lina relit les pages du site"})
	mustAdd(t, m, &Memory{Kind: kindPreference, About: key, Text: "Le site reste sobre"})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:other", Text: "UN FAIT SANS RAPPORT"})
	mustAdd(t, m, &Memory{Kind: kindFact, About: key, Text: "Une idée à valider", Status: memProposed})
	mustIntend(t, m, &Intention{Text: "Relancer Lina pour le site", About: key, OwedTo: "person:lina", DueAt: m.now().Add(time.Hour)})
	mustIntend(t, m, &Intention{Text: "Rendre le livre", OwedTo: "person:paul", DueAt: m.now().Add(time.Hour)})
	return id
}

// A node's card holds the node (its state, title and the first line of
// its doc), its last three decisions, its facts and people, its
// intentions, its cards and its preferences; an external line is named,
// not quoted; nothing of another; and reading it taints nothing.
func TestAboutCard(t *testing.T) {
	c, _ := testMemoryCore(t)
	id := siteNode(t, c)
	key := nodeRef(id)
	card, st := c.aboutCard(key, 4000)
	for _, want := range []string{
		"Fiche de " + key, "- nœud " + id + " [running", "« Site en ligne » : Il reste le certificat.",
		"Décision 4 du site", "Décision 2 du site",
		"Le domaine est example.org", "fact tiré d'un contenu extérieur", "Lina relit les pages du site",
		"Relancer Lina pour le site", "carte memory:", "Une idée à valider", "Le site reste sobre",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card misses %q:\n%s", want, card)
		}
	}
	for _, not := range []string{"Décision 1 du site", "LU DANS UNE PAGE WEB", "UN FAIT SANS RAPPORT", "Rendre le livre", "Le reste du doc", "https"} {
		if strings.Contains(card, not) {
			t.Errorf("card has %q", not)
		}
	}
	if strings.Index(card, "nœud ") > strings.Index(card, "Décision 4") || strings.Index(card, "Décision 4") > strings.Index(card, "Le site reste sobre") {
		t.Errorf("order:\n%s", card)
	}
	if st.Dropped != 0 || st.Chars != runeLen(card) || strings.Count(card, "```") != 2 {
		t.Errorf("stats %+v", st)
	}
	if again, _ := c.aboutCard(key, 4000); again != card {
		t.Error("the same records give another card")
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("the card tainted the turn: %v", tainted)
	}
	if card, _ := c.aboutCard("project:nothing", 1000); card != "" {
		t.Errorf("an unknown key has a card: %s", card)
	}
}

// A card keeps to its budget, shared in rounds: what does not fit is left
// out, and said.
func TestAboutCardBudget(t *testing.T) {
	c, _ := testMemoryCore(t)
	key := nodeRef(siteNode(t, c))
	card, st := c.aboutCard(key, aboutCardChars)
	if st.Chars > aboutCardChars || runeLen(card) != st.Chars {
		t.Fatalf("%d chars over %d:\n%s", st.Chars, aboutCardChars, card)
	}
	if !strings.Contains(card, "- nœud ") {
		t.Errorf("the node first:\n%s", card)
	}
	small, st := c.aboutCard(key, 500)
	if st.Dropped == 0 || !strings.Contains(small, "de plus : recall") || runeLen(small) > 500 || strings.Count(small, "```") != 2 {
		t.Errorf("cut %+v:\n%s", st, small)
	}
}

// A node that may carry someone else's words is named, its title not
// quoted.
func TestAboutCardExternal(t *testing.T) {
	c, _ := testMemoryCore(t)
	openGraph(t, c)
	d, err := c.nodes.Create(nodestore.New{Title: "PLAN EXTÉRIEUR", Body: "SUIVRE LE MAIL", External: true, Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	card, _ := c.aboutCard(nodeRef(id), aboutCardChars)
	if !strings.Contains(card, "- nœud "+id+" [idle] : il peut porter") || strings.Contains(card, "PLAN EXTÉRIEUR") || strings.Contains(card, "SUIVRE") {
		t.Fatalf("card:\n%s", card)
	}
}

// The card of the turn's node goes with the turn when the session does
// not hold it as it is now: once, then again only when it changed.
func TestAboutCardPushedToTurn(t *testing.T) {
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	id := siteNode(t, c)
	key := nodeRef(id)
	deliver := func(text, node string) string {
		t.Helper()
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
		c.store.db.Exec(`UPDATE inbox SET acked = 1`)
		if _, err := c.store.addUserMessage(text, nil, node); err != nil {
			t.Fatal(err)
		}
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
	got := deliver("Où en est le site ?", id)
	card, _ := c.aboutCard(key, aboutCardChars)
	if !strings.Contains(got, card) || !strings.Contains(got, "· nœud "+id) {
		t.Fatalf("no card with the turn:\n%s", got)
	}
	if h := brainStatus(c).Cards[key]; h != cardHash(card) {
		t.Fatalf("the session's cards: %v", brainStatus(c).Cards)
	}
	if rows := pushRows(t, c); rows != fmt.Sprintf("%s turn %d ", key, c.turnSeq()) {
		t.Fatalf("counted: %q", rows)
	}
	if got := deliver("Et alors ?", id); strings.Contains(got, "Fiche de") {
		t.Fatalf("the same card again:\n%s", got)
	}
	if got := deliver("Bonjour", ""); strings.Contains(got, "Fiche de") {
		t.Fatalf("a card about nothing:\n%s", got)
	}
	d, _ := c.nodes.Read(id, 0)
	if _, err := c.nodes.Write(id, "Le certificat est posé.", d.Version.Hash, byBrain); err != nil {
		t.Fatal(err)
	}
	if got := deliver("Le site ?", id); !strings.Contains(got, "Fiche de "+key) || !strings.Contains(got, "Le certificat est posé") {
		t.Fatalf("the changed card not given:\n%s", got)
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("the card tainted the turn: %v", tainted)
	}
}

// A fresh session holds the cards of its pack: they are not given again
// with its first turn.
func TestAboutCardInFreshSession(t *testing.T) {
	c, _ := testMemoryCore(t)
	key := nodeRef(siteNode(t, c))
	task := startedBrain(t, c, false)
	card, _ := c.aboutCard(key, aboutCardChars)
	if !strings.Contains(task.Prompt, "Fiches des nœuds en cours et en attente") || !strings.Contains(task.Prompt, card) {
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

// A worker task about a node starts with its card, after what it is
// asked; its prompt as shown is what it was asked.
func TestAboutCardAtStart(t *testing.T) {
	c, _ := testMemoryCore(t)
	key := nodeRef(siteNode(t, c))
	card, _ := c.aboutCard(key, aboutCardChars)
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
	if rows, want := pushRows(t, c), fmt.Sprintf("%s task 0 %s", key, task.ID); !strings.HasPrefix(rows, want) {
		t.Fatalf("counted: %q, want %q", rows, want)
	}
}

// The pack lists the nodes at work and waiting with their cards, and
// leaves out only what a card shows: a decision past the card's last
// three is still in its section.
func TestContextPackLeavesOutWhatCardsShow(t *testing.T) {
	c, _ := testMemoryCore(t)
	siteNode(t, c) // four decisions: the card shows the last three
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

// The pack lists every node at work or waiting for the user, with its
// title, state and the first line of its doc; a card that does not fit
// leaves its node in a line; an idle or a done node is not listed.
func TestContextPackListsActiveNodes(t *testing.T) {
	c, _ := testMemoryCore(t)
	openGraph(t, c)
	c.conf().Providers["p"] = Provider{Kind: selfHostedKind, BaseURL: "http://192.0.2.10:11434/v1"}
	var ids []string
	for i := range 4 {
		d, err := c.nodes.Create(nodestore.New{Title: fmt.Sprintf("Travail %d", i), Body: fmt.Sprintf("Où en est le travail %d.", i), Author: byBrain})
		if err != nil {
			t.Fatal(err)
		}
		id := d.Node.Meta.ID
		state := nodestore.Running
		if i%2 == 1 {
			state = nodestore.Waiting
		}
		c.nodes.SetState(id, state)
		ids = append(ids, id)
		for range 3 {
			mustAdd(t, c.mem, &Memory{Kind: kindDecision, About: nodeRef(id), Text: strings.Repeat("une décision ", 15)})
			mustAdd(t, c.mem, &Memory{Kind: kindFact, About: nodeRef(id), Text: strings.Repeat("un fait ", 25)})
		}
	}
	idle, _ := c.nodes.Create(nodestore.New{Title: "Au repos", Author: byBrain})
	pack, st := c.contextPack()
	if len(st.Cards) == 0 || len(st.Cards) == 4 {
		t.Fatalf("%d cards of 4: %s", len(st.Cards), st)
	}
	for i, id := range ids {
		state := "running"
		if i%2 == 1 {
			state = "waiting"
		}
		if !strings.Contains(pack, fmt.Sprintf("- nœud %s [%s", id, state)) || !strings.Contains(pack, fmt.Sprintf("« Travail %d » : Où en est le travail %d.", i, i)) {
			t.Errorf("node %s left out:\n%s", id, pack)
		}
	}
	if strings.Contains(pack, idle.Node.Meta.ID) {
		t.Errorf("an idle node listed:\n%s", pack)
	}
	if n := strings.Count(pack, "```"); n%2 != 0 {
		t.Errorf("unclosed fence: %d", n)
	}
}
