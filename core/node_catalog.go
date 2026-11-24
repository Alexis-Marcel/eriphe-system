package main

import (
	"context"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The catalog of a node's blocks, for whoever writes one (#632, #706):
// catalog_read gives the whole grammar of a block (a2ui.CatalogPrompt's
// components, then nodeGrammar: the block's envelope, its live sources,
// their items and how one opens, the glance, the sections' filters and
// the fields code writes on each item), sources_read what a live source
// may read and gives. Both only read what code holds, the same for every
// caller: the brain has them among its own tools (newMCPServer), and
// every worker task with a node beside its node's tools (eriphe-nodes,
// task_nodes.go), never lent: whoever writes a node has the grammar it
// is written in.

// registerCatalogTools adds the catalog's tools to an MCP server.
func (c *core) registerCatalogTools(s *mcp.Server) {
	tool(s, "catalog_read", "The whole grammar of a node's block, in French: its components with their properties and examples, its live sources (items, open, every, glance), the sections' filters and the fields code writes on each item.",
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			return map[string]any{"catalog": a2ui.CatalogPrompt() + "\n" + nodeGrammar, "id": a2ui.Eriphe().ID()}, nil
		})
	tool(s, "sources_read", sourcesReadHelp,
		func(ctx context.Context, in sourcesIn) (map[string]any, error) { return c.toolSourcesRead(in) })
}
