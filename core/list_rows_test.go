package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rowsBlock is a block that lists the monitor's received messages; extra
// is more of its fields, after its sources.
func rowsBlock(extra string) string {
	return "```eriphe:block inbox\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/received/messages", "componentId": "row"}},
  {"id": "row", "component": "Text", "text": {"@path": "subject"}}],
 "sources": {"received": {"resource": "monitor", "uri": "monitor://received", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}}` + extra + `}` + "\n```\n"
}

// rowsWeb is the workspace on a core with the monitor server, its
// received messages m1, m2, m3, and a node that lists them, read once.
func rowsWeb(t *testing.T) (*testWeb, string, *fakeMonitor, string) {
	t.Helper()
	c, fm, _ := viewCore(t)
	fm.got = []map[string]any{
		{"id": "m1", "subject": "Facture", "from": "Sam"},
		{"id": "m2", "subject": "Relance", "from": "Lou"},
		{"id": "m3", "subject": "Lettre", "from": "Max"},
	}
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n\n" + rowsBlock("")})
	c.readNode(context.Background(), id, nil, time.Now())
	return tw, csrf, fm, id
}

// A block's actions and marks are gone: a block that says them does not
// read, whoever writes it. A row's yes is its card's, drawn by code.
func TestNoBlockActions(t *testing.T) {
	c, _, _ := viewCore(t)
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n"})
	for _, extra := range []string{
		`, "actions": {"archive": {"tool": "monitor/move", "items": "ids", "source": "received"}}`,
		`, "marks": {"titles": {"t1": "keep"}}`,
	} {
		_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
			return n.AuthorBody("Les mails.\n\n" + rowsBlock(extra))
		})
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("%s: %v", extra, err)
		}
	}
}

// The page gets a List's rows only when it must know them: a card waits
// on one of them, it filters them, it opens them, or a Fold names it.
// Each row says its place, its id, what it shows and its card.
func TestBlockListsRows(t *testing.T) {
	tw, _, _, id := rowsWeb(t)
	c := tw.c
	d, _ := c.nodes.Read(id, 0)
	b := d.Node.Block("inbox")
	view := c.blockView(id, b, c.nodeReads(id))
	if lists := c.blockLists(b, view.Data); len(lists) != 0 {
		t.Fatalf("a plain List with no card on its rows: %+v", lists)
	}
	// A card on m2 (the brain's call on it): the page knows the List.
	labels, _ := json.Marshal([]itemLabel{{ID: "m2", Source: "received"}})
	res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, server, category, call, input, created, node, source, labels)
		VALUES ('t', 'monitor', 'change', 'mcp__monitor__move {}', '{"ids":["m2"]}', ?, ?, 'received', ?)`, time.Now().Unix(), id, string(labels))
	ask, _ := rowsID(res, err)
	view = c.blockView(id, b, c.nodeReads(id))
	lists := c.blockLists(b, view.Data)
	if len(lists) != 1 || lists[0].Component != "root" || lists[0].Source != "received" || len(lists[0].Items) != 3 {
		t.Fatalf("lists: %+v", lists)
	}
	if it := lists[0].Items[1]; it.N != 1 || it.ID != "m2" || it.Title != "Relance" || it.Detail != "Lou" || it.Card != proposalRef(cardAccess, ask) {
		t.Fatalf("row: %+v", it)
	}
	if it := lists[0].Items[0]; it.Card != "" {
		t.Fatalf("m1 has no card: %+v", it)
	}
	page, _ := tw.w.nodePageOf(id, 0, "fr")
	raw, _ := json.Marshal(page)
	if !strings.Contains(string(raw), `"lists":[{"component":"root","source":"received"`) || strings.Contains(string(raw), `"actions"`) {
		t.Fatalf("the page's lists: %s", raw)
	}
}

// A tool that says it throws away, or whose name or description does,
// looks destructive.
func TestDestructiveLooks(t *testing.T) {
	c, _, _ := viewCore(t)
	_, move := c.catalogTool("monitor/move")
	if destructive(move) {
		t.Fatal("move is not destructive")
	}
	for _, w := range []string{"trash", "Delete", "remove"} {
		if !destructive(&mcp.Tool{Name: "x", Description: "It will " + w + " them"}) {
			t.Errorf("%s: not destructive", w)
		}
	}
	yes := true
	if !destructive(&mcp.Tool{Name: "x", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes}}) {
		t.Error("destructiveHint: not destructive")
	}
}
