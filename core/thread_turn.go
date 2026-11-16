package main

import (
	"context"
	"errors"
	"fmt"
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
// given: none, unless an event of a thread is among it, or a message of
// the user's written about a thread (its surface open on the workspace,
// or a surface about a live thread); the last one wins. A new
// delivery never keeps the thread of the turn before.
func (c *core) threadsDelivered(msgs []Message) {
	var id int64
	for _, m := range msgs {
		if n, err := strconv.ParseInt(c.store.get(threadEventKey(m.Seq)), 10, 64); err == nil && n > 0 {
			id = n
		} else if n := c.threadAbout(m); n > 0 {
			id = n
		}
	}
	c.turn.about(id)
}

// threadAbout is the thread a message of the user's was written about: the
// thread whose surface was open, or the live thread the open surface is
// about; 0 if none.
func (c *core) threadAbout(m Message) int64 {
	if m.Role != roleUser {
		return 0
	}
	sid, ok := strings.CutPrefix(c.messageAbout(m.Seq), refSurface+":")
	if !ok {
		return 0
	}
	if n, ok := threadOfSurface(sid); ok {
		if _, err := c.thread(n); err == nil {
			return n
		}
		return 0
	}
	if s, err := c.surface(sid); err == nil && s.About != "" {
		if n, ok := c.liveThreadOf(s.About); ok {
			return n
		}
	}
	return 0
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

// taskOfThread makes the task the brain starts a task step of a thread:
// the thread its turn is about, else the live thread the
// user's message named. A task is never left loose and code never opens
// a thread for it: with neither, the brain opens (or finds) the matter's
// thread first. A led thread's plan changes with amend_thread, an ended
// one is reopened first. A surface to fill or a node named go with the
// step.
func (c *core) taskOfThread(ctx context.Context, in runRequest) (map[string]any, error) {
	s, err := c.taskThread()
	if err != nil {
		return nil, err
	}
	if err := c.delegateSurface(&in); err != nil { // workspace_tools.go
		return nil, err
	}
	if err := c.taintedResources(in); err != nil { // resources.go
		return nil, err
	}
	if err := c.checkTaskRequest(in); err != nil {
		return nil, err // refused as run_task refuses it, with no step born
	}
	title := in.Title
	if title == "" {
		title = (&TaskRow{Prompt: in.Prompt}).label()
	}
	c.turnThread(s.ID) // what the turn raises next is the thread's
	sp := stepSpec{Key: c.freeStepKey(s.ID), Title: title, Kind: stepTask, Prompt: in.Prompt,
		Agent: in.Agent, Resources: in.Resources, Contract: in.Contract, Node: in.Node, Surface: in.Surface}
	st, err := c.addStep(ctx, s.ID, sp, c.turnTaint(), byBrain)
	if err != nil {
		return nil, fmt.Errorf("not a step of thread %d « %s »: %w", s.ID, s.Title, err)
	}
	if st.Status == stepBlocked { // its task could not start: the thread shows why
		return nil, fmt.Errorf("the task could not start (step %s of thread %d, blocked): %s", st.Key, s.ID, st.Note)
	}
	out := map[string]any{"thread": s.ID, "step": st.Key, "task": st.Task, "status": st.Status,
		"note": fmt.Sprintf("A task step of thread %d « %s »: the thread follows it, and its end reaches you as the thread's event; settle it with settle_step.", s.ID, s.Title)}
	if t, err := c.store.task(st.Task); st.Task != "" && err == nil {
		out["start_state"] = c.startState(t) // task_start.go
	}
	return out, nil
}

// errOpenThreadFirst is run_task's refusal in a turn about no thread.
var errOpenThreadFirst = errors.New("a task is a step of a thread, and this turn is about none: open_thread the matter's thread first (it gives back the one that exists), then run_task; add_step adds a task to another thread")

// taskThread is the thread a task the brain starts is a step of: the one
// its turn is about, else the live thread the user's
// message named. A led one, an ended one or none is refused, saying what
// to do instead.
func (c *core) taskThread() (Thread, error) {
	id := c.turn.aboutThread()
	if id <= 0 {
		if about := c.turnAbout(); about != "" {
			if key, err := c.aboutLookup(about); err == nil && key != "" {
				id, _ = c.liveThreadOf(key)
			}
		}
	}
	if id <= 0 {
		return Thread{}, errOpenThreadFirst
	}
	s, err := c.thread(id)
	switch {
	case err != nil:
		return Thread{}, errOpenThreadFirst
	case s.led():
		return Thread{}, fmt.Errorf("thread %d « %s » is led: its plan changes with amend_thread (or tell_lead); open_thread another matter's thread for other work", s.ID, s.Title)
	case !s.active():
		return Thread{}, fmt.Errorf("thread %d « %s » is %s: reopen_thread it if its matter came back, or open_thread another", s.ID, s.Title, s.State)
	}
	return s, nil
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
