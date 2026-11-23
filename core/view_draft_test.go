package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A view proposed is its own node at once: a draft linked to the subject
// it came from, showing the page, its card waiting on it (the halo of
// what waits), never on the subject. Proposed again with its id, it is the
// same node and the same card, saying the new proposal. Yes makes it the
// standing view and the subject done; no archives the draft.

// daySubject opens « Suivre mes journées » as the brain does first, and
// proposes the day's view from it.
func daySubject(t *testing.T) (c *core, subject, draft, ref string) {
	t.Helper()
	c, _ = netCore(t, "Tu peux suivre et gérer mes journées ?", "")
	out, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Suivre mes journées", Body: "En cours : je prépare la page de ta journée.\n"})
	if err != nil {
		t.Fatal(err)
	}
	subject = out["id"].(string)
	res, err := c.proposeView(proposeViewIn{Title: "Ma journée", Goal: "Sa journée, tenue à jour.", Body: "Ta journée, tenue à jour.\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, _ = res["node"].(string)
	ref, _ = res["card"].(string)
	if draft == "" || draft == subject || ref == "" {
		t.Fatalf("propose_view: %v", res)
	}
	return c, subject, draft, ref
}

func TestViewDraftOpensBesideItsSubject(t *testing.T) {
	c, subject, draft, ref := daySubject(t)
	sum, err := c.nodes.Get(draft)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Title != "Ma journée" || sum.Lifetime != nodefile.Work || !slices.Equal(sum.Links, []string{subject}) || sum.Parent != "" {
		t.Errorf("the draft: %+v", sum)
	}
	if d, _ := c.nodes.Read(draft, 0); !strings.Contains(d.Node.Text(), "Ta journée, tenue à jour.") {
		t.Errorf("the draft does not show the page:\n%s", d.Raw)
	}
	if !slices.Equal(c.cardNodes(ref), []string{draft}) {
		t.Errorf("the card is held by %v, not the draft alone", c.cardNodes(ref))
	}
	if !cardWaitsOn(c, draft) || cardWaitsOn(c, subject) {
		t.Errorf("waits: draft %v, subject %v", cardWaitsOn(c, draft), cardWaitsOn(c, subject))
	}
	if nodeStatusNow(t, c, draft) != "waits" {
		t.Errorf("the draft's status: %s", nodeStatusNow(t, c, draft))
	}
	if x, _ := c.lastShown(); x.Node != draft || x.Talk {
		t.Errorf("the draft does not open beside the subject: %+v", x)
	}
}

func TestViewDraftProposedAgain(t *testing.T) {
	c, _, draft, ref := daySubject(t)
	res, err := c.proposeView(proposeViewIn{Node: draft, Title: "Ma journée", Body: "Ta journée et tes mails à traiter.\n",
		Skill: &writeSkillIn{Name: "bilan-jour", Description: "Le bilan du jour.", Instructions: "Fais le bilan.", When: "time daily 07:30"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res["node"] != draft || res["card"] != ref {
		t.Errorf("proposed again: %v, want node %s card %s", res, draft, ref)
	}
	if d, _ := c.nodes.Read(draft, 0); !strings.Contains(d.Node.Text(), "tes mails à traiter") {
		t.Errorf("the draft was not rewritten:\n%s", d.Raw)
	}
	if views := cardsOf(c, cardViewKind); len(views) != 1 || !slices.Equal(views[0].SummaryIn("fr"), []string{"Chaque jour à 7 h 30"}) {
		t.Errorf("the card is not replaced: %+v", views)
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM view_proposals`).Scan(&n)
	if n != 1 {
		t.Errorf("%d proposals, want the same one", n)
	}
	if _, err := c.proposeView(proposeViewIn{Node: "zzzz9999", Title: "Autre", Body: "x\n"}, nil); err == nil || !strings.Contains(err.Error(), "not a view's draft") {
		t.Errorf("a node that is no draft: %v", err)
	}
}

func TestViewDraftYesStandsAndEndsItsSubject(t *testing.T) {
	c, subject, draft, ref := daySubject(t)
	if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	sum, _ := c.nodes.Get(draft)
	if sum.Lifetime != nodefile.Standing || !sum.Archived.IsZero() || !slices.Equal(sum.Links, []string{subject}) {
		t.Errorf("the view: %+v", sum)
	}
	if sum.OpenedBy != nodefile.OpenedUser {
		t.Errorf("the view is not the user's: %+v", sum.OpenedBy)
	}
	if s, _ := c.nodes.Get(subject); s.Done.IsZero() || !s.Archived.IsZero() {
		t.Errorf("the subject: done %v, archived %v", s.Done, s.Archived)
	}
	if cardWaitsOn(c, draft) {
		t.Error("the view still waits")
	}
}

func TestViewDraftNoArchivesIt(t *testing.T) {
	c, subject, draft, ref := daySubject(t)
	if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if sum, _ := c.nodes.Get(draft); sum.Archived.IsZero() {
		t.Error("the draft stayed")
	}
	if s, _ := c.nodes.Get(subject); !s.Done.IsZero() || !s.Archived.IsZero() {
		t.Errorf("the subject changed: %+v", s)
	}
	if p := c.proposalOf(cardViewKind, 1); p.Pending || p.Outcome != cardRejected {
		t.Errorf("the card: %+v", p)
	}
}

// Asked at the home, a subject found (node_find), not opened: proposing
// the view moves the exchange into the subject and opens the draft beside.
func TestViewDraftFromAFoundSubject(t *testing.T) {
	c, _ := netCore(t, "Tu peux garder un œil sur mes mails ?", "")
	asked := c.turn.answer
	subject := mustNode(t, c, nodestore.New{Title: "Le courrier", Author: byBrain})
	c.turnNode(subject)
	res, err := c.proposeView(proposeViewIn{Title: "Mes mails", Body: "Les mails.\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft := res["node"].(string)
	if m := messageAt(t, c, asked); m.Node != subject {
		t.Errorf("the request stayed at the home: %+v", m)
	}
	if x, _ := c.lastShown(); x.Node != draft || x.Talk {
		t.Errorf("the draft is not beside the subject: %+v", x)
	}
	if sum, _ := c.nodes.Get(draft); !slices.Equal(sum.Links, []string{subject}) {
		t.Errorf("the draft's links: %v", sum.Links)
	}
}

// No archives the draft with what else waits on it.
func TestViewDraftNoWithdrawsItsOtherCards(t *testing.T) {
	c, _ := netCore(t, "Tu peux garder un œil sur mes mails ?", "")
	res, err := c.proposeView(proposeViewIn{Title: "Mes mails", Body: "Les mails.\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, ref := res["node"].(string), res["card"].(string)
	other := proposalRef(cardRule, proposedRule(t, c, "factures", "").ID) // raised in the draft, where the user talks
	if !slices.Contains(c.cardNodes(other), draft) {
		t.Fatalf("the rule's card is not the draft's: %v", c.cardNodes(other))
	}
	if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if x := cardOfRef(t, c, other); x.State != cardGone {
		t.Errorf("the draft's other card: %s", x.State)
	}
}
