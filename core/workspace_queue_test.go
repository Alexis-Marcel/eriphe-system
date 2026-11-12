package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func surfaceByID(t *testing.T, c *core, id string) (surface, bool) {
	t.Helper()
	s, err := c.surface(id)
	if err == errNoSurface {
		return s, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}

// The queue's items are surfaces written by code, with no code per kind: a
// card is a Proposal bound to its proposal, anything else an Alert with
// what Eriphe would say; they follow the queue, and nothing is written
// again when nothing changed.
func TestQueueSurfaces(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	x := cardOf(t, c, cardRule, r.ID)
	c.attend(attentionItem{Ref: "alert:7", Weight: 5, Why: "La sauvegarde a échoué", Say: "La sauvegarde de cette nuit a échoué.", Source: "skill:veille", tainted: true})
	c.attend(attentionItem{Ref: "mail:INBOX/9", Weight: 2, Why: "Une réponse de example.org"})
	c.syncQueueSurfaces()

	card, ok := surfaceByID(t, c, queueSurfaceID(cardItemRef(x.ID)))
	if !ok || card.Author != surfaceAuthorCore || card.Weight != 60 || card.Title != tr("fr", "card.kind."+cardRule) ||
		!strings.Contains(string(card.Components), `"component":"Proposal"`) || !strings.Contains(string(card.Components), `"proposalId":"`+x.Ref+`"`) {
		t.Fatalf("card surface %+v %s", card, card.Components)
	}
	alert, ok := surfaceByID(t, c, "q-alert-7")
	if !ok || alert.Weight != 100 || alert.Title != "Urgent" || alert.Why != "La sauvegarde a échoué" || alert.Tainted == "" ||
		!strings.Contains(string(alert.Components), `"text":"La sauvegarde de cette nuit a échoué."`) ||
		!strings.Contains(string(alert.Components), `"level":"error"`) {
		t.Fatalf("alert surface %+v %s", alert, alert.Components)
	}
	// A card whose proposal the Proposal cannot show is an Alert.
	c.store.db.Exec(`INSERT INTO attention (ref, weight, why, source, time) VALUES ('card:999999', 4, 'Une présence à suivre', 'brain', ?)`, time.Now().Unix())
	if in, ok := c.queueSurface(attentionItem{Ref: "card:999999", Weight: 4, Why: "Une présence à suivre"}, "fr"); !ok ||
		!strings.Contains(string(in.Components), `"component":"Alert"`) || in.Title != "Attend ta réponse" {
		t.Fatalf("a card without a proposal: %+v %s", in, in.Components)
	}
	c.attentionDone("card:999999")

	// A ref whose id a surface's may not hold gets a hashed one.
	mailID := queueSurfaceID("mail:INBOX/9")
	if !surfaceIDRe.MatchString(mailID) || !strings.HasPrefix(mailID, "q-mail-") {
		t.Fatalf("mail id %q", mailID)
	}
	mail, ok := surfaceByID(t, c, mailID)
	if !ok || mail.Title != "À voir" || !strings.Contains(string(mail.Components), `"level":"info"`) {
		t.Fatalf("mail surface %+v", mail)
	}

	// Urgent: in front at once, no model.
	if l := c.currentLayout(); l.Items[0].ID != "q-alert-7" || l.Items[0].Size != sizeXL {
		t.Fatalf("layout %s", tilesOf(l))
	}

	// Again with nothing changed: nothing written.
	c.syncQueueSurfaces()
	if again, _ := surfaceByID(t, c, "q-alert-7"); !again.Updated.Equal(alert.Updated) {
		t.Fatal("written again with nothing changed")
	}

	// In the user's language, once the workspace is opened in it.
	c.noteWorkspaceLang("en")
	c.syncQueueSurfaces()
	if s, _ := surfaceByID(t, c, mailID); s.Title != "To see" {
		t.Fatalf("english title %q", s.Title)
	}

	// The card decided, the alert done: their surfaces go.
	if err := c.decideProposal(t.Context(), cardRule, r.ID, false); err != nil {
		t.Fatal(err)
	}
	c.attentionDone("alert:7")
	c.syncQueueSurfaces()
	for _, id := range []string{card.ID, "q-alert-7"} {
		if _, ok := surfaceByID(t, c, id); ok {
			t.Errorf("%s still there", id)
		}
	}
	if _, ok := surfaceByID(t, c, mailID); !ok {
		t.Error("the mail's surface went")
	}
}

// The queue's surfaces never meet the pool's cap, and an assistant cannot
// write as code nor over code's surfaces.
func TestQueueSurfacesOutsideTheCap(t *testing.T) {
	c, _ := surfaceCore(t)
	comps := json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)
	for i := range surfacesMax {
		if _, errs, err := c.surfaceWrite("brain", surfaceIn{ID: "s-" + strconv.Itoa(i), Title: "t", Components: comps,
			Lifetime: surfaceLifetime{Kept: true}}); err != nil || len(errs) > 0 {
			t.Fatal(i, err, errs)
		}
	}
	c.attend(attentionItem{Ref: "alert:1", Weight: 5, Why: "Panne"})
	c.syncQueueSurfaces()
	if _, ok := surfaceByID(t, c, "q-alert-1"); !ok {
		t.Fatal("a full pool refused the queue's surface")
	}
	if _, _, err := c.surfaceWrite(surfaceAuthorCore, surfaceIn{ID: "fake", Title: "t", Components: comps}); err == nil {
		t.Fatal("an assistant wrote as code")
	}
	if _, _, err := c.surfaceWrite("brain", surfaceIn{ID: "q-alert-1", Title: "t", Components: comps}); err == nil {
		t.Fatal("the brain wrote over code's surface")
	}
	// Kept while its item is open, whatever the clock: code removes it.
	s, _ := c.surface("q-alert-1")
	if c.surfaceEnded(s, time.Now().Add(48*time.Hour)) {
		t.Fatal("the lifetime ended a queue surface")
	}
}
