package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Assistants are Eriphe's long-lived agents: each owns one task, as a
// Deployment owns its Pod. Its spec says what it is (a role), what it runs
// on (an agent, a node), what it is told (instructions), what it may use
// (tools, resources) and what wakes it (triggers); its status is what one
// controller (assistant_controller.go) keeps of the task it runs.
//
// The brain is the built-in assistant "eriphe", of role brain: the file's
// brain section is its spec, read-only like the file's other catalog
// entries, and the workspace sets what the file leaves out (its agent, its
// tools). Other assistants are added through the API; their role says what
// they get (assistantRoles), never their name.

const (
	kindAssistant = "assistant"

	brainAssistant      = "eriphe" // the built-in assistant, of role brain
	defaultDisplayName  = "Eriphe" // the brain's name, until the user gives it another
	assistantRoleBrain  = "brain"
	triggerConversation = "conversation" // the user's messages and what happens to the tasks
)

// Assistant is an assistant's spec.
type Assistant struct {
	Name string `json:"-"`
	Role string `json:"role"` // a card of assistantRoles
	// What the user calls it, as a person: in its instructions ({{name}})
	// and in the workspace's words. Empty: Eriphe for the brain, else its id.
	DisplayName string `json:"display_name,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"` // its task is stopped, and none started
	Agent       string `json:"agent,omitempty"`    // empty: the first agent that can run it
	Node        string `json:"node,omitempty"`     // its preferred node; empty lets the scheduler choose
	// Its standing instructions: the role's built-in ones unless replaced,
	// by text or, in the configuration file only, by a file. {{user}} is
	// replaced by the user's name, {{name}} by its own.
	Instructions     string `json:"instructions,omitempty"`
	InstructionsFile string `json:"instructions_file,omitempty"`
	// Its grant from the catalog, like a task's: server names or
	// server.tool globs. Its role may add the catalog's free reads.
	Tools []string `json:"tools,omitempty"`
	// What it is lent besides its tools, as run_task's (resources.go).
	// TODO: lend them at start, once an assistant's resources have their
	// approval path and taint gate; until then they are checked and kept.
	Resources []ResourceRef `json:"resources,omitempty"`
	// What wakes it: conversation (the user's messages, the tasks' events).
	// Later source:<name> and schedule. Empty: its role's.
	Triggers []string `json:"triggers,omitempty"`
	// The assistant it reports to: the brain, the only one it may name.
	// Empty: the brain, for any but the brain.
	ReportsTo string `json:"reports_to,omitempty"`

	// The configuration file's only: eriphe-core's MCP URL as seen from its
	// node (without it, its node relays eriphe-core's tools), and the
	// deprecated MCP servers given to it with their token.
	SelfURL    string               `json:"self_url,omitempty"`
	MCPServers map[string]MCPServer `json:"mcp_servers,omitempty"`
}

// triggers are the assistant's, or its role's.
func (a Assistant) triggers() []string {
	if len(a.Triggers) > 0 {
		return a.Triggers
	}
	return assistantRoles[a.Role].Triggers
}

func (a Assistant) has(trigger string) bool { return slices.Contains(a.triggers(), trigger) }

// displayName is what the user calls it.
func (a Assistant) displayName() string {
	switch {
	case a.DisplayName != "":
		return a.DisplayName
	case a.Role == assistantRoleBrain:
		return defaultDisplayName
	}
	return a.Name
}

// brainEntry is the brain's spec, enabled or not: the assistant with the
// conversation trigger, or the built-in one; nil when there is none.
func (c *Config) brainEntry() *Assistant {
	if b := c.brain(); b != nil {
		return b
	}
	for _, n := range slices.Sorted(maps.Keys(c.Assistants)) {
		if a := c.Assistants[n]; a.has(triggerConversation) {
			return &a
		}
	}
	return nil
}

// reportsTo is the name of the assistant a reports to: its reports_to, or
// the brain; "" for the brain itself, or when there is no brain.
func (c *Config) reportsTo(a Assistant) string {
	if a.has(triggerConversation) {
		return ""
	}
	if a.ReportsTo != "" {
		return a.ReportsTo
	}
	if b := c.brainEntry(); b != nil {
		return b.Name
	}
	return ""
}

// assistantName is what the user calls their assistant, the one the
// conversation goes to: the brain's display name, or Eriphe.
func (c *Config) assistantName() string {
	if b := c.brain(); b != nil {
		return b.displayName()
	}
	if a, ok := c.Assistants[brainAssistant]; ok {
		return a.displayName()
	}
	return defaultDisplayName
}

// A display name is short and speakable: 1 to 32 letters, digits, spaces,
// apostrophes and dashes, starting with a letter or a digit.
var displayNameRE = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{M}\p{N} '’-]*$`)

// normalizeDisplayName trims a display name and folds its runs of spaces.
func normalizeDisplayName(s string) string { return strings.Join(strings.Fields(s), " ") }

func checkDisplayName(s string) error {
	if n := utf8.RuneCountInString(s); n == 0 || n > 32 || !displayNameRE.MatchString(s) {
		return fmt.Errorf("display_name %q: 1 to 32 letters, digits, spaces, apostrophes or dashes", s)
	}
	return nil
}

func badDisplayName(n string, err error) error {
	return &catalogError{http.StatusBadRequest, "assistants.badname", n, err}
}

// standingPrompt is its standing instructions, before {{user}} and {{name}} are replaced.
func (a Assistant) standingPrompt() (string, error) {
	switch {
	case a.InstructionsFile != "":
		raw, err := os.ReadFile(a.InstructionsFile)
		return string(raw), err
	case a.Instructions != "":
		return a.Instructions, nil
	}
	return assistantRoles[a.Role].Prompt, nil
}

// assistant is the built-in assistant the file's brain section describes.
func (b *BrainConfig) assistant() Assistant {
	return Assistant{Name: brainAssistant, Role: assistantRoleBrain, DisplayName: b.DisplayName, Agent: b.Agent, Node: b.Node,
		InstructionsFile: b.PromptFile, SelfURL: b.SelfURL, Tools: b.Tools, MCPServers: b.MCPServers}
}

// fileAssistants are the configuration file's: its assistants, and the
// brain from its brain section.
func fileAssistants(base *Config) map[string]Assistant {
	out := map[string]Assistant{}
	for n, a := range base.Assistants {
		a.Name = n
		out[n] = a
	}
	if base.Brain != nil {
		if _, ok := out[brainAssistant]; !ok {
			out[brainAssistant] = base.Brain.assistant()
		}
	}
	return out
}

// overlay is a file's assistant with what the workspace sets of what the
// file leaves out: a field the file sets is the file's.
func overlay(file, ws Assistant) Assistant {
	a := file
	if a.DisplayName == "" {
		a.DisplayName = ws.DisplayName
	}
	if a.Agent == "" {
		a.Agent = ws.Agent
	}
	if a.Node == "" {
		a.Node = ws.Node
	}
	if a.Instructions == "" && a.InstructionsFile == "" {
		a.Instructions = ws.Instructions
	}
	if len(a.Tools) == 0 {
		a.Tools = ws.Tools
	}
	if len(a.Resources) == 0 {
		a.Resources = ws.Resources
	}
	if len(a.Triggers) == 0 {
		a.Triggers = ws.Triggers
	}
	if a.ReportsTo == "" {
		a.ReportsTo = ws.ReportsTo
	}
	if !a.Disabled {
		a.Disabled = ws.Disabled
	}
	return a
}

// readOnlyField names the first field of a workspace edit that changes
// what the file sets, or "".
func readOnlyField(file, ws Assistant) string {
	switch {
	case ws.Role != "" && ws.Role != file.Role:
		return "role"
	case ws.DisplayName != "" && file.DisplayName != "" && ws.DisplayName != file.DisplayName:
		return "display_name"
	case ws.Agent != "" && file.Agent != "" && ws.Agent != file.Agent:
		return "agent"
	case ws.Node != "" && file.Node != "" && ws.Node != file.Node:
		return "node"
	case ws.Instructions != "" && (file.Instructions != "" || file.InstructionsFile != "") && ws.Instructions != file.Instructions:
		return "instructions"
	case len(ws.Tools) > 0 && len(file.Tools) > 0 && !slices.Equal(ws.Tools, file.Tools):
		return "tools"
	case len(ws.Resources) > 0 && len(file.Resources) > 0 && !reflect.DeepEqual(ws.Resources, file.Resources):
		return "resources"
	case len(ws.Triggers) > 0 && len(file.Triggers) > 0 && !slices.Equal(ws.Triggers, file.Triggers):
		return "triggers"
	case ws.ReportsTo != "" && file.ReportsTo != "" && ws.ReportsTo != file.ReportsTo:
		return "reports_to"
	}
	return ""
}

// mergeAssistants are the file's assistants, with the workspace's edits,
// and the workspace's own. One without an agent, or whose workspace agent
// is gone, runs on the default agent, or the first that can run it.
func mergeAssistants(base *Config, m *Config, d dynamicCatalog) map[string]Assistant {
	out := map[string]Assistant{}
	for n, a := range fileAssistants(base) {
		fileAgent := a.Agent
		if ws, ok := d.Assistants[n]; ok {
			a = overlay(a, ws)
		}
		if fileAgent == "" {
			if _, ok := m.Agents[a.Agent]; !ok {
				a.Agent = firstAgentFor(m)
			}
		}
		out[n] = a
	}
	for n, a := range d.Assistants {
		if _, ok := out[n]; ok {
			continue
		}
		a.Name = n
		if _, ok := m.Agents[a.Agent]; !ok {
			a.Agent = firstAgentFor(m)
		}
		out[n] = a
	}
	return out
}

func firstAgentFor(m *Config) string {
	for _, n := range append([]string{m.DefaultAgent}, slices.Sorted(maps.Keys(m.Agents))...) {
		if a, ok := m.Agents[n]; ok && harnessOf(a.Runtime).canBeBrain() {
			return n
		}
	}
	return ""
}

// brain is the assistant the conversation goes to: the enabled one with
// the conversation trigger (there is one at most), or nil.
func (c *Config) brain() *Assistant {
	for _, n := range slices.Sorted(maps.Keys(c.Assistants)) {
		if a := c.Assistants[n]; !a.Disabled && a.has(triggerConversation) {
			return &a
		}
	}
	return nil
}

// assistantTools is an assistant's grant, without the servers the catalog
// does not have (yet).
func (c *Config) assistantTools(a Assistant) []string {
	var out []string
	for _, g := range a.Tools {
		server, _, _ := strings.Cut(strings.TrimSpace(g), ".")
		if _, ok := c.Servers[server]; ok {
			out = append(out, strings.TrimSpace(g))
		}
	}
	return out
}

// checkAssistants checks every assistant against the merged catalog.
func (c *Config) checkAssistants() error {
	conversation := ""
	for _, n := range slices.Sorted(maps.Keys(c.Assistants)) {
		a := c.Assistants[n]
		if err := checkAssistant(c, n, a); err != nil {
			return err
		}
		if !a.Disabled && a.has(triggerConversation) {
			if conversation != "" {
				return fmt.Errorf("assistants %s and %s both have the conversation: only one may", conversation, n)
			}
			conversation = n
		}
	}
	// One reports to the brain: the one with the conversation, enabled or not.
	for _, n := range slices.Sorted(maps.Keys(c.Assistants)) {
		a := c.Assistants[n]
		if a.ReportsTo == "" {
			continue
		}
		what := "assistant " + n
		if a.has(triggerConversation) {
			return fmt.Errorf("%s: reports_to %q: the brain reports to no assistant", what, a.ReportsTo)
		}
		if to, ok := c.Assistants[a.ReportsTo]; !ok || !to.has(triggerConversation) {
			return fmt.Errorf("%s: reports_to %q: it reports to the brain, the assistant with the conversation", what, a.ReportsTo)
		}
	}
	return nil
}

func checkAssistant(c *Config, name string, a Assistant) error {
	if !serverName.MatchString(name) {
		return fmt.Errorf("assistant %q: a name of lowercase letters, digits and dashes, up to 32", name)
	}
	what := "assistant " + name
	if name == brainAssistant {
		what = "brain" // as the file names it
	}
	r, ok := assistantRoles[a.Role]
	if !ok {
		return fmt.Errorf("%s: unknown role %q", what, a.Role)
	}
	if err := r.check(); err != nil {
		return fmt.Errorf("%s: role %s: %w", what, a.Role, err)
	}
	// What hears the conversation is the brain: its role says it may.
	if a.has(triggerConversation) && !r.can(powerReceivesConversation) {
		return fmt.Errorf("%s: the %s trigger is for a role that receives the conversation, not %s", what, triggerConversation, a.Role)
	}
	if a.DisplayName != "" {
		if err := checkDisplayName(a.DisplayName); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
	}
	for _, t := range a.Triggers {
		if _, ok := assistantTriggers[t]; !ok {
			return fmt.Errorf("%s: trigger %q is not supported (yet): conversation is", what, t)
		}
	}
	if a.Agent != "" && len(c.Agents) > 0 { // none: it waits for one
		ag, ok := c.Agents[a.Agent]
		if !ok && c.fileOnly {
			return nil // it may be one of the workspace's, not loaded yet: it says so if it is missing
		}
		if !ok {
			return fmt.Errorf("%s: agent %q is not an agent", what, a.Agent)
		}
		// Its tools are MCP servers, its prompt standing instructions.
		if !harnessOf(ag.Runtime).canBeBrain() {
			return fmt.Errorf("%s: agent %q runs %s, the %s needs a runtime that takes MCP servers and a system prompt", what, a.Agent, ag.Runtime, a.Role)
		}
	}
	for s := range a.MCPServers {
		if _, ok := c.Servers[s]; ok {
			return fmt.Errorf("%s: mcp_servers.%s is also in servers: keep only the catalog entry", what, s)
		}
	}
	// It may be granted a tool added in the workspace: an unknown server is
	// checked for its form only, and left out until it exists (assistantTools).
	for _, g := range a.Tools {
		if err := checkGrantEntry(g); err != nil {
			return fmt.Errorf("%s.tools: %w", what, err)
		}
	}
	if _, err := normalizeResources(runRequest{Resources: a.Resources}); err != nil {
		return fmt.Errorf("%s.resources: %w", what, err)
	}
	return nil
}

// checkFileAssistants checks the file's assistants alone, before the
// workspace's catalog: their agents may be the workspace's.
func (c *Config) checkFileAssistants() error {
	for n, a := range c.Assistants {
		if n == brainAssistant && c.Brain != nil {
			return fmt.Errorf("assistants.%s: the brain section is this assistant: keep only one", n)
		}
		if len(a.MCPServers) > 0 {
			return fmt.Errorf("assistants.%s: mcp_servers is deprecated: use servers and tools", n)
		}
	}
	m := *c
	m.Assistants = fileAssistants(c)
	m.fileOnly = true
	return m.checkAssistants()
}

// The workspace's assistants.

// putAssistant adds or replaces a workspace assistant, or sets what the
// file leaves out of one of its own. The workspace may not name files or
// tokens: instructions_file, self_url and mcp_servers are the file's.
func (c *core) putAssistant(name string, a Assistant) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	return c.putAssistantLocked(name, a, "")
}

func (c *core) putAssistantLocked(name string, a Assistant, event string) error {
	if err := checkEntryName(kindAssistant, name); err != nil {
		return err
	}
	if a.InstructionsFile != "" || a.SelfURL != "" || len(a.MCPServers) > 0 {
		return errInvalid(errors.New("instructions_file, self_url and mcp_servers belong to the configuration file"))
	}
	a.Name = name
	a.DisplayName = normalizeDisplayName(a.DisplayName)
	if a.DisplayName != "" {
		if err := checkDisplayName(a.DisplayName); err != nil {
			return badDisplayName(a.DisplayName, err)
		}
	}
	file, inBase := fileAssistants(c.base)[name]
	if inBase {
		if f := readOnlyField(file, a); f != "" {
			return errReadOnly("assistant", name+"."+f)
		}
		a.Role = "" // the file's
	} else if a.Role == "" {
		return errInvalid(errors.New("role is required"))
	}
	d := c.dyn.clone()
	d.Assistants[name] = a
	// The brain is told only of its own agent (setBrainAgent): a change of
	// its spec restarts it, and the other assistants are not its business yet.
	return c.commit(d, change{persist: func() error { return c.store.putCatalog(kindAssistant, name, a) }, event: event})
}

// deleteAssistant removes a workspace assistant, or the workspace's edits
// of one of the file's.
func (c *core) deleteAssistant(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if _, ok := c.dyn.Assistants[name]; !ok {
		if _, inBase := fileAssistants(c.base)[name]; inBase {
			return errReadOnly(kindAssistant, name)
		}
		return errNotFound(kindAssistant, name)
	}
	d := c.dyn.clone()
	delete(d.Assistants, name)
	return c.commit(d, change{persist: func() error { return c.store.deleteCatalog(kindAssistant, name) }})
}

// setBrainAgent picks the agent the brain runs, unless the configuration
// file names it. The brain restarts on it once its turn is over.
func (c *core) setBrainAgent(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	b := c.conf().brain()
	if b == nil {
		return &catalogError{http.StatusConflict, "catalog.nobrain", name, errors.New("no brain in the configuration file")}
	}
	if file, ok := fileAssistants(c.base)[b.Name]; ok && file.Agent != "" {
		return errReadOnly("brain.agent", file.Agent)
	}
	a, ok := c.conf().Agents[name]
	if !ok {
		return errNotFound(kindAgent, name)
	}
	if !harnessOf(a.Runtime).canBeBrain() {
		return &catalogError{http.StatusUnprocessableEntity, "catalog.notbrain", name,
			fmt.Errorf("agent %s runs %s, the brain needs a runtime that takes MCP servers and a system prompt", name, a.Runtime)}
	}
	ws := c.dyn.Assistants[b.Name]
	ws.Agent = name
	if _, inBase := fileAssistants(c.base)[b.Name]; !inBase {
		ws.Role = b.Role
	}
	return c.putAssistantLocked(b.Name, ws, fmt.Sprintf("Catalogue : %s réfléchit maintenant avec l'agent « %s » (%s).", b.displayName(), name, a.Runtime))
}

// assistantSource tells where an assistant comes from: config when the
// file defines it (the workspace may still set what the file leaves out).
func (c *core) assistantSource(name string) string {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if _, ok := fileAssistants(c.base)[name]; ok {
		return sourceConfig
	}
	if _, ok := c.dyn.Assistants[name]; ok {
		return sourceWorkspace
	}
	return ""
}

// brainAgentSource is config when the file names the brain's agent.
func (c *core) brainAgentSource(name string) string {
	if c.fileSets(name)["agent"] {
		return sourceConfig
	}
	return sourceWorkspace
}

// fileSets tells which fields of an assistant the file sets: they are
// read-only in the workspace.
func (c *core) fileSets(name string) map[string]bool {
	file, ok := fileAssistants(c.base)[name]
	if !ok {
		return map[string]bool{}
	}
	return map[string]bool{"agent": file.Agent != "", "tools": len(file.Tools) > 0, "node": file.Node != "", "display_name": file.DisplayName != "",
		"instructions": file.Instructions != "" || file.InstructionsFile != ""}
}

// Status.

const assistantSchema = `CREATE TABLE IF NOT EXISTS assistant_status (
  name TEXT PRIMARY KEY, status TEXT NOT NULL, updated INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, assistantSchema) }

// AssistantStatus is what the controller keeps of an assistant's task.
type AssistantStatus struct {
	Task      string `json:"task,omitempty"`  // its current task
	Agent     string `json:"agent,omitempty"` // the agent its task runs
	LastTurn  int64  `json:"last_turn,omitempty"`
	LastError string `json:"last_error,omitempty"`

	// The controller's own: restarts, sessions, what the task was started on.
	Failures       int    `json:"failures,omitempty"`
	NextStart      int64  `json:"next_start,omitempty"`
	Session        string `json:"session,omitempty"`
	SessionRuntime string `json:"session_runtime,omitempty"` // a session is its harness's own
	Resume         string `json:"resume,omitempty"`          // the session it was started to resume
	SpecHash       string `json:"spec_hash,omitempty"`
	ToolsHash      string `json:"tools_hash,omitempty"`
	SeenSeq        int    `json:"seen_seq,omitempty"` // its events relayed

	// The conversation's: its start answer kept quiet, the profile it has,
	// the context pack owed to a session that was not resumed.
	Quiet   bool   `json:"quiet,omitempty"`
	Profile string `json:"profile,omitempty"`
	PackDue bool   `json:"pack_due,omitempty"`
}

// assistantStatuses is the controller's record, in memory and in the store.
type assistantStatuses struct {
	mu sync.Mutex
	m  map[string]*AssistantStatus
}

func (c *core) assistantStatus(name string) AssistantStatus {
	c.asst.mu.Lock()
	defer c.asst.mu.Unlock()
	if st := c.asst.m[name]; st != nil {
		return *st
	}
	return AssistantStatus{}
}

// updateAssistant changes an assistant's status and stores it. Each change
// reads the status as it is: two goroutines never undo each other's.
func (c *core) updateAssistant(name string, f func(*AssistantStatus)) {
	c.asst.mu.Lock()
	defer c.asst.mu.Unlock()
	st := c.asst.m[name]
	if st == nil {
		st = &AssistantStatus{}
		c.asst.m[name] = st
	}
	f(st)
	raw, _ := json.Marshal(st)
	c.store.db.Exec(`INSERT INTO assistant_status (name, status, updated) VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET status = excluded.status, updated = excluded.updated`, name, string(raw), time.Now().Unix())
}

func (c *core) dropAssistantStatus(name string) {
	c.asst.mu.Lock()
	defer c.asst.mu.Unlock()
	delete(c.asst.m, name)
	c.store.db.Exec(`DELETE FROM assistant_status WHERE name = ?`, name)
}

func (c *core) assistantStatusNames() []string {
	c.asst.mu.Lock()
	defer c.asst.mu.Unlock()
	return slices.Sorted(maps.Keys(c.asst.m))
}

// assistantOwning is the name of the assistant whose current task id is, or "".
func (c *core) assistantOwning(id string) string {
	if id == "" {
		return ""
	}
	c.asst.mu.Lock()
	defer c.asst.mu.Unlock()
	for n, st := range c.asst.m {
		if st.Task == id {
			return n
		}
	}
	return ""
}

// isAssistantTask tells whether a task is an assistant's: its role's
// approval path answers it, not the brain.
func (c *core) isAssistantTask(id string) bool { return c.assistantOwning(id) != "" }

// brainTaskID is the brain's current task, or "".
func (c *core) brainTaskID() string {
	b := c.conf().brain()
	if b == nil {
		return ""
	}
	return c.assistantStatus(b.Name).Task
}

// assistantOf is the assistant a task is, with its role: the one whose
// status names it or, for a task of a role's kind none names (one being
// built, a leftover), the enabled assistant of that role.
func (c *core) assistantOf(t *TaskRow) (Assistant, assistantRole, bool) {
	m := c.conf()
	if n := c.assistantOwning(t.ID); n != "" {
		if a, ok := m.Assistants[n]; ok {
			return a, assistantRoles[a.Role], true
		}
	}
	if r, ok := assistantRoles[t.Kind]; ok {
		for _, n := range slices.Sorted(maps.Keys(m.Assistants)) {
			if a := m.Assistants[n]; a.Role == t.Kind && !a.Disabled {
				return a, r, true
			}
		}
	}
	return Assistant{}, assistantRole{}, false
}

// isRoleKind tells whether tasks of this kind are assistants'.
func isRoleKind(kind string) bool {
	_, ok := assistantRoles[kind]
	return ok
}

// The kv keys the brain kept before it was an assistant.
var legacyBrainKeys = []string{"brain_task", "brain_failures", "brain_next_start", "brain_session", "brain_session_runtime",
	"brain_resume", "brain_agent", "brain_tools", "brain_seen_seq", "brain_quiet", "brain_profile", "brain_pack_due"}

// loadAssistants reads the statuses, and moves the brain's from the kv
// keys it had before it was an assistant.
func (c *core) loadAssistants() {
	c.asst.m = map[string]*AssistantStatus{}
	if rows, err := c.store.db.Query(`SELECT name, status FROM assistant_status`); err == nil {
		for rows.Next() {
			var name, raw string
			st := &AssistantStatus{}
			if rows.Scan(&name, &raw) == nil && json.Unmarshal([]byte(raw), st) == nil {
				c.asst.m[name] = st
			}
		}
		rows.Close()
	}
	b := c.conf().Assistants[brainAssistant]
	if _, ok := c.asst.m[brainAssistant]; ok || b.Role != assistantRoleBrain {
		return
	}
	kv := map[string]string{}
	for _, k := range legacyBrainKeys {
		if v := c.store.get(k); v != "" {
			kv[k] = v
		}
	}
	if len(kv) == 0 {
		return
	}
	num := func(k string) int64 {
		var n int64
		fmt.Sscan(kv[k], &n)
		return n
	}
	// The same agent as before keeps its task: only another one restarts it.
	spec := kv["brain_agent"]
	if spec != "" && spec == c.legacyAgentHash(b) {
		spec = c.assistantSpecHash(b)
	}
	c.updateAssistant(brainAssistant, func(st *AssistantStatus) {
		*st = AssistantStatus{Task: kv["brain_task"], Failures: int(num("brain_failures")), NextStart: num("brain_next_start"),
			Session: kv["brain_session"], SessionRuntime: kv["brain_session_runtime"], Resume: kv["brain_resume"],
			SpecHash: spec, ToolsHash: kv["brain_tools"], SeenSeq: int(num("brain_seen_seq")),
			Quiet: kv["brain_quiet"] != "", Profile: kv["brain_profile"], PackDue: kv["brain_pack_due"] != ""}
		if t, err := c.store.task(st.Task); err == nil {
			st.Agent = t.Agent
		}
	})
	for _, k := range legacyBrainKeys {
		c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, k)
	}
	log.Printf("assistants: the brain's state moved to the assistant %s (task %s)", brainAssistant, kv["brain_task"])
}

// Conditions, as the API and the workspace show them.

type assistantCondition struct {
	Type   string `json:"type"` // Running, Busy, Ready
	Status bool   `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type assistantView struct {
	Name     string            `json:"name"`
	Source   string            `json:"source"`              // config or workspace
	FileSets []string          `json:"file_sets,omitempty"` // the fields the file sets: read-only
	Spec     Assistant         `json:"spec"`
	Status   assistantStatusUI `json:"status"`
}

type assistantStatusUI struct {
	Conditions []assistantCondition `json:"conditions"`
	Task       string               `json:"task,omitempty"`
	State      string               `json:"state,omitempty"` // its task's
	Agent      string               `json:"agent,omitempty"`
	Session    string               `json:"session,omitempty"`
	LastTurn   *time.Time           `json:"last_turn,omitempty"`
	LastError  string               `json:"last_error,omitempty"`
	Failures   int                  `json:"failures,omitempty"`
}

func (c *core) assistantView(a Assistant) assistantView {
	st := c.assistantStatus(a.Name)
	v := assistantView{Name: a.Name, Source: c.assistantSource(a.Name), Spec: a}
	for f, set := range c.fileSets(a.Name) {
		if set {
			v.FileSets = append(v.FileSets, f)
		}
	}
	sort.Strings(v.FileSets)
	v.Spec.Triggers = a.triggers()
	if v.Spec.MCPServers != nil {
		v.Spec.MCPServers = nil // their tokens' places are the file's business
	}
	u := assistantStatusUI{Task: st.Task, Agent: st.Agent, Session: st.Session, LastError: st.LastError, Failures: st.Failures}
	if st.LastTurn > 0 {
		t := time.Unix(st.LastTurn, 0)
		u.LastTurn = &t
	}
	var t *TaskRow
	if st.Task != "" {
		t, _ = c.store.task(st.Task)
	}
	running, busy, ready := false, false, false
	reason := "no task"
	switch {
	case a.Disabled:
		reason = "disabled"
	case a.Agent == "":
		reason = "no agent can run it"
	}
	if t != nil {
		u.State = t.State
		running = !finished(t.State) && t.State != statePending
		busy = t.State == stateRunning || t.State == stateWaitingPermission
		ready = t.State == stateIdle && st.Failures == 0
		reason = t.State
		if t.State == statePending && t.PendingReason != "" {
			reason = t.PendingReason
		}
	}
	u.Conditions = []assistantCondition{{"Running", running, reason}, {"Busy", busy, ""}, {"Ready", ready, ""}}
	v.Status = u
	return v
}

func (c *core) assistantViews() []assistantView {
	m := c.conf()
	out := []assistantView{}
	for _, n := range slices.Sorted(maps.Keys(m.Assistants)) {
		out = append(out, c.assistantView(m.Assistants[n]))
	}
	return out
}

// The API (bearer token).
//
//	GET    /v1/assistants          every assistant, with its source and status
//	GET    /v1/assistants/{name}
//	PUT    /v1/assistants/{name}   an assistant's spec; for one of the file's, what the file leaves out
//	DELETE /v1/assistants/{name}   a workspace assistant, or the workspace's edits of one of the file's

func (c *core) assistantRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/assistants", bearer(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, c.assistantViews())
	})))
	mux.Handle("GET /v1/assistants/{name}", bearer(token, http.HandlerFunc(c.handleGetAssistant)))
	mux.Handle("PUT /v1/assistants/{name}", bearer(token, http.HandlerFunc(c.handlePutAssistant)))
	mux.Handle("DELETE /v1/assistants/{name}", bearer(token, http.HandlerFunc(c.handleDeleteAssistant)))
}

func (c *core) handleGetAssistant(w http.ResponseWriter, r *http.Request) {
	a, ok := c.conf().Assistants[r.PathValue("name")]
	if !ok {
		http.Error(w, "no such assistant", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, c.assistantView(a))
}

func (c *core) handlePutAssistant(w http.ResponseWriter, r *http.Request) {
	var a Assistant
	if !decodeBody(w, r, &a) {
		return
	}
	name := r.PathValue("name")
	if err := c.putAssistant(name, a); err != nil {
		catalogFail(w, err)
		return
	}
	c.ping() // the controller takes it at once
	writeJSON(w, http.StatusOK, c.assistantView(c.conf().Assistants[name]))
}

func (c *core) handleDeleteAssistant(w http.ResponseWriter, r *http.Request) {
	if err := c.deleteAssistant(r.PathValue("name")); err != nil {
		catalogFail(w, err)
		return
	}
	c.ping()
	w.WriteHeader(http.StatusNoContent)
}
