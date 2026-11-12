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
)

// The attention queue on the workspace (#422): each open item of the queue
// (attention.go) is a surface written by code (author "core"), with no code
// per kind of item:
//
//   - a card is a Proposal bound to its proposal (kind:id): its Oui / Non
//     answer that proposal and no other (GET /api/proposals/{ref});
//   - any other item (a skill's message, a notification held for the brief,
//     an alert) is an Alert with what Eriphe would say of it, else its why.
//
// Its weight is the item's (queueSurfaceWeight: 4 and 5 are urgent, placed
// first by code), its why the item's. It lives while the item is open:
// syncQueueSurfaces makes the surfaces again from the queue whenever it
// changes (attend and attentionDone kick it) and every queueSyncEvery, so a
// card decided elsewhere leaves too. Its words are in the workspace's
// language (workspaceLang).

const (
	queueSurfacePrefix = "q-"
	queueSyncEvery     = 2 * time.Second
	queueTitleMax      = 80
	workspaceLangKey   = "workspace_lang" // the language the workspace page was last opened in
)

var queueIDPart = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// queueSurfaceID is the surface of an item's ref: q-<kind>-<id>, or a hash
// of the ref when its id is not one a surface's id may hold.
func queueSurfaceID(ref string) string {
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

// workspaceLang is the language the workspace was last opened in: fr or en.
func (c *core) workspaceLang() string {
	if c.store.get(workspaceLangKey) == "en" {
		return "en"
	}
	return "fr"
}

// noteWorkspaceLang keeps the workspace's language; its queue's surfaces
// are written in it again when it changes.
func (c *core) noteWorkspaceLang(lang string) {
	if lang != "en" {
		lang = "fr"
	}
	if c.store.get(workspaceLangKey) != lang {
		c.store.set(workspaceLangKey, lang)
		c.kickWorkspace()
	}
}

// queueSurface is the surface of an open item; false when it has none (a
// card whose proposal is gone).
func (c *core) queueSurface(it attentionItem, lang string) (surfaceIn, bool) {
	in := surfaceIn{ID: queueSurfaceID(it.Ref), Weight: queueSurfaceWeight(it.Weight), Why: truncate(oneLine(it.Why), surfaceWhyMax),
		Lifetime: surfaceLifetime{Kept: true}}
	if it.tainted {
		in.Tainted = []string{"attention"}
	}
	var comp map[string]any
	if raw, ok := strings.CutPrefix(it.Ref, "card:"); ok {
		id, _ := strconv.ParseInt(raw, 10, 64)
		x, err := c.card(id)
		if err != nil {
			return in, false
		}
		if _, _, ok := parseProposalRef(x.Ref); !ok {
			return in, false
		}
		in.Title = tr(lang, "card.kind."+x.Kind)
		comp = map[string]any{"id": "root", "component": "Proposal", "proposalId": x.Ref}
	} else {
		level, title := "info", "espace.queue.note"
		switch {
		case it.Weight >= 5:
			level, title = "error", "espace.queue.urgent"
		case it.Weight >= weightBlocksATask:
			level, title = "warning", "espace.queue.waits"
		}
		text := strings.TrimSpace(it.Say)
		if text == "" {
			text = strings.TrimSpace(it.Why)
		}
		if text == "" {
			return in, false
		}
		in.Title = tr(lang, title)
		comp = map[string]any{"id": "root", "component": "Alert", "level": level, "text": truncate(text, surfaceWhyMax)}
	}
	in.Title = truncate(oneLine(in.Title), queueTitleMax)
	in.Components, _ = json.Marshal([]any{comp})
	return in, true
}

// syncQueueSurfaces makes the queue's surfaces what the queue is: one per
// open item, written again only when it changed, removed once its item
// left.
func (c *core) syncQueueSurfaces() {
	st := &c.layout
	st.syncMu.Lock()
	defer st.syncMu.Unlock()
	lang := c.workspaceLang()
	want := map[string]surfaceIn{}
	for _, it := range c.attentionOpen() {
		if in, ok := c.queueSurface(it, lang); ok {
			want[in.ID] = in
		}
	}
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

// sameQueueSurface tells whether a stored surface already is what in says.
func sameQueueSurface(s surface, in surfaceIn) bool {
	var a, b bytes.Buffer
	if json.Compact(&a, s.Components) != nil || json.Compact(&b, in.Components) != nil {
		return false
	}
	return a.String() == b.String() && s.Title == in.Title && s.Why == in.Why && s.Weight == in.Weight &&
		(len(s.Look.AuthorTaint) > 0) == (len(in.Tainted) > 0)
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
	for {
		c.syncQueueSurfaces()
		c.relayout()
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-tick.C:
		}
	}
}
