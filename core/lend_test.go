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

// What a task is lent, it uses: a read and an action that stays in the
// mailbox go through without asking; a send, or an action no annotation
// says stays inside, is the user's; what is not lent is not authorized.
func TestLendAuthorizes(t *testing.T) {
	c := newMailCore(t, "")
	got := ask(t, c, "t-1", "mail", "search_messages", "move", "send", "unannotated")
	want := map[string]string{"search_messages": "allowed", "move": "allowed", "send": "user", "unannotated": "user"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s", k, got[k], v)
		}
	}
	if got := ask(t, c, "t-2", "mail.search_messages", "move"); got["move"] != "user" {
		t.Errorf("an action not lent: %s", got["move"])
	}
	// A task the brain started alone (a watch woke it): its reads only.
	if got := ask(t, c, "t-alone", "mail", "search_messages", "move"); got["search_messages"] != "allowed" || got["move"] != "user" {
		t.Errorf("a task started without my message: %v", got)
	}

	// A server the user wants to be asked for everything: the lend does not change it.
	c = newMailCore(t, accessAsk)
	if got := ask(t, c, "t-3", "mail", "move"); got["move"] != "user" {
		t.Errorf("on a server set to ask: %s", got["move"])
	}
}

// A request the brain was told of and left at the end of its turn is the
// user's; one it was not told of yet stays its own.
func TestBrainLeftPermissions(t *testing.T) {
	c := newMailCore(t, "")
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
		State: stateWaitingPermission})
	c.store.addEvents("t-1", []EventRow{
		{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-told", "tool_name": "mcp__mail__move"}},
		{Seq: 2, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-new", "tool_name": "mcp__mail__move"}},
	})
	told, _ := c.store.addMessage(roleEvent, "une permission", true)
	later, _ := c.store.addMessage(roleEvent, "une autre", false)
	c.store.set("notified_perm:t-1:p-told", idText(told))
	c.store.set("notified_perm:t-1:p-new", idText(later))

	c.brainLeftPermissions()
	if c.store.userOnly("t-1", "p-told") == "" {
		t.Error("a request the brain left is not the user's")
	}
	if c.store.userOnly("t-1", "p-new") != "" {
		t.Error("a request the brain has not seen yet was handed over")
	}
}

// The user sees the requests that are theirs; the brain's only while it is
// away.
func TestShownPermissions(t *testing.T) {
	tw := newTestWeb(t)
	c := tw.c
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "x",
		State: stateWaitingPermission})
	c.store.addEvents("t-1", []EventRow{
		{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-brain", "tool_name": "mcp__mail__move"}},
		{Seq: 2, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-user", "tool_name": "mcp__mail__send"}},
	})
	c.store.markUserOnly("t-1", "p-user", "envoie un mail")
	st, err := c.status("t-1", 200)
	if err != nil {
		t.Fatal(err)
	}
	if got := tw.w.shownPermissions(st, false); len(got) != 1 || got[0]["id"] != "p-user" {
		t.Errorf("shown %v", got)
	}
	if got := tw.w.shownPermissions(st, true); len(got) != 2 {
		t.Errorf("shown while the brain is away: %v", got)
	}
}

// A turn is the user's when one of their messages started it, not an
// automation's nor an event's.
func TestTurnFromUser(t *testing.T) {
	c := newMailCore(t, "")
	ev, _ := c.store.addMessage(roleEvent, "une veille", true)
	c.turn.start(int(ev), int(ev), 0)
	if c.turnFromUser() {
		t.Error("an event's turn is the user's")
	}
	auto, _ := c.store.addMessageVia(roleUser, "lumière", automationVia, true)
	c.turn.start(int(auto), int(auto), 0)
	if c.turnFromUser() {
		t.Error("an automation's turn is the user's")
	}
	mine, _ := c.store.addMessage(roleUser, "Ok pour supprimer les mails", true)
	c.turn.start(int(mine), int(mine), 0)
	if !c.turnFromUser() {
		t.Error("my message's turn is not mine")
	}
}
