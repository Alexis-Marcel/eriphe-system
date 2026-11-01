package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFailureClass(t *testing.T) {
	for raw, want := range map[string]string{
		"dial tcp: connection refused":                   classTransient,
		"API Error: 529 overloaded":                      classTransient,
		"API Error: 429 rate limit exceeded":             classTransient,
		"Your credit balance is too low":                 classAccount,
		"Failed to authenticate. API Error: 401":         classAccount,
		"model claude-x not found":                       classAccount,
		"error_max_turns":                                classInvalid,
		"API Error: 400 invalid_request_error: too long": classInvalid,
	} {
		if got := failureClass(raw); got != want {
			t.Errorf("%q: %s, want %s", raw, got, want)
		}
	}
	if failureSig(classTransient, "timeout after 30s (req 123)") != failureSig(classTransient, "timeout after 31s (req 456)") {
		t.Error("the numbers make two failures differ")
	}
	if retryDelay(0) != 30*time.Second || retryDelay(1) != time.Minute || retryDelay(9) != 5*time.Minute {
		t.Error("retry delays")
	}
}

func idleTask(t *testing.T, c *core, id string, events ...EventRow) {
	t.Helper()
	if _, err := c.store.task(id); err != nil {
		c.store.insertTask(&TaskRow{ID: id, Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	}
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	c.store.addEvents(id, events)
	c.notifyBrain(id, stateIdle)
}

func failedTurn(seq int, raw string) EventRow {
	return ev(seq, "turn_end", map[string]any{"subtype": "success", "is_error": true, "result": raw})
}

// A passing failure is retried after a wait, without the brain; the same
// failure again stops the task, and the brain hears why.
func TestTransientRetryThenSameFailure(t *testing.T) {
	c := receiptCore(t)
	idleTask(t, c, "t-1", failedTurn(1, "API Error: 529 overloaded (req 1)"))
	if msg := lastEvent(t, c); msg != "" {
		t.Fatalf("the brain heard %q", msg)
	}
	h := c.store.harness("t-1")
	if h.RetryAt == 0 || len(h.Failures) != 1 || h.Failures[0] != classTransient {
		t.Fatalf("harness %+v", h)
	}
	c.harnessTick(time.Unix(h.RetryAt-1, 0)) // not due yet
	if items, _ := c.store.inbox("t-1"); len(items) != 0 {
		t.Fatal("retried early")
	}
	c.harnessTick(time.Unix(h.RetryAt, 0))
	items, _ := c.store.inbox("t-1")
	if len(items) != 1 || !strings.Contains(items[0].Data["text"].(string), "passing error (API Error: 529 overloaded") {
		t.Fatalf("inbox %+v", items)
	}
	if task, _ := c.store.task("t-1"); task.State != stateRunning {
		t.Errorf("state %s", task.State)
	}
	if h := c.store.harness("t-1"); h.Retries != 1 || h.RetryAt != 0 {
		t.Errorf("harness %+v", h)
	}
	c.store.ackInbox("t-1", items[0].Seq)
	idleTask(t, c, "t-1", failedTurn(2, "API Error: 529 overloaded (req 2)"))
	task, _ := c.store.task("t-1")
	if h := c.store.harness("t-1"); h.StopReason != stopSameFailure {
		t.Fatalf("not stopped: %+v", h)
	}
	if desired := desiredOf(t, c, "t-1"); desired != desiredStopped {
		t.Errorf("desired %s", desired)
	}
	c.notifyBrain(task.ID, stateStopped)
	if msg := lastEvent(t, c); !strings.Contains(msg, "est maintenant stopped. Arrêtée par eriphe-core : elle a échoué deux fois de la même façon") {
		t.Errorf("brain event %q", msg)
	}
}

func desiredOf(t *testing.T, c *core, id string) string {
	t.Helper()
	var d string
	c.store.db.QueryRow(`SELECT desired FROM tasks WHERE id = ?`, id).Scan(&d)
	return d
}

// An account's failure is never retried: the brain hears it at once, and why.
func TestAccountFailureNotRetried(t *testing.T) {
	c := receiptCore(t)
	idleTask(t, c, "t-1", failedTurn(1, "Failed to authenticate. API Error: 401"))
	if h := c.store.harness("t-1"); h.RetryAt != 0 {
		t.Error("retried")
	}
	if msg := lastEvent(t, c); !strings.Contains(msg, "Échec du compte de l'agent") {
		t.Errorf("brain event %q", msg)
	}
}

// Passing failures are retried within the attempts, then told.
func TestTransientAttemptsSpent(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.setContract("t-1", &taskContract{Objective: "x", Budget: &taskBudget{Attempts: 1}})
	c.store.setHarness("t-1", map[string]any{"retries": 1})
	idleTask(t, c, "t-1", failedTurn(1, "connection reset by peer"))
	if msg := lastEvent(t, c); !strings.Contains(msg, "eriphe-core l'a déjà relancée 1 fois et ne la relance plus") {
		t.Errorf("brain event %q", msg)
	}
}

// A task past its time budget is stopped by the tick; past its turns, at
// the end of its turn, and no new turn is given.
func TestBudgets(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-time", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateRunning})
	c.store.setContract("t-time", &taskContract{Objective: "x", Budget: &taskBudget{Minutes: 10}})
	c.harnessTick(time.Now())
	if h := c.store.harness("t-time"); h.StopReason != "" {
		t.Fatal("stopped within its budget")
	}
	c.harnessTick(time.Now().Add(11 * time.Minute))
	if h := c.store.harness("t-time"); h.StopReason != stopMinutes || desiredOf(t, c, "t-time") != desiredStopped {
		t.Errorf("harness %+v", h)
	}
	task, _ := c.store.task("t-time")
	c.notifyBrain(task.ID, stateStopped)
	if msg := lastEvent(t, c); !strings.Contains(msg, "son budget de temps est épuisé") {
		t.Errorf("brain event %q", msg)
	}

	c.store.insertTask(&TaskRow{ID: "t-turns", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.setContract("t-turns", &taskContract{Objective: "x", Budget: &taskBudget{Turns: 1}})
	partial := "```json\n{\"receipt\": {\"status\": \"partial\"}}\n```"
	if _, err := c.answer(context.Background(), "t-turns", "", "go on", nil, byBrain); err != nil {
		t.Fatalf("refused before its budget: %v", err)
	}
	idleTask(t, c, "t-turns", ev(1, "text", map[string]any{"text": partial}), ev(2, "turn_end", map[string]any{"subtype": "success"}))
	if h := c.store.harness("t-turns"); h.StopReason != stopTurns {
		t.Errorf("harness %+v", h)
	}
	if _, err := c.answer(context.Background(), "t-turns", "", "go on", nil, byBrain); err == nil || !strings.Contains(err.Error(), "budget of 1 turns") {
		t.Errorf("answered past its budget: %v", err)
	}

	c.store.insertTask(&TaskRow{ID: "t-tokens", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.setContract("t-tokens", &taskContract{Objective: "x", Budget: &taskBudget{Tokens: 1000}})
	idleTask(t, c, "t-tokens", ev(1, "text", map[string]any{"text": partial}), ev(2, "turn_end", map[string]any{"subtype": "success",
		"usage": map[string]any{"input_tokens": 400, "output_tokens": 500, "cache_write_tokens": 200, "cache_read_tokens": 90000}}))
	if n := c.store.tokensUsed("t-tokens"); n != 1100 {
		t.Errorf("tokens %d", n)
	}
	if h := c.store.harness("t-tokens"); h.StopReason != stopTokens {
		t.Errorf("harness %+v", h)
	}
}

// A done task is not stopped for its budget: the brain closes it.
func TestDoneNotStoppedForBudget(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.setContract("t-1", &taskContract{Objective: "x", Budget: &taskBudget{Turns: 1}})
	idleTask(t, c, "t-1", ev(1, "text", map[string]any{"text": doneReceipt}), ev(2, "turn_end", map[string]any{"subtype": "success"}))
	if h := c.store.harness("t-1"); h.StopReason != "" {
		t.Errorf("stopped: %+v", h)
	}
	if msg := lastEvent(t, c); !strings.Contains(msg, "statut done") {
		t.Errorf("brain event %q", msg)
	}
}

// A failed task's end comes with its class: whether starting it again is worth it.
func TestFailedTaskClassed(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateFailed})
	c.store.addEvents("t-1", []EventRow{ev(1, "error", map[string]any{"message": "dial tcp 192.0.2.1:443: i/o timeout"})})
	c.notifyBrain("t-1", stateFailed)
	if msg := lastEvent(t, c); !strings.Contains(msg, "est maintenant failed. (Échec passager : la relancer une fois peut suffire.)") {
		t.Errorf("brain event %q", msg)
	}
	if h := c.store.harness("t-1"); len(h.Failures) != 1 || h.Failures[0] != classTransient {
		t.Errorf("harness %+v", h)
	}
}

// The drawer says why eriphe-core stopped a task, or when it retries it.
func TestStopReasonOnDrawer(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateStopped})
	tw.c.store.setHarness("t-1", map[string]any{"stop_reason": stopMinutes})
	tw.c.store.insertTask(&TaskRow{ID: "t-2", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	tw.c.store.setHarness("t-2", map[string]any{"retry_at": time.Now().Add(time.Minute).Unix()})
	if _, body, _ := tw.do("GET", "/tasks/t-1", nil, map[string]string{"HX-Request": "true"}); !strings.Contains(body, "Arrêtée par Eriphe : son budget de temps est épuisé.") {
		t.Error("no stop reason")
	}
	if _, body, _ := tw.do("GET", "/tasks/t-2", nil, map[string]string{"HX-Request": "true"}); !strings.Contains(body, "Échec passager : Eriphe la relance à") {
		t.Error("no retry time")
	}
}

// A check the node stopped sending back (the same failure twice) stops
// the task.
func TestCheckSameFailureStops(t *testing.T) {
	c := receiptCore(t)
	idleTask(t, c, "t-1", ev(1, "text", map[string]any{"text": doneReceipt}), ev(2, "turn_end", map[string]any{"subtype": "success"}),
		ev(3, "check", map[string]any{"name": "tests", "ok": false, "exit_code": 1, "output": "FAIL"}),
		ev(4, "check_stop", map[string]any{"check": "tests", "reason": "same_failure"}))
	if h := c.store.harness("t-1"); h.StopReason != stopSameFailure || h.Failures[len(h.Failures)-1] != classCheck {
		t.Errorf("harness %+v", h)
	}
}
