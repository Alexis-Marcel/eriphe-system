package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleEvents streams what changes to an open page, as Server-Sent
// Events of ready-made HTML: "message" (new messages, to append), "draft"
// (the brain's message being written, empty once it has landed), "tasks"
// (the side panel), "pending" (the requests at the end of the
// conversation) and "cards" (cards that changed), each sent only when it
// differs from the last one.
//
// Asked with a2ui=1 (the graph workspace, the home page), it sends none of
// that HTML but JSON:
//   - "node": the graph's rows (graphEvNode, web_graph.go), all of them at
//     once (reset) and when the stream fell behind, then those that
//     changed, and the ids of those that left the graph (gone: archived);
//   - "doc": a node's new version ({id, version}), or its trace that grew
//     ({id, trace: true}): a page shown reads it again;
//   - "progress": a task's line of where its node's work is
//     ({node, text, at});
//   - "source": a live block's new value ({node, block, name, value}), at
//     /<name> of the block's data, as the page draws it;
//   - "show": the brain asks to open a node's page ({node, by, at, seq}),
//     the last request at once, then each new one;
//   - "exchange": the conversation's last exchange of the node the console
//     talks to (its id in the node query; exchangeEvent, web_console.go),
//     at once and whenever it changes;
//   - "proposals": the refs of the proposals whose card was made or
//     decided since, all of them first (proposalsEvent, card_moves.go),
//     for what shows them to read them again.
//
// Every event carries the last message's seq as its id, so a reconnecting
// browser picks up where it left. Both send "notice": the notifications
// routed to their tab (its id in the tab query, as the tab tells its
// presence, web_push.go), JSON.
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
	// The graph, for the workspace: its rows, then what changes.
	var graphSeq int64
	var graphNext <-chan struct{} // nil when not asked: never ready
	workspace := r.URL.Query().Get("a2ui") == "1"
	talked := w.c.nodeSaid(r.URL.Query().Get("node")) // the node the console talks to: its exchange
	if workspace {
		// The workspace, the home page, does not show the conversation's
		// messages: its stream starts after the last one, not with them all,
		// and its "exchange" event says the last exchange.
		if after == 0 {
			after = int(w.c.store.lastMessageSeq())
		}
		graphSeq, graphNext = w.sendGraph(rw, after)
		if x, ok := w.c.lastShown(); ok {
			if raw, err := json.Marshal(x); err == nil {
				writeEvent(rw, "show", after, string(raw))
			}
		}
		sendProposals()
		flusher.Flush()
	}

	var lastTasks, lastPending, lastDraft string
	lastCards := map[int64]string{}
	// The workspace's exchange: made again when a message, the draft or
	// Eriphe's state changed, sent when it differs.
	var lastExchange, exchangeDraft string
	thinking, exchangeThinking, exchangeStale := false, false, true
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
		switch {
		case fresh && workspace:
			after = msgs[len(msgs)-1].Seq
			exchangeStale = true
		case fresh:
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
		if !workspace {
			if html, err := w.fragment(lang, "home", "draft", draft); err == nil && html != lastDraft {
				writeEvent(rw, "draft", after, html)
				lastDraft, sent = html, true
			}
		}
		// The panels cost more: every other second, or at once when woken.
		// The workspace has none: Eriphe's state is all it reads of them.
		panels := woken || fresh || time.Since(panelsAt) > 1500*time.Millisecond
		if panels {
			panelsAt, woken = time.Now(), false
		}
		if panels && workspace {
			thinking = w.brainState() == "thinking"
		} else if panels {
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
		if workspace && (exchangeStale || draft != exchangeDraft || thinking != exchangeThinking) {
			// The exchange of the node the console talks to (node_turn.go).
			if x, err := w.exchangeFor(talked, w.c.turn.talkNode(), draft, thinking); err == nil {
				exchangeStale, exchangeDraft, exchangeThinking = false, draft, thinking
				raw, _ := json.Marshal(x)
				if string(raw) != lastExchange {
					writeEvent(rw, "exchange", after, string(raw))
					lastExchange, sent = string(raw), true
				}
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
		case <-graphNext:
			graphSeq, graphNext = w.sendGraphChanges(rw, after, graphSeq)
			flusher.Flush()
			quiet = time.Now()
		case <-proposalsMoved:
			exchangeStale = true // the cards it carries may have moved
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

// sendGraph sends the graph's rows, all of them ("node", reset): the seq
// of the graph's changes they hold, and a channel closed at the next one.
func (w *web) sendGraph(rw http.ResponseWriter, after int) (int64, <-chan struct{}) {
	seq, next := w.c.graphHead() // before the rows: a change meanwhile is sent again
	rows, err := w.c.graphRows()
	if err != nil {
		rows = []graphNode{}
	}
	raw, _ := json.Marshal(map[string]any{"reset": true, "nodes": rows})
	writeEvent(rw, "node", after, string(raw))
	return seq, next
}

// sendGraphChanges sends what changed on the graph since seq: the rows
// that changed, then the docs, the progress lines, the sources' values and
// the show requests, each once.
func (w *web) sendGraphChanges(rw http.ResponseWriter, after int, seq int64) (int64, <-chan struct{}) {
	evs, last, behind, next := w.c.graphAfter(seq)
	if behind {
		return w.sendGraph(rw, after)
	}
	var rows []graphNode
	gone := []string{}
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Kind != graphEvNode && ev.Kind != graphEvProgress || seen[ev.Node] {
			continue
		}
		seen[ev.Node] = true
		sum, err := w.c.nodes.Get(ev.Node)
		if err != nil || !sum.Archived.IsZero() {
			gone = append(gone, ev.Node)
			continue
		}
		rows = append(rows, w.c.graphRow(sum))
	}
	if len(rows) > 0 || len(gone) > 0 {
		if rows == nil {
			rows = []graphNode{}
		}
		raw, _ := json.Marshal(map[string]any{"nodes": rows, "gone": gone})
		writeEvent(rw, "node", after, string(raw))
	}
	said := map[string]bool{}
	for _, ev := range evs {
		var data any
		key := ev.Kind + " " + ev.Node + " " + ev.Block + " " + ev.Name
		switch ev.Kind {
		case graphEvDoc:
			sum, err := w.c.nodes.Get(ev.Node)
			if err != nil {
				continue
			}
			data = map[string]any{"id": ev.Node, "version": sum.Version}
		case graphEvTrace:
			data = map[string]any{"id": ev.Node, "trace": true}
		case graphEvProgress:
			key += " " + strconv.FormatInt(ev.Seq, 10) // each line counts
			data = map[string]any{"node": ev.Node, "text": ev.Text, "at": ev.At}
		case graphEvSource:
			v, ok := w.c.sourceValue(ev.Node, ev.Block, ev.Name)
			if !ok {
				continue
			}
			data = map[string]any{"node": ev.Node, "block": ev.Block, "name": ev.Name, "value": v}
		case graphEvShow:
			x, ok := w.c.lastShown()
			if !ok {
				continue
			}
			data = x
		default:
			continue
		}
		if said[key] {
			continue
		}
		said[key] = true
		event := ev.Kind
		if event == graphEvTrace {
			event = graphEvDoc
		}
		raw, _ := json.Marshal(data)
		writeEvent(rw, event, after, string(raw))
	}
	return last, next
}
