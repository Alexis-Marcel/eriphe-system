package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callOn calls a tool of an MCP session: its structured result, or the
// error text it gave.
func callOn(t *testing.T, sess *mcp.ClientSession, name string, args any) (map[string]any, string) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		var b strings.Builder
		for _, c := range res.Content {
			if x, ok := c.(*mcp.TextContent); ok {
				b.WriteString(x.Text)
			}
		}
		return nil, b.String()
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(raw, &out)
	return out, ""
}

func connectServer(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// boundText is a block whose one Text says what is at path.
func boundText(path string) []map[string]any {
	return []map[string]any{{"id": "root", "component": "Text", "text": map[string]any{"@path": path}}}
}

// The brain writes no surface (#631: views are nodes): no surface_write
// nor surface_remove; the catalog in full on demand, and its section
// stays out of its tools' descriptions and instructions.
func TestWorkspaceToolsThroughMCP(t *testing.T) {
	c, _ := ledgerCore(t)
	brain := connectServer(t, newMCPServer(c))

	tools, err := brain.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	full := a2ui.CatalogPrompt()
	for _, x := range tools.Tools {
		if strings.Contains(x.Description, full[:200]) {
			t.Errorf("%s carries the catalog's section", x.Name)
		}
		if x.Name == "surface_write" || x.Name == "surface_remove" {
			t.Errorf("the brain still has %s", x.Name)
		}
	}
	if strings.Contains(instructions, "Metric") {
		t.Error("the brain's instructions carry the catalog")
	}
	got, _ := callOn(t, brain, "catalog_read", map[string]any{})
	if got["catalog"] != full+"\n"+nodeGrammar {
		t.Fatalf("catalog_read: %v", got)
	}
}

// taskEndpoint opens a task's MCP server as its device relays it.
func taskEndpoint(t *testing.T, c *core, task, server string, secret ...string) (*mcp.ClientSession, error) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/v1/device/tasks/{id}/mcp/{server}", c.taskMCPHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	bearer := "device-secret"
	if len(secret) > 0 {
		bearer = secret[0]
	}
	hc := &http.Client{Transport: headerTransport{"Authorization": "Bearer " + bearer, "Eriphe-Device": "pc"}}
	tr := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/v1/device/tasks/" + task + "/mcp/" + server, HTTPClient: hc, MaxRetries: -1}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "relay"}, nil).Connect(context.Background(), tr, nil)
	if err == nil {
		t.Cleanup(func() { sess.Close() })
	}
	return sess, err
}

// Every worker with a node has the catalog beside its node's tools,
// allowed in advance, never lent: eriphe-workspace is no server, a task is
// not lent it, and list_tools does not list it.
func TestWorkerHasTheCatalog(t *testing.T) {
	c := nodeCore(t)
	task, _ := startInNode(t, c, runRequest{Title: "Une page", Prompt: "Compose la page."})
	got := mustNodes(t, c.core, task.ID, "catalog_read", nil)
	if got["catalog"] != a2ui.CatalogPrompt()+"\n"+nodeGrammar {
		t.Fatalf("catalog_read: %v", got)
	}
	mustNodes(t, c.core, task.ID, "sources_read", nil)
	c.store.registerDevice(task.Device, hashSecret("x"))
	spec := assignedSpec(t, c.core, task.Device, task.ID)
	for _, name := range []string{"mcp__eriphe-nodes__catalog_read", "mcp__eriphe-nodes__sources_read"} {
		if !slices.Contains(spec.AllowedTools, name) {
			t.Errorf("%s not allowed in advance: %v", name, spec.AllowedTools)
		}
	}
	if _, err := c.runTask(context.Background(), runRequest{Prompt: "x", Resources: []ResourceRef{{Kind: resTool, Ref: "eriphe-workspace"}}}); err == nil {
		t.Error("eriphe-workspace was lent")
	}
	listed, _ := callOn(t, connectServer(t, newMCPServer(c.core)), "list_tools", map[string]any{})
	if raw, _ := json.Marshal(listed); strings.Contains(string(raw), "eriphe-workspace") {
		t.Fatalf("list_tools: %s", raw)
	}
}
