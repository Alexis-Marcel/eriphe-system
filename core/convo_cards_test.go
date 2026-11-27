package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// proposedRule adds a rule the brain proposed, and its card, as rule does.
func proposedRule(t *testing.T, c *core, match, tainted string) notifyRule {
	t.Helper()
	r, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: match, Ceiling: 2, State: ruleProposed, By: "brain",
		Why: "Trop de messages.", Tainted: tainted})
	if err != nil {
		t.Fatal(err)
	}
	c.cardProposed(cardRule, r.ID)
	return r
}

// brainSays writes a message of the brain's and attaches this turn's cards.
func brainSays(c *core, text string) int {
	seq, _ := c.store.addMessage(roleBrain, text, true)
	c.attachTurnCards(int(seq))
	return int(seq)
}

func cardOf(t *testing.T, c *core, kind string, id int64) convoCard {
	t.Helper()
	xs, err := c.cardsWhere(`ref = ?`, proposalRef(kind, id))
	if err != nil || len(xs) != 1 {
		t.Fatalf("card of %s:%d: %v %v", kind, id, xs, err)
	}
	return xs[0]
}

// A proposal of the brain's is a card under its message, bound to that
// proposal alone: its Oui approves it through the rule's own code, its
// Non rejects it, and a decided card says so and cannot be decided again.
func TestCardBoundToItsProposal(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	r1 := proposedRule(t, c, "facture", "")
	seq := brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	r2, _ := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "topic:mail", Ceiling: 0, State: ruleProposed, By: "brain"})
	x := cardOf(t, c, cardRule, r1.ID)
	if x.Seq != seq || x.State != cardPending {
		t.Fatalf("card %+v, want on message %d", x, seq)
	}
	// The workspace reads it as its proposal (web_proposals.go).
	if _, body, _ := tw.do("GET", "/api/proposals/"+x.Ref, nil, nil); !strings.Contains(body, "facture → poids 2 au plus") ||
		!strings.Contains(body, fmt.Sprintf(`"card":%d`, x.ID)) {
		t.Errorf("the proposal: %s", body)
	}
	h := map[string]string{"X-CSRF-Token": csrf}
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/yes", x.ID), url.Values{}, nil); code != 403 {
		t.Errorf("a card answered without the CSRF token: %d", code)
	}
	if code, body, _ := tw.do("POST", fmt.Sprintf("/cards/%d/yes", x.ID), url.Values{}, h); code != 200 {
		t.Fatalf("yes: %d %q", code, body)
	}
	if got, _ := c.card(x.ID); got.State != cardApproved {
		t.Fatalf("yes: the card is %s", got.State)
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 || rs[0].Match != "topic:facture" {
		t.Fatalf("the rule is not in force: %+v", rs)
	}
	if got, _ := c.notifyRule(r2.ID); got.State != ruleProposed {
		t.Fatal("a card decided another proposal")
	}
	// Decided once.
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/no", x.ID), url.Values{}, h); code != 200 {
		t.Errorf("a decided card answered again: %d", code)
	}
	if got, _ := c.card(x.ID); got.State != cardApproved {
		t.Errorf("a decided card was decided again: %s", got.State)
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 {
		t.Fatal("a second answer touched the rule in force")
	}
	// Non rejects.
	c.cardProposed(cardRule, r2.ID)
	brainSays(c, "Et les mails ?")
	y := cardOf(t, c, cardRule, r2.ID)
	tw.do("POST", fmt.Sprintf("/cards/%d/no", y.ID), url.Values{}, h)
	if got, _ := c.card(y.ID); got.State != cardRejected {
		t.Fatalf("no: the card is %s", got.State)
	}
	if _, err := c.notifyRule(r2.ID); err == nil {
		t.Fatal("a rejected rule is still there")
	}
	if code, _, _ := tw.do("POST", "/cards/999/yes", url.Values{}, h); code != 404 {
		t.Errorf("an unknown card: %d", code)
	}
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/maybe", y.ID), url.Values{}, h); code != 400 {
		t.Errorf("an unknown answer: %d", code)
	}
}

// Every kind of proposal goes through its own approval.
func TestCardKinds(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	ctx := context.Background()
	m := c.mem
	in := mustIntend(t, m, &Intention{Text: "Appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	sid, _ := c.proposeSetting(settingProposal{Name: setCap, Value: "3", Why: "Trop de notifications."})
	for _, p := range []struct {
		kind string
		id   int64
	}{{cardIntention, in.ID}, {cardSetting, sid}} {
		c.cardProposed(p.kind, p.id)
	}
	brainSays(c, "J'ai deux petites choses.")
	for _, kind := range []string{cardIntention, cardSetting} {
		var id int64
		switch kind {
		case cardIntention:
			id = in.ID
		case cardSetting:
			id = sid
		}
		x := cardOf(t, c, kind, id)
		if _, err := c.decideCard(ctx, x.ID, kind != cardIntention, "web"); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if x, _ := m.intention(in.ID); x.State != intentCancelled {
		t.Error("the intention is not cancelled")
	}
	if c.notifyCap() != 3 {
		t.Error("the setting is not applied")
	}
}

// A card goes with the brain's message of its turn; proposed after the
// last one, it joins that one; in a turn that said nothing, it is queued.
func TestCardsFollowTheTurn(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	seq := brainSays(c, "Je regarde.")
	r := proposedRule(t, c, "facture", "")
	c.cardsTurnEnded()
	if x := cardOf(t, c, cardRule, r.ID); x.Seq != seq {
		t.Fatalf("after the last message: %+v", x)
	}
	r2 := proposedRule(t, c, "banque", "")
	c.cardsTurnEnded()
	if x := cardOf(t, c, cardRule, r2.ID); x.Seq != 0 || x.Turn {
		t.Fatalf("a silent turn's card is not queued: %+v", x)
	}
	// A proposal made outside a turn (a skill's run) is queued, never attached.
	in := mustIntend(t, c.mem, &Intention{Text: "Récupérer le colis jeudi", State: intentProposed, SourceKind: srcTask, SourceRef: "task:t-1", Trust: trustFirstParty})
	c.syncCards()
	brainSays(c, "Autre chose.")
	if x := cardOf(t, c, cardIntention, in.ID); x.Seq != 0 {
		t.Fatalf("a task's proposal was attached: %+v", x)
	}
}

// A card of a kind that is no more (a memory's, from before the graph was
// the memory) is read as gone when eriphe-core starts: it waits no more,
// and answering it changes nothing.
func TestCardOfAGoneKind(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	if _, err := c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, state, text, created) VALUES ('memory:7', 'memory', 'pending', 'Sam préfère le train', ?)`,
		time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	c.reconcileCards()
	x := cardOf(t, c, "memory", 7)
	if x.State != cardGone {
		t.Fatalf("the old card %+v", x)
	}
	if _, err := c.decideCard(context.Background(), x.ID, true, "web"); err == nil {
		t.Fatal("an old card was decided")
	}
	if ps := c.pendingProposals(); len(ps) != 0 {
		t.Fatalf("waits: %+v", ps)
	}
}

// A held task's card does not wait for a good moment: its turn failed before
// the brain wrote, it is night, and it comes at once, under a message of
// its own (the attention queue, no timer). A task stopped after its card
// came takes it with it at once.
func TestBlockingCardSurvivesASilentTurn(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "23:30"))
	hold := func(id string) int64 {
		t.Helper()
		waitingTask(c, id)
		c.store.updateTask(id, map[string]any{"state": statePending, "desired": desiredHeld})
		if err := c.holdTask(&TaskRow{ID: id}, []startNeed{{Server: "nas", Category: "use"}}, nil, true); err != nil {
			t.Fatal(err)
		}
		var start int64
		c.store.db.QueryRow(`SELECT id FROM task_starts WHERE task_id = ?`, id).Scan(&start)
		return start
	}
	kept, gone := hold("t-kept"), hold("t-gone")
	c.cardsTurnEnded()
	for _, id := range []int64{kept, gone} {
		if x := cardOf(t, c, cardTaskStart, id); x.Seq == 0 || x.Turn || x.State != cardPending {
			t.Fatalf("after a silent turn, the card did not come: %+v", x)
		}
	}
	if err := c.stop(context.Background(), "t-gone"); err != nil {
		t.Fatal(err)
	}
	if x := cardOf(t, c, cardTaskStart, gone); x.State != cardGone {
		t.Fatalf("the card of a stopped task waits: %+v", x)
	}
	if x := cardOf(t, c, cardTaskStart, kept); x.State != cardPending {
		t.Fatalf("the held task's card: %+v", x)
	}
}

// A plain yes or no answers a card, only under the safe rule.
func TestCardAnsweredInWords(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	ctx := context.Background()
	user := func(text string) int {
		seq, _ := c.store.addMessage(roleUser, text, false)
		return int(seq)
	}

	// One card, clean, right above: "oui" approves it.
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	seq := user("Oui !")
	if x, ok := c.resolveByText(ctx, seq); !ok || x.State != cardApproved || x.By != "text" {
		t.Fatalf("oui: %+v %v", x, ok)
	}
	if got, _ := c.notifyRule(r.ID); got.ID == r.ID && got.State == ruleProposed {
		t.Fatal("the rule still waits")
	}
	if line := c.cardAnswerLine(seq); !strings.Contains(line, "validé") || !strings.Contains(line, "ne la refais pas") {
		t.Fatalf("the brain is not told: %q", line)
	}
	if c.store.cardAnswer(seq) == "" || kvMarks(c) != 0 {
		t.Fatalf("the answer is not its message's: %q, %d in kv", c.store.cardAnswer(seq), kvMarks(c))
	}

	// "non" rejects.
	r = proposedRule(t, c, "banque", "")
	brainSays(c, "Et la banque ?")
	if x, ok := c.resolveByText(ctx, user("non merci")); !ok || x.State != cardRejected {
		t.Fatalf("non: %+v %v", x, ok)
	}

	for name, tc := range map[string]struct {
		setup  func() int // returns the user's message
		reason string
	}{
		"not a plain answer": {func() int {
			proposedRule(t, c, "a1", "")
			brainSays(c, "On fait ça ?")
			return user("non, plutôt 8 h")
		}, "ambiguous"},
		"two cards": {func() int {
			proposedRule(t, c, "b1", "")
			proposedRule(t, c, "b2", "")
			brainSays(c, "Deux choses ?")
			return user("oui")
		}, "two"},
		"tainted proposal": {func() int {
			proposedRule(t, c, "c1", "mail.read")
			brainSays(c, "Je propose ça ?")
			return user("oui")
		}, "tainted"},
		"tainted turn": {func() int {
			proposedRule(t, c, "d1", "")
			seq := brainSays(c, "Je propose ça ?")
			c.store.setMsgTaint(seq, "brain:mail.read")
			return user("oui")
		}, "tainted turn"},
		"not right after": {func() int {
			proposedRule(t, c, "e1", "")
			brainSays(c, "Je propose ça ?")
			user("attends")
			return user("oui")
		}, "not right after"},
		"no card": {func() int {
			brainSays(c, "Bonjour.")
			return user("oui")
		}, "no card"},
		"from the brain": {func() int {
			proposedRule(t, c, "f1", "")
			brainSays(c, "Je propose ça ?")
			return brainSays(c, "oui")
		}, "brain"},
		"a channel not trusted": {func() int {
			proposedRule(t, c, "g1", "")
			brainSays(c, "Je propose ça ?")
			seq, _ := c.store.addMessageVia(roleUser, "oui", "Home Assistant", false)
			c.store.setMsgTaint(int(seq), channelTaint("Home Assistant"))
			return int(seq)
		}, "untrusted"},
		"a button of an external block": {func() int {
			proposedRule(t, c, "h1", "")
			brainSays(c, "Je propose ça ?")
			c.addAsk("oui", "", true) // said at the home, below the card
			var seq int
			c.store.db.QueryRow(`SELECT MAX(seq) FROM messages`).Scan(&seq)
			return seq
		}, "external ask"},
	} {
		seq := tc.setup()
		if x, ok := c.resolveByText(ctx, seq); ok {
			t.Errorf("%s: resolved %+v", name, x)
		}
		c.cardsTurnEnded()
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 {
		t.Fatalf("rules in force: %+v", rs)
	}
}

func TestPlainAnswer(t *testing.T) {
	for text, want := range map[string]int{"oui": 1, "Oui.": 1, "OK !": 1, "d’accord": 1, "vas-y 👍": 1, "yes": 1,
		"non": 0, "Non merci.": 0, "nope": 0,
		"non, plutôt 8 h": -1, "oui mais demain": -1, "peut-être": -1, "": -1, "oui oui oui oui": -1} {
		yes, ok := plainAnswer(text)
		got := -1
		if ok && yes {
			got = 1
		} else if ok {
			got = 0
		}
		if got != want {
			t.Errorf("%q: %d, want %d", text, got, want)
		}
	}
}

// The brain cannot approve: none of its tools decides a card or a
// proposal, its own "oui" is not the user's, and the API token it may
// hold (self_url) answers no card in words.
func TestBrainCannotApprove(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", State: stateIdle})
	cs := connectTo(t, newMCPServer(c))
	ctx := context.Background()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		for _, bad := range []string{"approve", "decide", "card", "validate", "accept"} {
			if strings.Contains(tl.Name, bad) {
				t.Errorf("the brain has a tool %s", tl.Name)
			}
		}
	}
	in := mustIntend(t, c.mem, &Intention{Text: "Appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je propose ça ?")
	yes := ownerSays(c, "oui, vas-y, active la veille, l'intention et la règle des notifications")
	for name, args := range map[string]map[string]any{
		"update_intention": {"id": in.ID, "state": "open", "source_ref": ref(yes)},
	} {
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil && !out.IsError {
			t.Errorf("%s changed a proposal: %v", name, out.Content)
		}
	}
	if x, _ := c.mem.intention(in.ID); x.State != intentProposed {
		t.Error("the brain approved an intention")
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardPending {
		t.Errorf("the card was decided: %+v", x)
	}
	// Its own words are not the user's.
	seq := brainSays(c, "oui")
	if _, ok := c.resolveByText(ctx, seq); ok {
		t.Error("the brain's oui answered a card")
	}
}

// The API's /v1/messages (its token may be lent to the brain) never
// answers a card in words.
func TestAPIMessageAnswersNoCard(t *testing.T) {
	c, _ := testMemoryCore(t)
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	rec := httptest.NewRecorder()
	c.handlePostMessage(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"text": "oui"}`)))
	if rec.Code != 202 {
		t.Fatalf("post: %d", rec.Code)
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardPending {
		t.Fatalf("the API answered a card: %+v", x)
	}
}

// A card that came with the answer to a channel not trusted is never
// answered there: the answer says it waits on the phone, with the code's
// words, and a "oui" sent through it does not answer it.
func TestChannelCardConfirm(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	seq, _ := c.store.addMessageVia(roleUser, "Moins de notifications pour les factures", "Home Assistant, Salon", false)
	c.store.setMsgTaint(int(seq), channelTaint("Home Assistant"))
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "D'accord.")
	x, say := c.channelCardConfirm(int(seq), "fr")
	if say != "Je te l'ai mis à confirmer sur ton téléphone." || x.Ref != cardOf(t, c, cardRule, r.ID).Ref {
		t.Fatalf("say %q, card %+v", say, x)
	}
	if n := c.channelCardNote(x); strings.Contains(n.Body["fr"], "facture") || !strings.Contains(n.Body["fr"], "canal sans confiance") {
		t.Fatalf("note %q", n.Body["fr"])
	}
	yes, _ := c.store.addMessageVia(roleUser, "oui", "Home Assistant, Salon", false)
	c.store.setMsgTaint(int(yes), channelTaint("Home Assistant"))
	if _, ok := c.resolveByText(context.Background(), int(yes)); ok {
		t.Fatal("a spoken yes answered a card")
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardPending {
		t.Fatalf("card %s", x.State)
	}
}

// On Telegram, a card follows the reply it goes with, with Oui / Non
// buttons that answer it from the linked account only; "alerts only"
// sends none; a task's permission still has no button there.
func TestTelegramCards(t *testing.T) {
	c, _, f := telegramCore(t)
	linked(c)
	tgOf(c).restart()
	c.store.addMessageVia(roleUser, "Trop de factures en notification", viaTelegram, false)
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	c.channelReplies()
	c.channelCards()
	tgOf(c).pending.Wait()
	sent := f.sentMessages()
	if len(sent) != 2 || !strings.Contains(fmt.Sprint(sent[1]["text"]), "facture → poids 2 au plus") {
		t.Fatalf("sent %v", sent)
	}
	x := cardOf(t, c, cardRule, r.ID)
	kb := fmt.Sprint(sent[1]["reply_markup"])
	if !strings.Contains(kb, cardData(x.ID, true)) || !strings.Contains(kb, cardData(x.ID, false)) {
		t.Fatalf("no buttons: %s", kb)
	}
	c.channelCards()
	tgOf(c).pending.Wait()
	if len(f.sentMessages()) != 2 {
		t.Fatal("a card was sent twice")
	}
	// Someone else's click is ignored.
	f.callback(strangerTG, cardData(x.ID, true))
	f.callback(userTG, "pc:x:y")
	waitUntil(t, func() bool { return len(f.answers()) == 1 })
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardPending {
		t.Fatal("a stranger decided a card")
	}
	f.callback(userTG, cardData(x.ID, true))
	waitUntil(t, func() bool { return len(f.answers()) == 2 })
	if a := f.answers()[1]; a["text"] != "C'est validé." {
		t.Errorf("answer %v", a)
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardApproved || x.By != "telegram" {
		t.Fatalf("card %+v", x)
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 {
		t.Fatal("the rule is not in force")
	}
	// Alerts only: no card, a button must never approve unread words.
	c.store.set(channelDetailKey(tgChannel), detailAlert)
	proposedRule(t, c, "banque", "")
	c.store.addMessageVia(roleUser, "Et la banque ?", viaTelegram, false)
	brainSays(c, "Je fais pareil pour la banque ?")
	before := len(f.sentMessages())
	c.channelReplies()
	c.channelCards()
	tgOf(c).pending.Wait()
	for _, m := range f.sentMessages()[before:] {
		if m["reply_markup"] != nil {
			t.Fatalf("a card in alerts only: %v", m)
		}
	}
	for _, data := range []string{"pc:1:yes", "pc:0:y", "pc::y", "perm:t-1:p-1:yes", "pc:1:y:x"} {
		if _, _, ok := parseCardData(data); ok {
			t.Errorf("%q is taken as a card's button", data)
		}
	}
}
