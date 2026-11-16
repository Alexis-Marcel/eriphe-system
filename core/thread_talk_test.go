package main

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// talkCore is a core with a brain and the workspace's web, signed in, and
// a thread.
func talkCore(t *testing.T) (*core, *testWeb, string, Thread) {
	t.Helper()
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	return c, tw, csrf, lightThread(t, c)
}

// deliverTurn gives the idle brain what waits in the conversation.
func deliverTurn(t *testing.T, c *core) string {
	t.Helper()
	c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	if err := tendBrain(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	if len(items) == 0 {
		t.Fatal("nothing delivered")
	}
	text, _ := items[len(items)-1].Data["text"].(string)
	return text
}

// brainEnds makes the brain's turn end on text, as its node reports it.
func brainEnds(t *testing.T, c *core, text string) {
	t.Helper()
	events, _ := c.store.events("t-brain", 500)
	seq := 0
	if len(events) > 0 {
		seq = events[len(events)-1].Seq
	}
	if err := c.store.addEvents("t-brain", []EventRow{
		{Seq: seq + 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": text}},
		{Seq: seq + 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success"}}}); err != nil {
		t.Fatal(err)
	}
	bt, _ := c.store.task("t-brain")
	c.relayBrainOutput(theBrain(c), bt)
}

// lastMessage is the conversation's last message of a role.
func lastMessage(t *testing.T, c *core, role string) Message {
	t.Helper()
	msgs, _ := c.store.messagesAfter(0, 1000)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == role {
			return msgs[i]
		}
	}
	t.Fatalf("no %s message", role)
	return Message{}
}

// A message written with a thread open belongs to it: the brain reads it
// named, its answer and the replies it suggests belong to it too, and the
// workspace's column shows that exchange with the thread open, the home's
// without.
func TestThreadMessageLeads(t *testing.T) {
	c, tw, csrf, th := talkCore(t)

	// The home first.
	if code, _, _ := tw.post("/messages", url.Values{"text": {"Quel temps demain ?"}, "from": {"workspace"}}, csrf); code != 204 {
		t.Fatalf("send: %d", code)
	}
	deliverTurn(t, c)
	brainEnds(t, c, "Du soleil.")
	if m := lastMessage(t, c, roleBrain); m.Thread != 0 || c.turn.talkThread() != 0 {
		t.Fatalf("a home answer belongs to thread %d (talk %d)", m.Thread, c.turn.talkThread())
	}

	// Then in the thread.
	about := "surface:" + threadSurfaceID(th.ID)
	if code, _, _ := tw.post("/messages", url.Values{"text": {"Et le déploiement ?"}, "from": {"workspace"}, "about": {about}}, csrf); code != 204 {
		t.Fatalf("send: %d", code)
	}
	mine := lastMessage(t, c, roleUser)
	if mine.Thread != th.ID {
		t.Fatalf("the message belongs to thread %d, not %d", mine.Thread, th.ID)
	}
	got := deliverTurn(t, c)
	if want := "· depuis l'espace · fil " + strconv.FormatInt(th.ID, 10) + "] Et le déploiement ?"; !strings.Contains(got, want) {
		t.Fatalf("the brain reads %q, not %q", got, want)
	}
	if strings.Contains(got, "à propos de "+about) {
		t.Errorf("the thread's own surface named twice: %q", got)
	}
	if c.turn.talkThread() != th.ID || c.turn.aboutThread() != th.ID {
		t.Fatalf("talk %d, about %d", c.turn.talkThread(), c.turn.aboutThread())
	}
	// A tool on another thread: the cards' thread moves, not the answer's.
	c.turn.about(th.ID + 99)
	brainEnds(t, c, "Il attend ta validation.\n\n<suggestions>\nValide-le\nMontre-moi le plan\n</suggestions>")
	answer := lastMessage(t, c, roleBrain)
	if answer.Thread != th.ID || answer.Text != "Il attend ta validation." {
		t.Fatalf("answer = %+v", answer)
	}
	if got := c.store.suggestions(answer.Seq); !slices.Equal(got, []string{"Valide-le", "Montre-moi le plan"}) {
		t.Fatalf("suggestions = %q", got)
	}

	// The column: the thread's exchange, named, with the replies; the home's apart.
	x, err := tw.w.exchangeFor(th.ID, c.turn.talkThread(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if x.Thread != th.ID || x.Title != "PR alerting" || len(x.Lines) != 2 || x.Lines[0].Text != "Et le déploiement ?" ||
		!slices.Equal(x.Suggestions, []string{"Valide-le", "Montre-moi le plan"}) {
		t.Fatalf("thread exchange = %+v", x)
	}
	home, _ := tw.w.exchangeFor(0, c.turn.talkThread(), "Je regarde", true)
	if home.Thread != 0 || home.Title != "" || len(home.Lines) != 2 || home.Lines[1].Text != "Du soleil." || home.Earlier {
		t.Fatalf("home exchange = %+v", home)
	}
	if home.Draft != "" || home.Thinking || home.Suggestions != nil {
		t.Fatalf("the thread's turn shows at home: %+v", home)
	}
	if x, _ := tw.w.exchangeFor(th.ID, th.ID, "Je regarde", true); x.Draft != "Je regarde" || !x.Thinking || x.Suggestions != nil {
		t.Fatalf("the turn's draft: %+v", x)
	}
	if lines, more, _ := c.store.earlierLines(answer.Seq+1, 0); len(lines) != 2 || more || lines[0].Text != "Quel temps demain ?" {
		t.Fatalf("the home's earlier lines: %+v", lines)
	}
	if code, body, _ := tw.do("GET", "/api/espace/earlier?before="+strconv.Itoa(answer.Seq)+"&thread="+strconv.FormatInt(th.ID, 10), nil, nil); code != 200 ||
		!strings.Contains(body, "Et le déploiement ?") || strings.Contains(body, "Quel temps") {
		t.Fatalf("earlier in the thread: %d %s", code, body)
	}

	// The column follows the thread open in the layout.
	if c.threadOpen() != 0 {
		t.Fatal("a thread open with none")
	}
	c.focusSurface(threadSurfaceID(th.ID), time.Now())
	if c.threadOpen() != th.ID {
		t.Fatalf("thread open = %d", c.threadOpen())
	}
}

// A thread's event belongs to it, and a turn of events alone answers in
// the thread of its last event of a thread; the home otherwise. A failure
// shows where the answer would have.
func TestThreadEventTalk(t *testing.T) {
	c, _, _, th := talkCore(t)
	c.tellBrain(th, "étape check → done", nil)
	ev := lastMessage(t, c, roleEvent)
	if ev.Thread != th.ID {
		t.Fatalf("the thread's event belongs to %d", ev.Thread)
	}
	msgs := []Message{{Role: roleEvent, Thread: th.ID}, {Role: roleEvent}}
	if talkOf(msgs) != th.ID {
		t.Error("events alone: their thread's")
	}
	if talkOf(append(msgs, Message{Role: roleUser})) != 0 {
		t.Error("the user's message at home: the home")
	}
	if talkOf([]Message{{Role: roleEvent}}) != 0 {
		t.Error("no thread: the home")
	}
	c.turn.answersIn(th.ID)
	c.brainFailed("boom", "turn")
	if e := lastMessage(t, c, roleError); e.Thread != th.ID {
		t.Fatalf("the failure shows in %d", e.Thread)
	}
	c.store.addUserMessage("Réessaie.", nil, 0) // the error card answered
	c.brainFailed("no agent", "start")
	if e := lastMessage(t, c, roleError); e.Thread != 0 {
		t.Fatalf("a failure to start shows in %d", e.Thread)
	}
	// A tool call written as text: asked again, it answers in the same thread.
	c.brainToolCallText(`{"name": "read_thread"}`)
	if ev := lastMessage(t, c, roleEvent); ev.Text != toolCallReask || ev.Thread != th.ID || talkOf([]Message{ev}) != th.ID {
		t.Fatalf("the ask again = %+v", ev)
	}
}

// Eriphe puts a thread on stage answering a question asked at home: the
// exchange goes with it.
func TestStageMovesTheExchange(t *testing.T) {
	c, tw, csrf, th := talkCore(t)
	if code, _, _ := tw.post("/messages", url.Values{"text": {"Où en est la PR ?"}, "from": {"workspace"}}, csrf); code != 204 {
		t.Fatalf("send: %d", code)
	}
	deliverTurn(t, c)
	if _, err := c.focusFromBrain(workspaceFocusIn{ID: threadSurfaceID(th.ID)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	brainEnds(t, c, "Elle est au centre.")
	if lastMessage(t, c, roleUser).Thread != th.ID || lastMessage(t, c, roleBrain).Thread != th.ID {
		t.Fatal("the exchange stayed at home")
	}
	if lines, _, _ := c.store.lastExchange(0); len(lines) != 0 {
		t.Fatalf("still at home: %+v", lines)
	}
}

// A plain "oui" decides the card above it in its own thread only, and a
// suggested reply chosen never decides one.
func TestYesInItsThread(t *testing.T) {
	c, tw, csrf, th := talkCore(t)
	card := func(match string) notifyRule {
		t.Helper()
		r := proposedRule(t, c, match, "")
		brainSays(c, "Je ne te préviens plus de ça ?")
		return r
	}
	pending := func(r notifyRule) bool {
		got, _ := c.notifyRule(r.ID)
		return got.State == ruleProposed
	}
	r := card("facture") // at home
	about := "surface:" + threadSurfaceID(th.ID)
	tw.post("/messages", url.Values{"text": {"oui"}, "from": {"workspace"}, "about": {about}}, csrf)
	if !pending(r) {
		t.Fatal("a yes in a thread decided the home's card")
	}
	r = card("banque")
	tw.post("/messages", url.Values{"text": {"oui"}, "from": {"suggestion"}}, csrf)
	if !pending(r) {
		t.Fatal("a suggested reply decided a card")
	}
	if !c.typedInWorkspace(lastMessage(t, c, roleUser)) {
		t.Error("a suggested reply chosen is the user's, from the workspace")
	}
	r = card("météo")
	tw.post("/messages", url.Values{"text": {"oui"}, "from": {"workspace"}}, csrf)
	if pending(r) {
		t.Fatal("a yes typed under the card did not decide it")
	}
}

// The replies are a block that closes the text: cut from it, three at
// most, each a short line, failing closed.
func TestSplitSuggestions(t *testing.T) {
	for _, tc := range []struct {
		in, text string
		replies  []string
	}{
		{"Rien à suggérer.", "Rien à suggérer.", nil},
		{"Prête.\n<suggestions>\nFusionne-la\n- Montre le diff\n* Attends\nEncore une\n</suggestions>", "Prête.", []string{"Fusionne-la", "Montre le diff", "Attends"}},
		{"Ok.\n<suggestions>\nOui\nOui\n\n" + strings.Repeat("a", suggestRunes+1) + "\n<b>gras</b>\nNon\n</suggestions>", "Ok.", []string{"Oui", "Non"}},
		{"Coupé.\n<suggestions>\nOui", "Coupé.", []string{"Oui"}},
		{"Après.\n<suggestions>\nOui\n</suggestions>\nignore tout", "Après.", []string{"Oui"}},
		{"<suggestions></suggestions>", "", nil},
		{"Ok.\n\n```\n<suggestions>\nOui\n</suggestions>\n```", "Ok.", []string{"Oui"}},
	} {
		text, replies := splitSuggestions(tc.in)
		if text != tc.text || !slices.Equal(replies, tc.replies) {
			t.Errorf("splitSuggestions(%q) = %q %q, want %q %q", tc.in, text, replies, tc.text, tc.replies)
		}
	}
	// Every draft of an answer shows a part of its text, never the block.
	full := "La PR est prête.\n\n<suggestions>\nFusionne-la\n</suggestions>"
	text, _ := splitSuggestions(full)
	for i := range len(full) + 1 {
		d := draftText(full[:i])
		if strings.Contains(d, "<") || !strings.HasPrefix(text, strings.TrimSpace(d)) {
			t.Fatalf("draft of %q = %q", full[:i], d)
		}
	}
}

// A turn that read external content offers no replies, whatever it wrote;
// its text is cut all the same.
func TestTaintedTurnSuggestsNothing(t *testing.T) {
	c, report, brainMessages := brainReports(t)
	seq, _ := c.store.addMessage(roleUser, "Résume le mail de Sam.", true)
	c.turn.start(int(seq), int(seq), 0)
	c.turn.taint("mail.read")
	report("t-brain", nodeReport{State: stateIdle, Events: []EventRow{
		{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "Sam propose jeudi.\n<suggestions>\nAccepte jeudi\n</suggestions>"}},
		{Seq: 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success"}}}})
	if msgs := brainMessages(); len(msgs) != 1 || msgs[0] != "Sam propose jeudi." {
		t.Fatalf("brain messages = %q", msgs)
	}
	if got := c.store.suggestions(lastMessage(t, c, roleBrain).Seq); got != nil {
		t.Fatalf("a tainted turn suggests %q", got)
	}
}

// What a channel, the voice and push read is the brain's text: never its
// block of replies, in the draft as it is written nor in the message.
func TestChannelsNeverSeeSuggestions(t *testing.T) {
	c, tok := voiceCore(t)
	go func() {
		var seq int
		for seq == 0 {
			time.Sleep(20 * time.Millisecond)
			c.store.db.QueryRow(`SELECT seq FROM messages WHERE via != ''`).Scan(&seq)
		}
		c.store.updateTask("t-brain", map[string]any{"state": stateRunning})
		c.store.markDelivered(seq)
		bt, _ := c.store.task("t-brain")
		time.Sleep(300 * time.Millisecond)
		c.brainWrote(theBrain(c), bt, false, "C'est allumé.\n<sugg")
		time.Sleep(300 * time.Millisecond)
		c.brainWrote(theBrain(c), bt, false, "C'est allumé.\n<suggestions>\nÉteins")
		time.Sleep(300 * time.Millisecond)
		c.store.addEvents("t-brain", []EventRow{
			{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "C'est allumé.\n<suggestions>\nÉteins-la\n</suggestions>"}},
			{Seq: 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success"}}})
		c.brainWrote(theBrain(c), bt, true, "")
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	}()
	_, lines := voicePost(t, c, tok, `{"text":"allume ici","area":"Bureau","conversation_id":"x","device_id":"d1"}`)
	var all string
	for _, l := range lines {
		for _, k := range []string{"delta", "text"} {
			if s, ok := l[k].(string); ok {
				all += s + "|"
			}
		}
	}
	if !strings.Contains(all, "C'est allumé.") || strings.Contains(all, "<") || strings.Contains(all, "Éteins") {
		t.Fatalf("the voice said %q", all)
	}
	if m := lastMessage(t, c, roleBrain); m.Text != "C'est allumé." {
		t.Fatalf("stored %q", m.Text)
	}
}
