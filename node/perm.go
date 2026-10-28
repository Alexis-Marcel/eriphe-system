package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// permHandler serves one MCP endpoint per task, /perm/{id}/mcp. The agent in
// the task's container calls its "approve" tool for every permission request
// (--permission-prompt-tool), authenticated with the task's own secret.
func (n *node) permHandler() http.Handler {
	type req struct {
		ToolName  string         `json:"tool_name"`
		Input     map[string]any `json:"input"`
		ToolUseID string         `json:"tool_use_id,omitempty"`
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		t := n.get(r.PathValue("id"))
		s := mcp.NewServer(&mcp.Implementation{Name: "eriphe-node", Version: "0"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "approve", Description: "Ask Eriphe whether a tool call may run"},
			func(ctx context.Context, _ *mcp.CallToolRequest, in req) (*mcp.CallToolResult, any, error) {
				a := t.decide(ctx, in.ToolName, in.Input)
				decision := map[string]any{"behavior": "allow", "updatedInput": in.Input}
				if !a.Allow {
					msg := a.Message
					if msg == "" {
						msg = "Denied."
					}
					decision = map[string]any{"behavior": "deny", "message": msg}
				}
				// Claude Code reads the decision as JSON text, not as structured content.
				raw, _ := json.Marshal(decision)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
			})
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := n.get(r.PathValue("id"))
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if t == nil || subtle.ConstantTimeCompare([]byte(got), []byte(t.secret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
}
