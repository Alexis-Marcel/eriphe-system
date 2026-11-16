package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// A thread's surface is composed of generic blocks (#574), the same code
// for every thread, in one fixed order; a block shows only when the
// thread has what it shows, never by the kind of thread; how it ends is
// read twice: a standing one shows no goal (its title and state line say
// what it keeps) and no Stop, and the banner of an ended one:
//
//  1. head: the goal of one that ends on it, a Progress (the state word in
//     a pill, and a bar of its steps when it has some; none for a calm
//     thread with no steps), the state line (threadStateText: its headline
//     or its items' counts, when sorted, « extérieur » once), and for a
//     thread that runs steps, Pause or Resume, and Stop;
//  2. ended: once it ended, a banner saying how, with « Ranger » for a
//     thread that ends on its goal (the only block that reads ends);
//  3. yours: what waits for the user (threadYours, thread_header.go);
//  4. children: one row each, its title, status word and line, opening it
//     (blockChildren, thread_children.go);
//  5. items: each source that lists things, its items grouped by their
//     notes' state, each group counted on its line, with its action
//     (threadSourcesPart, thread_sources.go);
//  6. live: the monitors it follows and their last events (thread_live.go);
//  7. where it stands, when it has a summary, then what changed since the
//     user's last visit, when something did (threadWhere, threadSince);
//  8. steps: the done ones folded in one line, with the last yes given;
//     the others as a Timeline; decided cards one line each (compact);
//  9. the holder's part.
//
// A child's surface starts with its crumb back to its parent (blockParent).

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
	if s.led() && s.Phase == phasePaused {
		return tr(lang, "thread.state.paused"), "muted"
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

// blockControls composes the head's buttons, each code's (controlOf):
// Stop on any active thread stopThread takes (not a standing one, which
// never ends); on a thread that runs steps (led), Pause while it runs and
// Resume while paused (more time when its budget held it) before it.
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
	var row []string
	switch {
	case !s.led():
	case s.Phase == phaseRunning:
		row = append(row, button("pause", "thread.surface.pause", "default"))
	case s.Phase == phasePaused && s.Held == heldBudget:
		row = append(row, button("resume", "thread.surface.resume.more", "primary"))
	case s.Phase == phasePaused:
		row = append(row, button("resume", "thread.surface.resume", "primary"))
	}
	row = append(row, button("stop", "thread.surface.stop", "quiet"))
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
	for _, a := range st.Answers {
		answer := map[string]any{"label": a.text(lang), "state": "done", "date": a.At.In(loc).Format(time.RFC3339)}
		if a.Tainted != "" {
			answer["detail"] = tr(lang, "thread.surface.external")
		}
		out = append(out, answer)
	}
	return out
}

// blockSteps composes the steps: those done folded in one line (« 4
// étapes faites · accord : Fusion »), opened to see them and their
// decided cards, each one line; then the others as a Timeline, a decided
// card of theirs one line under it. A card that waits is the user's
// (threadYours) while the thread is active.
func (c *core) blockSteps(out *threadComps, data map[string]any, s Thread, steps []threadSurfaceStep) {
	if len(steps) == 0 {
		return
	}
	lang, loc := c.userLang(), c.userZone()
	type comp = map[string]any
	shownHere := func(st threadSurfaceStep) bool { return st.Card != "" && (!st.Waits || !s.active()) }
	var done, rest []any
	var doneCards, restCards []string
	ndone, yes := 0, ""
	for i, st := range steps {
		id := "thread_card_" + strconv.Itoa(i)
		if st.State == "done" {
			ndone++
			done = append(done, stepItem(st, lang, loc)...)
			if shownHere(st) {
				doneCards = append(doneCards, id)
				out.inner(comp{"id": id, "component": "Proposal", "proposalId": st.Card, "compact": true})
				if c.cardState(st.Card) == cardApproved {
					yes = st.Label
				}
			}
			continue
		}
		rest = append(rest, stepItem(st, lang, loc)...)
		if shownHere(st) {
			restCards = append(restCards, id)
		}
	}
	if ndone > 0 {
		label := fmt.Sprintf(tr(lang, "thread.steps.done"), ndone)
		if ndone == 1 {
			label = tr(lang, "thread.steps.done1")
		}
		if yes != "" {
			label += threadExternalSep + fmt.Sprintf(tr(lang, "thread.steps.yes"), yes)
		}
		data["done"] = done
		data["donelabel"] = label
		out.inner(comp{"id": "thread_done_steps", "component": "Timeline", "items": map[string]any{"@path": "/thread/done"}},
			comp{"id": "thread_done_list", "component": "Column", "children": append([]string{"thread_done_steps"}, doneCards...)})
		out.add(comp{"id": "thread_done", "component": "Fold", "label": map[string]any{"@path": "/thread/donelabel"}, "child": "thread_done_list"})
	}
	if len(rest) > 0 {
		data["steps"] = rest
		out.add(comp{"id": "thread_steps", "component": "Timeline", "items": map[string]any{"@path": "/thread/steps"}})
	}
	for _, id := range restCards {
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "thread_card_"))
		out.add(comp{"id": id, "component": "Proposal", "proposalId": steps[i].Card, "compact": true})
	}
}

// blockWatch composes the last events of the monitors a thread follows,
// as a Timeline, under its live values.
func blockWatch(out *threadComps, data map[string]any, watch []any) {
	if len(watch) == 0 {
		return
	}
	data["watch"] = watch
	out.add(map[string]any{"id": "thread_watch", "component": "Timeline", "items": map[string]any{"@path": "/thread/watch"}})
}
