package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeSink is a catalog server with a tool that takes a node's file
// (eriphe/files: document) and records what it was given.
type fakeSink struct {
	mu  sync.Mutex
	got []map[string]any
}

func (f *fakeSink) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "sink", Version: "1"}, nil)
	type in struct {
		Document string `json:"document" jsonschema:"the document"`
		Title    string `json:"title,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "send", Description: "Send a document.", Meta: mcp.Meta{filesMetaKey: []string{"document"}}},
		func(_ context.Context, _ *mcp.CallToolRequest, v in) (*mcp.CallToolResult, map[string]any, error) {
			f.mu.Lock()
			f.got = append(f.got, map[string]any{"document": v.Document, "title": v.Title})
			f.mu.Unlock()
			return nil, map[string]any{"ok": true}, nil
		})
	return s
}

func (f *fakeSink) calls() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.got...)
}

// sinkCore is a core with a graph and the sink server in its catalog.
func sinkCore(t *testing.T) (*core, *fakeSink) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Servers: map[string]Server{"sink": {URL: "https://sink.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "sends documents"}}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	sink := &fakeSink{}
	c.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := sink.server().Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	openGraph(t, c)
	runPool(t, context.Background(), c.pool)
	waitStatus(t, c.pool.get("sink"), statusReady)
	return c, sink
}

// A node's file goes to a tool that declares it takes one: the ticket,
// the card and the log keep its reference, the server gets its bytes.
func TestToolTakesNodeFile(t *testing.T) {
	c, sink := sinkCore(t)
	d, err := c.nodes.Create(nodestore.New{Title: "Recette", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	pdf := []byte("%PDF-1.4\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n")
	name, err := c.nodes.PutFile(id, byBrain, "pdf", pdf)
	if err != nil {
		t.Fatal(err)
	}
	ref := "media:" + id + "/" + name
	u := c.pool.get("sink")
	call := func(in map[string]any) *mcp.CallToolResult {
		t.Helper()
		raw, _ := json.Marshal(in)
		return c.proxyCall(context.Background(), "t-1", []string{"sink"}, u, "send", raw)
	}

	// The schema the task sees says how to give a file.
	cs := connectTo(t, c.taskServer("t-1", []string{"sink"}, "sink"))
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	raw, _ := json.Marshal(tools.Tools[0].InputSchema)
	var schema struct {
		Properties map[string]struct{ Description string } `json:"properties"`
	}
	json.Unmarshal(raw, &schema)
	if d := schema.Properties["document"].Description; d != "the document. "+fileArgsNote {
		t.Errorf("document's description = %q", d)
	}
	if d := schema.Properties["title"].Description; d != "" {
		t.Errorf("title's description = %q", d)
	}

	// Without a ticket: refused, nothing read nor sent.
	in := map[string]any{"document": ref, "title": "media:" + id + "/" + name}
	if res := call(in); !res.IsError || len(sink.calls()) != 0 {
		t.Fatalf("no ticket = %+v", res)
	}
	// The ticket is for the reference: the bytes go upstream.
	c.store.recordDecision("t-1", "p1", "mcp__sink__send", in, decideAllow, byUser, "")
	if res := call(in); res.IsError {
		t.Fatalf("allowed call = %s", resultText(res))
	}
	got := sink.calls()
	want := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdf)
	if len(got) != 1 || got[0]["document"] != want {
		t.Fatalf("sent = %+v", got)
	}
	if got[0]["title"] != in["title"] {
		t.Errorf("an argument not declared was changed: %v", got[0]["title"])
	}
	// The ticket is spent.
	if res := call(in); !res.IsError {
		t.Fatal("a ticket was used twice")
	}

	// What is not a node's file is refused, even with a ticket, before
	// anything goes upstream.
	for _, bad := range []string{
		"data:application/pdf;base64,JVBERi0=", // bytes of the caller's own
		"/etc/passwd",
		"media:" + name, // without the node
		"media:" + id + "/../" + name,
		"media:" + id + "/med_00000000000000000000000000000000.pdf", // no such file
		"media:nope/" + name, // no such node
	} {
		in := map[string]any{"document": bad}
		c.store.recordDecision("t-1", "p1", "mcp__sink__send", in, decideAllow, byUser, "")
		res := call(in)
		if !res.IsError || !strings.Contains(resultText(res), "document") {
			t.Errorf("%q = %+v", bad, res)
		}
		if strings.Contains(resultText(res), "JVBERi0=") && len(bad) > 60 {
			t.Errorf("%q echoed whole", bad)
		}
	}
	if n := len(sink.calls()); n != 1 {
		t.Errorf("%d calls went upstream, want 1", n)
	}
}

// A card's yes sends the file too, its call kept with the reference.
func TestCardCallTakesNodeFile(t *testing.T) {
	c, sink := sinkCore(t)
	d, err := c.nodes.Create(nodestore.New{Title: "Facture", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	name, err := c.nodes.PutFile(d.Node.Meta.ID, byBrain, "png", png)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"document": "media:" + d.Node.Meta.ID + "/" + name})
	var allowed map[string]any
	if _, _, code, why := c.catalogCall(context.Background(), "mcp__sink__send", string(raw), func(in map[string]any) { allowed = in }); code != "" {
		t.Fatalf("card call = %s %s", code, why)
	}
	if !strings.HasPrefix(allowed["document"].(string), "media:") {
		t.Errorf("decision kept %v", allowed)
	}
	got := sink.calls()
	if len(got) != 1 || got[0]["document"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("sent = %+v", got)
	}
	// A file gone meanwhile: the call does not run.
	raw, _ = json.Marshal(map[string]any{"document": "media:" + d.Node.Meta.ID + "/med_00000000000000000000000000000000.png"})
	if _, _, code, _ := c.catalogCall(context.Background(), "mcp__sink__send", string(raw), func(map[string]any) { t.Error("recorded") }); code != callWhyGone {
		t.Errorf("gone file = %q", code)
	}
}
