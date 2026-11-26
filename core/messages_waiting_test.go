package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitingCore is a core whose brain is idle, its workspace signed in: the
// session's CSRF token.
func waitingCore(t *testing.T) (*core, *testWeb, string) {
	t.Helper()
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	tw := newTestWebFor(t, c)
	return c, tw, tw.setup()
}

// deliverNow is one delivery to the idle brain: the text of its turn, ""
// when nothing was given.
func deliverNow(t *testing.T, c *core) string {
	t.Helper()
	c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	c.store.db.Exec(`UPDATE inbox SET acked = 1`)
	if err := tendBrain(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox("t-brain")
	if len(items) == 0 {
		return ""
	}
	text, _ := items[len(items)-1].Data["text"].(string)
	return text
}

func waitingSeqs(ls []waitingLine) []int {
	var out []int
	for _, l := range ls {
		out = append(out, l.Seq)
	}
	return out
}

// The messages that wait for Eriphe are in the orb's event, in order,
// with their node; one withdrawn never reaches the brain; once delivered,
// none waits and one cannot be withdrawn any more.
func TestWaitingMessagesWithdrawn(t *testing.T) {
	c, tw, csrf := waitingCore(t)
	openGraph(t, c)
	node := siteNode(t, c)
	first, _ := c.store.addUserMessage("Et pour le budget ?", nil, "")
	second, _ := c.store.addUserMessage("Et pour le planning ?", nil, node)
	third, _ := c.store.addUserMessage("Et les devis ?", nil, "")

	x, err := tw.w.orbFor("", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(waitingSeqs(x.Waiting), []int{int(first), int(second), int(third)}) || x.Asked != 0 {
		t.Fatalf("waiting while Eriphe works: %+v", x)
	}
	if w := x.Waiting[1]; w.Text != "Et pour le planning ?" || w.Node != node || w.Title == "" {
		t.Errorf("the second, said to a node: %+v", w)
	}
	if x.Waiting[0].Node != "" || x.Waiting[0].Title != "" {
		t.Errorf("the first, said at the home: %+v", x.Waiting[0])
	}
	// Eriphe idle: a message just sent is taken at once, not shown waiting.
	if x, _ = tw.w.orbFor("", false); len(x.Waiting) != 0 {
		t.Errorf("a fresh message shown waiting while Eriphe is idle: %+v", x.Waiting)
	}

	withdraw := func(seq int64) int {
		t.Helper()
		code, _, _ := tw.do("DELETE", fmt.Sprintf("/messages/%d", seq), nil, map[string]string{"X-CSRF-Token": csrf})
		return code
	}
	if code, _, _ := tw.do("DELETE", fmt.Sprintf("/messages/%d", second), nil, nil); code != http.StatusForbidden {
		t.Errorf("withdrawn without the CSRF token: %d", code)
	}
	if code := withdraw(second); code != http.StatusNoContent {
		t.Fatalf("withdraw: %d", code)
	}
	if code := withdraw(second); code != http.StatusNotFound {
		t.Errorf("withdrawn twice: %d", code)
	}
	if x, _ = tw.w.orbFor("", true); !slices.Equal(waitingSeqs(x.Waiting), []int{int(first), int(third)}) {
		t.Errorf("after the withdrawal: %+v", x.Waiting)
	}

	turn := deliverNow(t, c)
	if !strings.Contains(turn, "Et pour le budget ?") || !strings.Contains(turn, "Et les devis ?") || strings.Contains(turn, "planning") {
		t.Fatalf("the turn given to the brain:\n%s", turn)
	}
	msgs, _ := c.store.messagesAfter(0, 100)
	for _, m := range msgs {
		if m.Seq == int(second) || strings.Contains(m.Text, "planning") {
			t.Errorf("the withdrawn message is still in the conversation: %+v", m)
		}
	}
	if x, _ = tw.w.orbFor("", true); len(x.Waiting) != 0 || x.Asked != int(third) {
		t.Errorf("delivered: %+v", x)
	}
	if code := withdraw(first); code != http.StatusConflict {
		t.Errorf("a delivered message withdrawn: %d", code)
	}
	brain, _ := c.store.addMessage(roleBrain, "C'est noté.", true)
	if code := withdraw(brain); code != http.StatusNotFound {
		t.Errorf("Eriphe's message withdrawn: %d", code)
	}
}

// The bar's message with files (orb/Bar.tsx): said to a node, it is the
// node's; while it waits, the bar shows its files' names, a message of
// files alone too; delivered, the brain's turn tells it of each file, to
// read it with read_attachment.
func TestWaitingMessageWithFiles(t *testing.T) {
	c, tw, csrf := waitingCore(t)
	node := siteNode(t, c)
	fields := map[string]string{"text": "Regarde ça.", "node": node, "from": "workspace"}
	if code, body := tw.sendForm(csrf, fields, upload{"capture.png", pngBytes(t)}, upload{"notes.txt", []byte("une ligne")}); code != http.StatusNoContent {
		t.Fatalf("send: %d %s", code, body)
	}
	if code, body := tw.sendForm(csrf, map[string]string{"text": "", "from": "workspace"}, upload{"seul.md", []byte("# seul")}); code != http.StatusNoContent {
		t.Fatalf("files alone: %d %s", code, body)
	}
	msgs, _ := c.store.waitingMessages()
	if len(msgs) != 2 || msgs[0].Node != node || len(msgs[0].Attachments) != 2 || msgs[1].Node != "" {
		t.Fatalf("waiting: %+v", msgs)
	}

	x, err := tw.w.orbFor("", true)
	if err != nil || len(x.Waiting) != 2 {
		t.Fatalf("orb: %v %+v", err, x)
	}
	if w := x.Waiting[0]; w.Text != "Regarde ça." || !slices.Equal(w.Files, []string{"capture.png", "notes.txt"}) || w.Node != node {
		t.Errorf("the message with files: %+v", w)
	}
	if w := x.Waiting[1]; w.Text != "" || !slices.Equal(w.Files, []string{"seul.md"}) {
		t.Errorf("files alone: %+v", w)
	}

	turn := deliverNow(t, c)
	for _, a := range msgs[0].Attachments {
		if !strings.Contains(turn, "[pièce jointe "+a.ID) || !strings.Contains(turn, a.Name) {
			t.Errorf("the brain is not told of %s:\n%s", a.Name, turn)
		}
	}
	if !strings.Contains(turn, "Regarde ça.") || !strings.Contains(turn, "seul.md") {
		t.Errorf("the turn given to the brain:\n%s", turn)
	}
}

// A plain yes or no that decided a card has acted: it cannot be withdrawn.
func TestWaitingMessageThatDecidedACard(t *testing.T) {
	c, _, _ := waitingCore(t)
	seq, _ := c.store.addUserMessage("oui", nil, "")
	c.store.db.Exec(`UPDATE messages SET card_answer = ? WHERE seq = ?`, "approved|p-1|Garder un œil", seq)
	if err := c.withdrawMessage(int(seq)); !errors.Is(err, errMessageTaken) {
		t.Fatalf("withdraw: %v", err)
	}
	if msgs, _ := c.store.waitingMessages(); len(msgs) != 1 {
		t.Errorf("waiting: %+v", msgs)
	}
}

// A withdrawal tried while a delivery holds what it read waits for it,
// and finds the message taken.
func TestWaitingWithdrawDuringDelivery(t *testing.T) {
	c, _, _ := waitingCore(t)
	seq, _ := c.store.addUserMessage("Et pour le budget ?", nil, "")
	var werr error
	done := make(chan struct{})
	c.deliverRead = func() {
		c.deliverRead = nil
		started := make(chan struct{})
		go func() {
			close(started)
			werr = c.withdrawMessage(int(seq))
			close(done)
		}()
		<-started
		time.Sleep(100 * time.Millisecond) // the withdrawal would be through, were it not held
	}
	turn := deliverNow(t, c)
	<-done
	if !strings.Contains(turn, "Et pour le budget ?") || !errors.Is(werr, errMessageTaken) {
		t.Fatalf("withdraw: %v; turn:\n%s", werr, turn)
	}
}

// Withdrawing races the delivery: a message is either in the brain's turn
// or withdrawn, never both, never neither.
func TestWaitingWithdrawRacesDelivery(t *testing.T) {
	c, _, _ := waitingCore(t)
	for i := range 30 {
		keep := fmt.Sprintf("garde %d", i)
		gone := fmt.Sprintf("retire %d", i)
		c.store.addUserMessage(keep, nil, "")
		seq, _ := c.store.addUserMessage(gone, nil, "")
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
		c.store.db.Exec(`UPDATE inbox SET acked = 1`)
		var wg sync.WaitGroup
		var werr error
		wg.Add(2)
		go func() { defer wg.Done(); werr = c.withdrawMessage(int(seq)) }()
		go func() { defer wg.Done(); tendBrain(context.Background(), c) }()
		wg.Wait()
		items, _ := c.store.inbox("t-brain")
		var turn string
		for _, it := range items {
			text, _ := it.Data["text"].(string)
			turn += text
		}
		if turn == "" { // the withdrawal went first and the brain was handed nothing yet
			turn = deliverNow(t, c)
		}
		given := strings.Contains(turn, gone)
		switch {
		case werr == nil && given:
			t.Fatalf("round %d: withdrawn, and given to the brain", i)
		case errors.Is(werr, errMessageTaken) && !given:
			t.Fatalf("round %d: not withdrawn, and not given to the brain", i)
		case werr != nil && !errors.Is(werr, errMessageTaken):
			t.Fatalf("round %d: %v", i, werr)
		}
		if !strings.Contains(turn, keep) {
			t.Fatalf("round %d: the message kept was not given:\n%s", i, turn)
		}
	}
}

// The words of a button of a block marked external, delivered, taint the
// turn as an untrusted channel's message does, and say so; a clean
// block's are the user's own.
func TestExternalAskTaintsItsTurn(t *testing.T) {
	c, _, _ := waitingCore(t)
	if err := c.addAsk("Ajoute mes mails", "n1", false); err != nil {
		t.Fatal(err)
	}
	if turn := deliverNow(t, c); !strings.Contains(turn, "Ajoute mes mails") || len(c.turnTaint()) != 0 {
		t.Fatalf("a clean block's ask: taint %v; turn:\n%s", c.turnTaint(), turn)
	}
	if err := c.addAsk("Ajoute mes mails", "n1", true); err != nil {
		t.Fatal(err)
	}
	turn := deliverNow(t, c)
	if !strings.Contains(turn, "bouton d'un bloc extérieur") || !containsStr(c.turnTaint(), askTaintPrefix+"n1/changer") {
		t.Fatalf("an external block's ask: taint %v; turn:\n%s", c.turnTaint(), turn)
	}
}
