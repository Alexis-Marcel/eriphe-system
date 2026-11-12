package main

import (
	"encoding/json"
	"testing"
)

// Vibe's settings, through its VIBE_* variables: everything asked, nothing
// of the account's own, its sessions in the task's profile.
func TestVibeSetup(t *testing.T) {
	env := vibeSetup(&Task{Spec: TaskSpec{Model: "mistral-medium-3.5"}})
	for k, want := range map[string]string{
		"VIBE_HOME": "/profile/vibe", "VIBE_DEFAULT_AGENT": "ask", "VIBE_ENABLE_AUTO_APPROVE": "false",
		"VIBE_ENABLE_CONNECTORS": "false", "VIBE_ENABLE_TELEMETRY": "false", "VIBE_ENABLE_UPDATE_CHECKS": "false",
		"VIBE_ENABLE_AUTO_UPDATE": "false", "VIBE_ACTIVE_MODEL": "mistral-medium-3.5",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if _, ok := vibeSetup(&Task{})["VIBE_ACTIVE_MODEL"]; ok {
		t.Error("a model set when the agent names none: Vibe runs its own")
	}
	args, _ := vibeRuntime.command(newNode(options{callbackURL: "http://n"}), newTestTask())
	if len(args) != 1 || args[0] != "/opt/eriphe/vibe/vibe-acp" {
		t.Errorf("command = %v", args)
	}
	if rt, err := runtimeFor("vibe"); err != nil || agentPackages["vibe"] != "vibe" || rt == nil {
		t.Errorf("vibe runtime %v, package %q", err, agentPackages["vibe"])
	}
}

// Vibe takes the task's HTTP servers though its initialize does not say
// so, and its tool calls are named by their _meta.tool_name, their title
// being a summary of the call.
func TestVibeSession(t *testing.T) {
	task := mcpTask()
	vibeRuntime.command(newNode(options{callbackURL: "http://n"}), task)
	script := func(f *fakeAgent, m rpcMessage) {
		call := map[string]any{"toolCallId": "e1", "title": "Write a.md", "kind": "other", "status": "pending",
			"rawInput": map[string]any{"path": "a.md"}, "_meta": map[string]any{"tool_name": "vault_write_note", "effect_kind": "tool"}}
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e1", "title": "Write a.md", "kind": "other",
			"status": "pending", "rawInput": map[string]any{"path": "a.md"}, "_meta": call["_meta"]})
		// As Vibe does: the request carries the call's id only.
		f.ask("session/request_permission", map[string]any{"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "e1"},
			"options": []map[string]any{{"optionId": "allow_once", "kind": "allow_once"}, {"optionId": "deny", "kind": "reject_once"}}})
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e2", "title": "Run: ls", "kind": "execute",
			"status": "in_progress", "rawInput": map[string]any{"command": "ls"}, "_meta": map[string]any{"tool_name": "bash"}})
		f.write(map[string]any{"id": m.ID, "result": map[string]any{"stopReason": "end_turn"}})
	}
	s, f, done := startFake(t, task, "go", false, func(f *fakeAgent) { f.client, f.script = vibeRuntime, script })
	p := answer(t, task, permAnswer{Allow: true})
	if p.ToolName != "mcp__vault__write_note" || p.Input["path"] != "a.md" {
		t.Errorf("permission = %s %v", p.ToolName, p.Input)
	}
	waitTurns(t, task, 1)
	tools := eventsOf(task, "tool_use")
	if len(tools) != 1 || tools[0].Data["name"] != "Bash" {
		t.Errorf("tool_use = %v", tools)
	}
	f.mu.Lock()
	var params struct {
		MCPServers []map[string]any `json:"mcpServers"`
	}
	json.Unmarshal(f.requests[1].Params, &params)
	f.mu.Unlock()
	if len(params.MCPServers) != 3 || params.MCPServers[0]["type"] != "http" {
		t.Errorf("mcpServers = %v", params.MCPServers)
	}
	s.stop()
	<-done
}
