package main

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// How a node's page is written (#786), for whoever writes one: the
// format sheet (page-prompt.md, in French) says what a page holds as
// nodefile reads and checks it (docs/node-format.md): its lines and
// items, references and their targets, where cards and actions go
// (card:, do:, ask:), lines that follow the journal (on:) and callouts.
// It is read on demand, never in a prompt: the brain has page_format
// among its own tools (newMCPServer), and every worker task with a node
// beside its node's tools (eriphe-nodes, task_nodes.go), never lent. Its
// examples are pages the check finds nothing in (TestPageFormatExamples).

//go:embed page-prompt.md
var pageFormat string

// registerPageFormat adds page_format to an MCP server.
func registerPageFormat(s *mcp.Server) {
	tool(s, "page_format", "How a node's page is written, in French: a Markdown page whose file holds all it shows; items, references to a tool's items, cards, actions on a heading's items, probable replies, lines that follow events, callouts, line edits. Read it before writing a page.",
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			return map[string]any{"format": pageFormat}, nil
		})
}
