package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// A brain-like task: eriphe-core's own server, a catalog server lent
// through the node, a deprecated server whose token is in the identity.
func mcpTask() *Task {
	return &Task{ID: "t1", State: stateRunning, secret: "sec", perms: map[string]*permission{},
		Spec: TaskSpec{Prompt: "go", Servers: []string{"vault"},
			Identity: map[string]string{"TOK": "tok-1"},
			MCPServers: map[string]any{
				"eriphe-core": map[string]any{"type": "http", "url": "http://core:8080/mcp",
					"headers": map[string]any{"Authorization": "Bearer core-token"}},
				"old": map[string]any{"type": "http", "url": "http://old/mcp",
					"headers": map[string]any{"Authorization": "Bearer ${TOK}", "X-Other": "${UNSET}"}},
			},
			AllowedTools:       []string{"mcp__eriphe-core", "mcp__vault__read_note", "Read"},
			AppendSystemPrompt: "You are Eriphe."}}
}

// Every runtime gets the same servers as Claude Code, but its permission
// tool, with the same addresses and credentials.
func TestMCPServersFor(t *testing.T) {
	task := mcpTask()
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	servers, err := mcpServersFor(n.callbackFor(task), task)
	if err != nil {
		t.Fatal(err)
	}
	want := []mcpServer{
		{Name: "eriphe-core", URL: "http://core:8080/mcp", Headers: map[string]string{"Authorization": "Bearer core-token"}},
		{Name: "old", URL: "http://old/mcp", Headers: map[string]string{"Authorization": "Bearer tok-1", "X-Other": "${UNSET}"}},
		{Name: "vault", URL: "http://eriphe-node:8790/mcp/vault", Headers: map[string]string{"Authorization": "Bearer sec"}},
	}
	if !reflect.DeepEqual(servers, want) {
		t.Errorf("servers = %+v", servers)
	}

	args, _ := claudeCode{}.command(n, task)
	var cfg struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	json.Unmarshal([]byte(args[slices.Index(args, "--mcp-config")+1]), &cfg)
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
	}
	claude := slices.Sorted(func(yield func(string) bool) {
		for k := range cfg.MCPServers {
			if k != "eriphe_node" && !yield(k) {
				return
			}
		}
	})
	if !slices.Equal(names, claude) {
		t.Errorf("servers %v, Claude Code's %v", names, claude)
	}

	if _, err := mcpServersFor("", &Task{Spec: TaskSpec{MCPServers: map[string]any{"x": map[string]any{"type": "sse", "url": "u"}}}}); err == nil {
		t.Error("an SSE server was taken")
	}
}

func TestAllowedMCP(t *testing.T) {
	servers, tools := allowedMCP([]string{"mcp__a", "mcp__b__x", "mcp__b__y", "mcp__c__*", "Bash", "mcp__"})
	if !reflect.DeepEqual(servers, map[string]bool{"a": true, "c": true}) || !reflect.DeepEqual(tools, map[string][]string{"b": {"x", "y"}}) {
		t.Errorf("servers %v, tools %v", servers, tools)
	}
}

// session/new carries the servers as ACP's McpServerHttp; OpenCode's MCP
// tool, asked for without its input, is named and filled as the policy
// reads it; the message is streamed as a draft.
func TestACPMCP(t *testing.T) {
	task := mcpTask()
	task.Spec.Stream = true
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	opencodeRuntime.command(n, task)
	drafts := make(chan string, 10)
	script := func(f *fakeAgent, m rpcMessage) {
		f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "Wri"}})
		f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "ting"}})
		// The client has read both chunks once it answers a request.
		f.ask("fs/read_text_file", map[string]any{"sessionId": "s1", "path": "/x"})
		drafts <- task.draftNow()
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "vault_write_note", "kind": "other",
			"status": "pending", "rawInput": map[string]any{}})
		// As OpenCode does: the request first, the input in the update just after.
		asked := make(chan rpcMessage, 1)
		go func() {
			asked <- f.ask("session/request_permission", map[string]any{"sessionId": "s1",
				"toolCall": map[string]any{"toolCallId": "c1", "title": "vault_write_note", "kind": "other", "status": "pending", "rawInput": map[string]any{}},
				"options":  []map[string]any{{"optionId": "once", "kind": "allow_once"}, {"optionId": "reject", "kind": "reject_once"}}})
		}()
		time.Sleep(50 * time.Millisecond)
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "title": "vault_write_note", "kind": "other",
			"status": "in_progress", "rawInput": map[string]any{"path": "a.md"}})
		<-asked
		f.write(map[string]any{"id": m.ID, "result": map[string]any{"stopReason": "end_turn"}})
	}
	s, f, done := startFake(t, task, "go", false, func(f *fakeAgent) { f.mcpHTTP, f.script = true, script })
	p := answer(t, task, permAnswer{Allow: true})
	if p.ToolName != "mcp__vault__write_note" || p.Input["path"] != "a.md" {
		t.Errorf("permission = %s %v", p.ToolName, p.Input)
	}
	waitTurns(t, task, 1)
	if d := <-drafts; d != "Writing" {
		t.Errorf("draft = %q", d)
	}
	if d := task.draftNow(); d != "" {
		t.Errorf("draft after the message = %q", d)
	}
	if tools := eventsOf(task, "tool_use"); len(tools) != 1 || tools[0].Data["name"] != "mcp__vault__write_note" {
		t.Errorf("tool_use = %v", tools)
	}
	f.mu.Lock()
	var params struct {
		MCPServers []map[string]any `json:"mcpServers"`
	}
	json.Unmarshal(f.requests[1].Params, &params)
	f.mu.Unlock()
	raw, _ := json.Marshal(params.MCPServers)
	want := `[{"headers":[{"name":"Authorization","value":"Bearer core-token"}],"name":"eriphe-core","type":"http","url":"http://core:8080/mcp"},` +
		`{"headers":[{"name":"Authorization","value":"Bearer tok-1"},{"name":"X-Other","value":"${UNSET}"}],"name":"old","type":"http","url":"http://old/mcp"},` +
		`{"headers":[{"name":"Authorization","value":"Bearer sec"}],"name":"vault","type":"http","url":"http://eriphe-node:8790/mcp/vault"}]`
	if string(raw) != want {
		t.Errorf("mcpServers = %s", raw)
	}
	s.stop()
	<-done
}

// An agent that takes no HTTP server cannot run a task that has some.
func TestACPMCPNoHTTP(t *testing.T) {
	task := mcpTask()
	opencodeRuntime.command(newNode(options{callbackURL: "http://n"}), task)
	_, _, done := startFake(t, task, "go", false)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "over HTTP") {
		t.Errorf("run = %v", err)
	}
}

// Without servers, session/new still carries an empty list, as before.
func TestACPNoServers(t *testing.T) {
	task := newTestTask()
	s, f, done := startFake(t, task, "go", false)
	answer(t, task, permAnswer{Allow: true})
	waitTurns(t, task, 1)
	f.mu.Lock()
	if !strings.Contains(string(f.requests[1].Params), `"mcpServers":[]`) {
		t.Errorf("session/new = %s", f.requests[1].Params)
	}
	f.mu.Unlock()
	s.stop()
	<-done
}

func TestOpenCodeMCPConfig(t *testing.T) {
	task := mcpTask()
	task.Spec.NoAgentMemory = true
	n := newNode(options{callbackURL: "http://n"})
	args, env := opencodeRuntime.command(n, task)
	if args[0] != "sh" || !strings.Contains(args[2], "exec opencode acp") {
		t.Errorf("args = %v", args)
	}
	var cfg struct {
		Permission   map[string]string `json:"permission"`
		Instructions []string          `json:"instructions"`
	}
	raw := env["OPENCODE_CONFIG_CONTENT"]
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"eriphe-core_*": "allow", "old_*": "ask", "vault_*": "ask", "vault_read_note": "allow", "bash": "ask"} {
		if cfg.Permission[k] != v {
			t.Errorf("permission %s = %q", k, cfg.Permission[k])
		}
	}
	// The last matching rule wins: a server's tools come after its "_*".
	if i, j := strings.Index(raw, `"vault_*"`), strings.Index(raw, `"vault_read_note"`); i < 0 || j < i {
		t.Errorf("order: %s", raw)
	}
	if !slices.Equal(cfg.Instructions, []string{agentInstructions}) {
		t.Errorf("the brain's instructions = %v", cfg.Instructions)
	}
	if env[instructionsEnv] != "You are Eriphe." || env["OPENCODE_DISABLE_CLAUDE_CODE"] != "1" {
		t.Errorf("env = %v", env)
	}

	// A worker reads the repository's files, and the prompt if it has one.
	_, env = opencodeRuntime.command(n, &Task{Spec: TaskSpec{Repo: "o/r", AppendSystemPrompt: "Be brief."}})
	json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &cfg)
	if !slices.Equal(cfg.Instructions, []string{"/work/repo/AGENTS.md", "/work/repo/CLAUDE.md", agentInstructions}) ||
		env["OPENCODE_DISABLE_CLAUDE_CODE"] != "" {
		t.Errorf("a worker's instructions = %v, env %v", cfg.Instructions, env)
	}
	_, env = opencodeRuntime.command(n, &Task{Spec: TaskSpec{}})
	if _, ok := env[instructionsEnv]; ok {
		t.Error("an empty prompt is written")
	}
}

// thread/start carries the servers in its config, the prompt as developer
// instructions; an MCP approval comes as an elicitation; the message is
// streamed as a draft.
func TestCodexMCP(t *testing.T) {
	task := mcpTask()
	task.Spec.Stream, task.Spec.NoAgentMemory = true, true
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	args, _ := codexRuntime{}.command(n, task)
	if !slices.Contains(args, "features.tool_call_mcp_elicitation=true") {
		t.Errorf("args = %v", args)
	}
	drafts := make(chan string, 1)
	actions := make(chan string, 3)
	script := func(f *fakeCodex, p map[string]any) {
		f.note("item/agentMessage/delta", map[string]any{"itemId": "m1", "delta": "Wri"})
		f.note("item/agentMessage/delta", map[string]any{"itemId": "m1", "delta": "ting"})
		f.ask("item/tool/requestUserInput", map[string]any{"threadId": "thr_1"}) // both deltas are read
		drafts <- task.draftNow()
		f.note("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m1", "text": "Writing"}})
		elicit := func(server string, args map[string]any, kind string) string {
			r := f.ask("mcpServer/elicitation/request", map[string]any{"threadId": "thr_1", "turnId": "turn_1", "serverName": server,
				"mode": "form", "message": `Allow the ` + server + ` MCP server to run tool "write_note"?`,
				"_meta": map[string]any{"codex_approval_kind": kind, "tool_params": args}, "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}})
			var out struct {
				Action string `json:"action"`
			}
			json.Unmarshal(r.Result, &out)
			return out.Action
		}
		f.note("item/started", map[string]any{"item": map[string]any{"type": "mcpToolCall", "id": "x1", "server": "vault", "tool": "list", "arguments": map[string]any{}}})
		f.note("item/started", map[string]any{"item": map[string]any{"type": "mcpToolCall", "id": "x2", "server": "vault", "tool": "write_note",
			"status": "inProgress", "arguments": map[string]any{"path": "a.md"}}})
		actions <- elicit("vault", map[string]any{"path": "a.md"}, "mcp_tool_call")
		f.note("item/completed", map[string]any{"item": map[string]any{"type": "mcpToolCall", "id": "x2", "server": "vault", "tool": "write_note"}})
		// No call seen: the message names the tool.
		actions <- elicit("old", map[string]any{"q": 1}, "mcp_tool_call")
		// A server's own question: no one to ask.
		actions <- elicit("vault", nil, "")
		f.note("turn/completed", map[string]any{"turn": map[string]any{"id": "turn_1", "status": "completed"}})
	}
	s, f, done := startCodex(t, task, "go", false, func(f *fakeCodex) { f.script = script })
	p := answerNext(t, task, permAnswer{Allow: true})
	if p.ToolName != "mcp__vault__write_note" || p.Input["path"] != "a.md" {
		t.Errorf("permission = %s %v", p.ToolName, p.Input)
	}
	if a := <-actions; a != "accept" {
		t.Errorf("action = %q", a)
	}
	p = answerNext(t, task, permAnswer{Allow: false, Message: "no"})
	if p.ToolName != "mcp__old__write_note" || p.Input["q"] != float64(1) {
		t.Errorf("permission = %s %v", p.ToolName, p.Input)
	}
	if a := <-actions; a != "decline" {
		t.Errorf("action = %q", a)
	}
	if a := <-actions; a != "decline" {
		t.Errorf("a server's question: %q", a)
	}
	waitTurns(t, task, 1)
	if d := <-drafts; d != "Writing" {
		t.Errorf("draft = %q", d)
	}
	if d := task.draftNow(); d != "" {
		t.Errorf("draft after the message = %q", d)
	}

	f.mu.Lock()
	var start map[string]any
	for _, r := range f.requests {
		if r.Method == "thread/start" {
			json.Unmarshal(r.Params, &start)
		}
	}
	f.mu.Unlock()
	if start["developerInstructions"] != "You are Eriphe." || start["approvalPolicy"] != "untrusted" || start["sandbox"] != "danger-full-access" {
		t.Errorf("thread/start = %v", start)
	}
	raw, _ := json.Marshal(start["config"])
	want := `{"mcp_servers":{` +
		`"eriphe-core":{"default_tools_approval_mode":"approve","http_headers":{"Authorization":"Bearer core-token"},"tool_timeout_sec":3600,"url":"http://core:8080/mcp"},` +
		`"old":{"default_tools_approval_mode":"prompt","http_headers":{"Authorization":"Bearer tok-1","X-Other":"${UNSET}"},"tool_timeout_sec":3600,"url":"http://old/mcp"},` +
		`"vault":{"default_tools_approval_mode":"prompt","http_headers":{"Authorization":"Bearer sec"},"tool_timeout_sec":3600,"tools":{"read_note":{"approval_mode":"approve"}},"url":"http://eriphe-node:8790/mcp/vault"}},` +
		`"project_doc_max_bytes":0,"web_search":"cached"}`
	if string(raw) != want {
		t.Errorf("config = %s", raw)
	}
	s.stop()
	<-done
}

// A resumed thread takes today's prompt and servers too; a worker without
// either gets only its web search held to the cached index.
func TestCodexResumeParams(t *testing.T) {
	task := mcpTask()
	task.SessionID = "thr_9"
	codexRuntime{}.command(newNode(options{callbackURL: "http://n"}), task)
	s, f, done := startCodex(t, task, "go", true)
	for range 3 {
		answerNext(t, task, permAnswer{Allow: true})
	}
	waitTurns(t, task, 1)
	f.mu.Lock()
	var resume map[string]any
	json.Unmarshal(f.requests[2].Params, &resume)
	f.mu.Unlock()
	if f.requests[2].Method != "thread/resume" || resume["developerInstructions"] != "You are Eriphe." || resume["config"] == nil {
		t.Errorf("%s %v", f.requests[2].Method, resume)
	}
	s.stop()
	<-done

	worker := &codexSession{t: &Task{Spec: TaskSpec{}}, cwd: "/work"}
	if p := worker.threadParams(); !reflect.DeepEqual(p["config"], map[string]any{"web_search": "cached"}) || p["developerInstructions"] != nil {
		t.Errorf("a worker's thread = %v", p)
	}
}

// A catalog server marked always_load is loaded upfront by Claude Code,
// the others stay behind its tool search.
func TestClaudeAlwaysLoad(t *testing.T) {
	task := mcpTask()
	task.Spec.Servers = []string{"vault", "house"}
	task.Spec.AlwaysLoad = []string{"house"}
	args, _ := claudeCode{}.command(newNode(options{callbackURL: "http://eriphe-node:8790"}), task)
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	json.Unmarshal([]byte(args[slices.Index(args, "--mcp-config")+1]), &cfg)
	if cfg.MCPServers["house"]["alwaysLoad"] != true || cfg.MCPServers["vault"]["alwaysLoad"] != nil {
		t.Fatalf("servers = %v", cfg.MCPServers)
	}
}
