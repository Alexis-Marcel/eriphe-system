package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
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
	seq, err := c.store.addMessage(roleEvent, text, true)
	if err != nil {
		return
	}
	if taint != "" {
		c.store.set(taintKey(int(seq)), taint)
	}
	if id > 0 {
		c.store.set(threadEventKey(int(seq)), strconv.FormatInt(id, 10))
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// runTaskIn is run_task as the brain calls it: a task, and whether it
// stays outside the thread the turn is about.
type runTaskIn struct {
	runRequest
	Thread string `json:"thread,omitempty" jsonschema:"none: a task of its own, outside the thread this turn is about; leave it out otherwise (add_step adds a task to another thread)"`
}

// noThread is run_task's thread for a task of its own.
const noThread = "none"

// taskOfThread makes a task the brain starts in a turn about a light
// thread a task step of that thread, so the thread follows it: its tile,
// its timeline and its event at the end. ok is false when it stays a task
// of its own: thread none, a surface to fill or a node named (a step has
// neither), a turn about no thread, or about one led or ended. A step the
// thread cannot take (its limits) leaves it a task of its own too.
func (c *core) taskOfThread(ctx context.Context, in runTaskIn) (map[string]any, bool, error) {
	switch strings.ToLower(strings.TrimSpace(in.Thread)) {
	case noThread:
		return nil, false, nil
	case "":
	default:
		return nil, false, errors.New("thread: none for a task of its own, or leave it out; add_step adds a task to another thread")
	}
	id := c.turn.aboutThread()
	if id <= 0 || in.Surface != "" || in.Node != "" {
		return nil, false, nil
	}
	s, err := c.thread(id)
	if err != nil || s.led() || !s.active() {
		return nil, false, nil
	}
	if err := c.taintedResources(in.runRequest); err != nil { // resources.go
		return nil, false, err
	}
	if err := c.checkTaskRequest(in.runRequest); err != nil {
		return nil, false, err // refused as run_task refuses it, with no step born
	}
	title := in.Title
	if title == "" {
		title = (&TaskRow{Prompt: in.Prompt}).label()
	}
	sp := stepSpec{Key: c.freeStepKey(id), Title: title, Kind: stepTask, Prompt: in.Prompt,
		Agent: in.Agent, Resources: in.Resources, Contract: in.Contract}
	st, err := c.addStep(ctx, id, sp, c.turnTaint(), byBrain)
	if err != nil {
		log.Printf("run_task: not a step of thread %d: %v", id, err)
		return nil, false, nil
	}
	if st.Status == stepBlocked { // its task could not start: the thread shows why
		return nil, true, fmt.Errorf("the task could not start (step %s of thread %d, blocked): %s", st.Key, s.ID, st.Note)
	}
	return map[string]any{"thread": s.ID, "step": st.Key, "task": st.Task, "status": st.Status,
		"note": fmt.Sprintf("A task step of thread %d « %s »: the thread follows it, and its end reaches you as the thread's event; settle it with settle_step.", s.ID, s.Title)}, true, nil
}

// checkTaskRequest refuses before any step what startTask would refuse of
// the task itself: no prompt, a contract or an agent that is none, what it
// is lent that cannot be.
func (c *core) checkTaskRequest(r runRequest) error {
	if strings.TrimSpace(r.Prompt) == "" {
		return errors.New("prompt is required")
	}
	if _, _, err := c.conf().agent(r.Agent); err != nil {
		return err
	}
	lent, err := c.resolveRequest(r) // resources.go
	if err != nil {
		return err
	}
	if r.Contract != nil {
		k := *r.Contract
		if err := k.normalize(); err != nil { // contract.go
			return err
		}
		return k.checkLent(lent.Repo != nil, folderNames(lent.Folders))
	}
	return nil
}

// freeStepKey is a key no step of the thread has: task, task-2…
func (c *core) freeStepKey(id int64) string {
	steps, _ := c.steps(id)
	taken := map[string]bool{}
	for _, st := range steps {
		taken[st.Key] = true
	}
	key := stepTask
	for n := 2; taken[key]; n++ {
		key = fmt.Sprintf("%s-%d", stepTask, n)
	}
	return key
}
