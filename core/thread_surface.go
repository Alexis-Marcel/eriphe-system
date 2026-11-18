package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// Every thread has its surface (#468): thread-<id>, its timeline, written
// and kept by code from the thread's data, never by a model remembering
// to write it.
//
//   - Its author is thread:<id>, code's for that thread: nobody else
//     writes or removes it as a whole (mayChange), and it does not count
//     in surfacesMax.
//   - Code's frame, thin and the same for every thread (thread_blocks.go):
//     the head (a child's crumb, the status word, the state line, the
//     controls, how it ended), « À toi » (every card, ask, choice and step
//     that waits for the user, each once) and its children. Its component
//     ids start with thread_.
//   - Its data, code's, under /thread, for whatever is composed to bind:
//     its steps (/thread/steps), each source's items
//     (/thread/items/<source>), its children, the user's marks
//     (/thread/marks, thread_marks.go).
//   - What is composed: the holder's part, who holds the thread (the
//     brain, or its lead) composing it by named blocks with surface_write
//     on thread-<id> (a state, points to see, a diff, a link), each kept
//     apart, checked alone, and shown under the frame in its order
//     (thread_part.go); their sources become the thread's
//     (thread_sources.go): read by code, the surface written from their
//     last read, so a change of the thread rewrites it at once and never
//     waits for a tool. While the holder has written no block, the
//     default composition from the thread's data (thread_default.go).
//   - Taint, per part: a row, a step or a block that may carry someone
//     else's words says « extérieur » beside it, never alone. For the
//     models that read the workspace, the surface is tainted as soon as
//     one part is (surfaceLook.CodeTaint, AuthorTaint). Its
//     Proposals still answer in place (surfaceLook.Answers) unless the
//     thread itself is tainted: code placed them, each bound to one of
//     the thread's own cards, as the attention queue's (#443).
//   - Small: a one-line summary (surfaceLook.Summary, the page's
//     extensions.eriphe.summary) for a small tile or a chip.
//   - Lifetime: while the thread is active; once done or dropped, until
//     the end of its day (an hour at least), then it leaves.
//   - A child's surface opens inside its parent's, never a tile of its
//     own (thread_children.go); a change of it writes its parent's again.
//
// keepThreadSurface is the hook: the store calls it at each change of a
// thread (setThread, setStep, insertSteps, its cards), and the engine's
// tick for an active thread that has none. It writes only when what the
// surface shows changed. Step kinds plug in through threadSurfaceSteps,
// which maps a step to what the surface shows of it.

const (
	threadSurfacePrefix  = "thread-"          // + the thread's id: its surface
	threadCompPrefix     = "thread_"          // code's component ids
	threadDataKey        = "thread"           // code's data, /thread
	threadPartRoot       = "thread_part"      // + _<block>: a block of the holder's part's root, renamed
	threadExternalPrefix = "thread_external_" // + <block>: a block that may show someone else's words, with its mark
	threadMarkPrefix     = "thread_mark_"     // + <block>: that mark, « extérieur »
	threadPartKey        = "thread_part:"     // + the thread's id: the holder's part, its blocks as JSON
	threadShownKey       = "thread_shown:"    // + the thread's id: a hash of what its surface shows
	threadAgainKey       = "thread_again:"    // + the thread's id: when a link showed it again once ended, unix
	threadPartMax        = 24 << 10           // components and data of a part's blocks together, each
	threadAfterEnd       = time.Hour          // a done thread's surface stays this long at least
	threadLineMax        = 80                 // runes of the one-line summary
	threadDetailMax      = 200                // runes of a step's detail
	threadExternalSep    = " · "              // between the parts of a line
	threadWeight         = 50                 // an active thread's surface
	threadWeightYours    = 70                 // one that waits for the user
	threadWeightEnded    = 30                 // one that ended
)

// threadSurfaceMu makes the surface writes one at a time: two changes of
// a thread at the same moment never write it out of order.
var threadSurfaceMu sync.Mutex

func threadSurfaceID(id int64) string { return threadSurfacePrefix + strconv.FormatInt(id, 10) }
func threadAuthor(id int64) string    { return "thread:" + strconv.FormatInt(id, 10) }

// threadOfSurface is the thread whose surface id is, if it is one.
func threadOfSurface(id string) (int64, bool) {
	raw, ok := strings.CutPrefix(id, threadSurfacePrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}

// threadOfAuthor is the thread an author keeps the surface of, if it is one.
func threadOfAuthor(author string) (int64, bool) {
	raw, ok := strings.CutPrefix(author, "thread:")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0
}

// keptByCode tells whether a surface's author is code: the attention
// queue's, or a thread's.
func keptByCode(author string) bool {
	_, thread := threadOfAuthor(author)
	return author == surfaceAuthorCore || thread
}

// threadSurfaceStep is a step as the surface shows it, whatever its kind.
type threadSurfaceStep struct {
	Key      string
	Label    string
	Kind     string    // task, choice, approval, call, user, check, deadline
	State    string    // a Timeline item's: done, current, pending, failed
	At       time.Time // when it ended, else when it started
	Started  time.Time // when it started, zero if not yet
	Ended    time.Time // when it ended, zero if not yet
	Card     string    // the ref of its proposal, shown in place: answered there while it waits, then a call's outcome
	Check    string    // ok or failed: what its checks said
	Note     string    // the last word on it
	External bool      // what it reports may come from outside
	Waits    bool      // it waits for the user: its card, their choice, their doing
	Options  []string  // a choice that waits: what the user picks from
	Yours    bool      // the user does it and has not yet: « C'est fait »
}

// linkedWaiting are the cards linked to a thread (thread_cards) that still
// wait for the user and are no step's: they are answered on its surface.
func (c *core) linkedWaiting(thread int64, steps []threadSurfaceStep) []string {
	rows, err := c.store.db.Query(`SELECT ref FROM thread_cards WHERE thread = ? ORDER BY created, ref`, thread)
	if err != nil {
		return nil
	}
	var refs []string
	for rows.Next() {
		var ref string
		if rows.Scan(&ref) == nil {
			refs = append(refs, ref)
		}
	}
	rows.Close() // one connection: the states are read after
	var out []string
	for _, ref := range refs {
		if slices.ContainsFunc(steps, func(st threadSurfaceStep) bool { return st.Card == ref }) {
			continue
		}
		if _, _, ok := parseProposalRef(ref); ok && c.cardState(ref) == cardPending {
			out = append(out, ref)
		}
	}
	return out
}

// threadCardsWaiting are the refs of the cards waiting for the user that
// a thread's surface shows: its own (asks), the ones linked to it, its
// steps'.
func threadCardsWaiting(steps []threadSurfaceStep, asks []int64, linked []string) []string {
	out := slices.Clone(linked)
	for _, st := range steps {
		if st.Waits && st.Card != "" {
			out = append(out, st.Card)
		}
	}
	return out
}

// threadShownCards are the cards waiting for the user that an active
// thread's surface shows and answers, by ref, with the thread (the newest,
// should two show one): answered there, they leave the attention queue's
// surfaces (queuePlace, workspace_queue.go).
func (c *core) threadShownCards() map[string]int64 {
	out := map[string]int64{}
	ss, err := c.threadsWhere(activeThreads + ` ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return out
	}
	for _, s := range ss {
		for _, ref := range threadCardsWaiting(nil, nil, c.linkedWaiting(s.ID, nil)) {
			if _, ok := out[ref]; !ok {
				out[ref] = s.ID
			}
		}
	}
	return out
}

// showThreadAgain puts an ended thread's surface back on the workspace, for
// as long as one that just ended: a link to it (/espace?open=thread-<id>,
// from a card, the tasks, a skill's run) opens it. Not one the user put
// away (« Ranger »), nor a day's: its day is over.
func (c *core) showThreadAgain(id int64, now time.Time) {
	s, err := c.thread(id)
	if err != nil || s.active() || s.tidied > 0 || s.Ends == endsDay {
		return
	}
	if s.Parent != 0 {
		c.showThreadAgain(s.Parent, now) // a child opens inside its parent
	}
	c.store.set(threadAgainKey+strconv.FormatInt(id, 10), strconv.FormatInt(now.Unix(), 10))
	c.keepThreadSurface(id)
}

// threadAgain is when a link last showed an ended thread again; zero if never.
func (c *core) threadAgain(id int64) time.Time {
	n, err := strconv.ParseInt(c.store.get(threadAgainKey+strconv.FormatInt(id, 10)), 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// threadSurfaceIn is a thread's surface as code writes it; false when an
// ended thread's time on the workspace is over.
func (c *core) threadSurfaceIn(s Thread, steps []threadSurfaceStep, asks []int64, part *threadPart, now time.Time) (surfaceIn, bool) {
	lang, loc := c.userLang(), c.userZone()
	w := func(key string) string { return tr(lang, key) }
	// No why: what the thread says of itself is its state line.
	in := surfaceIn{ID: threadSurfaceID(s.ID), Title: truncate(s.Title, surfaceTitleMax), Weight: threadWeight}
	if s.active() {
		in.Lifetime.While = "thread:" + strconv.FormatInt(s.ID, 10)
	} else {
		if s.Ends == endsDay || s.tidied > 0 {
			return in, false // its day is over, or the user put it away
		}
		end := s.Ended
		if again := c.threadAgain(s.ID); again.After(end) {
			end = again // shown again: as long as one that just ended
		}
		if end.IsZero() {
			end = now
		}
		end = end.In(loc)
		until := time.Date(end.Year(), end.Month(), end.Day()+1, 0, 0, 0, 0, loc)
		if until.Sub(end) < threadAfterEnd {
			until = end.Add(threadAfterEnd)
		}
		if !until.After(now) {
			return in, false
		}
		if s.Parent != 0 {
			// An ended child leaves with its parent's surface, whose children it
			// is shown among (thread_children.go).
			if _, err := c.surface(threadSurfaceID(s.Parent)); err != nil {
				return in, false
			}
		}
		in.Lifetime.Until = until.Truncate(time.Second)
		in.Weight = threadWeightEnded
	}

	type comp = map[string]any
	var head, out threadComps
	data := map[string]any{}
	var linked []string
	if s.active() {
		linked = c.linkedWaiting(s.ID, steps)
	}
	// The frame (thread_blocks.go): the head is composed last, once the
	// weight is known, and put first.
	c.blockEnded(&out, data, s, now)
	c.threadYours(&in, &out, s, steps, asks, linked)
	// Its children (thread_children.go): what waits in one raises it.
	kids := c.threadKidsOf(s, lang)
	blockChildren(&out, data, kids.rows, lang)
	if s.active() {
		in.Weight = max(in.Weight, kids.weight)
	}

	// Its data, code's, there to bind whatever is composed: its steps, its
	// sources' items (thread_sources.go), monitors among them: past a
	// limit, one makes it rise (thread_live.go).
	var cards []itemCard
	if s.active() {
		cards = c.threadItemCards(s.ID, now)
	}
	items := c.threadItemsOf(&in, s, cards, now)
	if items.hot {
		in.Weight = max(in.Weight, threadWeightWatch)
	}
	data["steps"] = c.threadStepsData(s, steps)
	data["items"] = items.data
	data["since"] = c.threadSinceData(s, steps, now)
	data["summary"] = strings.TrimSpace(s.Summary)
	// Raised (a signal about it): it takes the
	// item's weight, as a surface its item's (workspace_queue.go).
	if w := c.threadRaised(s.ID); w > 0 {
		in.Weight = max(in.Weight, queueSurfaceWeight(w))
	}
	// A note due soon (or past) makes it urgent: the tick writes it again
	// as the time comes (keepThreadSurfaces).
	if s.active() && c.notesDueBy(s.ID, now.Add(dueSoon)) {
		in.Weight = max(in.Weight, weightUrgent)
	}

	// Taint, per part: the thread's own, each step that read outside, its
	// headline; its sources', notes' and monitors' are counted already.
	if s.Tainted != "" {
		in.codeTaint = append(in.codeTaint, strings.Split(s.Tainted, ", ")...)
	}
	for _, st := range steps {
		if st.External {
			in.codeTaint = append(in.codeTaint, "step:"+st.Key)
		}
	}
	headline := c.threadHeadlineOf(s.ID)
	if headline != nil && len(headline.Tainted) > 0 {
		in.codeTaint = append(in.codeTaint, threadHeadlineTaint)
	}
	// A child's line may carry someone else's words: its parent shows it,
	// marked on its row.
	in.codeTaint = append(in.codeTaint, kids.taint...)

	waits := s.active() && (threadWaitsUser(steps, asks, linked) || kids.waits)
	in.fresh = c.threadNew(s, steps, lang)
	in.state = threadTileState(s, steps, waits, in.Weight, in.fresh)
	word, tone := threadStatus(s, steps, waits, in.Weight, lang)
	state := ""
	if s.active() {
		state = c.threadStateText(s, headline, items.seen, now)
	}
	c.blockParent(&head, data, s)
	c.blockHead(&head, data, s, steps, word, tone, state)
	blockControls(&head, s, lang)
	out.list = append(head.list, out.list...)
	out.children = append(head.children, out.children...)

	// What is composed: the holder's blocks, in their order (thread_part.go),
	// each its root under the frame, their data side by side, no key in
	// two; « extérieur » beside each that may carry someone else's words.
	// While it has none, the default composition from the thread's data
	// (thread_default.go).
	composed := map[string]any{}
	if part != nil {
		in.Tainted = part.tainted()
		in.blockAt = map[int]string{}
		for _, b := range part.Blocks {
			var list []comp
			var d map[string]any
			json.Unmarshal(b.Components, &list)
			json.Unmarshal(b.Data, &d)
			for i := range list {
				in.blockAt[1+len(out.list)+i] = b.Name // after the root
			}
			out.inner(list...)
			maps.Copy(composed, d)
			if !b.external(items.untrusted) {
				out.children = append(out.children, partRoot(b.Name))
				continue
			}
			marked := threadMarkPrefix + b.Name
			out.inner(comp{"id": marked, "component": "Text", "variant": "mark", "text": w("thread.surface.external")})
			out.add(comp{"id": threadExternalPrefix + b.Name, "component": "Column", "children": []string{marked, partRoot(b.Name)}})
		}
	} else {
		threadDefault(&out, data["steps"].(map[string]any), items.data, items.order, lang)
	}
	if !slices.ContainsFunc(out.children, func(id string) bool { return id != "thread_controls" && id != "thread_parent" }) {
		// Nothing yet but its buttons: a standing thread before its first
		// item, a thread just opened with no headline, say.
		out.add(comp{"id": "thread_state", "component": "Text", "variant": "caption", "text": w("thread.item.none")})
	}
	for name, v := range items.values {
		composed[name] = v // where the holder's part binds it
	}
	// The user's marks, code's, always there to bind (thread_marks.go): a
	// rewrite of the part never loses one.
	data[threadMarksKey] = c.marksData(s.ID)
	composed[threadDataKey] = data
	comps := append([]comp{{"id": "root", "component": "Column", "children": out.children}}, out.list...)
	in.Components, _ = json.Marshal(comps)
	in.Data, _ = json.Marshal(composed)

	// Its Proposals answer in place, tainted or not: code bound them to the
	// thread's cards, each showing its card's own text and taint, as the
	// queue's tile would; only a part written after reading external
	// content can only ask (answerOf, surface_actions.go).
	in.answers = true
	// Its one line, for a chip: what its headline or its items say, else
	// its steps.
	in.summary = threadSays(headline, items.seen, lang)
	if in.summary == "" || !s.active() {
		in.summary = threadLine(s, steps, asks, lang)
	} else if headline != nil && len(headline.Tainted) > 0 {
		in.summary += threadExternalSep + w("thread.surface.external") // someone else's words may be in it
	}
	in.folds = s.active() && s.standing() // a chip while it has nothing for the user (workspace_layout.go)
	in.inside = insideOf(s)               // a child opens inside its parent (thread_children.go)
	in.composed = true
	return in, true
}

// threadLine is a thread's one-line summary, for a small tile or a chip:
// the last step done, then what waits (« Fusion ✓ · Déploiement à toi »).
func threadLine(s Thread, steps []threadSurfaceStep, asks []int64, lang string) string {
	if !s.active() {
		return tr(lang, "thread.line."+s.State)
	}
	var parts []string
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].State == "done" {
			parts = append(parts, steps[i].Label+" ✓")
			break
		}
	}
	next := ""
	for _, st := range steps {
		if st.Waits {
			next = fmt.Sprintf(tr(lang, "thread.line.yours"), st.Label)
			break
		}
	}
	if next == "" && len(asks) > 0 {
		next = tr(lang, "thread.line.asks")
	}
	if next == "" {
		for _, st := range steps {
			if st.State == "current" {
				next = st.Label + " …"
				break
			}
		}
	}
	if next == "" && len(parts) == 0 {
		next = tr(lang, "thread.state."+s.Look())
	}
	if next != "" {
		parts = append(parts, next)
	}
	return truncate(strings.Join(parts, threadExternalSep), threadLineMax)
}

// keepThreadSurface writes a thread's surface as its data says, when what
// it shows changed: the hook of each change of a thread. An active thread
// always has its surface; an ended one keeps it until its time is over,
// and gets it again only when a link shows it again (showThreadAgain). A
// child's change writes its parent's next, which shows it.
func (c *core) keepThreadSurface(id int64) {
	if c.conf() == nil {
		return // not configured yet: its tick gives it its surface
	}
	c.keepThreadKin(id, c.writeThreadSurface(id)) // thread_children.go
}

// writeThreadSurface is keepThreadSurface for one thread; it tells
// whether the thread has its surface after.
func (c *core) writeThreadSurface(id int64) (has bool) {
	sid := threadSurfaceID(id)
	defer func() {
		_, err := c.surface(sid)
		has = err == nil
	}()
	threadSurfaceMu.Lock()
	defer threadSurfaceMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return
	}
	_, err = c.surface(sid)
	exists := err == nil
	if !s.active() && !exists && c.threadAgain(id).IsZero() {
		return
	}
	// A thread has no steps nor asks any more (node_steps.go): its
	// surface shows its part, its sources and its cards.
	var steps []threadSurfaceStep
	var asks []int64
	part := c.threadPartOf(id)
	in, ok := c.threadSurfaceIn(s, steps, asks, part, time.Now())
	if !ok {
		if exists {
			c.surfaceRemove(sid, surfaceByLifetime)
		}
		return
	}
	key := threadShownKey + strconv.FormatInt(id, 10)
	shown := shownHash(in)
	if exists && c.store.get(key) == shown {
		return
	}
	_, errs, err := c.writeSurface(threadAuthor(id), in)
	if (err != nil || len(errs) > 0) && part != nil {
		// Each block was checked alone: should one still not hold with the
		// rest (a source not read yet, or failing), the surface is written
		// without the blocks the errors point at, else without the part,
		// and their author told.
		why := fmt.Sprintf("%v", err)
		if len(errs) > 0 {
			raw, _ := json.Marshal(errs)
			why = string(raw)
		}
		out := part.blocksAt(in.blockAt, errs)
		kept := part.without(out)
		in, _ = c.threadSurfaceIn(s, steps, asks, kept, time.Now())
		_, errs, err = c.writeSurface(threadAuthor(id), in)
		if (err != nil || len(errs) > 0) && kept != nil {
			out = part.names()
			in, _ = c.threadSurfaceIn(s, steps, asks, nil, time.Now())
			_, errs, err = c.writeSurface(threadAuthor(id), in)
		}
		// A source of the thread not read yet (or failing) holds them
		// quietly until it is: nothing for their author to fix.
		if err == nil && len(errs) == 0 && !c.sourceUnread(id) {
			c.surfaceError(sid, a2ui.Error{Code: a2ui.CodeValidationFailed, SurfaceID: sid,
				Message: "the blocks " + strings.Join(out, ", ") + " of the part are left out of the thread's surface: " + truncate(why, 400)})
		}
	}
	if err != nil || len(errs) > 0 {
		log.Printf("thread %d: its surface: %v %v", id, err, errs)
		return
	}
	c.store.set(key, shown)
	return
}

// shownHash is what a surface written by code shows, as a hash.
func shownHash(in surfaceIn) string {
	raw, _ := json.Marshal([]any{in.Title, in.Why, in.Weight, in.Lifetime, string(in.Components), string(in.Data), in.Tainted, in.codeTaint, in.answers, in.summary, in.state, in.fresh, in.folds, in.inside})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// keepThreadSurfaces gives each active thread its surface, should it have
// none (made before, or removed): the engine's tick.
func (c *core) keepThreadSurfaces() {
	ss, err := c.threadsWhere(activeThreads + ` ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return
	}
	due := c.threadsDueBy(time.Now().Add(dueSoon))
	for _, s := range ss {
		if sf, err := c.surface(threadSurfaceID(s.ID)); errors.Is(err, errNoSurface) || (err == nil && sf.Author != threadAuthor(s.ID)) {
			c.keepThreadSurface(s.ID)
		} else if err == nil && due[s.ID] && sf.Weight < weightUrgent {
			c.keepThreadSurface(s.ID) // a note's due time came close: it turns urgent
		} else if err == nil && (sf.Look.Folds != s.standing() || sf.Why != "") {
			c.keepThreadSurface(s.ID) // written before it said whether it folds, or before it had no why; or its ends changed
		}
	}
}

// threadButtonOf is the context a code's button of a thread's surface
// carries for an action (control): the clicked Button is code's
// (its id starts with thread_), on its thread's own surface, carries that
// action for that thread, and the page sends its context unchanged.
func threadButtonOf(s surface, a a2ui.Action, name string) (int64, map[string]any, *errActionRefused) {
	thread, ok := threadOfAuthor(s.Author)
	comp, at, found := componentOf(s, a.SourceComponentID)
	if !ok || !found || comp["component"] != "Button" || !strings.HasPrefix(a.SourceComponentID, threadCompPrefix) {
		return 0, nil, &errActionRefused{Path: at, Message: name + " comes from a thread's own button, on its surface"}
	}
	act, _ := comp["action"].(map[string]any)
	ev, _ := act["event"].(map[string]any)
	carried, _ := ev["context"].(map[string]any)
	n, _ := carried["thread"].(float64)
	if ev["name"] != name || int64(n) != thread {
		return 0, nil, &errActionRefused{Path: at + "/action", Message: "this component carries no " + name}
	}
	if len(a.Context) != len(carried) {
		return 0, nil, &errActionRefused{Path: at + "/action", Message: "a " + name + "'s context is the one its button carries"}
	}
	for k, want := range carried {
		var got any
		if json.Unmarshal(a.Context[k], &got) != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			return 0, nil, &errActionRefused{Path: at + "/action", Message: "a " + name + "'s context is the one its button carries"}
		}
	}
	return thread, carried, nil
}

// controlOf is what a button of a thread's surface does to it: stop.
func controlOf(s surface, a a2ui.Action) (int64, string, *errActionRefused) {
	thread, ctx, refused := threadButtonOf(s, a, a2ui.ActionControl)
	if refused != nil {
		return 0, "", refused
	}
	do, _ := ctx["do"].(string)
	if !slices.Contains(threadControls, do) {
		return 0, "", &errActionRefused{Message: "no control " + strconv.Quote(do)}
	}
	return thread, do, nil
}

// threadControls are what the user does to a thread.
var threadControls = []string{"stop"}

// controlThread does a control the user clicked.
func (c *core) controlThread(ctx context.Context, thread int64, do string) error {
	return c.stopThread(ctx, thread, byUser)
}

// threadTidyID is the id of « Ranger » on an ended thread's surface.
const threadTidyID = "thread_tidy"

// tidyOf is the thread a « Ranger » puts away: the one the clicked Button
// carries as code wrote it, on its thread's surface.
func tidyOf(s surface, a a2ui.Action) (int64, *errActionRefused) {
	thread, ok := threadOfAuthor(s.Author)
	comp, at, found := componentOf(s, a.SourceComponentID)
	if !ok || !found || comp["component"] != "Button" || a.SourceComponentID != threadTidyID {
		return 0, &errActionRefused{Path: at, Message: "tidy comes from a thread's own « Ranger », on its surface"}
	}
	act, _ := comp["action"].(map[string]any)
	ev, _ := act["event"].(map[string]any)
	ctx, _ := ev["context"].(map[string]any)
	n, _ := ctx["thread"].(float64)
	if ev["name"] != a2ui.ActionTidy || int64(n) != thread {
		return 0, &errActionRefused{Path: at + "/action", Message: "this component carries no tidy"}
	}
	var sent int64
	json.Unmarshal(a.Context["thread"], &sent)
	if sent != thread || len(a.Context) != 1 {
		return 0, &errActionRefused{Path: at + "/action", Message: `a tidy's context is the one its button carries: {"thread": id}`}
	}
	return thread, nil
}

