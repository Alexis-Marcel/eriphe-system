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

// boundText is a surface whose one Text says what is at path.
func boundText(path string) []map[string]any {
	return []map[string]any{{"id": "root", "component": "Text", "text": map[string]any{"@path": path}}}
}

// The brain writes no surface (#631: views are nodes): no surface_write
// nor surface_remove; the catalog in full on demand, and its section
// stays out of its tools' descriptions and instructions.
func TestWorkspaceToolsThroughMCP(t *testing.T) {
	c, _ := surfaceCore(t)
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
	if got["catalog"] != full {
		t.Fatalf("catalog_read: %v", got)
	}
	if all, _ := c.surfaces(); len(all) != 0 {
		t.Fatalf("written: %+v", all)
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

// The workspace reaches a task through its grant only, like any tool: a
// task not lent it does not reach it; one lent a tool of it sees that one,
// its device relays it and allows it in advance; skills lend it by name.
func TestWorkspaceGrantPath(t *testing.T) {
	c, _ := surfaceCore(t)
	c.store.registerDevice("pc", hashSecret("device-secret"))
	c.store.setDeviceStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	c.store.insertTask(&TaskRow{ID: "t-none", Device: "pc", Runtime: "claude-code", State: stateRunning, Tools: "ledger"})
	if _, err := taskEndpoint(t, c, "t-none", workspaceServer); err == nil {
		t.Fatal("a task without the grant reached the workspace")
	}

	ctx := context.Background()
	if _, err := c.runTask(ctx, runRequest{Prompt: "x", Tools: []string{"nowhere"}}); err == nil {
		t.Fatal("an unknown server was granted")
	}
	task, err := c.runTask(ctx, runRequest{Prompt: "lis l'espace", Resources: []ResourceRef{{Kind: resTool, Ref: workspaceServer + ".catalog_read"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.store.updateTask(task.ID, map[string]any{"device": "pc", "state": stateRunning})
	sess, err := taskEndpoint(t, c, task.ID, workspaceServer)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sess.ListTools(ctx, nil)
	if err != nil || len(res.Tools) != 1 || res.Tools[0].Name != "catalog_read" {
		t.Fatalf("tools %+v %v", res, err)
	}

	req := httptest.NewRequest("GET", "/v1/device/assignments?rev=0&wait=0", nil)
	req.Header.Set("Authorization", "Bearer device-secret")
	req.Header.Set("Eriphe-Device", "pc")
	w := httptest.NewRecorder()
	c.handleAssignments(w, req)
	var out struct {
		Tasks []deviceAssignment `json:"tasks"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	found := false
	for _, a := range out.Tasks {
		if a.ID == task.ID {
			found = true
			if !slices.Contains(a.Spec.Servers, workspaceServer) || !slices.Contains(a.Spec.AllowedTools, "mcp__eriphe-workspace__catalog_read") ||
				slices.Contains(a.Spec.AllowedTools, "mcp__eriphe-workspace__surface_write") {
				t.Fatalf("spec %+v", a.Spec)
			}
		}
	}
	if !found {
		t.Fatalf("assignments %s", w.Body.Bytes())
	}

	listed, _ := callOn(t, connectServer(t, newMCPServer(c)), "list_tools", map[string]any{})
	if raw, _ := json.Marshal(listed["provided"]); !strings.Contains(string(raw), workspaceServer) || !strings.Contains(string(raw), "catalog_read") {
		t.Fatalf("list_tools: %s", raw)
	}
}
