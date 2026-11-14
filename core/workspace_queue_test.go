package main

import (
	"encoding/json"
	"net/http"
	"slices"
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

// groupRefs are the refs a group of the queue lists, in order.
func groupRefs(t *testing.T, s surface) []string {
	t.Helper()
	var d struct {
		Items []struct {
			Ref string `json:"ref"`
		} `json:"items"`
	}
	if err := json.Unmarshal(s.Data, &d); err != nil {
		t.Fatalf("%s data %s: %v", s.ID, s.Data, err)
	}
	var out []string
	for _, it := range d.Items {
		out = append(out, it.Ref)
	}
	return out
}

// queueCards makes the cards of the tests: a rule (a real proposal), three
// memories, a reminder with no date and one due today, all queued.
type queueCards struct {
	rule, today convoCard
	mems        []convoCard
	reminder    convoCard
}

func makeQueueCards(t *testing.T, c *core) queueCards {
	t.Helper()
	var q queueCards
	r := proposedRule(t, c, "facture", "")
	var mems []int64
	for _, text := range []string{"Sam préfère le train", "Sam a un vélo", "Sam lit le soir"} {
		m := mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: text, Status: memProposed, SourceKind: srcBrain})
		c.cardProposed(cardMemory, m.ID)
		mems = append(mems, m.ID)
	}
	in := mustIntend(t, c.mem, &Intention{Text: "appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	c.cardProposed(cardIntention, in.ID)
	y, m, d := time.Now().In(c.userZone()).Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, c.userZone())
	due := mustIntend(t, c.mem, &Intention{Text: "ajouter l'événement à l'agenda", DueAt: noon, State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	c.cardProposed(cardIntention, due.ID)
	brainSays(c, "Voilà ce que je te propose.")
	q.rule, q.reminder, q.today = cardOf(t, c, cardRule, r.ID), cardOf(t, c, cardIntention, in.ID), cardOf(t, c, cardIntention, due.ID)
	for i, id := range mems {
		x := cardOf(t, c, cardMemory, id)
		// The first memory is the oldest: the group lists it last.
		c.store.db.Exec(`UPDATE attention SET time = ? WHERE ref = ?`, time.Now().Add(time.Duration(i-10)*time.Minute).Unix(), cardItemRef(x.ID))
		q.mems = append(q.mems, x)
	}
	return q
}

// The queue's items are surfaces written by code, grouped by what they
// ask, with no code per kind: the cards that keep a memory, those that
// keep a reminder, those that do something, each group a List of
// Proposals bound to its items, its title counting them; an urgent item
// alone; anything else an Alert in « À voir ». They follow the queue, and
// nothing is written again when nothing changed.
func TestQueueSurfaces(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	q := makeQueueCards(t, c)
	c.attend(attentionItem{Ref: "alert:7", Weight: 5, Why: "La sauvegarde a échoué", Say: "La sauvegarde de cette nuit a échoué.", Source: "skill:veille", tainted: true})
	c.attend(attentionItem{Ref: "mail:INBOX/9", Weight: 2, Why: "Une réponse de example.org"})
	c.attend(attentionItem{Ref: "note:3", Weight: 1, Why: "Un colis est arrivé"})
	c.syncQueueSurfaces()

	// The memories: one surface, its title counting them, newest first.
	mems, ok := surfaceByID(t, c, queueMemories)
	if !ok || mems.Author != surfaceAuthorCore || mems.Weight != 60 || mems.Title != "Souvenirs à garder · 3" ||
		!strings.Contains(string(mems.Components), `"children":{"componentId":"item","path":"/items"}`) ||
		!strings.Contains(string(mems.Components), `"proposalId":{"@path":"ref"}`) {
		t.Fatalf("memories %+v %s", mems, mems.Components)
	}
	if got, want := groupRefs(t, mems), []string{q.mems[2].Ref, q.mems[1].Ref, q.mems[0].Ref}; !slices.Equal(got, want) {
		t.Fatalf("memories %v, want %v", got, want)
	}
	// One item: titled by its subject, not its kind.
	if s, ok := surfaceByID(t, c, queueReminders); !ok || s.Title != "Appeler le garage" || !slices.Equal(groupRefs(t, s), []string{q.reminder.Ref}) {
		t.Fatalf("reminders %+v", s)
	}
	if s, ok := surfaceByID(t, c, queueDecide); !ok || s.Title != "Facture → au brief" || !slices.Equal(groupRefs(t, s), []string{q.rule.Ref}) {
		t.Fatalf("decide %+v", s)
	}
	// What takes place today is urgent enough to stand alone, a Proposal
	// bound to its proposal.
	today, ok := surfaceByID(t, c, queueSurfaceID(cardItemRef(q.today.ID)))
	if !ok || today.Title != "Ajouter l'événement à l'agenda" ||
		!strings.Contains(string(today.Components), `"proposalId":"`+q.today.Ref+`"`) {
		t.Fatalf("today %+v %s", today, today.Components)
	}
	// No card has a surface of its own but the urgent one.
	all, _ := c.surfaces()
	var ids []string
	for _, s := range all {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	want := []string{queueDecide, queueMemories, queueNotes, queueReminders, "q-alert-7", today.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("surfaces %v, want %v", ids, want)
	}

	alert, ok := surfaceByID(t, c, "q-alert-7")
	if !ok || alert.Weight != 100 || alert.Title != "Urgent" || alert.Why != "La sauvegarde a échoué" || alert.Tainted == "" ||
		!strings.Contains(string(alert.Components), `"text":"La sauvegarde de cette nuit a échoué."`) ||
		!strings.Contains(string(alert.Components), `"level":"error"`) {
		t.Fatalf("alert surface %+v %s", alert, alert.Components)
	}
	notes, ok := surfaceByID(t, c, queueNotes)
	if !ok || notes.Title != "À voir · 2" || notes.Weight != 40 || strings.Count(string(notes.Components), `"component":"Alert"`) != 2 ||
		!strings.Contains(string(notes.Components), `"text":"Une réponse de example.org"`) {
		t.Fatalf("notes %+v %s", notes, notes.Components)
	}
	// A card whose proposal the Proposal cannot show is an Alert.
	c.store.db.Exec(`INSERT INTO attention (ref, weight, why, source, time) VALUES ('card:999999', 4, 'Une présence à suivre', 'brain', ?)`, time.Now().Unix())
	e := c.queuePlace(attentionItem{Ref: "card:999999", Weight: 4, Why: "Une présence à suivre"}, time.Now(), nil)
	if in, ok := c.queueSurface(e.surface, []queueEntry{e}, "fr"); !ok ||
		!strings.Contains(string(in.Components), `"component":"Alert"`) || in.Title != "Attend ta réponse" {
		t.Fatalf("a card without a proposal: %+v %s", in, in.Components)
	}
	c.attentionDone("card:999999")

	// A ref whose id a surface's may not hold gets a hashed one.
	if id := queueSurfaceID("mail:INBOX/9"); !surfaceIDRe.MatchString(id) || !strings.HasPrefix(id, "q-mail-") {
		t.Fatalf("mail id %q", id)
	}

	// Urgent: in front at once, no model.
	if l := c.currentLayout(); l.Items[0].ID != "q-alert-7" || l.Items[0].Size != sizeXL {
		t.Fatalf("layout %s", tilesOf(l))
	}

	// Again with nothing changed: nothing written.
	c.syncQueueSurfaces()
	if again, _ := surfaceByID(t, c, queueMemories); !again.Updated.Equal(mems.Updated) {
		t.Fatal("written again with nothing changed")
	}

	// A memory decided elsewhere: the group, same id, lists the others.
	if _, err := c.decideCard(t.Context(), q.mems[1].ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	c.syncQueueSurfaces()
	if s, _ := surfaceByID(t, c, queueMemories); s.Title != "Souvenirs à garder · 2" || !slices.Equal(groupRefs(t, s), []string{q.mems[2].Ref, q.mems[0].Ref}) {
		t.Fatalf("memories once one is kept %+v %s", s, s.Data)
	}

	// In the user's language, once the workspace is opened in it.
	c.noteUserLang("en")
	c.syncQueueSurfaces()
	for id, title := range map[string]string{queueMemories: "Memories to keep · 2", queueNotes: "To see · 2"} {
		if s, _ := surfaceByID(t, c, id); s.Title != title {
			t.Errorf("english title of %s %q, want %q", id, s.Title, title)
		}
	}

	// The cards decided, the alert done: their surfaces go; a group goes
	// with its last item.
	for _, x := range []convoCard{q.mems[0], q.mems[2], q.rule} {
		if _, err := c.decideCard(t.Context(), x.ID, false, "web"); err != nil {
			t.Fatal(err)
		}
	}
	c.attentionDone("alert:7")
	c.syncQueueSurfaces()
	for _, id := range []string{queueMemories, queueDecide, "q-alert-7"} {
		if _, ok := surfaceByID(t, c, id); ok {
			t.Errorf("%s still there", id)
		}
	}
	for _, id := range []string{queueNotes, queueReminders, today.ID} {
		if _, ok := surfaceByID(t, c, id); !ok {
			t.Errorf("%s went", id)
		}
	}
}

// The flood seen on a real workspace: twelve extracted memories and six
// reminders waiting to be offered, some dated but not today. Two surfaces
// show them, each counting its items, and the layout names none by an id.
func TestQueueFloodGrouped(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	for i := range 12 {
		mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Sam aime le thé " + strconv.Itoa(i), Status: memProposed, SourceKind: srcTask})
	}
	later := time.Now().AddDate(0, 0, 3)
	for i := range 6 {
		x := &Intention{Text: "Rappel " + strconv.Itoa(i), State: intentProposed, SourceKind: srcExtraction, SourceRef: "msg:1", Trust: trustFirstParty}
		if i%2 == 0 {
			x.DueAt = later
		}
		mustIntend(t, c.mem, x)
	}
	c.attendOffers()
	if n := len(c.attentionOpen()); n != 18 {
		t.Fatalf("%d items", n)
	}
	c.syncQueueSurfaces()
	all, _ := c.surfaces()
	var ids []string
	for _, s := range all {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{queueMemories, queueReminders}) {
		t.Fatalf("surfaces %v", ids)
	}
	l := c.relayout()
	titles := map[string]string{}
	for _, it := range l.Items {
		titles[it.ID] = it.Title
	}
	for _, ch := range l.Chips {
		titles[ch.ID] = ch.Title
	}
	if len(titles) != 2 || titles[queueMemories] != "Souvenirs à garder · 12" || titles[queueReminders] != "Rappels à garder · 6" {
		t.Fatalf("tiles and chips %v", titles)
	}
}

// A single card's title is its subject, short, from what it proposes; its
// kind when the card may carry external content.
func TestQueueSubject(t *testing.T) {
	long := strings.Repeat("relire le devis du plombier ", 5)
	e := queueEntry{p: proposal{Kind: cardIntention, text: func(string) string { return long }}}
	got := queueSubject(e, "fr")
	if !strings.HasPrefix(got, "Relire le devis") || !strings.HasSuffix(got, "…") || len([]rune(got)) > queueSubjectMax+1 {
		t.Fatalf("subject %q", got)
	}
	e.p.Tainted = "mail"
	if got := queueSubject(e, "en"); got != "Reminder" {
		t.Fatalf("tainted subject %q", got)
	}
	e.p.Tainted, e.p.text = "", func(string) string { return "" }
	if got := queueSubject(e, "fr"); got != "Rappel" {
		t.Fatalf("empty subject %q", got)
	}
}

// The groups' ids never meet an item's, and a group is answered by the
// Proposal of its List: the item's own ref, never another's.
func TestQueueGroupAnswer(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	q := makeQueueCards(t, c)
	c.syncQueueSurfaces()
	for _, id := range []string{queueDecide, queueReminders, queueMemories, queueNotes} {
		if strings.Count(id, "-") != 1 || !surfaceIDRe.MatchString(id) {
			t.Errorf("group id %q", id)
		}
	}
	answer := func(comp, ref, yes string) string {
		return actionOn("answer", queueMemories, comp, `{"proposal":"`+ref+`","answer":"`+yes+`"}`)
	}
	for _, bad := range []string{answer("item", q.reminder.Ref, "yes"), answer("root", q.mems[0].Ref, "yes")} {
		if code, body := postJSON(tw, "/api/espace/action", bad, csrf); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, code, body)
		}
	}
	if code, body := postJSON(tw, "/api/espace/action", answer("item", q.mems[0].Ref, "no"), csrf); code != http.StatusNoContent {
		t.Fatalf("no: %d %s", code, body)
	}
	if x := cardOf(t, c, cardMemory, mustProposalID(t, q.mems[0].Ref)); x.State != cardRejected || x.By != "workspace" {
		t.Fatalf("card %+v", x)
	}
	c.syncQueueSurfaces()
	if s, _ := surfaceByID(t, c, queueMemories); len(groupRefs(t, s)) != 2 {
		t.Fatalf("memories %s", s.Data)
	}
}

func mustProposalID(t *testing.T, ref string) int64 {
	t.Helper()
	_, id, ok := parseProposalRef(ref)
	if !ok {
		t.Fatalf("ref %q", ref)
	}
	return id
}

// Closing a group hides it for the session, its cards still waiting and
// its other items ending; opening one reads only what it showed then;
// removing it takes its items out of the queue. The composer sees each
// item with the group that shows it.
func TestQueueGroupGestures(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	q := makeQueueCards(t, c)
	c.attend(attentionItem{Ref: "note:1", Weight: 2, Why: "Un colis est arrivé"})
	c.attend(attentionItem{Ref: "note:2", Weight: 2, Why: "Une facture est payée"})
	c.syncQueueSurfaces()
	now := time.Now()

	snap := c.poolSnapshot(momentChange, "")
	shownBy := map[string]string{}
	for _, it := range snap.Queue {
		shownBy[it.Ref] = it.Surface
	}
	if shownBy[cardItemRef(q.mems[0].ID)] != queueMemories || shownBy["note:1"] != queueNotes ||
		shownBy[cardItemRef(q.today.ID)] != queueSurfaceID(cardItemRef(q.today.ID)) {
		t.Fatalf("shown by %v", shownBy)
	}

	// Closed: hidden for the session, the memories still wait.
	if err := c.closeSurface(queueMemories, now); err != nil {
		t.Fatal(err)
	}
	if c.placementNow(now).Closed[queueMemories] == 0 {
		t.Fatal("not closed")
	}
	if got := len(c.queueItemsOf(queueMemories)); got != 3 {
		t.Fatalf("memories in the queue: %d", got)
	}

	// Opened then left: what it showed ends, a note come since stays.
	c.focusSurface(queueNotes, now)
	c.attend(attentionItem{Ref: "note:3", Weight: 2, Why: "Un rendez-vous a bougé"})
	c.focusSurface("", now)
	c.queueSweep(now.Add(time.Second))
	var left []string
	for _, it := range c.queueItemsOf(queueNotes) {
		left = append(left, it.Ref)
	}
	if !slices.Equal(left, []string{"note:3"}) {
		t.Fatalf("notes left %v", left)
	}

	// Closed, the notes end.
	if err := c.closeSurface(queueNotes, now); err != nil {
		t.Fatal(err)
	}
	if got := c.queueItemsOf(queueNotes); len(got) != 0 {
		t.Fatalf("notes %v", got)
	}

	// Removed: its items leave the queue, the cards wait in the conversation.
	if err := c.surfaceRemove(queueMemories, surfaceByUser); err != nil {
		t.Fatal(err)
	}
	if got := c.queueItemsOf(queueMemories); len(got) != 0 {
		t.Fatalf("memories %v", got)
	}
	if x := cardOf(t, c, cardMemory, mustProposalID(t, q.mems[0].Ref)); x.State != cardPending {
		t.Fatalf("card %+v", x)
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
