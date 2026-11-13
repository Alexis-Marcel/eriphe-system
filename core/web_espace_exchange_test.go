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

// The last exchange is the user's last message and Eriphe's lines after
// it: never an event's line nor an error's raw text, the last few replies.
func TestLastExchange(t *testing.T) {
	tw := newTestWeb(t)
	st := tw.c.store
	if got, err := st.lastExchange(); err != nil || len(got) != 0 {
		t.Fatalf("empty conversation: %+v %v", got, err)
	}
	st.addMessage(roleBrain, "Bonjour Sam.", true)
	if got, _ := st.lastExchange(); len(got) != 1 || got[0].Role != roleBrain {
		t.Fatalf("no message of the user's: Eriphe's last line, %+v", got)
	}
	st.addMessage(roleUser, "avant", true)
	st.addMessageVia(roleUser, "mets l'agenda en grand", "Telegram", true)
	st.addMessage(roleEvent, "<b>du dehors</b>", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	for _, s := range []string{"un", "deux", "trois", "quatre"} {
		st.addMessage(roleBrain, s, true)
	}
	st.addMessageVia(roleUser, "réglage", firstRunVia, true) // the first run's, hidden
	got, err := st.lastExchange()
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, l := range got {
		parts = append(parts, l.Role+":"+l.Text+":"+l.Via)
	}
	if strings.Join(parts, " ") != "user:mets l'agenda en grand:Telegram brain:deux: brain:trois: brain:quatre:" {
		t.Fatalf("exchange %v", parts)
	}
	st.addMessage(roleUser, "et demain ?", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	got, _ = st.lastExchange()
	if len(got) != 2 || got[1].Role != roleError || got[1].Text != "" || got[1].Via != "" {
		t.Fatalf("an error, without its text: %+v", got)
	}
	// Many events since: Eriphe's newest line still comes.
	for range 60 {
		st.addMessage(roleEvent, "un événement", true)
	}
	st.addMessage(roleBrain, "enfin", true)
	got, _ = st.lastExchange()
	if len(got) != 3 || got[0].Text != "et demain ?" || got[2].Text != "enfin" {
		t.Fatalf("after many events: %+v", got)
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
