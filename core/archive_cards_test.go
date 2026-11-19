package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Archiving a node is setting its subject aside: the cards that wait on
// it and on the nodes under it are withdrawn with it (withdrawCards), and
// nothing else's.

// viewState is a view proposal's state.
func viewState(t *testing.T, c *core, id int64) string {
	t.Helper()
	var state string
	if err := c.store.db.QueryRow(`SELECT state FROM view_proposals WHERE id = ?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestArchiveWithdrawsItsCards(t *testing.T) {
	c, answer := netCore(t, "Tu peux garder un œil sur mes mails ?", "")
	node := func(title, parent string) string {
		t.Helper()
		r, err := c.nodes.Create(nodestore.New{Title: title, Parent: parent, Author: byBrain})
		if err != nil {
			t.Fatal(err)
		}
		return r.Node.Meta.ID
	}
	subject := node("Garder un œil sur mes mails", "")
	child := node("Les mails de la banque", subject)
	other := node("Garage", "")
	view := func(title string) string {
		t.Helper()
		if _, err := c.proposeView(proposeViewIn{Title: title, Body: title + ".\n"}, nil); err != nil {
			t.Fatal(err)
		}
		return proposalRef(cardViewKind, int64(len(c.proposedViews())))
	}
	mine := view("Mes mails")
	answer(longAnswer) // shown, with the brain's message
	kids := view("La banque")
	about := view("Les factures")
	beside := view("Le garage")
	shared := view("Le garage et les mails")
	home := view("La météo")
	c.cardRaisedFor(nodeRef(subject), mine)
	c.cardRaisedFor(nodeRef(child), kids) // still queued: withdrawn too
	c.store.db.Exec(`UPDATE convo_cards SET about = ? WHERE ref = ?`, nodeRef(child), about)
	c.cardRaisedFor(nodeRef(other), beside)
	c.cardRaisedFor(nodeRef(subject), shared)
	c.cardRaisedFor(nodeRef(other), shared) // a live node still holds it

	if err := c.archiveNode(context.Background(), subject, byUser); err != nil {
		t.Fatal(err)
	}
	c.syncCards() // a proposal still waiting would get a new card here
	for _, ref := range []string{mine, kids, about} {
		x := cardOfRef(t, c, ref)
		if x.State != cardGone || x.By != cardByArchive {
			t.Errorf("%s: %s by %q, want gone by the archive", ref, x.State, x.By)
		}
		_, id, _ := parseProposalRef(ref)
		if s := viewState(t, c, id); s != viewDropped {
			t.Errorf("%s: view %s, want dropped", ref, s)
		}
		if c.proposalOf(cardViewKind, id).Pending {
			t.Errorf("%s: its proposal still waits", ref)
		}
	}
	for _, ref := range []string{beside, shared, home} {
		if x := cardOfRef(t, c, ref); x.State != cardPending {
			t.Errorf("%s, not the archived nodes' alone: %s", ref, x.State)
		}
	}
	unheld := c.unheldCards()
	for _, ref := range []string{mine, kids, about} {
		if slices.Contains(unheld, ref) {
			t.Errorf("%s falls into the console: %v", ref, unheld)
		}
	}
	if !slices.Contains(unheld, home) || slices.Contains(unheld, beside) {
		t.Errorf("the console's cards: %v", unheld)
	}

	// Reopened, the subject comes back as a page: its cards stay gone.
	if err := c.reopenNode(subject, byUser); err != nil {
		t.Fatal(err)
	}
	c.syncCards()
	for _, ref := range []string{mine, kids, about} {
		if x := cardOfRef(t, c, ref); x.State != cardGone {
			t.Errorf("%s came back: %s", ref, x.State)
		}
	}
	if c.nodeCardWaits(subject) {
		t.Error("the reopened subject waits on a withdrawn card")
	}
}

// The view card's summary never says when twice: a description that
// starts by saying it stands alone.
func TestViewSummarySaysWhenOnce(t *testing.T) {
	for _, tc := range []struct{ lang, desc, want string }{
		{"fr", "À chaque passage de mails reçus, les lit et note ce qui compte.", "À chaque passage de mails reçus, les lit et note ce qui compte."},
		{"en", "À chaque passage de mails reçus, les lit.", "À chaque passage de mails reçus, les lit."},
		{"fr", "Quand une PR arrive, la résume.", "Quand une PR arrive, la résume."},
		{"en", "Each time mail comes in, reads it.", "Each time mail comes in, reads it."},
		{"fr", "Each time mail comes in, reads it.", "Each time mail comes in, reads it."},
		{"fr", "Lit les mails reçus.", "À chaque fois : Lit les mails reçus."},
		{"en", "Reads the mail received.", "Each time: Reads the mail received."},
	} {
		v := viewProposal{Title: "Mes mails", manifest: &skillManifest{Description: tc.desc}}
		got := viewSummary(v, tc.lang)
		if len(got) != 2 || got[1] != tc.want {
			t.Errorf("%s %q: %q", tc.lang, tc.desc, got)
		}
		if strings.Count(strings.ToLower(got[len(got)-1]), "chaque") > 1 {
			t.Errorf("%s: says when twice: %q", tc.lang, got)
		}
	}
}
