package main

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shownAtOnce checks that a card made outside a brain turn is in the
// conversation already, with no user message: under a message of its own
// written as the brain's, among the cards the event stream keeps up to
// date, and in the attention queue with its weight.
func shownAtOnce(t *testing.T, c *core, x convoCard, weight int) {
	t.Helper()
	if x.Seq <= 0 || x.State != cardPending {
		t.Fatalf("card not shown: %+v", x)
	}
	msgs, err := c.store.messagesAfter(x.Seq-1, 1)
	if err != nil || len(msgs) != 1 || msgs[0].Seq != x.Seq || msgs[0].Role != roleBrain || strings.TrimSpace(msgs[0].Text) == "" {
		t.Fatalf("no message of its own: %+v %v", msgs, err)
	}
	if msgs, _ := c.store.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE role = ?`, roleUser); len(msgs) != 0 {
		t.Fatalf("a user message: %+v", msgs)
	}
	w, err := newWeb(c, WebConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(w.liveCards(), func(y convoCard) bool { return y.ID == x.ID }) {
		t.Fatalf("card %d not among the live cards", x.ID)
	}
	if got := attentionOf(c, cardItemRef(x.ID)); got == nil || got.Weight != weight {
		t.Fatalf("attention item %+v, want weight %d", got, weight)
	}
}

func attentionOf(c *core, ref string) *attentionItem {
	for _, it := range c.attentionOpen() {
		if it.Ref == ref {
			return &it
		}
	}
	return nil
}

// Items about the same ref merge: the highest weight, the latest why, a say
// kept unless a new one comes; the heaviest come first.
func TestAttentionMergeByRef(t *testing.T) {
	c := startCore(t)
	at := time.Now().Add(-time.Minute)
	c.attend(attentionItem{Ref: "mail:1", Weight: 4, Why: "a bill due today", Say: "Ta facture est due.", Source: "skill:mail", Time: at})
	c.attend(attentionItem{Ref: "mail:1", Weight: 2, Why: "the bill, paid?", Source: "skill:mail"})
	c.attend(attentionItem{Ref: "session:3", Weight: 9, Why: "the plan waits", Source: "session:3"})
	c.attend(attentionItem{Ref: "agenda:2026-11-12", Weight: 1, Why: "a quiet day", Source: "skill:brief"})
	items := c.attentionOpen()
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Ref != "session:3" || items[0].Weight != 5 {
		t.Errorf("a weight is 1 to 5, the heaviest first: %+v", items[0])
	}
	m := attentionOf(c, "mail:1")
	if m.Weight != 4 || m.Why != "the bill, paid?" || m.Say != "Ta facture est due." || !m.Time.After(at) {
		t.Errorf("merged %+v", m)
	}
	c.attentionDone("mail:1")
	if attentionOf(c, "mail:1") != nil {
		t.Error("an item done stays")
	}
}

// A card enters the queue with a weight by its kind: one a task or a
// session waits on, 4; any other proposal, 3. Decided, it leaves.
func TestAttentionWeightByKind(t *testing.T) {
	for kind, want := range map[string]int{cardTaskStart: 4, cardAccess: 4, cardSession: 4, cardRule: 3, cardMemory: 3, cardRunCalls: 3} {
		if got := cardWeight(kind); got != want {
			t.Errorf("%s: weight %d, want %d", kind, got, want)
		}
	}
	tw, _ := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	x := cardOf(t, c, cardRule, r.ID)
	it := attentionOf(c, cardItemRef(x.ID))
	if it == nil || it.Weight != 3 || it.Source != byBrain || !strings.Contains(it.Why, "facture") {
		t.Fatalf("item %+v", it)
	}
	if !strings.Contains(c.attentionBlock(), cardItemRef(x.ID)) {
		t.Errorf("the brain does not read it: %q", c.attentionBlock())
	}
	if err := c.decideProposal(t.Context(), cardRule, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if attentionOf(c, cardItemRef(x.ID)) != nil || c.attentionBlock() != "" {
		t.Error("a card decided on its page stays in the queue")
	}
}

// A queue made by v0.156.0 (no tainted, delivered nor due) gets its
// columns when the database opens, twice as once; its items, delivered as
// they entered then, stay delivered.
func TestAttentionUpgradeFromV0156(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE attention (ref TEXT PRIMARY KEY, weight INTEGER NOT NULL, why TEXT NOT NULL DEFAULT '',
		say TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '', time INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO attention (ref, weight, why, source, time) VALUES ('mail:1', 3, 'a bill', 'skill:mail', 1000)`)
	db.Close()
	for range 2 {
		s, err := openStore(path)
		if err != nil {
			t.Fatal(err)
		}
		var delivered, due, tainted int64
		if err := s.db.QueryRow(`SELECT delivered, due, tainted FROM attention WHERE ref = 'mail:1'`).Scan(&delivered, &due, &tainted); err != nil || delivered != 1000 {
			t.Fatalf("delivered %d, due %d, tainted %d: %v", delivered, due, tainted, err)
		}
		s.db.Close()
	}
}

func brainMessages(t *testing.T, c *core) []Message {
	t.Helper()
	msgs, err := c.store.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE role = ? ORDER BY seq`, roleBrain)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

// Below weight 4, an item is held until a breakpoint, which delivers what
// is held in one message, the heaviest first; weight 4 and 5 are delivered
// as they enter. A breakpoint with nothing held does nothing.
func TestAttentionHeldUntilBreakpoint(t *testing.T) {
	c, _, now := pollCore(t)
	c.attend(attentionItem{Ref: "mail:1", Weight: 1, Why: "a newsletter", Say: "Une lettre d'information.", Source: "skill:mail", Time: *now})
	c.attend(attentionItem{Ref: "mail:2", Weight: 3, Why: "a bill due today", Say: "Ta facture est due aujourd'hui.", Source: "skill:mail", Time: *now})
	c.attend(attentionItem{Ref: "agenda:2026-11-04", Weight: 2, Why: "a quiet day", Say: "Journée calme.", Source: "skill:agenda", Time: *now})
	c.attend(attentionItem{Ref: "session:3", Weight: 4, Why: "the plan waits", Source: "session:3", Time: *now})
	c.attend(attentionItem{Ref: "site:down", Weight: 5, Why: "the site is down", Source: "skill:prod", Time: *now})
	for ref, held := range map[string]bool{"mail:1": true, "mail:2": true, "agenda:2026-11-04": true, "session:3": false, "site:down": false} {
		if it := attentionOf(c, ref); it == nil || it.Held != held {
			t.Errorf("%s: %+v, want held %v", ref, it, held)
		}
	}
	if it := attentionOf(c, "mail:2"); !it.Due.Equal(now.Add(attentionCap3).Truncate(time.Second)) {
		t.Errorf("a weight 3 item's cap: %v", it.Due)
	}
	if it := attentionOf(c, "mail:1"); !it.Due.IsZero() {
		t.Errorf("a weight 1 item waits for a breakpoint, no time: %v", it.Due)
	}
	if b := c.attentionBlock(); !strings.Contains(b, "mail:2 (poids 3, skill:mail) : a bill due today (retenu") {
		t.Errorf("the brain does not read the held item as held: %q", b)
	}
	if len(brainMessages(t, c)) != 0 {
		t.Fatal("a held item was said")
	}
	if n := c.attentionBreak(breakAnswer, *now); n != 3 {
		t.Fatalf("delivered %d", n)
	}
	msgs := brainMessages(t, c)
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
	bill, day, letter := strings.Index(msgs[0].Text, "facture"), strings.Index(msgs[0].Text, "calme"), strings.Index(msgs[0].Text, "lettre")
	if !strings.HasPrefix(msgs[0].Text, tr("fr", "attention.release")) || bill < 0 || !(bill < day && day < letter) {
		t.Errorf("not in weight order: %q", msgs[0].Text)
	}
	for _, it := range c.attentionOpen() {
		if it.Held {
			t.Errorf("still held: %+v", it)
		}
	}
	if n := c.attentionBreak(breakReturn, *now); n != 0 || len(brainMessages(t, c)) != 1 {
		t.Error("a breakpoint with nothing held said something")
	}
	// A delivered item merged with a new one stays delivered.
	c.attend(attentionItem{Ref: "mail:2", Weight: 2, Why: "the bill, again", Source: "skill:mail", Time: *now})
	if it := attentionOf(c, "mail:2"); it.Held || it.Weight != 3 {
		t.Errorf("merged %+v", it)
	}
}

// Nothing waits past its cap: a weight 3 item goes two hours after it
// entered, breakpoint or not; a lighter one waits for a breakpoint.
func TestAttentionCap(t *testing.T) {
	c, _, now := pollCore(t)
	c.attend(attentionItem{Ref: "mail:old", Weight: 3, Why: "a bill", Say: "Ta facture.", Source: "skill:mail", Time: now.Add(-3 * time.Hour)})
	c.attend(attentionItem{Ref: "mail:new", Weight: 3, Why: "a parcel", Say: "Ton colis.", Source: "skill:mail", Time: now.Add(-time.Hour)})
	c.attend(attentionItem{Ref: "mail:glance", Weight: 2, Why: "a newsletter", Source: "skill:mail", Time: now.Add(-10 * time.Hour)})
	if n := c.attentionBreak(breakCap, *now); n != 1 {
		t.Fatalf("delivered %d at the cap", n)
	}
	if msgs := brainMessages(t, c); len(msgs) != 1 || msgs[0].Text != "Ta facture." {
		t.Errorf("messages %+v", msgs)
	}
	if !attentionOf(c, "mail:new").Held || !attentionOf(c, "mail:glance").Held || attentionOf(c, "mail:old").Held {
		t.Errorf("queue %+v", c.attentionOpen())
	}
	if n := c.attentionBreak(breakCap, now.Add(time.Hour)); n != 1 || !attentionOf(c, "mail:glance").Held {
		t.Errorf("the second cap delivered %d", n)
	}
}

// The end of the brain's answer to a user message is a breakpoint, once:
// a turn that answers no new message (a wake) is not.
func TestAttentionAnswerBreakpoint(t *testing.T) {
	c, _, now := pollCore(t)
	c.attend(attentionItem{Ref: "mail:1", Weight: 3, Why: "a bill", Say: "Ta facture.", Source: "skill:mail", Time: *now})
	ownerSays(c, "Salut")
	brainSays(c, "Salut Sam.")
	c.attentionAnswered(*now)
	if msgs := brainMessages(t, c); len(msgs) != 2 || msgs[1].Text != "Ta facture." {
		t.Fatalf("messages %+v", msgs)
	}
	c.attend(attentionItem{Ref: "mail:2", Weight: 3, Why: "a parcel", Say: "Ton colis.", Source: "skill:mail", Time: *now})
	c.attentionAnswered(*now) // a wake's turn
	if !attentionOf(c, "mail:2").Held {
		t.Error("a turn that answered no new message was a breakpoint")
	}
}

// A notification held for the brief waits in the queue for the user's
// return, not for another breakpoint; the return tells it, as the brief.
func TestAttentionReturnTellsTheBrief(t *testing.T) {
	c, _, now := pollCore(t)
	c.subscribeEvents(c.attentionOnEvent)
	n, err := c.notify(&notification{Text: "La prod a redémarré cette nuit.", Why: "Skill « prod »", Ref: "skill:prod", Source: notifyFromSkill},
		notifyItem{Level: levelBrief, Ref: "skill:prod"})
	if err != nil || !n.Held {
		t.Fatalf("%+v %v", n, err)
	}
	if it := attentionOf(c, noteItemRef(n.ID)); it == nil || !it.Held || it.Weight != weightHeldNote || it.Source != "skill:prod" {
		t.Fatalf("item %+v", it)
	}
	if c.attentionBreak(breakAnswer, *now) != 0 || c.attentionBreak(breakSession, *now) != 0 {
		t.Fatal("the brief was told before the return")
	}
	say := func(when time.Time) {
		seq := ownerSays(c, "salut")
		c.store.db.Exec(`UPDATE messages SET time = ? WHERE seq = ?`, when.Unix(), seq)
	}
	say(now.Add(-5 * time.Hour))
	say(now.Add(-2 * time.Minute))
	c.tickReturned(*now)
	msgs := brainMessages(t, c)
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, tr("fr", "attention.brief")) || !strings.Contains(msgs[0].Text, "redémarré") {
		t.Fatalf("brief %+v", msgs)
	}
	if n, _ = c.notificationByID(n.ID); n.Held || n.Msg != msgs[0].Seq {
		t.Errorf("notification %+v", n)
	}
	if it := attentionOf(c, noteItemRef(n.ID)); it == nil || it.Held || c.store.get(lastBriefKey) != strconv.FormatInt(now.Unix(), 10) {
		t.Error("the brief is not kept as given")
	}
	// « Pas maintenant » holds it again for the next brief.
	if _, err := c.notificationFeedback(n.ID, "later"); err != nil {
		t.Fatal(err)
	}
	if it := attentionOf(c, noteItemRef(n.ID)); it == nil || !it.Held {
		t.Errorf("not held again: %+v", it)
	}
}

// A skill's plain message in the quiet hours: its item (weight 4) is in
// the queue at once, its push kept back; the brief tells it on return.
func TestAttentionQuietMessageToldAtTheBrief(t *testing.T) {
	c, _, now := pollCore(t)
	*now = at(t, "2026-11-04", "23:30")
	n, err := c.notify(&notification{Text: "Le site est revenu.", Why: "Skill « prod »", Ref: "skill:prod", Source: notifyFromSkill},
		notifyItem{Level: levelNow, Ref: "skill:prod"})
	if err != nil || !n.Held || n.Reason != reasonQuiet {
		t.Fatalf("%+v %v", n, err)
	}
	c.attend(runSignal(n.ID, n.Text, false, levelNow, "skill:prod", false))
	if it := attentionOf(c, noteItemRef(n.ID)); it == nil || it.Held || it.Weight != weightRunNow {
		t.Fatalf("item %+v", it)
	}
	if c.attentionBreak(breakAnswer, *now) != 0 {
		t.Fatal("told before the return")
	}
	if c.attentionBreak(breakReturn, *now) != 1 {
		t.Fatal("not told at the brief")
	}
	if n, _ = c.notificationByID(n.ID); n.Held {
		t.Error("still held for the brief")
	}
}

// A session's end is a breakpoint while the user is there; away, what is
// held waits for their return.
func TestAttentionSessionEndBreakpoint(t *testing.T) {
	c, _, now := pollCore(t)
	c.subscribeEvents(c.attentionOnEvent)
	seq := ownerSays(c, "Lance la session")
	c.store.db.Exec(`UPDATE messages SET time = ? WHERE seq = ?`, now.Add(-3*time.Hour).Unix(), seq)
	c.attend(attentionItem{Ref: "mail:1", Weight: 2, Why: "a newsletter", Say: "Une lettre.", Source: "skill:mail", Time: *now})
	c.emitLogged(journalEvent{Kind: evSessionEnded, Key: "session:1", Source: "session"})
	if !attentionOf(c, "mail:1").Held {
		t.Fatal("released while the user is away")
	}
	seq = ownerSays(c, "Me revoilà")
	c.store.db.Exec(`UPDATE messages SET time = ? WHERE seq = ?`, now.Add(-time.Minute).Unix(), seq)
	c.emitLogged(journalEvent{Kind: evSessionEnded, Key: "session:2", Source: "session"})
	if attentionOf(c, "mail:1").Held {
		t.Fatal("not released at the session's end")
	}
}

// A ref is a known kind, a colon and an id of that kind's form.
func TestParseRef(t *testing.T) {
	for _, tc := range []struct{ in, kind, id string }{
		{"mail:INBOX/42", refMail, "INBOX/42"},
		{"mail:a:b:c", refMail, "a:b:c"},
		{"agenda:2026-11-12", refAgenda, "2026-11-12"},
		{"event:ev-1@example.org", refEvent, "ev-1@example.org"},
		{"session:3", refSession, "3"},
		{"card:12", refCard, "12"},
		{"task:t-abc", refTask, "t-abc"},
		{"project:portfolio", refProject, "portfolio"},
		{"alert:7", refAlert, "7"},
		{"surface:agenda", refSurface, "agenda"},
		{" task:t-1 ", refTask, "t-1"},
	} {
		r, err := parseRef(tc.in)
		if err != nil || r.Kind != tc.kind || r.ID != tc.id || r.String() != tc.kind+":"+tc.id {
			t.Errorf("%q: %+v %v", tc.in, r, err)
		}
	}
	for _, bad := range []string{"", "mail", "mail:", "note:1", "agenda:12/11/2026", "agenda:2026-13-01",
		"session:x", "session:0", "card:-1", "surface:Not An Id", "task:" + strings.Repeat("x", 1001)} {
		var e errBadRef
		if _, err := parseRef(bad); !errors.As(err, &e) {
			t.Errorf("%q parsed: %v", bad, err)
		}
	}
}
