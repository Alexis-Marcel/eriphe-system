package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Who keeps the workspace's surfaces (#423). Nothing is recurring by
// default: an author writes what it has the context for, and is woken
// when its surface is wanted.
//
//   - A thread's surface is code's (thread_surface.go): written from the
//     thread's data at each of its changes. Who holds the thread (the
//     brain, or its lead) adds its part to it; a surface.stale about it
//     wakes the lead that wrote that part. What else a lead writes lives
//     while its thread does (while: thread:<id>, its default lifetime);
//     code removes it when the thread ends (dropThreadSurfaces).
//   - A skill writes its surface on its trigger, lent eriphe-workspace in
//     its tools, as skill:<name>. when: surface.stale id=<id> matches it as
//     any event; and a skill lent the workspace is run by a surface.stale
//     about a surface it wrote, whatever its when (queueSkillEvent): a
//     time trigger cannot name an event, and a skill at fixed times must
//     hear it too. Its card and its page say so (skillTrigger.Text).
//   - An author is woken by a surface.stale once per surfaceStaleQuiet a
//     surface: a rewrite the composer still finds stale does not loop.
//   - The brain writes what the user asks; a recurring request is a skill.
//     A surface.missing (what the composer would show and nothing does)
//     reaches it as an event of the conversation: it writes the surface,
//     a skill, or nothing.
//
// The composer emits surface.stale (key and payload id: the surface; it
// is out of date) and surface.missing (payload text: what is missing)
// (workspace_pool.go); code reads their form, never their meaning.

const (
	surfaceMissingQuiet = time.Hour               // the same missing tells the brain once an hour
	surfaceMissingKey   = "surface_missing:"      // + a hash of the text: when it was told, unix
	surfaceMissingMax   = 300                     // runes of the text the brain reads
	taintSurfaceEvent   = "event:surface.missing" // a missing drawn from external content
	surfaceStaleQuiet   = 30 * time.Minute        // a surface's author is woken once in it
	surfaceStaleKey     = "surface_stale:"        // + the surface: the event that woke its author and when, id:unix
)

// staleSurfaceID is the surface a surface.stale is about: its payload's
// id, else its key.
func staleSurfaceID(e journalEvent) string {
	if id, ok := e.Payload["id"].(string); ok && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	return strings.TrimSpace(e.Key)
}

// staleWake is the author a surface.stale wakes: its surface's, once per
// surfaceStaleQuiet a surface; "" for another event, a surface gone, or
// within the quiet. Every listener of one event gets the same answer.
func (c *core) staleWake(e journalEvent) string {
	if e.Kind != evSurfaceStale {
		return ""
	}
	s, err := c.surface(staleSurfaceID(e))
	if err != nil {
		return ""
	}
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	key, id := surfaceStaleKey+s.ID, strconv.FormatInt(e.ID, 10)
	if prev, when, ok := strings.Cut(c.store.get(key), ":"); ok {
		if prev == id {
			return s.Author
		}
		if t, err := strconv.ParseInt(when, 10, 64); err == nil && at.Sub(time.Unix(t, 0)) < surfaceStaleQuiet {
			return ""
		}
	}
	c.store.set(key, id+":"+strconv.FormatInt(at.Unix(), 10))
	return s.Author
}

// surfaceEventHeard listens to the journal for the authors code wakes:
// the brain on a surface.missing; a lead on a surface.stale of a surface
// it wrote, or of its thread's surface when it added the part there (code
// keeps the rest up to date). A skill's are queued with its other events
// (queueSkillEvent).
func (c *core) surfaceEventHeard(e journalEvent) {
	switch e.Kind {
	case evSurfaceMissing:
		c.tellBrainMissing(e)
	case evSurfaceStale:
		author := c.staleWake(e)
		text := "Ta surface « " + staleSurfaceID(e) + " » n'est plus à jour : relis le thread et réécris-la (surface_write, même id)."
		raw, ok := strings.CutPrefix(author, "lead:")
		if n, thread := threadOfAuthor(author); thread {
			p := c.threadPartOf(n)
			if p == nil || p.Author != "lead:"+strconv.FormatInt(n, 10) {
				return // code keeps the rest
			}
			raw, ok = strconv.FormatInt(n, 10), true
			text = "Ta partie de la surface du thread (« " + staleSurfaceID(e) + " ») n'est plus à jour : relis le thread et réécris-la (surface_write, même id), ou retire-la (surface_remove)."
		}
		if !ok {
			return
		}
		n, _ := strconv.ParseInt(raw, 10, 64)
		s, err := c.thread(n)
		if err != nil || !s.active() || !c.hasLead(s) {
			return
		}
		var taint []string
		if e.External {
			taint = []string{"event:" + e.Kind}
		}
		c.queueLead(s, text, taint)
	}
}

// tellBrainMissing tells the brain what the composer found missing, once
// an hour for the same text: it decides to write a surface, a skill if it
// comes back, or nothing. Its turn is tainted when the event is external.
func (c *core) tellBrainMissing(e journalEvent) {
	text, _ := e.Payload["text"].(string)
	if text = truncate(oneLine(text), surfaceMissingMax); text == "" {
		text = truncate(oneLine(e.Key), surfaceMissingMax)
	}
	if text == "" {
		return
	}
	sum := sha256.Sum256([]byte(strings.ToLower(text)))
	key := surfaceMissingKey + hex.EncodeToString(sum[:8])
	if last, err := strconv.ParseInt(c.store.get(key), 10, 64); err == nil && e.At.Sub(time.Unix(last, 0)) < surfaceMissingQuiet {
		return
	}
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	c.store.set(key, strconv.FormatInt(at.Unix(), 10))
	line := fmt.Sprintf("[espace] Il manque à l'espace de %s : %s", c.conf().User, packQuote(text))
	// Written delivered, then handed to the brain once its taint is kept.
	seq, err := c.store.addMessage(roleEvent, line, true)
	if err != nil {
		log.Printf("surfaces: missing: %v", err)
		return
	}
	if e.External {
		c.store.set(taintKey(int(seq)), taintSurfaceEvent)
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// dropThreadSurfaces removes at once the surfaces whose lifetime ended
// with a thread: its lead's, and those kept while it ran. Its own surface
// stays a while (thread_surface.go).
func (c *core) dropThreadSurfaces(thread int64) {
	id := strconv.FormatInt(thread, 10)
	all, _ := c.surfaces()
	now := time.Now()
	for _, s := range all {
		if s.ID == threadSurfaceID(thread) {
			continue
		}
		if (s.Author == "lead:"+id || s.Lifetime.While == "thread:"+id) && c.surfaceEnded(s, now) {
			c.surfaceRemove(s.ID, surfaceByLifetime)
		}
	}
}

// Proposals waiting to be offered (memory, intentions, rules…) enter the
// attention queue at a low weight, so the composer may show them; the
// brain still offers them in the conversation as before (offerCards).
// Their items, source sourceOffer, are never delivered by a breakpoint nor
// listed to the brain as waiting: offerCards is what offers them.

const (
	sourceOffer           = "offer"
	weightOffered         = 1 // a proposal waiting to be offered
	weightOfferedDue      = 2 // an intention: something due
	attentionNotOffered   = `source != '` + sourceOffer + `'`
	offersAttendedEachMax = 50
)

// attendOffers puts the proposals waiting to be offered in the queue, an
// item each, unless one is there already (a card shown, held, or offered).
func (c *core) attendOffers() {
	c.syncCards()
	queued, err := c.cardsWhere(`seq = 0 AND turn = 0 AND state = ? AND kind != ? ORDER BY id LIMIT ?`, cardPending, cardDigest, offersAttendedEachMax)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, x := range queued {
		if blocksATask(x.Kind) {
			continue // attended at weight 4 when made
		}
		x, p := c.liveCard(x)
		if x.State != cardPending {
			continue
		}
		w := weightOffered
		if x.Kind == cardIntention {
			w = weightOfferedDue
		}
		text := p.Text(c.userLang())
		if text == "" {
			text = x.Text
		}
		c.store.db.Exec(`INSERT INTO attention (ref, weight, why, source, time, tainted, subject) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (ref) DO NOTHING`,
			cardItemRef(x.ID), w, truncate(oneLine(tr(c.userLang(), "card.kind."+x.Kind)+" : "+text), attentionWhyMax), sourceOffer, now, boolInt(p.Tainted != ""), x.Subject)
	}
}
