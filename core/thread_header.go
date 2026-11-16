package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The header of a thread's surface (#553): opening a thread tells in
// seconds what changed, where it stands and what is the user's, without
// rereading it. Each block shows only when it has something to say
// (thread_blocks.go), all composed by code:
//
//   - The state line: its headline (set_thread), else what its items
//     count, else its state; when its items were sorted; « extérieur »
//     once (threadStateText).
//   - Depuis ta dernière visite: facts made by code from the thread's own
//     data (its steps' starts and ends, its summary written again, its
//     end) since the user's visit before the last one, oldest first, the
//     last threadFactsMax; nothing before a first visit, nor when none.
//     What was settled for its tasks is one: kept as a kind and its
//     parameters (task_answer_log.go), worded here. Never the log's
//     lines: they are French only, and may quote a lead.
//   - Où on en est: the thread's summary, as remember (kind progress),
//     the extraction or its close wrote it, with its date; no model call;
//     nothing while it has none.
//   - À toi: what waits for the user (its cards, a step of theirs); none
//     when nothing waits, nor once it ended.
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

// threadWaitsUser tells whether something of a thread waits for the user:
// its own cards, the ones linked to it, a step of theirs.
func threadWaitsUser(steps []threadSurfaceStep, asks []int64, linked []string) bool {
	if len(asks) > 0 || len(linked) > 0 {
		return true
	}
	return slices.ContainsFunc(steps, func(st threadSurfaceStep) bool { return st.Waits })
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

// threadSince composes « Depuis ta dernière visite »: the facts since
// the user's visit before the last one, its monitors' events among them.
func (c *core) threadSince(out *threadComps, data map[string]any, s Thread, steps []threadSurfaceStep, more []threadFact, now time.Time) {
	lang, loc := c.userLang(), c.userZone()
	w := func(key string) string { return tr(lang, key) }
	type comp = map[string]any

	facts := threadFacts(s, steps, s.seen, lang)
	for _, f := range more { // its monitors' events (thread_live.go)
		if f.At.Unix() > s.seen {
			facts = append(facts, f)
		}
	}
	slices.SortStableFunc(facts, func(a, b threadFact) int { return a.At.Compare(b.At) })
	// Before a visit, all of it is new: the thread itself says it. After,
	// only what happened since, when something did.
	if s.seen == 0 || len(facts) == 0 {
		return
	}
	out.add(comp{"id": "thread_since", "component": "Text", "variant": "subtitle",
		"text": fmt.Sprintf(w("thread.head.sincewhen"), threadClock(time.Unix(s.seen, 0), now, loc, lang))})
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
}

// threadWhere composes « Où on en est »: the thread's summary, with its
// date; nothing while it has none. An ended thread says it in its banner
// (blockEnded).
func (c *core) threadWhere(out *threadComps, data map[string]any, s Thread, now time.Time) {
	sum := strings.TrimSpace(s.Summary)
	if sum == "" {
		return
	}
	lang, loc := c.userLang(), c.userZone()
	w := func(key string) string { return tr(lang, key) }
	type comp = map[string]any
	out.add(comp{"id": "thread_where", "component": "Text", "variant": "subtitle", "text": w("thread.head.where")})
	data["summary"] = sum
	out.add(comp{"id": "thread_summary", "component": "Markdown", "text": map[string]any{"@path": "/thread/summary"}})
	if s.summarized > 0 {
		out.add(comp{"id": "thread_summary_at", "component": "Text", "variant": "caption",
			"text": fmt.Sprintf(w("thread.head.summaryat"), threadClock(time.Unix(s.summarized, 0), now, loc, lang))})
	}
}

// A thread's headline (open_thread or set_thread headline): what the brain,
// a skill or a task tending it says of it to the user, one line, shown at the top of its surface in
// place of what code counts, and as its chip's line. Its author and taint
// are code's: written after reading external content, the surface is
// tainted for the models that read the workspace (codeTaint headline), and
// its state line says « extérieur ».
const (
	threadHeadlineKey   = "thread_headline:" // + the thread's id: its headline, JSON
	threadHeadlineMax   = 120                // runes
	threadHeadlineTaint = "headline"
)

type threadHeadline struct {
	Text    string   `json:"text"`
	By      string   `json:"by"`
	Tainted []string `json:"tainted,omitempty"`
	At      int64    `json:"at"`
}

// threadHeadlineOf is a thread's headline, nil for none.
func (c *core) threadHeadlineOf(id int64) *threadHeadline {
	raw := c.store.get(threadHeadlineKey + strconv.FormatInt(id, 10))
	if raw == "" {
		return nil
	}
	var h threadHeadline
	if json.Unmarshal([]byte(raw), &h) != nil || h.Text == "" {
		return nil
	}
	return &h
}

// headlineText is a headline as it is stored: one line, of
// threadHeadlineMax runes at most.
func headlineText(text string) (string, error) {
	text = oneLine(text)
	if utf8.RuneCountInString(text) > threadHeadlineMax {
		return "", fmt.Errorf("a headline of %d characters at most", threadHeadlineMax)
	}
	return text, nil
}

// setThreadHeadline writes a thread's headline, or takes it away (text
// empty), by who and with why its words may be someone else's.
func (c *core) setThreadHeadline(id int64, text, by string, taint []string) error {
	text, err := headlineText(text)
	if err != nil {
		return err
	}
	key := threadHeadlineKey + strconv.FormatInt(id, 10)
	if text == "" {
		c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, key)
	} else {
		raw, _ := json.Marshal(threadHeadline{Text: text, By: by, Tainted: slices.Compact(slices.Sorted(slices.Values(taint))), At: time.Now().Unix()})
		c.store.set(key, string(raw))
	}
	c.threadMoved(id)
	return nil
}

// threadSays is what a thread says of itself in one line: its headline,
// else what its items count (« 1 à faire · 2 pour info »); "" when it has
// neither.
func threadSays(h *threadHeadline, seen threadItemsSeen, lang string) string {
	if h != nil {
		return h.Text
	}
	var parts []string
	for _, state := range itemGroups {
		if n := seen.counts[state]; n > 0 && state != itemStateDone {
			word := "thread.item.state." + state
			if state == "" {
				word = "thread.item.state.other"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, tr(lang, word)))
		}
	}
	return strings.Join(parts, threadExternalSep)
}

// threadStateText is an active thread's state line, at the top of its
// surface: what it says of itself (threadSays), else its state when that
// says more; when its items were last sorted; « extérieur » once, when
// some of what it shows may carry someone else's words.
func (c *core) threadStateText(s Thread, says string, seen threadItemsSeen, tainted bool, now time.Time) string {
	lang, loc := c.userLang(), c.userZone()
	var parts []string
	if says == "" {
		says = c.threadStateLine(s, lang)
	}
	if says != "" {
		parts = append(parts, says)
	}
	if seen.sorted > 0 {
		parts = append(parts, fmt.Sprintf(tr(lang, "thread.state.sorted"), threadClock(time.Unix(seen.sorted, 0), now, loc, lang)))
	}
	if tainted {
		parts = append(parts, tr(lang, "thread.surface.external"))
	}
	return strings.Join(parts, threadExternalSep)
}

// threadYours composes « À toi »: what waits for the user, the thread's
// own cards, the cards raised for it (thread_turn.go), then its steps';
// nothing when nothing waits, or once it ended. What waits gives the
// surface its weight.
func (c *core) threadYours(in *surfaceIn, out *threadComps, s Thread, steps []threadSurfaceStep, asks []int64, linked []string) {
	w := func(key string) string { return tr(c.userLang(), key) }
	type comp = map[string]any
	if !s.active() {
		return
	}
	if !threadWaitsUser(steps, asks, linked) {
		return
	}
	out.add(comp{"id": "thread_yours", "component": "Text", "variant": "subtitle", "text": w("thread.surface.yours")})
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
		case len(st.Options) > 0:
			// A choice: one button per option, each bound by code to it (chooseOf).
			kids := []string{"thread_choice_" + n + "_label"}
			out.inner(comp{"id": kids[0], "component": "Text", "text": st.Label})
			for j, o := range st.Options {
				id := "thread_choice_" + n + "_" + strconv.Itoa(j)
				out.inner(comp{"id": id, "component": "Button", "label": o, "variant": "default",
					"action": map[string]any{"event": map[string]any{"name": a2ui.ActionChoose,
						"context": map[string]any{"thread": s.ID, "step": st.Key, "option": o}}}})
				kids = append(kids, id)
			}
			out.add(comp{"id": "thread_choice_" + n, "component": "Row", "align": "center", "children": kids})
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
