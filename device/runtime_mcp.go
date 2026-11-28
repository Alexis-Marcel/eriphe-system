package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// mcpServer is one MCP server a task's agent is given, over HTTP or stdio,
// whatever its runtime.
type mcpServer struct {
	Name    string
	URL     string // over HTTP
	Headers map[string]string
	Command string // over stdio
	Args    []string
	Env     map[string]string
}

// mcpServersFor are the MCP servers of a task as every runtime gets them,
// sorted by name: the spec's own (the brain's eriphe-core) and the catalog
// servers, which point at the device with the task's secret, never at the
// server itself, whose credential stays with eriphe-core. Claude Code also
// gets the device's eriphe_device, its --permission-prompt-tool: the other
// runtimes ask through their own protocol.
//
// A server over stdio (one that runs in the task, eriphe-core's TaskTool) is
// also given env, the task's way out (egressEnv), under its own variables:
// some agents start it with a few variables of theirs only (Codex), and it
// would have no route out of the container without them.
func mcpServersFor(callback string, t *Task, env map[string]string) ([]mcpServer, error) {
	spec := t.Spec
	var out []mcpServer
	for name, def := range spec.MCPServers {
		s, err := parseMCPServer(name, def, spec.AgentEnv)
		if err != nil {
			return nil, err
		}
		if s.Command != "" {
			s.Env = withEnv(s.Env, env)
		}
		out = append(out, s)
	}
	for _, name := range spec.Servers {
		out = slices.DeleteFunc(out, func(s mcpServer) bool { return s.Name == name })
		out = append(out, mcpServer{Name: name, URL: callback + "/mcp/" + name,
			Headers: map[string]string{"Authorization": "Bearer " + t.secret}})
	}
	slices.SortFunc(out, func(a, b mcpServer) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// withEnv is a stdio server's own variables over base's.
func withEnv(own, base map[string]string) map[string]string {
	if len(base) == 0 {
		return own
	}
	out := maps.Clone(base)
	maps.Copy(out, own)
	return out
}

// stdioDef is a spec's server as Claude Code's mcp.json takes it: a stdio
// one (a command) with env under its own variables (mcpServersFor).
func stdioDef(def any, env map[string]string) any {
	m, ok := def.(map[string]any)
	if !ok || m["command"] == nil || len(env) == 0 {
		return def
	}
	out := maps.Clone(m)
	own := map[string]string{}
	if e, ok := m["env"].(map[string]any); ok {
		for k, v := range e {
			own[k] = fmt.Sprint(v)
		}
	}
	if e, ok := m["env"].(map[string]string); ok {
		maps.Copy(own, e)
	}
	out["env"] = withEnv(own, env)
	return out
}

// parseMCPServer reads a server of the spec, in .mcp.json format. A
// ${NAME} in its address or headers is read from the task's identity, as
// Claude Code reads it from its environment.
func parseMCPServer(name string, def any, identity map[string]string) (mcpServer, error) {
	m, _ := def.(map[string]any)
	expand := func(v any) string {
		s, _ := v.(string)
		return envRef.ReplaceAllStringFunc(s, func(ref string) string {
			if v, ok := identity[ref[2:len(ref)-1]]; ok {
				return v
			}
			return ref
		})
	}
	strs := func(v any) map[string]string {
		in, _ := v.(map[string]any)
		out := map[string]string{}
		for k, v := range in {
			out[k] = expand(v)
		}
		return out
	}
	s := mcpServer{Name: name}
	typ, _ := m["type"].(string)
	switch {
	case typ == "http" || typ == "" && m["url"] != nil:
		s.URL, s.Headers = expand(m["url"]), strs(m["headers"])
	case typ == "stdio" || typ == "" && m["command"] != nil:
		s.Command, s.Env = expand(m["command"]), strs(m["env"])
		args, _ := m["args"].([]any)
		for _, a := range args {
			s.Args = append(s.Args, expand(a))
		}
	default:
		return s, fmt.Errorf("mcp server %s: transport %q is not supported (http or stdio)", name, typ)
	}
	if s.URL == "" && s.Command == "" {
		return s, fmt.Errorf("mcp server %s: no url nor command", name)
	}
	return s, nil
}

var envRef = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

// allowedMCP reads the spec's allowed tools that are MCP tools, as Claude
// Code names them: mcp__<server> for all of a server's tools, or
// mcp__<server>__<tool> for one. Others are not about MCP.
func allowedMCP(allowed []string) (servers map[string]bool, tools map[string][]string) {
	servers, tools = map[string]bool{}, map[string][]string{}
	for _, a := range allowed {
		rest, ok := strings.CutPrefix(strings.TrimSpace(a), "mcp__")
		if !ok || rest == "" {
			continue
		}
		server, tool, _ := strings.Cut(rest, "__")
		if tool == "" || tool == "*" {
			servers[server] = true
		} else {
			tools[server] = append(tools[server], tool)
		}
	}
	return servers, tools
}

// agentInstructions is where the agents that take no system prompt of
// their own (OpenCode) find the task's: written to the container's /tmp
// tmpfs at start, never to the device's disk.
const agentInstructions = "/tmp/eriphe/instructions.md"

// instructionsEnv passes the system prompt to the start script that writes
// it: by name, so it never appears on a command line, and unset before the
// agent starts.
const instructionsEnv = "ERIPHE_INSTRUCTIONS"
