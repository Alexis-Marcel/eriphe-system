package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCore is a minimal eriphe-core: a conversation, tasks and their events.
type fakeCore struct {
	mu      sync.Mutex
	msgs    []Message
	tasks   []Task
	events  map[string][]Event
	answers []map[string]any
	stopped []string
}

func (f *fakeCore) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			h(w, r)
		})
	}
	reply := func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
	mux.Handle("GET /v1/messages", auth(func(w http.ResponseWriter, r *http.Request) {
		after := atoi(r.URL.Query().Get("after"))
		out := []Message{}
		for _, m := range f.msgs {
			if m.Seq > after {
				out = append(out, m)
			}
		}
		reply(w, out)
	}))
	mux.Handle("POST /v1/messages", auth(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Text string }
		json.NewDecoder(r.Body).Decode(&in)
		seq := len(f.msgs) + 1
		f.msgs = append(f.msgs, Message{Seq: seq, Role: "user", Text: in.Text},
			Message{Seq: seq + 1, Role: "brain", Text: "echo: " + in.Text})
		w.WriteHeader(http.StatusAccepted)
		reply(w, map[string]int{"seq": seq})
	}))
	mux.Handle("GET /v1/tasks", auth(func(w http.ResponseWriter, r *http.Request) {
		out := []Task{}
		for _, t := range f.tasks {
			if r.URL.Query().Get("all") == "1" || !finished(t.State) {
				out = append(out, t)
			}
		}
		reply(w, out)
	}))
	mux.Handle("GET /v1/tasks/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		for _, t := range f.tasks {
			if t.ID == r.PathValue("id") {
				evs := f.events[t.ID]
				n := min(atoi(r.URL.Query().Get("events")), len(evs))
				reply(w, TaskStatus{Task: t, Recent: evs[len(evs)-n:]})
				return
			}
		}
		http.Error(w, "no such task", http.StatusNotFound)
	}))
	mux.Handle("GET /v1/tasks/{id}/events", auth(func(w http.ResponseWriter, r *http.Request) {
		after := atoi(r.URL.Query().Get("after"))
		out := []Event{}
		for _, e := range f.events[r.PathValue("id")] {
			if e.Seq > after {
				out = append(out, e)
			}
		}
		reply(w, out)
	}))
	mux.Handle("POST /v1/tasks/{id}/answer", auth(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		in["id"] = r.PathValue("id")
		f.answers = append(f.answers, in)
		reply(w, map[string]string{"result": "message sent"})
	}))
	mux.Handle("POST /v1/tasks/{id}/stop", auth(func(w http.ResponseWriter, r *http.Request) {
		f.stopped = append(f.stopped, r.PathValue("id"))
		w.WriteHeader(http.StatusAccepted)
		reply(w, map[string]string{"result": "stopping"})
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func TestClient(t *testing.T) {
	now := time.Now()
	f := &fakeCore{
		tasks: []Task{{ID: "t-1", Node: "pc", State: stateIdle}, {ID: "t-2", Node: "pc", State: stateStopped}},
		events: map[string][]Event{"t-1": {
			{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "hi"}},
			{Seq: 2, Time: now, Type: "turn_end", Data: map[string]any{}},
		}},
	}
	c := NewClient(f.server(t).URL+"/", "tok")
	ctx := context.Background()

	seq, err := c.Send(ctx, "hello")
	if err != nil || seq != 1 {
		t.Fatalf("send: %d %v", seq, err)
	}
	msgs, err := c.Messages(ctx, seq, 0)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "echo: hello" {
		t.Fatalf("messages: %+v %v", msgs, err)
	}
	if tasks, _ := c.Tasks(ctx, false); len(tasks) != 1 {
		t.Fatalf("active tasks: %+v", tasks)
	}
	if tasks, _ := c.Tasks(ctx, true); len(tasks) != 2 {
		t.Fatalf("all tasks: %+v", tasks)
	}
	st, err := c.Task(ctx, "t-1", 1)
	if err != nil || st.Task.ID != "t-1" || len(st.Recent) != 1 || st.Recent[0].Seq != 2 {
		t.Fatalf("task: %+v %v", st, err)
	}
	if evs, _ := c.Events(ctx, "t-1", 1, 0); len(evs) != 1 || evs[0].Type != "turn_end" {
		t.Fatalf("events: %+v", evs)
	}
	no := false
	if _, err := c.Answer(ctx, "t-1", "p1", "because", &no); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Answer(ctx, "t-1", "", "go on", nil); err != nil {
		t.Fatal(err)
	}
	if a := f.answers; len(a) != 2 || a[0]["allow"] != false || a[0]["reply"] != "because" {
		t.Fatalf("answers: %+v", a)
	} else if _, set := a[1]["allow"]; set {
		t.Fatalf("allow sent without being set: %+v", a[1])
	}
	if err := c.Stop(ctx, "t-1"); err != nil || len(f.stopped) != 1 {
		t.Fatalf("stop: %v %v", f.stopped, err)
	}

	_, err = c.Task(ctx, "nope", 0)
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusNotFound || api.Msg != "no such task" {
		t.Fatalf("missing task: %v", err)
	}
	bad := NewClient(c.URL, "wrong")
	if _, err := bad.Messages(ctx, 0, 0); !errors.As(err, &api) || api.Status != http.StatusUnauthorized {
		t.Fatalf("bad token: %v", err)
	}
}

func TestSayAndAttach(t *testing.T) {
	now := time.Now()
	f := &fakeCore{
		tasks:  []Task{{ID: "t-1", Node: "pc", Agent: "opus", State: stateStopped, Prompt: "do it"}},
		events: map[string][]Event{"t-1": {{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "done"}}}},
	}
	c := NewClient(f.server(t).URL, "tok")
	var out bytes.Buffer
	if err := say(context.Background(), c, &out, "ping", 5*time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if out.String() != "echo: ping\n" {
		t.Fatalf("say printed %q", out.String())
	}

	out.Reset()
	if err := attach(context.Background(), c, &out, "t-1"); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "t-1 on pc (opus), stopped: do it") || !strings.Contains(s, "agent      done") ||
		!strings.Contains(s, "Task t-1 is stopped.") {
		t.Fatalf("attach printed:\n%s", s)
	}
}

func TestPrintTasks(t *testing.T) {
	var out bytes.Buffer
	printTasks(&out, nil, time.Now())
	if out.String() != "No tasks.\n" {
		t.Fatalf("empty: %q", out.String())
	}
	out.Reset()
	now := time.Now()
	printTasks(&out, []Task{{ID: "t-1", State: stateRunning, Node: "pc", Dir: "https://github.com/o/r.git", Created: now}}, now)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[1], "o/r") || !strings.Contains(lines[1], " - ") {
		t.Fatalf("table:\n%s", out.String())
	}
}

func TestConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Setenv("ERIPHE_URL", "")
	t.Setenv("ERIPHE_TOKEN", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("no configuration: want an error")
	}
	path, err := saveConfig(&Config{URL: "http://core:8800", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || (fi.Mode().Perm() != 0o600 && os.PathSeparator == '/') {
		t.Fatalf("mode: %v %v", fi.Mode(), err)
	}
	c, err := loadConfig()
	if err != nil || c.URL != "http://core:8800" || c.Token != "tok" {
		t.Fatalf("load: %+v %v", c, err)
	}
	t.Setenv("ERIPHE_TOKEN", "other")
	if c, _ := loadConfig(); c.Token != "other" || c.URL != "http://core:8800" {
		t.Fatalf("env override: %+v", c)
	}
}
