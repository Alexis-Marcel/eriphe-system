package main

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Cloud storage as a source of places: eriphe-files (servers/files), a
// local server like eriphe-mail with a card like it (tool_cards.go), serves
// an account's folders to tasks. eriphe-core runs the
// sign-in (oauth_client.go), keeps the refresh token in the vault and lends
// it to the files service on its node, like a password: it never enters a
// task. The app the sign-in uses is the user's own, kept in the vault
// (connection_apps.go); its places are picked on the tool's page
// (places.go, web_places.go).

func resultText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// setupTool tells a tool a server keeps for eriphe-core's workspace
// (servers/README.md: browse_places, which sees every folder name of the
// account, and test_account): the pool leaves it out of the server's tools,
// so no grant, no task and not the brain ever get it; only browsePlaces and
// testAccount call them.
func setupTool(t *mcp.Tool) bool { return t.Meta["eriphe/setup"] == true }
