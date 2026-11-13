package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A task's image goes to eriphe-core under the node's identity, with the
// surface it is for, only for a task lent the workspace, within the limit.
func TestMediaRelay(t *testing.T) {
	var got *http.Request
	var body []byte
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"src":"media:med_x"}`)
	}))
	defer core.Close()

	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1", State: stateRunning, secret: "task-secret", Spec: TaskSpec{Servers: []string{workspaceServer}}}
	other := &Task{ID: "t-2", State: stateRunning, secret: "other-secret", Spec: TaskSpec{Servers: []string{"github"}}}
	n.tasks[task.ID], n.tasks[other.ID] = task, other
	mux := http.NewServeMux()
	mux.Handle("POST "+mediaPath, n.mediaHandler(&state{Core: core.URL, Name: "pc", Secret: "node-secret"}))
	relay := httptest.NewServer(mux)
	defer relay.Close()

	post := func(secret string, data []byte) int {
		req, _ := http.NewRequest("POST", relay.URL+mediaPath+"?surface=s-1&other=x", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "image/png")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	png := []byte("\x89PNG\r\n\x1a\n....")
	if code := post("task-secret", png); code != 200 {
		t.Fatalf("relayed: %d", code)
	}
	if got.URL.Path != "/v1/node/tasks/t-1/media" || got.URL.RawQuery != "surface=s-1" ||
		got.Header.Get("Authorization") != "Bearer node-secret" || got.Header.Get("Eriphe-Node") != "pc" || !bytes.Equal(body, png) {
		t.Fatalf("core got %s?%s %v %q", got.URL.Path, got.URL.RawQuery, got.Header, body)
	}
	got = nil
	if code := post("wrong", png); code != http.StatusUnauthorized || got != nil {
		t.Fatalf("wrong secret: %d", code)
	}
	if code := post("other-secret", png); code != http.StatusForbidden || got != nil {
		t.Fatalf("no workspace: %d", code)
	}
	if code := post("task-secret", make([]byte, mediaMax+1)); code != http.StatusRequestEntityTooLarge || got != nil {
		t.Fatalf("too large: %d", code)
	}
	task.State = stateStopped
	if code := post("task-secret", png); code != http.StatusGone || got != nil {
		t.Fatalf("finished task: %d", code)
	}

	if env := n.mediaEnv(task); env["ERIPHE_MEDIA_URL"] != "http://eriphe-node:8790"+mediaPath {
		t.Fatalf("env = %v", env)
	}
	if env := n.mediaEnv(other); env != nil {
		t.Fatalf("env without the workspace = %v", env)
	}
}
