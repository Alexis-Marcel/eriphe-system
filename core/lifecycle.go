package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"time"
)

// The lifecycle is declarative, like a kubelet's: eriphe-core writes what
// each task should be (its spec, run or stop, its inbox), and the device
// watches its assignments, makes the containers match, and reports their
// state and events. Nothing is lost while a device or eriphe-core is away: the
// device catches up when it comes back.
//
//	GET  /v1/device/assignments?rev=R&wait=S   the device's tasks, as soon as they change
//	POST /v1/device/tasks/{id}/report           state, session, events, inbox delivered
//
// Services (local MCP servers) come in the same assignments; see services.go.

// bump wakes the devices watching their assignments.
func (c *core) bump() {
	c.mu.Lock()
	c.rev++
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}

// lend reads the credentials of a task's agent: its provider's. They are
// read when a device fetches the task and never stored.
func (c *core) lend(agentName string) (map[string]string, error) {
	_, agent, err := c.conf().agent(agentName)
	if err != nil {
		return nil, err
	}
	return c.conf().lendAgent(agent)
}

// deviceFromRequest authenticates a device by its name and secret, and by its
// certificate (authDevice, machine_tls.go).
func (c *core) deviceFromRequest(r *http.Request) (string, bool) {
	name, _, err := c.authDevice(r)
	return name, err == nil
}

// deviceAssignment is one task as a device receives it, with its credentials.
type deviceAssignment struct {
	ID      string         `json:"id"`
	Desired string         `json:"desired"`
	Spec    deviceTaskSpec `json:"spec"`
	Inbox   []InboxItem    `json:"inbox"`
}

func (c *core) handleAssignments(w http.ResponseWriter, r *http.Request) {
	name, ok := c.deviceFromRequest(r)
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
	out := []deviceAssignment{}
	for _, a := range rows {
		spec := a.Spec
		if a.Desired == desiredRunning {
			env, err := c.lend(a.Agent)
			if err != nil {
				// The configuration changed under the task: it cannot run any more.
				log.Printf("task %s fails: %v", a.ID, err)
				c.store.updateTask(a.ID, map[string]any{"state": stateFailed})
				c.notifyBrain(a.ID, stateFailed)
				continue
			}
			spec.AgentEnv = env
			spec.TZ = c.conf().TimeZone
			spec.Accesses = append(c.lentAccesses(a.ID, a.Resources), lentGit(a.ID, a.Resources)...) // lent_access.go, git_access.go
			if spec.Folders, err = c.specFoldersOn(name, a.Folders, a.Resources); err != nil {
				// A folder was removed (or moved) under the task.
				log.Printf("task %s fails: %v", a.ID, err)
				c.store.updateTask(a.ID, map[string]any{"state": stateFailed})
				c.notifyBrain(a.ID, stateFailed)
				continue
			}
			if spec.Egress == nil {
				spec.Egress = defaultEgress()
			}
			c.conf().modelEndpoint(a.Agent, env, &spec)
			// Its catalog servers, as the configuration has them now: the
			// device points the task at itself for each, never at the server.
			spec.Servers = c.specServers(&TaskRow{Kind: a.Kind, Tools: a.Tools})
			spec.AlwaysLoad = nil
			for _, s := range spec.Servers {
				if c.conf().Servers[s].AlwaysLoad {
					spec.AlwaysLoad = append(spec.AlwaysLoad, s)
				}
			}
			// Its own node, written through its device (task_nodes.go).
			if a.Kind == "worker" && c.nodes != nil && c.taskNode(a.ID) != "" {
				spec.Servers = append(spec.Servers, nodesServer)
				spec.AlwaysLoad = append(spec.AlwaysLoad, nodesServer)
				spec.AllowedTools = append(append([]string(nil), spec.AllowedTools...), taskNodesTools()...)
			}
			// Its memory, read through its device (situations.go).
			if !isRoleKind(a.Kind) && c.mem != nil {
				spec.Servers = append(spec.Servers, taskMemServer)
				spec.AlwaysLoad = append(spec.AlwaysLoad, taskMemServer)
				spec.AllowedTools = append(append([]string(nil), spec.AllowedTools...), taskMemTools()...)
			}
			// An assistant's task: what its role's card gives it (assistant_controller.go).
			if isRoleKind(a.Kind) {
				if err := c.fetchAssistant(&TaskRow{ID: a.ID, Kind: a.Kind}, &spec); err != nil {
					log.Printf("%s: %v", a.Kind, err)
					continue
				}
			}
		}
		inbox, err := c.store.inbox(a.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, deviceAssignment{ID: a.ID, Desired: a.Desired, Spec: spec, Inbox: inbox})
	}
	// Devices from before services ignore them.
	services, err := c.serviceAssignments(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"rev": current, "tasks": out, "services": services, "prepare": c.devicePrepare(name)}
	// Devices from before updates ignore it (machine_updates.go).
	if u := c.deviceUpdateRequest(name); u != nil {
		resp["update"] = u
	}
	writeJSON(w, http.StatusOK, resp)
}

// devicePrepare is what a device gets ready before any task asks for it: the
// runtimes of the configured agents it could run (it carries their needed
// labels) and the images they name. The device unpacks those agents and pulls
// those images, with its default image, in the background; an agent no one
// configured is never downloaded.
func (c *core) devicePrepare(name string) map[string][]string {
	var labels []string
	if devices, err := c.store.devices(); err == nil {
		for _, n := range devices {
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

// deviceReport is what a device says about one of its tasks.
type deviceReport struct {
	State     string     `json:"state"`
	SessionID string     `json:"session_id,omitempty"`
	Events    []EventRow `json:"events"`
	Delivered int        `json:"delivered"` // inbox items handled, up to this seq
	Draft     string     `json:"draft"`     // the text being written, after the events (Stream)
}

func (c *core) handleReport(w http.ResponseWriter, r *http.Request) {
	name, ok := c.deviceFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	t, err := c.store.task(id)
	if err != nil || t.Device != name {
		http.Error(w, "no such task on this device", http.StatusNotFound)
		return
	}
	var in deviceReport
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	if len(in.Events) > 0 {
		if err := c.store.addEvents(id, in.Events); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		c.logResults(t, in.Events)      // its access log (device_access.go)
		c.noteAgentHealth(t, in.Events) // its account's real state (provider_health.go)
	}
	isAssistant := c.isAssistantTask(id)
	if owner, ok := c.reportingAssistant(id); ok {
		for _, kind := range owner.wakeKinds() {
			if h := assistantTriggers[kind].wrote; h != nil {
				h(c, owner, t, len(in.Events) > 0, in.Draft)
			}
		}
	}
	// The policy answers what it decides before anyone hears of it.
	forBrain := c.applyPolicy(id, in.Events)
	if in.Delivered > 0 {
		c.store.ackInbox(id, in.Delivered)
	}
	fields := c.reportedFields(t, in)
	if len(fields) > 0 {
		c.store.updateTask(id, fields)
	}
	c.announceReport(id, fields, len(in.Events) > 0, isAssistant, forBrain)
	w.WriteHeader(http.StatusNoContent)
}

// reportedFields are what a report changes of its task: its session, and
// its state unless it has ended. A task answered by eriphe-core stays
// running until the device has the answer.
func (c *core) reportedFields(t *TaskRow, in deviceReport) map[string]any {
	fields := map[string]any{}
	if in.SessionID != "" && in.SessionID != t.SessionID {
		fields["session_id"] = in.SessionID
	}
	state := in.State
	if state == stateIdle && t.State == stateRunning && c.hasUndelivered(t.ID) {
		state = stateRunning
	}
	if state != "" && state != t.State && !finished(t.State) {
		fields["state"] = state
	}
	return fields
}

// announceReport tells who needs it of a report: the device, the assistants,
// and the brain.
func (c *core) announceReport(id string, fields map[string]any, events, isAssistant, forBrain bool) {
	if s, ok := fields["state"].(string); ok {
		if finished(s) {
			c.bump() // the task leaves its device's assignments
			if !isAssistant {
				c.taskEnded(id, s)
			}
		}
		if isAssistant {
			c.ping()
		} else if s != stateWaitingPermission {
			c.notifyBrain(id, s)
		}
	} else if events && isAssistant {
		c.ping()
	}
	if forBrain {
		c.notifyBrain(id, stateWaitingPermission)
	}
	if events {
		c.bump() // the policy may have answered: wake the device
	}
}

func (c *core) hasUndelivered(id string) bool {
	items, err := c.store.inbox(id)
	return err == nil && len(items) > 0
}
