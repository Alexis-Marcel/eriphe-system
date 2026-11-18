package main

import (
	"context"
	"slices"
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

// brainEnds makes the brain's turn end on text, as its device reports it.
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

// The end of a task about a live thread is an event of that thread; of a
// task about none, or about an ended one, the home's.
func TestTaskEventOfItsThread(t *testing.T) {
	c, _, _, th := talkCore(t)
	for _, task := range []*TaskRow{
		{ID: "t-1", Kind: "worker", Device: "box", Runtime: "claude-code", State: stateStopped, Prompt: "x", About: threadRef(th.ID)},
		{ID: "t-2", Kind: "worker", Device: "box", Runtime: "claude-code", State: stateStopped, Prompt: "x"},
	} {
		if err := c.store.insertTask(task); err != nil {
			t.Fatal(err)
		}
	}
	c.notifyBrain("t-1", stateStopped)
	if ev := lastMessage(t, c, roleEvent); !strings.Contains(ev.Text, "t-1") || ev.Thread != th.ID {
		t.Fatalf("a task of the thread: %+v", ev)
	}
	c.notifyBrain("t-2", stateStopped)
	if ev := lastMessage(t, c, roleEvent); !strings.Contains(ev.Text, "t-2") || ev.Thread != 0 {
		t.Fatalf("a task of none: %+v", ev)
	}
}

// A thread's event belongs to it, and a turn of events alone answers in
// the thread of its last event of a thread; the home otherwise. A failure
// shows where the answer would have.
func TestThreadEventTalk(t *testing.T) {
	c, _, _, th := talkCore(t)
	c.store.addThreadMessage(roleEvent, "[thread 1 « x »] carte acceptée", "", th.ID, true) // as callToBrain writes it
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
	report("t-brain", deviceReport{State: stateIdle, Events: []EventRow{
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
