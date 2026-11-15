package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The visit's lines are the conversation's since the user came back, the
// last few: the user's (not the first run's), Eriphe's and errors without
// their raw text; never an event's line.
func TestVisitLines(t *testing.T) {
	tw := newTestWeb(t)
	st := tw.c.store
	if got, err := st.visitLines(time.Time{}); err != nil || len(got) != 0 {
		t.Fatalf("empty conversation: %+v %v", got, err)
	}
	old, _ := st.addMessage(roleUser, "hier soir", true)
	st.db.Exec(`UPDATE messages SET time = ? WHERE seq = ?`, time.Now().Add(-5*time.Hour).Unix(), old)
	st.addMessage(roleBrain, "Bonjour Sam.", true)
	st.addMessageVia(roleUser, "mets l'agenda en grand", "Telegram", true)
	st.addMessage(roleEvent, "<b>du dehors</b>", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	st.addMessageVia(roleUser, "réglage", firstRunVia, true) // the first run's, hidden
	got, err := st.visitLines(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, l := range got {
		parts = append(parts, l.Role+":"+l.Text+":"+l.Via)
	}
	if strings.Join(parts, " ") != "brain:Bonjour Sam.: user:mets l'agenda en grand:Telegram error::" {
		t.Fatalf("visit %v", parts)
	}
	if all, _ := st.visitLines(time.Time{}); len(all) != 4 || all[0].Text != "hier soir" {
		t.Fatalf("never away: the whole conversation's last lines, %+v", all)
	}
	// Many lines since: the last few.
	for range 60 {
		st.addMessage(roleEvent, "un événement", true)
		st.addMessage(roleBrain, "encore", true)
	}
	st.addMessage(roleBrain, "enfin", true)
	got, _ = st.visitLines(time.Time{})
	if len(got) != exchangeVisitMax || got[len(got)-1].Text != "enfin" {
		t.Fatalf("after many lines: %+v", got)
	}
}

// The workspace's stream sends the exchange when it opens, then again as
// Eriphe writes (the draft) and once its reply lands, as JSON.
func TestWorkspaceStreamExchange(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	seq, _ := tw.c.store.addMessage(roleUser, "quel temps demain ?", true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events?a2ui=1", nil)
	resp, err := tw.hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	exchanges := make(chan exchangeEvent, 20)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
				if event == "message" || event == "draft" {
					t.Errorf("HTML event %s on the workspace's stream", event)
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "exchange" {
				var ev exchangeEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					exchanges <- ev
				}
			}
		}
		close(exchanges)
	}()
	next := func(ok func(exchangeEvent) bool) exchangeEvent {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev, open := <-exchanges:
				if !open {
					t.Fatal("stream closed")
				}
				if ok(ev) {
					return ev
				}
			case <-deadline:
				t.Fatal("no such exchange")
			}
		}
	}
	first := next(func(exchangeEvent) bool { return true })
	if len(first.Lines) != 1 || first.Lines[0].Text != "quel temps demain ?" || first.Draft != "" {
		t.Fatalf("first exchange %+v", first)
	}
	tw.c.draft.set("Demain, il", int(seq))
	next(func(ev exchangeEvent) bool { return ev.Draft == "Demain, il" })
	tw.c.store.addMessage(roleBrain, "Demain, il **pleut**.", true)
	tw.c.draft.set("", int(seq)+1)
	got := next(func(ev exchangeEvent) bool { return len(ev.Lines) == 2 })
	if got.Lines[1].Role != roleBrain || got.Lines[1].Text != "Demain, il **pleut**." || got.Draft != "" {
		t.Fatalf("reply %+v", got)
	}
}
