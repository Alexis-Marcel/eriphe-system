package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// memory.trusted_servers is migrated into the store at start-up, each name
// once: a mark the user removed is not put back, a name added later is.
func TestTrustedServersMigration(t *testing.T) {
	c, _ := testMemoryCore(t) // its configuration trusts "vault"
	if !c.trustsServer("vault") || c.trustsServer("mail") {
		t.Fatalf("migrated: %v", c.trustedServers())
	}
	if err := c.setServerTrust("vault", false, trustByUser); err != nil {
		t.Fatal(err)
	}
	c.base.Memory.TrustedServers = []string{"vault", "notes"}
	c.migrateTrustedServers() // a restart
	if c.trustsServer("vault") {
		t.Fatal("a mark the user removed came back")
	}
	if !c.trustsServer("notes") {
		t.Fatal("a name added to the file was not taken")
	}
}

// A tool taints unless it is marked trusted: the brain's turn through the
// proxy and through its events.
func TestUntrustedToolTaints(t *testing.T) {
	c, _ := testMemoryCore(t)
	ownerSays(c, "Regarde mes notes")
	c.calledCatalog("t-brain", "notes", "read_note")
	if tainted := c.turnTaint(); !containsStr(tainted, "notes.read_note") {
		t.Fatalf("an untrusted tool: %v", tainted)
	}
	c.setServerTrust("notes", true, trustByUser)
	ownerSays(c, "Et encore")
	c.calledCatalog("t-brain", "notes", "read_note")
	if tainted := c.turnTaint(); len(tainted) != 0 {
		t.Fatalf("a tool marked trusted: %v", tainted)
	}
	// Seen in its events, the same.
	c.store.addEvents("t-brain", []EventRow{
		{Seq: 1, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "mcp__notes__read_note"}},
		{Seq: 2, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "mcp__mail__search"}},
	})
	if tainted := c.turnTaint(); len(tainted) != 1 || tainted[0] != "mcp__mail__search" {
		t.Fatalf("events: %v", tainted)
	}
}

// A tool removed from the catalog leaves an untrusted mark: a new tool of
// the same name starts untrusted.
func TestRemovedToolForgetsItsTrust(t *testing.T) {
	cc := newDynamicCore(t, nil)
	srv := Server{URL: "https://notes.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "notes"}
	if err := cc.putServer(putCreate, "notes", srv, nil); err != nil {
		t.Fatal(err)
	}
	cc.setServerTrust("notes", true, trustByUser)
	if err := cc.deleteServer("notes"); err != nil {
		t.Fatal(err)
	}
	if err := cc.putServer(putCreate, "notes", srv, nil); err != nil {
		t.Fatal(err)
	}
	if cc.trustsServer("notes") {
		t.Fatal("a new tool inherited a removed one's trust")
	}
}

// Settings → Tools: the mark on the tool's page, any tool's (the
// configuration file's too), and a tag on its row; list_tools says it.
func TestWebToolTrust(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/docs/access", nil, nil)
	if !strings.Contains(body, `id="trust-docs"`) || !strings.Contains(body, "De confiance") || strings.Contains(body, `name="trusted" value="1" checked`) {
		t.Fatalf("access page: %s", body)
	}
	code, body, _ := tw.post("/settings/tools/docs/trust", url.Values{"set": {"1"}, "trusted": {"1"}}, csrf)
	if code != 200 || !c.trustsServer("docs") || !strings.Contains(body, `name="trusted" value="1" checked`) {
		t.Fatalf("mark: %d %v %s", code, c.trustsServer("docs"), body)
	}
	if !c.listServers("docs", false)[0].TrustedContent {
		t.Fatal("list_tools does not say it")
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "#trust-docs") || !strings.Contains(body, "de confiance") {
		t.Fatalf("tools page: %s", body)
	}
	if code, _, _ := tw.post("/settings/tools/docs/trust", url.Values{"set": {"1"}}, csrf); code != 200 || c.trustsServer("docs") {
		t.Fatalf("unmark: %d %v", code, c.trustsServer("docs"))
	}
	if code, _, _ := tw.post("/settings/tools/nothing/trust", url.Values{"set": {"1"}, "trusted": {"1"}}, csrf); code != 400 || c.trustsServer("nothing") {
		t.Fatalf("unknown tool: %d", code)
	}
}
