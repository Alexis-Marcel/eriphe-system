package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// actionsBlock is a block that lists the monitor's received messages,
// with actions on them (by name, their JSON).
func actionsBlock(actions string) string {
	return "```eriphe:block inbox\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/received/messages", "componentId": "row"}},
  {"id": "row", "component": "Text", "text": {"@path": "subject"}}],
 "sources": {"received": {"resource": "monitor", "uri": "monitor://received", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}},
 "actions": ` + actions + `}` + "\n```\n"
}

const archiveAction = `{"archive": {"label": "Ranger", "tool": "monitor/move", "args": {"to": "archive"}, "items": "ids", "source": "received"}}`

// actionsWeb is the workspace on a core with the monitor server (move
// takes ids), its received messages m1, m2, m3, and a node that lists them
// with the actions given, read once.
func actionsWeb(t *testing.T, actions string) (*testWeb, string, *fakeMonitor, string) {
	t.Helper()
	c, fm, _ := viewCore(t)
	fm.got = []map[string]any{
		{"id": "m1", "subject": "Facture", "from": "Sam"},
		{"id": "m2", "subject": "Relance", "from": "Lou"},
		{"id": "m3", "subject": "Lettre", "from": "Max"},
	}
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n\n" + actionsBlock(actions)})
	c.readNode(context.Background(), id, nil, time.Now())
	return tw, csrf, fm, id
}

// An action is checked when an author writes it: its shape in the block,
// then its tool against the catalog (there, its parameter a list, its
// fixed args held by the schema).
func TestListActionsChecked(t *testing.T) {
	c, _, _ := viewCore(t)
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n"})
	write := func(actions string) error {
		_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
			return n.AuthorBody("Les mails.\n\n" + actionsBlock(actions))
		})
		return err
	}
	if err := write(archiveAction); err != nil {
		t.Fatalf("a good action: %v", err)
	}
	for want, actions := range map[string]string{
		"not a tool of the catalog":   `{"a": {"tool": "monitor/nope", "items": "ids", "source": "received"}}`,
		"is not a list of ids":        `{"a": {"tool": "monitor/move", "args": {"ids": []}, "items": "to", "source": "received"}}`,
		"takes no list of ids":        `{"a": {"tool": "monitor/rollback", "items": "version", "source": "received"}}`,
		"args:":                       `{"a": {"tool": "monitor/move", "args": {"to": 5}, "items": "ids", "source": "received"}}`,
		"source names one":            `{"a": {"tool": "monitor/move", "args": {"to": "archive"}, "items": "ids", "source": "sent"}}`,
		"args has no ids":             `{"a": {"tool": "monitor/move", "args": {"to": "archive", "ids": ["m1"]}, "items": "ids", "source": "received"}}`,
		"tool is <server>/<tool>":     `{"a": {"tool": "move", "items": "ids", "source": "received"}}`,
		"label is one line":           `{"a": {"label": "` + strings.Repeat("x", 70) + `", "tool": "monitor/move", "items": "ids", "source": "received"}}`,
		"a name is lowercase letters": `{"Ranger": {"tool": "monitor/move", "items": "ids", "source": "received"}}`,
	} {
		if err := write(actions); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", actions, err, want)
		}
	}
	// A source with no items: nothing to pick.
	noItems := strings.Replace(actionsBlock(archiveAction), `, "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}`, "", 1)
	_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
		return n.AuthorBody("Les mails.\n\n" + noItems)
	})
	if err == nil || !strings.Contains(err.Error(), "says no items") {
		t.Fatalf("a source with no items: %v", err)
	}
}

// The click runs the action as stored on exactly the ids picked, each one
// the source lists; the tool on approval takes the click as the user's
// yes, kept as such in the journal and the node's trace.
func TestNodeActRunsThePicked(t *testing.T) {
	tw, csrf, fm, id := actionsWeb(t, archiveAction)
	c := tw.c
	d, _ := c.nodes.Read(id, 0)
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "archive", Version: d.Version.N, IDs: []string{"m1", "m3"}})
	if code != http.StatusOK || out["text"] != "2 déplacés" {
		t.Fatalf("act: %d %v", code, out)
	}
	sent := sentNow(fm)
	if len(sent) != 1 || sent[0]["tool"] != "move" || sent[0]["to"] != "archive" || !slices.Equal(sent[0]["ids"].([]string), []string{"m1", "m3"}) {
		t.Fatalf("the tool got %v", sent)
	}
	rows, _ := c.store.decisions(0, 5)
	if len(rows) == 0 || rows[0].By != byUser || rows[0].Decision != decideAllow || !strings.Contains(rows[0].Reason, "vaut son oui") {
		t.Fatalf("journal: %+v", rows)
	}
	raw, _ := c.nodes.Trace(id)
	if tr := string(raw); !strings.Contains(tr, "· user") || !strings.Contains(tr, "Déplacer 2 éléments → archive (monitor/move)") || !strings.Contains(tr, "son clic vaut son oui") || strings.Contains(tr, "Facture") {
		t.Fatalf("trace: %s", raw)
	}

	// Ids the source does not list, an action the block has not: nothing runs.
	if code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "archive", IDs: []string{"m1", "m9"}}); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "1 élément") {
		t.Fatalf("an id not listed: %d %v", code, out)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "trash", IDs: []string{"m1"}}); code != http.StatusConflict {
		t.Fatalf("an action not in the block: %d", code)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "archive"}); code != http.StatusBadRequest {
		t.Fatalf("no ids: %d", code)
	}
	if n := len(sentNow(fm)); n != 1 {
		t.Fatalf("calls after the refusals: %d", n)
	}

	// The action changed since the page showed it: refused.
	shown := d.Version.N
	changed := strings.Replace(archiveAction, `"archive"}`, `"inbox"}`, 1)
	if _, err := c.nodes.Change(id, lastHash(t, c, id), byBrain, func(n *nodefile.Node) error {
		return n.AuthorBody("Les mails.\n\n" + actionsBlock(changed))
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "archive", Version: shown, IDs: []string{"m2"}}); code != http.StatusConflict {
		t.Fatalf("a changed action: %d", code)
	}
	if n := len(sentNow(fm)); n != 1 {
		t.Fatalf("calls after a changed action: %d", n)
	}
}

// A tool the policy never allows is refused, whoever clicks.
func TestNodeActNever(t *testing.T) {
	tw, csrf, fm, id := actionsWeb(t, archiveAction)
	setLevel(t, tw.c, "monitor", accessReadOnly)
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "archive", IDs: []string{"m1"}})
	if code != http.StatusForbidden || !strings.Contains(out["error"].(string), "jamais permis") {
		t.Fatalf("never: %d %v", code, out)
	}
	if n := len(sentNow(fm)); n != 0 {
		t.Fatalf("the tool ran %d times", n)
	}
}

// The page gets the List the user picks in, its rows and the buttons'
// words, code's: the author's label only as a hint, never on an external
// block; a tool that throws away asks once more.
func TestBlockListsWords(t *testing.T) {
	tw, _, _, id := actionsWeb(t, `{"archive": {"label": "Ranger", "tool": "monitor/move", "args": {"to": "archive"}, "items": "ids", "source": "received"},
 "drop": {"label": "Archiver tout", "tool": "monitor/move", "args": {"to": "trash bin"}, "items": "ids", "source": "received"}}`)
	c := tw.c
	d, _ := c.nodes.Read(id, 0)
	b := d.Node.Block("inbox")
	view := c.blockView(id, b, c.nodeReads(id))
	b.External = false
	lists := c.blockLists(b, view.Data, "fr")
	if len(lists) != 1 || lists[0].Component != "root" || lists[0].Source != "received" || len(lists[0].Items) != 3 {
		t.Fatalf("lists: %+v", lists)
	}
	if it := lists[0].Items[1]; it.N != 1 || it.ID != "m2" || it.Title != "Relance" || it.Detail != "Lou" {
		t.Fatalf("row: %+v", it)
	}
	a := lists[0].Actions[0]
	if a.Name != "archive" || a.One != "Déplacer 1 élément → archive (monitor/move)" || a.Many != "Déplacer {n} éléments → archive (monitor/move)" || a.Hint != "Ranger" || a.Confirm {
		t.Fatalf("archive: %+v", a)
	}
	if a.Tool != "monitor/move" || !slices.Equal(a.Args, []string{"to : archive"}) {
		t.Fatalf("its call: %+v", a)
	}
	b.External = true
	for _, a := range c.blockLists(b, view.Data, "en") {
		if a.Actions[0].Hint != "Ranger" || !a.Actions[0].HintExternal || !a.Actions[1].HintExternal {
			t.Fatalf("an external block's hint is not shown marked: %+v", a.Actions)
		}
		if a.Actions[0].One != "Move 1 item → archive (monitor/move)" {
			t.Fatalf("in English: %q", a.Actions[0].One)
		}
	}
	page, _ := tw.w.nodePageOf(id, 0, "fr")
	raw, _ := json.Marshal(page)
	if !strings.Contains(string(raw), `"lists":[{"component":"root","source":"received"`) {
		t.Fatalf("the page has no lists: %s", raw)
	}
}

// A tool that says it throws away, or whose name or description does,
// asks once more.
func TestDestructiveLooks(t *testing.T) {
	c, _, _ := viewCore(t)
	_, move := c.actionTool("monitor/move")
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
