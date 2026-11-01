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
	r, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: match, Outcome: levelBrief, State: ruleProposed, By: "brain",
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
	r2, _ := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "source:mail", Outcome: levelNever, State: ruleProposed, By: "brain"})
	x := cardOf(t, c, cardRule, r1.ID)
	if x.Seq != seq || x.State != cardPending {
		t.Fatalf("card %+v, want on message %d", x, seq)
	}
	_, body, _ := tw.do("GET", "/", nil, nil)
	for _, want := range []string{fmt.Sprintf(`id="card-%d"`, x.ID), fmt.Sprintf(`hx-post="/cards/%d/yes"`, x.ID),
		fmt.Sprintf(`hx-post="/cards/%d/no"`, x.ID), "data-card-edit=", "facture → au brief", `href="/settings/approve"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the conversation lacks %q", want)
		}
	}
	h := map[string]string{"X-CSRF-Token": csrf}
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/yes", x.ID), url.Values{}, nil); code != 403 {
		t.Errorf("a card answered without the CSRF token: %d", code)
	}
	code, body, hd := tw.do("POST", fmt.Sprintf("/cards/%d/yes", x.ID), url.Values{}, h)
	if code != 200 || !strings.Contains(body, "is-approved") || !strings.Contains(hd.Get("HX-Trigger"), "memory-changed") {
		t.Fatalf("yes: %d %q", code, body)
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 || rs[0].Match != "facture" {
		t.Fatalf("the rule is not in force: %+v", rs)
	}
	if got, _ := c.notifyRule(r2.ID); got.State != ruleProposed {
		t.Fatal("a card decided another proposal")
	}
	// Decided once.
	_, _, hd = tw.do("POST", fmt.Sprintf("/cards/%d/no", x.ID), url.Values{}, h)
	if !strings.Contains(hd.Get("HX-Trigger"), "attend plus") {
		t.Errorf("a decided card was decided again: %q", hd.Get("HX-Trigger"))
	}
	if rs, _ := c.notifyRules(ruleActive); len(rs) != 1 {
		t.Fatal("a second answer touched the rule in force")
	}
	// Non rejects.
	c.cardProposed(cardRule, r2.ID)
	brainSays(c, "Et les mails ?")
	y := cardOf(t, c, cardRule, r2.ID)
	if _, body, _ := tw.do("POST", fmt.Sprintf("/cards/%d/no", y.ID), url.Values{}, h); !strings.Contains(body, "is-rejected") {
		t.Fatalf("no: %q", body)
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
	mem := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Sam préfère le train", Status: memProposed, SourceKind: srcBrain})
	in := mustIntend(t, m, &Intention{Text: "Appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	w, _ := c.addWatch(&watch{Name: "Revue du dimanche", Source: sourceSchedule, Spec: watchSpec{Schedule: "sunday 18:00"},
		Prompt: "Fais la revue", State: watchProposed, By: "brain"})
	sid, _ := c.proposeSetting(settingProposal{Name: setCap, Value: "3", Why: "Trop de notifications."})
	for _, p := range []struct {
		kind string
		id   int64
	}{{cardMemory, mem.ID}, {cardIntention, in.ID}, {cardWatch, w.ID}, {cardSetting, sid}} {
		c.cardProposed(p.kind, p.id)
	}
	brainSays(c, "J'ai quatre petites choses.")
	for _, kind := range []string{cardMemory, cardIntention, cardWatch, cardSetting} {
		var id int64
		switch kind {
		case cardMemory:
			id = mem.ID
		case cardIntention:
			id = in.ID
		case cardWatch:
			id = w.ID
		case cardSetting:
			id = sid
		}
		x := cardOf(t, c, kind, id)
		if _, err := c.decideCard(ctx, x.ID, kind != cardIntention, "web"); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if x, _ := m.memory(mem.ID); x.Status != memActive {
		t.Error("the memory is not active")
	}
	if x, _ := m.intention(in.ID); x.State != intentCancelled {
		t.Error("the intention is not cancelled")
	}
	if x, _ := c.watch(w.ID); x.State != watchActive {
		t.Error("the watch is not active")
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
	// A proposal from the memory's extraction is queued, never attached.
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction})
	c.syncCards()
	brainSays(c, "Autre chose.")
	if x := cardOf(t, c, cardMemory, 1); x.Seq != 0 {
		t.Fatalf("an extracted proposal was attached: %+v", x)
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
			c.store.set(taintKey(seq), "brain:mail.read")
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
		"an automation": {func() int {
			proposedRule(t, c, "g1", "")
			brainSays(c, "Je propose ça ?")
			seq, _ := c.store.addMessageVia(roleUser, "oui", automationVia, false)
			return int(seq)
		}, "automation"},
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
	w, _ := c.addWatch(&watch{Name: "Revue", Source: sourceSchedule, Spec: watchSpec{Schedule: "sunday 18:00"},
		Prompt: "Fais la revue", State: watchProposed, By: "brain"})
	in := mustIntend(t, c.mem, &Intention{Text: "Appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	mem := mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Sam préfère le train", Status: memProposed, SourceKind: srcBrain})
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je propose ça ?")
	yes := ownerSays(c, "oui, vas-y, active la veille, l'intention, le souvenir et la règle des notifications")
	for name, args := range map[string]map[string]any{
		"update_watch":     {"id": w.ID, "action": "resume", "source_ref": ref(yes)},
		"update_intention": {"id": in.ID, "state": "open", "source_ref": ref(yes)},
		"update_memory":    {"id": mem.ID, "text": "Sam préfère le train", "source_ref": ref(yes)},
	} {
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil && !out.IsError {
			t.Errorf("%s changed a proposal: %v", name, out.Content)
		}
	}
	if x, _ := c.watch(w.ID); x.State != watchProposed {
		t.Error("the brain approved a watch")
	}
	if x, _ := c.mem.intention(in.ID); x.State != intentProposed {
		t.Error("the brain approved an intention")
	}
	if x, _ := c.mem.memory(mem.ID); x.Status != memProposed {
		t.Error("the brain approved a memory")
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

// Queued proposals are offered a few at a time, at a good moment: never in
// the quiet hours, at night, or when a source says not to disturb.
func TestCardsOfferedAtTheRightMoment(t *testing.T) {
	queue := func(c *core, n int) {
		for i := range n {
			mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: fmt.Sprintf("Fait numéro %d", i), Status: memProposed, SourceKind: srcExtraction})
		}
	}
	// The day: up to three, said naturally, then not again within the hour.
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	queue(c, 4)
	now := c.notifyTime()
	offer := c.offerCards(now)
	if !strings.Contains(offer, "à proposer : 3") || !strings.Contains(offer, "J'ai deux petites choses") || !strings.Contains(offer, "Fait numéro 0") {
		t.Fatalf("offer %q", offer)
	}
	if strings.Contains(offer, "Fait numéro 3") {
		t.Error("more than three offered at once")
	}
	if again := c.offerCards(now.Add(10 * time.Minute)); again != "" {
		t.Errorf("offered again within the hour: %q", again)
	}
	seq := brainSays(c, "Voilà. J'ai trois petites choses à te proposer.")
	if xs, _ := c.cardsWhere(`seq = ?`, seq); len(xs) != 3 {
		t.Fatalf("%d cards with the message", len(xs))
	}
	// Not taken by the brain: back to the queue at the turn's end.
	c2, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	queue(c2, 1)
	c2.offerCards(c2.notifyTime())
	c2.cardsTurnEnded()
	if xs, _ := c2.cardsWhere(`seq = 0 AND turn = 0`); len(xs) != 1 {
		t.Fatalf("not back in the queue: %+v", xs)
	}

	for name, setup := range map[string]func(*core) time.Time{
		"night": func(c *core) time.Time { return at(t, "2026-11-04", "23:30") },
		"early": func(c *core) time.Time { return at(t, "2026-11-04", "06:10") },
		"quiet": func(c *core) time.Time {
			r := quietRule("all", "06:00", "11:00")
			r.By, r.Why = "owner", "Le matin, je travaille."
			if _, err := c.addNotifyRule(r); err != nil {
				t.Fatal(err)
			}
			return at(t, "2026-11-04", "10:00")
		},
		"dnd": func(c *core) time.Time {
			now := at(t, "2026-11-04", "10:00")
			s, err := checkSignal(ctxInput{Kind: sigDND, Value: "on", Reason: "game"}, nil, "machine:pc", now)
			if err != nil {
				t.Fatal(err)
			}
			c.putSignals([]ctxSignal{s})
			return now
		},
	} {
		now := at(t, "2026-11-04", "10:00")
		c, _ := notifyCore(t, now)
		queue(c, 2)
		when := setup(c)
		if offer := c.offerCards(when); offer != "" {
			t.Errorf("%s: offered %q", name, offer)
		}
	}
}

// A digest card follows the brain's read of since, with the items and
// their links; a spontaneous message says why it was sent.
func TestDigestAndWhy(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	c.store.insertTask(&TaskRow{ID: "t-9", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "Range les factures"})
	c.logTask("t-9", stateRunning, nil)
	c.cardDigest(time.Now().Add(-time.Hour), time.Now().Add(time.Minute), nil)
	brainSays(c, "Pendant ton absence, j'ai rangé les factures.")
	n, err := c.notify(&notification{Text: "Le serveur répond lentement.", Why: "La veille du site a vu trois erreurs.", Ref: "project:site",
		Source: notifyFromBrain}, notifyItem{Level: levelNow})
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := c.store.addMessage(roleBrain, n.Text, true)
	c.store.db.Exec(`UPDATE notifications SET msg = ? WHERE id = ?`, seq, n.ID)
	_, body, _ := tw.do("GET", "/", nil, nil)
	for _, want := range []string{`class="pcard pdigest"`, "Range les factures", `hx-get="/tasks/t-9"`, `href="/settings/journal"`,
		`class="msg-why"`, "Pourquoi ?", "La veille du site a vu trois erreurs.", "project:site"} {
		if !strings.Contains(body, want) {
			t.Errorf("the conversation lacks %q", want)
		}
	}
}

// The voice asks the one card that came with its answer, short; two are
// not asked aloud.
func TestVoiceCardQuestion(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	seq, _ := c.store.addMessageVia(roleUser, "Moins de notifications pour les factures", "voix, Salon", false)
	proposedRule(t, c, "facture", "")
	brainSays(c, "D'accord.")
	if q := c.voiceCardQuestion(int(seq), "fr"); q != "Je te propose : facture → au brief. Oui ou non ?" {
		t.Fatalf("question %q", q)
	}
	proposedRule(t, c, "banque", "")
	brainSays(c, "Et la banque.")
	if q := c.voiceCardQuestion(int(seq), "fr"); q != "" {
		t.Fatalf("two cards asked aloud: %q", q)
	}
}

// On Telegram, a card follows the reply it goes with, with Oui / Non
// buttons that answer it from the linked account only; "alerts only"
// sends none; a task's permission still has no button there.
func TestTelegramCards(t *testing.T) {
	c, _, f := telegramCore(t)
	linked(c)
	c.tg.restart()
	c.store.addMessageVia(roleUser, "Trop de factures en notification", viaTelegram, false)
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	c.telegramReplies()
	c.telegramCards()
	c.tg.pending.Wait()
	sent := f.sentMessages()
	if len(sent) != 2 || !strings.Contains(fmt.Sprint(sent[1]["text"]), "facture → au brief") {
		t.Fatalf("sent %v", sent)
	}
	x := cardOf(t, c, cardRule, r.ID)
	kb := fmt.Sprint(sent[1]["reply_markup"])
	if !strings.Contains(kb, cardData(x.ID, true)) || !strings.Contains(kb, cardData(x.ID, false)) {
		t.Fatalf("no buttons: %s", kb)
	}
	c.telegramCards()
	c.tg.pending.Wait()
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
	c.store.set(tgDetailKey, detailAlert)
	proposedRule(t, c, "banque", "")
	c.store.addMessageVia(roleUser, "Et la banque ?", viaTelegram, false)
	brainSays(c, "Je fais pareil pour la banque ?")
	before := len(f.sentMessages())
	c.telegramReplies()
	c.telegramCards()
	c.tg.pending.Wait()
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
