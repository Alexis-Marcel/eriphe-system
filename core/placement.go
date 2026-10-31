package main

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// Placement classes, like Kubernetes' hard rules and preferences. Nothing
// here is a user setting: the class comes from what the task is.
//
//   - resident: an assistant that talks to the user or is woken by events
//     (the brain first). Hard rule: an online always-on node that fits; the
//     best other node only when none does, and it comes back as soon as it
//     can. Someone waits for its answer: a machine that sleeps is no home.
//   - bound: a task that needs one node's folders (placement.Machine). It
//     goes there and waits for it.
//   - the others: the scores of scheduler.go, preferences only.
//
// A node chosen by hand (the task's or the assistant's node) always wins.

const (
	// residentNodeGrace is how long a resident task waits for its node to
	// come back before it moves: short, someone may be waiting.
	residentNodeGrace = 30 * time.Second
	// pendingNodeGrace is how long a task not started yet waits for its
	// node before it is placed again, like a pod on an unreachable node.
	pendingNodeGrace = 5 * time.Minute
)

// resident tells whether an assistant of this role lives on an always-on
// node: one that talks to the user, hears the conversation, or is woken
// by triggers.
func (r assistantRole) resident() bool {
	return r.can(powerTalksToUser) || r.can(powerReceivesConversation) || len(r.Triggers) > 0
}

// residentKind tells whether tasks of this kind are resident.
func residentKind(kind string) bool {
	r, ok := assistantRoles[kind]
	return ok && r.resident()
}

// steadyNodes are the online always-on nodes that can run p.
func steadyNodes(nodes []NodeRow, p placement) []NodeRow {
	var out []NodeRow
	for _, n := range nodes {
		if n.Online && p.fits(n) && parseInfo(n).AlwaysOn {
			out = append(out, n)
		}
	}
	return out
}

// placementOf is what a task asks of its node, from its agent and folders now.
func (c *core) placementOf(t *TaskRow) (placement, error) {
	_, agent, err := c.conf().agent(t.Agent)
	if err != nil {
		return placement{}, err
	}
	folders, _, err := c.folderGrant(splitNames(t.Folders))
	if err != nil {
		return placement{}, err
	}
	p := c.taskPlacement(agent, folders)
	p.Resident = residentKind(t.Kind)
	return p, nil
}

// noteFallback logs why a resident task went to a node that may sleep.
func noteFallback(id, kind, name string, nodes []NodeRow, p placement) {
	if !p.Resident || name == "" {
		return
	}
	var why []string
	for _, n := range nodes {
		if n.Name == name && parseInfo(n).AlwaysOn {
			return
		}
		if !parseInfo(n).AlwaysOn {
			continue
		}
		switch {
		case !n.Online:
			why = append(why, n.Name+" is offline")
		case !p.fits(n):
			why = append(why, n.Name+" cannot run "+p.String())
		}
	}
	if len(why) == 0 {
		why = []string{"no node is always on"}
	}
	log.Printf("task %s (%s) placed on %s for now: %s", id, kind, name, strings.Join(why, ", "))
}

// since is when a node was last there, eriphe-core's start at the
// earliest: after a restart, nodes need a moment to connect again.
func (c *core) since(n NodeRow) time.Time {
	if n.LastSeen.Before(c.started) {
		return c.started
	}
	return n.LastSeen
}

// moveResident moves an assistant's task that should live elsewhere: its
// node has been offline past residentNodeGrace and another can take it,
// or it runs on a node that may sleep while an always-on one can take it
// now (only at idle, between two turns). It stops the task; the
// controller starts the next one, which the scheduler places, without
// resuming: the session is on the old node, and the thread is eriphe-core's.
// It reports whether it moved it.
func (c *core) moveResident(a Assistant, r assistantRole, t *TaskRow) bool {
	if a.Node != "" || t.Node == "" || !r.resident() {
		return false
	}
	p, err := c.placementOf(t)
	if err != nil || p.Machine != "" {
		return false // bound to its folders' machine: it waits for it
	}
	all, err := c.store.nodes()
	if err != nil {
		return false
	}
	nodes := c.schedulable(all)
	i := slices.IndexFunc(all, func(n NodeRow) bool { return n.Name == t.Node })
	var why string
	switch {
	case i < 0:
		return false // forgotten: its tasks are lost with it, and the controller starts another
	case !all[i].Online:
		if time.Since(c.since(all[i])) < residentNodeGrace {
			return false
		}
		if !slices.ContainsFunc(nodes, func(n NodeRow) bool { return n.Online && n.Name != t.Node && p.fits(n) }) {
			return false // nowhere else to go: it waits for its node
		}
		why = fmt.Sprintf("node %s has been offline since %s", t.Node, c.since(all[i]).Format(time.TimeOnly))
	case !parseInfo(all[i]).AlwaysOn && t.State == stateIdle && !c.hasUndelivered(t.ID):
		steady := steadyNodes(nodes, p)
		if len(steady) == 0 {
			return false
		}
		why = fmt.Sprintf("node %s may sleep and the always-on %s can take it", t.Node, steady[0].Name)
	default:
		return false
	}
	log.Printf("%s: moving task %s off %s: %s", c.assistantLog(a), t.ID, t.Node, why)
	// What it was given but its node never received goes to the next task.
	if a.has(triggerConversation) && c.hasUndelivered(t.ID) {
		if from, to := c.turn.span(); from > 0 {
			c.store.undeliver(from, to)
		}
	}
	c.store.set("brain_stopped:"+t.ID, "1") // not an end to report
	c.store.updateTask(t.ID, map[string]any{"desired": desiredStopped, "state": stateStopped,
		"pending_reason": "moved: " + why})
	c.updateAssistant(a.Name, func(st *AssistantStatus) {
		st.Session, st.Resume, st.NextStart = "", "", 0
	})
	c.bump()
	return true
}

// replaceStranded places again the tasks not started yet whose node has
// been offline past pendingNodeGrace, unless they are bound to it (by
// hand or by their folders). Running tasks keep their node: their work
// is there. Assistants' tasks are moveResident's.
func (c *core) replaceStranded() {
	rows, err := c.store.stranded()
	if err != nil || len(rows) == 0 {
		return
	}
	nodes, err := c.store.nodes()
	if err != nil {
		return
	}
	moved := false
	for _, t := range rows {
		if isRoleKind(t.Kind) {
			continue
		}
		i := slices.IndexFunc(nodes, func(n NodeRow) bool { return n.Name == t.Node })
		if i >= 0 && (nodes[i].Online || time.Since(c.since(nodes[i])) < pendingNodeGrace) {
			continue
		}
		if p, err := c.placementOf(t); err != nil || p.Machine != "" {
			continue
		}
		reason := fmt.Sprintf("node %s is offline", t.Node)
		if ok, _ := c.store.unassign(t.ID, t.Node, reason); ok {
			log.Printf("task %s leaves node %s: %s, it has not started", t.ID, t.Node, reason)
			moved = true
		}
	}
	if moved {
		c.placePending()
		c.bump()
	}
}
