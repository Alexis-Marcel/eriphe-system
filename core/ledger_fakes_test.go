package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeLedger is a server nobody wrote anything for: its name, its tools
// and its resource appear nowhere in eriphe-core. balances reads (with an
// outputSchema), move_funds acts, ledger://latest is a resource it tells
// the updates of.
type fakeLedger struct {
	mu       sync.Mutex
	accounts []ledgerAccount
	latest   string
	calls    int
	listened []string      // the resources the server holds a subscription to
	hold     chan struct{} // the next read of the resource waits on it
	gate     chan struct{} // while set, every read of the resource waits on it
	inflight int           // reads of the resource waiting on the gate
	most     int           // the most of them at once
	srv      *mcp.Server
}

type ledgerAccount struct {
	Name    string  `json:"name"`
	Balance float64 `json:"balance"`
	Photo   string  `json:"photo,omitempty"`
}

type ledgerOut struct {
	Accounts []ledgerAccount `json:"accounts"`
	Total    float64         `json:"total"`
}

func newFakeLedger() *fakeLedger {
	l := &fakeLedger{accounts: []ledgerAccount{{Name: "Courant", Balance: 120}}, latest: "first"}
	s := mcp.NewServer(&mcp.Implementation{Name: "ledger", Version: "1"}, &mcp.ServerOptions{
		SubscribeHandler:   func(context.Context, *mcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	mcp.AddTool(s, &mcp.Tool{Name: "balances", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ledgerOut, error) {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.calls++
			out := ledgerOut{Accounts: append([]ledgerAccount{}, l.accounts...)}
			for _, a := range l.accounts {
				out.Total += a.Balance
			}
			return nil, out, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "move_funds"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"ok": true}, nil
		})
	s.AddResource(&mcp.Resource{URI: "ledger://latest", Name: "latest", MIMEType: "application/json"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			l.mu.Lock()
			l.calls++
			raw, _ := json.Marshal(map[string]any{"entry": l.latest})
			hold, gate := l.hold, l.gate
			l.hold = nil
			if gate != nil {
				l.inflight++
				l.most = max(l.most, l.inflight)
			}
			l.mu.Unlock()
			if hold != nil {
				<-hold // the test holds this read
			}
			if gate != nil {
				<-gate
				l.set(func(l *fakeLedger) { l.inflight-- })
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "ledger://latest", Text: string(raw)}}}, nil
		})
	// A subscription the server holds, and no sooner: the client's Subscribe
	// returns once its request is sent (the SDK does not wait for an answer),
	// and an update told before the server holds it reaches nobody. The
	// server acknowledges a subscription once it holds it.
	s.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if p, ok := req.GetParams().(*mcp.SubscriptionsAcknowledgedParams); ok && p != nil {
				l.set(func(l *fakeLedger) { l.listened = append(l.listened, p.Notifications.ResourceSubscriptions...) })
			}
			return next(ctx, method, req)
		}
	})
	l.srv = s
	return l
}

// listens tells whether the server holds a subscription to a resource: it
// tells its updates from then on.
func (l *fakeLedger) listens(uri string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.listened, uri)
}

func (l *fakeLedger) set(f func(l *fakeLedger)) {
	l.mu.Lock()
	f(l)
	l.mu.Unlock()
}

func (l *fakeLedger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// ledgerCore is a core whose catalog links the fake ledger, lent to the
// brain, its reads free.
func ledgerCore(t *testing.T) (*core, *fakeLedger) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	cfg := &Config{User: "Sam", DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: secret}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p"}},
		Servers: map[string]Server{"ledger": {URL: "https://ledger.example.org/mcp", Auth: &ServerAuth{None: true},
			Use: "accounts", Access: accessReads}}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	l := newFakeLedger()
	c.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := l.srv.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.pool.run(ctx)
	waitStatus(t, c.pool.get("ledger"), statusReady)
	waitFor(t, func() bool { return len(c.pool.get("ledger").resourceURIs()) > 0 })
	setBrain(c, &BrainConfig{Agent: "small", Tools: []string{"ledger"}})
	return c, l
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
