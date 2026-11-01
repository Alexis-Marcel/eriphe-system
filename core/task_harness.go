package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// What eriphe-core keeps of a worker task besides its row and its events:
// its last receipt, as checked (receipt.go), and how many times it asked
// the agent to write it again.

func init() {
	extraSchema = append(extraSchema, `CREATE TABLE IF NOT EXISTS task_harness (
  task_id TEXT PRIMARY KEY, receipt TEXT NOT NULL DEFAULT '', repairs INTEGER NOT NULL DEFAULT 0,
  repair_turn INTEGER NOT NULL DEFAULT 0, updated INTEGER NOT NULL DEFAULT 0)`)
}

// The columns of the retries and budgets (task_retry.go).
func init() {
	extraColumns = append(extraColumns,
		`ALTER TABLE task_harness ADD COLUMN retries INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE task_harness ADD COLUMN retry_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE task_harness ADD COLUMN last_failure TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_harness ADD COLUMN failures TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_harness ADD COLUMN stop_reason TEXT NOT NULL DEFAULT ''`)
}

// harnessRow is a task's row of task_harness.
type harnessRow struct {
	Receipt     *checkedReceipt
	Repairs     int      // receipts asked again
	RepairTurn  int      // the turn count when the last one was asked
	Retries     int      // passing failures retried by eriphe-core
	RetryAt     int64    // when the next retry is due, 0 for none
	LastFailure string   // the last failure's signature
	Failures    []string // the class of each failure, in order
	StopReason  string   // why eriphe-core stopped it, if it did
}

func (s *Store) harness(id string) harnessRow {
	var h harnessRow
	var raw, failures string
	s.db.QueryRow(`SELECT receipt, repairs, repair_turn, retries, retry_at, last_failure, failures, stop_reason
		FROM task_harness WHERE task_id = ?`, id).Scan(&raw, &h.Repairs, &h.RepairTurn, &h.Retries, &h.RetryAt,
		&h.LastFailure, &failures, &h.StopReason)
	if raw != "" {
		json.Unmarshal([]byte(raw), &h.Receipt)
	}
	if failures != "" {
		json.Unmarshal([]byte(failures), &h.Failures)
	}
	return h
}

// setHarness writes some of a task's harness fields (column → value).
func (s *Store) setHarness(id string, fields map[string]any) error {
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO task_harness (task_id) VALUES (?)`, id); err != nil {
		return err
	}
	for k, v := range fields {
		if r, ok := v.(*checkedReceipt); ok {
			raw, _ := json.Marshal(r)
			v = string(raw)
		}
		if _, err := s.db.Exec(fmt.Sprintf(`UPDATE task_harness SET %s = ?, updated = ? WHERE task_id = ?`, k), v, time.Now().Unix(), id); err != nil {
			return err
		}
	}
	return nil
}

// maxReceiptRepairs is how many times a task is asked to write its receipt
// again, never twice in a row.
const maxReceiptRepairs = 2

// receiptRepair is the message that asks for it.
const receiptRepair = "Your last message had no readable receipt. Reply with only the receipt of the work so far: " +
	"the fenced ```json block {\"receipt\": {...}} described in your instructions. Do not do any other work."

// turnsOf counts the turns that ended.
func turnsOf(events []EventRow) int {
	n := 0
	for _, e := range events {
		if e.Type == "turn_end" {
			n++
		}
	}
	return n
}

// turnOutcome is what eriphe-core made of a worker's turn that ended.
type turnOutcome struct {
	receipt *checkedReceipt
	hold    bool   // the task goes on (a retry, its receipt asked) or stops: nothing to tell yet
	note    string // for the brain: what eriphe-core did or will not do, in French
}

// settleTurn reads the receipt of a worker's turn that just ended and keeps
// it, then decides in code what follows: past its budget the task stops; a
// failed turn is retried, stopped or told by its class (task_retry.go);
// without a receipt, the agent is asked for it once, without the brain. A
// turn that failed is not asked for its receipt.
func (c *core) settleTurn(t *TaskRow) turnOutcome {
	events, err := c.store.events(t.ID, 400)
	if err != nil {
		return turnOutcome{}
	}
	cr := crossCheck(events)
	if cr == nil {
		return turnOutcome{}
	}
	c.store.setHarness(t.ID, map[string]any{"receipt": cr})
	if out, decided := c.decideTurn(t, events, cr); decided {
		return out
	}
	if !cr.missing() {
		return turnOutcome{receipt: cr}
	}
	h := c.store.harness(t.ID)
	turns := turnsOf(events)
	if h.Repairs >= maxReceiptRepairs || h.RepairTurn != 0 && turns == h.RepairTurn+1 {
		return turnOutcome{receipt: cr} // asked already, or the answer to the last ask
	}
	// skip_checks: the node runs no check after this turn, which changes nothing.
	if err := c.store.addInbox(t.ID, "message", map[string]any{"text": receiptRepair, "skip_checks": true}); err != nil {
		return turnOutcome{receipt: cr}
	}
	c.store.setHarness(t.ID, map[string]any{"repairs": h.Repairs + 1, "repair_turn": turns})
	c.store.updateTask(t.ID, map[string]any{"state": stateRunning})
	c.bump()
	return turnOutcome{receipt: cr, hold: true}
}

// turnFailed tells whether the last turn ended on an error.
func turnFailed(events []EventRow) bool {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "turn_end" {
			_, bad := turnFailure(events[i])
			return bad
		}
	}
	return false
}
