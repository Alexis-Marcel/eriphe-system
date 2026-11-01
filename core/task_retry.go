package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// What follows a worker's turn that failed is decided in code, by the
// failure's class (the causes of problems.go), not left to the brain's
// judgement: a passing failure is retried after a growing wait, an
// account's or an invalid request's is not, and the same failure twice
// stops the task. A task's budget (contract.go: minutes, turns, tokens) is
// kept here too: past it, eriphe-core stops the task and tells the brain
// why, with its last receipt.

// The classes of a failure.
const (
	classTransient  = "transient"  // the network, the provider's servers, a rate limit: retried
	classAccount    = "account"    // the key, the quota, the model: not retried, the user is told
	classInvalid    = "invalid"    // a request the agent or its provider refused: not retried blindly
	classPermission = "permission" // the policy refused an action: never retried, the brain is told
	classCheck      = "check"      // a check of the contract failed
	classBudget     = "budget"     // the task ran out of its budget
)

// Why eriphe-core stopped a task (stop_reason).
const (
	stopSameFailure = "same_failure"
	stopMinutes     = "budget_minutes"
	stopTurns       = "budget_turns"
	stopTokens      = "budget_tokens"
)

var rateLimitRe = regexp.MustCompile(`(?i)\b429\b|rate[ _-]?limit|too many requests|overloaded|try again`)

// failureClass reads a failed turn's raw error as a class.
func failureClass(raw string) string {
	switch classify(raw) {
	case causeNetwork, causeServer:
		return classTransient
	case causeQuota:
		if rateLimitRe.MatchString(raw) {
			return classTransient
		}
		return classAccount
	case causeAuth, causeModel:
		return classAccount
	}
	return classInvalid
}

var digitsRe = regexp.MustCompile(`[0-9]+`)

// failureSig is a failure as compared with the one before: its class and
// its words, without the numbers that change each time (ids, times).
func failureSig(class, raw string) string {
	return class + ":" + truncate(strings.Join(strings.Fields(digitsRe.ReplaceAllString(strings.ToLower(raw), "#")), " "), 200)
}

// retryDelay is the wait before the n-th retry (from 0): 30 s, doubled
// each time, at most 5 minutes.
func retryDelay(n int) time.Duration {
	return min(30*time.Second<<min(n, 8), 5*time.Minute)
}

const retryMessage = "Your last turn failed on a passing error (%s). Eriphe retries it: carry on where you were, then end your turn with your receipt."

// decideTurn decides what follows a turn that ended, before anyone is
// told: true when it decided (hold: nothing to tell yet).
func (c *core) decideTurn(t *TaskRow, events []EventRow, cr *checkedReceipt) (turnOutcome, bool) {
	k := c.store.contract(t.ID)
	h := c.store.harness(t.ID)
	if reason := c.overBudget(t, k, time.Now()); reason != "" && cr.Status != "done" {
		c.autoStop(t, reason)
		return turnOutcome{receipt: cr, hold: true}, true
	}
	end, ok := lastTurnEnd(events)
	if !ok {
		return turnOutcome{}, false
	}
	if raw, bad := turnFailure(end); bad {
		class := failureClass(raw)
		sig := failureSig(class, raw)
		c.noteFailure(t.ID, h, class, sig)
		if sig == h.LastFailure {
			c.autoStop(t, stopSameFailure)
			return turnOutcome{receipt: cr, hold: true}, true
		}
		if class == classTransient && h.Retries < attemptsOf(k) {
			at := time.Now().Add(retryDelay(h.Retries))
			c.store.setHarness(t.ID, map[string]any{"retry_at": at.Unix()})
			log.Printf("task %s: passing failure, retried at %s: %s", t.ID, at.Format(time.TimeOnly), truncate(raw, 200))
			return turnOutcome{receipt: cr, hold: true}, true
		}
		return turnOutcome{receipt: cr, note: c.failureNote(class, raw, h.Retries)}, true
	}
	if denials, _ := end.Data["denials"].([]any); len(denials) > 0 {
		c.noteFailure(t.ID, h, classPermission, "") // told to the brain as it is
		h = c.store.harness(t.ID)
	}
	if failed := cr.failedChecks(); len(failed) > 0 && failed[0].Seq > end.Seq {
		// A check of this turn failed: the same way as the last failure, or
		// twice in a row on the node, it stops the task.
		ch := failed[0]
		sig := failureSig(classCheck, fmt.Sprintf("%s %d %s", ch.Name, ch.ExitCode, ch.Output))
		c.noteFailure(t.ID, h, classCheck, sig)
		if sig == h.LastFailure || checkStoppedSame(events, end.Seq) {
			c.autoStop(t, stopSameFailure)
			return turnOutcome{receipt: cr, hold: true}, true
		}
	} else if h.LastFailure != "" {
		c.store.setHarness(t.ID, map[string]any{"last_failure": ""}) // a turn went through
	}
	return turnOutcome{}, false
}

// lastTurnEnd is the last turn_end event.
func lastTurnEnd(events []EventRow) (EventRow, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "turn_end" {
			return events[i], true
		}
	}
	return EventRow{}, false
}

// checkStoppedSame tells whether the node stopped sending a check back,
// after the turn that ended at seq, because it failed the same way twice.
func checkStoppedSame(events []EventRow, seq int) bool {
	for _, e := range events {
		if e.Seq > seq && e.Type == "check_stop" && e.Data["reason"] == "same_failure" {
			return true
		}
	}
	return false
}

// attemptsOf is how many passing failures are retried.
func attemptsOf(k *taskContract) int {
	if k == nil {
		return defaultAttempts
	}
	return k.attempts()
}

// noteFailure keeps a failure's class, and its signature to compare with
// the next one (a permission refused has none: the brain decides).
func (c *core) noteFailure(id string, h harnessRow, class, sig string) {
	failures, _ := json.Marshal(append(h.Failures, class))
	fields := map[string]any{"failures": string(failures)}
	if sig != "" {
		fields["last_failure"] = sig
	}
	c.store.setHarness(id, fields)
}

// overBudget says which budget a task has spent, if any.
func (c *core) overBudget(t *TaskRow, k *taskContract, now time.Time) string {
	if k == nil || k.Budget == nil {
		return ""
	}
	b := k.Budget
	switch {
	case b.Minutes > 0 && now.Sub(t.Created) > time.Duration(b.Minutes)*time.Minute:
		return stopMinutes
	case b.Turns > 0 && c.store.turnCount(t.ID) >= b.Turns:
		return stopTurns
	case b.Tokens > 0 && c.store.tokensUsed(t.ID) >= int64(b.Tokens):
		return stopTokens
	}
	return ""
}

// autoStop stops a task for a reason of eriphe-core's, which the brain hears
// when its node says it stopped (endedNote).
func (c *core) autoStop(t *TaskRow, reason string) {
	log.Printf("task %s stopped by eriphe-core: %s", t.ID, reason)
	h := c.store.harness(t.ID)
	if reason != stopSameFailure {
		c.noteFailure(t.ID, h, classBudget, "")
	}
	c.store.setHarness(t.ID, map[string]any{"stop_reason": reason, "retry_at": 0})
	if err := c.stop(context.Background(), t.ID); err != nil {
		log.Printf("task %s: %v", t.ID, err)
	}
}

// failureNote tells the brain what eriphe-core does not do about a failed
// turn, and why.
func (c *core) failureNote(class, raw string, retries int) string {
	user := c.conf().User
	switch class {
	case classTransient:
		return fmt.Sprintf("(Échec passager : eriphe-core l'a déjà relancée %d fois et ne la relance plus. Préviens %s si elle est utile.)", retries, user)
	case classAccount:
		return fmt.Sprintf("(Échec du compte de l'agent : clé, quota ou modèle. eriphe-core ne la relance pas : préviens %s.)", user)
	}
	return "(Demande refusée ou invalide : eriphe-core ne la relance pas à l'aveugle. Change la consigne, ou préviens " + user + ".)"
}

// endedNote is what the brain hears of a task that ended, besides its
// state: why eriphe-core stopped it, with its last receipt.
func (c *core) endedNote(t *TaskRow) string {
	h := c.store.harness(t.ID)
	var note string
	switch h.StopReason {
	case "":
		if t.State == stateFailed && t.Kind == "worker" {
			return c.failedNote(t, h)
		}
		return ""
	case stopSameFailure:
		note = " Arrêtée par eriphe-core : elle a échoué deux fois de la même façon. Ne la relance pas telle quelle."
	case stopMinutes, stopTurns, stopTokens:
		what := map[string]string{stopMinutes: "de temps", stopTurns: "de tours", stopTokens: "de jetons"}[h.StopReason]
		note = fmt.Sprintf(" Arrêtée par eriphe-core : son budget %s est épuisé.", what)
	}
	if h.Receipt.Given() {
		note += "\n" + h.Receipt.brainText()
	}
	return note
}

// failedNote classes the failure of a task whose agent or container
// failed, and tells the brain whether starting it again is worth it.
func (c *core) failedNote(t *TaskRow, h harnessRow) string {
	events, err := c.store.events(t.ID, 50)
	if err != nil {
		return ""
	}
	raw := ""
	for _, e := range events {
		if r, bad := turnFailure(e); bad {
			raw = r
		}
	}
	if raw == "" {
		raw = t.PendingReason // it could not be placed or lent its resources
	}
	if raw == "" {
		return ""
	}
	class := failureClass(raw)
	c.noteFailure(t.ID, h, class, "")
	switch class {
	case classTransient:
		return " (Échec passager : la relancer une fois peut suffire.)"
	case classAccount:
		return fmt.Sprintf(" (Échec du compte de l'agent : clé, quota ou modèle. Ne la relance pas : préviens %s.)", c.conf().User)
	}
	return " (Échec de la tâche : ne la relance pas telle quelle.)"
}

// turnsLeft refuses a new turn to a task that spent its budget of turns.
func (c *core) turnsLeft(id string) error {
	k := c.store.contract(id)
	if k == nil || k.Budget == nil || k.Budget.Turns == 0 {
		return nil
	}
	if n := c.store.turnCount(id); n >= k.Budget.Turns {
		return fmt.Errorf("the task spent its budget of %d turns: stop it, or start another", k.Budget.Turns)
	}
	return nil
}

// harnessTick sends the retries that are due and stops the tasks past
// their time budget. It runs every 15 seconds.
func (c *core) harnessTick(now time.Time) {
	rows, err := c.store.db.Query(`SELECT h.task_id FROM task_harness h JOIN tasks t ON t.id = h.task_id
		WHERE h.retry_at > 0 AND h.retry_at <= ?`, now.Unix())
	if err == nil {
		var due []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				due = append(due, id)
			}
		}
		rows.Close()
		for _, id := range due {
			c.retryNow(id)
		}
	}
	rows, err = c.store.db.Query(`SELECT h.task_id FROM task_harness h JOIN tasks t ON t.id = h.task_id
		WHERE h.contract != '' AND h.stop_reason = '' AND t.state NOT IN ('stopped', 'failed', 'lost')`)
	if err != nil {
		return
	}
	var active []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			active = append(active, id)
		}
	}
	rows.Close()
	for _, id := range active {
		t, err := c.store.task(id)
		if err != nil {
			continue
		}
		if reason := c.overBudget(t, c.store.contract(id), now); reason == stopMinutes {
			c.autoStop(t, reason)
		}
	}
}

// retryNow gives a task waiting for its retry its next turn.
func (c *core) retryNow(id string) {
	h := c.store.harness(id)
	c.store.setHarness(id, map[string]any{"retry_at": 0})
	t, err := c.store.task(id)
	if err != nil || t.State != stateIdle {
		return // it moved meanwhile (stopped, answered)
	}
	raw := ""
	if events, err := c.store.events(id, 50); err == nil {
		if end, ok := lastTurnEnd(events); ok {
			raw, _ = turnFailure(end)
		}
	}
	if err := c.store.addInbox(id, "message", map[string]any{"text": fmt.Sprintf(retryMessage, truncate(raw, 300))}); err != nil {
		return
	}
	c.store.setHarness(id, map[string]any{"retries": h.Retries + 1})
	c.store.updateTask(id, map[string]any{"state": stateRunning})
	c.bump()
}

// turnCount is how many turns of a task ended.
func (s *Store) turnCount(id string) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'turn_end'`, id).Scan(&n)
	return n
}

// tokensUsed is the tokens a task's turns used, as their harness reported
// them (turn_end's usage): input, output and cache writes; cache reads,
// cheap and repeated each turn, are left out.
func (s *Store) tokensUsed(id string) int64 {
	var n int64
	s.db.QueryRow(`SELECT COALESCE(SUM(COALESCE(json_extract(data, '$.usage.input_tokens'), 0)
		+ COALESCE(json_extract(data, '$.usage.output_tokens'), 0)
		+ COALESCE(json_extract(data, '$.usage.cache_write_tokens'), 0)), 0)
		FROM events WHERE task_id = ? AND type = 'turn_end'`, id).Scan(&n)
	return n
}
