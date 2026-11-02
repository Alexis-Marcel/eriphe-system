package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Task states. A turn that ends without the agent asking for anything
// leaves the task idle: the orchestrator decides whether it is done.
const (
	stateRunning           = "running"
	stateWaitingPermission = "waiting_permission"
	stateIdle              = "idle"
	stateStopped           = "stopped"
	stateFailed            = "failed"
)

// TaskSpec is what eriphe-core assigns. The identity (environment
// variables) is lent for this task only.
type TaskSpec struct {
	Runtime  string            `json:"runtime"`            // agent in the container: claude-code, opencode, codex or acp
	Prompt   string            `json:"prompt"`             // first user turn
	Image    string            `json:"image,omitempty"`    // worker image; the node's default if empty
	Repo     string            `json:"repo,omitempty"`     // git URL or owner/name on GitHub, cloned into /work/repo
	Ref      string            `json:"ref,omitempty"`      // branch or tag to start from
	Branch   string            `json:"branch,omitempty"`   // work branch to create
	Dir      string            `json:"dir,omitempty"`      // working directory inside the container
	Model    string            `json:"model,omitempty"`    // optional model override
	Command  []string          `json:"command,omitempty"`  // an ACP agent's command line (runtime acp), run as is
	Identity map[string]string `json:"identity,omitempty"` // env vars, e.g. CLAUDE_CODE_OAUTH_TOKEN, GH_TOKEN
	Resume   string            `json:"resume,omitempty"`   // agent session to resume
	TZ       string            `json:"tz,omitempty"`       // the user's time zone, e.g. Europe/Paris

	// Catalog servers lent to the task: the node relays them to eriphe-core
	// (relay.go).
	Servers []string `json:"servers,omitempty"`
	// Those of Servers whose tools are loaded upfront, not searched for.
	AlwaysLoad []string `json:"always_load,omitempty"`

	// For long-lived agents such as the orchestrator brain.
	MCPServers         map[string]any `json:"mcp_servers,omitempty"`          // extra MCP servers, .mcp.json format
	AppendSystemPrompt string         `json:"append_system_prompt,omitempty"` // standing instructions
	AllowedTools       []string       `json:"allowed_tools,omitempty"`        // tools that need no permission
	Profile            string         `json:"profile,omitempty"`              // persistent agent config, so a session can be resumed
	SystemNetwork      bool           `json:"system_network,omitempty"`       // the brain: also on eriphe-core's network, to reach its tools
	Stream             bool           `json:"stream,omitempty"`               // report the text being written as a draft
	NoAgentMemory      bool           `json:"no_agent_memory,omitempty"`      // the agent's own memory and instruction files are off
	// RepoSkills: the agent may load the skills its repository carries;
	// they stay off otherwise (skills.go).
	RepoSkills bool `json:"repo_skills,omitempty"`
	// The user's skills, by name and hash: the node fetches, checks and
	// mounts them (skills.go).
	Skills []TaskSkill `json:"skills,omitempty"`

	// Folders of this machine lent to the task, mounted at
	// /work/folders/<name> (shares.go).
	Folders []TaskFolder `json:"folders,omitempty"`

	// Where the task may connect, enforced by the egress relay (egress.go);
	// none given: the public Internet.
	Egress *EgressRule `json:"egress,omitempty"`

	// A self-hosted model server the agent uses (OpenCode, a selfhosted/
	// model): its address, reachable through Egress.Endpoints, or one
	// behind a node, reached through this node's task listener (model.go).
	ModelEndpoint *ModelEndpoint `json:"model_endpoint,omitempty"`

	// The checks of the task's contract, run in its container when a turn
	// ends, and how many times a failed one goes back to the agent
	// (turnend.go). None from an eriphe-core without contracts.
	Checks      []TaskCheck `json:"checks,omitempty"`
	CheckRounds int         `json:"check_rounds,omitempty"`
}

// ModelEndpoint is a self-hosted, OpenAI-compatible model server: its /v1
// address and the variable of Identity that carries its key, if any. With
// Relay, the server is behind one of the user's nodes: the task reaches it
// at this node's /model/v1, with its own secret, and this node relays to
// eriphe-core, which goes on through that node's tunnel (model.go).
type ModelEndpoint struct {
	BaseURL string `json:"base_url,omitempty"`
	KeyEnv  string `json:"key_env,omitempty"`
	Relay   bool   `json:"relay,omitempty"`
}

// Event is one line of what happened in a task, in order.
type Event struct {
	Seq  int            `json:"seq"`
	Time time.Time      `json:"time"`
	Type string         `json:"type"` // text, tool_use, permission, permission_answer, turn_end, diff, check, retry, check_stop, error, state
	Data map[string]any `json:"data"`
}

type permission struct {
	ID       string         `json:"id"`
	ToolName string         `json:"tool_name"`
	Input    map[string]any `json:"input"`
	answer   chan permAnswer
}

type permAnswer struct {
	Allow   bool   `json:"allow"`
	Message string `json:"message,omitempty"`
}

type Task struct {
	ID        string    `json:"id"`
	Spec      TaskSpec  `json:"-"`
	Runtime   string    `json:"runtime"`
	Image     string    `json:"image"`
	State     string    `json:"state"`
	SessionID string    `json:"session_id,omitempty"`
	Created   time.Time `json:"created"`

	dir      string // <root>/tasks/<id>: its workspace and agent config, kept across restarts
	secret   string // the task container's credential for its permission endpoint
	stopping bool   // stop was asked: the container's exit is not a failure
	mu       sync.Mutex
	seq      int     // last event number, continued across restarts
	events   []Event // events not reported yet
	perms    map[string]*permission
	run      session
	draft    string // the text the agent is writing, reported but never an event
	drafting bool   // a report of the draft is on its way

	inbox     []inboxItem // from the last assignment
	delivery  sync.Mutex  // one delivery at a time
	delivered int         // inbox items handled, up to this seq
	reported  struct {
		state     string
		session   string
		delivered int
		draft     string
	}
	restarts int
	onChange func() // tells the node something must be delivered or reported

	// The MCP servers the agent is given, as its runtime's command read
	// them (runtime_mcp.go), or why they could not be.
	mcp    []mcpServer
	mcpErr error

	egressHosts map[string]bool // hosts the task went to through the relay, logged once each

	// The agent's directory mounted at /opt/eriphe while the container runs
	// (inject.go), and the Eriphe/ folders it writes to (handback.go).
	agentDir  string
	results   []string
	onTurnEnd func()

	// onIdle runs when a turn has ended and the task is idle (turnend.go);
	// while it runs, finishing holds the task reported as running.
	onIdle    func()
	finishing int // runs of onIdle under way

	// The checks of its contract (turnend.go): no check after the turn
	// asked with skip_checks; failed checks sent back so far, and the last
	// failure, never sent back twice.
	skipChecks  bool
	checkRounds int
	lastFailure string

	// With the container engine (apple.go): the task's own network, and the
	// node's address on it, where the task reaches the node.
	network, gateway string

	// With a model behind a node: where the task reaches it, this node's
	// task listener (model.go), set when its agent starts.
	modelURL string
}

// emit records an event. Any event ends the draft: a text completes it,
// anything else means it stopped there.
func (t *Task) emit(typ string, data map[string]any) {
	t.mu.Lock()
	t.seq++
	t.events = append(t.events, Event{Seq: t.seq, Time: time.Now(), Type: typ, Data: data})
	t.draft = ""
	notify, turnEnd := t.onChange, t.onTurnEnd
	t.mu.Unlock()
	if notify != nil {
		notify()
	}
	if typ == "turn_end" && turnEnd != nil {
		go turnEnd()
	}
}

// draftEvery spaces the reports of a growing draft.
const draftEvery = 100 * time.Millisecond

// write adds to the draft: the text the agent is writing, before it comes
// whole as a text event. It is reported at most every draftEvery, and only
// the latest one: eriphe-core shows it live and never stores it.
func (t *Task) write(text string) {
	t.mu.Lock()
	t.draft += text
	scheduled := t.drafting
	t.drafting = true
	notify := t.onChange
	t.mu.Unlock()
	if scheduled || notify == nil {
		return
	}
	time.AfterFunc(draftEvery, func() {
		t.mu.Lock()
		t.drafting = false
		t.mu.Unlock()
		notify()
	})
}

func (t *Task) setState(s string) {
	t.mu.Lock()
	t.State = s
	idle := t.onIdle
	if s != stateIdle {
		idle = nil
	}
	if idle != nil {
		t.finishing++ // in the same lock: never reported idle before it ran
	}
	t.mu.Unlock()
	t.emit("state", map[string]any{"state": s})
	if idle != nil {
		go idle()
	}
}

// reportedState is the state eriphe-core is told: running while the end of
// a turn is still being looked at.
func (t *Task) reportedState() string {
	if t.finishing > 0 && t.State == stateIdle {
		return stateRunning
	}
	return t.State
}

// busyFinishing tells whether the end of the turn is still being looked at.
func (t *Task) busyFinishing() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.finishing > 0
}

func (t *Task) state() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.State
}

func finished(state string) bool { return state == stateStopped || state == stateFailed }

type node struct {
	opt      options
	mu       sync.Mutex
	tasks    map[string]*Task
	services map[string]*service // local MCP servers, by name (service.go)
	retired  map[string]*service // unassigned services whose container is stopping
	kick     chan struct{}       // something to deliver or report
	upd      *updates            // eriphe-core's update request, for the updater (update.go)
	nets     taskNets            // the tasks' networks, with the container engine (apple.go)
	agents   *agentStore         // the agents injected into tasks (inject.go)
	prep     preparer            // what eriphe-core wants ready on this node
	awake    *keepAwake          // the machine out of sleep while a task works (awake.go)
	// skillFetch gets a task's skill from eriphe-core (skills.go).
	skillFetch func(ctx context.Context, taskID, sha string) ([]byte, error)
}

func newNode(o options) *node {
	return &node{opt: o, tasks: map[string]*Task{}, services: map[string]*service{}, retired: map[string]*service{}, kick: make(chan struct{}, 1),
		upd: &updates{root: o.root}, awake: newKeepAwake(o.root), agents: newAgentStore(o.root, o.taskBin), prep: preparer{wake: make(chan struct{}, 1)}}
}

func (n *node) poke() {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// The node's API, through the tunnel: what this machine offers. Tasks come
// from its assignments (sync.go), not from calls.
func (n *node) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/node", n.handleNode)
	mux.HandleFunc("/v1/services/{name}/mcp", n.handleServiceMCP)
	mux.HandleFunc("POST /v1/wake", n.handleWake)
	mux.HandleFunc("/v1/model/{path...}", n.handleModel)
	n.shareRoutes(mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (n *node) get(id string) *Task {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.tasks[id]
}

// handleNode reports what this machine offers, for the scheduler.
func (n *node) handleNode(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	runtimes := []string{}
	var dockerVersion, engineVersion string
	var images []string
	if n.apple() {
		// Apple's container: no Docker, its own version and images.
		if engineVersion = appleVersion(ctx, n.opt.docker); engineVersion != "" {
			runtimes = injectedRuntimes
		}
		images = appleImages(ctx, n.opt.docker)
	} else {
		out, err := exec.CommandContext(ctx, n.opt.docker, "version", "--format", "{{.Server.Version}}").Output()
		if err == nil {
			dockerVersion = string(trimNL(out))
			engineVersion = dockerVersion
			runtimes = injectedRuntimes
		}
		images = localImages(ctx, n.opt.docker)
	}
	active := n.running()
	writeJSON(w, http.StatusOK, map[string]any{
		"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(),
		"docker": dockerVersion, "engine": n.opt.engine, "engine_version": engineVersion,
		"image": n.opt.image, "runtimes": runtimes, "labels": n.opt.labels,
		"active_tasks": active, "memory_bytes": memoryBytes(), "volumes": n.opt.volumeNames(),
		// The folders shared on this machine, never their paths (shared.go).
		"folders": n.sharedList(),
		"images":  images, "services": engineVersion != "",
		"always_on": n.opt.alwaysOn, "keeps_awake": n.awake.how, "version": version, "install": n.opt.install,
		// Its network card, for Wake-on-LAN (wake.go).
		"wake":   n.opt.wake,
		"update": n.upd.info(),
		// The agents unpacked here: a task of theirs starts at once (inject.go).
		"agents_ready": n.agents.readyRuntimes(),
		// It names a folder's results subfolder as eriphe-core says (shares.go).
		"results_named": true,
		// A model server eriphe-core may reach through the tunnel (model.go).
		"model_server": n.opt.modelServer != "",
		// How long the machine's keyboard and mouse have been idle (idle.go).
		"idle": userIdle(ctx, r.URL.Query()),
		// What it may tell of the user's session, at the level asked (context.go).
		"context": machineSession(ctx, requestLevel(r.URL.Query().Get("context"))),
	})
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// decide records a permission request from a task's agent and blocks until
// eriphe-core answers through the task's inbox, or denies it after an hour.
func (t *Task) decide(ctx context.Context, tool string, input map[string]any) permAnswer {
	p := &permission{ID: newID(), ToolName: tool, Input: input, answer: make(chan permAnswer, 1)}
	t.mu.Lock()
	t.perms[p.ID] = p
	t.mu.Unlock()
	t.setState(stateWaitingPermission)
	t.emit("permission", map[string]any{"id": p.ID, "tool_name": tool, "input": input})

	var a permAnswer
	select {
	case a = <-p.answer:
	case <-time.After(time.Hour):
		a = permAnswer{Allow: false, Message: "No answer within an hour."}
	case <-ctx.Done():
		a = permAnswer{Allow: false, Message: "Cancelled."}
	}
	t.mu.Lock()
	delete(t.perms, p.ID)
	t.mu.Unlock()
	t.emit("permission_answer", map[string]any{"id": p.ID, "allow": a.Allow, "message": a.Message})
	t.setState(stateRunning)
	return a
}

func validName(p string) bool {
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return p != ""
}

var errNotIdle = errors.New("the agent is busy; wait for the end of its turn")
