package main

import (
	"fmt"
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
// servers, which point at the node with the task's secret, never at the
// server itself, whose credential stays with eriphe-core. Claude Code also
// gets the node's eriphe_node, its --permission-prompt-tool: the other
// runtimes ask through their own protocol.
func mcpServersFor(callback string, t *Task) ([]mcpServer, error) {
	spec := t.Spec
	var out []mcpServer
	for name, def := range spec.MCPServers {
		s, err := parseMCPServer(name, def, spec.Identity)
		if err != nil {
			return nil, err
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
// tmpfs at start, never to the node's disk.
const agentInstructions = "/tmp/eriphe/instructions.md"

// instructionsEnv passes the system prompt to the start script that writes
// it: by name, so it never appears on a command line, and unset before the
// agent starts.
const instructionsEnv = "ERIPHE_INSTRUCTIONS"
