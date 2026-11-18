package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// childThread opens a light child of parent.
func childThread(t *testing.T, c *core, parent int64, title, alias string) Thread {
	t.Helper()
	s, err := c.openThread(threadIn{Title: title, Goal: title + ", fait.", Alias: alias, Parent: parent}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if s.Parent != parent {
		t.Fatalf("parent %d, want %d", s.Parent, parent)
	}
	return s
}

// Depth is 2: a child has no children, a thread with children is never
// made a child, and a parent is an active thread.
func TestChildThreadDepth(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")

	if _, err := c.openThread(threadIn{Title: "Une ligne du devis", Goal: "La ligne.", Alias: "topic:ligne", Parent: kid.ID}, nil, byBrain); err == nil ||
		!strings.Contains(err.Error(), "a child has no children") {
		t.Fatalf("a grandchild: %v", err)
	}
	other, err := c.openThread(threadIn{Title: "Le jardin", Goal: "Le jardin est fait.", Alias: "topic:jardin"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	// A parent found again for its matter is not moved under another.
	_, _, err = c.openThreadOnce(threadIn{Title: parent.Title, Goal: parent.Goal, Alias: "pr:example/infra#12", Parent: other.ID}, nil, byBrain)
	if err == nil || !strings.Contains(err.Error(), "has child threads") || !strings.Contains(err.Error(), "« Le devis »") {
		t.Fatalf("a parent made a child: %v", err)
	}
	// A top thread found again for its matter moves under the parent named.
	moved, note, err := c.openThreadOnce(threadIn{Title: "Le jardin", Goal: "Le jardin est fait.", Alias: "topic:jardin", Parent: parent.ID}, nil, byBrain)
	if err != nil || moved.ID != other.ID || moved.Parent != parent.ID || !strings.Contains(note, "now a child") {
		t.Fatalf("moved under: %+v %q %v", moved, note, err)
	}
	if _, err := c.openThread(threadIn{Title: "X", Goal: "X.", Alias: "topic:x", Parent: 999}, nil, byBrain); err == nil {
		t.Fatal("a parent that is not")
	}
	// A standing thread is a top thread: never opened, moved nor turned
	// into a child.
	mails, err := c.openThread(threadIn{Title: "Mails", Goal: "Les mails.", Alias: "topic:mails", Ends: endsStanding}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.openThread(threadIn{Title: "Agenda", Goal: "L'agenda.", Alias: "topic:agenda", Ends: endsStanding, Parent: parent.ID}, nil, byBrain); err == nil || !strings.Contains(err.Error(), "never a child") {
		t.Fatalf("a standing child: %v", err)
	}
	if _, _, err := c.openThreadOnce(threadIn{Title: "Mails", Goal: "Les mails.", Alias: "topic:mails", Parent: parent.ID}, nil, byBrain); err == nil || !strings.Contains(err.Error(), "never a child") {
		t.Fatalf("a standing thread moved under: %v", err)
	}
	if m, _ := c.thread(mails.ID); m.Parent != 0 {
		t.Fatal("moved")
	}
	if _, _, err := c.openThreadOnce(threadIn{Title: "Le devis", Goal: "Le devis, fait.", Alias: "topic:devis", Ends: endsStanding}, nil, byBrain); err == nil || !strings.Contains(err.Error(), "never a child") {
		t.Fatalf("a child turned standing: %v", err)
	}
	// An open refused for another reason moves nothing.
	loose, _ := c.openThread(threadIn{Title: "Le portail", Goal: "Le portail est posé.", Alias: "topic:portail"}, nil, byBrain)
	if _, _, err := c.openThreadOnce(threadIn{Title: "Le portail", Goal: "Le portail est posé.", Alias: "topic:portail", Parent: parent.ID, Ends: "someday"}, nil, byBrain); err == nil {
		t.Fatal("ends someday")
	}
	if l, _ := c.thread(loose.ID); l.Parent != 0 {
		t.Fatal("moved by a refused open")
	}
	ended, _ := c.openThread(threadIn{Title: "Fini", Goal: "Fini.", Alias: "topic:fini"}, nil, byBrain)
	c.closeLight(ended.ID, "Fait.", byBrain)
	if _, err := c.openThread(threadIn{Title: "Y", Goal: "Y.", Alias: "topic:y", Parent: ended.ID}, nil, byBrain); err == nil || !strings.Contains(err.Error(), "reopen it first") {
		t.Fatalf("under an ended parent: %v", err)
	}
}

// Closing a parent with active children is refused, naming them; once
// they ended, it closes.
func TestChildThreadCloseRefused(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")
	err := c.closeLight(parent.ID, "Fusionnée.", byBrain)
	if err == nil || !strings.Contains(err.Error(), "« Le devis »") {
		t.Fatalf("closed with an active child: %v", err)
	}
	if p, _ := c.thread(parent.ID); !p.active() {
		t.Fatal("the parent ended")
	}
	if err := c.closeLight(kid.ID, "Signé.", byBrain); err != nil {
		t.Fatal(err)
	}
	if err := c.closeLight(parent.ID, "Fusionnée.", byBrain); err != nil {
		t.Fatalf("once its child closed: %v", err)
	}
}

// Stopping a parent stops its active children; a child does not reopen
// under a parent that ended.
func TestChildThreadStopCascades(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")
	done := childThread(t, c, parent.ID, "Les photos", "topic:photos")
	c.closeLight(done.ID, "Envoyées.", byBrain)
	if err := c.stopThread(context.Background(), parent.ID, byBrain); err != nil {
		t.Fatal(err)
	}
	if k, _ := c.thread(kid.ID); k.State != threadDropped {
		t.Fatalf("child %s", k.State)
	}
	if k, _ := c.thread(done.ID); k.State != threadDone {
		t.Fatalf("an ended child changed: %s", k.State)
	}
	if !slices.ContainsFunc(c.threadLog(kid.ID, 5), func(l ThreadLine) bool { return strings.Contains(l.Text, "thread parent") }) {
		t.Errorf("log %v", c.threadLog(kid.ID, 5))
	}
	if _, err := c.reopenThread(kid.ID, "Le devis revient", byBrain); err == nil || !strings.Contains(err.Error(), "reopen it first") {
		t.Fatalf("reopened under an ended parent: %v", err)
	}
	if _, err := c.reopenThread(parent.ID, "Ça revient", byBrain); err != nil {
		t.Fatal(err)
	}
	if _, err := c.reopenThread(kid.ID, "Le devis revient", byBrain); err != nil {
		t.Fatalf("once its parent reopened: %v", err)
	}
}

// find_thread and read_thread show parent and children.
func TestFindReadShowKin(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")
	v, err := c.threadView(parent.ID, 5)
	if err != nil || len(v.Children) != 1 || v.Children[0].ID != kid.ID || v.Children[0].Title != "Le devis" || v.Children[0].State != threadOpen {
		t.Fatalf("read: %+v %v", v.Children, err)
	}
	kv, _ := c.threadView(kid.ID, 5)
	if kv.Parent != parent.ID || kv.ParentTitle != parent.Title {
		t.Fatalf("read the child: %d %q", kv.Parent, kv.ParentTitle)
	}
	out, isErr := callBrainTool(t, c, "find_thread", map[string]any{"alias": "pr:example/infra#12"})
	found := foundThreads(out)
	if isErr || len(found) != 1 {
		t.Fatalf("find: %v", out)
	}
	kids, _ := found[0].(map[string]any)["children"].([]any)
	if len(kids) != 1 || kids[0].(map[string]any)["title"] != "Le devis" {
		t.Fatalf("find: %v", found[0])
	}
}
