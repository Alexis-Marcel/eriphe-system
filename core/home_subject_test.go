package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A subject asked at the home opens its node, and its cards live there
// (node_turn.go's moveHomeTurn, web_console.go's unheldCards).

// messageAt is the message seq.
func messageAt(t *testing.T, c *core, seq int) Message {
	t.Helper()
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq = ?`, seq)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("message %d: %v %v", seq, msgs, err)
	}
	return msgs[0]
}

// cardOfRef is the card of a proposal.
func cardOfRef(t *testing.T, c *core, ref string) convoCard {
	t.Helper()
	xs, err := c.cardsWhere(`ref = ?`, ref)
	if err != nil || len(xs) != 1 {
		t.Fatalf("card %s: %v %v", ref, xs, err)
	}
	return xs[0]
}

// At the home, « keep an eye on my mails » opens its subject: the user's
// message and the brain's answer belong to it, the page opens with the
// console in it; the view proposed there is the subject's card, at the
// foot of its page; its yes makes the standing node and opens it.
func TestHomeSubjectTakesTheExchange(t *testing.T) {
	c, answer := netCore(t, "Tu peux garder un œil sur mes mails ?", "")
	asked := c.turn.answer
	out, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Garder un œil sur mes mails", Body: "Ce qu'on suit, et comment.\n"})
	if err != nil {
		t.Fatal(err)
	}
	subject := out["id"].(string)
	answer("Lesquels veux-tu suivre : tous, ou ceux qui demandent une réponse ?")
	if m := messageAt(t, c, asked); m.Node != subject {
		t.Errorf("the request stayed at the home: %+v", m)
	}
	if m := lastBrainMessage(t, c); m.Node != subject || m.Filed != "" {
		t.Errorf("the answer: %+v", m)
	}
	if x, ok := c.lastShown(); !ok || x.Node != subject || !x.Talk {
		t.Errorf("the subject is not opened with the console: %+v", x)
	}
	if lines, _ := c.store.lastExchange(subject); len(lines) != 2 || lines[0].Role != roleUser {
		t.Errorf("the subject's exchange: %+v", lines)
	}

	// The user answers in the subject; the view is proposed there.
	seq, _ := c.store.addUserMessage("Ceux qui demandent une réponse.", nil, subject)
	c.turn.start(int(seq), int(seq), 0)
	c.turn.answersIn(subject)
	c.nodesDelivered([]Message{{Seq: int(seq), Role: roleUser, Node: subject}})
	res, err := c.proposeView(proposeViewIn{Title: "Mes mails", Body: "Les mails qui demandent une réponse.\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := res["node"].(string)
	answer(longAnswer) // the exchange about the card: no new node for it
	if m := lastBrainMessage(t, c); m.Filed != "" || m.Node != subject {
		t.Errorf("the answer about the card was filed: %+v", m)
	}
	ref := proposalRef(cardViewKind, 1)
	if !slices.Contains(c.cardNodes(ref), subject) || !c.nodeCardWaits(subject) {
		t.Fatalf("the view's card does not wait on its subject: %v", c.cardNodes(ref))
	}
	w := &web{c: c}
	if x, _ := w.exchangeFor("", "", "", false); len(x.Cards) != 0 {
		t.Errorf("a card its subject holds is in the console too: %v", x.Cards)
	}
	if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if sum, err := c.nodes.Get(view); err != nil || sum.Lifetime != "standing" {
		t.Fatalf("the view's node: %+v %v", sum, err)
	}
	if x, _ := c.lastShown(); x.Node != view || x.Talk {
		t.Errorf("the view does not open beside: %+v", x)
	}
}

// The incident: a view proposed at the home, its subject never opened.
// Its card waits in the console, wherever it talks, until it is decided,
// and the long answer about it is no new node.
func TestHomeCardShowsInTheConsole(t *testing.T) {
	c, answer := netCore(t, "Tu peux garder un œil sur mes mails ?", "")
	if _, err := c.proposeView(proposeViewIn{Title: "Mes mails", Body: "Les mails.\n"}, nil); err != nil {
		t.Fatal(err)
	}
	answer(longAnswer)
	m := lastBrainMessage(t, c)
	if m.Filed != "" || m.Node != "" {
		t.Errorf("the answer that proposes the view was filed: %+v", m)
	}
	ref := proposalRef(cardViewKind, 1)
	x := cardOfRef(t, c, ref)
	if x.Seq != m.Seq || len(c.cardNodes(ref)) != 0 {
		t.Fatalf("card %+v, nodes %v", x, c.cardNodes(ref))
	}
	other, err := c.nodes.Create(nodestore.New{Title: "Garage", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	w := &web{c: c}
	for _, node := range []string{"", other.Node.Meta.ID} {
		if ex, _ := w.exchangeFor(node, "", "", false); !slices.Equal(ex.Cards, []string{ref}) {
			t.Errorf("exchange of %q: cards %v", node, ex.Cards)
		}
	}
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if ex, _ := w.exchangeFor("", "", "", false); len(ex.Cards) != 0 {
		t.Errorf("a decided card still waits: %v", ex.Cards)
	}
}

// Asked on the side, a node opens in the background: nothing is shown,
// the exchange stays where the user talks.
func TestAsideNodeStaysInTheBackground(t *testing.T) {
	c, answer := netCore(t, "Lance une recherche à part sur les devis", "")
	asked := c.turn.answer
	if _, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Devis", Aside: true}); err != nil {
		t.Fatal(err)
	}
	answer("Je lance la recherche.")
	if m := messageAt(t, c, asked); m.Node != "" {
		t.Errorf("the request moved: %+v", m)
	}
	if m := lastBrainMessage(t, c); m.Node != "" {
		t.Errorf("the answer moved: %+v", m)
	}
	if x, ok := c.lastShown(); ok {
		t.Errorf("shown: %+v", x)
	}
}

// Opened from a node, a node shows beside it: the console stays.
func TestNodeOpenFromANodeShowsBeside(t *testing.T) {
	c, _, _ := brainReports(t)
	openGraph(t, c)
	d, err := c.nodes.Create(nodestore.New{Title: "Garage", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	seq, _ := c.store.addUserMessage("Compare les devis", nil, id)
	c.turn.start(int(seq), int(seq), 0)
	c.turn.answersIn(id)
	out, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Devis", Parent: id})
	if err != nil {
		t.Fatal(err)
	}
	if x, _ := c.lastShown(); x.Node != out["id"] || x.Talk {
		t.Errorf("shown: %+v", x)
	}
	if m := messageAt(t, c, int(seq)); m.Node != id || c.turn.talkNode() != id {
		t.Errorf("the exchange moved: %+v", m)
	}
}

// A name that points somewhere else than itself is none of the code's (a
// stale row): it is dropped when the store opens, and never makes what a
// turn or a card is about.
func TestStaleNameNeverAnAbout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{}, store)
	openGraph(t, c)
	store.db.Exec(`INSERT INTO names (name, target, seen) VALUES ('topic:mail', 'old:16', 1)`)
	if got := c.named("garde un œil sur mes mails"); len(got) != 0 {
		t.Errorf("named %v", got)
	}
	if got := c.validAbout("old:16"); got != "" {
		t.Errorf("valid about %q", got)
	}
	if got := c.validAbout(nodeRef("nonode1234")); got != "" {
		t.Errorf("a node there is not: %q", got)
	}
	if got := c.validAbout("topic:mail"); got != "topic:mail" {
		t.Errorf("a key: %q", got)
	}
	store.db.Close()
	store, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	var n int
	store.db.QueryRow(`SELECT COUNT(*) FROM names WHERE name != target`).Scan(&n)
	if n != 0 {
		t.Errorf("%d stale names kept", n)
	}
}

// A wake after the user's turn, or a request said on a channel, never
// moves the home's exchange nor takes the console.
func TestHomeMovesOnlyTheUsersWorkspaceTurn(t *testing.T) {
	c, _ := netCore(t, "Compare les devis du garage", "")
	asked := c.turn.answer
	c.turn.goOn(0, 0, 0, nil) // a due intention wakes it (brain_wake.go)
	c.turn.answersIn("")
	if _, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Devis"}); err != nil {
		t.Fatal(err)
	}
	if m := messageAt(t, c, asked); m.Node != "" {
		t.Errorf("a wake moved the request: %+v", m)
	}
	if x, ok := c.lastShown(); ok {
		t.Errorf("a wake showed %+v", x)
	}

	seq, _ := c.store.addNodeMessage(roleUser, "Garde un œil sur mes mails", "telegram", "", false)
	c.turn.start(int(seq), int(seq), 0)
	c.turn.answersIn("")
	out, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Mails"})
	if err != nil {
		t.Fatal(err)
	}
	if m := messageAt(t, c, int(seq)); m.Node != "" || c.turn.talkNode() != "" {
		t.Errorf("a channel's request moved: %+v", m)
	}
	if x, _ := c.lastShown(); x.Node != out["id"] || x.Talk {
		t.Errorf("shown %+v", x)
	}
}

// From the home, showing the subject's node takes the exchange there too.
func TestHomeShowTakesTheExchange(t *testing.T) {
	c, _ := netCore(t, "Où en est le garage ?", "")
	asked := c.turn.answer
	d, err := c.nodes.Create(nodestore.New{Title: "Garage", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	if _, err := c.brainNodeShow(nodeShowIn{ID: id}); err != nil {
		t.Fatal(err)
	}
	if m := messageAt(t, c, asked); m.Node != id {
		t.Errorf("the request stayed at the home: %+v", m)
	}
	if x, _ := c.lastShown(); x.Node != id || !x.Talk {
		t.Errorf("shown %+v", x)
	}
}
