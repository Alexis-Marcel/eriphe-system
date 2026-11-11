package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeMonitor is a catalog server whose state the test sets: status (a
// read-only tool), rollback (a tool that acts).
type fakeMonitor struct {
	mu      sync.Mutex
	state   string
	version string
	failing bool
	calls   int
	rules   []map[string]any // alerts: Grafana-like alert rules, each uid, name, state
	cpu     float64          // status: data.cpu
}

func (m *fakeMonitor) set(state, version string) {
	m.mu.Lock()
	m.state, m.version = state, version
	m.mu.Unlock()
}

func (m *fakeMonitor) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "monitor", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "status", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Service string `json:"service"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.calls++
			if m.failing {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unreachable"}}}, nil, nil
			}
			return nil, map[string]any{"service": in.Service, "data": map[string]any{"state": m.state, "version": m.version, "cpu": m.cpu}}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "alerts", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.calls++
			if m.failing {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unreachable"}}}, nil, nil
			}
			rules := []any{}
			for _, r := range m.rules {
				rules = append(rules, r)
			}
			return nil, map[string]any{"data": map[string]any{"rules": rules}, "at": time.Now().Format(time.RFC3339Nano)}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "rollback"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Version string `json:"version"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"ok": true}, nil
		})
	return s
}

// pollCore is a core with a brain lent the monitor server, a vault, and a
// clock the test moves.
func pollCore(t *testing.T) (*core, *fakeMonitor, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := vaultKey(filepath.Join(dir, "vault.key"))
	v, err := openVault(store, key)
	if err != nil {
		t.Fatal(err)
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	cfg := &Config{User: "Sam", DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: secret}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p"}},
		Servers: map[string]Server{"monitor": {URL: "https://monitor.example.org/mcp", Auth: &ServerAuth{None: true},
			Use: "the site's health", Access: accessReads}}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	mon := &fakeMonitor{state: "up", version: "2.0.0"}
	c.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := mon.server().Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.pool.run(ctx)
	waitStatus(t, c.pool.get("monitor"), statusReady)
	setBrain(c, &BrainConfig{Agent: "small", Tools: []string{"monitor"}})
	store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Runtime: "claude-code", State: stateIdle, Prompt: "brain"})
	setBrainTask(c, "t-brain")
	c.store.set("notify_zone", "Europe/Paris")
	now := at(t, "2026-11-04", "10:00")
	c.notifyClock = func() time.Time { return now }
	return c, mon, &now
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// brainCallsTool is the brain asking for a catalog tool; true if allowed.
func brainCallsTool(t *testing.T, c *core, id, tool string, input map[string]any) bool {
	t.Helper()
	c.store.insertTask(&TaskRow{ID: id, Node: "pc", Kind: "brain", Runtime: "claude-code", State: stateWaitingPermission})
	c.store.addEvents(id, []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": "p1", "tool_name": tool, "input": input}}})
	task, _ := c.store.task(id)
	if err := c.brainPermission(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	items, _ := c.store.inbox(id)
	return len(items) == 1 && items[0].Data["allow"] == true
}
func callBrainTool(t *testing.T, c *core, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := newMCPServer(c).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(raw, &out)
	return out, res.IsError
}
