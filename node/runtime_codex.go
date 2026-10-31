package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
)

// codexRuntime drives Codex, OpenAI's coding agent, through its app-server
// (`codex app-server`): JSON-RPC 2.0 without the "jsonrpc" member, one
// message per line, over the container's stdin and stdout. The node is the
// client. A thread is the agent's session, a turn one prompt.
//
// The task container is the sandbox: Codex's own (bubblewrap) cannot run
// without privileges, so it runs with sandbox "danger-full-access" and the
// approval policy "untrusted", which asks before every command and every
// file change. Each request goes to Task.decide, so eriphe-core's policy
// applies as with the other runtimes. (codex-acp, the ACP adapter, only
// offers modes that rely on Codex's sandbox or never ask.)
//
// The task's MCP servers and system prompt come with each thread
// (thread/start and thread/resume): its config's mcp_servers, every tool
// asked for but the spec's allowed ones, and developerInstructions. An MCP
// tool is asked for through mcpServer/elicitation/request, as Codex does by
// default (feature tool_call_mcp_elicitation).
//
// The credential is lent by eriphe-core as the auth.json of a ChatGPT
// sign-in (ERIPHE_CODEX_AUTH): written to the /tmp tmpfs, linked from
// CODEX_HOME, and taken out of the environment before Codex starts. It
// carries no refresh token: eriphe-core refreshes the sign-in, never a task.
type codexRuntime struct{}

// codexStart writes the lent auth.json and starts the app-server. CODEX_HOME
// is in /profile, so a restarted task finds its threads; the auth.json in it
// is a link to /tmp, so the token never reaches the node's disk.
const codexStart = `umask 077
export CODEX_HOME=/profile/codex
mkdir -p "$CODEX_HOME" /tmp/eriphe-codex
if [ -n "$ERIPHE_CODEX_AUTH" ]; then
  printf '%s' "$ERIPHE_CODEX_AUTH" > /tmp/eriphe-codex/auth.json
  ln -sfn /tmp/eriphe-codex/auth.json "$CODEX_HOME/auth.json"
fi
unset ERIPHE_CODEX_AUTH
exec codex app-server "$@"`

// codexConfig is what Codex is told at start, as -c key=value (TOML values).
var codexConfig = []string{
	`cli_auth_credentials_store="file"`,
	`check_for_update_on_startup=false`,
	`sandbox_mode="danger-full-access"`,
	`analytics.enabled=false`,
	`feedback.enabled=false`,
	// MCP tool approvals as elicitations, which carry the tool's arguments.
	`features.tool_call_mcp_elicitation=true`,
}

const (
	codexApproval = "untrusted"
	codexSandbox  = "danger-full-access"
)

// codexExtra is more configuration, for tests against a fake model.
var codexExtra []string

func (codexRuntime) command(n *node, t *Task) ([]string, map[string]string) {
	servers, err := mcpServersFor(n.callbackFor(t), t)
	t.mu.Lock()
	t.mcp, t.mcpErr = servers, err
	t.mu.Unlock()
	args := []string{"sh", "-c", codexStart, "codex"}
	for _, c := range append(codexConfig, codexExtra...) {
		args = append(args, "-c", c)
	}
	if repoSkillsOff(t.Spec) {
		// No skill catalog in the model's context: the repository's
		// .agents/skills are not offered (skills.go).
		args = append(args, "-c", "skills.include_instructions=false")
	}
	return args, map[string]string{"NO_COLOR": "1", "RUST_LOG": "warn"}
}

func (codexRuntime) attach(t *Task, stdin io.WriteCloser) session {
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	servers, err := t.mcp, t.mcpErr
	t.mu.Unlock()
	return &codexSession{t: t, out: stdin, cwd: workDir(t), ctx: ctx, cancel: cancel, servers: servers, serversErr: err,
		calls: map[int64]chan rpcReply{}, changes: map[string][]string{}, mcpCalls: map[string]codexItem{}, busy: true}
}

// codexMessage is a JSON-RPC message as the app-server writes it.
type codexMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type codexSession struct {
	t          *Task
	cwd        string
	ctx        context.Context
	cancel     context.CancelFunc
	servers    []mcpServer // the task's MCP servers
	serversErr error

	wmu sync.Mutex
	out io.WriteCloser

	mu       sync.Mutex
	nextID   int64
	calls    map[int64]chan rpcReply
	thread   string
	turn     string
	busy     bool
	turnDone chan codexTurn
	changes  map[string][]string  // file change item → the files it touches
	mcpCalls map[string]codexItem // MCP tool calls started, not completed
	last     string               // the agent's last message of the turn
	denials  []string
	errText  string // the last error the server reported in the turn
	err      error
}

type codexTurn struct {
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (s *codexSession) write(m codexMessage) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.out.Write(append(raw, '\n'))
	return err
}

func (s *codexSession) call(method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	ch := make(chan rpcReply, 1)
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.calls[id] = ch
	s.mu.Unlock()
	if err := s.write(codexMessage{ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: raw}); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", method, r.err)
		}
		return r.result, nil
	case <-s.ctx.Done():
		return nil, fmt.Errorf("%s: the agent is gone", method)
	}
}

func (s *codexSession) notify(method string, params any) error {
	raw, _ := json.Marshal(params)
	return s.write(codexMessage{Method: method, Params: raw})
}

func (s *codexSession) run(first string, stdout io.Reader) error {
	defer s.cancel()
	go s.begin(first)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var m codexMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go s.serve(m)
		case m.Method != "":
			s.event(m.Method, m.Params)
		case len(m.ID) > 0:
			var id int64
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			s.mu.Lock()
			ch := s.calls[id]
			delete(s.calls, id)
			s.mu.Unlock()
			if ch != nil {
				r := rpcReply{result: m.Result}
				if m.Error != nil {
					r.err = m.Error
				}
				ch <- r
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *codexSession) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.out.Close()
}

// threadParams are what every thread of a task runs with, a resumed one
// too: it takes today's system prompt and servers, not the ones it began
// with.
func (s *codexSession) threadParams() map[string]any {
	p := map[string]any{"cwd": s.cwd, "approvalPolicy": codexApproval, "sandbox": codexSandbox}
	if m := s.t.Spec.Model; m != "" {
		p["model"] = m
	}
	if prompt := s.t.Spec.AppendSystemPrompt; prompt != "" {
		p["developerInstructions"] = prompt
	}
	if cfg := codexThreadConfig(s.t.Spec, s.servers); len(cfg) > 0 {
		p["config"] = cfg
	}
	return p
}

// codexToolTimeout is how long an MCP tool may run, as Claude Code's
// MCP_TOOL_TIMEOUT.
const codexToolTimeout = 3600

// codexThreadConfig is a thread's config overrides: the task's MCP servers
// (all of them: the configuration's own are replaced), the web search held
// to OpenAI's cached index (live, it would open pages from OpenAI's servers
// where eriphe-core cannot see them; and live is the default with full
// access) and, for the brain, no AGENTS.md of the repository's.
func codexThreadConfig(spec TaskSpec, servers []mcpServer) map[string]any {
	cfg := map[string]any{"web_search": "cached"}
	if spec.NoAgentMemory {
		cfg["project_doc_max_bytes"] = 0
	}
	if len(servers) == 0 {
		return cfg
	}
	allServers, allowedTools := allowedMCP(spec.AllowedTools)
	defs := map[string]any{}
	for _, srv := range servers {
		def := map[string]any{"tool_timeout_sec": codexToolTimeout, "default_tools_approval_mode": "prompt"}
		if srv.URL != "" {
			def["url"] = srv.URL
			if len(srv.Headers) > 0 {
				def["http_headers"] = srv.Headers
			}
		} else {
			def["command"] = srv.Command
			if len(srv.Args) > 0 {
				def["args"] = srv.Args
			}
			if len(srv.Env) > 0 {
				def["env"] = srv.Env
			}
		}
		if allServers[srv.Name] {
			def["default_tools_approval_mode"] = "approve"
		}
		if tools := allowedTools[srv.Name]; len(tools) > 0 {
			approve := map[string]any{}
			for _, tool := range tools {
				approve[tool] = map[string]string{"approval_mode": "approve"}
			}
			def["tools"] = approve
		}
		defs[srv.Name] = def
	}
	cfg["mcp_servers"] = defs
	return cfg
}

// begin starts the app-server's session, then a thread (or the task's own
// after a restart) and gives it its first turn.
func (s *codexSession) begin(first string) {
	if s.serversErr != nil {
		s.fail(s.serversErr)
		return
	}
	if _, err := s.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "eriphe-node", "title": "Eriphe", "version": "0"},
	}); err != nil {
		s.fail(err)
		return
	}
	s.notify("initialized", map[string]any{})

	s.t.mu.Lock()
	resume := s.t.SessionID
	s.t.mu.Unlock()
	if resume == "" {
		resume = s.t.Spec.Resume
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if resume != "" {
		p := s.threadParams()
		p["threadId"] = resume
		p["excludeTurns"] = true
		raw, err := s.call("thread/resume", p)
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		if err != nil || out.Thread.ID == "" {
			s.t.emit("error", map[string]any{"message": fmt.Sprintf("thread %s not resumed (%v): a new one starts", resume, err)})
			resume = ""
			if first != s.t.Spec.Prompt {
				first = s.t.Spec.Prompt + "\n\n" + first
			}
		}
	}
	if resume == "" {
		raw, err := s.call("thread/start", s.threadParams())
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		if err == nil && out.Thread.ID == "" {
			err = errors.New("thread/start: no thread id")
		}
		if err != nil {
			s.fail(err)
			return
		}
	}
	if want := s.t.Spec.Model; want != "" && out.Model != "" && out.Model != want {
		s.fail(fmt.Errorf("the agent runs model %s, not %s: is it part of the ChatGPT plan?", out.Model, want))
		return
	}
	s.mu.Lock()
	s.thread = out.Thread.ID
	s.mu.Unlock()
	s.t.mu.Lock()
	s.t.SessionID = out.Thread.ID
	s.t.mu.Unlock()
	s.prompt(first)
}

func (s *codexSession) send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy || s.thread == "" {
		return errNotIdle
	}
	s.busy = true
	go s.prompt(text)
	return nil
}

// codexStatuses are the turn's ends as the subtypes of Claude Code's results.
var codexStatuses = map[string]string{
	"completed":   "success",
	"interrupted": "cancelled",
	"failed":      "error_during_execution",
}

// prompt runs one turn: turn/start answers at once, turn/completed ends it.
func (s *codexSession) prompt(text string) {
	s.mu.Lock()
	if len(s.denials) > 0 {
		text = "Denied during your last turn:\n- " + strings.Join(s.denials, "\n- ") + "\n\n" + text
		s.denials = nil
	}
	s.last, s.errText = "", ""
	done := make(chan codexTurn, 1)
	s.turnDone = done
	thread := s.thread
	s.mu.Unlock()

	var out struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	raw, err := s.call("turn/start", map[string]any{"threadId": thread,
		"input": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}})
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	var turn codexTurn
	if err == nil {
		s.mu.Lock()
		s.turn = out.Turn.ID
		s.mu.Unlock()
		select {
		case turn = <-done:
		case <-s.ctx.Done():
			return // the agent is gone: its exit says the rest
		}
	}
	s.t.mu.Lock()
	stopping := s.t.stopping
	s.t.mu.Unlock()
	if s.ctx.Err() != nil || stopping && err != nil {
		return
	}
	s.mu.Lock()
	s.busy, s.turn = false, ""
	data := map[string]any{"subtype": codexStatuses[turn.Status], "stop_reason": turn.Status, "result": s.last}
	errText := s.errText
	s.mu.Unlock()
	if data["subtype"] == "" {
		data["subtype"] = turn.Status
	}
	if err == nil && turn.Status == "failed" {
		msg := errText
		if turn.Error != nil && turn.Error.Message != "" {
			msg = turn.Error.Message
		}
		err = errors.New(msg)
	}
	if err != nil {
		msg := err.Error()
		data["subtype"], data["result"] = "error_during_execution", msg
		if authError(msg) {
			data["auth_error"] = true
		}
	}
	s.t.emit("turn_end", data)
	s.t.setState(stateIdle)
}

func (s *codexSession) stop() {
	s.mu.Lock()
	thread, turn, busy := s.thread, s.turn, s.busy
	s.mu.Unlock()
	if busy && thread != "" && turn != "" {
		// Not waited for: closing its input ends the app-server anyway.
		raw, _ := json.Marshal(map[string]string{"threadId": thread, "turnId": turn})
		s.mu.Lock()
		s.nextID++
		id := s.nextID
		s.mu.Unlock()
		s.write(codexMessage{ID: json.RawMessage(fmt.Sprint(id)), Method: "turn/interrupt", Params: raw})
	}
	s.out.Close()
}

// codexItem is the part of a thread item the node reads.
type codexItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Command string `json:"command"`
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Query     string         `json:"query"`
}

// event reads one notification: the agent's messages become text events,
// its commands, file changes and tool calls tool_use events as they start.
func (s *codexSession) event(method string, params json.RawMessage) {
	var p struct {
		Item  codexItem `json:"item"`
		Turn  codexTurn `json:"turn"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool   `json:"willRetry"`
		Delta     string `json:"delta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	switch method {
	case "item/started":
		it := p.Item
		switch it.Type {
		case "commandExecution":
			s.t.emit("tool_use", map[string]any{"name": "Bash", "input": map[string]any{"command": unwrapShell(it.Command)}})
		case "fileChange":
			var paths []string
			for _, c := range it.Changes {
				paths = append(paths, c.Path)
			}
			s.mu.Lock()
			s.changes[it.ID] = paths
			s.mu.Unlock()
			for _, f := range paths {
				s.t.emit("tool_use", map[string]any{"name": "Edit", "input": map[string]any{"file_path": f}})
			}
		case "mcpToolCall":
			s.mu.Lock()
			s.mcpCalls[it.ID] = it
			s.mu.Unlock()
			s.t.emit("tool_use", map[string]any{"name": "mcp__" + it.Server + "__" + it.Tool, "input": it.Arguments})
		case "webSearch":
			s.t.emit("tool_use", map[string]any{"name": "WebSearch", "input": map[string]any{"query": it.Query}})
		}
	case "item/agentMessage/delta":
		if s.t.Spec.Stream && p.Delta != "" {
			s.t.write(p.Delta) // the draft, until the whole message completes
		}
	case "item/completed":
		if p.Item.Type == "mcpToolCall" {
			s.mu.Lock()
			delete(s.mcpCalls, p.Item.ID)
			s.mu.Unlock()
		}
		if p.Item.Type == "agentMessage" && strings.TrimSpace(p.Item.Text) != "" {
			s.mu.Lock()
			s.last = p.Item.Text
			s.mu.Unlock()
			s.t.emit("text", map[string]any{"text": p.Item.Text})
		}
	case "error":
		if !p.WillRetry && p.Error.Message != "" {
			s.mu.Lock()
			s.errText = p.Error.Message
			s.mu.Unlock()
		}
	case "turn/completed":
		s.mu.Lock()
		done := s.turnDone
		s.turnDone = nil
		s.mu.Unlock()
		if done != nil {
			done <- p.Turn
		}
	}
}

// serve answers a request of the app-server: an approval, through
// Task.decide, or an error for what the node does not offer (more
// permissions, questions to the user, client tools).
func (s *codexSession) serve(m codexMessage) {
	reply := func(result any) {
		raw, _ := json.Marshal(result)
		s.write(codexMessage{ID: m.ID, Result: raw})
	}
	var p struct {
		ItemID  string `json:"itemId"`
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
		Reason  string `json:"reason"`
	}
	json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "item/commandExecution/requestApproval":
		in := map[string]any{"command": unwrapShell(p.Command)}
		if p.Cwd != "" {
			in["cwd"] = p.Cwd
		}
		if p.Reason != "" {
			in["reason"] = p.Reason
		}
		reply(map[string]string{"decision": s.decision("Bash", in)})
	case "item/fileChange/requestApproval":
		s.mu.Lock()
		paths := s.changes[p.ItemID]
		s.mu.Unlock()
		if len(paths) == 0 {
			paths = []string{""}
		}
		// Every file is asked for: the policy reads one path at a time.
		decision := "accept"
		for _, f := range paths {
			if f != "" && !path.IsAbs(f) {
				f = path.Join(s.cwd, f)
			}
			if decision = s.decision("Edit", map[string]any{"file_path": f}); decision != "accept" {
				break
			}
		}
		reply(map[string]string{"decision": decision})
	case "mcpServer/elicitation/request":
		reply(map[string]any{"action": s.mcpApproval(m.Params)})
	default:
		s.write(codexMessage{ID: m.ID, Error: &rpcError{Code: -32601, Message: "Method not found: " + m.Method}})
	}
}

// mcpApproval answers an MCP elicitation: Codex's approval of an MCP tool
// call goes through Task.decide, once, never for the session. Any other
// elicitation (a server asking the user for input) is declined: the node
// has no one to ask.
//
//	{"threadId":"t","turnId":"u","serverName":"eriphe-core","mode":"form",
//	 "message":"Allow the eriphe-core MCP server to run tool \"write_note\"?",
//	 "_meta":{"codex_approval_kind":"mcp_tool_call","tool_params":{...}},
//	 "requestedSchema":{"type":"object","properties":{}}}
func (s *codexSession) mcpApproval(params json.RawMessage) string {
	var p struct {
		ServerName string `json:"serverName"`
		Mode       string `json:"mode"`
		Message    string `json:"message"`
		Meta       struct {
			Kind   string         `json:"codex_approval_kind"`
			Params map[string]any `json:"tool_params"`
		} `json:"_meta"`
	}
	json.Unmarshal(params, &p)
	if p.Mode != "form" || p.Meta.Kind != "mcp_tool_call" {
		return "decline"
	}
	tool, in := s.mcpCall(p.ServerName, p.Meta.Params, p.Message)
	// accept, decline or cancel: the elicitation's actions too.
	return s.decision("mcp__"+p.ServerName+"__"+tool, in)
}

// mcpCall finds the tool call an approval is about: the elicitation names
// the server and the arguments, the call's item/started the tool. The
// message names it too, as a last resort.
func (s *codexSession) mcpCall(server string, args map[string]any, message string) (string, map[string]any) {
	want, _ := json.Marshal(args)
	s.mu.Lock()
	defer s.mu.Unlock()
	tool := ""
	for _, it := range s.mcpCalls {
		if it.Server != server {
			continue
		}
		if got, _ := json.Marshal(it.Arguments); string(got) == string(want) || tool == "" {
			tool = it.Tool
			if args == nil {
				args = it.Arguments
			}
		}
	}
	if tool == "" {
		if _, rest, ok := strings.Cut(message, `run tool "`); ok {
			tool, _, _ = strings.Cut(rest, `"`)
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	return tool, args
}

// decision asks the task's permission for one tool call: accept or decline,
// never for the whole session.
func (s *codexSession) decision(tool string, in map[string]any) string {
	a := s.t.decide(s.ctx, tool, in)
	if s.ctx.Err() != nil {
		return "cancel"
	}
	if a.Allow {
		return "accept"
	}
	if a.Message != "" {
		s.mu.Lock()
		s.denials = append(s.denials, tool+" "+subjectOf(in)+": "+a.Message)
		s.mu.Unlock()
	}
	return "decline"
}

// unwrapShell is the command a login shell is asked to run: Codex runs
// every command as /bin/bash -lc '<command>', and the policy reads the
// command itself (its rules look for the start of a command).
func unwrapShell(cmd string) string {
	for _, sh := range []string{"/bin/bash", "bash", "/bin/sh", "sh", "/usr/bin/bash", "/bin/zsh", "zsh"} {
		for _, flag := range []string{" -lc ", " -c "} {
			rest, ok := strings.CutPrefix(cmd, sh+flag)
			if !ok {
				continue
			}
			if inner, ok := shellWord(rest); ok {
				return inner
			}
		}
	}
	return cmd
}

// shellWord reads s as one shell word: quoted with ” or "" (as shlex
// joins them), or bare. ok is false if anything follows it.
func shellWord(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return "", false
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 2
		case '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
				}
				b.WriteByte(s[i])
			}
			if i >= len(s) {
				return "", false
			}
			i++
		case ' ', '\t', '\n':
			return "", false
		case '\\':
			if i+1 < len(s) {
				b.WriteByte(s[i+1])
			}
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), true
}
