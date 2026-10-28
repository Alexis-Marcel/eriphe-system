package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Task API: what the CLI uses to list, follow and answer worker tasks. The
// handlers are thin: the same methods back the brain's MCP tools.

// taskRoutes registers the task API on mux, behind the bearer token.
func (c *core) taskRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/tasks", bearer(token, http.HandlerFunc(c.handleTasks)))
	mux.Handle("GET /v1/tasks/{id}", bearer(token, http.HandlerFunc(c.handleTask)))
	mux.Handle("GET /v1/tasks/{id}/events", bearer(token, http.HandlerFunc(c.handleTaskEvents)))
	mux.Handle("GET /v1/tasks/{id}/credentials", bearer(token, http.HandlerFunc(c.handleTaskCredentials)))
	mux.Handle("POST /v1/tasks/{id}/answer", bearer(token, http.HandlerFunc(c.handleAnswer)))
	mux.Handle("POST /v1/tasks/{id}/stop", bearer(token, http.HandlerFunc(c.handleStop)))
}

// handleTasks lists worker tasks, the most recent first: active ones only,
// unless ?all=1.
func (c *core) handleTasks(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all")
	tasks, err := c.store.tasks(all != "1" && all != "true", 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []*TaskRow{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

// handleTask returns a task's status with its last ?events=N events (20).
func (c *core) handleTask(w http.ResponseWriter, r *http.Request) {
	n := 20
	if v := r.URL.Query().Get("events"); v != "" {
		fmt.Sscan(v, &n)
	}
	st, err := c.status(r.PathValue("id"), max(n, 0))
	if err != nil {
		taskError(w, err)
		return
	}
	if st.Recent == nil {
		st.Recent = []EventRow{}
	}
	writeJSON(w, http.StatusOK, st)
}

// handleTaskEvents returns a task's events after ?after=N, waiting up to
// ?wait=S seconds (max 120) for new ones. A finished task answers at once.
func (c *core) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, wait := 0, 0
	fmt.Sscan(r.URL.Query().Get("after"), &after)
	fmt.Sscan(r.URL.Query().Get("wait"), &wait)
	deadline := time.Now().Add(time.Duration(min(wait, 120)) * time.Second)
	for {
		t, err := c.store.task(id)
		if err != nil {
			taskError(w, err)
			return
		}
		events, err := c.store.eventsAfter(id, after, 500)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(events) > 0 || finished(t.State) || time.Now().After(deadline) || r.Context().Err() != nil {
			if events == nil {
				events = []EventRow{}
			}
			writeJSON(w, http.StatusOK, events)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// handleAnswer allows or denies a task's pending permission, or replies to
// its question: {permission_id?, reply, allow}.
func (c *core) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PermissionID string `json:"permission_id"`
		Reply        string `json:"reply"`
		Allow        *bool  `json:"allow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	res, err := c.answer(r.Context(), r.PathValue("id"), in.PermissionID, in.Reply, in.Allow, byUser)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": res})
}

func (c *core) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := c.stop(r.Context(), r.PathValue("id")); err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"result": "stopping"})
}

// taskError answers 404 for an unknown task, 409 otherwise: the task is not
// in a state that allows the call, or its node refused it.
func taskError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, errNoTask) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
