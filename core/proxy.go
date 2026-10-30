package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The per-task proxy. A task's MCP configuration names, for every catalog
// server it was granted, its own node (callback-url/mcp/<server>, with the
// task's secret); the node relays to eriphe-core with its own secret and the
// task's id:
//
//	/v1/node/tasks/{id}/mcp/{server}   (node secret)
//
// eriphe-core serves there an MCP server made of the upstream's cached
// tools that the task's grant covers, and forwards each call upstream with
// the real credential. A call must also have been allowed through the
// permission policy first (a ticket, one per allowed request): an agent that
// found its task's secret cannot call a tool behind its agent's back.

type proxyServerKey struct{}

// taskMCPHandler serves the per-task MCP endpoints, for nodes.
func (c *core) taskMCPHandler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		s, _ := r.Context().Value(proxyServerKey{}).(*mcp.Server)
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node, ok := c.nodeFromRequest(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		t, err := c.store.task(r.PathValue("id"))
		if err != nil || t.Node != node || finished(t.State) {
			http.Error(w, "no such task on this node", http.StatusNotFound)
			return
		}
		server := r.PathValue("server")
		if server == selfServer {
			// eriphe-core's own tools, for an assistant whose role launches tasks,
			// without self_url: what the bearer token gives at /mcp.
			if a, r, ok := c.assistantOf(t); !ok || !r.can(powerLaunchesTasks) || a.SelfURL != "" || c.self == nil {
				http.Error(w, "this task has no access to "+server, http.StatusForbidden)
				return
			}
			h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), proxyServerKey{}, c.self)))
			return
		}
		grant := c.taskGrant(t)
		if !slices.Contains(grantServers(grant), server) || c.pool.get(server) == nil {
			http.Error(w, "this task has no access to "+server, http.StatusForbidden)
			return
		}
		s := c.taskServer(t.ID, grant, server)
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), proxyServerKey{}, s)))
	})
}

// taskGrant is what a task may use of the catalog: an assistant's comes
// from its spec and the catalog (assistantGrant), a worker's from run_task.
func (c *core) taskGrant(t *TaskRow) []string {
	if a, _, ok := c.assistantOf(t); ok {
		return c.assistantGrant(a)
	}
	if isRoleKind(t.Kind) {
		return nil // an assistant's task none owns any more
	}
	return splitGrant(t.Tools)
}

// brainGrant is what the brain may use of the catalog (assistantGrant).
func (c *core) brainGrant() []string {
	b := c.conf().brain()
	if b == nil {
		return nil
	}
	return c.assistantGrant(*b)
}

type catalogTool struct{ server, tool string }

// freeTools are the tools Eriphe may call without the user, as the pool
// lists them now: the read-only ones of the servers trusted for their
// annotations, and every tool of a server whose access is free.
func (c *core) freeTools() []catalogTool {
	var out []catalogTool
	for name := range c.conf().Servers {
		u := c.pool.get(name)
		if u == nil || !u.srv.trusts() {
			continue
		}
		_, _, tools := u.snapshot()
		for _, t := range tools {
			if u.srv.level() == accessFree || (t.Annotations != nil && t.Annotations.ReadOnlyHint) {
				out = append(out, catalogTool{name, t.Name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].server+"."+out[i].tool < out[j].server+"."+out[j].tool
	})
	return out
}

// globEscape makes a tool name match only itself in a grant.
func globEscape(s string) string { return globChars.ReplaceAllString(s, `\$0`) }

var globChars = regexp.MustCompile(`[\\*?\[]`)

// freeRead tells whether the brain may call a catalog tool without asking:
// free by its server's access level (a read of a server believed for it,
// or any tool of a free one), and named by no rule of my policy (such a
// tool goes through the permission flow, with its input).
func (c *core) freeRead(claudeName string) bool {
	return c.pool.facts(claudeName).free() && !c.conf().Policy.names(claudeName)
}

// readsFreely tells whether a task is an assistant's whose role reads freely.
func (c *core) readsFreely(taskID string) bool {
	n := c.assistantOwning(taskID)
	if n == "" {
		return false
	}
	a, ok := c.conf().Assistants[n]
	return ok && assistantRoles[a.Role].FreeReads
}

// brainFreeReads are the Claude Code names of the free reads: the node of
// an assistant that reads freely allows them in advance, so they never
// wait for a permission.
func (c *core) brainFreeReads() []string {
	var out []string
	for _, r := range c.freeTools() {
		if name := claudeToolName(r.server, r.tool); c.freeRead(name) && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// actingGrant is what a grant lends beyond believed reads: an entry
// covering a tool that is not read-only by a believed annotation, or no
// tool at all (nothing vouches for it).
func (c *core) actingGrant(grant []string) []string {
	var out []string
	for _, g := range grant {
		g = strings.TrimSpace(g)
		server, _, _ := strings.Cut(g, ".")
		var tools []*mcp.Tool
		var srv Server
		if u := c.pool.get(server); u != nil {
			_, _, tools = u.snapshot()
			srv = u.srv
		}
		covered, acts := 0, false
		for _, t := range tools {
			if grantAllows([]string{g}, server, t.Name) && lends(srv, t) {
				covered++
				acts = acts || !c.pool.readOnly(claudeToolName(server, t.Name))
			}
		}
		if acts || covered == 0 {
			out = append(out, g)
		}
	}
	return out
}

// specServers are the catalog servers a task's node must relay for it.
func (c *core) specServers(t *TaskRow) []string {
	var out []string
	for _, s := range grantServers(c.taskGrant(t)) {
		if _, ok := c.conf().Servers[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// taskServer is the MCP server one task sees of one catalog server: the
// cached tools its grant covers, with their own input schemas.
func (c *core) taskServer(taskID string, grant []string, server string) *mcp.Server {
	u := c.pool.get(server)
	s := mcp.NewServer(&mcp.Implementation{Name: server, Version: version}, nil)
	if u == nil {
		return s
	}
	_, _, tools := u.snapshot()
	for _, tl := range tools {
		if !grantAllows(grant, server, tl.Name) || !lends(u.srv, tl) || !objectSchema(tl.InputSchema) {
			continue
		}
		cp := *tl
		name := tl.Name
		s.AddTool(&cp, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return c.proxyCall(ctx, taskID, grant, u, name, req.Params.Arguments), nil
		})
	}
	return s
}

// objectSchema tells whether an input schema is an object, as the SDK
// requires of a tool: a server's odd tool is left out rather than crash.
func objectSchema(schema any) bool {
	raw, err := json.Marshal(schema)
	if err != nil {
		return false
	}
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && m["type"] == "object"
}

// proxyCall forwards one call of a task upstream, once it is granted and
// allowed. Failures come back as tool errors, which the agent reads.
func (c *core) proxyCall(ctx context.Context, taskID string, grant []string, u *upstream, tool string, args json.RawMessage) *mcp.CallToolResult {
	if !grantAllows(grant, u.name, tool) {
		return toolError("this tool was not lent to the task")
	}
	var input map[string]any
	if len(args) > 0 && json.Unmarshal(args, &input) != nil {
		return toolError("arguments must be a JSON object")
	}
	claude := claudeToolName(u.name, tool)
	f := c.pool.facts(claude)
	switch {
	case f.level == accessReadOnly && !f.read:
		return toolError("this tool is read only in Eriphe: only its reads are lent")
	case c.store.useTicket(taskID, claude, input):
	case c.readsFreely(taskID) && c.freeRead(claude):
		// The brain reads freely: no permission round trip, a decision on record all the same.
		why := "lecture libre du cerveau"
		if !f.read {
			why = "outil en accès libre"
		}
		c.store.logDecision(taskID, "", claude, input, decideAllow, byPolicy, why)
	default:
		return toolError("this call was not allowed through Eriphe's permissions: ask for it as usual")
	}
	c.brainCalledCatalog(taskID, u.name, tool) // what the brain reads taints its turn's memory writes
	start := time.Now()
	res, err := u.call(ctx, tool, args)
	log.Printf("task %s: %s.%s %dms err=%v", taskID, u.name, tool, time.Since(start).Milliseconds(), err != nil)
	if err != nil {
		return toolError(err.Error())
	}
	return res
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// Tickets: an allowed permission request for a catalog tool lets one call
// with that exact input through, within 30 minutes.
const ticketSchema = `CREATE TABLE IF NOT EXISTS tickets (
  task_id TEXT NOT NULL, tool TEXT NOT NULL, input_hash TEXT NOT NULL, time INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, ticketSchema) }

const ticketTTL = 30 * time.Minute

// inputHash identifies an input by its canonical JSON (sorted keys).
func inputHash(input map[string]any) string {
	if input == nil {
		input = map[string]any{}
	}
	raw, _ := json.Marshal(input)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// normalizeToolName is a tool name as Claude Code would write it.
func normalizeToolName(tool string) string {
	if server, name, ok := splitToolName(tool); ok {
		return claudeToolName(server, name)
	}
	return tool
}

func (s *Store) addTicket(taskID, tool string, input map[string]any) {
	s.db.Exec(`DELETE FROM tickets WHERE time < ?`, time.Now().Add(-ticketTTL).Unix())
	s.db.Exec(`INSERT INTO tickets (task_id, tool, input_hash, time) VALUES (?, ?, ?, ?)`,
		taskID, normalizeToolName(tool), inputHash(input), time.Now().Unix())
}

// useTicket consumes the ticket of a call: false if there is none.
func (s *Store) useTicket(taskID, tool string, input map[string]any) bool {
	res, err := s.db.Exec(`DELETE FROM tickets WHERE rowid = (SELECT rowid FROM tickets
		WHERE task_id = ? AND tool = ? AND input_hash = ? AND time >= ? ORDER BY time LIMIT 1)`,
		taskID, normalizeToolName(tool), inputHash(input), time.Now().Add(-ticketTTL).Unix())
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// serverInfo is what the CLI and the brain see of a catalog server.
type serverInfo struct {
	Name             string     `json:"name"`
	Use              string     `json:"use"`
	Kind             string     `json:"kind"`   // remote or local
	Source           string     `json:"source"` // config (the file) or workspace
	URL              string     `json:"url,omitempty"`
	Auth             string     `json:"auth"`   // bearer, oauth, none or local
	Access           string     `json:"access"` // what Eriphe may do on it (access.go)
	TrustAnnotations bool       `json:"trust_annotations,omitempty"`
	Status           string     `json:"status"`
	Error            string     `json:"error,omitempty"`
	Connect          string     `json:"connect,omitempty"` // what the user runs when it needs an authorization
	RedirectURI      string     `json:"redirect_uri,omitempty"`
	ToolCount        int        `json:"tool_count"`
	Tools            []toolInfo `json:"tools,omitempty"`
}

type toolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"` // by the server's own annotation
}

// listServers describes the catalog, or one server of it; withTools adds
// each tool with a short description.
func (c *core) listServers(only string, withTools bool) []serverInfo {
	out := []serverInfo{}
	for name, s := range c.conf().Servers {
		if only != "" && name != only {
			continue
		}
		info := serverInfo{Name: name, Use: s.Use, Kind: "remote", URL: s.URL, Auth: s.authKind(),
			Access: s.level(), TrustAnnotations: s.trusts(), Status: statusConnecting, Source: sourceWorkspace}
		if _, ok := c.base.Servers[name]; ok {
			info.Source = sourceConfig
		}
		if s.local() {
			info.Kind = "local"
		}
		if o := oauthOf(s); o != nil {
			info.RedirectURI = o.RedirectURI
			// The CLI's hint: a loopback redirect only (not the workspace's callback).
			if reg := c.store.oauthClient(name); info.RedirectURI == "" && reg != nil && loopbackRedirect(reg.RedirectURI) {
				info.RedirectURI = reg.RedirectURI
			}
		}
		if u := c.pool.get(name); u != nil {
			status, errText, tools := u.snapshot()
			// Read only: its reads alone are shown, as they alone are lent.
			tools = slices.DeleteFunc(slices.Clone(tools), func(t *mcp.Tool) bool { return !lends(s, t) })
			info.Status, info.Error, info.ToolCount = status, truncate(errText, 300), len(tools)
			if withTools {
				for _, t := range tools {
					ti := toolInfo{Name: t.Name, Description: truncate(oneLine(t.Description), 160)}
					ti.ReadOnly = t.Annotations != nil && t.Annotations.ReadOnlyHint
					info.Tools = append(info.Tools, ti)
				}
			}
		}
		if info.Status == statusNeedsAuth {
			info.Connect = "eriphe connect " + name
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func oauthOf(s Server) *OAuthConfig {
	if s.Auth == nil {
		return nil
	}
	return s.Auth.OAuth
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
