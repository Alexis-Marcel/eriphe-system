package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The node keeps its tasks in line with what eriphe-core wants, like a
// kubelet: it watches its assignments, starts or stops containers to match,
// delivers each task's inbox to its agent, and reports state and events.
// A task's folder (<root>/tasks/<id>) keeps its workspace, its agent's config
// and task.json, so after a restart of the node or of the machine the task
// comes back and resumes its session.

const maxRestarts = 3

// assignment is one task as eriphe-core wants it.
type assignment struct {
	ID      string      `json:"id"`
	Desired string      `json:"desired"` // running or stopped
	Spec    TaskSpec    `json:"spec"`
	Inbox   []inboxItem `json:"inbox"`
}

type inboxItem struct {
	Seq  int            `json:"seq"`
	Kind string         `json:"kind"` // message or permission
	Data map[string]any `json:"data"`
}

// saved is what a task's folder remembers between runs.
type saved struct {
	SessionID string `json:"session_id,omitempty"`
	Seq       int    `json:"seq"`
	Delivered int    `json:"delivered"`
	Restarts  int    `json:"restarts"`
}

type coreClient struct {
	s    *state
	http *http.Client
}

func (c *coreClient) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.s.base()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.s.Secret)
	req.Header.Set("Eriphe-Node", c.s.Name)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errUnknownTask
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

var errUnknownTask = errors.New("eriphe-core does not know this task")

// sync runs the watch loop and the report loop until the process ends.
func (n *node) sync(s *state) {
	c := &coreClient{s: s, http: s.longClient()}
	n.removeStrayContainers()
	n.removeStrayServices()
	go n.reportLoop(c)
	var rev int64
	backoff := time.Second
	for {
		var out struct {
			Rev      int64               `json:"rev"`
			Tasks    []assignment        `json:"tasks"`
			Services []serviceAssignment `json:"services"` // none from an eriphe-core without services
			Update   *updateRequest      `json:"update"`   // none from an eriphe-core without updates
			Prepare  *prepareSpec        `json:"prepare"`  // what to have ready (inject.go); none from an older eriphe-core
		}
		err := c.do(context.Background(), http.MethodGet, fmt.Sprintf("/v1/node/assignments?rev=%d&wait=30", rev), nil, &out)
		if err != nil {
			log.Printf("assignments: %v; retrying in %s", err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		rev = out.Rev
		n.reconcile(out.Tasks)
		n.reconcileServices(out.Services)
		n.wanted(out.Prepare)
		if n.upd.request(out.Update) {
			n.poke()
		}
	}
}

// removeStrayContainers removes task containers left by a previous run of
// the node: their agent lost its connection, the tasks start again.
func (n *node) removeStrayContainers() {
	n.removeLabelled("eriphe.task")
}

// reconcile makes the local tasks match the assignments.
func (n *node) reconcile(list []assignment) {
	wanted := map[string]bool{}
	for _, a := range list {
		wanted[a.ID] = true
		t := n.get(a.ID)
		switch {
		case t == nil && a.Desired == "running":
			n.launch(a)
		case t == nil:
			// Stopped before it ever ran here: say so, and forget it.
			n.removeDir(a.ID)
			n.adopt(&Task{ID: a.ID, State: stateStopped, perms: map[string]*permission{}})
		case a.Desired == "stopped" && !finished(t.state()):
			go n.stopContainer(t)
		}
		if t := n.get(a.ID); t != nil {
			t.mu.Lock()
			t.inbox = a.Inbox
			t.mu.Unlock()
			n.deliver(t)
		}
	}
	// What eriphe-core no longer wants: stop it, and once it is over and
	// reported, remove it and its folder.
	n.mu.Lock()
	var gone []*Task
	for id, t := range n.tasks {
		if !wanted[id] {
			gone = append(gone, t)
		}
	}
	n.mu.Unlock()
	for _, t := range gone {
		if !finished(t.state()) {
			go n.stopContainer(t)
			continue
		}
		n.mu.Lock()
		delete(n.tasks, t.ID)
		n.mu.Unlock()
		n.removeDir(t.ID)
	}
}

func (n *node) taskDir(id string) string { return filepath.Join(n.opt.root, "tasks", id) }

func (n *node) removeDir(id string) {
	if validName(id) {
		os.RemoveAll(n.taskDir(id))
	}
}

func (n *node) adopt(t *Task) {
	t.onChange = n.poke
	n.mu.Lock()
	n.tasks[t.ID] = t
	n.mu.Unlock()
	n.poke()
}

// launch starts an assigned task, or restarts it from its folder if it ran
// here before.
func (n *node) launch(a assignment) {
	spec := a.Spec
	if spec.Image == "" {
		spec.Image = n.opt.image
	}
	t := &Task{ID: a.ID, Spec: spec, Runtime: spec.Runtime, Image: spec.Image, State: stateRunning,
		Created: time.Now(), dir: n.taskDir(a.ID), secret: newID() + newID(), perms: map[string]*permission{}}
	first := spec.Prompt
	var prev saved
	if raw, err := os.ReadFile(filepath.Join(t.dir, "task.json")); err == nil && json.Unmarshal(raw, &prev) == nil {
		t.SessionID, t.seq, t.delivered, t.restarts = prev.SessionID, prev.Seq, prev.Delivered, prev.Restarts+1
		t.reported.delivered = prev.Delivered
		first = "You were interrupted: the machine or eriphe-node restarted. Check where you were and carry on with the task."
		if t.SessionID == "" {
			first = spec.Prompt // it never got as far as a session: start over
		}
	}
	if !validName(a.ID) || (spec.Profile != "" && !validName(spec.Profile)) {
		t.State = stateFailed
		n.adopt(t)
		t.emit("error", map[string]any{"message": "bad task or profile name"})
		return
	}
	n.adopt(t)
	if t.restarts > maxRestarts {
		t.emit("error", map[string]any{"message": fmt.Sprintf("restarted %d times, giving up", maxRestarts)})
		t.setState(stateFailed)
		return
	}
	if err := os.MkdirAll(t.dir, 0o700); err != nil {
		t.emit("error", map[string]any{"message": err.Error()})
		t.setState(stateFailed)
		return
	}
	t.save()
	if t.restarts > 0 {
		t.emit("restart", map[string]any{"restarts": t.restarts})
	}
	t.emit("state", map[string]any{"state": stateRunning})
	// In the background: its agent may have to be downloaded first.
	go func() {
		if err := n.startContainer(t, first); err != nil {
			t.emit("error", map[string]any{"message": err.Error()})
			t.setState(stateFailed)
		}
	}()
}

// save writes what the task must remember if it has to restart.
func (t *Task) save() {
	if t.dir == "" {
		return
	}
	t.mu.Lock()
	s := saved{SessionID: t.SessionID, Seq: t.seq, Delivered: t.delivered, Restarts: t.restarts}
	t.mu.Unlock()
	raw, _ := json.Marshal(s)
	os.WriteFile(filepath.Join(t.dir, "task.json"), raw, 0o600)
}

// deliver hands the task's inbox to its agent, in order: an answer to a
// pending permission, or a message once the agent's turn is over.
func (n *node) deliver(t *Task) {
	t.delivery.Lock()
	defer t.delivery.Unlock()
	t.mu.Lock()
	items, delivered, run := t.inbox, t.delivered, t.run
	t.mu.Unlock()
	for _, it := range items {
		if it.Seq <= delivered {
			continue
		}
		switch it.Kind {
		case "permission":
			id, _ := it.Data["id"].(string)
			allow, _ := it.Data["allow"].(bool)
			msg, _ := it.Data["message"].(string)
			t.mu.Lock()
			p := t.perms[id]
			t.mu.Unlock()
			// A request from before a restart is gone: its answer is dropped.
			if p != nil {
				p.answer <- permAnswer{Allow: allow, Message: msg}
			}
		case "message":
			text, _ := it.Data["text"].(string)
			if run == nil || t.state() != stateIdle {
				return // the agent is busy: later
			}
			if err := run.send(text); err != nil {
				return
			}
			t.setState(stateRunning)
		}
		delivered = it.Seq
		t.mu.Lock()
		t.delivered = delivered
		t.mu.Unlock()
		t.save()
		n.poke()
	}
}

// reportLoop sends what changed in each task to eriphe-core, and delivers
// what had to wait for the end of a turn.
func (n *node) reportLoop(c *coreClient) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-n.kick:
		case <-tick.C:
		}
		n.mu.Lock()
		list := make([]*Task, 0, len(n.tasks))
		for _, t := range n.tasks {
			list = append(list, t)
		}
		n.mu.Unlock()
		for _, t := range list {
			n.deliver(t)
			n.report(c, t)
		}
		n.reportServices(c)
		n.upd.handOver(n.running())
		n.awake.set(n.working())
	}
}

func (n *node) report(c *coreClient, t *Task) {
	t.mu.Lock()
	events := append([]Event(nil), t.events...)
	r := struct {
		State     string  `json:"state"`
		SessionID string  `json:"session_id,omitempty"`
		Events    []Event `json:"events"`
		Delivered int     `json:"delivered"`
		Draft     string  `json:"draft,omitempty"` // the text being written, after the events
	}{t.State, t.SessionID, events, t.delivered, t.draft}
	same := len(events) == 0 && r.State == t.reported.state && r.SessionID == t.reported.session &&
		r.Delivered == t.reported.delivered && r.Draft == t.reported.draft
	t.mu.Unlock()
	if same {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := c.do(ctx, http.MethodPost, "/v1/node/tasks/"+t.ID+"/report", r, nil)
	if errors.Is(err, errUnknownTask) {
		// eriphe-core forgot it: nothing to keep.
		go n.stopContainer(t)
		return
	}
	if err != nil {
		log.Printf("report %s: %v", t.ID, err)
		return
	}
	t.mu.Lock()
	t.events = t.events[len(events):]
	t.reported.state, t.reported.session, t.reported.delivered, t.reported.draft = r.State, r.SessionID, r.Delivered, r.Draft
	t.mu.Unlock()
	t.save()
}
