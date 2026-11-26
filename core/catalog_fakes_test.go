package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeMonitor is a catalog server whose state the test sets: status (a
// read-only tool), rollback (a tool that acts), and a resource,
// monitor://received (the mail server's mail://received).
type fakeMonitor struct {
	mu       sync.Mutex
	state    string
	version  string
	failing  bool
	calls    int
	rules    []map[string]any // alerts: Grafana-like alert rules, each uid, name, state
	stamped  bool             // alerts: each rule comes with its evaluation, new at each call
	cpu      float64          // status: data.cpu
	got      []map[string]any // monitor://received: messages, each id, subject
	silenced []string         // silence: the uids it was called on
	invites  []map[string]any // monitor://invitations: invitations, each id, title
	sent     []map[string]any // send and respond: what they were called with
	opened   []string         // message and letter: tool:id of each call
	// moveFails: move answers with this error, moving nothing.
	moveFails string
}

func (m *fakeMonitor) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "monitor", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "status", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}},
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
	mcp.AddTool(s, &mcp.Tool{Name: "alerts", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.calls++
			if m.failing {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unreachable"}}}, nil, nil
			}
			rules := []any{}
			for _, r := range m.rules {
				if m.stamped {
					r = maps.Clone(r)
					r["evaluated"] = m.calls
				}
				rules = append(rules, r)
			}
			return nil, map[string]any{"data": map[string]any{"rules": rules}, "at": time.Now().Format(time.RFC3339Nano)}, nil
		})
	s.AddResource(&mcp.Resource{URI: "monitor://received", Name: "received", MIMEType: "application/json"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.calls++
			msgs := []any{}
			for _, g := range m.got {
				msgs = append(msgs, g)
			}
			raw, _ := json.Marshal(map[string]any{"messages": msgs})
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "monitor://received", Text: string(raw)}}}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "rollback"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Version string `json:"version"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"ok": true}, nil
		})
	// silence acts on one alert, by its uid: an action on an item.
	mcp.AddTool(s, &mcp.Tool{Name: "silence"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			UID string `json:"uid"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.silenced = append(m.silenced, in.UID)
			return nil, map[string]any{"ok": true}, nil
		})
	// The invitations waiting for an answer, as the calendar server's
	// calendar://invitations lists them.
	s.AddResource(&mcp.Resource{URI: "monitor://invitations", Name: "invitations", MIMEType: "application/json"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			list := []any{}
			for _, g := range m.invites {
				list = append(list, g)
			}
			raw, _ := json.Marshal(map[string]any{"invitations": list})
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "monitor://invitations", Text: string(raw)}}}, nil
		})
	// send replies to a message, respond answers an invitation: actions on
	// an item, by its id. send has a title of its own; reply's schema names
	// its text.
	s.AddTool(&mcp.Tool{Name: "reply", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string"}, "text": map[string]any{"type": "string", "title": "Ta réponse"}}}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "send", Annotations: &mcp.ToolAnnotations{Title: "Send a reply"}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.sent = append(m.sent, map[string]any{"tool": "send", "id": in.ID, "body": in.Body})
			return nil, map[string]any{"ok": true}, nil
		})
	// move files messages by their ids, and says its call, as the mail
	// server's move does (call_says.go).
	mcp.AddTool(s, &mcp.Tool{Name: "move", Meta: mcp.Meta{saysMetaKey: map[string]any{
		"text":   map[string]any{"fr": "Déplacer {count} vers {to}", "en": "Move {count} to {to}"},
		"items":  map[string]any{"fr": []any{"1 mail", "{n} mails"}, "en": []any{"1 email", "{n} emails"}},
		"done":   map[string]any{"fr": []any{"1 déplacé", "{n} déplacés"}, "en": []any{"1 moved", "{n} moved"}},
		"values": map[string]any{"to": map[string]any{"archive": map[string]any{"fr": "les archives", "en": "the archive"}}},
	}}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Account string   `json:"account,omitempty"`
			IDs     []string `json:"ids"`
			To      string   `json:"to"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.moveFails != "" {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: m.moveFails}}}, nil, nil
			}
			m.sent = append(m.sent, map[string]any{"tool": "move", "ids": in.IDs, "to": in.To, "account": in.Account})
			return nil, map[string]any{"moved": len(in.IDs)}, nil
		})
	// discard declares only its annotations: a title, and that it throws
	// away (no destructiveHint: MCP's default, destructive).
	mcp.AddTool(s, &mcp.Tool{Name: "discard", Annotations: &mcp.ToolAnnotations{Title: "Discard", OpenWorldHint: closedWorld}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			IDs []string `json:"ids"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"discarded": len(in.IDs)}, nil
		})
	// tag and flag declare a read for their card (card_context.go): tag the
	// status, a free read; flag silence, a write, never run for a card.
	reads := func(tool, with string) mcp.Meta {
		return mcp.Meta{readsMetaKey: map[string]any{
			"tools": []any{map[string]any{"tool": tool}}, "with": []any{with},
			"show": []any{map[string]any{"field": "service", "name": map[string]any{"fr": "Service", "en": "Service"}}},
		}}
	}
	for name, meta := range map[string]mcp.Meta{"tag": reads("status", "service"), "flag": reads("silence", "uid")} {
		mcp.AddTool(s, &mcp.Tool{Name: name, Annotations: &mcp.ToolAnnotations{OpenWorldHint: closedWorld}, Meta: meta},
			func(context.Context, *mcp.CallToolRequest, struct {
				Service string `json:"service,omitempty"`
				UID     string `json:"uid,omitempty"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				return nil, map[string]any{"ok": true}, nil
			})
	}
	// respond says its call itself, as the calendar server's respond_event
	// does (call_says.go).
	mcp.AddTool(s, &mcp.Tool{Name: "respond", Title: "Answer an invitation", Meta: mcp.Meta{saysMetaKey: map[string]any{
		"text": map[string]string{"fr": "{answer} {item}[, {start}]", "en": "{answer} {item}[, {start}]"},
		"item": map[string]string{"fr": "l'invitation", "en": "the invitation"},
		"values": map[string]any{"answer": map[string]any{
			"accept":  map[string]string{"fr": "Accepter", "en": "Accept"},
			"decline": map[string]string{"fr": "Refuser", "en": "Decline"},
			"propose": map[string]string{"fr": "Proposer un autre moment pour", "en": "Propose another time for"},
		}},
	}}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			ID     string `json:"id"`
			Answer string `json:"answer"`
			Start  string `json:"start,omitempty"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.sent = append(m.sent, map[string]any{"tool": "respond", "id": in.ID, "answer": in.Answer, "start": in.Start})
			return nil, map[string]any{"ok": true}, nil
		})
	// message reads one received message in full, by its id, and declares
	// its fields as the mail server's read_message does; letter the same,
	// reaching the open web (no openWorldHint): an item opened.
	readOne := func(name string) func(context.Context, *mcp.CallToolRequest, struct {
		ID string `json:"id"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		return func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			ID string `json:"id"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.opened = append(m.opened, name+":"+in.ID)
			for _, g := range m.got {
				if g["id"] == in.ID {
					return nil, map[string]any{"message": map[string]any{"subject": g["subject"], "from": g["from"], "date": "2026-11-04T09:00:00Z", "body": "Le texte de " + in.ID}}, nil
				}
			}
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no such message"}}}, nil, nil
		}
	}
	mcp.AddTool(s, &mcp.Tool{Name: "message", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld},
		Meta: mcp.Meta{openMetaKey: map[string]any{"id": "id", "title": "subject", "from": "from", "date": "date", "body": "body"}}}, readOne("message"))
	// memo is message declaring only its annotations: no field of what it
	// says is declared (item_open.go).
	mcp.AddTool(s, &mcp.Tool{Name: "memo", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}}, readOne("memo"))
	mcp.AddTool(s, &mcp.Tool{Name: "letter", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, readOne("letter"))
	// thread reads a message by its id and its folder, both required: no
	// item is opened by its id alone with it.
	mcp.AddTool(s, &mcp.Tool{Name: "thread", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			ID     string `json:"id"`
			Folder string `json:"folder"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"id": in.ID}, nil
		})
	// page reads the open web: no openWorldHint.
	mcp.AddTool(s, &mcp.Tool{Name: "page", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"text": "a page"}, nil
		})
	return s
}

// pollCore is a core with a brain lent the monitor server, a vault, and a
// clock the test moves.
// Each of before is done to the core before its pool runs (withGraph).
func pollCore(t *testing.T, before ...func(*core)) (*core, *fakeMonitor, *time.Time) {
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
	for _, f := range before {
		f(c)
	}
	runPool(t, context.Background(), c.pool)
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
