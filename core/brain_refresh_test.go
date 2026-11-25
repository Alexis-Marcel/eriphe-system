package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// refreshCore is a core whose idle brain task t-brain ran session s-1,
// its turns' context per call as given, the oldest first.
func refreshCore(t *testing.T, turns ...map[string]any) *core {
	t.Helper()
	c, _, _ := waitingCore(t)
	c.store.updateTask("t-brain", map[string]any{"session_id": "s-1"})
	var events []EventRow
	for i, u := range turns {
		events = append(events, EventRow{Seq: i + 1, Time: time.Now().Add(time.Duration(i-len(turns)) * time.Minute),
			Type: "turn_end", Data: map[string]any{"subtype": "success", "usage": u}})
	}
	if err := c.store.addEvents("t-brain", events); err != nil {
		t.Fatal(err)
	}
	setBrainStatus(c, func(st *AssistantStatus) {
		st.Session, st.SessionRuntime, st.Quiet = "s-1", "claude-code", false
	})
	return c
}

// A running session past brainSessionTokens starts fresh at its next
// idle: its task stops before anything is delivered to it, the next one
// starts a new session with the pack, and what waited (the user's message,
// a card, the turn's taint) goes to it.
func TestBrainSessionOutgrownStartsFresh(t *testing.T) {
	// Its last turn failed and reported no usage: the bound reads the
	// last exact measure, the resume rule the last turn, which says
	// nothing; the fresh start must not rest on it.
	c := refreshCore(t, map[string]any{"context_tokens": 45000}, map[string]any{"context_tokens": 120000}, map[string]any{})
	ctx := context.Background()
	say(t, c, roleUser, "Rappelle-moi le devis", true)
	waiting, _ := c.store.addUserMessage("Et le budget ?", nil, "")
	ask := c.newAccessAsk(accessQuestion{TaskID: "t-brain", Need: accessNeed{Server: "mail", Category: catSend},
		Call: "mail__send []", Input: `{}`})
	c.turn.goOn(0, 0, 0, nil)
	c.turn.taint("mail.read") // the turn read an untrusted tool, and goes on over events

	// Two ticks while its device stops it: nothing is delivered.
	for range 2 {
		if err := tendBrain(ctx, c); err != nil {
			t.Fatal(err)
		}
		if items, _ := c.store.inbox("t-brain"); len(items) != 0 {
			t.Fatalf("delivered to the session that ends: %v", items)
		}
	}
	if c.store.get("brain_stopped:t-brain") == "" || !brainStatus(c).Refresh {
		t.Fatalf("not stopped for a fresh session: %+v", brainStatus(c))
	}
	if msgs, _ := c.store.undelivered(); len(msgs) != 1 || msgs[0].Seq != int(waiting) {
		t.Fatalf("the waiting message: %v", msgs)
	}

	// Stopped: the next task starts a fresh session, never s-1 resumed,
	// though the brain's status and its last task row still name it.
	c.store.updateTask("t-brain", map[string]any{"state": stateStopped})
	noWait(c, brainAssistant)
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	st := brainStatus(c)
	next, err := c.store.task(st.Task)
	if err != nil || next.ID == "t-brain" {
		t.Fatalf("no new task: %+v, %v", st, err)
	}
	spec, _ := c.store.taskSpec(next.ID)
	if spec.Resume != "" || st.Resume != "" || st.Refresh {
		t.Fatalf("resumed %q, status %+v", spec.Resume, st)
	}
	if !strings.HasPrefix(next.Prompt, "Tu démarres dans une session neuve") || !strings.Contains(next.Prompt, "Rappelle-moi le devis") {
		t.Fatalf("not fresh with the pack: %q", next.Prompt)
	}
	if _, _, _, tainted := c.turn.snapshot(); !slices.Contains(tainted, "mail.read") {
		t.Fatalf("the turn's taint was lost: %v", tainted)
	}
	if !c.proposalOf(cardAccess, ask).Pending {
		t.Fatal("the card was lost")
	}
	// The old task's call is still the brain's: its card names no task.
	if a, err := c.accessAskOf(ask); err != nil || c.accessOrigin(a, "fr").From != "" {
		t.Fatalf("the brain's card names a task: %+v, %v", c.accessOrigin(a, "fr"), err)
	}

	// Its first idle gets what waited, with a fresh budget.
	c.store.updateTask(next.ID, map[string]any{"state": stateIdle})
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox(next.ID)
	if len(items) == 0 {
		t.Fatal("nothing delivered to the fresh session")
	}
	if text, _ := items[len(items)-1].Data["text"].(string); !strings.Contains(text, "Et le budget ?") {
		t.Fatalf("the fresh session's turn: %q", text)
	}
	if c.turn.budget.scale != 1 || c.turn.budget.gestures != 0 {
		t.Fatalf("budget = %+v", c.turn.budget)
	}
}

// Under the bound, or without an exact measure, or with a session that
// starts near the bound, nothing changes: the turn is delivered to the
// running session.
func TestBrainSessionKeptUnderBound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		turns []map[string]any
	}{
		{"under the bound", []map[string]any{{"context_tokens": 45000}, {"context_tokens": 90000}}},
		{"a sum over calls only", []map[string]any{{"input_tokens": 5, "cache_read_tokens": 45000},
			{"input_tokens": 5, "cache_read_tokens": 300000, "cache_write_tokens": 2000}}},
		{"little history", []map[string]any{{"context_tokens": 80000}, {"context_tokens": 110000}}},
		{"no turn measured", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := refreshCore(t, tc.turns...)
			c.store.addUserMessage("Et le budget ?", nil, "")
			if turn := deliverNow(t, c); !strings.Contains(turn, "Et le budget ?") {
				t.Fatalf("not delivered: %q", turn)
			}
			if c.store.get("brain_stopped:t-brain") != "" || brainStatus(c).Refresh {
				t.Fatal("stopped under the bound")
			}
		})
	}
}

// A start turn not answered yet, or a delivery its device has not taken,
// is never cut.
func TestBrainSessionRefreshWaitsForInFlight(t *testing.T) {
	c := refreshCore(t, map[string]any{"context_tokens": 45000}, map[string]any{"context_tokens": 120000})
	setBrainStatus(c, func(st *AssistantStatus) { st.Quiet = true })
	if err := tendBrain(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if c.store.get("brain_stopped:t-brain") != "" {
		t.Fatal("stopped before its start turn was answered")
	}
	// Answered, but a delivery its device has not taken yet.
	setBrainStatus(c, func(st *AssistantStatus) { st.Quiet = false })
	if _, err := c.store.db.Exec(`INSERT INTO inbox (task_id, kind, data, created) VALUES ('t-brain', 'message', '{}', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := tendBrain(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if c.store.get("brain_stopped:t-brain") != "" {
		t.Fatal("stopped with a delivery in flight")
	}
}
