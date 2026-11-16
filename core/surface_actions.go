package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
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
//     Proposal shows the proposal's own taint, as its card does; a
//     thread's, whose Proposals code bound to its own cards, whatever
//     the thread read: surfaceLook.Answers). A Proposal bound inside a
//     list answers only the proposal at the place it says (at): a row's
//     card from its row (item_cards.go).
//   - stepDone: « C'est fait » on a thread's surface, for a step the user
//     does: the Button code wrote there, its thread and step as stored
//     (thread_surface.go, settleUserStep).
//   - tidy: « Ranger » on an ended thread's surface: the Button code wrote
//     there, its thread as stored (thread_surface.go, tidyThread).
//   - choose, control: an option of a thread's choice step, Pause, Resume
//     or Stop: the Buttons code wrote on its surface, their context as
//     stored (thread_surface.go, chooseOf, controlOf).
//   - offer: an action offered on an item of a thread's source, or on a
//     group of them: the Button code wrote on its surface; the note and
//     its action read again from the store, it makes one card bound to
//     the item(s) and runs nothing (item_offers.go).
//   - mark: keep, drop or done on something a thread shows, from a
//     component of the thread's own surface that carries it (its holder's
//     block): its thread and mark as stored, its list and item written
//     there or taken from the data where its binding reads; recorded in
//     the thread's data, a gesture for the brain's next turn, never a
//     turn of its own (thread_marks.go).
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
	var text, ref, step, option, do, source, item, state, offer, list, mark string
	var yes, external bool
	var thread int64
	var refused *errActionRefused
	var offerCallsOf []offerCall
	switch a2ui.ActionKind(a.Name) {
	case a2ui.KindAsk:
		text, refused = askOf(s, a)
	case a2ui.KindAnswer:
		ref, yes, refused = c.answerOf(s, a)
	case a2ui.KindStep:
		thread, step, refused = c.stepDoneOf(s, a) // thread_surface.go
	case a2ui.KindTidy:
		thread, refused = tidyOf(s, a) // thread_surface.go
	case a2ui.KindChoose:
		thread, step, option, refused = c.chooseOf(s, a)
	case a2ui.KindControl:
		thread, do, refused = controlOf(s, a)
	case a2ui.KindOffer:
		thread, source, item, state, offer, refused = offerOf(s, a) // item_offers.go
		var calls []offerCall
		if refused == nil {
			calls, err = c.offerCalls(thread, source, item, state, offer)
			if err != nil {
				refused = &errActionRefused{Message: err.Error()}
			}
		}
		offerCallsOf = calls
	case a2ui.KindMark:
		thread, list, item, mark, external, refused = c.markOf(s, a) // thread_marks.go
	case a2ui.KindLocal:
	default:
		refused = &errActionRefused{Message: fmt.Sprintf("no action %q: only ask, answer (a Proposal's), stepDone, tidy, choose, control, offer and mark (a thread's) and %s", a.Name, strings.Join(a2ui.LocalActions, ", "))}
	}
	if refused != nil {
		refused.Message = "the user's action " + strconv.Quote(truncate(a.Name, 64)) + " on " + truncate(a.SourceComponentID, 64) + " was refused: " + refused.Message
		c.surfaceError(id, a2ui.Error{Code: "ACTION_REFUSED", SurfaceID: id, Path: refused.Path, Message: refused.Message})
		return refused
	}
	now := time.Now()
	if th, ok := threadOfSurface(id); ok && a.Name == a2ui.ActionOpen && s.Author == threadAuthor(th) {
		// A visit of the thread: its header tells what changed since the
		// one before (thread_header.go). Before the note, which a rewrite
		// of the surface would lose.
		c.threadVisited(th, now)
	}
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
	case a2ui.KindStep:
		return c.settleUserStep(ctx, thread, step)
	case a2ui.KindTidy:
		return c.tidyThread(thread, "user")
	case a2ui.KindChoose:
		_, err := c.chooseStep(ctx, thread, step, option, byUser)
		return err
	case a2ui.KindControl:
		return c.controlThread(ctx, thread, do)
	case a2ui.KindOffer:
		_, err := c.offer(ctx, thread, offerCallsOf)
		if errors.Is(err, errOfferAsked) {
			return nil // its card waits on the row already
		}
		return err
	case a2ui.KindMark:
		return c.setMark(thread, list, item, mark, external, now) // no turn: a gesture the brain reads at its next
	}
	switch a.Name {
	case a2ui.ActionOpen:
		// Code places what the user opened first (workspace_layout.go),
		// and keeps the gesture for memory.
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
	if s.Tainted != "" && s.Author != surfaceAuthorCore && !s.Look.Answers {
		return "", false, &errActionRefused{Message: "the surface shows external content: it can only ask, a proposal is answered in the conversation"}
	}
	comp, at, ok := componentOf(s, a.SourceComponentID)
	if !ok || comp["component"] != "Proposal" {
		return "", false, &errActionRefused{Path: at, Message: "an answer comes from a Proposal component of the surface"}
	}
	if s.Look.Answers && len(s.Look.AuthorTaint) > 0 && !strings.HasPrefix(a.SourceComponentID, threadCompPrefix) {
		return "", false, &errActionRefused{Path: at, Message: "a part written after reading external content can only ask: its proposal is answered in the conversation"}
	}
	var ref, answer, row string
	json.Unmarshal(a.Context["proposal"], &ref)
	json.Unmarshal(a.Context["answer"], &answer)
	_, hasRow := a.Context["at"]
	if hasRow && json.Unmarshal(a.Context["at"], &row) != nil {
		hasRow = false
	}
	keys := 2
	if hasRow {
		keys = 3
	}
	if answer != "yes" && answer != "no" || len(a.Context) != keys {
		return "", false, &errActionRefused{Path: at, Message: `an answer's context is {"proposal": ref, "answer": "yes"|"no"}, and "at": where its Proposal is in the data, for one in a list`}
	}
	shown, refused := c.proposalsShownAt(s, a.SourceComponentID, row, hasRow)
	if refused != "" {
		return "", false, &errActionRefused{Path: at + "/proposalId", Message: refused}
	}
	for _, x := range shown {
		if x == ref {
			return ref, answer == "yes", nil
		}
	}
	where := "this Proposal"
	if hasRow {
		where += " at " + strconv.Quote(truncate(row, 100))
	}
	return "", false, &errActionRefused{Path: at + "/proposalId", Message: where + " does not show " + strconv.Quote(truncate(ref, 100))}
}

// proposalsShownAt are the refs a Proposal component shows where the
// user clicked it: for one bound inside a list, the one at row (its place
// in the data, as the page gives it), checked to be one of the places its
// binding takes. A thread's surface names the row: there each row of
// items has its own cards (item_cards.go). refused says why not.
func (c *core) proposalsShownAt(s surface, id, row string, hasRow bool) ([]string, string) {
	comp, _, _ := componentOf(s, id)
	if ref, ok := comp["proposalId"].(string); ok {
		return []string{ref}, ""
	}
	binding, _ := comp["proposalId"].(map[string]any)
	path, _ := binding["@path"].(string)
	var places [][]string
	listed := false
	for _, b := range a2ui.Eriphe().BoundPaths(s.Components) {
		if b.Component == id && b.Prop == "proposalId" && !b.List {
			places = append(places, tokens(b.Path))
			listed = listed || slices.Contains(tokens(b.Path), "*")
		}
	}
	_, thread := threadOfSurface(s.ID)
	switch {
	case !hasRow && listed && thread:
		return nil, "an answer from a row of a thread's surface says where its Proposal is (at)"
	case !hasRow:
		return c.proposalsShown(s, id), ""
	case path == "":
		return nil, "this Proposal is bound to nothing"
	}
	concrete := path
	if !strings.HasPrefix(path, "/") {
		concrete = strings.TrimSuffix(row, "/") + "/" + path
	}
	toks := tokens(concrete)
	if !slices.ContainsFunc(places, func(p []string) bool { return placeOf(p, toks) }) {
		return nil, "this Proposal is not at " + strconv.Quote(truncate(row, 100))
	}
	var data any
	json.Unmarshal(s.Data, &data)
	var out []string
	for _, v := range valuesAt(data, toks) {
		if ref, ok := v.(string); ok {
			out = append(out, ref)
		}
	}
	return out, ""
}

// placeOf tells whether a path is one place of a pattern: the same
// tokens, an index of a list where the pattern says "*".
func placeOf(pattern, toks []string) bool {
	if len(pattern) != len(toks) {
		return false
	}
	for i, p := range pattern {
		if p == "*" {
			if n, err := strconv.Atoi(toks[i]); err != nil || n < 0 || toks[i] != strconv.Itoa(n) {
				return false
			}
			continue
		}
		if p != toks[i] {
			return false
		}
	}
	return true
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

// boundValues are where a component's property is bound (at: the
// component's place, "/components/<i>"), each path absolute through the
// templates it is rendered in, and the values those take in the surface's
// data.
func boundValues(s surface, at, prop string) (paths []string, values []any) {
	where := strings.TrimPrefix(at, "/components") + "/" + prop + "/@path"
	var data any
	json.Unmarshal(s.Data, &data)
	for _, b := range a2ui.Eriphe().BoundPaths(s.Components) {
		if b.At == where {
			paths = append(paths, b.Path)
			values = append(values, valuesAt(data, tokens(b.Path))...)
		}
	}
	return paths, values
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
	c.store.set(aboutMsgKey(seq), "surface:"+s.ID)
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
