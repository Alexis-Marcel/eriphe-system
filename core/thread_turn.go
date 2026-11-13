package main

import (
	"fmt"
	"strconv"
)

// The thread a brain's turn is about. A turn that names a thread (a
// thread tool called on it, or the event of one of its steps delivered)
// is about that thread until it names another: what it raises then for
// the user (the card of one of its own calls, a task's start) is the
// thread's, and shows in its timeline (linkCard, thread_steps.go). Two
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

// threadEventKey marks an event of the conversation as one of a thread's
// (tellBrain): its delivery makes the turn about it.
func threadEventKey(seq int) string { return fmt.Sprintf("thread_msg:%d", seq) }

// threadsDelivered sets the thread the turn is about from what it was
// given: none, unless an event of a thread is among it. A new delivery
// never keeps the thread of the turn before.
func (c *core) threadsDelivered(msgs []Message) {
	var id int64
	for _, m := range msgs {
		if n, err := strconv.ParseInt(c.store.get(threadEventKey(m.Seq)), 10, 64); err == nil && n > 0 {
			id = n
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
	if _, ok := c.stepOfCard(ref); ok {
		return // a step's card: its step moves the thread
	}
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
