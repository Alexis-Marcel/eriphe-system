package main

import (
	"context"
	"testing"
	"time"
)

// An older node reports a task running again once the first of two requests
// made at once is answered: the second stays pending, and the brain can
// answer it; a turn's end leaves none waiting.
func TestPendingWhileRunning(t *testing.T) {
	c, _ := newTestAPI(t)
	addTask(t, c, "t-1", stateRunning)
	now := time.Now()
	c.store.addEvents("t-1", []EventRow{
		{Seq: 1, Time: now, Type: "permission", Data: map[string]any{"id": "p1", "tool_name": "mcp__mail__search_messages"}},
		{Seq: 2, Time: now, Type: "permission", Data: map[string]any{"id": "p2", "tool_name": "mcp__mail__search_messages"}},
		{Seq: 3, Time: now, Type: "permission_answer", Data: map[string]any{"id": "p1", "allow": true}},
		{Seq: 4, Time: now, Type: "state", Data: map[string]any{"state": "running"}},
	})
	st, err := c.status("t-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.PendingPermissions) != 1 || st.PendingPermissions[0]["id"] != "p2" {
		t.Fatalf("pending %v", st.PendingPermissions)
	}
	yes := true
	if _, err := c.answer(context.Background(), "t-1", "p2", "", &yes, byBrain); err != nil {
		t.Fatalf("answer the second: %v", err)
	}

	addTask(t, c, "t-2", stateRunning)
	c.store.addEvents("t-2", []EventRow{
		{Seq: 1, Time: now, Type: "permission", Data: map[string]any{"id": "p3", "tool_name": "Bash"}},
		{Seq: 2, Time: now, Type: "turn_end", Data: map[string]any{}},
	})
	if st, _ := c.status("t-2", 0); len(st.PendingPermissions) != 0 {
		t.Errorf("pending after the turn's end: %v", st.PendingPermissions)
	}
}
