package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// acpRuntime drives an agent that speaks the Agent Client Protocol
// (agentclientprotocol.com): JSON-RPC 2.0, one message per line, over the
// container's stdin and stdout. The node is the client. It offers no file
// system or terminal of its own: the agent works in its container, and asks
// before acting through session/request_permission, which goes to
// Task.decide. The task's MCP servers are given in session/new and
// session/load, as to Claude Code.
type acpRuntime struct {
	program []string // the agent's command line
	// args, when set, are the task's own arguments, after the program's.
	args func(t *Task) []string
	// setup adds what this agent needs to the program's environment.
	setup func(t *Task) map[string]string
}

func (r acpRuntime) command(n *node, t *Task) ([]string, map[string]string) {
	servers, err := mcpServersFor(n.callbackFor(t), t)
	t.mu.Lock()
	t.mcp, t.mcpErr = servers, err
	t.mu.Unlock()
	t.modelURL = n.modelBase(t)
	env := map[string]string{}
	if r.setup != nil {
		env = r.setup(t)
	}
	args := slices.Clone(r.program)
	if r.args != nil {
		args = append(args, r.args(t)...)
	}
	return args, env
}

// customACPRuntime is any other ACP agent (runtime acp), by the command of
// its agent: run as is by the entrypoint's exec, never through a shell.
// eriphe-core checks the command; a bad one fails the task, saying why.
var customACPRuntime = acpRuntime{args: func(t *Task) []string {
	if err := checkACPCommand(t.Spec.Command); err != nil {
		return []string{"sh", "-c", `echo "eriphe: $1" >&2; exit 64`, "acp", err.Error()}
	}
	return t.Spec.Command
}}

var acpShells = []string{"sh", "bash", "dash", "ash", "zsh", "ksh", "mksh", "fish", "csh", "tcsh", "busybox", "env"}

// checkACPCommand is eriphe-core's check of an ACP agent's command, again:
// a program, by name or absolute path, and its arguments, no shell.
func checkACPCommand(cmd []string) error {
	if len(cmd) == 0 || len(cmd) > 32 {
		return errors.New("an acp agent needs its command, of at most 32 arguments")
	}
	for _, arg := range cmd {
		if arg == "" || len(arg) > 1024 || strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("bad argument %q in the agent's command", arg)
		}
	}
	prog := cmd[0]
	if strings.ContainsAny(prog, " \t") || strings.Contains(prog, "/") && !path.IsAbs(prog) ||
		slices.Contains(acpShells, path.Base(prog)) {
		return fmt.Errorf("the agent's program %q is not one to run as is", prog)
	}
	return nil
}

func (r acpRuntime) attach(t *Task, stdin io.WriteCloser) session {
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	servers, err := t.mcp, t.mcpErr
	t.mu.Unlock()
	return &acpSession{t: t, out: stdin, cwd: workDir(t), ctx: ctx, cancel: cancel, servers: servers, serversErr: err,
		calls: map[int64]chan rpcReply{}, tools: map[string]string{}, inputs: map[string]map[string]any{},
		emitted: map[string]bool{}, busy: true}
}

// rpcMessage is any JSON-RPC message: a request has a method and an id, a
// notification a method only, a response an id only.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

type rpcReply struct {
	result json.RawMessage
	err    error
}

type acpSession struct {
	t          *Task
	cwd        string
	ctx        context.Context // ends with the agent's output
	cancel     context.CancelFunc
	servers    []mcpServer // the task's MCP servers
	serversErr error

	wmu sync.Mutex
	out io.WriteCloser

	mu        sync.Mutex
	nextID    int64
	calls     map[int64]chan rpcReply
	sessionID string
	busy      bool                      // a turn is going on
	loading   bool                      // session/load replays the conversation: not news
	tools     map[string]string         // tool call id → tool name
	inputs    map[string]map[string]any // tool call id → its input, once known
	emitted   map[string]bool           // tool calls already reported
	text      strings.Builder           // the agent's message being streamed
	messageID string
	last      string   // the agent's last message of the turn
	denials   []string // why requests were denied this turn, for the next prompt
	err       error    // why the session failed
}

func (s *acpSession) write(m rpcMessage) error {
	m.JSONRPC = "2.0"
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.out.Write(append(raw, '\n'))
	return err
}

// call sends a request to the agent and waits for its answer.
func (s *acpSession) call(method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	ch := make(chan rpcReply, 1)
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.calls[id] = ch
	s.mu.Unlock()
	if err := s.write(rpcMessage{ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: raw}); err != nil {
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

func (s *acpSession) notify(method string, params any) error {
	raw, _ := json.Marshal(params)
	return s.write(rpcMessage{Method: method, Params: raw})
}

func (s *acpSession) run(first string, stdout io.Reader) error {
	defer s.cancel()
	go s.begin(first)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var m rpcMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue // not JSON-RPC: a stray log line
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go s.serve(m)
		case m.Method != "":
			if m.Method == "session/update" {
				s.update(m.Params)
			}
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

// fail ends a session the agent cannot carry on: closing its input ends it.
func (s *acpSession) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.out.Close()
}

// begin opens the agent's session, a new one or the task's own after a
// restart, and gives it its first turn.
func (s *acpSession) begin(first string) {
	var init struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession     bool `json:"loadSession"`
			McpCapabilities struct {
				HTTP bool `json:"http"`
			} `json:"mcpCapabilities"`
		} `json:"agentCapabilities"`
	}
	raw, err := s.call("initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]string{"name": "eriphe-node", "version": "0"},
	})
	if err == nil {
		err = json.Unmarshal(raw, &init)
	}
	if err == nil && init.ProtocolVersion != 1 {
		err = fmt.Errorf("the agent speaks ACP version %d, not 1", init.ProtocolVersion)
	}
	if err == nil {
		err = s.serversErr
	}
	var servers []any
	if err == nil {
		servers, err = acpServers(s.servers, init.AgentCapabilities.McpCapabilities.HTTP)
	}
	if err != nil {
		s.fail(err)
		return
	}

	s.t.mu.Lock()
	resume := s.t.SessionID
	s.t.mu.Unlock()
	if resume == "" {
		resume = s.t.Spec.Resume
	}
	if resume != "" {
		err := errors.New("the agent cannot load a session")
		if init.AgentCapabilities.LoadSession {
			s.mu.Lock()
			s.loading = true
			s.mu.Unlock()
			var raw json.RawMessage
			raw, err = s.call("session/load", map[string]any{"sessionId": resume, "cwd": s.cwd, "mcpServers": servers})
			s.mu.Lock()
			s.loading = false
			s.mu.Unlock()
			if err == nil {
				if err = s.checkModel(raw); err != nil {
					s.fail(err)
					return
				}
			}
		}
		if err == nil {
			s.setSession(resume)
		} else {
			// The workspace is still there: start again from the task's prompt.
			s.t.emit("error", map[string]any{"message": fmt.Sprintf("session %s not resumed (%v): a new one starts", resume, err)})
			resume = ""
			if first != s.t.Spec.Prompt {
				first = s.t.Spec.Prompt + "\n\n" + first
			}
		}
	}
	if resume == "" {
		var out struct {
			SessionID string `json:"sessionId"`
		}
		raw, err := s.call("session/new", map[string]any{"cwd": s.cwd, "mcpServers": servers})
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		if err == nil && out.SessionID == "" {
			err = errors.New("session/new: no session id")
		}
		if err == nil {
			err = s.checkModel(raw)
		}
		if err != nil {
			s.fail(err)
			return
		}
		s.setSession(out.SessionID)
	}
	s.prompt(first)
}

// checkModel makes sure the session runs the task's model, when the agent
// says which it runs: OpenCode, for one, falls back on another model when
// the one asked for is not available, a missing key for instance.
func (s *acpSession) checkModel(session json.RawMessage) error {
	var out struct {
		ConfigOptions []struct {
			Category     string `json:"category"`
			CurrentValue string `json:"currentValue"`
		} `json:"configOptions"`
	}
	json.Unmarshal(session, &out)
	for _, o := range out.ConfigOptions {
		if want := s.t.Spec.Model; o.Category == "model" && want != "" && o.CurrentValue != "" && o.CurrentValue != want {
			return fmt.Errorf("the agent runs model %s, not %s: is the key of its provider there?", o.CurrentValue, want)
		}
	}
	return nil
}

// acpServers are the task's MCP servers as session/new and session/load
// take them. Over HTTP only if the agent says it can (mcpCapabilities.http);
// over stdio always (ACP requires it).
func acpServers(servers []mcpServer, http bool) ([]any, error) {
	pairs := func(m map[string]string) []map[string]string {
		out := []map[string]string{}
		for _, k := range slices.Sorted(maps.Keys(m)) {
			out = append(out, map[string]string{"name": k, "value": m[k]})
		}
		return out
	}
	out := []any{}
	for _, srv := range servers {
		if srv.URL != "" {
			if !http {
				return nil, fmt.Errorf("the agent cannot reach MCP server %s: it takes no MCP server over HTTP", srv.Name)
			}
			out = append(out, map[string]any{"type": "http", "name": srv.Name, "url": srv.URL, "headers": pairs(srv.Headers)})
			continue
		}
		args := srv.Args
		if args == nil {
			args = []string{}
		}
		out = append(out, map[string]any{"name": srv.Name, "command": srv.Command, "args": args, "env": pairs(srv.Env)})
	}
	return out, nil
}

func (s *acpSession) setSession(id string) {
	s.mu.Lock()
	s.sessionID = id
	s.mu.Unlock()
	s.t.mu.Lock()
	s.t.SessionID = id
	s.t.mu.Unlock()
}

func (s *acpSession) send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy || s.sessionID == "" {
		return errNotIdle
	}
	s.busy = true
	go s.prompt(text)
	return nil
}

// stopReasons are ACP's stop reasons as the subtypes of Claude Code's
// results, which eriphe-core already knows.
var stopReasons = map[string]string{
	"end_turn":          "success",
	"max_tokens":        "error_max_tokens",
	"max_turn_requests": "error_max_turns",
	"refusal":           "refusal",
	"cancelled":         "cancelled",
}

// prompt runs one turn, the session being busy, and reports its end.
func (s *acpSession) prompt(text string) {
	s.mu.Lock()
	if len(s.denials) > 0 {
		// ACP carries no reason with a denial: the agent hears it now.
		text = "Denied during your last turn:\n- " + strings.Join(s.denials, "\n- ") + "\n\n" + text
		s.denials = nil
	}
	s.last = ""
	sid := s.sessionID
	s.mu.Unlock()

	var out struct {
		StopReason string `json:"stopReason"`
	}
	raw, err := s.call("session/prompt", map[string]any{"sessionId": sid,
		"prompt": []any{map[string]string{"type": "text", "text": text}}})
	s.t.mu.Lock()
	stopping := s.t.stopping
	s.t.mu.Unlock()
	if s.ctx.Err() != nil || stopping && err != nil {
		return // the agent is gone: its exit says the rest
	}
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	s.flush()
	s.mu.Lock()
	s.busy = false
	data := map[string]any{"subtype": stopReasons[out.StopReason], "stop_reason": out.StopReason, "result": s.last}
	s.mu.Unlock()
	if data["subtype"] == "" {
		data["subtype"] = out.StopReason
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

func authError(msg string) bool {
	m := strings.ToLower(msg)
	for _, w := range []string{"api key", "api_key", "unauthorized", "authentication", "401"} {
		if strings.Contains(m, w) {
			return true
		}
	}
	return false
}

func (s *acpSession) stop() {
	s.mu.Lock()
	sid, busy := s.sessionID, s.busy
	s.mu.Unlock()
	if sid != "" && busy {
		s.notify("session/cancel", map[string]string{"sessionId": sid})
	}
	s.out.Close()
}

// acpToolCall is the part of a tool call the node reads, in session/update
// and session/request_permission.
type acpToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"`
	RawInput   map[string]any `json:"rawInput"`
	Locations  []struct {
		Path string `json:"path"`
	} `json:"locations"`
}

// update reads one session/update: the agent's message, streamed in chunks,
// becomes one text event; a tool call becomes a tool_use event once it runs.
func (s *acpSession) update(params json.RawMessage) {
	var p struct {
		Update struct {
			acpToolCall
			SessionUpdate string          `json:"sessionUpdate"`
			MessageID     string          `json:"messageId"`
			Content       json.RawMessage `json:"content"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	u := p.Update
	s.mu.Lock()
	loading := s.loading
	s.mu.Unlock()
	if loading {
		return
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &c) != nil || c.Type != "text" {
			return
		}
		s.mu.Lock()
		changed := u.MessageID != "" && s.messageID != "" && u.MessageID != s.messageID
		s.mu.Unlock()
		if changed {
			s.flush()
		}
		s.mu.Lock()
		s.messageID = u.MessageID
		s.text.WriteString(c.Text)
		s.mu.Unlock()
		if s.t.Spec.Stream {
			s.t.write(c.Text) // the draft, until the whole message is flushed
		}
	case "tool_call", "tool_call_update":
		s.flush()
		tc := u.acpToolCall
		s.mu.Lock()
		name, known := s.tools[tc.ToolCallID]
		if !known {
			name = s.toolName(tc.Title, tc.Kind)
			s.tools[tc.ToolCallID] = name
		}
		if len(tc.RawInput) > 0 {
			s.inputs[tc.ToolCallID] = tc.RawInput
		}
		// The input is complete once the call runs, not while it is pending.
		report := tc.Status != "" && tc.Status != "pending" && !s.emitted[tc.ToolCallID]
		if report {
			s.emitted[tc.ToolCallID] = true
		}
		s.mu.Unlock()
		if report {
			s.t.emit("tool_use", map[string]any{"name": name, "input": acpInput(name, tc)})
		}
	}
}

// flush reports the agent's message streamed so far.
func (s *acpSession) flush() {
	s.mu.Lock()
	text := s.text.String()
	s.text.Reset()
	s.messageID = ""
	if text != "" {
		s.last = text
	}
	s.mu.Unlock()
	if strings.TrimSpace(text) != "" {
		s.t.emit("text", map[string]any{"text": text})
	}
}

// serve answers a request of the agent: a permission, through Task.decide,
// or an error for what the node does not offer.
func (s *acpSession) serve(m rpcMessage) {
	if m.Method != "session/request_permission" {
		s.write(rpcMessage{ID: m.ID, Error: &rpcError{Code: -32601, Message: "Method not found: " + m.Method}})
		return
	}
	var p struct {
		ToolCall acpToolCall `json:"toolCall"`
		Options  []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.write(rpcMessage{ID: m.ID, Error: &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()}})
		return
	}
	s.flush()
	tc := p.ToolCall
	if len(tc.RawInput) == 0 {
		// OpenCode asks for an MCP tool without its input: the tool call
		// that runs it carries it, and may come just after the request. The
		// policy and whoever answers must see what the call does.
		tc.RawInput = s.inputFor(tc.ToolCallID)
	}
	s.mu.Lock()
	name, ok := s.tools[tc.ToolCallID]
	s.mu.Unlock()
	if !ok {
		name = s.toolName(tc.Title, tc.Kind)
	}
	a := s.t.decide(s.ctx, name, acpInput(name, tc))
	outcome := map[string]any{"outcome": "cancelled"}
	if s.ctx.Err() == nil {
		// Never an "always" option: every call is asked again.
		want := "reject_once"
		if a.Allow {
			want = "allow_once"
		}
		for _, o := range p.Options {
			if o.Kind == want {
				outcome = map[string]any{"outcome": "selected", "optionId": o.OptionID}
				break
			}
		}
		if !a.Allow && a.Message != "" {
			s.mu.Lock()
			s.denials = append(s.denials, name+" "+subjectOf(acpInput(name, tc))+": "+a.Message)
			s.mu.Unlock()
		}
	}
	raw, _ := json.Marshal(map[string]any{"outcome": outcome})
	s.write(rpcMessage{ID: m.ID, Result: raw})
}

// acpTools names an agent's tools as Claude Code does, so that eriphe-core's
// policy reads them the same way. OpenCode's tool calls start titled with
// the tool's name; other agents give a kind.
var acpTools = map[string]string{
	"bash": "Bash", "shell": "Bash",
	"edit": "Edit", "multiedit": "MultiEdit", "patch": "Edit", "apply_patch": "Edit",
	"write": "Write", "read": "Read",
	"webfetch": "WebFetch", "websearch": "WebSearch",
	"glob": "Glob", "grep": "Grep", "list": "LS",
	"todowrite": "TodoWrite", "todoread": "TodoRead", "task": "Task",
}

var acpKinds = map[string]string{
	"execute": "Bash", "edit": "Edit", "delete": "Edit", "move": "Edit",
	"read": "Read", "search": "Grep", "fetch": "WebFetch",
}

// toolName names a tool call as Claude Code would: an MCP tool, which
// OpenCode titles <server>_<tool> with both names reduced to
// [a-zA-Z0-9_-], becomes mcp__<server>__<tool>, as the policy knows it.
func (s *acpSession) toolName(title, kind string) string {
	title = strings.TrimSpace(title)
	best := ""
	for _, srv := range s.servers {
		if prefix := opencodeName(srv.Name) + "_"; strings.HasPrefix(title, prefix) && len(title) > len(prefix) &&
			len(srv.Name) > len(best) {
			best = srv.Name
		}
	}
	if best != "" {
		return "mcp__" + best + "__" + title[len(opencodeName(best))+1:]
	}
	return acpToolName(title, kind)
}

// opencodeName is a name as OpenCode reduces it in its tools' names.
func opencodeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

func acpToolName(title, kind string) string {
	if name, ok := acpTools[strings.ToLower(strings.TrimSpace(title))]; ok {
		return name
	}
	if name, ok := acpKinds[kind]; ok {
		return name
	}
	if title != "" && !strings.ContainsAny(title, " /") {
		return title
	}
	if kind != "" {
		return kind
	}
	return "other"
}

// acpInput is a tool call's input with what the policy matches on under
// Claude Code's keys: command, file_path, url.
func acpInput(name string, tc acpToolCall) map[string]any {
	in := map[string]any{}
	for k, v := range tc.RawInput {
		in[k] = v
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			switch v := tc.RawInput[k].(type) {
			case string:
				if v != "" {
					return v
				}
			case []any:
				parts := make([]string, 0, len(v))
				for _, p := range v {
					parts = append(parts, fmt.Sprint(p))
				}
				return strings.Join(parts, " ")
			}
		}
		return ""
	}
	location := ""
	if len(tc.Locations) > 0 {
		location = tc.Locations[0].Path
	}
	switch name {
	case "Bash":
		if c := str("command", "cmd"); c != "" {
			in["command"] = c
		} else if tc.Title != "" {
			in["command"] = tc.Title
		}
	case "Edit", "MultiEdit", "Write", "Read":
		if p := str("file_path", "filePath", "filepath", "path"); p != "" {
			in["file_path"] = p
		} else if location != "" {
			in["file_path"] = location
		}
	case "WebFetch":
		if u := str("url"); u != "" {
			in["url"] = u
		} else if tc.Title != "" {
			in["url"] = tc.Title
		}
	default:
		if _, ok := in["path"]; !ok && location != "" {
			in["path"] = location
		}
	}
	return in
}

// subjectOf is what a tool acts on, for a short line.
func subjectOf(in map[string]any) string {
	for _, k := range []string{"command", "file_path", "url", "path"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	raw, _ := json.Marshal(in)
	return string(bytes.TrimSpace(raw))
}

// acpInputWait is how long a permission request without its input waits for
// the tool call that carries it.
var acpInputWait = 2 * time.Second

// inputFor is a tool call's input once its update has brought it, or nil
// after acpInputWait.
func (s *acpSession) inputFor(id string) map[string]any {
	for deadline := time.Now().Add(acpInputWait); ; time.Sleep(20 * time.Millisecond) {
		s.mu.Lock()
		in := s.inputs[id]
		s.mu.Unlock()
		if len(in) > 0 || time.Now().After(deadline) || s.ctx.Err() != nil {
			return in
		}
	}
}
