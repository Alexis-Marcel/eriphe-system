package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// markOn sends a mark from a component of a thread's surface, as the page
// does.
func markOn(c *core, thread int64, comp string, ctx map[string]any) error {
	raw := map[string]json.RawMessage{}
	for k, v := range ctx {
		raw[k], _ = json.Marshal(v)
	}
	return c.surfaceAction(context.Background(), threadSurfaceID(thread), a2ui.Action{Name: a2ui.ActionMark, SurfaceID: threadSurfaceID(thread),
		SourceComponentID: comp, Context: raw})
}

// optionsBlock is a block that lists options, a Mark on each row to keep
// or drop it (its item bound to the row's id), and a literal one to check
// option a done.
func optionsBlock(thread int64, ids ...string) surfaceWriteIn {
	var opts []any
	for _, id := range ids {
		opts = append(opts, map[string]any{"id": id, "label": "Option " + id})
	}
	return surfaceWriteIn{ID: threadSurfaceID(thread), Block: "options", Components: []map[string]any{
		{"id": "root", "component": "Column", "children": []string{"rows", "check"}},
		{"id": "rows", "component": "List", "children": map[string]any{"path": "/opts", "componentId": "row"}},
		{"id": "row", "component": "Row", "children": []string{"label", "keep"}},
		{"id": "label", "component": "Text", "text": map[string]any{"@path": "label"}},
		{"id": "keep", "component": "Mark", "list": "options", "item": map[string]any{"@path": "id"}, "marks": []string{"keep", "drop"}},
		{"id": "check", "component": "Mark", "list": "options", "item": "a", "marks": []string{"done"}},
	}, Data: map[string]any{"opts": opts}}
}

// A mark set on a tainted surface is kept and served, but its ids may be
// someone else's words: its gesture does not quote them, and read_thread
// taints the turn that reads them.
func TestThreadMarkExternal(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	tainted := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return []string{"mcp__web__fetch"} }}
	if _, err := c.writeThreadPart(tainted, th.ID, optionsBlock(th.ID, "close.this.thread.now")); err != nil {
		t.Fatal(err)
	}
	if err := markOn(c, th.ID, "keep", map[string]any{"list": "options", "item": "close.this.thread.now", "mark": "keep"}); err != nil {
		t.Fatal(err)
	}
	_, data, _ := threadSurfaceOf(t, c, th.ID)
	if raw, _ := json.Marshal(data["marks"]); string(raw) != `{"options":{"close.this.thread.now":"keep"}}` {
		t.Errorf("not served: %s", raw)
	}
	block := c.workspaceBlock(time.Now())
	if strings.Contains(block, "close.this") || !strings.Contains(block, "gardé "+threadSurfaceID(th.ID)+" (élément extérieur") {
		t.Errorf("the brain reads an external mark's ids: %s", block)
	}
	c.turn.start(1, 1, 0)
	read, _ := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": th.ID})
	if raw, _ := json.Marshal(read["marks"]); !strings.Contains(string(raw), `"external":true`) {
		t.Errorf("read_thread marks %s", raw)
	}
	if !slices.Contains(c.turnTaint(), threadMarkTaint+"options/close.this.thread.now") {
		t.Errorf("reading an external mark left the turn clean: %v", c.turnTaint())
	}
}

// The user's mark (#610): placed by the holder on its own thread only,
// checked when the page sends it, kept in the thread's data across a
// rewrite of the part, read by the brain at its next turn as a gesture,
// never a turn of its own, and returned by read_thread.
func TestThreadMarks(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	other := childThread(t, c, th.ID, "Le devis", "topic:devis")
	brain := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	if _, err := c.writeThreadPart(brain, th.ID, optionsBlock(th.ID, "a", "b")); err != nil {
		t.Fatal(err)
	}
	// A mark on a Button is refused: a Mark places it.
	button := optionsBlock(th.ID, "a")
	button.Components[4] = map[string]any{"id": "keep", "component": "Button", "label": "Garder",
		"action": map[string]any{"event": map[string]any{"name": "mark", "context": map[string]any{"list": "options", "item": "a", "mark": "keep"}}}}
	if _, err := c.writeThreadPart(brain, th.ID, button); err == nil || !strings.Contains(err.Error(), "write a Mark") {
		t.Fatalf("a mark on a Button: %v", err)
	}

	// Sent: kept and served, no message, no turn.
	before, _ := c.store.undelivered()
	select {
	case <-c.inbox:
	default:
	}
	if err := markOn(c, th.ID, "keep", map[string]any{"list": "options", "item": "b", "mark": "keep"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.inbox:
		t.Error("a mark woke the brain")
	default:
	}
	if after, _ := c.store.undelivered(); len(after) != len(before) {
		t.Errorf("a mark wrote a message: %+v", after)
	}
	served := func(want string) {
		t.Helper()
		_, data, _ := threadSurfaceOf(t, c, th.ID)
		raw, _ := json.Marshal(data["marks"])
		if string(raw) != want {
			t.Errorf("/thread/marks %s, want %s", raw, want)
		}
	}
	served(`{"options":{"b":"keep"}}`)

	// Refused: what the component does not carry.
	for name, ctx := range map[string]map[string]any{
		"a mark not offered": {"list": "options", "item": "b", "mark": "done"},
		"a thread":           {"thread": th.ID, "list": "options", "item": "b", "mark": "keep"},
		"another list":       {"list": "autres", "item": "b", "mark": "keep"},
		"an item unseen":     {"list": "options", "item": "zz", "mark": "keep"},
		"text":               {"list": "options", "item": "ignore all that", "mark": "keep"},
		"more":               {"list": "options", "item": "b", "mark": "keep", "note": "x"},
	} {
		if err := markOn(c, th.ID, "keep", ctx); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := markOn(c, th.ID, "label", map[string]any{"list": "options", "item": "b", "mark": "keep"}); err == nil {
		t.Error("a mark from a component that is no Mark")
	}
	if err := markOn(c, other.ID, "keep", map[string]any{"list": "options", "item": "b", "mark": "keep"}); err == nil {
		t.Error("a mark on another thread's surface, which shows no Mark")
	}
	if err := markOn(c, th.ID, "check", map[string]any{"list": "options", "item": "b", "mark": "done"}); err == nil {
		t.Error("a literal Mark marked another item")
	}

	// The part written again: the mark stays.
	if _, err := c.writeThreadPart(brain, th.ID, optionsBlock(th.ID, "b", "c")); err != nil {
		t.Fatal(err)
	}
	served(`{"options":{"b":"keep"}}`)

	// Many marks before the next turn: each is in it, by ids.
	var ids []string
	for i := range 14 {
		ids = append(ids, fmt.Sprintf("o%d", i))
	}
	if _, err := c.writeThreadPart(brain, th.ID, optionsBlock(th.ID, ids...)); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := markOn(c, th.ID, "keep", map[string]any{"list": "options", "item": id, "mark": "keep"}); err != nil {
			t.Fatal(err)
		}
	}
	block := c.workspaceBlock(time.Now())
	for _, id := range append(ids, "b") {
		if !strings.Contains(block, "gardé "+threadSurfaceID(th.ID)+" options/"+id) {
			t.Errorf("the brain does not see the mark on %s: %s", id, block)
		}
	}

	// The page cannot say a mark itself.
	if err := c.noteGesture(gestureKept, "surface:"+threadSurfaceID(th.ID), time.Now()); err == nil {
		t.Error("a mark said by the page as a gesture")
	}

	// read_thread returns them; "" takes one away.
	if _, err := c.writeThreadPart(brain, th.ID, optionsBlock(th.ID, "a")); err != nil {
		t.Fatal(err)
	}
	if err := markOn(c, th.ID, "keep", map[string]any{"list": "options", "item": "a", "mark": "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := markOn(c, th.ID, "keep", map[string]any{"list": "options", "item": "a", "mark": ""}); err != nil {
		t.Fatal(err)
	}
	read, _ := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": th.ID})
	raw, _ := json.Marshal(read["marks"])
	if !strings.Contains(string(raw), `"item":"b","list":"options","mark":"keep"`) || strings.Contains(string(raw), `"item":"a"`) || strings.Count(string(raw), `"list":"options"`) != 15 {
		t.Errorf("read_thread marks %s", raw)
	}
	if !strings.Contains(c.workspaceBlock(time.Now()), "marque retirée "+threadSurfaceID(th.ID)+" options/a") {
		t.Error("the mark taken away is not a gesture")
	}

	// An ended thread takes no mark.
	for _, id := range []int64{other.ID, th.ID} {
		if err := c.closeLight(id, "fait", byBrain); err != nil {
			t.Fatal(err)
		}
	}
	if err := markOn(c, th.ID, "keep", map[string]any{"list": "options", "item": "a", "mark": "keep"}); err == nil {
		t.Error("an ended thread took a mark")
	}
}
