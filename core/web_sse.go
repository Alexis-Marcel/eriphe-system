package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleEvents streams what changes to an open workspace, as Server-Sent
// Events of ready-made HTML: "message" (new messages, to append), "draft"
// (the brain's message being written, empty once it has landed), "tasks"
// (the side panel) and "pending" (the requests at the end of the
// conversation), each sent only when it differs from the last one. Every
// event carries the last message's seq as its id, so a reconnecting
// browser picks up where it left.
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
	h := rw.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "retry: 3000\n\n")
	flusher.Flush()

	var lastTasks, lastPending, lastDraft string
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
