package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The assistant controller: one loop keeps every assistant's task as its
// spec says. An enabled assistant has exactly one task running; one that
// fails is started again after a wait, resuming its session while that
// works; a change of its agent, instructions or tools restarts it once its
// turn is over; a disabled or removed one has its task stopped. What an
// assistant is comes from its role's card, what wakes it from its
// triggers' cards: the controller never asks for a name.

// assistantRole is a role's card: what an assistant of that role gets and
// does. A new role is one more card (brain.go has the brain's).
type assistantRole struct {
	// What its assistants may do (assistantPowers): checked where it is
	// decided, never read from a name.
	Powers      []string
	Prompt      string   // its built-in standing instructions
	StartPrompt string   // its first turn, unless firstPrompt gives it
	Triggers    []string // its triggers when its spec names none
	// The node profile that keeps its sessions between its tasks;
	// assistant-<name> by default.
	Profile      string
	FreeReads    bool     // the catalog's believed reads: in its grant, allowed in advance
	Network      bool     // the system network: it reaches eriphe-core by name
	Streams      bool     // its text as it is written, for its triggers' wrote
	AllowedTools []string // its harness's own tools allowed in advance
	NoWebSearch  string   // told in its instructions when its harness cannot search the web

	// Hooks; nil does nothing.
	instructions func(c *core, a Assistant, prompt string) string     // completes its standing prompt
	firstPrompt  func(c *core, a Assistant, resume bool) string       // its first turn
	started      func(c *core, a Assistant, t *TaskRow, resumed bool) // after its task is created
	// permission is its approval path; nil refuses everything: an
	// assistant works through its MCP tools.
	permission func(c *core, ctx context.Context, a Assistant, t *TaskRow) error
	failing    func(c *core, a Assistant) // its starts keep failing
}

// assistantRoles by name. Each card registers itself (init).
var assistantRoles = map[string]assistantRole{}

// What a role's assistants may do.
const (
	// It speaks to the user: what it writes goes to the conversation.
	powerTalksToUser = "talksToUser"
	// It may have the conversation trigger: the user's messages and the
	// tasks' events. One enabled assistant at most has it.
	powerReceivesConversation = "receivesConversation"
	// It has eriphe-core's own MCP tools (selfServer): it starts, follows
	// and stops tasks, and reads and writes the memory.
	powerLaunchesTasks = "launchesTasks"
	// It lends the tasks it starts their resources (run_task's).
	powerLendsResources = "lendsResources"
)

var assistantPowers = []string{powerTalksToUser, powerReceivesConversation, powerLaunchesTasks, powerLendsResources}

func (r assistantRole) can(power string) bool { return slices.Contains(r.Powers, power) }

// check tells whether a role's powers hold together: what hears the
// conversation answers in it, and only what starts tasks lends to them.
func (r assistantRole) check() error {
	for _, p := range r.Powers {
		if !slices.Contains(assistantPowers, p) {
			return fmt.Errorf("unknown power %q", p)
		}
	}
	switch {
	case r.can(powerReceivesConversation) && !r.can(powerTalksToUser):
		return fmt.Errorf("%s needs %s", powerReceivesConversation, powerTalksToUser)
	case r.can(powerLendsResources) && !r.can(powerLaunchesTasks):
		return fmt.Errorf("%s needs %s", powerLendsResources, powerLaunchesTasks)
	case slices.Contains(r.Triggers, triggerConversation) && !r.can(powerReceivesConversation):
		return fmt.Errorf("the %s trigger needs %s", triggerConversation, powerReceivesConversation)
	}
	return nil
}

// assistantTrigger is a trigger's card: what it brings an assistant at
// idle, and where what the assistant says goes.
type assistantTrigger struct {
	relay   func(c *core, a Assistant, t *TaskRow)                                    // its new events, out
	wrote   func(c *core, a Assistant, t *TaskRow, events bool, draft string)         // its node's report (Streams)
	deliver func(c *core, ctx context.Context, a Assistant, t *TaskRow) (bool, error) // what waits, in: true if a turn started
}

var assistantTriggers = map[string]assistantTrigger{}

// runAssistants keeps the assistants alive and feeds them, one turn at a time.
func (c *core) runAssistants(ctx context.Context, token string) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		c.tendAssistants(ctx, token)
		select {
		case <-ctx.Done():
		case <-c.inbox:
		case <-tick.C:
		}
	}
}

// tendAssistants is one pass of the controller over every assistant, and
// over the statuses of those that are gone.
func (c *core) tendAssistants(ctx context.Context, token string) {
	// Read each time: the workspace may give one another agent.
	m := c.conf()
	for _, n := range slices.Sorted(maps.Keys(m.Assistants)) {
		if err := c.tendAssistant(ctx, m.Assistants[n], token); err != nil {
			log.Printf("%s: %v", c.assistantLog(m.Assistants[n]), err)
			c.updateAssistant(n, func(st *AssistantStatus) { st.LastError = err.Error() })
		}
	}
	for _, n := range c.assistantStatusNames() {
		if _, ok := m.Assistants[n]; ok {
			continue
		}
		if id := c.assistantStatus(n).Task; id != "" {
			if t, err := c.store.task(id); err == nil && !finished(t.State) {
				if c.store.get("brain_stopped:"+id) == "" {
					log.Printf("assistants: %s was removed, stopping task %s", n, id)
					c.stopByBrain(ctx, id)
				}
				continue
			}
		}
		c.dropAssistantStatus(n)
	}
}

// assistantLog is how the log names an assistant: the brain as it always did.
func (c *core) assistantLog(a Assistant) string {
	if a.Role == assistantRoleBrain {
		return "brain"
	}
	return "assistant " + a.Name
}

func (c *core) tendAssistant(ctx context.Context, a Assistant, token string) error {
	r := assistantRoles[a.Role]
	st := c.assistantStatus(a.Name)
	var t *TaskRow
	if st.Task != "" {
		t, _ = c.store.task(st.Task)
	}
	if a.Disabled {
		if t != nil && !finished(t.State) && c.store.get("brain_stopped:"+t.ID) == "" {
			log.Printf("%s: disabled, stopping task %s", c.assistantLog(a), t.ID)
			return c.stopByBrain(ctx, t.ID)
		}
		return nil
	}
	if t == nil || finished(t.State) {
		if a.Agent == "" {
			return nil // no agent can run it yet: the workspace adds one
		}
		if c.catalogSettling() {
			return nil // its free reads are fixed when it starts: let the catalog list them first
		}
		// Wait between restarts: 5 s, then doubling up to 5 min.
		if time.Now().Unix() < st.NextStart {
			return nil
		}
		fails := st.Failures
		if t != nil && t.State == stateFailed {
			fails++
		} else {
			fails = 0
		}
		delay := min(5*time.Second<<min(fails, 6), 5*time.Minute)
		c.updateAssistant(a.Name, func(st *AssistantStatus) {
			st.Failures, st.NextStart = fails, time.Now().Add(delay).Unix()
		})
		if fails == 3 && r.failing != nil {
			r.failing(c, a)
		}
		// A resume that keeps failing is dropped: better a fresh session than none.
		resume := t != nil && fails < 2
		return c.startAssistant(ctx, a, r, token, resume)
	}
	for _, tr := range a.triggers() {
		if h := assistantTriggers[tr].relay; h != nil {
			h(c, a, t)
		}
	}
	c.noteSession(a, t)
	if t.SessionID != "" && t.SessionID != st.Session {
		c.updateAssistant(a.Name, func(st *AssistantStatus) { st.Session, st.SessionRuntime = t.SessionID, t.Runtime })
	}
	// Only a turn that went through clears the failures: an agent that
	// cannot start may still say it is idle before it exits.
	if t.State == stateIdle && c.assistantStatus(a.Name).Failures != 0 && c.lastTurnOK(t.ID) {
		c.updateAssistant(a.Name, func(st *AssistantStatus) { st.Failures, st.LastError = 0, "" })
	}

	switch t.State {
	case stateWaitingPermission:
		if r.permission == nil {
			return c.refuseAll(ctx, t)
		}
		return r.permission(c, ctx, a, t)
	case stateIdle:
		// Its agent lists its tools once: a tool added or changed since needs a
		// new one, on the same session, before it reads what is waiting.
		if h, was := c.assistantToolsHash(a), c.assistantStatus(a.Name).ToolsHash; h != "" && h != was {
			if was == "" { // started before it was kept
				c.updateAssistant(a.Name, func(st *AssistantStatus) { st.ToolsHash = h })
			} else {
				log.Printf("%s: its tools changed, restarting task %s", c.assistantLog(a), t.ID)
				return c.stopByBrain(ctx, t.ID)
			}
		}
		// Another agent, or the same one changed, or other instructions:
		// the next turn is the new spec's.
		if h, was := c.assistantSpecHash(a), c.assistantStatus(a.Name).SpecHash; h != was {
			if was == "" { // started before it was kept
				c.updateAssistant(a.Name, func(st *AssistantStatus) { st.SpecHash = h })
			} else {
				log.Printf("%s: its agent changed to %s, restarting task %s", c.assistantLog(a), a.Agent, t.ID)
				return c.stopByBrain(ctx, t.ID)
			}
		}
		for _, tr := range a.triggers() {
			h := assistantTriggers[tr].deliver
			if h == nil {
				continue
			}
			started, err := h(c, ctx, a, t)
			if err != nil {
				return err
			}
			if started {
				c.updateAssistant(a.Name, func(st *AssistantStatus) { st.LastTurn = time.Now().Unix() })
				break // one turn at a time
			}
		}
	}
	return nil
}

// refuseAll answers an assistant without an approval path: no.
func (c *core) refuseAll(ctx context.Context, t *TaskRow) error {
	st, err := c.status(t.ID, 0)
	if err != nil {
		return err
	}
	for _, p := range st.PendingPermissions {
		pid := fmt.Sprint(p["id"])
		if c.store.answered(t.ID, pid) {
			continue
		}
		no := false
		if _, err := c.answer(ctx, t.ID, pid, "Un assistant n'utilise que ses outils MCP.", &no, byPolicy); err != nil {
			return err
		}
	}
	return nil
}

// startAssistant starts an assistant's task, resuming its previous session if asked.
func (c *core) startAssistant(ctx context.Context, a Assistant, r assistantRole, token string, resume bool) error {
	raw, err := a.standingPrompt()
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	prompt := standingInstructions(raw, c.conf().User, a.displayName())
	// What its harness cannot do on the web, from its card (harnesses.go).
	if _, ag, err := c.conf().agent(a.Agent); err == nil && r.NoWebSearch != "" && !harnessOf(ag.Runtime).BuiltinWebSearch {
		prompt += "\n\n" + r.NoWebSearch + "\n"
	}
	if r.instructions != nil {
		prompt = r.instructions(c, a, prompt)
	}
	// Its catalog tools are not allowed in advance, but for its free reads,
	// added when its node fetches the task: the others go through its
	// approval path. The deprecated mcp_servers still are.
	var allowed []string
	if r.can(powerLaunchesTasks) {
		allowed = append(allowed, "mcp__"+selfServer)
	}
	allowed = append(allowed, r.AllowedTools...)
	for _, name := range slices.Sorted(maps.Keys(a.MCPServers)) {
		allowed = append(allowed, "mcp__"+name)
	}
	profile := r.Profile
	if profile == "" {
		profile = "assistant-" + a.Name
	}
	// A stable profile on the node keeps the session's transcript between its tasks.
	// Its MCP servers carry tokens: they are added when its node fetches the task.
	extra := nodeTaskSpec{AppendSystemPrompt: prompt, AllowedTools: allowed, Profile: profile, NoAgentMemory: true}
	// Another harness cannot resume this one's session: the context pack
	// carries the thread over instead (brain_context.go).
	_, agent, _ := c.conf().agent(a.Agent)
	st := c.assistantStatus(a.Name)
	runtime := st.SessionRuntime
	if runtime == "" {
		runtime = "claude-code" // sessions kept before their harness was
	}
	if resume && st.Session != "" && runtime == agent.Runtime {
		extra.Resume = st.Session
	}
	first := r.StartPrompt
	if r.firstPrompt != nil {
		first = r.firstPrompt(c, a, extra.Resume != "")
	}
	t, err := c.startTask(ctx, a.Role, runRequest{Prompt: first, Node: a.Node, Agent: a.Agent,
		Tools: c.conf().assistantTools(a)}, extra)
	if err != nil {
		return err
	}
	// Before its status names the new task: what its role carries over
	// from the previous one is read from it.
	if r.started != nil {
		r.started(c, a, t, extra.Resume != "")
	}
	tools := c.assistantToolsHash(a)
	if tools == "" {
		tools = "unready" // a server it reaches is down: restart once it is back
	}
	spec := c.assistantSpecHash(a)
	c.updateAssistant(a.Name, func(st *AssistantStatus) {
		st.Task, st.Agent, st.LastError = t.ID, t.Agent, ""
		st.ToolsHash, st.SpecHash = tools, spec
		st.SeenSeq = 0
		st.Quiet = true // until its first delivery
		st.Profile = c.profileHash()
		st.Resume = extra.Resume // checked against the session it reports
		st.PackDue = false       // a fresh session has it in its first turn
	})
	log.Printf("%s: started task %s on %s (resume=%v)", c.assistantLog(a), t.ID, a.Node, extra.Resume != "")
	return nil
}

// A resume can also fall through on the node, which then opens a new
// session (ACP, Codex) with the start prompt alone. eriphe-core sees it by
// the session the task reports: another than the one it asked for. The
// pack then goes with the next delivery.

// noteSession compares the session an assistant's task reports with the
// one it was started to resume.
func (c *core) noteSession(a Assistant, t *TaskRow) {
	want := c.assistantStatus(a.Name).Resume
	if t.SessionID == "" || want == "" {
		return
	}
	notResumed := t.SessionID != want
	c.updateAssistant(a.Name, func(st *AssistantStatus) {
		st.Resume = ""
		if notResumed {
			st.PackDue = true
		}
	})
	if notResumed {
		log.Printf("%s: session %s not resumed, %s is a new one: the context pack goes with the next delivery", c.assistantLog(a), want, t.SessionID)
	}
}

// assistantSpecHash fingerprints what an assistant's task was started on:
// its agent's name and spec, its instructions, its node and resources.
// Its tools are assistantToolsHash's.
func (c *core) assistantSpecHash(a Assistant) string {
	h := sha256.New()
	spec := []any{a.Agent, c.conf().Agents[a.Agent], a.Instructions, a.InstructionsFile, a.Node, a.Resources}
	// Its name is in its instructions: another restarts it. Eriphe, the
	// name it had before it could have another, keeps the fingerprint.
	if n := a.displayName(); a.Role == assistantRoleBrain && n != defaultDisplayName || a.Role != assistantRoleBrain && n != a.Name {
		spec = append(spec, n)
	}
	json.NewEncoder(h).Encode(spec)
	return hex.EncodeToString(h.Sum(nil))
}

// standingInstructions are an assistant's instructions with the user's
// name for {{user}} and its own for {{name}}.
func standingInstructions(raw, user, name string) string {
	return strings.NewReplacer("{{user}}", user, "{{name}}", name).Replace(raw)
}

// legacyAgentHash is the fingerprint the brain kept before it was an
// assistant (kv brain_agent): its agent's name and spec.
func (c *core) legacyAgentHash(a Assistant) string {
	h := sha256.New()
	json.NewEncoder(h).Encode([]any{a.Agent, c.conf().Agents[a.Agent]})
	return hex.EncodeToString(h.Sum(nil))
}

// assistantToolsHash fingerprints what an assistant's agent gets of the
// catalog: its grant, and the tools of the servers it reaches. It is ""
// while one of them is not ready, not to restart it for a passing outage,
// and while the catalog settles at start, when its grant is not whole yet.
func (c *core) assistantToolsHash(a Assistant) string {
	if c.catalogSettling() {
		return ""
	}
	grant := c.assistantGrant(a)
	h := sha256.New()
	json.NewEncoder(h).Encode(grant)
	for _, name := range grantServers(grant) {
		if _, ok := c.conf().Servers[name]; !ok {
			continue
		}
		u := c.pool.get(name)
		if u == nil {
			return ""
		}
		status, _, tools := u.snapshot()
		if status != statusReady {
			return ""
		}
		tools = slices.Clone(tools)
		slices.SortFunc(tools, func(a, b *mcp.Tool) int { return strings.Compare(a.Name, b.Name) })
		fmt.Fprintln(h, name)
		json.NewEncoder(h).Encode(tools)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// assistantGrant is what an assistant may use of the catalog: what its
// tools grant and, if its role reads freely, every tool a server says is
// read-only when the server is believed for its annotations (my "reads
// free", our own mail and calendar). It is computed on each use: a tool the
// catalog lists later is in it at once, for the proxy.
func (c *core) assistantGrant(a Assistant) []string {
	out := c.conf().assistantTools(a)
	if !assistantRoles[a.Role].FreeReads {
		return out
	}
	for _, r := range c.freeTools() {
		if g := r.server + "." + globEscape(r.tool); !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// assistantServers are an assistant's MCP servers, with their tokens:
// eriphe-core's own at self_url, if its role launches tasks, and the ones the file
// lists. Without self_url, eriphe-core's own goes through the node
// (spec.Servers): the node reaches eriphe-core wherever it is, and the
// assistant holds no token.
func (c *core) assistantServers(a Assistant, r assistantRole) (map[string]any, error) {
	servers := map[string]any{}
	if r.can(powerLaunchesTasks) && a.SelfURL != "" {
		servers[selfServer] = map[string]any{
			"type": "http", "url": a.SelfURL, "headers": map[string]string{"Authorization": "Bearer " + c.token}}
	}
	for name, srv := range a.MCPServers {
		def := map[string]any{"type": "http", "url": srv.URL}
		switch {
		case srv.TokenFile != "":
			tok, err := readSecret(srv.TokenFile)
			if err != nil {
				return nil, fmt.Errorf("mcp %s: %w", name, err)
			}
			def["headers"] = map[string]string{"Authorization": "Bearer " + tok}
		case srv.TokenEnv != "":
			def["headers"] = map[string]string{"Authorization": "Bearer ${" + srv.TokenEnv + "}"}
		}
		servers[name] = def
	}
	return servers, nil
}

// reportingAssistant is the assistant whose current task id is.
func (c *core) reportingAssistant(id string) (Assistant, bool) {
	n := c.assistantOwning(id)
	if n == "" {
		return Assistant{}, false
	}
	a, ok := c.conf().Assistants[n]
	return a, ok
}

// fetchAssistant completes an assistant task's spec as its node fetches
// it, from its role's card.
func (c *core) fetchAssistant(t *TaskRow, spec *nodeTaskSpec) error {
	a, r, ok := c.assistantOf(t)
	if !ok {
		return nil
	}
	spec.SystemNetwork = r.Network
	spec.Stream = r.Streams
	// One memory: eriphe-core's, not Claude Code's own.
	if spec.Identity == nil {
		spec.Identity = map[string]string{}
	}
	spec.Identity["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	// Its free reads, as the catalog lists them now: allowed in advance,
	// they never wait for a permission. The node reads them when it starts.
	if r.FreeReads {
		spec.AllowedTools = append(append([]string(nil), spec.AllowedTools...), c.brainFreeReads()...)
	}
	var err error
	if spec.MCPServers, err = c.assistantServers(a, r); err != nil {
		return err
	}
	if r.can(powerLaunchesTasks) && a.SelfURL == "" {
		spec.Servers = append(spec.Servers, selfServer) // relayed by its node
	}
	return nil
}
