package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleEvents streams what changes to the graph workspace (web_graph.go),
// as Server-Sent Events of JSON, each sent only when it differs from the
// last one:
//
//   - "node": the graph's rows (graphEvNode, web_graph.go), all of them at
//     once (reset) and when the stream fell behind, then those that
//     changed, and the ids of those that left the graph (gone: archived);
//
//   - "doc": a node's new version ({id, version}), or its trace that grew
//     ({id, trace: true}): a page shown reads it again;
//
//   - "progress": a task's line of where its node's work is
//     ({node, text, at});
//
//   - "show": the brain asks to open a node's page ({node, by, at, seq}),
//     the last request at once, then each new one;
//
//   - "orb": what Eriphe's orb shows, whatever page the bar talks to: its
//     work, its last words and where they belong, the cards and the
//     messages that wait (orbEvent, web_orb.go), at once and whenever it
//     changes;
//
//   - "proposals": the refs of the proposals whose card was made or
//     decided since, all of them first (proposalsEvent, card_moves.go),
//     for what shows them to read them again.
//
//   - "notice": the notifications routed to its tab (its id in the tab
//     query, as the tab tells its presence, web_push.go).
//
// Every event carries the last message's seq as its id, so a reconnecting
// browser takes up where it left.
func (w *web) handleEvents(rw http.ResponseWriter, r *http.Request, s *session) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
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
	// The graph: its rows, then what changes.
	// The workspace does not show the conversation's messages: its stream
	// starts after the last one, not with them all, and its "orb" event
	// says what the orb shows of them.
	if after == 0 {
		after = int(w.c.store.lastMessageSeq())
	}
	graphSeq, graphNext := w.sendGraph(rw, after)
	if x, ok := w.c.lastShown(); ok {
		if raw, err := json.Marshal(x); err == nil {
			writeEvent(rw, "show", after, string(raw))
		}
	}
	sendProposals()
	flusher.Flush()

	// The orb: made again when a message, the draft or Eriphe's state
	// changed, sent when it differs.
	var lastOrb, orbDraft, orbWaiting string
	thinking, orbThinking, orbStale := false, false, true
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	quiet, stateAt, woken := time.Now(), time.Time{}, true
	for {
		sent := false
		// The draft is read before the messages: if one of them replaced it,
		// it is older than that message and not shown (brain_draft.go).
		draft, at, drafted := w.c.draft.get()
		msgs, err := w.c.store.messagesAfter(after, 100)
		fresh := err == nil && len(msgs) > 0
		if fresh {
			after = msgs[len(msgs)-1].Seq
			orbStale = true
		}
		if at < after {
			draft = ""
		}
		// Eriphe's state costs more: every other second, or at once when woken.
		if woken || fresh || time.Since(stateAt) > 1500*time.Millisecond {
			stateAt, woken = time.Now(), false
			thinking = w.brainState() == "thinking"
		}
		// What waits for Eriphe: a delivery or a withdrawal adds no message
		// (messages_waiting.go).
		if k := w.c.waitingKey(thinking); k != orbWaiting {
			orbWaiting, orbStale = k, true
		}
		if orbStale || draft != orbDraft || thinking != orbThinking {
			if x, err := w.orbFor(draft, thinking); err == nil {
				orbStale, orbDraft, orbThinking = false, draft, thinking
				raw, _ := json.Marshal(x)
				if string(raw) != lastOrb {
					writeEvent(rw, "orb", after, string(raw))
					lastOrb, sent = string(raw), true
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
			woken = true // the next round reads Eriphe's state again
		case <-drafted:
		case <-graphNext:
			graphSeq, graphNext = w.sendGraphChanges(rw, after, graphSeq)
			flusher.Flush()
			quiet = time.Now()
		case <-proposalsMoved:
			orbStale = true // the cards it carries may have moved
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
	var facts *statusFacts // read once, for every row that changed
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
		if facts == nil {
			f := w.c.statusFacts("")
			facts = &f
		}
		rows = append(rows, w.c.graphRowWith(sum, *facts))
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
		key := ev.Kind + " " + ev.Node
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
