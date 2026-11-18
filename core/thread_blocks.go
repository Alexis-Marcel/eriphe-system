package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// A thread's surface has a thin frame of code's (#611), the same for
// every thread, in one fixed order; a block shows only when the thread has
// what it shows, never by the kind of thread; how it ends is read twice: a
// standing one has no Stop, and the banner of an ended one:
//
//  1. head: a child's crumb back to its parent (blockParent), a Progress
//     (the state word in a pill, and a bar of its steps when it has some;
//     none for a calm thread with no steps), the state line
//     (threadStateText: its headline or its items' counts, when sorted),
//     and for an active thread Pause or Resume (one that runs steps) and
//     Stop; once it ended, a banner saying how, with « Ranger » for a
//     thread that ends on its goal (the only block that reads ends);
//  2. yours: every card, ask, choice and step of the user's that waits,
//     each once (threadYours, thread_header.go);
//  3. children: one row each, its title, status word and line, opening it
//     (blockChildren, thread_children.go);
//  4. then what is composed: the holder's blocks (thread_part.go), or,
//     while it has written none, the default composition from the thread's
//     data (thread_default.go).
//
// Everything else is the thread's data, under /thread, for the default
// composition and the holder's blocks to bind alike: its steps
// (threadStepsData), each source's items (threadItemsOf), its children,
// the user's marks.

// threadStatus is a thread's state as its head says it: a word and a tone
// (a Progress's). Read from what it shows: ended, what waits for the user,
// weight from urgent up, a step running, else its state.
func threadStatus(s Thread, steps []threadSurfaceStep, waits bool, weight int, lang string) (word, tone string) {
	switch {
	case !s.active() && s.State == threadDone:
		return tr(lang, "thread.state.done"), "success"
	case !s.active():
		return tr(lang, "thread.state."+s.State), "muted"
	case waits && weight >= weightUrgent:
		return tr(lang, "thread.surface.yours"), "urgent"
	case waits:
		return tr(lang, "thread.surface.yours"), "attention"
	case weight >= weightUrgent:
		return tr(lang, "thread.status.urgent"), "urgent"
	}
	for _, st := range steps {
		if st.State == "current" {
			return tr(lang, "thread.state.running"), "default"
		}
	}
	return tr(lang, "thread.status.calm"), "default"
}

// threadTileState is a thread's tile state (workspace_layout.go): done
// once ended; yours while something waits for the user; urgent from
// weightUrgent (raised, a note due soon); new with something changed since
// the user's last visit; running while a step runs; quiet otherwise.
func threadTileState(s Thread, steps []threadSurfaceStep, waits bool, weight, fresh int) string {
	switch {
	case !s.active():
		return tileDone
	case waits:
		return tileYours
	case weight >= weightUrgent:
		return tileUrgent
	case fresh > 0:
		return tileNew
	}
	for _, st := range steps {
		if st.State == "current" {
			return tileRunning
		}
	}
	return tileQuiet
}

// threadNew counts what changed on a thread since the user's last visit
// (visited, not seen: the badge goes once they open it): the header's
// facts after it, and the notes written after it on items still listed.
func (c *core) threadNew(s Thread, steps []threadSurfaceStep, lang string) int {
	n := len(threadFacts(s, steps, s.visited, lang))
	var notes int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_items WHERE thread = ? AND gone = 0 AND noted > ?`, s.ID, s.visited).Scan(&notes)
	return n + notes
}

// pendingDue is the condition of a note still to deal with that has a due
// date: to do, to look at, or not sorted, on an item still listed.
const pendingDue = `due > 0 AND gone = 0 AND state IN ('act', 'look', '')`

// notesDueBy tells whether a thread has a note still to deal with due by
// then (or past).
func (c *core) notesDueBy(thread int64, by time.Time) bool {
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_items WHERE thread = ? AND `+pendingDue+` AND due <= ?`, thread, by.Unix()).Scan(&n)
	return n > 0
}

// threadsDueBy are the threads with such a note.
func (c *core) threadsDueBy(by time.Time) map[int64]bool {
	out := map[int64]bool{}
	rows, err := c.store.db.Query(`SELECT DISTINCT thread FROM thread_items WHERE `+pendingDue+` AND due <= ?`, by.Unix())
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// blockHead composes the head: the Progress when it says more than « À
// jour » or has steps to show, the state line. Never the goal: it is the
// agents' target, written for them, not for the user; the thread says what
// it is by its title and its state line (its headline).
func (c *core) blockHead(out *threadComps, data map[string]any, s Thread, steps []threadSurfaceStep, word, tone, state string) {
	type comp = map[string]any
	if state != "" {
		data["state"] = state
	}
	if len(steps) == 0 && word == tr(c.userLang(), "thread.status.calm") {
		if state != "" {
			out.add(comp{"id": "thread_state", "component": "Text", "variant": "caption", "text": map[string]any{"@path": "/thread/state"}})
		}
		return
	}
	progress := map[string]any{"label": word}
	bar := comp{"id": "thread_progress", "component": "Progress", "label": map[string]any{"@path": "/thread/progress/label"}, "tone": tone}
	if len(steps) > 0 {
		var done, current int
		for _, st := range steps {
			switch st.State {
			case "done":
				done++
			case "current":
				current++
			}
		}
		progress["value"], progress["current"], progress["total"] = done, current, len(steps)
		for _, k := range []string{"value", "current", "total"} {
			bar[k] = map[string]any{"@path": "/thread/progress/" + k}
		}
	}
	data["progress"] = progress
	out.add(bar)
	if state != "" {
		out.add(comp{"id": "thread_state", "component": "Text", "variant": "caption", "text": map[string]any{"@path": "/thread/state"}})
	}
}

// blockControls composes the head's button, code's (controlOf): Stop on
// any active thread stopThread takes (not a standing one, which never
// ends).
func blockControls(out *threadComps, s Thread, lang string) {
	if !s.active() || s.standing() {
		return
	}
	type comp = map[string]any
	button := func(do, label, variant string) string {
		id := "thread_control_" + do
		out.inner(comp{"id": id, "component": "Button", "label": tr(lang, label), "variant": variant,
			"action": map[string]any{"event": map[string]any{"name": a2ui.ActionControl, "context": map[string]any{"thread": s.ID, "do": do}}}})
		return id
	}
	row := []string{button("stop", "thread.surface.stop", "quiet")}
	out.add(comp{"id": "thread_controls", "component": "Row", "align": "center", "children": row})
}

// blockEnded composes the banner of an ended thread: how it ended and
// when, where it stands, and « Ranger » when it ends on its goal (tidyOf):
// the user puts it away at once.
func (c *core) blockEnded(out *threadComps, data map[string]any, s Thread, now time.Time) {
	if s.active() {
		return
	}
	lang, loc := c.userLang(), c.userZone()
	type comp = map[string]any
	title := tr(lang, "thread.line."+s.State)
	if !s.Ended.IsZero() {
		title += threadExternalSep + threadClock(s.Ended, now, loc, lang)
	}
	text := strings.TrimSpace(s.Summary)
	if h := c.threadHeadlineOf(s.ID); text == "" && h != nil {
		text = h.Text // never its goal: the agents' words
	}
	data["ended"] = map[string]any{"title": title, "text": text}
	level := "info"
	if s.State == threadDone {
		level = "success"
	}
	banner := comp{"id": "thread_ended_says", "component": "Alert", "level": level,
		"title": map[string]any{"@path": "/thread/ended/title"}, "text": map[string]any{"@path": "/thread/ended/text"}}
	if s.Ends != endsGoal {
		out.add(banner)
		return
	}
	out.inner(banner, comp{"id": threadTidyID, "component": "Button", "label": tr(lang, "thread.surface.tidy"), "variant": "primary",
		"action": map[string]any{"event": map[string]any{"name": a2ui.ActionTidy, "context": map[string]any{"thread": s.ID}}}})
	out.add(comp{"id": "thread_ended", "component": "Row", "align": "center", "children": []string{"thread_ended_says", threadTidyID}})
}

// stepItem is a step as a Timeline item, and what was settled for its
// task under it.
func stepItem(st threadSurfaceStep, lang string, loc *time.Location) []any {
	item := map[string]any{"label": st.Label, "state": st.State}
	if !st.At.IsZero() {
		item["date"] = st.At.In(loc).Format(time.RFC3339)
	}
	var parts []string
	if k := tr(lang, "thread.surface.kind."+st.Kind); k != "thread.surface.kind."+st.Kind {
		parts = append(parts, k)
	}
	if st.Check != "" {
		parts = append(parts, tr(lang, "thread.surface.check."+st.Check))
	}
	if note := oneLine(st.Note); note != "" {
		parts = append(parts, note)
	}
	if st.External {
		parts = append([]string{tr(lang, "thread.surface.external")}, parts...)
	}
	if len(parts) > 0 {
		item["detail"] = truncate(strings.Join(parts, threadExternalSep), threadDetailMax)
	}
	out := []any{item}
	return out
}

// threadStepsData is a thread's steps at /thread/steps: those done (done,
// Timeline items, with what was settled for their tasks), the line that
// says them (summary: « 2 étapes faites · accord : Fusion », the last call
// whose card was approved), the others (open), and the cards decided on
// them (cards: a call's, with how its run went). While the thread is
// active, a step that waits for the user (its card, a choice, its doing)
// is not there: it is in « À toi », once.
func (c *core) threadStepsData(s Thread, steps []threadSurfaceStep) map[string]any {
	lang, loc := c.userLang(), c.userZone()
	done, open, cards := []any{}, []any{}, []any{}
	ndone, yes := 0, ""
	for _, st := range steps {
		if st.Waits && s.active() {
			continue
		}
		if st.Card != "" {
			cards = append(cards, map[string]any{"ref": st.Card})
		}
		if st.State != "done" {
			open = append(open, stepItem(st, lang, loc)...)
			continue
		}
		ndone++
		done = append(done, stepItem(st, lang, loc)...)
		if st.Card != "" && c.cardState(st.Card) == cardApproved {
			yes = st.Label
		}
	}
	summary := ""
	if ndone > 0 {
		summary = fmt.Sprintf(tr(lang, "thread.steps.done"), ndone)
		if ndone == 1 {
			summary = tr(lang, "thread.steps.done1")
		}
		if yes != "" {
			summary += threadExternalSep + fmt.Sprintf(tr(lang, "thread.steps.yes"), yes)
		}
	}
	return map[string]any{"done": done, "summary": summary, "open": open, "cards": cards}
}
