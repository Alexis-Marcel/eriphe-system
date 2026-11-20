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
)

// sectionsBlock is a block that shows the monitor's received messages in
// two sections, by their group, with an action on the first.
func sectionsBlock(actions string) string {
	return "```eriphe:block inbox\n" + `{"components": [
  {"id": "root", "component": "Column", "children": ["drop", "info"]},
  {"id": "drop", "component": "List", "children": {"path": "/received/messages[_group=À supprimer]", "componentId": "row"}, "emptyText": "Rien"},
  {"id": "info", "component": "List", "children": {"path": "/received/messages[_group!=À supprimer]", "componentId": "row"}},
  {"id": "row", "component": "Text", "text": {"@path": "subject"}}],
 "sources": {"received": {"resource": "monitor", "uri": "monitor://received", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}},
 "actions": ` + actions + `}` + "\n```\n"
}

const dropAction = `{"drop": {"tool": "monitor/move", "args": {"to": "trash"}, "items": "ids", "source": "received", "list": "drop"}}`

// sectionsWeb is the workspace on a core whose monitor lists m1 to m4, a
// node that shows them in two sections, m1 and m3 grouped « À supprimer »,
// m2 « Pour info », m4 in none.
func sectionsWeb(t *testing.T) (*testWeb, string, *fakeMonitor, string) {
	t.Helper()
	c, fm, _ := viewCore(t)
	fm.mu.Lock()
	fm.got = []map[string]any{
		{"id": "m1", "subject": "Promo", "from": "Shop"},
		{"id": "m2", "subject": "Relance", "from": "Lou"},
		{"id": "m3", "subject": "Lettre", "from": "News"},
		{"id": "m4", "subject": "Facture", "from": "Sam"},
	}
	fm.mu.Unlock()
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n"})
	if _, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
		return n.AuthorBody("Les mails.\n\n" + sectionsBlock(dropAction))
	}); err != nil {
		t.Fatal(err)
	}
	c.readNode(context.Background(), id, nil, time.Now())
	for _, it := range []nodestore.Item{
		{Source: "received", Item: "m1", Group: "À supprimer", Note: "Une promotion", State: "none", Author: byBrain},
		{Source: "received", Item: "m2", Group: "Pour info", Author: byBrain, Tainted: "external:monitor"},
		{Source: "received", Item: "m3", Group: "À supprimer", Author: byBrain},
	} {
		if err := c.nodes.NoteItem(id, it); err != nil {
			t.Fatal(err)
		}
	}
	return tw, csrf, fm, id
}

// A List filters its source's items by a field, = or !=: checked when an
// author writes it; only a List, on a source's items, one filter.
func TestListSectionsChecked(t *testing.T) {
	c, _, _ := viewCore(t)
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n"})
	write := func(body string) error {
		_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
			return n.AuthorBody("Les mails.\n\n" + body)
		})
		return err
	}
	if err := write(sectionsBlock(dropAction)); err != nil {
		t.Fatalf("good sections: %v", err)
	}
	good := sectionsBlock(dropAction)
	for want, body := range map[string]string{
		"a filter follows a source's items": strings.Replace(good, "/received/messages[_group=À", "/received/other[_group=À", 1),
		"a filter is one":                   strings.Replace(good, "[_group=À supprimer]", "[_group]", 1),
		"only a List filters":               strings.Replace(good, `"id": "drop", "component": "List"`, `"id": "drop", "component": "Grid"`, 1),
		"is not a List of the source":       strings.Replace(good, `"list": "drop"`, `"list": "row"`, 1),
	} {
		if err := write(body); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if steps, err := parsePath("rules[state!=ok].name"); err != nil || !steps[1].not || steps[1].field != "state" || steps[1].equals != "ok" {
		t.Fatalf("!=: %+v %v", steps, err)
	}
	v, _ := extract(map[string]any{"rules": []any{map[string]any{"state": "ok", "name": "a"}, map[string]any{"state": "firing", "name": "b"}}}, "rules[state!=ok].name")
	if got, _ := v.([]any); len(got) != 1 || got[0] != "b" {
		t.Fatalf("a monitor's != : %v", v)
	}
}

// Each item of a source carries its note's fields in the block's data,
// never in its file; a List that filters gets its rows from code, in the
// source's order, and the page the List bound to the items as a whole.
func TestListSectionsRows(t *testing.T) {
	tw, _, _, id := sectionsWeb(t)
	c := tw.c
	page, err := tw.w.nodePageOf(id, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	var b *pageBlock
	for _, p := range page.Parts {
		if p.Block != nil {
			b = p.Block
		}
	}
	msgs := b.Data["received"].(map[string]any)["messages"].([]any)
	m1, m2, m4 := msgs[0].(map[string]any), msgs[1].(map[string]any), msgs[3].(map[string]any)
	if m1["_group"] != "À supprimer" || m1["_note"] != "Une promotion" || m1["_state"] != "none" || m1["_external"] != nil {
		t.Fatalf("m1: %v", m1)
	}
	if m2["_group"] != "Pour info" || m2["_external"] != true {
		t.Fatalf("m2, its note tainted: %v", m2)
	}
	if m4["_group"] != "" || m4["_note"] != "" {
		t.Fatalf("m4, not noted: %v", m4)
	}
	raw, _ := json.Marshal(b.Components)
	if strings.Contains(string(raw), "[_group") || !strings.Contains(string(raw), `"path":"/received/messages"`) {
		t.Fatalf("the page's components: %s", raw)
	}
	if len(b.Lists) != 2 {
		t.Fatalf("lists: %+v", b.Lists)
	}
	drop, info := b.Lists[0], b.Lists[1]
	ns := func(l pageList) (out []int) {
		for _, it := range l.Items {
			out = append(out, it.N)
		}
		return out
	}
	if drop.Component != "drop" || !drop.Filtered || !slices.Equal(ns(drop), []int{0, 2}) || len(drop.Actions) != 1 || drop.Actions[0].All != "Déplacer les {n} → trash (monitor/move)" {
		t.Fatalf("drop: %+v", drop)
	}
	if info.Component != "info" || !info.Filtered || !slices.Equal(ns(info), []int{1, 3}) || len(info.Actions) != 0 {
		t.Fatalf("info, no action of its own: %+v", info)
	}
	d, _ := c.nodes.Read(id, 0)
	if strings.Contains(string(d.Raw), "_note") || strings.Contains(string(d.Raw), `"_group"`) {
		t.Fatalf("the notes went into the file: %s", d.Raw)
	}

	// A note that changes: the source as the page draws it, sent again.
	c.useNodes(c.nodes) // watched, as at a start
	seq, _ := c.graphHead()
	if err := c.nodes.NoteItem(id, nodestore.Item{Source: "received", Item: "m4", Group: "À supprimer", Author: byBrain}); err != nil {
		t.Fatal(err)
	}
	evs, _, _, _ := c.graphAfter(seq)
	if !slices.ContainsFunc(evs, func(e graphEvent) bool {
		return e.Kind == graphEvSource && e.Node == id && e.Name == "received" && e.Block == ""
	}) {
		t.Fatalf("no source event: %+v", evs)
	}
	sent := c.sourceEvents(id, "", "received")
	if len(sent) != 1 || sent[0]["block"] != "inbox" {
		t.Fatalf("sent: %v", sent)
	}
	lists := sent[0]["lists"].([]map[string]any)
	if lists[0]["component"] != "drop" || len(lists[0]["items"].([]listItem)) != 3 {
		t.Fatalf("its lists: %v", lists)
	}
}

// An image under a List that filters is the page's as any source's: code
// serves it.
func TestListSectionsMedia(t *testing.T) {
	c, _, _ := viewCore(t)
	b := &nodefile.Block{Name: "pics", Components: json.RawMessage(`[
  {"id": "root", "component": "List", "children": {"path": "/received/messages[_group=x]", "componentId": "pic"}},
  {"id": "pic", "component": "Image", "url": {"@path": "url"}}]`),
		Sources:  map[string]nodefile.Source{"received": {Resource: "monitor", URI: "monitor://received", Items: &nodefile.Items{Path: "messages", ID: "id"}}},
		Snapshot: json.RawMessage(`{"received": {"messages": [{"id": "a", "url": "https://example.org/a.png"}]}}`)}
	v := c.blockView("n1", b, nil)
	got := v.Data["received"].(map[string]any)["messages"].([]any)[0].(map[string]any)["url"].(string)
	if !strings.HasPrefix(got, mediaPath+"?") {
		t.Fatalf("an image under a filtered List: %q", got)
	}

	// A filter that does not read (a file written by hand): the page
	// shows none of the items, never all of them.
	b.Components = json.RawMessage(strings.Replace(string(b.Components), "messages[_group=x]", "other[_group=x]", 1))
	lists := c.blockLists(b, v.Data, "fr")
	if len(lists) != 1 || !lists[0].Filtered || len(lists[0].Items) != 0 {
		t.Fatalf("a filter that does not read: %+v", lists)
	}
}

// The click on a section's action runs on exactly the ids the page sent,
// each an item of the source the List's filter keeps now: one that is
// not is enough for nothing to run, and an item come since is never
// added.
func TestNodeActSection(t *testing.T) {
	tw, csrf, fm, id := sectionsWeb(t)
	c := tw.c
	act := func(in nodeActIn) (int, map[string]any) {
		return gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", in)
	}
	// m2 is not « À supprimer »: refused, nothing runs.
	if code, out := act(nodeActIn{Block: "inbox", List: "drop", Action: "drop", IDs: []string{"m1", "m2"}}); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "1 élément") {
		t.Fatalf("an id the filter does not keep: %d %v", code, out)
	}
	// The action shows on its List only.
	if code, _ := act(nodeActIn{Block: "inbox", List: "info", Action: "drop", IDs: []string{"m2"}}); code != http.StatusConflict {
		t.Fatalf("on another List: %d", code)
	}
	if code, _ := act(nodeActIn{Block: "inbox", Action: "drop", IDs: []string{"m1"}}); code != http.StatusConflict {
		t.Fatalf("no List: %d", code)
	}
	if n := len(sentNow(fm)); n != 0 {
		t.Fatalf("calls after the refusals: %d", n)
	}

	// A mail come since the page showed the section, in its group: the
	// click takes what the page sent, never more.
	fm.mu.Lock()
	fm.got = append(fm.got, map[string]any{"id": "m5", "subject": "Encore", "from": "Shop"})
	fm.mu.Unlock()
	c.readNode(context.Background(), id, nil, time.Now())
	if err := c.nodes.NoteItem(id, nodestore.Item{Source: "received", Item: "m5", Group: "À supprimer", Author: byBrain}); err != nil {
		t.Fatal(err)
	}
	code, out := act(nodeActIn{Block: "inbox", List: "drop", Action: "drop", IDs: []string{"m1", "m3"}})
	if code != http.StatusOK {
		t.Fatalf("act: %d %v", code, out)
	}
	sent := sentNow(fm)
	if len(sent) != 1 || !slices.Equal(sent[0]["ids"].([]string), []string{"m1", "m3"}) {
		t.Fatalf("the tool got %v", sent)
	}

	// A note changed since: the filter as it stands.
	if err := c.nodes.NoteItem(id, nodestore.Item{Source: "received", Item: "m5", Group: "Pour info", Author: byBrain}); err != nil {
		t.Fatal(err)
	}
	if code, _ := act(nodeActIn{Block: "inbox", List: "drop", Action: "drop", IDs: []string{"m5"}}); code != http.StatusBadRequest {
		t.Fatalf("an item regrouped since: %d", code)
	}
}
