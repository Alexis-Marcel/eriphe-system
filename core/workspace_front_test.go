package main

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// workspace_focus puts a surface on stage only in the turn that answers the
// user in the workspace, once; the layout says Eriphe put it there, and the
// brain is not asked for a line about its own surface.
func TestWorkspaceFocusTurn(t *testing.T) {
	c := focusCore(t)
	brain := connectServer(t, newMCPServer(c))
	focus := func(id string) (map[string]any, string) {
		return callOn(t, brain, "workspace_focus", map[string]any{"id": id})
	}

	// Not answering the user in the workspace: refused, nothing moves.
	if _, msg := focus("agenda"); !strings.Contains(msg, "only in your answer to a message the user wrote in the workspace") {
		t.Fatalf("outside a workspace turn: %q", msg)
	}
	if l := c.currentLayout(); l.Focus != "" {
		t.Fatalf("focused outside a workspace turn: %q", l.Focus)
	}

	c.front.begin(true)
	if _, msg := focus("nowhere"); !strings.Contains(msg, "no such surface") {
		t.Fatalf("an unknown surface: %q", msg)
	}
	out, msg := focus("agenda")
	if msg != "" || out["result"] != "in front" {
		t.Fatalf("focus: %v %q", out, msg)
	}
	l := c.currentLayout()
	if l.Focus != "agenda" || l.FocusBy != focusByBrain || l.Previous != "" {
		t.Fatalf("layout: focus %q by %q previous %q", l.Focus, l.FocusBy, l.Previous)
	}
	if tile, _ := tileOf(l, "agenda"); sizeRank[tile.Size] < sizeRank[sizeL] {
		t.Fatalf("not large: %s", tilesOf(l))
	}
	// Its own surface: the brain is asked no line about it.
	c.settleFocus(time.Now())
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 {
		t.Fatalf("asked about its own surface: %+v", msgs)
	}
	if snap := c.poolSnapshot(momentFocus, ""); !strings.Contains(composerTurn(snap), "Au centre : agenda") {
		t.Fatalf("the composer reads it as the user's: %s", composerTurn(snap))
	}
	if ws := c.workspaceBlock(time.Now()); !strings.Contains(ws, "agenda « Agenda » · xl en 0,0 · au centre, mise par toi") &&
		!strings.Contains(ws, "· au centre, mise par toi") {
		t.Fatalf("the brain's next turn: %s", ws)
	}

	// Once per turn: the same again is fine, another is refused.
	if out, msg := focus("agenda"); msg != "" || out["result"] != "already in front" {
		t.Fatalf("again: %v %q", out, msg)
	}
	if _, msg := focus("soldes"); !strings.Contains(msg, "one surface in front per turn: agenda already is") {
		t.Fatalf("a second: %q", msg)
	}

	// A turn on events alone: refused again.
	c.front.begin(false)
	if _, msg := focus("soldes"); !strings.Contains(msg, "only in your answer") {
		t.Fatalf("on events: %q", msg)
	}
}

// The stage: opening a surface puts it in the center; what was there stays
// next to it, reduced, never folded; the brain's surface takes the stage
// the same way, and a request about the one it replaced is withdrawn.
// Back ends the stage.
func TestWorkspaceStage(t *testing.T) {
	c := focusCore(t)
	now := time.Now()
	c.focusSurface("agenda", now)
	c.settleFocus(now)
	if msgs, _ := c.store.undelivered(); len(msgs) != 1 {
		t.Fatalf("the user's open asks the brain: %+v", msgs)
	}

	// The brain answers on stage: agenda stays next to it, reduced.
	c.front.begin(true)
	if _, err := c.focusFromBrain(workspaceFocusIn{ID: "soldes"}, now); err != nil {
		t.Fatal(err)
	}
	l := c.currentLayout()
	if l.Focus != "soldes" || l.FocusBy != focusByBrain || l.Previous != "agenda" {
		t.Fatalf("stage: focus %q by %q previous %q", l.Focus, l.FocusBy, l.Previous)
	}
	prev, ok := tileOf(l, "agenda")
	if !ok || sizeRank[prev.Size] > sizeRank[sizeM] {
		t.Fatalf("the previous one is not a reduced tile: %s", tilesOf(l))
	}
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 {
		t.Fatalf("the request about agenda is still waiting: %+v", msgs)
	}
	if snap := c.poolSnapshot(momentFocus, ""); snap.Previous != "agenda" || !strings.Contains(composerTurn(snap), "Au centre juste avant : agenda") {
		t.Fatalf("the composer does not know the previous: %q", snap.Previous)
	}
	if ws := c.workspaceBlock(now); !strings.Contains(ws, "· au centre juste avant") {
		t.Fatalf("the brain does not read the previous: %s", ws)
	}

	// The user clicks agenda: it takes the stage, soldes stays next to it.
	c.focusSurface("agenda", now.Add(time.Second))
	l = c.currentLayout()
	if l.Focus != "agenda" || l.FocusBy != "" || l.Previous != "soldes" {
		t.Fatalf("swap: focus %q by %q previous %q", l.Focus, l.FocusBy, l.Previous)
	}
	if _, ok := tileOf(l, "soldes"); !ok {
		t.Fatalf("the previous one folded: %s", tilesOf(l))
	}

	// Back ends the stage: no previous left.
	c.focusSurface("", now.Add(2*time.Second))
	c.settleFocus(now.Add(2 * time.Second))
	if l := c.currentLayout(); l.Focus != "" || l.Previous != "" || l.FocusBy != "" {
		t.Fatalf("back: focus %q previous %q by %q", l.Focus, l.Previous, l.FocusBy)
	}
	c.focusSurface("soldes", now.Add(3*time.Second))
	if l := c.currentLayout(); l.Previous != "" {
		t.Fatalf("a stage after back keeps an old previous: %q", l.Previous)
	}
}

// A message typed in the workspace is marked for the brain, even with no
// surface open, and makes its turn one that may answer on stage; one from
// the conversation, an event or a channel does not.
func TestFromWorkspace(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	c := tw.c
	if code, _, _ := tw.post("/messages", url.Values{"text": {"Où en est le projet ?"}, "from": {"workspace"}}, csrf); code != 204 {
		t.Fatalf("send: %d", code)
	}
	if code, _, _ := tw.post("/messages", url.Values{"text": {"Et demain ?"}}, csrf); code != 204 {
		t.Fatalf("send: %d", code)
	}
	msgs, _ := c.store.undelivered()
	if len(msgs) != 2 {
		t.Fatalf("messages %+v", msgs)
	}
	if got := c.messageRef(msgs[0]); !strings.HasSuffix(got, " · depuis l'espace") {
		t.Fatalf("the brain reads %q", got)
	}
	if got := c.messageRef(msgs[1]); strings.Contains(got, "espace") {
		t.Fatalf("a conversation's message reads %q", got)
	}
	if !c.frontFor(msgs) || c.frontFor(msgs[1:]) {
		t.Fatal("frontFor")
	}
	ev, _ := c.store.addMessage(roleEvent, "Une tâche a fini.", false)
	if c.frontFor([]Message{{Seq: int(ev), Role: roleEvent}}) {
		t.Fatal("an event is from the workspace")
	}
	// With a surface open: both marks, in that order.
	seq, _ := c.store.addUserMessage("Et ça ?", nil)
	c.noteFromWorkspace(seq)
	writeTestSurface(t, c, surfaceIn{ID: "notes", Title: "Notes"})
	c.noteAbout(seq, "surface:notes")
	m := Message{Seq: int(seq), Role: roleUser}
	if got := c.messageRef(m); !strings.Contains(got, "· depuis l'espace · à propos de surface:notes") {
		t.Fatalf("the brain reads %q", got)
	}
}

// Through a real delivery: a message typed in the workspace opens the
// turn to one surface on stage; a delivery of events only, which goes on
// with that turn, closes it; a message from the conversation too.
func TestFrontTurnDelivery(t *testing.T) {
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	writeTestSurface(t, c, surfaceIn{ID: "projet", Title: "Projet"})
	deliver := func() {
		t.Helper()
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
		if err := tendBrain(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	focus := func() error {
		c.front.mu.Lock()
		c.front.put = "" // each case asks once
		c.front.mu.Unlock()
		_, err := c.focusFromBrain(workspaceFocusIn{ID: "projet"}, time.Now())
		return err
	}
	seq, _ := c.store.addUserMessage("Où en est le projet ?", nil)
	c.noteFromWorkspace(seq)
	deliver()
	items, _ := c.store.inbox("t-brain")
	if len(items) == 0 || !strings.Contains(items[len(items)-1].Data["text"].(string), "· depuis l'espace] Où en est le projet ?") {
		t.Fatalf("delivered: %+v", items)
	}
	if err := focus(); err != nil {
		t.Fatalf("the workspace turn: %v", err)
	}
	c.store.addMessage(roleEvent, "Une tâche a fini.", false)
	deliver()
	if err := focus(); err == nil {
		t.Fatal("a delivery of events only may put a surface on stage")
	}
	c.store.addUserMessage("Et demain ?", nil)
	deliver()
	if err := focus(); err == nil {
		t.Fatal("a message from the conversation may put a surface on stage")
	}
}

// What the brain proposes in a turn is in workspace_read's proposals in
// that same turn, ready for a Proposal.
func TestFrontProposalListed(t *testing.T) {
	c, _ := surfaceCore(t)
	c.store.insertTask(&TaskRow{ID: "t-brain", Runtime: "claude-code", State: stateRunning})
	id := c.newAccessAsk(accessQuestion{TaskID: "t-brain", Permission: "p1", Need: accessNeed{Server: "code", Category: "write"},
		Call: "mcp__code__merge {}", Input: `{"number":42}`})
	if id == 0 {
		t.Fatal("no access card")
	}
	raw, _ := json.Marshal(c.toolWorkspaceRead(surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}))
	ref := proposalRef(cardAccess, id)
	if !strings.Contains(string(raw), `"ref":"`+ref+`"`) {
		t.Fatalf("workspace_read lacks the proposal: %s", raw)
	}
	// The brain's answer embedding it relates to the queue's surface code
	// wrote for that card, where its Oui and Non always answer.
	c.syncQueueSurfaces()
	writeTestSurface(t, c, surfaceIn{ID: "pr-42", Title: "PR 42", Data: json.RawMessage(`{"p":"` + ref + `"}`),
		Components: json.RawMessage(`[{"id":"root","component":"Proposal","proposalId":{"@path":"/p"}}]`)})
	pool, _ := c.surfaces()
	rel := c.focusRelations("pr-42", pool)
	if len(rel) == 0 || !strings.HasPrefix(rel[0].ID, queueSurfacePrefix) || rel[0].words() != "même proposition" {
		t.Fatalf("relations %+v", rel)
	}
}

// A surface that shows a proposal relates to the queue's tile of the same
// proposal: on stage, the answerable tile is placed next to it.
func TestProposalRelates(t *testing.T) {
	brainView := surface{ID: "pr", Author: surfaceAuthorBrain, Data: json.RawMessage(`{"p":"access:4"}`),
		Components: json.RawMessage(`[{"id":"root","component":"Proposal","proposalId":{"@path":"/p"}}]`)}
	queue := surface{ID: queueSurfacePrefix + "card-9", Author: surfaceAuthorCore,
		Components: json.RawMessage(`[{"id":"root","component":"Proposal","proposalId":"access:4"}]`)}
	other := surface{ID: "q-other", Author: surfaceAuthorCore,
		Components: json.RawMessage(`[{"id":"root","component":"Proposal","proposalId":"rule:2"}]`)}
	got := surfaceRelations(brainView, []surface{brainView, queue, other}, nil)
	if len(got) != 1 || got[0].ID != queue.ID || got[0].Kind != relRef || got[0].words() != "même proposition" {
		t.Fatalf("relations %+v", got)
	}
	// A group of the queue: a list whose template shows each proposal.
	group := surface{ID: "q-decide", Author: surfaceAuthorCore, Data: json.RawMessage(`{"items":[{"ref":"rule:2"},{"ref":"access:4"}]}`),
		Components: json.RawMessage(`[{"id":"root","component":"List","children":{"path":"/items","componentId":"item"}},` +
			`{"id":"item","component":"Proposal","proposalId":{"@path":"ref"}}]`)}
	if got := proposalsIn(group); len(got) != 2 || got[1] != "access:4" {
		t.Fatalf("a group shows %v", got)
	}
	if got := surfaceRelations(brainView, []surface{brainView, group}, nil); len(got) != 1 || got[0].ID != "q-decide" {
		t.Fatalf("relations to a group %+v", got)
	}
}

// A call approved on its card shows running, then done or failed with a
// reason of ours, and its proposal moves at each step.
func TestCallState(t *testing.T) {
	c, _ := surfaceCore(t)
	_, from, _ := c.watchProposals(0)
	a := accessQuestion{ID: 7, Call: "mcp__nowhere__merge {}", Input: "{}"}
	text, _ := c.trackedCall(context.Background(), a, "mcp__nowhere__merge")
	if !strings.Contains(text, "n'a pas pu être exécutée") {
		t.Fatalf("told %q", text)
	}
	if state, why := c.callState(7); state != callFailed || why != callWhyGone {
		t.Fatalf("state %q why %q", state, why)
	}
	ev, last, _ := c.watchProposals(from)
	if len(ev.Refs) != 1 || ev.Refs[0] != "access:7" || last-from != 2 {
		t.Fatalf("moves %+v (%d)", ev, last-from)
	}
	c.setCallState(8, callDone)
	if state, why := c.callState(8); state != callDone || why != "" {
		t.Fatalf("done: %q %q", state, why)
	}
	if state, _ := c.callState(9); state != "" {
		t.Fatalf("not run: %q", state)
	}
}
