package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleEvents streams what changes to an open workspace, as Server-Sent
// Events of ready-made HTML: "message" (new messages, to append), "draft"
// (the brain's message being written, empty once it has landed), "tasks"
// (the side panel), "pending" (the requests at the end of the
// conversation) and "cards" (cards that changed), each sent only when it
// differs from the last one. Asked with a2ui=1 (the workspace, /espace), it
// also sends "a2ui": the surfaces' A2UI messages in order (JSON, a2uiEvent in
// surfaces_stream.go), a snapshot that resets the page first, and
// "layout": where each surface is on the grid (JSON, workspaceLayout in
// workspace_layout.go), sent after the "a2ui" event of the surfaces it
// names and whenever it changes, and "proposals": the refs of the
// proposals whose card was made or decided since, all of them first
// (JSON, proposalsEvent in surface_proposals.go), for the surfaces that
// show them to read them again. Every
// event carries the last message's seq as its id, so a reconnecting
// browser picks up where it left; its surfaces start again from a
// snapshot.
func (w *web) handleEvents(rw http.ResponseWriter, r *http.Request, s *session) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	if after == 0 {
		after, _ = strconv.Atoi(r.URL.Query().Get("after"))
	}
	lang := w.lang(r)
	tab := r.URL.Query().Get("tab") // the tab's id, as it tells its presence (web_push.go)
	if len(tab) > 64 {
		tab = ""
	}
	h := rw.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "retry: 3000\n\n")
	flusher.Flush()

	// The surfaces, for the workspace: a snapshot, then their changes.
	var feed *surfaceFeed
	var surfaces <-chan surfaceChange // nil when not asked: never ready
	sendSurfaces := func(ev a2uiEvent) {
		if raw, err := json.Marshal(ev); err == nil {
			writeEvent(rw, "a2ui", after, string(raw))
		}
		surfaces = feed.changes // a new subscription after a reset
	}
	// The layout of the surfaces, after them: sent when it changed.
	var layoutChanged <-chan struct{} // nil when not asked: never ready
	lastLayout := ""
	sendLayout := func() {
		l, ch := w.c.watchLayout()
		layoutChanged = ch
		if raw, err := json.Marshal(l); err == nil && string(raw) != lastLayout {
			writeEvent(rw, "layout", after, string(raw))
			lastLayout = string(raw)
		}
	}
	// The proposals that moved, for what shows them: their refs. The first
	// one of a connection says all: what the page shows may have been
	// decided while it was away.
	var proposalsMoved <-chan struct{} // nil when not asked: never ready
	var proposalsSeq int64
	sendProposals := func() {
		ev, seq, ch := w.c.watchProposals(proposalsSeq)
		if proposalsMoved == nil {
			ev = proposalsEvent{Refs: []string{}, All: true}
		}
		if ev.All || len(ev.Refs) > 0 {
			if raw, err := json.Marshal(ev); err == nil {
				writeEvent(rw, "proposals", after, string(raw))
			}
		}
		proposalsSeq, proposalsMoved = seq, ch
	}
	if r.URL.Query().Get("a2ui") == "1" {
		var first a2uiEvent
		feed, first = w.c.feedSurfaces()
		defer feed.close()
		sendSurfaces(first)
		sendLayout()
		sendProposals()
		flusher.Flush()
	}

	var lastTasks, lastPending, lastDraft string
	lastCards := map[int64]string{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	quiet, panelsAt, woken := time.Now(), time.Time{}, true
	for {
		sent := false
		// The draft is read before the messages: if one of them replaced it,
		// it is older than that message and not shown (brain_draft.go).
		draft, at, drafted := w.c.draft.get()
		msgs, err := w.c.store.messagesAfter(after, 100)
		fresh := err == nil && len(msgs) > 0
		if fresh {
			var b strings.Builder
			w.dress(msgs)
			w.dressCards(msgs, lang)
			for _, m := range msgs {
				html, err := w.fragment(lang, "home", "message", m)
				if err != nil {
					return
				}
				b.WriteString(html)
				after = m.Seq
			}
			writeEvent(rw, "message", after, b.String())
			sent = true
		}
		if at < after {
			draft = ""
		}
		if html, err := w.fragment(lang, "home", "draft", draft); err == nil && html != lastDraft {
			writeEvent(rw, "draft", after, html)
			lastDraft, sent = html, true
		}
		// The panels cost more: every other second, or at once when woken.
		if woken || fresh || time.Since(panelsAt) > 1500*time.Millisecond {
			panelsAt, woken = time.Now(), false
			tasks, perms, err := w.tasksAndPerms()
			if err == nil {
				if html, err := w.fragment(lang, "home", "tasks", tasks); err == nil && html != lastTasks {
					writeEvent(rw, "tasks", after, html)
					lastTasks, sent = html, true
				}
				if html, err := w.fragment(lang, "home", "pending", w.pending(perms)); err == nil && html != lastPending {
					writeEvent(rw, "pending", after, html)
					lastPending, sent = html, true
				}
			}
			// The cards: attached to a message already shown, or decided
			// (here, on a channel, in words): each sent again when it changed.
			var b strings.Builder
			for _, x := range w.liveCards() {
				if x.Seq > after {
					continue // its message comes with it
				}
				html, err := w.fragment(lang, "home", "card", w.cardView(x, lang))
				if err != nil || lastCards[x.ID] == html {
					continue
				}
				lastCards[x.ID] = html
				b.WriteString(html)
			}
			if b.Len() > 0 {
				writeEvent(rw, "cards", after, b.String())
				sent = true
			}
		}
		// A notification routed to this tab, in front of the user (route_send.go).
		if notes := w.c.showInApp(tab); len(notes) > 0 {
			if raw, err := json.Marshal(notes); err == nil {
				writeEvent(rw, "notice", after, string(raw))
				sent = true
			}
		}
		if !sent && time.Since(quiet) > 25*time.Second {
			fmt.Fprint(rw, ": keep-alive\n\n") // through proxies that close idle streams
			sent = true
		}
		if sent {
			flusher.Flush()
			quiet = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		case <-w.waker():
			woken = true // the next round refreshes the panels
		case <-drafted:
		case ch, ok := <-surfaces:
			sendSurfaces(feed.next(ch, ok))
			sendLayout()
			flusher.Flush()
			quiet = time.Now()
		case <-layoutChanged:
			sendLayout()
			flusher.Flush()
			quiet = time.Now()
		case <-proposalsMoved:
			sendProposals()
			flusher.Flush()
			quiet = time.Now()
		}
	}
}

func writeEvent(rw http.ResponseWriter, event string, id int, data string) {
	fmt.Fprintf(rw, "event: %s\nid: %d\n", event, id)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(rw, "data: %s\n", line)
	}
	fmt.Fprint(rw, "\n")
}
