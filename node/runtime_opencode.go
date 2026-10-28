package main

import (
	"encoding/json"
	"path"
	"strings"
)

// opencodeStart writes the task's system prompt where OpenCode reads its
// instructions (it takes no system prompt through ACP), then starts it.
const opencodeStart = `umask 077
if [ -n "$` + instructionsEnv + `" ]; then
  mkdir -p "$(dirname ` + agentInstructions + `)"
  printf '%s\n' "$` + instructionsEnv + `" > ` + agentInstructions + `
fi
unset ` + instructionsEnv + `
exec opencode acp "$@"`

var opencodeRuntime = acpRuntime{program: []string{"sh", "-c", opencodeStart, "opencode"}, setup: opencodeSetup}

// opencodeSetup configures OpenCode (opencode.ai) for a task: the agent's
// model ("provider/model", its key lent by eriphe-core in the provider's own
// variable), no update, no sharing, every edit, command and fetch asked
// through ACP, and no web search. Its sessions live in /profile, so a restarted task loads its
// own. A repository's opencode.json is ignored: it could add plugins and MCP
// servers that run without asking, or send the key to another address. Its
// AGENTS.md and CLAUDE.md are still read, but by the brain (NoAgentMemory),
// whose only instructions are its own.
//
// The task's MCP servers come through ACP (runtime_acp.go). Their tools
// are asked for too, but for the spec's allowed tools, as Claude Code's
// --allowedTools: OpenCode would run them without asking otherwise.
func opencodeSetup(t *Task) map[string]string {
	// Its page reads are asked, so that eriphe-core decides each; its web
	// search, through a third party (Exa or Parallel), is off for now.
	permission := map[string]string{"edit": "ask", "bash": "ask", "webfetch": "ask", "websearch": "deny"}
	for k, v := range opencodeMCPPermissions(t) {
		permission[k] = v
	}
	cfg := map[string]any{
		"$schema":    "https://opencode.ai/config.json",
		"autoupdate": false,
		"share":      "disabled",
		// OpenCode keeps the order of these rules, the last match winning:
		// encoding/json writes them sorted, so a server's "<server>_*" comes
		// before its tools.
		"permission": permission,
		"experimental": map[string]any{
			// A denied call goes on with the turn, as with Claude Code.
			"continue_loop_on_deny": true,
			// A tool can wait long for the user, as Claude Code's MCP_TOOL_TIMEOUT.
			"mcp_timeout": 3600000,
		},
	}
	var instructions []string
	if !t.Spec.NoAgentMemory {
		dir := workDir(t)
		instructions = append(instructions, path.Join(dir, "AGENTS.md"), path.Join(dir, "CLAUDE.md"))
	}
	if t.Spec.AppendSystemPrompt != "" {
		instructions = append(instructions, agentInstructions)
	}
	if len(instructions) > 0 {
		cfg["instructions"] = instructions
	}
	if m := t.Spec.Model; m != "" {
		cfg["model"] = m
		if !strings.HasPrefix(m, "opencode/") {
			// No falling back on OpenCode's own free models, which would
			// get the code instead of the provider chosen.
			cfg["disabled_providers"] = []string{"opencode"}
		}
	}
	raw, _ := json.Marshal(cfg)
	env := map[string]string{
		"OPENCODE_CONFIG_CONTENT":         string(raw),
		"OPENCODE_DISABLE_PROJECT_CONFIG": "1",
		"XDG_DATA_HOME":                   "/profile/data",
		"XDG_STATE_HOME":                  "/profile/state",
	}
	if t.Spec.AppendSystemPrompt != "" {
		env[instructionsEnv] = t.Spec.AppendSystemPrompt
	}
	if t.Spec.NoAgentMemory {
		// Neither ~/.claude/CLAUDE.md nor Claude Code's skills.
		env["OPENCODE_DISABLE_CLAUDE_CODE"] = "1"
	}
	return env
}

// opencodeMCPPermissions asks for every tool of the task's MCP servers
// ("<server>_*", OpenCode's names for them) but the allowed ones.
func opencodeMCPPermissions(t *Task) map[string]string {
	t.mu.Lock()
	servers := t.mcp
	t.mu.Unlock()
	allServers, allowedTools := allowedMCP(t.Spec.AllowedTools)
	out := map[string]string{}
	for _, srv := range servers {
		prefix := opencodeName(srv.Name) + "_"
		out[prefix+"*"] = "ask"
		if allServers[srv.Name] {
			out[prefix+"*"] = "allow"
		}
		for _, tool := range allowedTools[srv.Name] {
			out[prefix+opencodeName(tool)] = "allow"
		}
	}
	return out
}
