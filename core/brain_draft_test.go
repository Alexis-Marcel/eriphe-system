package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The brain's node reports its draft with every report: eriphe-core keeps
// it in memory, and stores the message it becomes once, whole.
func TestBrainDraftReport(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{Brain: &BrainConfig{}}, store)
	store.registerNode("pc", hashSecret("node-secret"))
	store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Runtime: "claude-code", State: stateRunning, Node: "pc"})
	store.insertTask(&TaskRow{ID: "t-work", Kind: "worker", Runtime: "claude-code", State: stateRunning, Node: "pc"})
	store.set("brain_task", "t-brain")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/node/tasks/{id}/report", c.handleReport)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	report := func(id string, r nodeReport) {
		t.Helper()
		raw, _ := json.Marshal(r)
		req, _ := http.NewRequest("POST", srv.URL+"/v1/node/tasks/"+id+"/report", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer node-secret")
		req.Header.Set("Eriphe-Node", "pc")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("report: %s", resp.Status)
		}
	}
	brainMessages := func() []string {
		msgs, _ := store.messagesAfter(0, 100)
		var out []string
		for _, m := range msgs {
			if m.Role == roleBrain {
				out = append(out, m.Text)
			}
		}
		return out
	}

	store.addMessage(roleUser, "Hi", true)
	_, _, changed := c.draft.get()
	report("t-brain", nodeReport{State: stateRunning, Draft: "Hel"})
	if text, after, _ := c.draft.get(); text != "Hel" || after != store.lastMessageSeq() {
		t.Fatalf("draft = %q after %d", text, after)
	}
	select {
	case <-changed:
	default:
		t.Error("the draft's change woke no one")
	}
	if msgs := brainMessages(); len(msgs) != 0 {
		t.Fatalf("the draft was stored: %q", msgs)
	}
	if events, _ := store.events("t-brain", 10); len(events) != 0 {
		t.Fatalf("the draft made events: %+v", events)
	}

	// A worker's draft is not the conversation's.
	report("t-work", nodeReport{State: stateRunning, Draft: "worker text"})
	if text, _, _ := c.draft.get(); text != "Hel" {
		t.Fatalf("draft = %q", text)
	}

	// The whole message lands before the draft ends.
	report("t-brain", nodeReport{State: stateIdle, Events: []EventRow{
		{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "Hello."}}}})
	text, after, _ := c.draft.get()
	if msgs := brainMessages(); len(msgs) != 1 || msgs[0] != "Hello." {
		t.Fatalf("brain messages = %q", msgs)
	}
	if text != "" || after != store.lastMessageSeq() {
		t.Fatalf("draft after the message = %q after %d", text, after)
	}
	// The brain's loop finds nothing more to copy.
	bt, _ := store.task("t-brain")
	c.relayBrainOutput(bt)
	if msgs := brainMessages(); len(msgs) != 1 {
		t.Fatalf("copied twice: %q", msgs)
	}
}

// The stream shows the draft as it grows, then the message that replaces
// it, never both; a draft older than the messages a viewer has is stale.
func TestWebDraftStream(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.addMessage(roleUser, "before", true)
	last := tw.c.store.lastMessageSeq()
	tw.c.draft.set("stale draft", last-1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events?after="+strconv.Itoa(last), nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	type event struct{ name, data string }
	events := make(chan event, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var e event
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				e.name = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok {
				e.data += v
			} else if line == "" && e.name != "" {
				events <- e
				e = event{}
			}
		}
		close(events)
	}()
	next := func(name string) string {
		t.Helper()
		for {
			select {
			case e, ok := <-events:
				if !ok {
					t.Fatalf("the stream ended before a %s event", name)
				}
				if e.name == "draft" && strings.Contains(e.data, "stale") {
					t.Fatal("the stream sent a stale draft")
				}
				if e.name == "message" && name == "draft" {
					t.Fatalf("message while a draft was expected: %s", e.data)
				}
				if e.name == name {
					return e.data
				}
			case <-ctx.Done():
				t.Fatalf("no %s event", name)
			}
		}
	}

	time.Sleep(100 * time.Millisecond)
	tw.c.draft.set("Writ", last)
	if d := next("draft"); !strings.Contains(d, "Writ") || !strings.Contains(d, "msg-draft") {
		t.Fatalf("draft = %s", d)
	}
	tw.c.draft.set("Writing **this**", last)
	if d := next("draft"); !strings.Contains(d, "<strong>this</strong>") {
		t.Fatalf("draft = %s", d)
	}
	// What brainWrote does when the message lands.
	seq, _ := tw.c.store.addMessage(roleBrain, "Written.", true)
	tw.c.draft.set("", int(seq))
	if m := next("message"); !strings.Contains(m, "Written.") {
		t.Fatalf("message = %s", m)
	}
	if d := next("draft"); d != "" {
		t.Fatalf("draft after the message = %q", d)
	}
}
