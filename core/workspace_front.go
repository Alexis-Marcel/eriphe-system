package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An answer on stage (#458). The workspace is one dashboard with a stage:
// one surface in the center, large, and around it smaller tiles that ask
// for attention. Opening a tile puts it on stage; the one that was there
// stays next to it, reduced, live (layoutStage: the layout's previous),
// never folded for the others; nothing "goes back". When the user asks
// from the workspace about a thing (« où en est … ? »), the brain answers
// with a surface and puts it on stage, as the user opening it would
// (workspace_focus.go); its text reply is one line pointing to it.
//
//   - From the workspace: a message typed in the workspace's input is
//     marked (fromWorkspaceKey), even with no surface open, and the brain
//     reads it « · depuis l'espace »; a button's ask is from it too.
//   - workspace_focus, the brain's only: in the turn that answers such a
//     message of the user's, never on events alone (a turn going on over a
//     task's end is not an answer to them), and once per turn (frontTurn).
//     It is display-only: it puts one surface that is already there on
//     stage, writes nothing, sends nothing anywhere, and the user's next
//     click takes the stage back; so it is allowed in a tainted turn too
//     (what the surface shows is marked as it always is, and its Proposal
//     follows the #443 rules).
//   - The layout says who put it there (focusBy): the page and the brain's
//     next turn know Eriphe did, not the user; the brain is not asked for a
//     line about its own surface (focusAskBrain).
//   - Back (the page's) ends the stage: the base composition comes back.

const (
	focusByBrain = placedByBrain // the layout's focusBy: Eriphe put it in front
	viaWorkspace = "workspace"   // POST /messages' from: typed in the workspace's input
)

func fromWorkspaceKey(seq int64) string { return fmt.Sprintf("from_workspace:%d", seq) }

// noteFromWorkspace marks a message of the user's as typed in the
// workspace.
func (c *core) noteFromWorkspace(seq int64) { c.store.set(fromWorkspaceKey(seq), "1") }

// typedInWorkspace tells whether a message of the user's was typed in the
// workspace's input.
func (c *core) typedInWorkspace(m Message) bool {
	return m.Role == roleUser && c.store.get(fromWorkspaceKey(int64(m.Seq))) == "1"
}

// inWorkspace tells whether a message is the user's own, written or sent by
// a button in the workspace.
func (c *core) inWorkspace(m Message) bool {
	return m.Role == roleUser && (m.FromWorkspace() || c.typedInWorkspace(m)) && !c.fromUntrusted(m.Seq)
}

// frontTurn is whether the brain's current turn answers the user in the
// workspace, and what it put in front: set at each delivery.
type frontTurn struct {
	mu        sync.Mutex
	workspace bool
	put       string
}

// begin starts a turn: an answer to the user in the workspace or not.
func (f *frontTurn) begin(workspace bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspace, f.put = workspace, ""
}

var (
	errFrontNotHere = errors.New("only in your answer to a message the user wrote in the workspace (« · depuis l'espace », « · bouton de l'espace »): elsewhere what is in front is the user's and the composer's; write the surface, the composer places it")
	errFrontOnce    = errors.New("one surface in front per turn")
)

// claim takes the turn's one surface in front.
func (f *frontTurn) claim(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case !f.workspace:
		return errFrontNotHere
	case f.put != "" && f.put != id:
		return fmt.Errorf("%w: %s already is", errFrontOnce, f.put)
	}
	f.put = id
	return nil
}

// frontFor tells whether the messages delivered in one turn are an answer
// to the user in the workspace.
func (c *core) frontFor(msgs []Message) bool {
	for _, m := range msgs {
		if c.inWorkspace(m) {
			return true
		}
	}
	return false
}

// setFocus is focusSurface, by the user ("") or by the brain: what was on
// stage before stays next to it, reduced (layoutStage); the brain is asked
// no line about a surface it put on stage itself.
func (c *core) setFocus(id string, now time.Time, by string) {
	st := &c.layout
	st.mu.Lock()
	was := st.focus
	if was != "" && now.Sub(st.focusAt) > focusFor {
		was = ""
	}
	var withdraw *focusAsk
	if id != was {
		st.f.prev = was // "" when the stage was empty, or ends
		if id == "" {
			st.f.prev = ""
		}
	}
	st.focus, st.focusAt, st.f.by = id, now, by
	if id != was {
		st.focusSince = now
		if id == "" {
			st.focusSince = time.Time{}
		}
		c.focusChangedLocked(was)
		if by == focusByBrain {
			st.f.asked = focusKey(id, st.focusSince) // its own: no line asked about it
			withdraw, st.f.ask = st.f.ask, nil
		}
	}
	if by == focusByBrain {
		c.loadPlacementLocked()
		if _, ok := st.user.Closed[id]; ok {
			delete(st.user.Closed, id) // asked for: shown again
			c.savePlacementLocked()
		}
	}
	st.mu.Unlock()
	if withdraw != nil {
		c.withdrawFocusAsk(withdraw)
	}
	if id != "" && by == "" {
		c.openedOnWorkspace(id, now) // no longer closed; a queue item read (workspace_gestures.go)
	}
	c.relayout()
}

// stageNow is who put the stage's surface there (focusByBrain, or "" for
// the user) and what was on stage before it.
func (c *core) stageNow(now time.Time) (by, previous string) {
	if c.workspaceFocus(now) == "" {
		return "", ""
	}
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.f.by, st.f.prev
}

// The brain's tool.

const workspaceFocusHelp = "Put a surface on the stage of the user's workspace, as if they had opened it: in the center, large, the workspace composed around it; what was on stage stays next to it, smaller. Only in your answer to a message the user wrote in the workspace (« · depuis l'espace », « · bouton de l'espace »), once per turn: when they ask about a thing (where a pull request, a project, a session stands), write its surface, then put it on stage, and answer in one line. Not over a surface they have open, unless they asked about something else. Display only: it writes nothing."

type workspaceFocusIn struct {
	ID string `json:"id" jsonschema:"the surface to put in front, as surface_write or workspace_read gives its id"`
}

// focusFromBrain is workspace_focus: the surface in front, in the turn
// that answers the user in the workspace, once.
func (c *core) focusFromBrain(in workspaceFocusIn, now time.Time) (map[string]any, error) {
	id := strings.TrimSpace(in.ID)
	if _, err := c.surface(id); err != nil {
		return nil, fmt.Errorf("%q: no such surface on the workspace (write it first, or read it again with workspace_read)", in.ID)
	}
	if err := c.front.claim(id); err != nil {
		return nil, err
	}
	out := map[string]any{"result": "in front", "id": id}
	was := c.workspaceFocus(now)
	if was == id {
		out["result"] = "already in front"
		return out, nil
	}
	c.setFocus(id, now, focusByBrain)
	if was != "" {
		out["previous"] = was
		out["note"] = was + " stays next to it, smaller. Now answer in one line that points to it."
	} else {
		out["note"] = "Now answer in one line that points to it."
	}
	return out, nil
}

func registerWorkspaceFocus(s *mcp.Server, c *core) {
	tool(s, "workspace_focus", workspaceFocusHelp,
		func(ctx context.Context, in workspaceFocusIn) (map[string]any, error) {
			return c.focusFromBrain(in, time.Now())
		})
}
