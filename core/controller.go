package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// core ties the modules together: the store, the devices, the scheduler, and
// the lifecycle: eriphe-core writes what each task should be, and its device
// makes it so and reports back (see lifecycle.go).
type core struct {
	base  *Config                // the configuration file, as loaded
	snap  atomic.Pointer[Config] // the file merged with the workspace's catalog (catalog.go)
	store *Store
	token string      // eriphe-core's own API token; the brain gets one derived from it, at self_url (assistantToken)
	self  *mcp.Server // eriphe-core's own MCP server, also relayed to the brain by its device
	pool  *pool       // the catalog's upstream sessions
	cimd  cimdCache   // which servers take Eriphe's published client only (cimd.go)

	started time.Time // when this eriphe-core started (the brain waits for the catalog, brain.go)

	remote *remoteAccess // remote access over the tailnet (remote.go); nil in tests

	machines *machineCA // the machines' certificate authority (machine_tls.go); nil without one

	updates *updates // the latest versions on the registry (updates.go)

	mem  *memStore // Eriphe's memory (memory.go); nil if it could not open
	turn brainTurn // what the brain's current turn read (memory_trust.go)

	nodes *nodestore.Store // the graph's nodes (nodes.go); nil if they could not open

	relayMu sync.Mutex // one copy of the brain's output at a time (relayBrainOutput)
	// brainSeqOfTurn is the brain's last message of its current turn, for
	// the cards it proposes after it (convo_cards.go); held by relayMu.
	brainSeqOfTurn int
	draft          brainDraft // the brain's message being written (brain_draft.go)

	placing  sync.Mutex // one pass at a time over the tasks waiting for a device
	failedOn sync.Map   // service name → the device it kept failing on, avoided while another can take it

	syncLinks syncLinks // the Syncthing connections joined between machines (sync_relay.go)
	// syncMu: one pass at a time over the copies' plans; syncPushed, the
	// plan each device last took, by name (folder_copies.go).
	syncMu     sync.Mutex
	syncPushed sync.Map

	signIns signInFlows     // sign-ins in progress (oauth_client.go)
	conns   connectionWatch // the connections' changes and checks (connections.go)

	push        *pusher          // notifications on the user's devices (push.go); nil without the workspace
	notifyClock func() time.Time // the time of notify's decisions (notify.go); nil: now
	botsMu      sync.Mutex
	bots        map[string]*botChannel // the bot channels at work, by id (bot_channel.go); none without the workspace
	obs         observations           // the machines' locked, idle, do not disturb, in memory only (observe.go)
	events      eventBus               // the event journal's subscribers (event_journal.go)
	skillRuns   skillRunner            // the triggered skills (skill_runs.go)
	out         channelOut             // what goes out through the channels the user added (channel_out.go)
	routeMu     sync.Mutex             // one escalation pass at a time (route_send.go)
	surf        surfaceHub             // the surfaces' changes and their subscribers (surfaces.go)
	layout      layoutState            // where the surfaces are on the workspace (workspace_layout.go)
	front       frontTurn              // whether the brain's turn answers the user in the workspace (workspace_front.go)
	props       proposalFeed           // the proposals that moved, for the workspace (surface_proposals.go)
	threadReads sync.Map               // thread id → *threadReadAgain: its sources being read (thread_sources.go)

	appClients // the clients of the providers that mint tokens (github_app.go)
	// minters are the providers whose connections core mints tokens for,
	// by name (connections.go).
	minters map[string]tokenMinter
	tokens  tokenCache // tokens minted for tasks, while they last (repo_lend.go)

	catalogMu sync.Mutex // one change of the catalog at a time
	dyn       dynamicCatalog
	skipped   map[string]string // workspace entries left out at start, and why

	mu      sync.Mutex
	devices map[string]*deviceClient // by name, created when first needed
	rev     int64                    // bumped whenever an assignment or an inbox changes
	changed chan struct{}            // closed and replaced on every bump
	inbox   chan struct{}            // pinged when an assistant has something to read
	asst    assistantStatuses        // what the assistant controller keeps (assistants.go)
}

func newCore(cfg *Config, store *Store) *core {
	c := &core{base: cfg, store: store, devices: map[string]*deviceClient{}, started: time.Now(),
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
	setAssistantName(m.assistantName())
	c.noteFirstRun(m)
	c.loadAssistants()
	c.turn.load(store.get("brain_turn"), c.brainTaskID() != "")
	c.turn.keep = func(st brainTurnState) {
		raw, _ := json.Marshal(st)
		store.set("brain_turn", string(raw))
	}
	c.pool = newPool(store, m.Servers)
	c.minters = map[string]tokenMinter{}
	for _, start := range providerStarts {
		start(c)
	}
	c.pool.connToken = c.connectionToken // a tool on a connection that mints its tokens
	c.pool.connRefused = c.connectionRefused
	c.pool.authHold = c.connectionHold // an account to connect again (connections.go)
	c.pool.resourceUpdated = c.surfaceResourceUpdated
	c.pool.toolsHeld = c.toolsHeld
	c.pool.trusted = c.trustsServer
	c.provideFolders() // the user's folders, a tool like any other (folder_tools.go)
	// Local servers are services, reached on their device.
	c.pool.dialLocal = func(ctx context.Context, name string, _ json.RawMessage) (mcp.Transport, error) {
		return c.dialService(ctx, name)
	}
	return c
}

func (c *core) device(name string) *deviceClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.devices[name]
	if n == nil {
		n = newDeviceClient(name)
		c.devices[name] = n
	}
	return n
}

// run starts the device status controller.
func (c *core) run(ctx context.Context) {
	c.checkDevices(ctx)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.checkDevices(ctx)
			}
		}
	}()
}

func (c *core) checkDevices(ctx context.Context) {
	devices, err := c.store.devices()
	if err != nil {
		log.Printf("store: %v", err)
		return
	}
	for _, row := range devices {
		c.refreshDevice(ctx, c.device(row.Name))
	}
	c.syncAll(ctx) // the copies' plans (folder_copies.go)
	if devices, err = c.store.devices(); err == nil {
		c.resolveUpdates(devices) // machine_updates.go
		c.autoTLSOnly(devices)    // machine_tls.go
	}
	c.followUpdates()
	c.autoUpdates(time.Now())
	c.replaceStranded()               // placement.go
	c.harnessTick(time.Now())         // retries due, time budgets (task_retry.go)
	c.threadsTick(time.Now())         // the threads' engine (thread_engine.go)
	c.judgeCardPushes(time.Now())     // how the cards pushed were used (card_use.go)
	c.placeServices()                 // also when no device answers: a device gone too long loses its services
	c.connectionTick(ctx, time.Now()) // the connections' tokens, now and then (connections.go)
}

// refreshDevice records whether a device answers, and what it offers. A device
// that answers may be the one a waiting task needs. It runs when a device
// connects and every 15 seconds.
func (c *core) refreshDevice(ctx context.Context, n *deviceClient) {
	var info json.RawMessage
	err := errOffline
	if n.connected() {
		info, err = n.info(ctx, deviceContextLevel) // locked, idle, do not disturb (route_signals.go)
	}
	if err == nil {
		// The folders shared or unshared there, and those eriphe-core took
		// off or lowered, told again (folders.go).
		var after deviceInfo
		before := c.store.lastInfo(n.name)
		json.Unmarshal(info, &after)
		c.noteShared(n.name, before, after)
		c.unshareOn(ctx, n, after)
		c.lowerOn(ctx, n, after)
	}
	if err == nil {
		info = c.takeIdle(n.name, info, time.Now()) // in memory only (route_signals.go)
	} else {
		c.obs.forget(n.name) // a device gone says nothing more
	}
	if err := c.store.setDeviceStatus(n.name, err == nil, info); err != nil {
		log.Printf("store: %v", err)
		return
	}
	if err == nil {
		c.placePending()
		c.placeServices()
	}
}

// placePending schedules again the tasks waiting for a device, and wakes the
// devices that get one.
func (c *core) placePending() {
	c.placing.Lock()
	defer c.placing.Unlock()
	tasks, err := c.store.unassigned()
	if err != nil || len(tasks) == 0 {
		return
	}
	devices, err := c.store.devices()
	if err != nil {
		log.Printf("store: %v", err)
		return
	}
	placed := false
	for _, t := range tasks {
		p, err := c.placementOf(t) // placement.go
		if err == nil {
			var name string
			ready := c.schedulable(devices)
			name, err = schedule(ready, "", p, c.store.activeCount)
			if err == nil {
				if ok, _ := c.store.assign(t.ID, name); ok {
					log.Printf("task %s placed on %s", t.ID, name)
					noteFallback(t.ID, t.Kind, name, ready, p)
					c.logPlaced(t, name)
					placed = true
				}
				continue
			}
		}
		var waiting *waitingError
		if errors.As(err, &waiting) {
			c.wakeFor(t) // the device it needs, if it may be woken (wake.go)
			if waiting.reason != t.PendingReason {
				c.store.updateTask(t.ID, map[string]any{"pending_reason": waiting.reason})
			}
			continue
		}
		// No device could ever run it now (forgotten, or the agent changed).
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

// title is what the user reads for a task: the one given, or its contract's
// objective. Without either, its prompt's first line (TaskRow.label).
func (r runRequest) title() string {
	title := oneLine(r.Title)
	if title == "" && r.Contract != nil {
		title = oneLine(r.Contract.Objective)
	}
	return truncate(title, taskTitleMax)
}

type runRequest struct {
	Title     string        `json:"title,omitempty" jsonschema:"what the user reads for this task, in a few words in their language: Écrire le README de portfolio. Not the agent's instructions"`
	Prompt    string        `json:"prompt" jsonschema:"what the agent must do, self-contained: it does not see this conversation"`
	Resources []ResourceRef `json:"resources,omitempty" jsonschema:"what the task is lent, each {kind, ref, access}: a repository the user gave to Eriphe cloned into /work/repo (repo, with branch, base, dir; see list_repos), the user's folders and the places of their tools (folder, place; see list_folders), catalog tools (tool; see list_tools), accesses a program uses, a package registry or an API key (access; see list_tools); only what it needs, with the least access"`
	Agent     string        `json:"agent,omitempty" jsonschema:"agent from list_agents; empty for the default one"`
	Device    string        `json:"device,omitempty" jsonschema:"device name; empty lets the scheduler choose"`
	About     string        `json:"about,omitempty" jsonschema:"what it is about: thread:<id>, or an entity's key (person:<slug>); leave it out for what this turn is about (its thread)"`
	Node      string        `json:"node,omitempty" jsonschema:"the node this work belongs to, by its id: the task works in a node of its own, opened as a child of it; leave it out for a node at the top"`
	// Contract: what must be true at the end, for a task that changes
	// something (contract.go); none for a search or a read.
	Contract *taskContract `json:"contract,omitempty" jsonschema:"for a task that changes something: objective (required), constraints, checks, evidence, verify, budget; leave it out for a search or a read. Approval is never part of it: the policy decides"`

	// Tools is an assistant's grant, never a brain's call.
	Tools []string `json:"-"`

	// askStart: what the task will need of the user's is asked at launch,
	// on a card, and the task waits for the answer (task_start.go); taint
	// is why its start may have been talked into it, if it may.
	askStart bool
	// cleanStart: started in a clean turn of the user's, or by a watch they
	// authorised: its Free writes hold (lend.go).
	cleanStart bool
	// approvedInAdvance: the user authorised exactly this task for a watch:
	// no card for what it is lent.
	approvedInAdvance bool
	taint             []string
	// inNode: a step's task works in the step's node (node_steps.go),
	// not in a node of its own.
	inNode string
	// byBrain: the brain started it in its turn (run_task), so the start's
	// card goes with its message; any other start shows its card at once
	// (task_start.go).
	byBrain bool
}

// workerVoice is a worker's standing instruction: its last message reaches
// the user through the assistant they named, so it speaks like it, in the
// user's language (user_lang.go).
func workerVoice(name, lang string) string {
	return "Your final message is relayed to the user as " + name + "'s. Write it, and anything else the user reads, " + userLangEN(lang) +
		", whatever the language of your instructions. Be concise: the result, without preamble.\n\n" + receiptInstruction
}

// taskUntrusted: a task started on what came from outside, which may leave
// something in its device's cache for the next tasks to run: the brain and the
// assistants (they read the mail, the web…), memory extraction (it reads
// their conversations), and a worker started on a tainted turn. Those get a
// cache of their own; workers the user or an untainted turn started share
// the other.
func taskUntrusted(kind string, taint []string) bool {
	return kind != "worker" || len(taint) > 0
}

// runTask schedules a worker task, sends it to its device and starts following it.
func (c *core) runTask(ctx context.Context, r runRequest) (*TaskRow, error) {
	return c.startTask(ctx, "worker", r, deviceTaskSpec{AppendSystemPrompt: workerVoice(c.conf().assistantName(), c.userLang())})
}

// startTask is runTask for any kind of task; extra carries the fields only
// the brain uses (MCP servers, system prompt, allowed tools, resume).
func (c *core) startTask(ctx context.Context, kind string, r runRequest, extra deviceTaskSpec) (*TaskRow, error) {
	if r.Prompt == "" {
		return nil, errors.New("prompt is required")
	}
	if r.Contract != nil {
		if kind != "worker" {
			return nil, errors.New("only a worker task takes a contract")
		}
		if err := r.Contract.normalize(); err != nil { // contract.go
			return nil, err
		}
	}
	about, err := c.aboutIs(r.About)
	if err != nil {
		return nil, fmt.Errorf("about: %w", err)
	}
	if r.Node != "" && kind != "worker" {
		return nil, errors.New("only a worker task works in a node")
	}
	if err := c.nodeParent(r.Node); err != nil { // task_node.go
		return nil, err
	}
	agentName, agent, err := c.conf().agent(r.Agent)
	if err != nil {
		return nil, err
	}
	devices, err := c.store.devices()
	if err != nil {
		return nil, err
	}
	lent, err := c.resolveRequest(r) // resources.go
	if err != nil {
		return nil, err
	}
	folders := lent.Folders
	if r.Contract != nil {
		// Its checks name only what the task is lent (contract.go).
		if err := r.Contract.checkLent(lent.Repo != nil, folderNames(folders)); err != nil {
			return nil, err
		}
	}
	if r.Device != "" && c.store.deviceSuspended(r.Device) {
		return nil, suspendedError(r.Device) // device_access.go
	}
	if len(folders) > 0 && r.Device != "" && !slices.Contains(c.folderHolders(folders), r.Device) {
		return nil, fmt.Errorf("the folders %s are not on %s, nor copied there: leave device empty", strings.Join(folderNames(folders), ", "), r.Device)
	}
	// No online device may fit yet: the task then waits, without a device, for
	// one that would (see placePending). An assistant someone waits on
	// lives on an always-on device (placement.go).
	p := c.taskPlacement(agent, folders)
	p.Resident = residentKind(kind)
	ready := c.schedulable(devices)
	name, err := schedule(ready, r.Device, p, c.store.activeCount)
	var waiting *waitingError
	if errors.As(err, &waiting) {
		name, err = "", nil
	}
	if err != nil {
		return nil, err
	}
	grant := lent.Grant
	if len(grant) > 0 && !harnessOf(agent.Runtime).MCP {
		return nil, fmt.Errorf("agent %s runs %s: catalog tools need a runtime that takes MCP servers", agentName, agent.Runtime)
	}
	// The credentials are checked now, but only read again when the device fetches the task.
	agentEnv, err := c.lend(agentName)
	if err != nil {
		return nil, err
	}
	if err := c.checkAgentEnv(lent.List, agentEnv); err != nil {
		return nil, err
	}
	// The device still gets the spec fields of before, derived from the resources.
	var dir, repo, base, branch string
	if lent.Repo != nil {
		dir, repo, base, branch = lent.Repo.Dir, lent.Repo.Ref, lent.Repo.Base, lent.Repo.Branch
	}
	where := dir
	if repo != "" {
		where = repo
		if branch != "" {
			where += "@" + branch
		}
	}
	var needs []startNeed
	if kind == "worker" && r.askStart && !r.approvedInAdvance {
		needs = append(c.startNeeds(lent.List, r.cleanStart), c.accessStartNeeds(lent.List, r.cleanStart)...) // task_start.go
		needs = append(needs, c.folderNeeds(lent.List, r.cleanStart)...)                                      // folder_values.go
	}
	t := &TaskRow{ID: newTaskID(), Device: name, Agent: agentName, Runtime: agent.Runtime, Model: agent.Model, About: about,
		Dir: where, Title: r.title(), Prompt: r.Prompt, State: statePending,
		Kind: kind, Tools: strings.Join(grant, ","), Folders: lent.folderNames(), Resources: lent.List, Pinned: r.Device != ""}
	if waiting != nil {
		t.PendingReason = waiting.reason
	}
	if len(needs) > 0 {
		t.Desired, t.PendingReason = desiredHeld, "waits for "+c.conf().User+"'s approval to start, asked on a card in the conversation"
	}
	if kind == "worker" {
		if err := c.openTaskNode(t, r, len(needs) > 0); err != nil { // task_node.go
			return nil, err
		}
	}
	if err := c.store.insertTask(t); err != nil {
		return nil, err
	}
	if len(r.taint) > 0 && c.store.get(taskTaintKey(t.ID)) == "" {
		// Started on what someone else wrote: tainted from its start (task_taint.go).
		c.store.set(taskTaintKey(t.ID), "started on a tainted turn: "+strings.Join(r.taint, ", "))
	}
	if r.Contract != nil {
		if err := c.store.setContract(t.ID, r.Contract); err != nil {
			return nil, err
		}
	}
	if name != "" {
		c.logPlaced(t, name) // its access log (device_access.go)
		if r.Device == "" {
			noteFallback(t.ID, kind, name, ready, p)
		}
	}
	spec := extra
	spec.Runtime, spec.Prompt, spec.Dir, spec.Model = agent.Runtime, r.Prompt, dir, agent.Model
	spec.Repo, spec.Ref, spec.Branch, spec.Image = repo, base, branch, agent.Image
	spec.Command = agent.Command
	spec.RepoSkills = c.repoSkills(ctx, lent.Repo)
	spec.Untrusted = taskUntrusted(kind, r.taint)
	spec.AgentEnv, spec.MCPServers, spec.Servers, spec.AlwaysLoad = nil, nil, nil, nil // added when the device fetches the task
	spec.Folders = nil                                                                 // the same
	if kind == "worker" {
		spec.AppendSystemPrompt = strings.TrimSpace(spec.AppendSystemPrompt + "\n\n" + c.taskNowText()) // situations.go
		if c.taskNode(t.ID) != "" {
			spec.AppendSystemPrompt += "\n\n" + nodesInstruction // task_nodes.go
		}
	}
	if len(folders) > 0 {
		spec.AppendSystemPrompt = strings.TrimSpace(spec.AppendSystemPrompt + "\n\n" + folderInstructions(folders))
	}
	if s := c.accessInstructions(lent.List); s != "" {
		spec.AppendSystemPrompt = strings.TrimSpace(spec.AppendSystemPrompt + "\n\n" + s)
	}
	if r.Contract != nil {
		spec.AppendSystemPrompt = strings.TrimSpace(spec.AppendSystemPrompt + "\n\n" + r.Contract.instruction())
		spec.Checks, spec.CheckRounds = r.Contract.Checks, r.Contract.attempts()
	}
	if kind == "worker" {
		c.lendSkills(&spec) // skills_tasks.go
		// What is known of its thread, after what it is asked (thread_card.go).
		if card := c.startCard(about, placeTask, t.ID); card != "" {
			spec.Prompt += "\n\n" + card
		}
	}
	if kind == "worker" && !harnessOf(agent.Runtime).takesSystemPrompt() {
		// It takes no system prompt: it reads it first.
		spec.Prompt = spec.AppendSystemPrompt + "\n\n" + spec.Prompt
		spec.AppendSystemPrompt = ""
	}
	if len(needs) == 0 {
		c.store.updateTask(t.ID, map[string]any{"desired": desiredRunning})
	}
	if err := c.store.setSpec(t.ID, spec); err != nil {
		return nil, err
	}
	if len(needs) > 0 {
		if err := c.holdTask(t, needs, r.taint, r.byBrain); err != nil {
			return nil, err
		}
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
	// Receipt is its last turn's receipt, as eriphe-core checked it (receipt.go).
	Receipt *checkedReceipt `json:"receipt,omitempty"`
	// Contract is what it was asked to make true (contract.go).
	Contract *taskContract `json:"contract,omitempty"`
	Recent   []EventRow    `json:"recent"`
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
			// Shown without its receipt (receipt.go); a message that was
			// only one leaves the one before.
			text, _ := e.Data["text"].(string)
			if t.Kind != "worker" {
				st.LastText = text
			} else if s := stripReceipt(text); s != "" || text == "" {
				st.LastText = s
			}
		case "turn_end":
			if b, _ := e.Data["auth_error"].(bool); b {
				st.AuthError = true
			}
		}
	}
	// An older device reported a task running again once one of its
	// requests made at once was answered: the others of its turn are still
	// pending.
	if t.State == stateWaitingPermission || t.State == stateRunning {
		for _, e := range events {
			pid := fmt.Sprint(e.Data["id"])
			switch {
			case e.Type == "turn_end":
				st.PendingPermissions = nil // a turn's end leaves none waiting
			case e.Type == "permission" && !answered[pid] && !c.store.answered(id, pid):
				st.PendingPermissions = append(st.PendingPermissions, e.Data)
			}
		}
		if len(st.PendingPermissions) > 0 {
			st.PendingPermission = st.PendingPermissions[0]
		}
	}
	if t.Kind == "worker" {
		st.Receipt = crossCheck(events)
		st.Contract = c.store.contract(id)
	}
	st.Recent = events[max(0, len(events)-n):]
	return st, nil
}

// answer replies to what a task waits for: a pending permission (allow or
// deny, with an optional message) or the agent's question (a new turn). The
// answer goes to the task's inbox, which its device delivers. permission names
// the request answered; it may be empty when only one is pending. by says
// who answers: the policy or the user, never the brain, which only replies.
func (c *core) answer(ctx context.Context, id, permission, reply string, allow *bool, by string) (string, error) {
	st, err := c.status(id, 0)
	if err != nil {
		return "", err
	}
	if len(st.PendingPermissions) > 0 || permission != "" {
		if by == byBrain {
			return "", fmt.Errorf("a permission is never yours to answer: it is %s's, on a card", c.conf().User)
		}
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
	if err := c.turnsLeft(id); err != nil { // its budget (task_retry.go)
		return "", err
	}
	if err := c.store.addInbox(id, "message", map[string]any{"text": reply}); err != nil {
		return "", err
	}
	// Running until the device says otherwise, so the same turn is not answered twice.
	c.store.updateTask(id, map[string]any{"state": stateRunning})
	c.bump()
	return "message sent", nil
}

// stop asks for a task to stop; a task its device never picked up, or that
// still waits for a device, stops at once.
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

// stopByBrain is stop for the brain, which is not told of the end it asked
// for; the assistant controller stops its own tasks with it too.
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
