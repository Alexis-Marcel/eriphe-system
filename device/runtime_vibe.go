package main

// vibeProgram is Mistral Vibe's ACP server, as its GitHub release ships it
// for Linux: a PyInstaller directory (the program and its _internal/),
// injected whole at /opt/eriphe/vibe (worker/agents/vibe).
const vibeProgram = injectDir + "/vibe/vibe-acp"

// vibeRuntime drives Mistral Vibe (github.com/mistralai/mistral-vibe) over
// ACP. It takes MCP servers over HTTP (streamable HTTP) without saying so
// in initialize: read in its code, version 3.1.1.
var vibeRuntime = acpRuntime{program: []string{vibeProgram}, setup: vibeSetup, httpMCP: true}

// vibeSetup configures Vibe for a task through its VIBE_* variables, which
// come before its remote experiments and its files: the agent's model (an
// alias of Vibe's, its key lent by eriphe-core in MISTRAL_API_KEY), every
// tool asked through ACP (mode "ask", no auto-approve), none of the
// account's own connectors (tools Eriphe would not see), no telemetry, no
// update. Its sessions live in /profile, so a restarted task loads its own.
// The task's folder is never trusted, so a repository's .vibe/ settings,
// AGENTS.md and skills are not read: its instructions and skills come from
// eriphe-core.
func vibeSetup(t *Task) map[string]string {
	env := map[string]string{
		"VIBE_HOME":                 "/profile/vibe",
		"VIBE_DEFAULT_AGENT":        "ask",
		"VIBE_ENABLE_AUTO_APPROVE":  "false",
		"VIBE_ENABLE_CONNECTORS":    "false",
		"VIBE_ENABLE_TELEMETRY":     "false",
		"VIBE_ENABLE_UPDATE_CHECKS": "false",
		"VIBE_ENABLE_AUTO_UPDATE":   "false",
		"VIBE_ENABLE_NOTIFICATIONS": "false",
	}
	if m := t.Spec.Model; m != "" {
		env["VIBE_ACTIVE_MODEL"] = m
	}
	return env
}
