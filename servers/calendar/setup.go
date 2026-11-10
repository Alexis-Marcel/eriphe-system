package main

import (
	"context"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Setup tools, kept for eriphe-core's workspace (servers/README.md): marked
// eriphe/setup, they are left out of the server's tools, so no task and not
// the brain ever get them.

var setupMeta = mcp.Meta{"eriphe/setup": true}

type testAccountIn struct {
	Account string `json:"account,omitempty" jsonschema:"the account to try, by its name; may be left out with one account"`
}

// testAccountOut says whether the account signs in; Error is why not, never
// its secret.
type testAccountOut struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// addTestAccount serves test_account: try signs the account in as the
// server will use it.
func addTestAccount(s *mcp.Server, try func(ctx context.Context, account string) error) {
	mcp.AddTool(s, &mcp.Tool{Name: "test_account", Meta: setupMeta,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
		Description: "Setup only, for the workspace: sign one account in as this server uses it, and say whether it works."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testAccountIn) (*mcp.CallToolResult, testAccountOut, error) {
			start := time.Now()
			err := try(ctx, in.Account)
			log.Printf("tool=test_account ms=%d ok=%t", time.Since(start).Milliseconds(), err == nil)
			if err != nil {
				return nil, testAccountOut{Error: err.Error()}, nil
			}
			return nil, testAccountOut{OK: true}, nil
		})
}
