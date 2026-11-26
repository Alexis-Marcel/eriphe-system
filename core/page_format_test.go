package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
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
// nor surface_remove, no catalog_read nor sources_read (#786: a page is
// Markdown); page_format gives the format sheet on demand, and it stays
// out of its tools' descriptions and instructions.
func TestWorkspaceToolsThroughMCP(t *testing.T) {
	c, _ := ledgerCore(t)
	brain := connectServer(t, newMCPServer(c))

	tools, err := brain.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range tools.Tools {
		if strings.Contains(x.Description, pageFormat[:200]) {
			t.Errorf("%s carries the format sheet", x.Name)
		}
		switch x.Name {
		case "surface_write", "surface_remove", "catalog_read", "sources_read":
			t.Errorf("the brain still has %s", x.Name)
		}
	}
	if strings.Contains(instructions, "[!figures]") {
		t.Error("the brain's instructions carry the format sheet")
	}
	got, _ := callOn(t, brain, "page_format", map[string]any{})
	if got["format"] != pageFormat {
		t.Fatalf("page_format: %v", got)
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

// Every worker with a node has the format sheet beside its node's tools,
// allowed in advance, never lent: eriphe-workspace is no server, a task is
// not lent it, and list_tools does not list it.
func TestWorkerHasTheFormat(t *testing.T) {
	c := nodeCore(t)
	task, _ := startInNode(t, c, runRequest{Title: "Une page", Prompt: "Compose la page."})
	got := mustNodes(t, c.core, task.ID, "page_format", nil)
	if got["format"] != pageFormat {
		t.Fatalf("page_format: %v", got)
	}
	c.store.registerDevice(task.Device, hashSecret("x"))
	spec := assignedSpec(t, c.core, task.Device, task.ID)
	if name := "mcp__eriphe-nodes__page_format"; !slices.Contains(spec.AllowedTools, name) {
		t.Errorf("%s not allowed in advance: %v", name, spec.AllowedTools)
	}
	if _, err := c.runTask(context.Background(), runRequest{Prompt: "x", Resources: []ResourceRef{{Kind: resTool, Ref: "eriphe-workspace"}}}); err == nil {
		t.Error("eriphe-workspace was lent")
	}
	listed, _ := callOn(t, connectServer(t, newMCPServer(c.core)), "list_tools", map[string]any{})
	if raw, _ := json.Marshal(listed); strings.Contains(string(raw), "eriphe-workspace") {
		t.Fatalf("list_tools: %s", raw)
	}
}

// markdownExamples are the pages a text teaches: each fence opened with
// ```markdown, whole.
func markdownExamples(text string) []string {
	var out []string
	for {
		_, rest, ok := strings.Cut(text, "```markdown\n")
		if !ok {
			return out
		}
		page, after, ok := strings.Cut(rest, "\n```")
		if !ok {
			return out
		}
		out = append(out, page+"\n")
		text = after
	}
}

// The pages the format sheet and the brain's prompt teach are pages the
// check finds nothing in (#786): read alone, their do: tools had by
// their author; and written by the brain, which is told nothing but the
// do: tools this catalog has not (it has no mail server).
func TestPageFormatExamples(t *testing.T) {
	examples := append(markdownExamples(pageFormat), markdownExamples(brainPrompt)...)
	if len(examples) < 2 {
		t.Fatalf("%d examples", len(examples))
	}
	all := strings.Join(examples, "\n")
	for _, want := range []string{"Écrit le ", "](do:", "](card:", "](ask:)", "](on:", "]: mail:", "📅 ", "> — ",
		"> [!figures", "> [!chart|", "> [!steps]", "> [!agenda]", "> [!person]", "> [!warning]"} {
		if !strings.Contains(all, want) {
			t.Errorf("no example shows %q", want)
		}
	}
	c := nodeCore(t).core
	for i, page := range examples {
		if pr := nodefile.ReadPage(page).Check(func(string) bool { return true }); len(pr) > 0 {
			t.Errorf("example %d: %+v\n%s", i, pr, page)
		}
		out := mustBrainNodes(t, c, "node_open", map[string]any{"title": fmt.Sprintf("Exemple %d", i), "goal": "Un exemple.", "body": page})
		for _, code := range problemCodes(t, out) {
			if code != nodefile.ProblemDoTool {
				t.Errorf("example %d written: %v", i, out["problems"])
			}
		}
		d, err := c.nodes.Read(out["id"].(string), 0)
		if err != nil {
			t.Fatal(err)
		}
		if body, _ := d.Node.Body(); body != page {
			t.Errorf("example %d kept as %q", i, body)
		}
	}
}
