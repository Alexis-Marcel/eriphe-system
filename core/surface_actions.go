package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The actions the user takes on a surface (#425), POST /api/espace/action.
// They follow a closed grammar (a2ui.ActionKind), checked here against the
// surface as it is stored, never against what the page says of it:
//
//   - ask: the text written on the clicked component (a Button whose
//     action is ask) becomes a message of the user's to the brain, as if
//     typed, about the surface (about = surface:<id>) and marked in the
//     conversation as sent from the workspace. The brain is never waited
//     for. On a tainted surface the message taints the brain's turn: what
//     the surface shows is read as data, never as a request.
//   - answer: yes or no to the one proposal a Proposal component of the
//     surface shows, decided like its card's Oui or Non; refused on a
//     tainted surface, unless code wrote it (the attention queue's, whose
//     Proposal shows the proposal's own taint, as its card does).
//   - local (open, back, expand, tab): recorded; "open" (a tile or a chip)
//     places the surface first on the layout, keeps the gesture and reads
//     its sources again, in the background; "back" gives the layout back.
//
// Anything else is refused and reported to the surface's author
// (surfaceError). No action calls a tool that writes.

const (
	// viaWorkspaceAsk is the Via of a message an ask sent: the
	// conversation shows it with its own words (Message.FromWorkspace).
	viaWorkspaceAsk = "workspace:ask"
	// surfaceTaintPrefix is the taint of an ask sent from a tainted surface.
	surfaceTaintPrefix = "surface:"
)

// errActionRefused is an action outside the grammar, or one the surface
// does not carry: 400, reported to its author.
type errActionRefused struct {
	Path    string
	Message string
}

func (e *errActionRefused) Error() string { return e.Message }

// surfaceAction is an action the user took on a surface: checked, recorded
// on it, then done.
func (c *core) surfaceAction(ctx context.Context, id string, a a2ui.Action) error {
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	var text, ref string
	var yes bool
	var refused *errActionRefused
	switch a2ui.ActionKind(a.Name) {
	case a2ui.KindAsk:
		text, refused = askOf(s, a)
	case a2ui.KindAnswer:
		ref, yes, refused = c.answerOf(s, a)
	case a2ui.KindLocal:
	default:
		refused = &errActionRefused{Message: fmt.Sprintf("no action %q: only ask, answer (a Proposal's) and %s", a.Name, strings.Join(a2ui.LocalActions, ", "))}
	}
	if refused != nil {
		refused.Message = "the user's action " + strconv.Quote(truncate(a.Name, 64)) + " on " + truncate(a.SourceComponentID, 64) + " was refused: " + refused.Message
		c.surfaceError(id, a2ui.Error{Code: "ACTION_REFUSED", SurfaceID: id, Path: refused.Path, Message: refused.Message})
		return refused
	}
	now := time.Now()
	err = c.noteOnSurface(id, func(l *surfaceLook) {
		l.Action = &surfaceActed{Name: a.Name, Component: a.SourceComponentID, At: now.Unix()}
	})
	if err != nil {
		return err
	}
	switch a2ui.ActionKind(a.Name) {
	case a2ui.KindAsk:
		return c.askFromSurface(s, text)
	case a2ui.KindAnswer:
		return c.answerFromSurface(ctx, ref, yes)
	}
	switch a.Name {
	case a2ui.ActionOpen:
		// Code places what the user opened first (workspace_layout.go),
		// and keeps the gesture for the composer and memory.
		c.focusSurface(id, now)
		c.noteGesture(gestureOpened, "surface:"+id, now)
		go c.surfaceOpened(context.WithoutCancel(ctx), id)
	case a2ui.ActionBack:
		if c.workspaceFocus(now) == id {
			c.focusSurface("", now)
		}
	}
	return nil
}

// componentOf is a component of a surface by its id, and where it is.
func componentOf(s surface, id string) (map[string]any, string, bool) {
	var list []map[string]any
	json.Unmarshal(s.Components, &list)
	for i, comp := range list {
		if cid, _ := comp["id"].(string); cid == id && id != "" {
			return comp, "/components/" + strconv.Itoa(i), true
		}
	}
	return nil, "", false
}

// askOf is the text an ask sends: the one its author wrote on the clicked
// component, whatever the page sent as its context.
func askOf(s surface, a a2ui.Action) (string, *errActionRefused) {
	_, at, ok := componentOf(s, a.SourceComponentID)
	if !ok {
		return "", &errActionRefused{Message: "the surface has no component " + strconv.Quote(a.SourceComponentID)}
	}
	text, ok := a2ui.AskText(s.Components, a.SourceComponentID)
	if !ok {
		return "", &errActionRefused{Path: at + "/action", Message: "this component carries no ask"}
	}
	return truncate(text, a2ui.AskTextMax), nil
}

// answerOf is the proposal an answer decides, and how: the one the
// clicked Proposal component shows, on a surface that is not tainted.
func (c *core) answerOf(s surface, a a2ui.Action) (string, bool, *errActionRefused) {
	if s.Tainted != "" && s.Author != surfaceAuthorCore {
		return "", false, &errActionRefused{Message: "the surface shows external content: it can only ask, a proposal is answered in the conversation"}
	}
	comp, at, ok := componentOf(s, a.SourceComponentID)
	if !ok || comp["component"] != "Proposal" {
		return "", false, &errActionRefused{Path: at, Message: "an answer comes from a Proposal component of the surface"}
	}
	var ref, answer string
	json.Unmarshal(a.Context["proposal"], &ref)
	json.Unmarshal(a.Context["answer"], &answer)
	if answer != "yes" && answer != "no" || len(a.Context) != 2 {
		return "", false, &errActionRefused{Path: at, Message: `an answer's context is {"proposal": ref, "answer": "yes"|"no"}`}
	}
	for _, shown := range c.proposalsShown(s, a.SourceComponentID) {
		if shown == ref {
			return ref, answer == "yes", nil
		}
	}
	return "", false, &errActionRefused{Path: at + "/proposalId", Message: "this Proposal does not show " + strconv.Quote(truncate(ref, 100))}
}

// proposalsShown are the refs a Proposal component of a surface shows: its
// proposalId as written, or each value it is bound to in the data.
func (c *core) proposalsShown(s surface, id string) []string {
	comp, _, _ := componentOf(s, id)
	if ref, ok := comp["proposalId"].(string); ok {
		return []string{ref}
	}
	var data any
	json.Unmarshal(s.Data, &data)
	var out []string
	for _, b := range a2ui.Eriphe().BoundPaths(s.Components) {
		if b.Component == id && b.Prop == "proposalId" && !b.List {
			for _, v := range valuesAt(data, tokens(b.Path)) {
				if ref, ok := v.(string); ok {
					out = append(out, ref)
				}
			}
		}
	}
	return out
}

// valuesAt reads the values at a path, "*" for each item of a list.
func valuesAt(v any, toks []string) []any {
	if len(toks) == 0 {
		return []any{v}
	}
	t, rest := toks[0], toks[1:]
	switch x := v.(type) {
	case map[string]any:
		if next, ok := x[t]; ok {
			return valuesAt(next, rest)
		}
	case []any:
		if t == "*" {
			var out []any
			for _, next := range x {
				out = append(out, valuesAt(next, rest)...)
			}
			return out
		}
		if i, err := strconv.Atoi(t); err == nil && i >= 0 && i < len(x) {
			return valuesAt(x[i], rest)
		}
	}
	return nil
}

// askFromSurface writes an ask as a message of the user's, about the
// surface, and wakes the brain; it does not wait for it.
func (c *core) askFromSurface(s surface, text string) error {
	seq, err := c.store.addMessageVia(roleUser, text, viaWorkspaceAsk, false)
	if err != nil {
		return err
	}
	c.store.set(aboutKey(seq), "surface:"+s.ID)
	if s.Tainted != "" {
		c.store.set(taintKey(int(seq)), surfaceTaintPrefix+s.ID)
	}
	log.Printf("surface %s: ask sent as message #%d", s.ID, seq)
	c.ping()
	return nil
}

// answerFromSurface decides a proposal like its card's Oui or Non:
// errNoCard when no card carries it, errCardDecided when it no longer
// waits.
func (c *core) answerFromSurface(ctx context.Context, ref string, yes bool) error {
	xs, err := c.cardsWhere(`ref = ?`, ref)
	if err != nil || len(xs) == 0 {
		return errNoCard
	}
	_, err = c.decideCard(ctx, xs[0].ID, yes, "workspace")
	if err != nil && !errors.Is(err, errNoCard) && !errors.Is(err, errCardDecided) {
		log.Printf("surface answer %s: %v", ref, err)
	}
	return err
}

// FromWorkspace tells whether a message of the user's was sent by a
// button of the workspace (an ask).
func (m Message) FromWorkspace() bool { return m.Role == roleUser && m.Via == viaWorkspaceAsk }
