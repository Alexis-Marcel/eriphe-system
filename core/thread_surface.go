package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
//   - Code's section: the goal, the state, what waits for the user (the
//     thread's cards as Proposals, a user step's « C'est fait »), the
//     steps as a Timeline (each with its kind, state, time, checks and
//     note), and the summary. Its component ids start with thread_, its
//     data is under /thread.
//   - The holder's part: who holds the thread (the brain, or its lead)
//     may add one with surface_write on thread-<id> (a state block, points
//     to see, a diff, a link). It is kept apart (threadPartKey), checked
//     alone, and shown under code's section, its root renamed thread_part.
//     A new write replaces it, surface_remove takes it away. Composed
//     only, no sources: a change of the thread rewrites the surface at
//     once and never waits for a tool.
//   - Taint, per part: a step whose task read external content, and a
//     part written after reading some, say « extérieur » where they show.
//     For the models that read the workspace, the surface is tainted as
//     soon as one part is (surfaceLook.CodeTaint, AuthorTaint). Its
//     Proposals still answer in place (surfaceLook.Answers) unless the
//     thread itself is tainted: code placed them, each bound to one of
//     the thread's own cards, as the attention queue's (#443).
//   - Small: a one-line summary (surfaceLook.Summary, the page's
//     extensions.eriphe.summary) for a small tile or a chip.
//   - Lifetime: while the thread is active; once done or dropped, until
//     the end of its day (an hour at least), then it leaves.
//
// keepThreadSurface is the hook: the store calls it at each change of a
// thread (setThread, setStep, insertSteps, its cards), and the engine's
// tick for an active thread that has none. It writes only when what the
// surface shows changed. Step kinds plug in through threadSurfaceSteps,
// which maps a step to what the surface shows of it.

const (
	threadSurfacePrefix = "thread-"        // + the thread's id: its surface
	threadCompPrefix    = "thread_"        // code's component ids
	threadDataKey       = "thread"         // code's data, /thread
	threadPartRoot      = "thread_part"    // the holder's part's root, renamed
	threadPartKey       = "thread_part:"   // + the thread's id: the holder's part, JSON
	threadShownKey      = "thread_shown:"  // + the thread's id: a hash of what its surface shows
	threadPartMax       = 24 << 10         // components and data of a part, each
	threadAfterEnd      = time.Hour        // a done thread's surface stays this long at least
	threadLineMax       = 80               // runes of the one-line summary
	threadDetailMax     = 200              // runes of a step's detail
	threadExternalSep   = " · "            // between the parts of a line
	threadWeight        = surfaceWeightDef // an active thread's surface
	threadWeightYours   = 70               // one that waits for the user
	threadWeightEnded   = 30               // one that ended
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
	Card     string    // the ref of its proposal, shown in place: answered there while it waits, then a call's outcome
	Check    string    // ok or failed: what its checks said
	Note     string    // the last word on it
	External bool      // what it reports may come from outside
	Waits    bool      // it waits for the user: its card, their choice, their doing
	Yours    bool      // the user does it and has not yet: « C'est fait »
}

// threadSurfaceSteps maps a thread's steps to what its surface shows. A
// dropped step is not shown.
func (c *core) threadSurfaceSteps(s Thread, steps []Step) []threadSurfaceStep {
	asks := c.pendingAsks(s.ID)
	var out []threadSurfaceStep
	for _, st := range steps {
		if st.Status == stepDropped {
			continue
		}
		v := threadSurfaceStep{Key: st.Key, Label: st.Title, Kind: st.Kind, Note: st.Note}
		switch st.Status {
		case stepAccepted:
			v.State = "done"
		case stepRunning, stepReported, stepVerified:
			v.State = "current"
		case stepRejected, stepBlocked:
			v.State = "failed"
		default:
			v.State = "pending"
		}
		if st.Key == finalStep {
			v.Label = tr(c.userLang(), "thread.surface.final")
		}
		switch {
		case st.Ended > 0:
			v.At = time.Unix(st.Ended, 0)
		case st.Started > 0:
			v.At = time.Unix(st.Started, 0)
		}
		switch {
		case st.Failed:
			v.Check = "failed"
		case st.Status == stepVerified:
			v.Check = "ok"
		}
		// A step's card: while it waits; once decided, only a call's, which
		// then shows how its run went. A card gone is not shown.
		if id, ok := asks[st.Key]; ok {
			v.Card, v.Waits = proposalRef(cardThread, id), true
		} else if ref := c.stepCard(s.ID, st); ref != "" {
			switch state := c.cardState(ref); {
			case state == cardPending:
				v.Card, v.Waits = ref, true
			case state != cardGone && st.Kind == stepCall:
				v.Card = ref
			}
		}
		if st.Kind == stepDeadline && st.DueAt > 0 && st.Ended == 0 {
			v.At = time.Unix(st.DueAt, 0)
		}
		v.External = st.Tainted != "" || (st.Task != "" && c.taskResultTaint(st.Task) != "")
		v.Yours = st.Kind == stepUser && st.Status == stepRunning
		v.Waits = v.Waits || v.Yours || (st.Kind == stepChoice && st.Status == stepRunning)
		out = append(out, v)
	}
	return out
}

// pendingAsks are what a thread asks about a step that still waits, by
// the step's key.
func (c *core) pendingAsks(thread int64) map[string]int64 {
	out := map[string]int64{}
	rows, err := c.store.db.Query(`SELECT id, step FROM thread_asks WHERE thread = ? AND state = ? ORDER BY id`, thread, askPending)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var step string
		if rows.Scan(&id, &step) == nil && step != "" {
			out[step] = id
		}
	}
	return out
}

// wholeThreadAsks are what a thread asks about itself that waits: its
// plan, a change of it, more time.
func (c *core) wholeThreadAsks(thread int64) []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM thread_asks WHERE thread = ? AND state = ? AND step = '' ORDER BY id`, thread, askPending)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
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
		if kind, _, ok := parseProposalRef(ref); ok && kind != cardThread && c.cardState(ref) == cardPending {
			out = append(out, ref)
		}
	}
	return out
}

// threadCardsWaiting are the refs of the cards waiting for the user that
// a thread's surface shows: its own (asks), the ones linked to it, its
// steps'.
func threadCardsWaiting(steps []threadSurfaceStep, asks []int64, linked []string) []string {
	out := make([]string, 0, len(asks)+len(linked))
	for _, id := range asks {
		out = append(out, proposalRef(cardThread, id))
	}
	out = append(out, linked...)
	for _, st := range steps {
		if st.Waits && st.Card != "" {
			out = append(out, st.Card)
		}
	}
	return out
}

// threadShownCards are the cards waiting for the user that an active
// thread's surface shows, by ref, with the thread (the newest, should two
// show one): answered there, they leave the attention queue's surfaces
// (queuePlace, workspace_queue.go).
func (c *core) threadShownCards() map[string]int64 {
	out := map[string]int64{}
	ss, err := c.threadsWhere(activeThreads + ` ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return out
	}
	for _, s := range ss {
		all, err := c.steps(s.ID)
		if err != nil {
			continue
		}
		steps := c.threadSurfaceSteps(s, all)
		for _, ref := range threadCardsWaiting(steps, c.wholeThreadAsks(s.ID), c.linkedWaiting(s.ID, steps)) {
			if _, ok := out[ref]; !ok {
				out[ref] = s.ID
			}
		}
	}
	return out
}

// threadPart is the part the thread's holder added to its surface.
type threadPart struct {
	Author     string          `json:"author"`
	Components json.RawMessage `json:"components"` // its root renamed threadPartRoot
	Data       json.RawMessage `json:"data,omitempty"`
	Tainted    []string        `json:"tainted,omitempty"`
}

func (c *core) threadPartOf(thread int64) *threadPart {
	raw := c.store.get(threadPartKey + strconv.FormatInt(thread, 10))
	if raw == "" {
		return nil
	}
	var p threadPart
	if json.Unmarshal([]byte(raw), &p) != nil || len(p.Components) == 0 {
		return nil
	}
	return &p
}

// threadSurfaceIn is a thread's surface as code writes it; false when an
// ended thread's time on the workspace is over.
func (c *core) threadSurfaceIn(s Thread, steps []threadSurfaceStep, asks []int64, part *threadPart, now time.Time) (surfaceIn, bool) {
	lang, loc := c.userLang(), c.userZone()
	w := func(key string) string { return tr(lang, key) }
	in := surfaceIn{ID: threadSurfaceID(s.ID), Title: truncate(s.Title, surfaceTitleMax), Why: w("thread.surface.why"), Weight: threadWeight}
	if s.active() {
		in.Lifetime.While = "thread:" + strconv.FormatInt(s.ID, 10)
	} else {
		end := s.Ended
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
		in.Lifetime.Until = until.Truncate(time.Second)
		in.Weight = threadWeightEnded
	}

	type comp = map[string]any
	var comps []comp
	var children []string
	add := func(c comp) {
		comps = append(comps, c)
		children = append(children, c["id"].(string))
	}
	data := map[string]any{"goal": s.Goal}

	add(comp{"id": "thread_goal", "component": "Text", "text": map[string]any{"@path": "/thread/goal"}})
	state := w("thread.state." + s.Look())
	if s.active() {
		if s.led() && s.Lead != "" {
			state += threadExternalSep + w("thread.surface.lead")
		} else {
			state += threadExternalSep + w("thread.surface.brain")
		}
	}
	data["state"] = state
	add(comp{"id": "thread_state", "component": "Text", "variant": "caption", "text": map[string]any{"@path": "/thread/state"}})
	if s.Tainted != "" {
		add(comp{"id": "thread_external", "component": "Text", "variant": "caption", "text": w("thread.surface.tainted")})
	}

	// What waits for the user: the thread's own cards, the cards raised for
	// it (thread_turn.go), then its steps'.
	linked := c.linkedWaiting(s.ID, steps)
	yours := len(asks) > 0 || len(linked) > 0
	for _, st := range steps {
		yours = yours || st.Waits
	}
	if yours && s.active() {
		// Its cards leave the attention queue for it: it takes their weight.
		in.Weight = threadWeightYours
		for _, ref := range threadCardsWaiting(steps, asks, linked) {
			kind, _, _ := parseProposalRef(ref)
			in.Weight = max(in.Weight, queueSurfaceWeight(cardWeight(kind)))
		}
		add(comp{"id": "thread_yours", "component": "Text", "variant": "subtitle", "text": w("thread.surface.yours")})
		for _, id := range asks {
			add(comp{"id": "thread_ask_" + strconv.FormatInt(id, 10), "component": "Proposal", "proposalId": proposalRef(cardThread, id)})
		}
		for i, ref := range linked {
			add(comp{"id": "thread_linked_" + strconv.Itoa(i), "component": "Proposal", "proposalId": ref})
		}
		for i, st := range steps {
			n := strconv.Itoa(i)
			switch {
			case !st.Waits:
			case st.Card != "":
				add(comp{"id": "thread_card_" + n, "component": "Proposal", "proposalId": st.Card})
			case !st.Yours:
				add(comp{"id": "thread_wait_" + n, "component": "Text", "text": st.Label})
			default:
				comps = append(comps,
					comp{"id": "thread_user_" + n + "_label", "component": "Text", "text": st.Label},
					comp{"id": "thread_user_" + n + "_done", "component": "Button", "label": w("thread.surface.done"), "icon": "check", "variant": "primary",
						"action": map[string]any{"event": map[string]any{"name": a2ui.ActionStepDone,
							"context": map[string]any{"thread": s.ID, "step": st.Key}}}})
				add(comp{"id": "thread_user_" + n, "component": "Row", "align": "center",
					"children": []string{"thread_user_" + n + "_label", "thread_user_" + n + "_done"}})
			}
		}
	}

	if len(steps) == 0 {
		add(comp{"id": "thread_nosteps", "component": "Text", "variant": "caption", "text": w("thread.surface.nosteps")})
	} else {
		items := make([]any, 0, len(steps))
		for _, st := range steps {
			item := map[string]any{"label": st.Label, "state": st.State}
			if !st.At.IsZero() {
				item["date"] = st.At.In(loc).Format(time.RFC3339)
			}
			var parts []string
			if k := tr(lang, "thread.surface.kind."+st.Kind); k != "thread.surface.kind."+st.Kind {
				parts = append(parts, k)
			}
			if st.Check != "" {
				parts = append(parts, w("thread.surface.check."+st.Check))
			}
			if note := oneLine(st.Note); note != "" {
				parts = append(parts, note)
			}
			if st.External {
				parts = append([]string{w("thread.surface.external")}, parts...)
			}
			if len(parts) > 0 {
				item["detail"] = truncate(strings.Join(parts, threadExternalSep), threadDetailMax)
			}
			items = append(items, item)
		}
		data["steps"] = items
		add(comp{"id": "thread_steps", "component": "Timeline", "items": map[string]any{"@path": "/thread/steps"}})
		// A call's card, once decided, shows how its run went, under the
		// steps.
		for i, st := range steps {
			if st.Card != "" && (!st.Waits || !s.active()) {
				add(comp{"id": "thread_card_" + strconv.Itoa(i), "component": "Proposal", "proposalId": st.Card})
			}
		}
	}
	if sum := strings.TrimSpace(s.Summary); sum != "" {
		data["summary"] = sum
		add(comp{"id": "thread_summary", "component": "Markdown", "text": map[string]any{"@path": "/thread/summary"}})
	}

	composed := map[string]any{}
	if part != nil {
		var list []comp
		json.Unmarshal(part.Components, &list)
		label := w("thread.surface.part.brain")
		if strings.HasPrefix(part.Author, "lead:") {
			label = w("thread.surface.part.lead")
		}
		if len(part.Tainted) > 0 {
			label += threadExternalSep + w("thread.surface.external")
		}
		add(comp{"id": "thread_part_by", "component": "Text", "variant": "caption", "text": label})
		comps = append(comps, list...)
		children = append(children, threadPartRoot)
		json.Unmarshal(part.Data, &composed)
		if composed == nil {
			composed = map[string]any{}
		}
		in.Tainted = part.Tainted
	}
	composed[threadDataKey] = data
	comps = append([]comp{{"id": "root", "component": "Column", "children": children}}, comps...)
	in.Components, _ = json.Marshal(comps)
	in.Data, _ = json.Marshal(composed)

	// Taint, per part: the thread's own, and each step that read outside.
	if s.Tainted != "" {
		in.codeTaint = append(in.codeTaint, strings.Split(s.Tainted, ", ")...)
	}
	for _, st := range steps {
		if st.External {
			in.codeTaint = append(in.codeTaint, "step:"+st.Key)
		}
	}
	in.answers = s.Tainted == ""
	in.summary = threadLine(s, steps, asks, lang)
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
// never gets it again once it left.
func (c *core) keepThreadSurface(id int64) {
	if c.conf() == nil {
		return // not configured yet: its tick gives it its surface
	}
	threadSurfaceMu.Lock()
	defer threadSurfaceMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return
	}
	sid := threadSurfaceID(id)
	old, err := c.surface(sid)
	exists := err == nil
	if exists && old.Author != threadAuthor(id) {
		// Its id taken before threads had their surface (a lead's own):
		// the thread's takes its place.
		c.surfaceRemove(sid, surfaceByLifetime)
		exists = false
	}
	if !s.active() && !exists {
		return
	}
	all, err := c.steps(id)
	if err != nil {
		return
	}
	steps, asks, part := c.threadSurfaceSteps(s, all), c.wholeThreadAsks(id), c.threadPartOf(id)
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
		// The part was checked alone: should it still not hold with code's,
		// code's section is written without it, and its author told.
		why := fmt.Sprintf("%v", err)
		if len(errs) > 0 {
			raw, _ := json.Marshal(errs)
			why = string(raw)
		}
		in, _ = c.threadSurfaceIn(s, steps, asks, nil, time.Now())
		_, errs, err = c.writeSurface(threadAuthor(id), in)
		if err == nil && len(errs) == 0 {
			c.surfaceError(sid, a2ui.Error{Code: a2ui.CodeValidationFailed, SurfaceID: sid, Message: "the part added to the thread's surface is left out: " + truncate(why, 400)})
		}
	}
	if err != nil || len(errs) > 0 {
		log.Printf("thread %d: its surface: %v %v", id, err, errs)
		return
	}
	c.store.set(key, shown)
}

// shownHash is what a surface written by code shows, as a hash.
func shownHash(in surfaceIn) string {
	raw, _ := json.Marshal([]any{in.Title, in.Why, in.Weight, in.Lifetime, string(in.Components), string(in.Data), in.Tainted, in.codeTaint, in.answers, in.summary})
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
	for _, s := range ss {
		if sf, err := c.surface(threadSurfaceID(s.ID)); errors.Is(err, errNoSurface) || (err == nil && sf.Author != threadAuthor(s.ID)) {
			c.keepThreadSurface(s.ID)
		}
	}
}

// mayAddPart tells whether an author holds a thread, and so may add its
// part to the thread's surface: the brain, or its own lead.
func mayAddPart(author string, thread int64) bool {
	return author == surfaceAuthorBrain || author == "lead:"+strconv.FormatInt(thread, 10)
}

// writeThreadPart is surface_write on a thread's surface: the holder's
// part, checked alone, kept apart, then the surface written again with it.
func (c *core) writeThreadPart(who surfaceCaller, thread int64, in surfaceWriteIn) (map[string]any, error) {
	sid := threadSurfaceID(thread)
	if who.only != "" || !mayAddPart(who.author, thread) {
		return nil, fmt.Errorf("the surface %s is the thread's, kept by code: only who holds the thread adds a part to it", sid)
	}
	s, err := c.thread(thread)
	if err != nil {
		return nil, err
	}
	if !s.active() {
		return nil, fmt.Errorf("the thread is %s: its surface takes no new part", s.State)
	}
	if len(in.Sources) > 0 || in.Refresh.Every != 0 {
		return nil, errors.New("a part of a thread's surface is composed: no sources nor refresh (write the values in data, or a surface of your own for live data)")
	}
	if in.Components == nil {
		return nil, errors.New("components are needed: a flat list, one with id root")
	}
	tainted := who.taint()
	var errs []a2ui.ValidationError
	fail := func(i int, msg string) {
		errs = append(errs, a2ui.ValidationError{Code: a2ui.CodeValidationFailed, SurfaceID: sid, Path: "/components/" + strconv.Itoa(i), Message: msg})
	}
	for i, comp := range in.Components {
		id, _ := comp["id"].(string)
		if strings.HasPrefix(id, threadCompPrefix) {
			fail(i, "ids starting with "+threadCompPrefix+" are code's on a thread's surface: name it otherwise")
		}
		if comp["component"] == "Proposal" && len(tainted) > 0 {
			fail(i, "a Proposal placed after reading external content could be answered as something else: the thread's cards are shown by code already")
		}
	}
	if _, ok := in.Data[threadDataKey]; ok {
		return nil, fmt.Errorf("data /%s is code's on a thread's surface: write your values under another key", threadDataKey)
	}
	comps, _ := json.Marshal(in.Components)
	data, _ := json.Marshal(orEmptyMap(in.Data))
	if len(comps) > threadPartMax || len(data) > threadPartMax {
		return nil, fmt.Errorf("a part of components and data of %d KiB at most each", threadPartMax>>10)
	}
	if len(errs) == 0 {
		errs = a2ui.ValidateSurface(a2ui.Surface{ID: sid, Components: comps, DataModel: data})
	}
	if len(errs) == 0 {
		errs = actionGuard(sid, comps, len(tainted) > 0)
	}
	if len(errs) == 0 {
		errs = a2ui.CheckLinks(sid, comps)
	}
	if len(errs) == 0 {
		errs = c.checkBindings(sid, comps, orEmptyMap(in.Data), nil, nil)
	}
	if len(errs) > 0 {
		return nil, invalidSurface(errs)
	}
	for _, comp := range in.Components {
		if comp["id"] == "root" {
			comp["id"] = threadPartRoot
		}
	}
	comps, _ = json.Marshal(in.Components)
	raw, _ := json.Marshal(threadPart{Author: who.author, Components: comps, Data: data, Tainted: slices.Compact(slices.Sorted(slices.Values(tainted)))})
	c.store.set(threadPartKey+strconv.FormatInt(thread, 10), string(raw))
	c.keepThreadSurface(thread)
	return map[string]any{"id": sid, "author": threadAuthor(thread), "part": true,
		"note": "Your part is on the thread's surface, under what code keeps there (its steps, its state, its cards). Writing it again replaces it; surface_remove takes it away. Its title, weight, why and lifetime are code's."}, nil
}

// removeThreadPart is surface_remove on a thread's surface: the holder's
// part goes, the surface stays.
func (c *core) removeThreadPart(who surfaceCaller, thread int64) (map[string]any, error) {
	if who.only != "" || !mayAddPart(who.author, thread) {
		return nil, fmt.Errorf("the surface %s is the thread's, kept by code while it lives", threadSurfaceID(thread))
	}
	c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, threadPartKey+strconv.FormatInt(thread, 10))
	c.keepThreadSurface(thread)
	return map[string]any{"removed": "part", "note": "Your part is gone; the thread's surface stays while the thread does."}, nil
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// stepDoneOf is the step a « C'est fait » settles: the one the clicked
// Button carries as code wrote it, on its thread's surface, a step the
// user does that waits.
func (c *core) stepDoneOf(s surface, a a2ui.Action) (int64, string, *errActionRefused) {
	thread, ok := threadOfAuthor(s.Author)
	comp, at, found := componentOf(s, a.SourceComponentID)
	if !ok || !found || comp["component"] != "Button" || !strings.HasPrefix(a.SourceComponentID, threadCompPrefix) {
		return 0, "", &errActionRefused{Path: at, Message: "stepDone comes from a thread's own « C'est fait », on its surface"}
	}
	act, _ := comp["action"].(map[string]any)
	ev, _ := act["event"].(map[string]any)
	ctx, _ := ev["context"].(map[string]any)
	step, _ := ctx["step"].(string)
	n, _ := ctx["thread"].(float64)
	if ev["name"] != a2ui.ActionStepDone || int64(n) != thread || step == "" {
		return 0, "", &errActionRefused{Path: at + "/action", Message: "this component carries no stepDone"}
	}
	var sentThread int64
	var sentStep string
	json.Unmarshal(a.Context["thread"], &sentThread)
	json.Unmarshal(a.Context["step"], &sentStep)
	if sentThread != thread || sentStep != step || len(a.Context) != 2 {
		return 0, "", &errActionRefused{Path: at + "/action", Message: `a stepDone's context is the one its button carries: {"thread": id, "step": key}`}
	}
	st, err := c.step(thread, step)
	if err != nil || st.Kind != stepUser || st.Status != stepRunning {
		return 0, "", &errActionRefused{Path: at, Message: "step " + strconv.Quote(step) + " does not wait for the user"}
	}
	return thread, step, nil
}

// errStepSettled is the answer to « C'est fait » for a step that no
// longer waits for the user: done meanwhile, or its thread ended. 409.
var errStepSettled = errors.New("the step no longer waits for the user")

// settleUserStep settles a step the user says is done, from its thread's
// surface: the engine's userStepDone (thread_steps.go), as from the
// thread's page.
func (c *core) settleUserStep(ctx context.Context, thread int64, step string) error {
	if _, err := c.userStepDone(ctx, thread, step, "user"); err != nil {
		return fmt.Errorf("%w: %v", errStepSettled, err)
	}
	return nil
}
