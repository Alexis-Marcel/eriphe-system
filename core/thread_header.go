package main

import (
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The header of a thread's surface (#553): opening a thread tells in
// seconds what changed, where it stands and what is the user's, without
// rereading it. Three parts, under its goal, all composed by code:
//
//   - Depuis ta dernière visite: facts made by code from the thread's own
//     data (its steps' starts and ends, its summary written again, its
//     end) since the user's visit before the last one, oldest first, the
//     last threadFactsMax; « Rien de nouveau depuis 14 h 32 » when none.
//     What was settled for its tasks is one: kept as a kind and its
//     parameters (task_answer_log.go), worded here. Never the log's
//     lines: they are French only, and may quote a lead.
//   - Où on en est: the thread's summary, as remember (kind progress),
//     the extraction or its close wrote it, with its date; no model call.
//   - À toi: what waits for the user (its cards, a step of theirs), or
//     « Rien pour l'instant. ». An ended thread has none.
//
// The visit: the user opening a thread's surface (the page's action
// "open", surfaceAction) moves its visits: seen takes the last one,
// visited is now; the header tells what changed since seen, so the visit
// that opens it still shows what was new. Opened again within
// threadVisitSame, it is the same visit: seen stays.

const (
	threadFactsMax  = 5               // facts the header shows, the last ones
	threadVisitSame = 5 * time.Minute // opened again within it: the same visit
)

// threadVisited records the user opening a thread's surface, then writes
// it again: its header now tells what changed since the visit before.
// Not a move of the thread: no log line, no watcher woken.
func (c *core) threadVisited(id int64, now time.Time) {
	var seen, visited int64
	if c.store.db.QueryRow(`SELECT seen, visited FROM threads WHERE id = ?`, id).Scan(&seen, &visited) != nil {
		return
	}
	if visited == 0 || now.Sub(time.Unix(visited, 0)) >= threadVisitSame {
		seen = visited
	}
	c.store.db.Exec(`UPDATE threads SET seen = ?, visited = ? WHERE id = ?`, seen, now.Unix(), id)
	c.keepThreadSurface(id)
}

// threadComps are the components of a thread's surface as code composes
// them, and the ones its root shows, in order.
type threadComps struct {
	list     []map[string]any
	children []string
}

// add places a component under the root.
func (t *threadComps) add(comp map[string]any) {
	t.list = append(t.list, comp)
	t.children = append(t.children, comp["id"].(string))
}

// inner keeps a component another one shows (a Row's).
func (t *threadComps) inner(comp ...map[string]any) { t.list = append(t.list, comp...) }

// threadFact is a thing that happened to a thread, as its header says it.
type threadFact struct {
	At   time.Time
	Text string
}

// threadFacts are what happened to a thread after since (0: since it
// opened), oldest first.
func threadFacts(s Thread, steps []threadSurfaceStep, since int64, lang string) []threadFact {
	after := func(t time.Time) bool { return !t.IsZero() && t.Unix() > since }
	var out []threadFact
	if after(s.Created) {
		out = append(out, threadFact{s.Created, tr(lang, "thread.fact.opened")})
	}
	for _, st := range steps {
		switch {
		case after(st.Ended):
			word := "thread.fact.ended"
			switch st.State {
			case "done":
				word = "thread.fact.done"
			case "failed":
				word = "thread.fact.failed"
			case "current":
				word = "thread.fact.reported"
			}
			out = append(out, threadFact{st.Ended, fmt.Sprintf(tr(lang, word), st.Label)})
		case after(st.Started) && st.Ended.IsZero():
			word := "thread.fact.started"
			if st.Waits {
				word = "thread.fact.waits" // its card, the user's choice or doing
			}
			out = append(out, threadFact{st.Started, fmt.Sprintf(tr(lang, word), st.Label)})
		}
		for _, a := range st.Answers {
			if after(a.At) {
				text := fmt.Sprintf(tr(lang, "thread.fact.answer"), st.Label, a.text(lang))
				if a.Tainted != "" {
					text += threadExternalSep + tr(lang, "thread.surface.external")
				}
				out = append(out, threadFact{a.At, text})
			}
		}
	}
	if s.summarized > since && s.summarized > 0 {
		out = append(out, threadFact{time.Unix(s.summarized, 0), tr(lang, "thread.fact.summary")})
	}
	if after(s.Ended) && !s.active() {
		out = append(out, threadFact{s.Ended, tr(lang, "thread.line."+s.State)})
	}
	slices.SortStableFunc(out, func(a, b threadFact) int { return a.At.Compare(b.At) })
	return out
}

// threadClock words a moment for the header, absolute (the surface is
// written again only when the thread changes): « 14 h 32 » the same day
// as now, else « 13 nov. 14 h 32 ».
func threadClock(t, now time.Time, loc *time.Location, lang string) string {
	t, now = t.In(loc), now.In(loc)
	clock := fmt.Sprintf("%d h %02d", t.Hour(), t.Minute())
	if lang == "en" {
		clock = t.Format("15:04")
	}
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return clock
	}
	month := tr(lang, "thread.month."+strconv.Itoa(int(t.Month())))
	if lang == "en" {
		return fmt.Sprintf("%s %d, %s", month, t.Day(), clock)
	}
	return fmt.Sprintf("%d %s %s", t.Day(), month, clock)
}

// threadStateLine is the thread's state under its goal, when it says more
// than the header: not for a light thread the brain holds that is open.
func (c *core) threadStateLine(s Thread, lang string) string {
	if s.active() && !s.led() && s.State == threadOpen {
		return ""
	}
	state := tr(lang, "thread.state."+s.Look())
	if s.active() {
		if s.led() && s.Lead != "" {
			state += threadExternalSep + tr(lang, "thread.surface.lead")
		} else {
			state += threadExternalSep + tr(lang, "thread.surface.brain")
		}
	}
	return state
}

// threadHeader composes the header of a thread's surface: since the
// user's last visit, where it stands, what is theirs. What waits for the
// user gives the surface its weight.
func (c *core) threadHeader(in *surfaceIn, out *threadComps, data map[string]any, s Thread, steps []threadSurfaceStep, asks []int64, more []threadFact, now time.Time) {
	lang, loc := c.userLang(), c.userZone()
	w := func(key string) string { return tr(lang, key) }
	type comp = map[string]any

	// Depuis ta dernière visite.
	facts := threadFacts(s, steps, s.seen, lang)
	for _, f := range more { // its monitors' events (thread_live.go)
		if f.At.Unix() > s.seen {
			facts = append(facts, f)
		}
	}
	slices.SortStableFunc(facts, func(a, b threadFact) int { return a.At.Compare(b.At) })
	switch {
	case s.seen == 0:
		out.add(comp{"id": "thread_since", "component": "Text", "variant": "subtitle", "text": w("thread.head.sofar")})
	case len(facts) == 0:
		out.add(comp{"id": "thread_since", "component": "Text", "variant": "subtitle", "text": w("thread.head.since")})
		// No fact code words, but the log says whether something moved
		// (a step added, a plan changed, a card linked, a reopen): never
		// « Rien de nouveau » then. Counted, never quoted.
		var n int
		var last sql.NullInt64
		c.store.db.QueryRow(`SELECT COUNT(*), MAX(at) FROM thread_log WHERE thread = ? AND at > ?`, s.ID, s.seen).Scan(&n, &last)
		if n > 0 && last.Valid {
			out.add(comp{"id": "thread_moved", "component": "Text", "variant": "caption",
				"text": fmt.Sprintf(w("thread.head.moved"), n, threadClock(time.Unix(last.Int64, 0), now, loc, lang))})
		} else {
			out.add(comp{"id": "thread_nothing", "component": "Text", "variant": "caption",
				"text": fmt.Sprintf(w("thread.head.nothing"), threadClock(time.Unix(s.seen, 0), now, loc, lang))})
		}
	default:
		out.add(comp{"id": "thread_since", "component": "Text", "variant": "subtitle",
			"text": fmt.Sprintf(w("thread.head.sincewhen"), threadClock(time.Unix(s.seen, 0), now, loc, lang))})
	}
	var lines []any
	if n := len(facts) - threadFactsMax; n > 0 {
		lines = append(lines, fmt.Sprintf(w("thread.head.earlier"), n))
		facts = facts[n:]
	}
	for _, f := range facts {
		lines = append(lines, threadClock(f.At, now, loc, lang)+threadExternalSep+f.Text)
	}
	if len(lines) > 0 {
		data["facts"] = lines
		for i := range lines {
			out.add(comp{"id": "thread_fact_" + strconv.Itoa(i), "component": "Text", "text": map[string]any{"@path": "/thread/facts/" + strconv.Itoa(i)}})
		}
	}

	// Où on en est.
	out.add(comp{"id": "thread_where", "component": "Text", "variant": "subtitle", "text": w("thread.head.where")})
	if sum := strings.TrimSpace(s.Summary); sum != "" {
		data["summary"] = sum
		out.add(comp{"id": "thread_summary", "component": "Markdown", "text": map[string]any{"@path": "/thread/summary"}})
		if s.summarized > 0 {
			out.add(comp{"id": "thread_summary_at", "component": "Text", "variant": "caption",
				"text": fmt.Sprintf(w("thread.head.summaryat"), threadClock(time.Unix(s.summarized, 0), now, loc, lang))})
		}
	} else {
		out.add(comp{"id": "thread_nosummary", "component": "Text", "variant": "caption", "text": w("thread.head.nosummary")})
	}

	// À toi: the thread's own cards, the cards raised for it
	// (thread_turn.go), then its steps'.
	if !s.active() {
		return
	}
	out.add(comp{"id": "thread_yours", "component": "Text", "variant": "subtitle", "text": w("thread.surface.yours")})
	linked := c.linkedWaiting(s.ID, steps)
	yours := len(asks) > 0 || len(linked) > 0
	for _, st := range steps {
		yours = yours || st.Waits
	}
	if !yours {
		out.add(comp{"id": "thread_yours_none", "component": "Text", "variant": "caption", "text": w("thread.head.nothingyours")})
		return
	}
	// Its cards leave the attention queue for it when it answers them
	// (threadShownCards): it takes their weight.
	in.Weight = threadWeightYours
	for _, ref := range threadCardsWaiting(steps, asks, linked) {
		kind, _, _ := parseProposalRef(ref)
		in.Weight = max(in.Weight, queueSurfaceWeight(cardWeight(kind)))
	}
	for _, id := range asks {
		out.add(comp{"id": "thread_ask_" + strconv.FormatInt(id, 10), "component": "Proposal", "proposalId": proposalRef(cardThread, id)})
	}
	for i, ref := range linked {
		out.add(comp{"id": "thread_linked_" + strconv.Itoa(i), "component": "Proposal", "proposalId": ref})
	}
	for i, st := range steps {
		n := strconv.Itoa(i)
		switch {
		case !st.Waits:
		case st.Card != "":
			out.add(comp{"id": "thread_card_" + n, "component": "Proposal", "proposalId": st.Card})
		case !st.Yours:
			out.add(comp{"id": "thread_wait_" + n, "component": "Text", "text": st.Label})
		default:
			out.inner(
				comp{"id": "thread_user_" + n + "_label", "component": "Text", "text": st.Label},
				comp{"id": "thread_user_" + n + "_done", "component": "Button", "label": w("thread.surface.done"), "icon": "check", "variant": "primary",
					"action": map[string]any{"event": map[string]any{"name": a2ui.ActionStepDone,
						"context": map[string]any{"thread": s.ID, "step": st.Key}}}})
			out.add(comp{"id": "thread_user_" + n, "component": "Row", "align": "center",
				"children": []string{"thread_user_" + n + "_label", "thread_user_" + n + "_done"}})
		}
	}
}
