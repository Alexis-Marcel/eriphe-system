package main

import (
	"bufio"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
)

// claudeCode runs `claude -p` in stream-json mode: one JSON line per user
// turn on stdin, its events on stdout. Permission requests come back
// through the node's MCP tool approve (perm.go).
type claudeCode struct{}

// The files Claude Code reads its configuration from, in the task's run
// directory (runFiles): never on its command line, which every user of the
// machine sees.
const (
	claudeMCP      = "mcp.json"
	claudeSettings = "settings.json"
	claudePrompt   = "system-prompt.md"
)

// Commits and PRs made by Eriphe's workers carry no attribution line, and
// its sessions never push to the account's phone: Eriphe decides when to
// tell its user, through its own channels.
const claudeSettingsJSON = `{"attribution":{"commit":"","pr":""},"agentPushNotifEnabled":false,"inputNeededNotifEnabled":false,"remoteControlAtStartup":false}`

// files are the task's MCP servers (with its secret), Claude Code's
// settings and the system prompt to append.
func (claudeCode) files(n *node, t *Task) map[string][]byte {
	spec := t.Spec
	perm := map[string]any{"type": "http", "url": n.callbackFor(t) + "/perm/" + t.ID + "/mcp",
		"headers": map[string]string{"Authorization": "Bearer " + t.secret}}
	servers := map[string]any{}
	for name, def := range spec.MCPServers {
		servers[name] = def
	}
	// Catalog servers point at the node, with the task's secret: never at
	// the server itself, whose credential stays with eriphe-core.
	for _, name := range spec.Servers {
		def := map[string]any{"type": "http", "url": n.callbackFor(t) + "/mcp/" + name,
			"headers": map[string]string{"Authorization": "Bearer " + t.secret}}
		if slices.Contains(spec.AlwaysLoad, name) {
			def["alwaysLoad"] = true // no tool search round trip before its first call
		}
		servers[name] = def
	}
	servers["eriphe_node"] = perm
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": servers})
	files := map[string][]byte{claudeMCP: mcpConfig, claudeSettings: []byte(claudeSettingsJSON)}
	if spec.AppendSystemPrompt != "" {
		files[claudePrompt] = []byte(spec.AppendSystemPrompt)
	}
	return files
}

func (claudeCode) command(n *node, t *Task) ([]string, map[string]string) {
	spec := t.Spec
	args := []string{"claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--mcp-config", runPath(claudeMCP), "--strict-mcp-config",
		"--permission-prompt-tool", "mcp__eriphe_node__approve", "--permission-mode", "default",
		"--settings", runPath(claudeSettings)}
	if spec.Profile != "" {
		// A resumed session takes the prompt it is started with, not the one
		// recorded when it began (the brain's changes with each deploy).
		args = append(args, "--system-prompt-snapshot", "off")
	}
	if spec.Stream {
		// The text as it is written, besides the whole messages (stream_event lines).
		args = append(args, "--include-partial-messages")
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt-file", runPath(claudePrompt))
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	if resume := t.SessionID; resume != "" {
		args = append(args, "--resume", resume)
	} else if spec.Resume != "" {
		args = append(args, "--resume", spec.Resume)
	}
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": "/profile",
		// A permission can wait for my answer for a long time: the agent must not give up on it.
		"MCP_TOOL_TIMEOUT": "3600000",
		// No usage statistics, error reports or update checks leave the task.
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		// The injected binary is read-only: updates come from a new package.
		"DISABLE_AUTOUPDATER": "1",
	}
	if spec.NoAgentMemory {
		env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
		env["CLAUDE_CODE_DISABLE_CLAUDE_MDS"] = "1"
	}
	return args, env
}

func (claudeCode) attach(t *Task, stdin io.WriteCloser) session {
	return &claudeSession{t: t, stdin: stdin}
}

type claudeSession struct {
	t     *Task
	mu    sync.Mutex
	stdin io.WriteCloser
	busy  bool
}

func (c *claudeSession) send(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy {
		return errNotIdle
	}
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	if _, err := c.stdin.Write(append(msg, '\n')); err != nil {
		return err
	}
	c.busy = true
	return nil
}

func (c *claudeSession) stop() { c.stdin.Close() }

func (c *claudeSession) run(first string, stdout io.Reader) error {
	err := c.send(first)
	t := c.t
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if sid, ok := ev["session_id"].(string); ok && sid != "" {
			t.mu.Lock()
			t.SessionID = sid
			t.mu.Unlock()
		}
		switch ev["type"] {
		case "stream_event":
			if text := textDelta(ev); text != "" {
				t.write(text)
			}
		case "assistant":
			msg, _ := ev["message"].(map[string]any)
			blocks, _ := msg["content"].([]any)
			// An API error comes as the agent's own text ("API Error: …"),
			// from a model of its own: it is marked so that it is never
			// shown as the agent's answer.
			apiError := ev["error"] != nil && ev["error"] != "" || msg["model"] == "<synthetic>"
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				switch bm["type"] {
				case "text":
					data := map[string]any{"text": bm["text"]}
					if apiError {
						data["error"] = true
					}
					t.emit("text", data)
				case "tool_use":
					t.emit("tool_use", map[string]any{"name": bm["name"], "input": bm["input"]})
				}
			}
		case "result":
			text, _ := ev["result"].(string)
			data := map[string]any{"subtype": ev["subtype"], "result": text, "denials": ev["permission_denials"]}
			if isErr, _ := ev["is_error"].(bool); isErr {
				data["is_error"] = true // an API error still ends with subtype "success"
			}
			// An auth failure still ends with subtype "success".
			if strings.Contains(text, "Failed to authenticate") || strings.Contains(text, "Invalid API key") ||
				strings.Contains(text, "Please run /login") {
				data["auth_error"] = true
			}
			c.mu.Lock()
			c.busy = false
			c.mu.Unlock()
			t.emit("turn_end", data)
			t.setState(stateIdle)
		}
	}
	return err
}

// textDelta is the piece of text a stream_event adds to the agent's answer:
//
//	{"type":"stream_event","parent_tool_use_id":null,
//	 "event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}}
//
// The block then comes whole in an assistant message, which ends the draft.
// Deltas of a subagent (a parent_tool_use_id), of thinking or of a tool's
// input are not the answer.
func textDelta(ev map[string]any) string {
	if parent, _ := ev["parent_tool_use_id"].(string); parent != "" {
		return ""
	}
	inner, _ := ev["event"].(map[string]any)
	if inner["type"] != "content_block_delta" {
		return ""
	}
	delta, _ := inner["delta"].(map[string]any)
	if delta["type"] != "text_delta" {
		return ""
	}
	text, _ := delta["text"].(string)
	return text
}
