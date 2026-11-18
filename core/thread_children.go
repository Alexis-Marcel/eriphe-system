package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Child threads (#609): a part of a thread that becomes a matter of its
// own is a thread of its own, its child (open_thread parent). Nothing
// else tells a child from a top thread: it ends on its goal or stands, as
// any other.
//
//   - Depth 2: a child has no children, and a thread with children is
//     never made a child (parentFor). A standing thread, what the user
//     manages continuously, is a top thread: never a child.
//   - No active child under an ended parent: closing a parent with active
//     children is refused, naming them (activeChildrenError); a parent
//     that ends any other way (stopped, its plan declined, its day over)
//     stops them (endChildren); a child of an ended parent does not
//     reopen before it.
//   - find_thread and read_thread show parent and children, so the brain
//     finds the child that exists before it opens another.

// children are a thread's children, the active ones first, then by age.
func (c *core) children(id int64) []Thread {
	kids, _ := c.threadsWhere(`parent = ? ORDER BY state NOT IN ('open', 'waiting'), id`, id)
	return kids
}

// activeChildren are a thread's children that may still move.
func (c *core) activeChildren(id int64) []Thread {
	kids, _ := c.threadsWhere(`parent = ? AND `+activeThreads+` ORDER BY id`, id)
	return kids
}

// parentFor is the thread a thread may be the child of: one that exists,
// is active and is a top thread. child is the thread to put under it, as
// it will end (ID 0 for a new one): never the parent itself, a standing
// thread, nor a thread with children.
func (c *core) parentFor(parent int64, child Thread) (Thread, error) {
	p, err := c.thread(parent)
	switch {
	case errors.Is(err, errNoThread):
		return p, fmt.Errorf("parent: no thread %d", parent)
	case err != nil:
		return p, err
	case parent == child.ID:
		return p, errors.New("parent: a thread is not its own child")
	case child.standing():
		return p, errors.New("parent: a standing thread is what the user manages continuously, a top thread: never a child")
	case p.Parent != 0:
		return p, fmt.Errorf("parent: thread %d « %s » is a child of thread %d already, and a child has no children: open it under thread %d, or as a top thread", p.ID, p.Title, p.Parent, p.Parent)
	case !p.active():
		return p, fmt.Errorf("parent: thread %d « %s » is %s: reopen it first, or open this one as a top thread", p.ID, p.Title, p.State)
	}
	if child.ID != 0 {
		if kids := c.children(child.ID); len(kids) > 0 {
			return p, fmt.Errorf("parent: thread %d has child threads (%s), and a child has no children", child.ID, kinList(kids))
		}
	}
	return p, nil
}

// moveUnder makes a thread found again for a matter (open_thread on its
// alias) the child of parent, as its opener asks; checked by parentFor
// first.
func (c *core) moveUnder(s Thread, parent int64, by string) error {
	p, err := c.thread(parent)
	if err != nil {
		return err
	}
	if err := c.setThread(s.ID, map[string]any{"parent": parent}); err != nil {
		return err
	}
	c.logThread(s.ID, by, fmt.Sprintf("Rangé sous le thread %d « %s ».", p.ID, p.Title))
	return nil
}

// activeChildrenError refuses to close a thread whose children still move.
func activeChildrenError(s Thread, kids []Thread) error {
	return fmt.Errorf("thread %d « %s » has active child threads: %s. Close or stop them first; stopping it stops them", s.ID, s.Title, kinList(kids))
}

// kinList names threads in an error: 5 « Le devis », 6 « Les photos ».
func kinList(ts []Thread) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, fmt.Sprintf("%d « %s »", t.ID, t.Title))
	}
	return strings.Join(parts, ", ")
}

// endChildren stops a thread's active children: it ended. Under threadMu.
func (c *core) endChildren(ctx context.Context, s Thread) {
	for _, kid := range c.activeChildren(s.ID) {
		c.stopLocked(ctx, kid, "engine", fmt.Sprintf("Arrêté avec son thread parent %d « %s ».", s.ID, s.Title))
	}
}

// threadKin is a parent or a child as find_thread and read_thread show it.
type threadKin struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

func kinOf(ts []Thread) []threadKin {
	out := make([]threadKin, 0, len(ts))
	for _, t := range ts {
		out = append(out, threadKin{ID: t.ID, Title: t.Title, State: t.State})
	}
	return out
}

// threadFound is a thread as find_thread lists it: with its children.
type threadFound struct {
	Thread
	Children []threadKin `json:"children,omitempty"`
}
