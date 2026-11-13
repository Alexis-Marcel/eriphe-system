package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The attention queue on the workspace (#422, #457): the open items of the
// queue (attention.go) are shown by surfaces written by code (author
// "core"), grouped by what they ask, with no code per kind of item:
//
//   - the cards whose proposal only asks to keep something: a memory
//     (q-memories, « Souvenirs à garder ») or a reminder (q-reminders,
//     « Rappels à garder »); yes keeps it and runs nothing;
//   - the cards whose proposal does something once accepted (q-decide,
//     « À décider »);
//   - anything else (a skill's message, a notification held for the brief,
//     an alert, a card whose proposal the Proposal cannot show): q-notes,
//     « À voir », an Alert each with what Eriphe would say of it, else its
//     why.
//
// A group is a List of its items, the most important then the newest
// first (queueGroupMax at most), its title counting them all; with one
// item, its title is that item's subject. A card in a group is a Proposal
// bound to its ref in the group's data (/items/*/ref): its buttons answer
// that proposal and no other (answerOf, surface_actions.go). An urgent
// item has a surface of its own, q-<kind>-<id> (queueSurfaceID): weight 4
// or 5 (a card a task waits on, an alert), or a proposal that takes place
// today (proposalSpans). The ids stay the same while a group lives, so the
// composer and the brain keep naming them.
//
// A card an active thread's surface shows (a step's, one linked to it, the
// thread's own: threadShownCards) is answered there and in no surface of
// the queue: the thread's surface takes its weight (threadSurfaceIn). Only
// one that interrupts (weight 5) or takes place today keeps a surface of
// its own as well.
//
// A surface's weight is its first item's (queueSurfaceWeight: 4 and 5 are
// urgent, placed first by code). It lives while it has an item: the user
// removing it takes its items out of the queue; closing it hides it for
// the session, its cards still waiting, its other items ending; an item
// other than a card also ends once seen (queueSweep,
// workspace_gestures.go). syncQueueSurfaces makes the surfaces again from
// the queue whenever it changes (attend and attentionDone kick it) and
// every queueSyncEvery, so a card decided elsewhere leaves too. Its words
// are in the user's language (userLang).

const (
	queueSurfacePrefix = "q-"
	queueSyncEvery     = 2 * time.Second
	queueTitleMax      = 80
	queueSubjectMax    = 60 // a single card's title: its subject, short
	queueGroupMax      = 20 // the items a group lists; its title counts them all

	// The groups' surfaces: one dash, never an item's q-<kind>-<id>.
	queueDecide    = "q-decide"
	queueReminders = "q-reminders"
	queueMemories  = "q-memories"
	queueNotes     = "q-notes"
)

// queueGroupTitle is each group's title, a key of the workspace's words.
var queueGroupTitle = map[string]string{
	queueDecide:    "espace.queue.decide",
	queueReminders: "espace.queue.reminders",
	queueMemories:  "espace.queue.memories",
	queueNotes:     "espace.queue.note",
}

// queueGroupOf is the group of a card, by what its proposal asks: to keep
// a memory, to keep a reminder, or to do something.
func queueGroupOf(kind string) string {
	switch kind {
	case cardMemory:
		return queueMemories
	case cardIntention:
		return queueReminders
	}
	return queueDecide
}

var queueIDPart = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// queueSurfaceID is the surface of an urgent item's ref: q-<kind>-<id>, or
// a hash of the ref when its id is not one a surface's id may hold; the
// surface itself for surface:<id>.
func queueSurfaceID(ref string) string {
	if id, ok := strings.CutPrefix(ref, refSurface+":"); ok {
		return id // an item about a surface is shown by that surface (#423)
	}
	kind, id, _ := strings.Cut(strings.ToLower(ref), ":")
	if !queueIDPart.MatchString(kind) || len(kind) > 16 {
		kind = "item"
	}
	if !queueIDPart.MatchString(id) {
		sum := sha256.Sum256([]byte(ref))
		id = hex.EncodeToString(sum[:6])
	}
	return queueSurfacePrefix + kind + "-" + id
}

// queueEntry is an open item of the queue and the surface that shows it.
type queueEntry struct {
	it      attentionItem
	surface string
	about   bool // shown by the surface it is about, not one of the queue's
	shown   bool // a card whose proposal the Proposal shows (p)
	p       proposal
}

// queuePlan places each open item of the queue, in its order (the
// heaviest, then the newest first).
func (c *core) queuePlan(now time.Time) []queueEntry {
	loc := c.userZone()
	y, m, d := now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	items := c.attentionOpen()
	out := make([]queueEntry, 0, len(items))
	var threads map[string]int64
	if len(items) > 0 {
		threads = c.threadShownCards()
	}
	for _, it := range items {
		out = append(out, c.queuePlace(it, today, threads))
	}
	return out
}

// queuePlace is where an item shows: on the surface of the thread that
// shows it, unless urgent; alone when urgent; in its group otherwise.
// threads are the cards an active thread's surface shows, by ref.
func (c *core) queuePlace(it attentionItem, today time.Time, threads map[string]int64) queueEntry {
	e := queueEntry{it: it}
	if id, ok := strings.CutPrefix(it.Ref, refSurface+":"); ok {
		e.surface, e.about = id, true
		return e
	}
	e.p, e.shown = c.queuedProposal(it.Ref)
	todays := e.shown && c.proposalToday(e.p, today)
	if thread, ok := threads[e.p.ref()]; ok && e.shown && it.Weight < weightInterrupts && !todays {
		e.surface, e.about = threadSurfaceID(thread), true // answered there (thread_surface.go)
		return e
	}
	switch {
	case it.Weight >= weightBlocksATask || todays:
		e.surface = queueSurfaceID(it.Ref)
	case e.shown:
		e.surface = queueGroupOf(e.p.Kind)
	default:
		e.surface = queueNotes
	}
	return e
}

// proposalToday tells whether a proposal takes place on the day that
// starts at today: what it is says so (proposalSpans), never a tool.
func (c *core) proposalToday(p proposal, today time.Time) bool {
	return slices.ContainsFunc(c.proposalSpans(p.Kind, p.ID, today.Location()), func(s proposalSpan) bool { return s.on(today) })
}

// queuedProposal is the proposal of a card's item, when the Proposal
// component can show it; any other card is an Alert.
func (c *core) queuedProposal(ref string) (proposal, bool) {
	raw, ok := strings.CutPrefix(ref, "card:")
	if !ok {
		return proposal{}, false
	}
	id, _ := strconv.ParseInt(raw, 10, 64)
	x, err := c.card(id)
	if err != nil {
		return proposal{}, false
	}
	if _, _, ok := parseProposalRef(x.Ref); !ok {
		return proposal{}, false
	}
	_, p := c.liveCard(x)
	return p, p.Kind != ""
}

// queueSurfaces are the surfaces the queue's items make, by id.
func (c *core) queueSurfaces(plan []queueEntry, lang string) map[string]surfaceIn {
	by := map[string][]queueEntry{}
	for _, e := range plan {
		if !e.about {
			by[e.surface] = append(by[e.surface], e)
		}
	}
	out := map[string]surfaceIn{}
	for id, es := range by {
		if in, ok := c.queueSurface(id, es, lang); ok {
			out[id] = in
		}
	}
	return out
}

// queueSurface is the surface of its items, the first the most important;
// false when it shows nothing.
func (c *core) queueSurface(id string, es []queueEntry, lang string) (surfaceIn, bool) {
	in := surfaceIn{ID: id, Weight: queueSurfaceWeight(es[0].it.Weight), Lifetime: surfaceLifetime{Kept: true}}
	if slices.ContainsFunc(es, func(e queueEntry) bool { return e.it.tainted }) {
		in.Tainted = []string{"attention"}
	}
	_, group := queueGroupTitle[id]
	var comps []any
	switch {
	case !group || len(es) == 1 && !es[0].shown:
		// One item: a Proposal bound to its proposal, or an Alert.
		e := es[0]
		if e.shown {
			in.Title = queueSubject(e, lang)
			comps = []any{map[string]any{"id": "root", "component": "Proposal", "proposalId": e.p.ref()}}
			break
		}
		comp, title, ok := queueAlert("root", e.it)
		if !ok {
			return in, false
		}
		in.Title, in.Why = tr(lang, title), truncate(oneLine(e.it.Why), surfaceWhyMax)
		comps = []any{comp}
	case id == queueNotes:
		// The other items: an Alert each.
		var ids []string
		for _, e := range es {
			if len(ids) == queueGroupMax {
				break
			}
			if comp, _, ok := queueAlert("n"+strconv.Itoa(len(ids)), e.it); ok {
				comps = append(comps, comp)
				ids = append(ids, comp["id"].(string))
			}
		}
		if len(ids) == 0 {
			return in, false
		}
		in.Title = queueCount(tr(lang, queueGroupTitle[id]), len(es))
		comps = append([]any{map[string]any{"id": "root", "component": "List", "children": ids}}, comps...)
	default:
		// Cards that ask the same: a List of Proposals, each bound to its
		// ref in the data.
		items := make([]map[string]string, 0, min(len(es), queueGroupMax))
		for _, e := range es[:min(len(es), queueGroupMax)] {
			items = append(items, map[string]string{"ref": e.p.ref()})
		}
		in.Data, _ = json.Marshal(map[string]any{"items": items})
		in.Title = queueCount(tr(lang, queueGroupTitle[id]), len(es))
		if len(es) == 1 {
			in.Title = queueSubject(es[0], lang)
		}
		comps = []any{
			map[string]any{"id": "root", "component": "List", "children": map[string]string{"path": "/items", "componentId": "item"}},
			map[string]any{"id": "item", "component": "Proposal", "proposalId": map[string]string{"@path": "ref"}},
		}
	}
	in.Title = truncate(oneLine(in.Title), queueTitleMax)
	in.Components, _ = json.Marshal(comps)
	return in, true
}

// ref is a proposal's own ref, kind:id.
func (p proposal) ref() string { return proposalRef(p.Kind, p.ID) }

// queueAlert is the Alert of an item other than a card the Proposal
// shows, and its title's key by its weight; false when it says nothing.
func queueAlert(id string, it attentionItem) (map[string]any, string, bool) {
	level, title := "info", "espace.queue.note"
	switch {
	case it.Weight >= weightInterrupts:
		level, title = "error", "espace.queue.urgent"
	case it.Weight >= weightBlocksATask:
		level, title = "warning", "espace.queue.waits"
	}
	text := strings.TrimSpace(it.Say)
	if text == "" {
		text = strings.TrimSpace(it.Why)
	}
	if text == "" {
		return nil, "", false
	}
	return map[string]any{"id": id, "component": "Alert", "level": level, "text": truncate(text, surfaceWhyMax)}, title, true
}

// queueCount is a group's title: what it holds, and how many.
func queueCount(title string, n int) string {
	if n < 2 {
		return title
	}
	return title + " · " + strconv.Itoa(n)
}

// queueSubject is the title of a card shown alone: its subject, short,
// from what it proposes; its kind when that may come from external content
// or says nothing.
func queueSubject(e queueEntry, lang string) string {
	kind := tr(lang, "card.kind."+e.p.Kind)
	if e.it.tainted || e.p.Tainted != "" {
		return kind
	}
	s, _, _ := strings.Cut(oneLine(e.p.Text(lang)), " · ") // its details (a due date) are the Proposal's
	if r := []rune(s); len(r) > queueSubjectMax {
		cut := string(r[:queueSubjectMax])
		if i := strings.LastIndexByte(cut, ' '); i > len(cut)/2 {
			cut = cut[:i]
		}
		s = strings.TrimRight(cut, " ,;:·-") + "…"
	}
	if s == "" {
		return kind
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// queueItemsOf are the open items a queue's surface shows.
func (c *core) queueItemsOf(id string) []attentionItem {
	if !strings.HasPrefix(id, queueSurfacePrefix) {
		return nil
	}
	var out []attentionItem
	for _, e := range c.queuePlan(time.Now()) {
		if e.surface == id && !e.about {
			out = append(out, e.it)
		}
	}
	return out
}

// queueSurfaceDismissed is the user removing a queue's surface (Settings →
// Surfaces): its items leave the queue, or code would write it again. A
// card stays to be answered in the conversation.
func (c *core) queueSurfaceDismissed(id string) {
	for _, it := range c.queueItemsOf(id) {
		c.attentionDone(it.Ref)
	}
}

// syncQueueSurfaces makes the queue's surfaces what the queue is: one per
// group and per urgent item, written again only when it changed, removed
// once its items left.
func (c *core) syncQueueSurfaces() {
	st := &c.layout
	st.syncMu.Lock()
	defer st.syncMu.Unlock()
	want := c.queueSurfaces(c.queuePlan(time.Now()), c.userLang())
	all, err := c.surfaces()
	if err != nil {
		return
	}
	for _, s := range all {
		if s.Author != surfaceAuthorCore {
			continue
		}
		in, ok := want[s.ID]
		if !ok {
			if err := c.surfaceRemove(s.ID, surfaceAuthorCore); err != nil {
				log.Printf("workspace: %s: %v", s.ID, err)
			}
			continue
		}
		if sameQueueSurface(s, in) {
			delete(want, s.ID)
		}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, errs, err := c.writeSurface(surfaceAuthorCore, want[id]); err != nil || len(errs) > 0 {
			log.Printf("workspace: %s: %v %v", id, err, errs)
		}
	}
}

// sameQueueSurface tells whether a stored surface already is what in says:
// its components, its data (a group's items), title, why, weight and taint.
func sameQueueSurface(s surface, in surfaceIn) bool {
	return compactJSON(s.Components) == compactJSON(in.Components) && compactJSON(s.Data) == compactJSON(in.Data) &&
		s.Title == in.Title && s.Why == in.Why && s.Weight == in.Weight &&
		(len(s.Look.AuthorTaint) > 0) == (len(in.Tainted) > 0)
}

// compactJSON is a value's JSON without its spaces; "" for none (null, an
// empty object), or for what is not JSON.
func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return ""
	}
	if s := b.String(); s != "null" && s != "{}" {
		return s
	}
	return ""
}

// kickWorkspace asks for the queue's surfaces to be made again soon, on
// runWorkspace's goroutine: never inline, its caller may hold a lock.
func (c *core) kickWorkspace() {
	st := &c.layout
	st.mu.Lock()
	if st.kick == nil {
		st.kick = make(chan struct{}, 1)
	}
	k := st.kick
	st.mu.Unlock()
	select {
	case k <- struct{}{}:
	default:
	}
}

func (c *core) workspaceKick() <-chan struct{} {
	st := &c.layout
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.kick == nil {
		st.kick = make(chan struct{}, 1)
	}
	return st.kick
}

// runWorkspace keeps the queue's surfaces and the layout: at once when the
// queue changes, and every queueSyncEvery for what changed under it (a card
// decided elsewhere, the surface opened no longer recent).
func (c *core) runWorkspace(ctx context.Context) {
	kick := c.workspaceKick()
	tick := time.NewTicker(queueSyncEvery)
	defer tick.Stop()
	var sessionChecked time.Time
	for {
		c.queueSweep(time.Now()) // items seen end (workspace_gestures.go)
		c.syncQueueSurfaces()    // writes and removals lay out again themselves
		if c.layoutFocused() {
			c.relayout() // the surface opened may no longer be recent
		} else if time.Since(sessionChecked) >= time.Minute && c.placementActive() {
			sessionChecked = time.Now()
			c.relayout() // the session may have ended: what the user closed and placed goes
		}
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-tick.C:
		}
	}
}
