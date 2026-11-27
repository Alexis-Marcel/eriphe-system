package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// browserTool is the browser as the catalog has it: a server that runs in
// the task (worker/base/eriphe-browser).
var browserTool = Server{Use: "a browser, for the apps a task runs", Task: &TaskTool{Command: "eriphe-browser"}}

// taskToolsCore is a core with a browser that runs in the task, one at
// Never, and a remote server, and a machine that runs Claude Code.
func taskToolsCore(t *testing.T) *core {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	off := browserTool
	off.Task = &TaskTool{Command: "eriphe-browser", Args: []string{"--mobile"}, Off: true}
	browser := browserTool
	browser.Task = &TaskTool{Command: "eriphe-browser", Args: []string{"--viewport-size", "1280x800"}, Env: map[string]string{"A": "1"}}
	cfg := &Config{
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Model: "sonnet"}},
		DefaultAgent: "sonnet",
		User:         "Alex",
		Servers: map[string]Server{
			"navigateur": browser,
			"mobile":     off,
			"notes":      {URL: "https://notes.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "notes"},
		},
	}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	store.registerDevice("nas", hashSecret("x"))
	store.setDeviceStatus("nas", true, []byte(`{"runtimes":["claude-code"]}`))
	return c
}

// An entry is remote, local or in the task, never two; one in the task has
// its command and no grid: it is Free or Never as a whole.
func TestTaskToolConfig(t *testing.T) {
	for name, s := range map[string]Server{
		"url":     {Use: "x", URL: "https://example.org/mcp", Auth: &ServerAuth{None: true}, Task: &TaskTool{Command: "x"}},
		"run":     {Use: "x", Run: []byte(`{"image":"x"}`), Task: &TaskTool{Command: "x"}},
		"command": {Use: "x", Task: &TaskTool{Command: " "}},
		"grid":    {Use: "x", Task: &TaskTool{Command: "x"}, Access: accessAsk},
		"except":  {Use: "x", Task: &TaskTool{Command: "x"}, Exceptions: map[string]string{"*": gridNever}},
	} {
		cfg := &Config{Servers: map[string]Server{"tool": s}}
		if err := cfg.checkServers(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cfg := &Config{Servers: map[string]Server{"navigateur": browserTool}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	if got := browserTool.authKind(); got != "task" {
		t.Errorf("auth kind %q", got)
	}
}

// A worker lent the browser gets it as a command its agent starts in its
// container, allowed whole, and not relayed by its device; one lent nothing
// gets nothing; one at Never is lent to no task.
func TestTaskToolInTheSpec(t *testing.T) {
	c := taskToolsCore(t)
	ctx := context.Background()
	task, err := c.runTask(ctx, runRequest{Prompt: "test the front", Resources: []ResourceRef{
		{Kind: resTool, Ref: "navigateur"}, {Kind: resTool, Ref: "notes"}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := assignedSpec(t, c, "nas", task.ID)
	want := map[string]any{"navigateur": map[string]any{"type": "stdio", "command": "eriphe-browser",
		"args": []any{"--viewport-size", "1280x800"}, "env": map[string]any{"A": "1"}}}
	if !reflect.DeepEqual(spec.MCPServers, want) {
		t.Errorf("mcp servers %#v", spec.MCPServers)
	}
	if !slices.Equal(spec.Servers, []string{"notes"}) {
		t.Errorf("relayed %v: the browser runs in the task", spec.Servers)
	}
	if !slices.Contains(spec.AllowedTools, "mcp__navigateur") {
		t.Errorf("allowed %v", spec.AllowedTools)
	}

	plain, err := c.runTask(ctx, runRequest{Prompt: "sum"})
	if err != nil {
		t.Fatal(err)
	}
	if spec := assignedSpec(t, c, "nas", plain.ID); len(spec.MCPServers) != 0 || slices.Contains(spec.AllowedTools, "mcp__navigateur") {
		t.Errorf("a task lent nothing: %v %v", spec.MCPServers, spec.AllowedTools)
	}

	if _, err := c.runTask(ctx, runRequest{Prompt: "x", Resources: []ResourceRef{{Kind: resTool, Ref: "mobile"}}}); err == nil ||
		!strings.Contains(err.Error(), "Never") {
		t.Errorf("lent at Never: %v", err)
	}
	if _, err := c.runTask(ctx, runRequest{Prompt: "x", Resources: []ResourceRef{{Kind: resTool, Ref: "navigateur", Hold: true}}}); err == nil {
		t.Error("held back: it would be in the task from its start")
	}
	// Set to Never after it was lent: the task no longer gets it.
	c.conf().Servers["navigateur"].Task.Off = true
	if spec := assignedSpec(t, c, "nas", task.ID); len(spec.MCPServers) != 0 || !slices.Equal(spec.Servers, []string{"notes"}) {
		t.Errorf("at Never: %v %v", spec.MCPServers, spec.Servers)
	}
}

// The brain sees it in its list of tools, to lend it, but is never given
// one: no upstream, nothing to connect.
func TestTaskToolNotConnected(t *testing.T) {
	c := taskToolsCore(t)
	if c.pool.get("navigateur") != nil {
		t.Fatal("a server that runs in the task has an upstream")
	}
	c.pool.sync(c.conf().Servers)
	if c.pool.get("navigateur") != nil {
		t.Fatal("synced: an upstream")
	}
	for _, info := range c.listServers("", false) {
		switch info.Name {
		case "navigateur":
			if info.Kind != "task" || info.Status != statusReady || info.Access != gridFree {
				t.Errorf("listed %+v", info)
			}
		case "mobile":
			if info.Access != gridNever {
				t.Errorf("at Never, listed %+v", info)
			}
		}
	}
	if spec := c.taskTools(&TaskRow{Kind: "worker", Tools: "navigateur,mobile"}); len(spec) != 1 || spec["navigateur"] == nil {
		t.Errorf("task tools %v", spec)
	}
}

// Its calls are the container's: allowed for the task that was lent it,
// refused for another, and at Never.
func TestTaskToolPolicy(t *testing.T) {
	c := taskToolsCore(t)
	decide := func(id, tools, tool string) bool {
		t.Helper()
		c.store.insertTask(&TaskRow{ID: id, Kind: "worker", Device: "nas", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
			State: stateWaitingPermission, Tools: tools})
		ev := []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
			Data: map[string]any{"id": "p1", "tool_name": tool, "input": map[string]any{"url": "http://localhost:5173"}}}}
		c.store.addEvents(id, ev)
		c.applyPolicy(id, ev)
		items, _ := c.store.inbox(id)
		for _, it := range items {
			if it.Data["id"] == "p1" {
				return it.Data["allow"] == true
			}
		}
		t.Fatalf("%s: no answer", id)
		return false
	}
	if !decide("t-1", "navigateur", "mcp__navigateur__browser_click") {
		t.Error("lent: refused")
	}
	if decide("t-2", "notes", "mcp__navigateur__browser_click") {
		t.Error("not lent: allowed")
	}
	if decide("t-3", "mobile", "mcp__mobile__browser_click") {
		t.Error("at Never: allowed")
	}
}
