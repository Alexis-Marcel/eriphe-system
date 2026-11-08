package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMailCore is a core whose catalog has a mail server trusted for its
// annotations: a read, an action that stays in the mailbox, and a send.
func newMailCore(t *testing.T, access string) *core {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Servers: map[string]Server{
		"mail": {URL: "https://mail.example/mcp", Auth: &ServerAuth{None: true}, Use: "mail", TrustAnnotations: true, Access: access},
	}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	srv := mcp.NewServer(&mcp.Implementation{Name: "mail"}, nil)
	no, yes := false, true
	for _, tl := range []*mcp.Tool{
		{Name: "search_messages", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no}},
		{Name: "move", Annotations: &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}},
		{Name: "send", Annotations: &mcp.ToolAnnotations{OpenWorldHint: &yes}},
		{Name: "unannotated"},
	} {
		mcp.AddTool(srv, tl, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{}, nil
		})
	}
	c.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := srv.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.pool.run(ctx)
	waitStatus(t, c.pool.get("mail"), statusReady)
	return c
}

// ask makes a worker's task request each tool once, and returns what the
// policy did: allowed, the user's, or left to the brain.
func ask(t *testing.T, c *core, id, tools string, names ...string) map[string]string {
	t.Helper()
	if !strings.HasPrefix(id, "t-alone") {
		c.store.set(lentByUserKey(id), "1") // started in the turn of my message
	}
	c.store.insertTask(&TaskRow{ID: id, Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "Trie les mails",
		State: stateWaitingPermission, Tools: tools})
	var events []EventRow
	for i, n := range names {
		events = append(events, EventRow{Seq: i + 1, Time: time.Now(), Type: "permission",
			Data: map[string]any{"id": n, "tool_name": "mcp__mail__" + n, "input": map[string]any{}}})
	}
	c.store.addEvents(id, events)
	c.applyPolicy(id, events)
	got := map[string]string{}
	items, _ := c.store.inbox(id)
	for _, n := range names {
		got[n] = "brain"
		if c.store.userOnly(id, n) != "" {
			got[n] = "user"
		}
		for _, it := range items {
			if it.Data["id"] == n && it.Data["allow"] == true {
				got[n] = "allowed"
			}
		}
	}
	return got
}

// A task's lend says what it reaches, the grid what it may do: at the
// default grid, a read goes through and everything else is the user's, even
// in a task the user started: their sentence approves nothing.
func TestGridDecidesWhatIsLent(t *testing.T) {
	c := newMailCore(t, "")
	got := ask(t, c, "t-1", "mail", "search_messages", "move", "send", "unannotated")
	want := map[string]string{"search_messages": "allowed", "move": "user", "send": "user", "unannotated": "user"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s", k, got[k], v)
		}
	}
	// A server the user wants to be asked for everything: its reads too.
	c = newMailCore(t, accessAsk)
	if got := ask(t, c, "t-3", "mail", "search_messages"); got["search_messages"] != "user" {
		t.Errorf("on a server set to ask: %s", got["search_messages"])
	}
}

// The user sees every pending request, but for those a card asks.
func TestShownPermissions(t *testing.T) {
	tw := newTestWeb(t)
	c := tw.c
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
		State: stateWaitingPermission})
	c.store.addEvents("t-1", []EventRow{
		{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-any", "tool_name": "mcp__mail__move"}},
		{Seq: 2, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-user", "tool_name": "mcp__mail__send"}},
	})
	c.store.markUserOnly("t-1", "p-user", "envoie un mail")
	st, err := c.status("t-1", 200)
	if err != nil {
		t.Fatal(err)
	}
	if got := tw.w.shownPermissions(st); len(got) != 2 {
		t.Errorf("shown %v", got)
	}
}

// A turn is the user's when one of their messages started it, not one
// from a channel the user did not trust nor an event.
func TestTurnFromUser(t *testing.T) {
	c := newMailCore(t, "")
	ev, _ := c.store.addMessage(roleEvent, "une veille", true)
	c.turn.start(int(ev), int(ev), 0)
	if c.turnFromUser() {
		t.Error("an event's turn is the user's")
	}
	auto, _ := c.store.addMessageVia(roleUser, "lumière", "Home Assistant", true)
	c.store.set(taintKey(int(auto)), channelTaint("Home Assistant"))
	c.turn.start(int(auto), int(auto), 0)
	if c.turnFromUser() {
		t.Error("an untrusted channel's turn is the user's")
	}
	mine, _ := c.store.addMessage(roleUser, "Ok pour supprimer les mails", true)
	c.turn.start(int(mine), int(mine), 0)
	if !c.turnFromUser() {
		t.Error("my message's turn is not mine")
	}
}
