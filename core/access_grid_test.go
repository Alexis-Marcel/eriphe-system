package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolCategory(t *testing.T) {
	no, yes := false, true
	cases := []struct {
		name    string
		a       *mcp.ToolAnnotations
		trusted bool
		want    string
	}{
		{"read", &mcp.ToolAnnotations{ReadOnlyHint: true}, true, catRead},
		{"change", &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}, true, catChange},
		{"delete", &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &yes}, true, catDelete},
		{"delete by default", &mcp.ToolAnnotations{OpenWorldHint: &no}, true, catDelete},
		{"send", &mcp.ToolAnnotations{OpenWorldHint: &yes, DestructiveHint: &no}, true, catSend},
		{"send by default", &mcp.ToolAnnotations{DestructiveHint: &no}, true, catSend},
		{"unannotated", nil, true, catSend},
		{"not believed", &mcp.ToolAnnotations{ReadOnlyHint: true}, false, catSend},
	}
	for _, tc := range cases {
		if got := toolCategory(&mcp.Tool{Name: "x", Annotations: tc.a}, tc.trusted); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Each level reads as the issue's table says, and comes back from its grid.
func TestLevelsReadAsGrids(t *testing.T) {
	for level, want := range map[string]accessGrid{
		accessReadOnly: {gridFree, gridNever, gridNever, gridNever},
		accessReads:    {gridFree, gridApproval, gridApproval, gridApproval},
		accessFree:     {gridFree, gridFree, gridFree, gridFree},
		accessAsk:      {gridApproval, gridApproval, gridApproval, gridApproval},
	} {
		if got := levelGrid(level); got != want {
			t.Errorf("%s = %+v", level, got)
		}
		if got := gridLevel(want, level != accessAsk); got != level {
			t.Errorf("%s comes back as %s", level, got)
		}
	}
	// What no level says is the closest with writes, so a credential keeps them.
	if got := gridLevel(accessGrid{Delete: gridNever, Send: gridFree}, true); got != accessReads {
		t.Errorf("a grid of its own = %s", got)
	}
	// Annotations not believed: every tool is Send, on approval.
	s := Server{Access: accessAsk}
	if f := s.toolFacts(&mcp.Tool{Name: "x", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, nil); f.category != catSend || f.value != gridApproval {
		t.Errorf("ask = %+v", f)
	}
}

func TestGridChecked(t *testing.T) {
	bad := []Server{
		{Grid: &accessGrid{Send: "sometimes"}},
		{Exceptions: map[string]string{"x": "yes"}},
		{AccountGrids: map[string]accountGrid{"a": {Grid: accessGrid{Read: "maybe"}}}},
	}
	for i, s := range bad {
		if s.checkAccess() == nil {
			t.Errorf("%d: accepted", i)
		}
	}
	raw := `{"grid": {"send": "never"}, "exceptions": {"draft": "free"}, "trust_annotations": true}`
	var s Server
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s.checkAccess() != nil {
		t.Fatalf("the new form: %v", err)
	}
	if s.Grid.value(catRead) != gridFree || s.Grid.value(catChange) != gridApproval {
		t.Errorf("a row left out = %+v", s.Grid)
	}
}

// A row may be anything: nothing ties one to another, and an exception
// replaces its category's value, Never included.
func TestGridRowsAndExceptions(t *testing.T) {
	no := false
	move := &mcp.Tool{Name: "move", Annotations: &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}}
	send := &mcp.Tool{Name: "send"}
	s := Server{TrustAnnotations: true, Grid: &accessGrid{Change: gridNever, Send: gridFree}}
	if lends(s, move) || !lends(s, send) {
		t.Errorf("lent: move %v, send %v", lends(s, move), lends(s, send))
	}
	s.Exceptions = map[string]string{"move": gridFree, "send": gridNever}
	if !lends(s, move) || lends(s, send) {
		t.Errorf("with exceptions: move %v, send %v", lends(s, move), lends(s, send))
	}
}

// An account's value decides the call that names it; one that names none
// is refused when their values differ.
func TestAccountGrids(t *testing.T) {
	no := false
	move := &mcp.Tool{Name: "move", Annotations: &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}}
	read := &mcp.Tool{Name: "search", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	s := Server{TrustAnnotations: true, AccountGrids: map[string]accountGrid{
		"pro@example.org":   {Grid: accessGrid{Change: gridNever}},
		"perso@example.org": {Grid: accessGrid{Change: gridFree}, Exceptions: map[string]string{"search": gridNever}},
	}}
	if f := s.toolFacts(move, map[string]any{"account": "PERSO@example.org"}); f.value != gridFree {
		t.Errorf("named = %+v", f)
	}
	if f := s.toolFacts(move, nil); !f.noAccount {
		t.Errorf("no account = %+v", f)
	}
	if f := s.toolFacts(read, nil); !f.noAccount {
		t.Errorf("a read no account may refuse = %+v", f)
	}
	if !lends(s, move) {
		t.Error("a tool one account allows is not lent")
	}
	if got := s.withLevel(accessReads); got.Grid != nil || got.AccountGrids != nil || len(got.AccountAccess) != 2 {
		t.Errorf("a level over grids = %+v", got)
	}
}

// The brain: Free is free on a clean turn; on a tainted one every Free
// above Read is the user's, and Read stays free. Never and On approval do
// not move. The turn the user starts is clean again.
func TestBrainFreeWriteFollowsTheTurn(t *testing.T) {
	c := newMailCore(t, accessFree)
	read, write := "mcp__mail__search_messages", "mcp__mail__move"
	for tool, want := range map[string]string{read: decideAllow, write: decideAllow} {
		if got, why := c.decideBrainCall(tool, nil, nil); got != want {
			t.Errorf("clean: %s = %q (%s)", tool, got, why)
		}
	}
	c.turn.taint("mail.search_messages")
	if got, _ := c.decideBrainCall(read, nil, nil); got != decideAllow {
		t.Errorf("tainted: a read = %q", got)
	}
	if got, why := c.decideBrainCall(write, nil, nil); got != decideAsk || why != uncleanReason {
		t.Errorf("tainted: a write = %q (%s)", got, why)
	}
	// "Always allow" is a Free exception: it follows the same rule.
	c = newMailCore(t, accessReads)
	if err := c.store.allowAction("mail", write, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decideBrainCall(write, nil, nil); got != decideAllow {
		t.Errorf("remembered, clean: %q", got)
	}
	c.turn.taint("mail.search_messages")
	if got, _ := c.decideBrainCall(write, nil, nil); got != decideAsk {
		t.Errorf("remembered, tainted: %q", got)
	}
	c.turn.start(1, 1, 0)
	if got, _ := c.decideBrainCall(write, nil, nil); got != decideAllow {
		t.Errorf("the user's next turn: %q", got)
	}
}

// The gap this closes: a Free write is never allowed in advance nor called
// without a ticket, so it cannot skip the turn's taint.
func TestBrainFreeWriteNeedsThePolicy(t *testing.T) {
	c := newMailCore(t, accessFree)
	if got := strings.Join(c.brainFreeReads(), ","); got != "mcp__mail__search_messages" {
		t.Errorf("allowed in advance: %s", got)
	}
	if c.freeCall("mcp__mail__move", nil) {
		t.Error("a Free write is a free call")
	}
	if !c.freeCall("mcp__mail__search_messages", nil) {
		t.Error("a Free read is not a free call")
	}
}

// A task: the rule is read at launch, and holds whatever it reads since.
func TestTaskFreeWriteFollowsItsStart(t *testing.T) {
	c := newMailCore(t, accessFree)
	if got := ask(t, c, "t-1", "mail", "search_messages", "move"); got["search_messages"] != "allowed" || got["move"] != "allowed" {
		t.Errorf("a clean start: %v", got)
	}
	if got := ask(t, c, "t-alone", "mail", "search_messages", "move"); got["search_messages"] != "allowed" || got["move"] != "user" {
		t.Errorf("no clean start: %v", got)
	}
}

// "Always allow" lifts On approval, not Never: a server gone read only stays so.
func TestRememberedDoesNotLiftNever(t *testing.T) {
	c := newMailCore(t, accessReadOnly)
	write := "mcp__mail__move"
	if err := c.store.allowAction("mail", write, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decide(write, nil); got != decideDeny {
		t.Errorf("remembered on Never = %q", got)
	}
}

// Never: refused at the proxy, a ticket or not.
func TestNeverAtTheProxy(t *testing.T) {
	c := newMailCore(t, accessReadOnly)
	c.store.addTicket("t-1", "mcp__mail__move", nil)
	out := c.proxyCall(context.Background(), "t-1", []string{"mail"}, c.pool.get("mail"), "move", nil)
	if !out.IsError || !strings.Contains(resultText(out), "not allowed") {
		t.Errorf("a Never with a ticket: %s", resultText(out))
	}
}

func TestListToolsSaysCategoryAndValue(t *testing.T) {
	c := newMailCore(t, "")
	tools := map[string]toolInfo{}
	for _, ti := range c.listServers("mail", true)[0].Tools {
		tools[ti.Name] = ti
	}
	for name, want := range map[string][2]string{
		"search_messages": {catRead, gridFree}, "move": {catChange, gridApproval},
		"send": {catSend, gridApproval}, "unannotated": {catSend, gridApproval},
	} {
		if got := tools[name]; got.Category != want[0] || got.Value != want[1] {
			t.Errorf("%s = %+v", name, got)
		}
	}
}
