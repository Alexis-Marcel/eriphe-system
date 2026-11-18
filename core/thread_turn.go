package main

import (
	"context"
	"fmt"
)

// The thread a brain's turn is about. A turn that names a thread (a
// thread tool called on it, or the event of one of its steps delivered)
// is about that thread until it names another: what it raises then for
// the user (the card of one of its own calls, a task's start) is the
// thread's, and shows in its timeline (linkCard, thread_light.go). Two
// threads in one turn (« pendant que je relis la PR, cherche… »): the
// last one named.

// about records the thread the turn is about now.
func (t *brainTurn) about(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.thread != id {
		t.thread = id
		t.save()
	}
}

// aboutThread is the thread the turn is about; 0 if none.
func (t *brainTurn) aboutThread() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.thread
}

// threadsDelivered sets the thread the turn is about from what it was
// given: the last message that belongs to a thread (an event of a thread,
// or a message of the user's written with it open: thread_talk.go); none
// otherwise. A new delivery never keeps the thread of the turn before.
func (c *core) threadsDelivered(msgs []Message) {
	var id int64
	for _, m := range msgs {
		if m.Thread > 0 {
			id = m.Thread
		}
	}
	c.turn.about(id)
}

// turnThread notes that the brain's turn named a thread, by a tool of its
// own on it; an id that is no thread changes nothing.
func (c *core) turnThread(id int64) {
	if id <= 0 {
		return
	}
	if _, err := c.thread(id); err == nil {
		c.turn.about(id)
	}
}

// linkedCardMoved tells the threads a card is linked to that it was
// decided: their surface shows it waiting no more.
func (c *core) linkedCardMoved(ref string) {
	rows, err := c.store.db.Query(`SELECT thread FROM thread_cards WHERE ref = ?`, ref)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close() // one connection: the threads are read after
	for _, id := range ids {
		c.threadMoved(id)
	}
}

// turnSource is who raises a card in the brain's turn: the thread it is
// about (thread:<id>), or "" when it is about none.
func (c *core) turnSource() string {
	if id := c.turn.aboutThread(); id > 0 {
		return threadRef(id)
	}
	return ""
}

// linkedThread is the thread a card was last linked to (thread_cards); 0
// if none.
func (c *core) linkedThread(ref string) int64 {
	var id int64
	c.store.db.QueryRow(`SELECT thread FROM thread_cards WHERE ref = ? ORDER BY created DESC, thread DESC LIMIT 1`, ref).Scan(&id)
	return id
}

// callToBrain tells the brain how a call kept on its card went (run on a
// yes, or refused), taint being what its words make the turn. A card
// linked to a thread makes it an event of that thread: the turn that reads
// it is about the thread (threadsDelivered), and what it raises next is
// the thread's. Written delivered, its marks set, then handed to the
// brain, as tellBrain does: it is never delivered before its marks.
func (c *core) callToBrain(a accessQuestion, text, taint string) {
	id := c.linkedThread(proposalRef(cardAccess, a.ID))
	if s, err := c.thread(id); id > 0 && err == nil {
		text = fmt.Sprintf("[thread %d « %s »] %s", s.ID, s.Title, text)
	} else {
		id = 0
	}
	seq, err := c.store.addThreadMessage(roleEvent, text, "", id, true)
	if err != nil {
		return
	}
	if taint != "" {
		c.store.set(taintKey(int(seq)), taint)
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// brainRunTask starts the task the brain asks for, in a node of its own
// (a child of node, when it names one): its start's card goes with the
// brain's message, it is tainted by the turn, and its end reaches the
// brain as an event pointing to its node (task_node.go). A turn about a
// thread makes the task about it.
func (c *core) brainRunTask(ctx context.Context, in runRequest) (map[string]any, error) {
	if err := c.taintedResources(in); err != nil { // resources.go
		return nil, err
	}
	if in.About == "" {
		in.About = c.turnSource()
	}
	in.askStart, in.byBrain, in.taint = true, true, c.turnTaint()
	t, err := c.runTask(ctx, in)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"task": t.ID, "state": t.State, "start_state": c.startState(t)} // task_start.go
	if node := c.taskNode(t.ID); node != "" {
		out["node"] = node
	}
	return out, nil
}
