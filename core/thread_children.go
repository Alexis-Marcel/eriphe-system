package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
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
//     reopen before it. An ended child's surface leaves with its parent's
//     (keepThreadKin), and a link that shows it again shows its parent.
//   - The workspace lays out top threads only. A child's surface says
//     the surface it opens inside (surfaceLook.Inside): it is never a
//     tile nor a chip; opened, its parent is on stage with it inside
//     (workspace_layout.go). It shows a crumb back to its parent.
//   - Its parent shows it among its children (blockChildren): title,
//     status word and one line, read from the child's own surface, and a
//     button that opens it. What waits for the user in a child raises its
//     parent: its parent waits for the user too, at the highest weight of
//     its own and its children's.
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
	if s.Parent != 0 {
		c.keepThreadSurface(s.Parent) // it leaves its old parent's children
	}
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

// The surfaces.

// threadChild is a child as its parent's surface shows it, at
// /thread/children: read from the child's own surface, as code wrote it.
type threadChild struct {
	Thread  int64  `json:"thread"`
	Surface string `json:"surface"` // what its row's button opens
	Title   string `json:"title"`
	Status  string `json:"status"` // its status word
	Line    string `json:"line"`   // its one line
	Level   string `json:"level"`  // high while it waits for the user, low once ended
	Mark    string `json:"mark"`   // « extérieur » when its surface may carry someone else's words
}

// threadKids are a thread's children as its surface shows them, and what
// they raise it by.
type threadKids struct {
	rows   []threadChild
	weight int      // the highest of its active children's
	waits  bool     // one of them waits for the user
	taint  []string // those whose surface may carry someone else's words
}

// threadKidsOf reads a thread's children from their surfaces: an active
// one always, an ended one while its surface is on the workspace.
func (c *core) threadKidsOf(s Thread, lang string) threadKids {
	var k threadKids
	for _, kid := range c.children(s.ID) {
		sid := threadSurfaceID(kid.ID)
		sf, err := c.surface(sid)
		has := err == nil
		if !kid.active() && !has {
			continue
		}
		row := threadChild{Thread: kid.ID, Surface: sid, Title: kid.Title, Level: "normal"}
		if has && sf.Look.Summary != tr(lang, "thread.state."+kid.Look()) && sf.Look.Summary != tr(lang, "thread.line."+kid.State) {
			row.Line = sf.Look.Summary // not when it only says its state again, as its status word does
		}
		switch {
		case !kid.active():
			row.Status, row.Level = tr(lang, "thread.state."+kid.State), "low"
		case has && sf.Look.State == tileYours:
			row.Status, row.Level = tr(lang, "thread.surface.yours"), "high"
			k.waits = true
		case has && sf.Look.State == tileUrgent:
			row.Status = tr(lang, "thread.status.urgent")
		case has && sf.Look.State == tileRunning:
			row.Status = tr(lang, "thread.state.running")
		default:
			row.Status = tr(lang, "thread.status.calm")
		}
		if kid.active() && has {
			k.weight = max(k.weight, sf.Weight)
		}
		if has && sf.Tainted != "" {
			row.Mark = tr(lang, "thread.surface.external")
			k.taint = append(k.taint, threadAuthor(kid.ID))
		}
		k.rows = append(k.rows, row)
	}
	return k
}

// blockChildren composes a thread's children: one row each, its title,
// status word and line, and a button that opens it inside its parent (a
// local open that names the child's surface: the page opens it, nothing
// reaches the brain). Its data is at /thread/children, for a part to bind.
func blockChildren(out *threadComps, data map[string]any, kids []threadChild, lang string) {
	if len(kids) == 0 {
		data["children"] = []threadChild{} // there to bind all the same
		return
	}
	data["children"] = kids
	type comp = map[string]any
	bound := func(k string) map[string]any { return map[string]any{"@path": k} }
	out.inner(
		comp{"id": "thread_child", "component": "Row", "align": "center", "children": []string{"thread_child_item", "thread_child_open"}},
		comp{"id": "thread_child_item", "component": "Item", "title": bound("title"), "detail": bound("status"), "note": bound("line"),
			"level": bound("level"), "mark": bound("mark")},
		comp{"id": "thread_child_open", "component": "Button", "label": tr(lang, "thread.child.open"), "variant": "quiet",
			"action": map[string]any{"event": map[string]any{"name": a2ui.ActionOpen, "context": map[string]any{"surface": bound("surface")}}}},
	)
	out.add(comp{"id": "thread_children_title", "component": "Text", "variant": "caption", "text": tr(lang, "thread.children")})
	out.add(comp{"id": "thread_children", "component": "List", "children": map[string]any{"path": "/thread/children", "componentId": "thread_child"}})
}

// blockParent composes a child's crumb: its parent's title, a button that
// opens the parent (the same local open, the other way).
func (c *core) blockParent(out *threadComps, data map[string]any, s Thread) {
	if s.Parent == 0 {
		return
	}
	p, err := c.thread(s.Parent)
	if err != nil {
		return
	}
	sid := threadSurfaceID(p.ID)
	data["parent"] = map[string]any{"thread": p.ID, "surface": sid, "title": p.Title}
	out.add(map[string]any{"id": "thread_parent", "component": "Button", "icon": "arrowUp", "variant": "quiet",
		"label":  map[string]any{"@path": "/thread/parent/title"},
		"action": map[string]any{"event": map[string]any{"name": a2ui.ActionOpen, "context": map[string]any{"surface": sid}}}})
}

// keepThreadKin writes again the surfaces a thread's own depends on: its
// parent's, which shows it among its children; and, once its own surface
// is gone, its ended children's, which leave with it (threadSurfaceIn).
func (c *core) keepThreadKin(id int64, shown bool) {
	if c.conf() == nil {
		return
	}
	s, err := c.thread(id)
	if err != nil {
		return
	}
	if s.Parent != 0 {
		c.writeThreadSurface(s.Parent)
	}
	if shown {
		return
	}
	for _, kid := range c.children(id) {
		if !kid.active() {
			c.writeThreadSurface(kid.ID)
		}
	}
}

// insideOf is the surface a thread's surface opens inside: its parent's.
func insideOf(s Thread) string {
	if s.Parent == 0 {
		return ""
	}
	return threadSurfaceID(s.Parent)
}
