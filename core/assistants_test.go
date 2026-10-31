package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A role for the tests: what a sentinel will be, with no trigger yet.
const roleTest = "sentinel-test"

func init() {
	assistantRoles[roleTest] = assistantRole{Prompt: "Tu veilles pour {{user}}.", StartPrompt: "Prêt ?"}
}

// tasksOfKind are the tasks of one kind, newest first.
func tasksOfKind(t *testing.T, c *core, kind string) []*TaskRow {
	t.Helper()
	rows, err := c.store.db.Query(`SELECT id FROM tasks WHERE kind = ? ORDER BY created DESC`, kind)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []*TaskRow
	for _, id := range ids {
		if x, err := c.store.task(id); err == nil {
			out = append(out, x)
		}
	}
	return out
}

// noWait lets the controller start an assistant again at once.
func noWait(c *core, name string) {
	c.updateAssistant(name, func(st *AssistantStatus) { st.NextStart = 0 })
}

func brainCore(t *testing.T, b *BrainConfig) *catalogCore {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	c := newDynamicCore(t, func(cfg *Config) {
		cfg.Brain = b
		cfg.Providers["anthropic"] = Provider{Kind: "claude-subscription", Secret: secret}
	})
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	return c
}

// The brain is the built-in assistant eriphe: the controller starts its
// task as the brain's always was (its kind, profile, tools, first turn).
func TestBrainIsAnAssistant(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	b := c.conf().brain()
	if b == nil || b.Name != brainAssistant || b.Role != assistantRoleBrain || b.Agent != "sonnet" || !b.has(triggerConversation) {
		t.Fatalf("brain = %+v", b)
	}
	c.tendAssistants(context.Background(), "tok")
	st := brainStatus(c.core)
	task, err := c.store.task(st.Task)
	if err != nil || task.Kind != "brain" || task.Agent != "sonnet" || st.Agent != "sonnet" || !st.Quiet || st.SpecHash == "" || st.ToolsHash == "" {
		t.Fatalf("task = %+v, status = %+v, %v", task, st, err)
	}
	if !strings.HasPrefix(task.Prompt, "Tu démarres dans une session neuve") {
		t.Fatalf("first turn = %q", task.Prompt)
	}
	spec, err := c.store.taskSpec(task.ID)
	if err != nil || spec.Profile != "brain" || !spec.NoAgentMemory || strings.Join(spec.AllowedTools, ",") != "mcp__eriphe-core,WebSearch" ||
		!strings.Contains(spec.AppendSystemPrompt, "Eriphe") {
		t.Fatalf("spec = %+v, %v", spec, err)
	}
	// Its node gets what the brain's card says.
	if err := c.fetchAssistant(task, &spec); err != nil || !spec.SystemNetwork || !spec.Stream ||
		strings.Join(spec.Servers, ",") != selfServer || spec.Identity["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" {
		t.Fatalf("fetched = %+v, %v", spec, err)
	}
	// One task: a second pass starts none.
	c.tendAssistants(context.Background(), "tok")
	if n := len(tasksOfKind(t, c.core, "brain")); n != 1 {
		t.Fatalf("%d brain tasks", n)
	}
}

// A change of its instructions restarts the idle brain, as another agent does.
func TestAssistantSpecHashRestart(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	ctx := context.Background()
	c.tendAssistants(ctx, "tok")
	id := brainStatus(c.core).Task
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	if c.tendAssistants(ctx, "tok"); c.store.get("brain_stopped:"+id) != "" {
		t.Fatal("restarted with nothing changed")
	}
	if err := c.putAssistant(brainAssistant, Assistant{Instructions: "Sois bref, {{user}}."}); err != nil {
		t.Fatal(err)
	}
	if c.tendAssistants(ctx, "tok"); c.store.get("brain_stopped:"+id) == "" {
		t.Fatal("not restarted on new instructions")
	}
	c.store.updateTask(id, map[string]any{"state": stateStopped})
	noWait(c.core, brainAssistant)
	c.tendAssistants(ctx, "tok")
	next := brainStatus(c.core).Task
	spec, _ := c.store.taskSpec(next)
	if next == id || !strings.HasPrefix(spec.AppendSystemPrompt, "Sois bref, .") {
		t.Fatalf("restarted as %s with %q", next, truncate(spec.AppendSystemPrompt, 80))
	}
	// Its tools too, once the catalog's servers are ready.
	waitStatus(t, c.pool.get("docs"), statusReady)
	c.store.updateTask(next, map[string]any{"state": stateIdle})
	c.tendAssistants(ctx, "tok")
	if c.store.get("brain_stopped:"+next) != "" {
		t.Fatal("restarted before its tools changed")
	}
	if err := c.editAssistant(brainAssistant, assistantEdit{Tools: []string{"docs"}, SetTools: true}); err != nil {
		t.Fatal(err)
	}
	if c.tendAssistants(ctx, "tok"); c.store.get("brain_stopped:"+next) == "" {
		t.Fatal("not restarted on new tools")
	}
	if g := c.brainGrant(); strings.Join(g, ",") != "docs" {
		t.Fatalf("grant = %v", g)
	}
}

// An existing install moves on its own: the brain's kv keys become its
// status, the setting brain_agent its workspace agent, and the same agent
// keeps its task.
func TestAssistantMigration(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.putCatalog(kindAgent, "free", Agent{Runtime: "claude-code", Provider: "anthropic", Use: "free"})
	store.putCatalog(kindSetting, settingBrainAgent, "free")
	cfg := &Config{
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: "/secrets/claude"}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Use: "default"}},
		DefaultAgent: "sonnet", Brain: &BrainConfig{},
	}
	// What the brain kept before: its agent's fingerprint, as it was then.
	legacy := newCore(cfg, store)
	b := Assistant{Name: brainAssistant, Role: assistantRoleBrain, Agent: "free"}
	store.insertTask(&TaskRow{ID: "b-old", Node: "pc", Kind: "brain", Agent: "free", Runtime: "claude-code", State: stateIdle})
	for k, v := range map[string]string{"brain_task": "b-old", "brain_failures": "1", "brain_session": "s-9", "brain_session_runtime": "claude-code",
		"brain_agent": legacy.legacyAgentHash(b), "brain_tools": "t-hash", "brain_seen_seq": "42", "brain_quiet": "", "brain_profile": "p-hash",
		"brain_pack_due": "1", "brain_next_start": "1700000000"} {
		store.set(k, v)
	}
	legacy.dropAssistantStatus(brainAssistant) // as if it had never run

	c := newCore(cfg, store)
	if a := c.conf().brain(); a == nil || a.Agent != "free" {
		t.Fatalf("brain = %+v", a)
	}
	st := brainStatus(c)
	if st.Task != "b-old" || st.Failures != 1 || st.Session != "s-9" || st.SessionRuntime != "claude-code" || st.ToolsHash != "t-hash" ||
		st.SeenSeq != 42 || st.Quiet || st.Profile != "p-hash" || !st.PackDue || st.NextStart != 1700000000 || st.Agent != "free" ||
		st.SpecHash != c.assistantSpecHash(*c.conf().brain()) {
		t.Fatalf("status = %+v", st)
	}
	for _, k := range legacyBrainKeys {
		if store.get(k) != "" {
			t.Errorf("kv %s left", k)
		}
	}
	rows, _ := store.catalogRows()
	var kinds []string
	for _, r := range rows {
		kinds = append(kinds, r.Kind+"/"+r.Name)
	}
	if got := strings.Join(kinds, ","); got != "agent/free,assistant/eriphe" {
		t.Fatalf("catalog = %s", got)
	}
	// Started again, it keeps all that: nothing moves twice.
	again := newCore(cfg, store)
	if st := brainStatus(again); st.Task != "b-old" || again.conf().brain().Agent != "free" {
		t.Fatalf("after a restart: %+v", st)
	}
	// Another agent's fingerprint restarts the brain on its next idle turn.
	store2, _ := openStore(filepath.Join(t.TempDir(), "core.db"))
	store2.insertTask(&TaskRow{ID: "b-old", Node: "pc", Kind: "brain", Agent: "sonnet", Runtime: "claude-code", State: stateIdle})
	store2.set("brain_task", "b-old")
	store2.set("brain_agent", "another")
	if st := brainStatus(newCore(cfg, store2)); st.SpecHash != "another" {
		t.Fatalf("a changed agent adopted: %+v", st)
	}
}

// What the file sets is read-only: the workspace sets only what it leaves out.
func TestAssistantFileReadOnly(t *testing.T) {
	prompt := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(prompt, []byte("x"), 0o600)
	c := brainCore(t, &BrainConfig{Agent: "sonnet", PromptFile: prompt, Tools: []string{"docs"}})
	wantKey(t, c.putAssistant(brainAssistant, Assistant{Agent: "other"}), "catalog.readonly")
	wantKey(t, c.putAssistant(brainAssistant, Assistant{Tools: []string{"mail"}}), "catalog.readonly")
	wantKey(t, c.putAssistant(brainAssistant, Assistant{Instructions: "y"}), "catalog.readonly")
	wantKey(t, c.putAssistant(brainAssistant, Assistant{Role: roleTest}), "catalog.readonly")
	wantKey(t, c.setBrainAgent("sonnet"), "catalog.readonly")
	wantKey(t, c.editAssistant(brainAssistant, assistantEdit{Tools: []string{"mail"}, SetTools: true}), "catalog.readonly")
	wantKey(t, c.deleteAssistant(brainAssistant), "catalog.readonly")
	// Files and tokens are the file's.
	for _, a := range []Assistant{{Role: roleTest, InstructionsFile: "/etc/passwd"}, {Role: roleTest, SelfURL: "http://x/mcp"},
		{Role: roleTest, MCPServers: map[string]MCPServer{"x": {URL: "http://x"}}}} {
		if err := c.putAssistant("other", a); err == nil {
			t.Errorf("%+v accepted", a)
		}
	}
	// The same values, and what the file leaves out, are fine.
	if err := c.putAssistant(brainAssistant, Assistant{Agent: "sonnet", Node: "nas"}); err != nil {
		t.Fatal(err)
	}
	if b := c.conf().brain(); b.Node != "nas" || b.InstructionsFile != prompt || strings.Join(b.Tools, ",") != "docs" {
		t.Fatalf("brain = %+v", b)
	}
	if v := c.assistantView(*c.conf().brain()); v.Source != sourceConfig || strings.Join(v.FileSets, ",") != "agent,instructions,tools" {
		t.Fatalf("view = %+v", v)
	}
	// The file's assistants section, like its brain.
	cfg := &Config{Brain: &BrainConfig{}, Assistants: map[string]Assistant{brainAssistant: {Role: assistantRoleBrain}}}
	if err := cfg.checkFileAssistants(); err == nil {
		t.Fatal("the brain twice in the file")
	}
}

// A second assistant, disabled, is never started; enabled, it gets its
// own task, of its role's kind, beside the brain's.
func TestSecondAssistant(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	ctx := context.Background()
	if err := c.putAssistant("watch", Assistant{Role: roleTest, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	c.tendAssistants(ctx, "tok")
	if n := len(tasksOfKind(t, c.core, roleTest)); n != 0 || c.assistantStatus("watch").Task != "" {
		t.Fatalf("a disabled assistant started: %d", n)
	}
	if err := c.putAssistant("watch", Assistant{Role: roleTest}); err != nil {
		t.Fatal(err)
	}
	c.tendAssistants(ctx, "tok")
	tasks := tasksOfKind(t, c.core, roleTest)
	st := c.assistantStatus("watch")
	if len(tasks) != 1 || st.Task != tasks[0].ID || st.Task == brainStatus(c.core).Task || tasks[0].Prompt != "Prêt ?" {
		t.Fatalf("tasks = %+v, status = %+v", tasks, st)
	}
	spec, _ := c.store.taskSpec(st.Task)
	if spec.Profile != "assistant-watch" || spec.AppendSystemPrompt != "Tu veilles pour ." || len(spec.AllowedTools) != 0 {
		t.Fatalf("spec = %+v", spec)
	}
	// Its role has no self tools, no free reads, no network of its own.
	if err := c.fetchAssistant(tasks[0], &spec); err != nil || spec.SystemNetwork || spec.Stream || len(spec.Servers) != 0 {
		t.Fatalf("fetched = %+v, %v", spec, err)
	}
	if c.brainTaskID() == st.Task || !c.isAssistantTask(st.Task) || c.readsFreely(st.Task) {
		t.Fatal("its task taken for the brain's")
	}
	// It hears nothing of the conversation.
	c.store.updateTask(st.Task, map[string]any{"state": stateIdle})
	c.store.addMessage(roleUser, "Bonjour", false)
	c.tendAssistant(ctx, c.conf().Assistants["watch"], "tok")
	if items, _ := c.store.inbox(st.Task); len(items) != 0 {
		t.Fatalf("inbox = %+v", items)
	}
	// Disabled again, its task stops; removed, its status goes.
	c.putAssistant("watch", Assistant{Role: roleTest, Disabled: true})
	if c.tendAssistants(ctx, "tok"); c.store.get("brain_stopped:"+st.Task) == "" {
		t.Fatal("a disabled assistant kept its task")
	}
	c.store.updateTask(st.Task, map[string]any{"state": stateStopped})
	if err := c.deleteAssistant("watch"); err != nil {
		t.Fatal(err)
	}
	if c.tendAssistants(ctx, "tok"); slicesContain(c.assistantStatusNames(), "watch") {
		t.Fatal("a removed assistant's status kept")
	}
	// Only one assistant has the conversation.
	if err := c.putAssistant("brain2", Assistant{Role: assistantRoleBrain}); err == nil {
		t.Fatal("two brains")
	}
	if err := c.putAssistant("brain2", Assistant{Role: roleTest, Triggers: []string{"schedule"}}); err == nil {
		t.Fatal("a trigger not supported yet accepted")
	}
}

func slicesContain(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// The status follows the task: running, busy, ready, and the last error.
func TestAssistantStatus(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	ctx := context.Background()
	cond := func() string {
		v := c.assistantView(*c.conf().brain())
		var out []string
		for _, x := range v.Status.Conditions {
			if x.Status {
				out = append(out, x.Type)
			}
		}
		return strings.Join(out, ",")
	}
	if got := cond(); got != "" {
		t.Fatalf("before start: %s", got)
	}
	c.tendAssistants(ctx, "tok")
	id := brainStatus(c.core).Task
	if got := cond(); got != "" { // no node yet: pending
		t.Fatalf("pending: %s", got)
	}
	c.store.updateTask(id, map[string]any{"state": stateRunning})
	if got := cond(); got != "Running,Busy" {
		t.Fatalf("running: %s", got)
	}
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	if got := cond(); got != "Running,Ready" {
		t.Fatalf("idle: %s", got)
	}
	// A delivered turn is its last.
	c.store.addMessage(roleUser, "Bonjour", false)
	c.tendAssistants(ctx, "tok")
	if v := c.assistantView(*c.conf().brain()); v.Status.LastTurn == nil || v.Status.Agent != "sonnet" {
		t.Fatalf("status = %+v", v.Status)
	}
	// A start that fails says why.
	c.store.updateTask(id, map[string]any{"state": stateFailed})
	noWait(c.core, brainAssistant)
	editBrain(c.core, func(a *Assistant) { a.InstructionsFile = "/nonexistent/prompt.md" })
	c.tendAssistants(ctx, "tok")
	if st := brainStatus(c.core); !strings.Contains(st.LastError, "prompt") || st.Failures != 1 {
		t.Fatalf("status = %+v", st)
	}
}

// The API lists, reads and edits the assistants.
func TestAssistantsAPI(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	mux := http.NewServeMux()
	c.assistantRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var list []assistantView
	if code := call(t, srv, "GET", "/v1/assistants", "", &list); code != 200 || len(list) != 1 || list[0].Name != brainAssistant ||
		list[0].Spec.Role != assistantRoleBrain || list[0].Source != sourceConfig || len(list[0].Status.Conditions) != 3 {
		t.Fatalf("list: %d %+v", code, list)
	}
	var one assistantView
	if code := call(t, srv, "PUT", "/v1/assistants/watch", `{"role":"`+roleTest+`","agent":"sonnet","tools":["docs"]}`, &one); code != 200 ||
		one.Spec.Agent != "sonnet" || one.Source != sourceWorkspace {
		t.Fatalf("put: %d %+v", code, one)
	}
	if code := call(t, srv, "GET", "/v1/assistants/watch", "", &one); code != 200 || strings.Join(one.Spec.Tools, ",") != "docs" {
		t.Fatalf("get: %d %+v", code, one)
	}
	if code := call(t, srv, "PUT", "/v1/assistants/watch", `{"role":"`+roleTest+`","self_url":"http://x/mcp"}`, nil); code != 400 {
		t.Fatalf("self_url: %d", code)
	}
	if code := call(t, srv, "PUT", "/v1/assistants/eriphe", `{"role":"`+roleTest+`"}`, nil); code != 409 {
		t.Fatalf("role of the file's: %d", code)
	}
	if code := call(t, srv, "DELETE", "/v1/assistants/watch", "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := call(t, srv, "GET", "/v1/assistants/watch", "", nil); code != 404 {
		t.Fatalf("deleted: %d", code)
	}
	// Kept across a restart: a workspace entry like the others.
	if err := c.putAssistant("watch", Assistant{Role: roleTest, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	again := startDynamicCore(t, c.base, c.store)
	if a, ok := again.conf().Assistants["watch"]; !ok || !a.Disabled || len(again.skipped) != 0 {
		t.Fatalf("after a restart: %+v %v", a, again.skipped)
	}
}

// The workspace shows the assistants and edits the brain's tools.
func TestWebAssistants(t *testing.T) {
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{} })
	code, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	if code != 200 || !strings.Contains(body, `id="assistant-eriphe"`) || !strings.Contains(body, "cerveau") ||
		!strings.Contains(body, "les lectures libres du catalogue") {
		t.Fatalf("page: %d", code)
	}
	if _, _, h := tw.post("/settings/assistants/eriphe", url.Values{"agent": {"sonnet"}, "tools": {"docs, docs.search"}}, csrf); h.Get("HX-Redirect") != "/settings/agents?assistant=eriphe" {
		t.Fatalf("save: %v", h)
	}
	if b := c.conf().brain(); strings.Join(b.Tools, ",") != "docs,docs.search" {
		t.Fatalf("brain = %+v", b)
	}
	if _, _, h := tw.post("/settings/assistants/eriphe", url.Values{"tools": {"Bad Server"}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("a bad grant saved")
	}
}

// readOnlyField: a workspace edit may set what the file leaves out, or the
// file's own value, but not change what the file sets. The role counts
// even when the file has none.
func TestReadOnlyField(t *testing.T) {
	file := Assistant{Role: roleTest, DisplayName: "Sam", Agent: "sonnet", Node: "nas", Instructions: "x",
		Tools: []string{"docs"}, Resources: []ResourceRef{{Kind: "repo", Ref: "o/r"}}, Triggers: []string{triggerConversation}, ReportsTo: brainAssistant}
	for _, c := range []struct {
		file, ws Assistant
		want     string
	}{
		{file, Assistant{}, ""},
		{file, file, ""},
		{file, Assistant{Role: "other"}, "role"},
		{Assistant{}, Assistant{Role: roleTest}, "role"},
		{file, Assistant{DisplayName: "Alex"}, "display_name"},
		{Assistant{}, Assistant{DisplayName: "Alex"}, ""},
		{file, Assistant{Agent: "opus"}, "agent"},
		{Assistant{}, Assistant{Agent: "opus"}, ""},
		{file, Assistant{Node: "pc"}, "node"},
		{Assistant{}, Assistant{Node: "pc"}, ""},
		{file, Assistant{Instructions: "y"}, "instructions"},
		{Assistant{InstructionsFile: "/p.md"}, Assistant{Instructions: "y"}, "instructions"},
		{Assistant{}, Assistant{Instructions: "y"}, ""},
		{file, Assistant{Tools: []string{"mail"}}, "tools"},
		{Assistant{}, Assistant{Tools: []string{"mail"}}, ""},
		{file, Assistant{Resources: []ResourceRef{{Kind: "repo", Ref: "o/other"}}}, "resources"},
		{Assistant{}, Assistant{Resources: []ResourceRef{{Kind: "repo", Ref: "o/other"}}}, ""},
		{file, Assistant{Triggers: []string{"schedule"}}, "triggers"},
		{Assistant{}, Assistant{Triggers: []string{"schedule"}}, ""},
		{file, Assistant{ReportsTo: "other"}, "reports_to"},
		{Assistant{}, Assistant{ReportsTo: "other"}, ""},
		// The first field that differs is named.
		{file, Assistant{Agent: "opus", Tools: []string{"mail"}}, "agent"},
	} {
		if got := readOnlyField(c.file, c.ws); got != c.want {
			t.Errorf("readOnlyField(%+v, %+v) = %q, want %q", c.file, c.ws, got, c.want)
		}
	}
}
