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
// thread has what it shows, never by the kind of thread:
//
//  1. head: its goal, a Progress (the state word in a pill, and a bar of
//     its steps when it has some), the state line when it says more;
//  2. ended: once it ended, a banner saying how, with « Ranger » for a
//     thread that ends on its goal (the only block that reads ends);
//  3. yours: what waits for the user (threadYours, thread_header.go);
//  4. items: each source that lists things, its items grouped by their
//     notes' state, with their counts (threadSourcesPart, thread_sources.go);
//  5. live: the monitors it follows and their last events (thread_live.go);
//  6. where it stands, then what changed since the user's last visit
//     (threadWhere, threadSince);
//  7. steps: the done ones folded in one line, with the last yes given;
//     the others as a Timeline; decided cards one line each (compact);
//  8. the holder's part.

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

// blockHead composes the head: the goal, the Progress, the state line.
func (c *core) blockHead(out *threadComps, data map[string]any, s Thread, steps []threadSurfaceStep, word, tone string) {
	type comp = map[string]any
	out.add(comp{"id": "thread_goal", "component": "Text", "text": map[string]any{"@path": "/thread/goal"}})
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
	if state := c.threadStateLine(s, c.userLang()); state != "" && s.active() {
		data["state"] = state
		out.add(comp{"id": "thread_state", "component": "Text", "variant": "caption", "text": map[string]any{"@path": "/thread/state"}})
	}
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
	if text == "" {
		text = s.Goal
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
