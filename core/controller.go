package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// core ties the modules together: the store, the nodes, the scheduler, and
// the lifecycle: eriphe-core writes what each task should be, and its node
// makes it so and reports back (see lifecycle.go).
type core struct {
	base  *Config                // the configuration file, as loaded
	snap  atomic.Pointer[Config] // the file merged with the workspace's catalog (catalog.go)
	store *Store
	token string      // eriphe-core's own API token, lent to the brain at self_url
	self  *mcp.Server // eriphe-core's own MCP server, also relayed to the brain by its node
	pool  *pool       // the catalog's upstream sessions

	started time.Time // when this eriphe-core started (the brain waits for the catalog, brain.go)

	remote *remoteAccess // remote access over the tailnet (remote.go); nil in tests

	machines *machineCA // the machines' certificate authority (machine_tls.go); nil without one

	updates *updates // the latest versions on the registry (updates.go)

	mem  *memStore // Eriphe's memory (memory.go); nil if it could not open
	turn brainTurn // what the brain's current turn read (memory_trust.go)

	relayMu sync.Mutex // one copy of the brain's output at a time (relayBrainOutput)
	draft   brainDraft // the brain's message being written (brain_draft.go)

	placing  sync.Mutex // one pass at a time over the tasks waiting for a node
	failedOn sync.Map   // service name → the node it kept failing on, avoided while another can take it

	cloud cloudFlows // Google authorizations in progress (cloud.go)

	catalogMu sync.Mutex // one change of the catalog at a time
	dyn       dynamicCatalog
	skipped   map[string]string // workspace entries left out at start, and why

	mu      sync.Mutex
	nodes   map[string]*nodeClient // by name, created when first needed
	rev     int64                  // bumped whenever an assignment or an inbox changes
	changed chan struct{}          // closed and replaced on every bump
	inbox   chan struct{}          // pinged when the brain has something to read
}

func newCore(cfg *Config, store *Store) *core {
	c := &core{base: cfg, store: store, nodes: map[string]*nodeClient{}, started: time.Now(),
		rev: time.Now().UnixNano(), changed: make(chan struct{}), inbox: make(chan struct{}, 1),
		updates: newUpdates(registryURL)}
	c.dyn, c.skipped = loadCatalog(cfg, store)
	m := mergeCatalog(cfg, c.dyn)
	if err := m.validateLoading(); err != nil {
		// The file alone was checked when loaded: keep it as it is.
		log.Printf("catalog: %v", err)
		c.dyn, m = newDynamic(), mergeCatalog(cfg, newDynamic())
		m.validate()
	}
	c.snap.Store(m)
	c.turn.load(store.get("brain_turn"), store.get("brain_task") != "")
	c.turn.keep = func(st brainTurnState) {
		raw, _ := json.Marshal(st)
		store.set("brain_turn", string(raw))
	}
	c.pool = newPool(store, m.Servers)
	// Local servers are services, reached on their node.
	c.pool.dialLocal = func(ctx context.Context, name string, _ json.RawMessage) (mcp.Transport, error) {
		return c.dialService(ctx, name)
	}
	return c
}

func (c *core) node(name string) *nodeClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.nodes[name]
	if n == nil {
		n = newNodeClient(name)
		c.nodes[name] = n
	}
	return n
}

// run starts the node status controller.
func (c *core) run(ctx context.Context) {
	c.checkNodes(ctx)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.checkNodes(ctx)
			}
		}
	}()
}

func (c *core) checkNodes(ctx context.Context) {
	nodes, err := c.store.nodes()
	if err != nil {
		log.Printf("store: %v", err)
		return
	}
	for _, row := range nodes {
		c.refreshNode(ctx, c.node(row.Name))
	}
	if nodes, err = c.store.nodes(); err == nil {
		c.resolveUpdates(nodes) // machine_updates.go
	}
	c.autoUpdates(time.Now())
	c.placeServices() // also when no node answers: a node gone too long loses its services
}

// refreshNode records whether a node answers, and what it offers. A node
// that answers may be the one a waiting task needs. It runs when a node
// connects and every 15 seconds.
func (c *core) refreshNode(ctx context.Context, n *nodeClient) {
	var info json.RawMessage
	err := errOffline
	if n.connected() {
		info, err = n.info(ctx)
	}
	if err := c.store.setNodeStatus(n.name, err == nil, info); err != nil {
		log.Printf("store: %v", err)
		return
	}
	if err == nil {
		c.placePending()
		c.placeServices()
	}
}

// placePending schedules again the tasks waiting for a node, and wakes the
// nodes that get one.
func (c *core) placePending() {
	c.placing.Lock()
	defer c.placing.Unlock()
	tasks, err := c.store.unassigned()
	if err != nil || len(tasks) == 0 {
		return
	}
	nodes, err := c.store.nodes()
	if err != nil {
		log.Printf("store: %v", err)
		return
	}
	placed := false
	for _, t := range tasks {
		_, agent, err := c.conf().agent(t.Agent)
		if err == nil {
			var name string
			var folders []*Folder
			if folders, _, err = c.folderGrant(splitNames(t.Folders)); err == nil {
				name, err = schedule(c.schedulable(nodes), "", c.taskPlacement(agent, folders), c.store.activeCount)
			}
			if err == nil {
				if ok, _ := c.store.assign(t.ID, name); ok {
					log.Printf("task %s placed on %s", t.ID, name)
					placed = true
				}
				continue
			}
		}
		var waiting *waitingError
		if errors.As(err, &waiting) {
			if waiting.reason != t.PendingReason {
				c.store.updateTask(t.ID, map[string]any{"pending_reason": waiting.reason})
			}
			continue
		}
		// No node could ever run it now (forgotten, or the agent changed).
		log.Printf("task %s fails: %v", t.ID, err)
		c.store.updateTask(t.ID, map[string]any{"state": stateFailed, "pending_reason": err.Error()})
		c.notifyBrain(t.ID, stateFailed)
	}
	if placed {
		c.bump()
	}
}

func newTaskID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "t-" + hex.EncodeToString(b)
}

type runRequest struct {
	Prompt     string   `json:"prompt" jsonschema:"what the agent must do, self-contained: it does not see this conversation"`
	Repo       string   `json:"repo,omitempty" jsonschema:"repository to clone into the task's container: owner/name on GitHub or a git URL"`
	Ref        string   `json:"ref,omitempty" jsonschema:"branch or tag to start from; default branch if empty"`
	Branch     string   `json:"branch,omitempty" jsonschema:"work branch to create for this task"`
	Dir        string   `json:"dir,omitempty" jsonschema:"working directory inside the container, if not the repository root"`
	Agent      string   `json:"agent,omitempty" jsonschema:"agent from list_agents; empty for the default one"`
	Node       string   `json:"node,omitempty" jsonschema:"node name; empty lets the scheduler choose"`
	Identities []string `json:"identities,omitempty" jsonschema:"other identities lent to the agent, e.g. github to push a branch or open a PR"`
	Tools      []string `json:"tools,omitempty" jsonschema:"catalog tools lent to the agent (see list_tools): a server name for all its tools, or server.tool with * globs, e.g. github.get_*"`
	Folders    []string `json:"folders,omitempty" jsonschema:"the user's folders lent to the agent, by name (see list_folders), mounted at /work/folders/<name>; the task runs on their machine, so all must be on the same one"`
}

// workerVoice is a worker's standing instruction: its last message reaches
// me, so it speaks like Eriphe.
const workerVoice = "Your final message is relayed to the user as Eriphe's. Write it in the language of your instructions; " +
	"in French, use tutoiement (« tu »), never « vous ». Be concise: the result, without preamble."

// runTask schedules a worker task, sends it to its node and starts following it.
func (c *core) runTask(ctx context.Context, r runRequest) (*TaskRow, error) {
	return c.startTask(ctx, "worker", r, nodeTaskSpec{AppendSystemPrompt: workerVoice})
}

// startTask is runTask for any kind of task; extra carries the fields only
// the brain uses (MCP servers, system prompt, allowed tools, resume).
func (c *core) startTask(ctx context.Context, kind string, r runRequest, extra nodeTaskSpec) (*TaskRow, error) {
	if r.Prompt == "" {
		return nil, errors.New("prompt is required")
	}
	agentName, agent, err := c.conf().agent(r.Agent)
	if err != nil {
		return nil, err
	}
	nodes, err := c.store.nodes()
	if err != nil {
		return nil, err
	}
	folders, machine, err := c.folderGrant(r.Folders)
	if err != nil {
		return nil, err
	}
	if machine != "" && r.Node != "" && r.Node != machine {
		return nil, fmt.Errorf("the folders %s are on %s, not on %s: leave node empty", strings.Join(folderNames(folders), ", "), machine, r.Node)
	}
	// No online node may fit yet: the task then waits, without a node, for
	// one that would (see placePending).
	name, err := schedule(c.schedulable(nodes), r.Node, c.taskPlacement(agent, folders), c.store.activeCount)
	var waiting *waitingError
	if errors.As(err, &waiting) {
		name, err = "", nil
	}
	if err != nil {
		return nil, err
	}
	grant, err := c.conf().checkGrant(r.Tools)
	if err != nil {
		return nil, err
	}
	if len(grant) > 0 && !harnessOf(agent.Runtime).MCP {
		return nil, fmt.Errorf("agent %s runs %s: catalog tools need a runtime that takes MCP servers", agentName, agent.Runtime)
	}
	// The credentials are checked now, but only read again when the node fetches the task.
	if _, err := c.lend(kind, agentName, strings.Join(r.Identities, ",")); err != nil {
		return nil, err
	}
	where := r.Dir
	if r.Repo != "" {
		where = r.Repo
		if r.Branch != "" {
			where += "@" + r.Branch
		}
	}
	t := &TaskRow{ID: newTaskID(), Node: name, Agent: agentName, Runtime: agent.Runtime, Model: agent.Model,
		Identity: strings.Join(r.Identities, ","), Dir: where, Prompt: r.Prompt, State: statePending,
		Kind: kind, Tools: strings.Join(grant, ","), Folders: strings.Join(folderNames(folders), ",")}
	if waiting != nil {
		t.PendingReason = waiting.reason
	}
	if err := c.store.insertTask(t); err != nil {
		return nil, err
	}
	spec := extra
	spec.Runtime, spec.Prompt, spec.Dir, spec.Model = agent.Runtime, r.Prompt, r.Dir, agent.Model
	spec.Repo, spec.Ref, spec.Branch, spec.Image = r.Repo, r.Ref, r.Branch, agent.Image
	spec.Command = agent.Command
	spec.Identity, spec.MCPServers, spec.Servers, spec.AlwaysLoad = nil, nil, nil, nil // added when the node fetches the task
	spec.Folders = nil                                                                 // the same
	if len(folders) > 0 {
		spec.AppendSystemPrompt = strings.TrimSpace(spec.AppendSystemPrompt + "\n\n" + folderInstructions(folders))
	}
	if kind == "worker" && !harnessOf(agent.Runtime).takesSystemPrompt() {
		// It takes no system prompt: it reads it first.
		spec.Prompt = spec.AppendSystemPrompt + "\n\n" + spec.Prompt
		spec.AppendSystemPrompt = ""
	}
	c.store.updateTask(t.ID, map[string]any{"desired": desiredRunning})
	if err := c.store.setSpec(t.ID, spec); err != nil {
		return nil, err
	}
	c.bump()
	return c.store.task(t.ID)
}

// status summarises a task for the brain: its state, the agent's last
// words, and what it waits for, if anything.
type taskStatus struct {
	Task *TaskRow `json:"task"`
	// PendingPermission is the oldest permission still waiting for an answer;
	// PendingPermissions are all of them: an agent can ask for several at once.
	PendingPermission  map[string]any   `json:"pending_permission,omitempty"`
	PendingPermissions []map[string]any `json:"pending_permissions,omitempty"`
	LastText           string           `json:"last_text,omitempty"`
	AuthError          bool             `json:"auth_error,omitempty"`
	Recent             []EventRow       `json:"recent"`
}

func (c *core) status(id string, n int) (*taskStatus, error) {
	t, err := c.store.task(id)
	if err != nil {
		return nil, err
	}
	events, err := c.store.events(id, 200)
	if err != nil {
		return nil, err
	}
	st := &taskStatus{Task: t}
	answered := map[string]bool{}
	for _, e := range events {
		switch e.Type {
		case "permission_answer":
			answered[fmt.Sprint(e.Data["id"])] = true
		case "text":
			st.LastText, _ = e.Data["text"].(string)
		case "turn_end":
			if b, _ := e.Data["auth_error"].(bool); b {
				st.AuthError = true
			}
		}
	}
	if t.State == stateWaitingPermission {
		for _, e := range events {
			pid := fmt.Sprint(e.Data["id"])
			if e.Type == "permission" && !answered[pid] && !c.store.answered(id, pid) {
				st.PendingPermissions = append(st.PendingPermissions, e.Data)
			}
		}
		if len(st.PendingPermissions) > 0 {
			st.PendingPermission = st.PendingPermissions[0]
		}
	}
	st.Recent = events[max(0, len(events)-n):]
	return st, nil
}

// answer replies to what a task waits for: a pending permission (allow or
// deny, with an optional message) or the agent's question (a new turn). The
// answer goes to the task's inbox, which its node delivers. permission names
// the request answered; it may be empty when only one is pending. by says
// who answers: some permissions are the user's alone.
func (c *core) answer(ctx context.Context, id, permission, reply string, allow *bool, by string) (string, error) {
	st, err := c.status(id, 0)
	if err != nil {
		return "", err
	}
	if len(st.PendingPermissions) > 0 || permission != "" {
		var p map[string]any
		for _, q := range st.PendingPermissions {
			if permission == "" || fmt.Sprint(q["id"]) == permission {
				p = q
				break
			}
		}
		switch {
		case p == nil && c.store.answered(id, permission):
			return "", errors.New("this permission is already answered")
		case p == nil:
			return "", fmt.Errorf("the task has no pending permission %q", permission)
		case permission == "" && len(st.PendingPermissions) > 1:
			raw, _ := json.Marshal(st.PendingPermissions)
			return "", fmt.Errorf("the task waits for several permissions: answer each with its permission_id: %s", raw)
		case allow == nil:
			raw, _ := json.Marshal(p)
			return "", fmt.Errorf("the task waits for a permission, set allow: %s", raw)
		}
		pid := fmt.Sprint(p["id"])
		if reason := c.store.userOnly(id, pid); reason != "" && by != byUser {
			return "", fmt.Errorf("this permission is %s's decision alone (%s): relay it, do not answer it", c.conf().User, reason)
		}
		if by == byBrain && *allow {
			if err := c.taintedAllow(id, pid); err != nil {
				return "", err
			}
		}
		if err := c.store.addInbox(id, "permission", map[string]any{"id": pid, "allow": *allow, "message": reply, "by": by}); err != nil {
			return "", err
		}
		tool := fmt.Sprint(p["tool_name"])
		input, _ := p["input"].(map[string]any)
		decision := decideDeny
		if *allow {
			decision = decideAllow
		}
		c.store.recordDecision(id, pid, tool, input, decision, by, reply)
		c.bump()
		if *allow {
			return "permission granted", nil
		}
		return "permission denied", nil
	}
	if st.Task.State != stateIdle {
		return "", fmt.Errorf("the task is %s: nothing to answer yet", st.Task.State)
	}
	if reply == "" {
		return "", errors.New("reply is required")
	}
	if err := c.store.addInbox(id, "message", map[string]any{"text": reply}); err != nil {
		return "", err
	}
	// Running until the node says otherwise, so the same turn is not answered twice.
	c.store.updateTask(id, map[string]any{"state": stateRunning})
	c.bump()
	return "message sent", nil
}

// stop asks for a task to stop; a task its node never picked up, or that
// still waits for a node, stops at once.
func (c *core) stop(ctx context.Context, id string) error {
	t, err := c.store.task(id)
	if err != nil {
		return err
	}
	fields := map[string]any{"desired": desiredStopped}
	if t.State == statePending {
		fields["state"] = stateStopped
	}
	if err := c.store.updateTask(id, fields); err != nil {
		return err
	}
	c.bump()
	return nil
}

// stopByBrain is stop for the brain, which is not told of the end it asked for.
func (c *core) stopByBrain(ctx context.Context, id string) error {
	if err := c.stop(ctx, id); err != nil {
		return err
	}
	return c.store.set("brain_stopped:"+id, "1")
}

// waitForChange returns once the task has moved (new events) and needs an
// answer (waiting for a permission, or idle after its turn) or has ended,
// or after d.
func (c *core) waitForChange(ctx context.Context, id string, d time.Duration) {
	start, err := c.store.task(id)
	if err != nil {
		return
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		t, err := c.store.task(id)
		if err != nil || finished(t.State) {
			return
		}
		moved := t.LastSeq > start.LastSeq
		if moved && (t.State == stateIdle || t.State == stateWaitingPermission) {
			return
		}
		time.Sleep(time.Second)
	}
}
