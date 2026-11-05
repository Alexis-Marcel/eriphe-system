package main

import (
	"fmt"
	"strconv"
)

// A task's lent tools are its authorization. When the brain starts a task,
// it lends it the catalog tools the work needs (run_task's resources), and
// eriphe-core checks the lend: an acting tool only in a clean turn
// (taintedResources), never more than the user opened. Once lent, a tool is
// the task's to use: its calls are not asked again, one by one.
//
// Only the reads of a task the brain started alone (woken by a watch or an
// event): its actions are authorized by a lend made in the turn of one of
// my messages, the request it answers (lentByUserKey).
//
// What stays out of a lend, decided by the policy as before:
//
//   - the built-in denials, the rules of the configuration and the actions
//     the user allowed for good;
//   - a server set to ask (the user wants to be asked for everything) or
//     read-only;
//   - an action that reaches outside (sending a mail, publishing), or that
//     no annotation the server is trusted for says stays inside: the
//     user's to approve.
//
// What is left to the brain (a tool it did not lend reads as nothing here)
// is its to answer; if its turn ends without an answer, the request is the
// user's (brainLeftPermissions).

const lentReason = "prêté à la tâche"

// lendAllows tells whether a task's lend authorizes a call of a catalog
// tool, by its Claude Code name: lent, and a read or an action that stays
// inside its service, by the annotations of a server trusted for them.
func (c *core) lendAllows(t *TaskRow, claudeName string) bool {
	server, name, ok := splitToolName(claudeName)
	if !ok || c.pool == nil {
		return false
	}
	u := c.pool.get(server)
	if u == nil || !u.srv.trusts() {
		return false
	}
	tool := u.toolByClaudeName(name)
	if tool == nil || !grantAllows(c.taskGrant(t), server, tool.Name) || !lends(u.srv, tool) {
		return false
	}
	a := tool.Annotations
	if a == nil {
		return false
	}
	if a.ReadOnlyHint {
		return true
	}
	return a.OpenWorldHint != nil && !*a.OpenWorldHint && c.store.get(lentByUserKey(t.ID)) != ""
}

// lentByUserKey marks a task started in the turn of one of my messages (or
// as a watch I authorised in advance): its lend of actions answers what I
// asked. A task the brain started alone (woken by a watch, an event) has
// its reads only: its actions are asked as before.
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
