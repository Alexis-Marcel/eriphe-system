package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The last exchange is the user's last message and Eriphe's lines after
// it: never an event's line nor an error's raw text, the last few replies;
// and whether lines come before it.
func TestLastExchange(t *testing.T) {
	tw := newTestWeb(t)
	st := tw.c.store
	if got, earlier, err := st.lastExchange(""); err != nil || len(got) != 0 || earlier {
		t.Fatalf("empty conversation: %+v %v %v", got, earlier, err)
	}
	st.addMessage(roleBrain, "Bonjour Sam.", true)
	if got, earlier, _ := st.lastExchange(""); len(got) != 1 || got[0].Role != roleBrain || earlier {
		t.Fatalf("no message of the user's: Eriphe's last line, %+v", got)
	}
	st.addMessage(roleUser, "avant", true)
	st.addMessageVia(roleUser, "mets l'agenda en grand", "Telegram", true)
	st.addMessage(roleEvent, "<b>du dehors</b>", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	for _, s := range []string{"un", "deux", "trois"} {
		st.addMessage(roleBrain, s, true)
	}
	tainted, _ := st.addMessage(roleBrain, "lu dans un mail", true)
	st.set(taintKey(int(tainted)), "brain:task:t-1")
	st.addMessageVia(roleUser, "réglage", firstRunVia, true) // the first run's, hidden
	got, earlier, err := st.lastExchange("")
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, l := range got {
		parts = append(parts, l.Role+":"+l.Text+":"+l.Via)
	}
	if strings.Join(parts, " ") != "user:mets l'agenda en grand:Telegram error:: brain:un: brain:deux: brain:trois: brain:lu dans un mail:" || !earlier {
		t.Fatalf("exchange %v, earlier %v", parts, earlier)
	}
	if !got[len(got)-1].External || got[len(got)-2].External {
		t.Errorf("the line of a tainted turn is not marked: %+v", got)
	}
	st.addMessage(roleUser, "et demain ?", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	got, _, _ = st.lastExchange("")
	if len(got) != 2 || got[1].Role != roleError || got[1].Text != "" || got[1].Via != "" {
		t.Fatalf("an error, without its text: %+v", got)
	}
	// Many events and lines since: Eriphe's newest lines still come, the
	// last few.
	for range 60 {
		st.addMessage(roleEvent, "un événement", true)
		st.addMessage(roleBrain, "encore", true)
	}
	st.addMessage(roleBrain, "enfin", true)
	got, _, _ = st.lastExchange("")
	if len(got) != 1+exchangeReplies || got[0].Text != "et demain ?" || got[len(got)-1].Text != "enfin" {
		t.Fatalf("after many lines: %+v", got)
	}
}

// The lines before the last exchange come a few at a time, oldest first,
// with the same filter: GET /api/espace/earlier?before=<seq>.
func TestEarlierLines(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	st := tw.c.store
	var first int64
	for i := range 2*exchangeEarlier + 3 {
		seq, _ := st.addMessage(roleUser, fmt.Sprintf("m%d", i), true)
		if i == 0 {
			first = seq
		}
		st.addMessage(roleEvent, "<b>du dehors</b>", true)
	}
	st.addMessageVia(roleUser, "réglage", firstRunVia, true)
	last, _ := st.addMessage(roleUser, "la dernière", true)
	read := func(before int) earlierView {
		t.Helper()
		code, body, _ := tw.do("GET", fmt.Sprintf("/api/espace/earlier?before=%d", before), nil, nil)
		var v earlierView
		if code != http.StatusOK || json.Unmarshal([]byte(body), &v) != nil {
			t.Fatalf("GET earlier: %d %s", code, body)
		}
		return v
	}
	v := read(int(last))
	if len(v.Lines) != exchangeEarlier || !v.More || v.Lines[0].Text != fmt.Sprintf("m%d", exchangeEarlier+3) || v.Lines[len(v.Lines)-1].Text != fmt.Sprintf("m%d", 2*exchangeEarlier+2) {
		t.Fatalf("first page %+v", v)
	}
	v = read(v.Lines[0].Seq)
	v = read(v.Lines[0].Seq)
	if len(v.Lines) != 3 || v.More || v.Lines[0].Seq != int(first) {
		t.Fatalf("last page %+v", v)
	}
	for _, bad := range []string{"", "x", "0"} {
		if code, _, _ := tw.do("GET", "/api/espace/earlier?before="+bad, nil, nil); code != http.StatusBadRequest {
			t.Errorf("before=%q: %d", bad, code)
		}
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
