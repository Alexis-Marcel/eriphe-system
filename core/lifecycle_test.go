package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A task goes through its whole life without eriphe-core ever calling the
// node: the node reads its assignment (with the lent credential), reports,
// receives an answer through the inbox, and is told to stop.
func TestDeclarativeLifecycle(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Model: "sonnet"}},
		DefaultAgent: "sonnet",
		TimeZone:     "Europe/Paris",
		Brain:        &BrainConfig{},
	}
	c := newCore(cfg, store)
	store.registerNode("pc", hashSecret("node-secret"))
	store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/node/assignments", c.handleAssignments)
	mux.HandleFunc("POST /v1/node/tasks/{id}/report", c.handleReport)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(method, path string, in, out any) int {
		var body bytes.Buffer
		if in != nil {
			json.NewEncoder(&body).Encode(in)
		}
		req, _ := http.NewRequest(method, srv.URL+path, &body)
		req.Header.Set("Authorization", "Bearer node-secret")
		req.Header.Set("Eriphe-Node", "pc")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	type assignments struct {
		Rev   int64            `json:"rev"`
		Tasks []nodeAssignment `json:"tasks"`
	}

	task, err := c.runTask(context.Background(), runRequest{Prompt: "count the lines", Repo: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	var a assignments
	call("GET", "/v1/node/assignments?rev=0&wait=0", nil, &a)
	if len(a.Tasks) != 1 || a.Tasks[0].Desired != desiredRunning || a.Tasks[0].Spec.Prompt != "count the lines" ||
		a.Tasks[0].Spec.Identity["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" || a.Tasks[0].Spec.Model != "sonnet" ||
		a.Tasks[0].Spec.TZ != "Europe/Paris" || a.Tasks[0].Spec.AppendSystemPrompt != workerVoice(defaultDisplayName) ||
		a.Tasks[0].Spec.Egress == nil || a.Tasks[0].Spec.Egress.Mode != "internet" {
		t.Fatalf("assignment = %+v", a)
	}
	var stored string
	store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&stored)
	if stored == "" || bytes.Contains([]byte(stored), []byte("tok")) {
		t.Fatalf("spec stored with its secret, or not at all: %q", stored)
	}

	// A watch returns as soon as something changes.
	done := make(chan assignments, 1)
	go func() {
		var b assignments
		call("GET", "/v1/node/assignments?rev="+jsonInt(a.Rev)+"&wait=10", nil, &b)
		done <- b
	}()
	time.Sleep(100 * time.Millisecond)
	now := time.Now()
	call("POST", "/v1/node/tasks/"+task.ID+"/report", nodeReport{State: stateWaitingPermission, SessionID: "s1",
		Events: []EventRow{{Seq: 1, Time: now, Type: "permission", Data: map[string]any{"id": "p1", "tool_name": "Bash"}}}}, nil)
	yes := true
	if _, err := c.answer(context.Background(), task.ID, "", "", &yes, byBrain); err != nil {
		t.Fatal(err)
	}
	if _, err := c.answer(context.Background(), task.ID, "", "", &yes, byBrain); err == nil {
		t.Fatal("answered twice")
	}
	select {
	case b := <-done:
		// The watch woke on the report; the answer is there by now.
		call("GET", "/v1/node/assignments?rev=0&wait=0", nil, &b)
		if len(b.Tasks) != 1 || len(b.Tasks[0].Inbox) != 1 || b.Tasks[0].Inbox[0].Kind != "permission" {
			t.Fatalf("after answer = %+v", b)
		}
		call("POST", "/v1/node/tasks/"+task.ID+"/report", nodeReport{State: stateIdle, Delivered: b.Tasks[0].Inbox[0].Seq,
			Events: []EventRow{{Seq: 2, Time: now, Type: "permission_answer", Data: map[string]any{"id": "p1", "allow": true}},
				{Seq: 3, Time: now, Type: "text", Data: map[string]any{"text": "42 lines"}}}}, nil)
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not wake up")
	}
	st, _ := c.status(task.ID, 10)
	if st.Task.State != stateIdle || st.Task.SessionID != "s1" || st.LastText != "42 lines" || st.PendingPermission != nil {
		t.Fatalf("status = %+v", st.Task)
	}
	if items, _ := store.inbox(task.ID); len(items) != 0 {
		t.Fatalf("inbox not acked: %+v", items)
	}

	c.stopByBrain(context.Background(), task.ID)
	call("GET", "/v1/node/assignments?rev=0&wait=0", nil, &a)
	if a.Tasks[0].Desired != desiredStopped {
		t.Fatalf("desired = %s", a.Tasks[0].Desired)
	}
	call("POST", "/v1/node/tasks/"+task.ID+"/report", nodeReport{State: stateStopped}, nil)
	call("GET", "/v1/node/assignments?rev=0&wait=0", nil, &a)
	if len(a.Tasks) != 0 {
		t.Fatalf("a stopped task is still assigned: %+v", a.Tasks)
	}
	// The brain hears the end of the turn, not the stop it asked for.
	msgs, _ := store.undelivered()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Text, "a fini son tour") {
		t.Fatalf("brain events = %+v", msgs)
	}

	// Another node cannot report on it.
	req, _ := http.NewRequest("POST", srv.URL+"/v1/node/tasks/"+task.ID+"/report", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer wrong")
	req.Header.Set("Eriphe-Node", "pc")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %s", resp.Status)
	}
}

func jsonInt(n int64) string { raw, _ := json.Marshal(n); return string(raw) }
