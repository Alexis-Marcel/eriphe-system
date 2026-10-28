package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestAPI serves the task API over a temporary store, without any node.
func newTestAPI(t *testing.T) (*core, *httptest.Server) {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{}, s)
	mux := http.NewServeMux()
	c.taskRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return c, srv
}

// call sends a request with the test token and decodes a JSON answer into out.
func call(t *testing.T, srv *httptest.Server, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func addTask(t *testing.T, c *core, id, state string) {
	t.Helper()
	if err := c.store.insertTask(&TaskRow{ID: id, Node: "pc", Agent: "sonnet", Runtime: "claude-code",
		Dir: "owner/repo", Prompt: "do", State: state}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskList(t *testing.T) {
	c, srv := newTestAPI(t)
	var tasks []TaskRow
	if code := call(t, srv, "GET", "/v1/tasks", "", &tasks); code != 200 || tasks == nil || len(tasks) != 0 {
		t.Fatalf("empty list: %d %v", code, tasks)
	}
	addTask(t, c, "t-1", stateRunning)
	addTask(t, c, "t-2", stateStopped)
	call(t, srv, "GET", "/v1/tasks", "", &tasks)
	if len(tasks) != 1 || tasks[0].ID != "t-1" {
		t.Fatalf("active = %+v", tasks)
	}
	call(t, srv, "GET", "/v1/tasks?all=1", "", &tasks)
	if len(tasks) != 2 {
		t.Fatalf("all = %+v", tasks)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/v1/tasks", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
}

func TestTaskStatusAndEvents(t *testing.T) {
	c, srv := newTestAPI(t)
	addTask(t, c, "t-1", stateWaitingPermission)
	now := time.Now()
	c.store.addEvents("t-1", []EventRow{
		{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "hello"}},
		{Seq: 2, Time: now, Type: "permission", Data: map[string]any{"id": "p1", "tool_name": "Bash"}},
	})

	var st taskStatus
	if code := call(t, srv, "GET", "/v1/tasks/t-1?events=1", "", &st); code != 200 {
		t.Fatalf("status: %d", code)
	}
	if st.Task.ID != "t-1" || st.LastText != "hello" || st.PendingPermission["id"] != "p1" || len(st.Recent) != 1 {
		t.Fatalf("status = %+v", st)
	}
	if code := call(t, srv, "GET", "/v1/tasks/nope", "", nil); code != 404 {
		t.Fatalf("unknown task: %d", code)
	}

	var events []EventRow
	call(t, srv, "GET", "/v1/tasks/t-1/events?after=1", "", &events)
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("events after 1 = %+v", events)
	}

	// A long poll returns as soon as an event arrives.
	go func() {
		time.Sleep(300 * time.Millisecond)
		c.store.addEvents("t-1", []EventRow{{Seq: 3, Time: time.Now(), Type: "state", Data: map[string]any{"state": "idle"}}})
	}()
	start := time.Now()
	call(t, srv, "GET", "/v1/tasks/t-1/events?after=2&wait=10", "", &events)
	if len(events) != 1 || events[0].Seq != 3 || time.Since(start) > 5*time.Second {
		t.Fatalf("long poll = %+v after %s", events, time.Since(start))
	}

	// Nothing new: an empty array once the wait is over, never null.
	var raw json.RawMessage
	call(t, srv, "GET", "/v1/tasks/t-1/events?after=3&wait=1", "", &raw)
	if string(raw) != "[]\n" && string(raw) != "[]" {
		t.Fatalf("no events = %s", raw)
	}
	if code := call(t, srv, "GET", "/v1/tasks/nope/events", "", nil); code != 404 {
		t.Fatalf("unknown task events: %d", code)
	}
}

func TestTaskAnswerAndStop(t *testing.T) {
	c, srv := newTestAPI(t)
	addTask(t, c, "t-1", stateWaitingPermission)
	c.store.addEvents("t-1", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p1"}}})

	// A pending permission needs allow.
	if code := call(t, srv, "POST", "/v1/tasks/t-1/answer", `{"reply":"ok"}`, nil); code != http.StatusConflict {
		t.Fatalf("answer without allow: %d", code)
	}
	if code := call(t, srv, "POST", "/v1/tasks/t-1/answer", `not json`, nil); code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", code)
	}
	if code := call(t, srv, "POST", "/v1/tasks/nope/answer", `{}`, nil); code != 404 {
		t.Fatalf("unknown task answer: %d", code)
	}

	// Stop asks for the task to stop; its node confirms (a pending task stops at once).
	var out map[string]string
	if code := call(t, srv, "POST", "/v1/tasks/t-1/stop", "", &out); code != http.StatusAccepted || out["result"] != "stopping" {
		t.Fatalf("stop: %d %v", code, out)
	}
	var desired string
	c.store.db.QueryRow(`SELECT desired FROM tasks WHERE id = 't-1'`).Scan(&desired)
	if desired != desiredStopped {
		t.Fatalf("desired = %s", desired)
	}
	if code := call(t, srv, "POST", "/v1/tasks/t-1/answer", `{"reply":"more"}`, nil); code != http.StatusConflict {
		t.Fatalf("answer a stopped task: %d", code)
	}
	if code := call(t, srv, "POST", "/v1/tasks/nope/stop", "", nil); code != 404 {
		t.Fatalf("unknown task stop: %d", code)
	}
}
