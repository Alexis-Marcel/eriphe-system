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

// brainReports is a core with a brain task and a worker's on node pc,
// and its node's report endpoint: report sends a report as the node does,
// brainMessages is what the brain said in the conversation.
func brainReports(t *testing.T) (c *core, report func(id string, r nodeReport), brainMessages func() []string) {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c = newCore(&Config{Brain: &BrainConfig{}}, store)
	store.registerNode("pc", hashSecret("node-secret"))
	store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Runtime: "claude-code", State: stateRunning, Node: "pc"})
	store.insertTask(&TaskRow{ID: "t-work", Kind: "worker", Runtime: "claude-code", State: stateRunning, Node: "pc"})
	setBrainTask(c, "t-brain")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/node/tasks/{id}/report", c.handleReport)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	report = func(id string, r nodeReport) {
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
	brainMessages = func() []string {
		msgs, _ := store.messagesAfter(0, 100)
		var out []string
		for _, m := range msgs {
			if m.Role == roleBrain {
				out = append(out, m.Text)
			}
		}
		return out
	}
	return c, report, brainMessages
}

// The brain's node reports its draft with every report: eriphe-core keeps
// it in memory, and stores the message it becomes once, whole.
func TestBrainDraftReport(t *testing.T) {
	c, report, brainMessages := brainReports(t)
	store := c.store

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

	// The whole message comes before its turn's end: it stays the draft
	// until the turn decides it, and is stored when the turn ends on it.
	report("t-brain", nodeReport{State: stateRunning, Events: []EventRow{
		{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "Hello."}}}})
	if text, _, _ := c.draft.get(); text != "Hello." {
		t.Fatalf("draft of the whole message = %q", text)
	}
	if msgs := brainMessages(); len(msgs) != 0 {
		t.Fatalf("stored before its turn decided it: %q", msgs)
	}
	report("t-brain", nodeReport{State: stateIdle, Events: []EventRow{
		{Seq: 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success", "result": "Hello."}}}})
	text, after, _ := c.draft.get()
	if msgs := brainMessages(); len(msgs) != 1 || msgs[0] != "Hello." {
		t.Fatalf("brain messages = %q", msgs)
	}
	if text != "" || after != store.lastMessageSeq() {
		t.Fatalf("draft after the message = %q after %d", text, after)
	}
	// The brain's loop finds nothing more to copy.
	bt, _ := store.task("t-brain")
	c.relayBrainOutput(theBrain(c), bt)
	if msgs := brainMessages(); len(msgs) != 1 {
		t.Fatalf("copied twice: %q", msgs)
	}
}

// A note the brain writes before a tool call, in the middle of its turn,
// is meant for no one (#608): only what it writes after its last tool
// call is its answer. The note comes in a report of its own, the tool
// call and the answer in the next.
func TestBrainWorkingNoteNotShown(t *testing.T) {
	c, report, brainMessages := brainReports(t)
	c.store.addMessage(roleEvent, "[thread 1 « Garage »] A step reported its work.", true)

	report("t-brain", nodeReport{State: stateRunning, Events: []EventRow{
		{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "Need the whole report."}}}})
	if msgs := brainMessages(); len(msgs) != 0 {
		t.Fatalf("an undecided text was stored: %q", msgs)
	}
	report("t-brain", nodeReport{State: stateIdle, Events: []EventRow{
		{Seq: 2, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "mcp__eriphe-core__task_status", "input": map[string]any{"task_id": "t-step"}}},
		{Seq: 3, Time: time.Now(), Type: "text", Data: map[string]any{"text": "The garage can take the car on Tuesday."}},
		{Seq: 4, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success", "result": "The garage can take the car on Tuesday."}}}})
	if msgs := brainMessages(); len(msgs) != 1 || msgs[0] != "The garage can take the car on Tuesday." {
		t.Fatalf("brain messages = %q", msgs)
	}
	if text, _, _ := c.draft.get(); text != "" {
		t.Fatalf("draft after the turn = %q", text)
	}
	lines, _, err := c.store.lastExchange(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Text != "The garage can take the car on Tuesday." {
		t.Fatalf("exchange = %+v", lines)
	}

	// A note and its tool call in one report, a turn that ends on a tool
	// call: nothing for the user.
	report("t-brain", nodeReport{State: stateIdle, Events: []EventRow{
		{Seq: 5, Time: time.Now(), Type: "text", Data: map[string]any{"text": "Checking the step."}},
		{Seq: 6, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "mcp__eriphe-core__settle_step", "input": map[string]any{}}},
		{Seq: 7, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success"}}}})
	if msgs := brainMessages(); len(msgs) != 1 {
		t.Fatalf("brain messages = %q", msgs)
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
