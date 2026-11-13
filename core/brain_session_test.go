package main

import (
	"strings"
	"testing"
	"time"
)

// sessionTask records a brain task that ran session s.
func sessionTask(t *testing.T, c *core, id, s, state string) {
	t.Helper()
	if err := c.store.insertTask(&TaskRow{ID: id, Node: "n", Runtime: "claude-code", Kind: assistantRoleBrain, State: state}); err != nil {
		t.Fatal(err)
	}
	if err := c.store.updateTask(id, map[string]any{"session_id": s}); err != nil {
		t.Fatal(err)
	}
}

// sessionTurn records a task of session s whose turn ended ago, with usage.
func sessionTurn(t *testing.T, c *core, id, s string, ago time.Duration, usage map[string]any) {
	t.Helper()
	sessionTask(t, c, id, s, stateIdle)
	if err := c.store.addEvents(id, []EventRow{{Seq: 1, Time: time.Now().Add(-ago), Type: "turn_end",
		Data: map[string]any{"subtype": "success", "usage": usage}}}); err != nil {
		t.Fatal(err)
	}
}

// The brain resumes its session while it is small and its cache warm;
// past brainResumeTokens per call, or brainResumeIdle above
// brainResumeIdleFloor, it starts fresh with the pack. The measure is the session's, whichever task ran its last turn.
func TestBrainSessionBounds(t *testing.T) {
	small := map[string]any{"input_tokens": 3, "cache_read_tokens": 60000, "cache_write_tokens": 2000, "context_tokens": 40000}
	mid := map[string]any{"context_tokens": 90000}
	for _, tc := range []struct {
		name   string
		turns  func(t *testing.T, c *core)
		resume bool
	}{
		{"small and recent", func(t *testing.T, c *core) { sessionTurn(t, c, "b-1", "s-1", 10*time.Minute, small) }, true},
		{"no turn measured", func(t *testing.T, c *core) {}, true},
		{"too large", func(t *testing.T, c *core) {
			sessionTurn(t, c, "b-1", "s-1", time.Minute, map[string]any{"cache_read_tokens": 400000, "context_tokens": 200000})
		}, false},
		{"its cache gone", func(t *testing.T, c *core) { sessionTurn(t, c, "b-1", "s-1", 2*time.Hour, mid) }, false},
		{"small after a pause", func(t *testing.T, c *core) { sessionTurn(t, c, "b-1", "s-1", 2*time.Hour, small) }, true},
		{"the turn's sum without context_tokens", func(t *testing.T, c *core) {
			sessionTurn(t, c, "b-1", "s-1", time.Minute, map[string]any{"input_tokens": 3, "cache_read_tokens": 150000, "cache_write_tokens": 1000})
		}, false},
		{"its last task failed before a turn", func(t *testing.T, c *core) {
			sessionTurn(t, c, "b-1", "s-1", time.Minute, map[string]any{"context_tokens": 200000})
			sessionTask(t, c, "b-2", "s-1", stateFailed)
		}, false},
		{"another session's turn", func(t *testing.T, c *core) {
			sessionTurn(t, c, "b-1", "s-0", time.Minute, map[string]any{"context_tokens": 200000})
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testMemoryCore(t)
			say(t, c, roleUser, "Rappelle-moi le devis", true)
			setBrainStatus(c, func(st *AssistantStatus) { st.Session, st.SessionRuntime = "s-1", "claude-code" })
			tc.turns(t, c)
			task := startedBrain(t, c, true)
			if tc.resume {
				if task.Prompt != brainStartPrompt || brainStatus(c).Resume != "s-1" {
					t.Fatalf("not resumed: %q", task.Prompt)
				}
				return
			}
			if brainStatus(c).Resume != "" || !strings.HasPrefix(task.Prompt, "Tu démarres dans une session neuve") ||
				!strings.Contains(task.Prompt, "Rappelle-moi le devis") {
				t.Fatalf("not fresh with the pack: %q", task.Prompt)
			}
		})
	}
}

// A role without bounds resumes whatever its session's size or age.
func TestSessionWornUnbounded(t *testing.T) {
	c, _ := testMemoryCore(t)
	sessionTurn(t, c, "b-1", "s-1", 3*time.Hour, map[string]any{"context_tokens": 900000})
	if why := c.sessionWorn(assistantRole{}, "s-1"); why != "" {
		t.Fatalf("unbounded role: %q", why)
	}
	if why := c.sessionWorn(assistantRoles[assistantRoleBrain], "s-1"); why == "" {
		t.Fatal("the brain resumes a worn session")
	}
}
