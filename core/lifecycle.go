package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// The lifecycle is declarative, like a kubelet's: eriphe-core writes what
// each task should be (its spec, run or stop, its inbox), and the node
// watches its assignments, makes the containers match, and reports their
// state and events. Nothing is lost while a node or eriphe-core is away: the
// node catches up when it comes back.
//
//	GET  /v1/node/assignments?rev=R&wait=S   the node's tasks, as soon as they change
//	POST /v1/node/tasks/{id}/report           state, session, events, inbox delivered
//
// Services (local MCP servers) come in the same assignments; see services.go.

// bump wakes the nodes watching their assignments.
func (c *core) bump() {
	c.mu.Lock()
	c.rev++
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}

// lend reads the credentials of a task: its agent's provider, its identities
// and, for the brain, eriphe-core's own token. They are read when a node
// fetches the task and never stored.
func (c *core) lend(kind, agentName, identities string) (map[string]string, error) {
	_, agent, err := c.conf().agent(agentName)
	if err != nil {
		return nil, err
	}
	env, err := c.conf().lendAgent(agent)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(identities, ",") {
		if n != "" {
			names = append(names, n)
		}
	}
	return env, c.conf().lend(names, env)
}

// nodeFromRequest authenticates a node by its name and secret, and by its
// certificate (authNode, machine_tls.go).
func (c *core) nodeFromRequest(r *http.Request) (string, bool) {
	name, _, err := c.authNode(r)
	return name, err == nil
}

// nodeAssignment is one task as a node receives it, with its credentials.
type nodeAssignment struct {
	ID      string       `json:"id"`
	Desired string       `json:"desired"`
	Spec    nodeTaskSpec `json:"spec"`
	Inbox   []InboxItem  `json:"inbox"`
}

func (c *core) handleAssignments(w http.ResponseWriter, r *http.Request) {
	name, ok := c.nodeFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var rev int64
	wait := 0
	fmt.Sscan(r.URL.Query().Get("rev"), &rev)
	fmt.Sscan(r.URL.Query().Get("wait"), &wait)
	c.mu.Lock()
	current, changed := c.rev, c.changed
	c.mu.Unlock()
	if rev == current && wait > 0 {
		select {
		case <-changed:
		case <-time.After(time.Duration(min(wait, 60)) * time.Second):
		case <-r.Context().Done():
			return
		}
		c.mu.Lock()
		current = c.rev
		c.mu.Unlock()
	}
	rows, err := c.store.assignments(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []nodeAssignment{}
	for _, a := range rows {
		spec := a.Spec
		if a.Desired == desiredRunning {
			env, err := c.lend(a.Kind, a.Agent, a.Idents)
			if err != nil {
				// The configuration changed under the task: it cannot run any more.
				log.Printf("task %s fails: %v", a.ID, err)
				c.store.updateTask(a.ID, map[string]any{"state": stateFailed})
				c.notifyBrain(a.ID, stateFailed)
				continue
			}
			spec.Identity = env
			spec.TZ = c.conf().TimeZone
			if spec.Folders, err = c.specFolders(a.Folders); err != nil {
				// A folder was removed (or moved) under the task.
				log.Printf("task %s fails: %v", a.ID, err)
				c.store.updateTask(a.ID, map[string]any{"state": stateFailed})
				c.notifyBrain(a.ID, stateFailed)
				continue
			}
			if spec.Egress == nil {
				spec.Egress = defaultEgress()
			}
			// Its catalog servers, as the configuration has them now: the
			// node points the task at itself for each, never at the server.
			spec.Servers = c.specServers(&TaskRow{Kind: a.Kind, Tools: a.Tools})
			spec.AlwaysLoad = nil
			for _, s := range spec.Servers {
				if c.conf().Servers[s].AlwaysLoad {
					spec.AlwaysLoad = append(spec.AlwaysLoad, s)
				}
			}
			if a.Kind == "brain" {
				spec.SystemNetwork = true
				spec.Stream = true
				// One memory: eriphe-core's, not Claude Code's own.
				if spec.Identity == nil {
					spec.Identity = map[string]string{}
				}
				spec.Identity["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
				// Its free reads, as the catalog lists them now: allowed in
				// advance, they never wait for a permission. The node reads
				// them when the brain starts.
				spec.AllowedTools = append(append([]string(nil), spec.AllowedTools...), c.brainFreeReads()...)
				spec.MCPServers, err = c.brainServers()
				if err != nil {
					log.Printf("brain: %v", err)
					continue
				}
				if c.conf().Brain.SelfURL == "" {
					spec.Servers = append(spec.Servers, selfServer) // relayed by its node
				}
			}
		}
		inbox, err := c.store.inbox(a.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, nodeAssignment{ID: a.ID, Desired: a.Desired, Spec: spec, Inbox: inbox})
	}
	// Nodes from before services ignore them.
	services, err := c.serviceAssignments(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"rev": current, "tasks": out, "services": services, "prepare": c.nodePrepare(name)}
	// Nodes from before updates ignore it (machine_updates.go).
	if u := c.nodeUpdateRequest(name); u != nil {
		resp["update"] = u
	}
	writeJSON(w, http.StatusOK, resp)
}

// nodePrepare is what a node gets ready before any task asks for it: the
// runtimes of the configured agents it could run (it carries their needed
// labels) and the images they name. The node unpacks those agents and pulls
// those images, with its default image, in the background; an agent no one
// configured is never downloaded.
func (c *core) nodePrepare(name string) map[string][]string {
	var labels []string
	if nodes, err := c.store.nodes(); err == nil {
		for _, n := range nodes {
			if n.Name == name {
				labels = parseInfo(n).Labels
			}
		}
	}
	runtimes, images := []string{}, []string{}
	for _, a := range c.conf().Agents {
		if slices.ContainsFunc(a.Needs, func(l string) bool { return !slices.Contains(labels, l) }) {
			continue
		}
		if !slices.Contains(runtimes, a.Runtime) {
			runtimes = append(runtimes, a.Runtime)
		}
		if a.Image != "" && !slices.Contains(images, a.Image) {
			images = append(images, a.Image)
		}
	}
	sort.Strings(runtimes)
	sort.Strings(images)
	return map[string][]string{"runtimes": runtimes, "images": images}
}

// nodeReport is what a node says about one of its tasks.
type nodeReport struct {
	State     string     `json:"state"`
	SessionID string     `json:"session_id,omitempty"`
	Events    []EventRow `json:"events"`
	Delivered int        `json:"delivered"` // inbox items handled, up to this seq
	Draft     string     `json:"draft"`     // the text being written, after the events (Stream)
}

func (c *core) handleReport(w http.ResponseWriter, r *http.Request) {
	name, ok := c.nodeFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	t, err := c.store.task(id)
	if err != nil || t.Node != name {
		http.Error(w, "no such task on this node", http.StatusNotFound)
		return
	}
	var in nodeReport
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	if len(in.Events) > 0 {
		if err := c.store.addEvents(id, in.Events); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		c.logResults(t, in.Events) // its access log (node_access.go)
	}
	if id == c.store.get("brain_task") {
		c.brainWrote(t, len(in.Events) > 0, in.Draft)
	}
	// The policy answers what it decides before anyone hears of it.
	forBrain := c.applyPolicy(id, in.Events)
	if in.Delivered > 0 {
		c.store.ackInbox(id, in.Delivered)
	}
	fields := map[string]any{}
	if in.SessionID != "" && in.SessionID != t.SessionID {
		fields["session_id"] = in.SessionID
	}
	// A task answered by eriphe-core stays running until the node has the answer.
	state := in.State
	if state == stateIdle && t.State == stateRunning && c.hasUndelivered(id) {
		state = stateRunning
	}
	if state != "" && state != t.State && !finished(t.State) {
		fields["state"] = state
	}
	if len(fields) > 0 {
		c.store.updateTask(id, fields)
	}
	if s, ok := fields["state"].(string); ok {
		if finished(s) {
			c.bump() // the task leaves its node's assignments
		}
		if id == c.store.get("brain_task") {
			c.ping()
		} else if s != stateWaitingPermission {
			c.notifyBrain(id, s)
		}
	} else if len(in.Events) > 0 && id == c.store.get("brain_task") {
		c.ping()
	}
	if forBrain {
		c.notifyBrain(id, stateWaitingPermission)
	}
	if len(in.Events) > 0 {
		c.bump() // the policy may have answered: wake the node
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) hasUndelivered(id string) bool {
	items, err := c.store.inbox(id)
	return err == nil && len(items) > 0
}
