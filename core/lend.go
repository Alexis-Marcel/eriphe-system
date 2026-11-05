package main

import (
	"fmt"
	"strconv"
)

// What a task is lent is what it may reach, not what it may do: the grid
// decides each call (access_grid.go). The lend only says how a task
// started, which a Free write depends on (decideTask).
//
// A tool may also be lent held back (a resource with hold): for work the
// user validates before it acts (analyse, report, then act on what they
// approved). The task sees it from the start, since its agent lists its
// tools once, but until the user approves its release on a card
// (access_card.go) a Free does not hold for it.

// heldTool tells whether a task's resources hold a tool back: lent held,
// and covered by no entry lent outright.
func heldTool(list []ResourceRef, server, tool string) bool {
	held := false
	for _, e := range list {
		if e.Kind != resTool || !grantAllows([]string{e.Ref}, server, tool) {
			continue
		}
		if !e.Hold {
			return false
		}
		held = true
	}
	return held
}

// lentByUserKey marks a clean start: a task started in a clean turn of one
// of my messages, or as a watch I authorised in advance. It keeps its Free
// writes whatever it reads since. Any other task, started on a tainted turn
// or by the brain alone (woken by a watch, an event), has its Free writes
// On approval.
func lentByUserKey(id string) string { return "lent_by_user:" + id }

// turnFromUser tells whether the brain's turn going on was started by a
// message of mine, not an automation's.
func (c *core) turnFromUser() bool {
	from, to := c.turn.span()
	if from == 0 {
		return false
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? AND seq BETWEEN ? AND ? AND via != ?`,
		roleUser, from, to, automationVia).Scan(&n)
	return n > 0
}

// brainLeftPermissions hands the user what the brain did not answer: a
// task's request it was told of before the turn that just ended, still
// pending. It ended its turn on it: it said it to the user, or could not
// decide. Either way the decision is the user's now.
func (c *core) brainLeftPermissions() {
	tasks, err := c.store.tasks(true, 50)
	if err != nil {
		return
	}
	for _, t := range tasks {
		if t.Kind != "worker" {
			continue
		}
		st, err := c.status(t.ID, 0)
		if err != nil {
			continue
		}
		for _, p := range st.PendingPermissions {
			pid := fmt.Sprint(p["id"])
			if c.store.answered(t.ID, pid) || c.store.userOnly(t.ID, pid) != "" {
				continue
			}
			seq, _ := strconv.Atoi(c.store.get("notified_perm:" + t.ID + ":" + pid))
			if seq > 0 && c.store.isDelivered(seq) {
				c.store.markUserOnly(t.ID, pid, c.conf().assistantName()+" ne l'a pas tranchée")
			}
		}
	}
}
