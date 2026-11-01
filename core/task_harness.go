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

// harnessRow is a task's row of task_harness.
type harnessRow struct {
	Receipt    *checkedReceipt
	Repairs    int // receipts asked again
	RepairTurn int // the turn count when the last one was asked
}

func (s *Store) harness(id string) harnessRow {
	var h harnessRow
	var raw string
	s.db.QueryRow(`SELECT receipt, repairs, repair_turn FROM task_harness WHERE task_id = ?`, id).Scan(&raw, &h.Repairs, &h.RepairTurn)
	if raw != "" {
		json.Unmarshal([]byte(raw), &h.Receipt)
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

// settleTurn reads the receipt of a worker's turn that just ended and keeps
// it. When the agent gave none, it asks for it once, without the brain
// (true: the task goes on, nothing to tell yet). A turn that failed is not
// asked again.
func (c *core) settleTurn(t *TaskRow) (*checkedReceipt, bool) {
	events, err := c.store.events(t.ID, 400)
	if err != nil {
		return nil, false
	}
	cr := crossCheck(events)
	if cr == nil {
		return nil, false
	}
	c.store.setHarness(t.ID, map[string]any{"receipt": cr})
	if !cr.missing() || turnFailed(events) {
		return cr, false
	}
	h := c.store.harness(t.ID)
	turns := turnsOf(events)
	if h.Repairs >= maxReceiptRepairs || h.RepairTurn != 0 && turns == h.RepairTurn+1 {
		return cr, false // asked already, or the answer to the last ask
	}
	// skip_checks: the node runs no check after this turn, which changes nothing.
	if err := c.store.addInbox(t.ID, "message", map[string]any{"text": receiptRepair, "skip_checks": true}); err != nil {
		return cr, false
	}
	c.store.setHarness(t.ID, map[string]any{"repairs": h.Repairs + 1, "repair_turn": turns})
	c.store.updateTask(t.ID, map[string]any{"state": stateRunning})
	c.bump()
	return cr, true
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
