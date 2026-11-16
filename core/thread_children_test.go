package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
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

// threadData is code's data on a thread's surface, /thread.
func threadData(t *testing.T, c *core, id int64) map[string]any {
	t.Helper()
	sf, err := c.surface(threadSurfaceID(id))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	json.Unmarshal(sf.Data, &data)
	thread, _ := data[threadDataKey].(map[string]any)
	return thread
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

// Closing a parent with active children is refused, naming them, light
// or led; once they ended, it closes.
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

	// Led: its engine's close is refused the same.
	led, ls := threadCore(t)
	led.setThread(ls.ID, map[string]any{"phase": phaseRunning})
	childThread(t, led, ls.ID, "Les noms libres", "topic:noms")
	if _, err := led.finishThread(context.Background(), ls.ID, "Fini.", byBrain); err == nil || !strings.Contains(err.Error(), "« Les noms libres »") {
		t.Fatalf("a led parent closed with an active child: %v", err)
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

// What waits for the user in a child raises its parent: the parent waits
// for the user, at the highest weight of its own and its children's, and
// shows the child's row, its status word, its line and the surface it
// opens. The child shows a crumb to its parent, and opens inside it.
func TestChildThreadRaisesParent(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	c.threadVisited(parent.ID, time.Now())
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")
	psid, ksid := threadSurfaceID(parent.ID), threadSurfaceID(kid.ID)
	if ps, _ := c.surface(psid); ps.Look.State == tileYours {
		t.Fatalf("raised with nothing waiting: %+v", ps.Look)
	}
	kids := threadData(t, c, parent.ID)["children"].([]any)
	if len(kids) != 1 || kids[0].(map[string]any)["surface"] != ksid || kids[0].(map[string]any)["status"] != "À jour" {
		t.Fatalf("children %v", kids)
	}

	if _, err := c.addStep(context.Background(), kid.ID, stepSpec{Key: "sign", Title: "Signer le devis", Kind: stepUser, Question: "Signe le devis."}, nil, byBrain); err != nil {
		t.Fatal(err)
	}
	ks, _ := c.surface(ksid)
	ps, _ := c.surface(psid)
	if ks.Look.State != tileYours || ps.Look.State != tileYours || ps.Weight < ks.Weight || ps.Weight < threadWeightYours {
		t.Fatalf("child %s/%d, parent %s/%d", ks.Look.State, ks.Weight, ps.Look.State, ps.Weight)
	}
	data := threadData(t, c, parent.ID)
	row := data["children"].([]any)[0].(map[string]any)
	if row["status"] != "À toi" || row["level"] != "high" || row["title"] != "Le devis" || row["line"] != ks.Look.Summary {
		t.Fatalf("row %v", row)
	}
	if data["progress"].(map[string]any)["label"] != "À toi" {
		t.Errorf("the parent's word: %v", data["progress"])
	}
	comps := string(ps.Components)
	if !strings.Contains(comps, `"thread_children"`) || !strings.Contains(comps, `"context":{"surface":{"@path":"surface"}}`) {
		t.Errorf("no children block: %s", comps)
	}
	// The child: its crumb, the surface it opens inside.
	if ks.Look.Inside != psid || ps.Look.Inside != "" {
		t.Fatalf("inside %q / %q", ks.Look.Inside, ps.Look.Inside)
	}
	if kd := threadData(t, c, kid.ID); kd["parent"].(map[string]any)["surface"] != psid ||
		!strings.Contains(string(ks.Components), `"context":{"surface":"`+psid+`"}`) {
		t.Fatalf("crumb %v %s", kd["parent"], ks.Components)
	}

	// Done by the user: the parent calms down again.
	if _, err := c.userStepDone(context.Background(), kid.ID, "sign", byUser); err != nil {
		t.Fatal(err)
	}
	if ps, _ = c.surface(psid); ps.Look.State == tileYours {
		t.Fatalf("still raised: %+v", ps.Look)
	}
}

// A child's taint reaches its parent's surface, marked on its row.
func TestChildThreadTaintsParent(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid, err := c.openThread(threadIn{Title: "Le devis", Goal: "Le devis est signé.", Alias: "topic:devis", Parent: parent.ID}, []string{"mail.read"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if ks, _ := c.surface(threadSurfaceID(kid.ID)); ks.Tainted == "" {
		t.Fatal("the child is not tainted")
	}
	ps, _ := c.surface(threadSurfaceID(parent.ID))
	if !slices.Contains(ps.Look.CodeTaint, threadAuthor(kid.ID)) {
		t.Fatalf("parent taint %v", ps.Look.CodeTaint)
	}
	row := threadData(t, c, parent.ID)["children"].([]any)[0].(map[string]any)
	if row["mark"] != "extérieur" {
		t.Errorf("row %v", row)
	}
}

// An ended child's surface leaves with its parent's: put away, its
// parent takes it along, never left as a tile of its own; and a child's
// surface that leaves takes its row from its parent's.
func TestChildSurfaceLeavesWithParent(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")
	psid, ksid := threadSurfaceID(parent.ID), threadSurfaceID(kid.ID)
	c.closeLight(kid.ID, "Signé.", byBrain)
	c.closeLight(parent.ID, "Fusionnée.", byBrain)
	if _, err := c.surface(ksid); err != nil {
		t.Fatal("an ended child's surface left before its parent's")
	}
	if err := c.tidyThread(parent.ID, byUser); err != nil {
		t.Fatal(err)
	}
	if _, err := c.surface(psid); err == nil {
		t.Fatal("the parent stayed")
	}
	if _, err := c.surface(ksid); err == nil {
		t.Fatal("the child stayed without its parent")
	}
	if l := c.relayout(); slices.Contains(chipsOf(l), ksid) || slices.ContainsFunc(l.Items, func(x layoutTile) bool { return x.ID == ksid }) {
		t.Fatalf("the child laid out: %s %v", tilesOf(l), chipsOf(l))
	}

	// Its lifetime over, an ended child leaves its active parent's rows.
	c2, _ := surfaceCore(t)
	p2 := lightThread(t, c2)
	k2 := childThread(t, c2, p2.ID, "Le devis", "topic:devis")
	c2.closeLight(k2.ID, "Signé.", byBrain)
	if rows := threadData(t, c2, p2.ID)["children"].([]any); len(rows) != 1 {
		t.Fatalf("rows %v", rows)
	}
	c2.tickSurfaces(context.Background(), time.Now().Add(72*time.Hour))
	if _, err := c2.surface(threadSurfaceID(k2.ID)); err == nil {
		t.Fatal("the child's surface outlived its lifetime")
	}
	if rows := threadData(t, c2, p2.ID)["children"].([]any); len(rows) != 0 {
		t.Fatalf("a row for a child no longer shown: %v", rows)
	}
}

// The workspace lays out top threads only: a child is neither a tile nor a
// chip; opened, its parent is on stage with it inside. A child whose
// parent's surface is gone is laid out as any.
func TestLayoutTopThreadsOnly(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	kid := childThread(t, c, parent.ID, "Le devis", "topic:devis")
	psid, ksid := threadSurfaceID(parent.ID), threadSurfaceID(kid.ID)
	l := c.relayout()
	if _, ok := tileOf(l, psid); !ok {
		t.Fatalf("no parent tile: %s", tilesOf(l))
	}
	if _, ok := tileOf(l, ksid); ok || slices.Contains(chipsOf(l), ksid) {
		t.Fatalf("a child laid out: %s / %v", tilesOf(l), chipsOf(l))
	}
	c.focusSurface(ksid, time.Now())
	if l = c.currentLayout(); l.Focus != psid || l.Inside != ksid {
		t.Fatalf("opened: focus %q inside %q", l.Focus, l.Inside)
	}
	if _, ok := tileOf(l, ksid); ok {
		t.Fatalf("the child a tile: %s", tilesOf(l))
	}
	c.focusSurface(psid, time.Now())
	if l = c.currentLayout(); l.Focus != psid || l.Inside != "" {
		t.Fatalf("its parent opened: focus %q inside %q", l.Focus, l.Inside)
	}

	at := time.Now()
	orphan := []surface{{ID: "thread-9", Title: "Orphelin", Weight: 50, Updated: at, Look: surfaceLook{Inside: "thread-8"}}}
	if l := layoutOf(orphan, ""); len(l.Items) != 1 || l.Items[0].ID != "thread-9" {
		t.Fatalf("orphan: %s", tilesOf(l))
	}
}

// /thread/children is in the parent's surface data: a part may bind it.
func TestChildrenBindable(t *testing.T) {
	c, _ := surfaceCore(t)
	parent := lightThread(t, c)
	if kids, ok := threadData(t, c, parent.ID)["children"].([]any); !ok || len(kids) != 0 {
		t.Fatalf("no children list: %v", threadData(t, c, parent.ID)["children"])
	}
	childThread(t, c, parent.ID, "Le devis", "topic:devis")
	who := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	_, err := c.writeThreadPart(who, parent.ID, surfaceWriteIn{ID: threadSurfaceID(parent.ID), Components: []map[string]any{
		{"id": "root", "component": "List", "children": map[string]any{"path": "/thread/children", "componentId": "kid"}},
		{"id": "kid", "component": "Text", "text": map[string]any{"@path": "title"}},
	}})
	if err != nil {
		t.Fatalf("a part bound to /thread/children: %v", err)
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
