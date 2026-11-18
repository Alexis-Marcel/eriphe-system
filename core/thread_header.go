package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The head of a thread's surface (#553, #611): opening a thread tells in
// seconds where it stands and what is the user's, without rereading it.
// Composed by code (thread_blocks.go):
//
//   - The state line: its headline (open_thread), else what its items
//     count, else its state; when its items were sorted; « extérieur »
//     beside its headline when that may carry someone else's words
//     (threadStateText).
//   - À toi: what waits for the user (its cards, a choice, a step of
//     theirs), each once; none when nothing waits, nor once it ended.
//
// What it does not draw is in its data, for what is composed to bind:
//
//   - /thread/since: facts made by code from the thread's own data (its
//     steps' starts and ends, its summary written again, its end) since
//     the user's visit before the last one, oldest first, the last
//     threadFactsMax; none before a first visit. Never the log's lines: they are
//     French only, and may quote a lead.
//   - /thread/summary: the thread's summary, as remember (kind progress),
//     the extraction or its close wrote it; no model call.
//
// The visit: the user opening a thread's surface (the page's action
// "open", surfaceAction) moves its visits: seen takes the last one,
// visited is now; since tells what changed since seen, so the visit that
// opens it still shows what was new. Opened again within threadVisitSame,
// it is the same visit: seen stays. What changed since the last visit
// counts on its tile (threadNew).

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
	if s.active() && s.State == threadOpen {
		return ""
	}
	state := tr(lang, "thread.state."+s.Look())
	if s.active() {
		state += threadExternalSep + tr(lang, "thread.surface.brain")
	}
	return state
}

// threadSinceData are the facts since the user's visit before the last
// one, at /thread/since, each a line with its time (« 14 h 32 · Fusion
// faite »), the last threadFactsMax after a line counting the others;
// none before a first visit (all of the thread is new).
func (c *core) threadSinceData(s Thread, steps []threadSurfaceStep, now time.Time) []any {
	lines := []any{}
	if s.seen == 0 {
		return lines
	}
	lang, loc := c.userLang(), c.userZone()
	facts := threadFacts(s, steps, s.seen, lang)
	if n := len(facts) - threadFactsMax; n > 0 {
		lines = append(lines, fmt.Sprintf(tr(lang, "thread.head.earlier"), n))
		facts = facts[n:]
	}
	for _, f := range facts {
		lines = append(lines, threadClock(f.At, now, loc, lang)+threadExternalSep+f.Text)
	}
	return lines
}

// A thread's headline (open_thread): what the brain says of it to the user, one line, shown at the top of its surface in
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
// says more, with « extérieur » beside a headline that may carry someone
// else's words (h tainted), never alone; when its items were last sorted.
// What else may carry someone else's words says it where it shows: a row,
// a step, a block.
func (c *core) threadStateText(s Thread, h *threadHeadline, seen threadItemsSeen, now time.Time) string {
	lang, loc := c.userLang(), c.userZone()
	var parts []string
	if says := threadSays(h, seen, lang); says != "" {
		parts = append(parts, says)
		if h != nil && len(h.Tainted) > 0 {
			parts = append(parts, tr(lang, "thread.surface.external"))
		}
	} else if state := c.threadStateLine(s, lang); state != "" {
		parts = append(parts, state)
	}
	if seen.sorted > 0 {
		parts = append(parts, fmt.Sprintf(tr(lang, "thread.state.sorted"), threadClock(time.Unix(seen.sorted, 0), now, loc, lang)))
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
	for i, ref := range linked {
		out.add(comp{"id": "thread_linked_" + strconv.Itoa(i), "component": "Proposal", "proposalId": ref})
	}
}
