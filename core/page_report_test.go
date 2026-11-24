package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A page that stays true (#749): its author hears what it shows once
// written (the render report), its figures count what its sources list,
// and its setup buttons leave once what they set up is.

// pageFence is a block's fence, its fields as JSON.
func pageFence(name string, fields map[string]any) string {
	raw, _ := json.Marshal(fields)
	return "```eriphe:block " + name + "\n" + string(raw) + "\n```\n"
}

// reportOf is one block's report among a write's, as JSON gives it.
func reportOf(t *testing.T, out map[string]any, block string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(out["page"])
	var page []map[string]any
	json.Unmarshal(raw, &page)
	for _, b := range page {
		if b["block"] == block {
			return b
		}
	}
	t.Fatalf("no report of block %s in %v", block, out["page"])
	return nil
}

func TestPageReportAndSetupButtons(t *testing.T) {
	c, fm, _ := viewCore(t)
	c.setServerTrust("monitor", true, "user")
	fm.mu.Lock()
	fm.got = []map[string]any{{"id": "m1", "subject": "Devis", "from": "sam@example.org"}, {"id": "m2", "subject": "Facture", "from": "lina@example.org"}}
	fm.mu.Unlock()
	mails := map[string]any{"resource": "monitor", "uri": "monitor://received", "items": map[string]any{"path": "messages", "id": "id", "title": "subject"}}
	recus := pageFence("recus", map[string]any{
		"components": []map[string]any{
			{"id": "root", "component": "Column", "children": []string{"chiffres", "liste", "rien", "mettre"}},
			{"id": "chiffres", "component": "Row", "children": []string{"compte", "fige"}},
			{"id": "compte", "component": "Metric", "label": "À lire", "list": "liste"},
			{"id": "fige", "component": "Metric", "label": "Portefeuille", "value": 18385, "unit": "€"},
			{"id": "liste", "component": "List", "children": map[string]any{"path": "/mails/messages", "componentId": "ligne"}},
			{"id": "ligne", "component": "Text", "text": map[string]any{"@path": "subject"}},
			{"id": "rien", "component": "Text", "text": map[string]any{"@path": "/mails/total"}},
			{"id": "mettre", "component": "Button", "label": "Surveiller mes mails", "until": map[string]any{"monitor": "mails"},
				"action": map[string]any{"event": map[string]any{"name": "ask", "context": map[string]any{"text": "Surveille mes mails"}}}},
		},
		"sources": map[string]any{"mails": mails},
	})
	etat := pageFence("etat", map[string]any{
		"components": map[string]any{"component": "Text", "text": map[string]any{"@path": "/web/data/state"}},
		"sources":    map[string]any{"web": map[string]any{"tool": "monitor/status", "args": map[string]any{"service": "web"}}},
	})
	fm.mu.Lock()
	fm.failing = true
	fm.mu.Unlock()

	out, isErr := callBrainTool(t, c, "propose_view", map[string]any{
		"title": "Mes mails", "goal": "Sam voit ses mails.", "body": recus + etat,
		"monitor": map[string]any{"name": "mails", "server": "monitor", "resource": "monitor://received", "cond": "new", "path": "messages", "id": "id", "every": "1m"},
	})
	if isErr {
		t.Fatalf("propose_view: %v", out)
	}
	node, _ := out["node"].(string)
	raw, _ := json.Marshal(out)
	var said map[string]any
	json.Unmarshal(raw, &said)
	page, _ := said["page"].([]any)
	byBlock := map[string]map[string]any{}
	for _, b := range page {
		m, _ := b.(map[string]any)
		byBlock[m["block"].(string)] = m
	}

	// The list's rows, counted; the binding that leads nowhere in what
	// the source gave; the figure written by hand on a page that lasts.
	r := byBlock["recus"]
	if rows, _ := r["rows"].(map[string]any); rows["liste"] != float64(2) {
		t.Errorf("rows %v", r["rows"])
	}
	problems, _ := json.Marshal(r["problems"])
	for _, want := range []string{"Text rien: nothing at /mails/total in what the source mails gave (there: messages)", "Metric fige"} {
		if !strings.Contains(string(problems), want) {
			t.Errorf("the report lacks %q: %s", want, problems)
		}
	}
	if strings.Contains(string(problems), "compte") {
		t.Errorf("a Metric counting a List is no figure written by hand: %s", problems)
	}
	if r["state"] != pageError || r["sources"].(map[string]any)["mails"] != pageOK || r["hidden"] != nil {
		t.Errorf("recus: %v", r)
	}
	// A source that fails: said, its trusted server's words with it.
	e := byBlock["etat"]
	if src, _ := e["sources"].(map[string]any)["web"].(string); e["state"] != pageError || !strings.HasPrefix(src, "failed: ") {
		t.Errorf("etat: %v", e)
	}

	// The page knows the rows the Metric counts, as a batch's: they come
	// with the List, and again at each read of its source.
	d, _ := c.nodes.Read(node, 0)
	v := c.blockView(node, d.Node.Block("recus"), c.keptReads(node))
	lists := c.blockLists(d.Node.Block("recus"), v.Data)
	if len(lists) != 1 || lists[0].Component != "liste" || len(lists[0].Items) != 2 {
		t.Fatalf("lists %+v", lists)
	}
	if !slices.ContainsFunc(v.Components, func(c2 a2ui.Component) bool { return c2.ID == "mettre" }) {
		t.Fatal("the setup button is hidden before its monitor runs")
	}

	// Yes: the monitor runs, the button leaves the page, and the column
	// no longer names it.
	views := cardsOf(c, cardViewKind)
	if err := c.decideProposal(context.Background(), cardViewKind, views[0].ID, true); err != nil {
		t.Fatal(err)
	}
	c.live.wg.Wait()
	d, _ = c.nodes.Read(node, 0)
	v = c.blockView(node, d.Node.Block("recus"), c.keptReads(node))
	for _, comp := range v.Components {
		if comp.ID == "mettre" {
			t.Fatal("the setup button stays once its monitor runs")
		}
		if comp.ID == "root" && strings.Contains(string(comp.Props["children"]), "mettre") {
			t.Fatalf("root still names it: %s", comp.Props["children"])
		}
	}
	// Its author hears it at its next write.
	c.turn.start(0, 0, 0)
	w, err := c.brainNodeWrite(node, d.Version.Hash, func(n *nodefile.Node) error { return n.AuthorBody(recus) })
	if err != nil {
		t.Fatal(err)
	}
	if hidden, _ := json.Marshal(reportOf(t, w, "recus")["hidden"]); string(hidden) != `["mettre"]` {
		t.Errorf("hidden %s", hidden)
	}
}

func TestWithoutComponents(t *testing.T) {
	comps := func() []map[string]any {
		var out []map[string]any
		json.Unmarshal([]byte(`[
			{"id": "root", "component": "Column", "children": ["haut", "carte", "onglets"]},
			{"id": "haut", "component": "Row", "children": ["a"]},
			{"id": "a", "component": "Button"},
			{"id": "carte", "component": "Card", "child": "b"},
			{"id": "b", "component": "Button"},
			{"id": "onglets", "component": "Tabs", "tabs": [{"title": "Un", "child": "c"}, {"title": "Deux", "child": "t"}]},
			{"id": "c", "component": "Button"},
			{"id": "t", "component": "Text", "text": "reste"}
		]`), &out)
		return out
	}
	ids := func(cs []map[string]any) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c["id"].(string))
		}
		return out
	}
	got := withoutComponents(comps(), []string{"a", "b", "c"})
	if want := []string{"root", "onglets", "t"}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v", ids(got), want)
	}
	if kids, _ := json.Marshal(got[0]["children"]); string(kids) != `["onglets"]` {
		t.Errorf("root's children %s", kids)
	}
	if tabs, _ := got[1]["tabs"].([]any); len(tabs) != 1 {
		t.Errorf("tabs %v", got[1]["tabs"])
	}
	// All of it gone: an empty root.
	got = withoutComponents(comps(), []string{"a", "b", "c", "t"})
	if len(got) != 1 || got[0]["component"] != "Column" {
		t.Fatalf("all gone: %v", got)
	}
}

// A source of a server not trusted: the report says what fails and what
// leads nowhere in code's words only, never the source's (its error, its
// keys), and the turn stays clean.
func TestPageReportQuotesNoUntrustedWords(t *testing.T) {
	c, fm, _ := viewCore(t)
	fm.mu.Lock()
	fm.failing = true
	fm.mu.Unlock()
	body := pageFence("recus", map[string]any{
		"components": map[string]any{"component": "Text", "text": map[string]any{"@path": "/mails/total"}},
		"sources":    map[string]any{"mails": map[string]any{"resource": "monitor", "uri": "monitor://received"}},
	}) + pageFence("etat", map[string]any{
		"components": map[string]any{"component": "Text", "text": map[string]any{"@path": "/web/data/state"}},
		"sources":    map[string]any{"web": map[string]any{"tool": "monitor/status", "args": map[string]any{"service": "web"}}},
	})
	c.turn.start(0, 0, 0)
	out := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Mes mails", "body": body})
	r := reportOf(t, out, "recus")
	problems, _ := json.Marshal(r["problems"])
	if !strings.Contains(string(problems), "nothing at /mails/total in what the source mails gave") || strings.Contains(string(problems), "there:") {
		t.Errorf("recus: %s", problems)
	}
	if e := reportOf(t, out, "etat"); e["sources"].(map[string]any)["web"] != "failed" {
		t.Errorf("etat: %v", e)
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("the report tainted the turn: %v", tainted)
	}
}

// A page whose source is node_find, read again by the write itself: the
// report waits for that read, and counts its rows.
func TestPageReportWaitsForTheRead(t *testing.T) {
	c := graphCore(t)
	c.useNodes(c.nodes)
	openStudy(t, c, "Étude sur le sommeil", map[string]any{"tags": []any{"etude"}})
	openStudy(t, c, "Étude sur le bruit", map[string]any{"tags": []any{"etude"}})
	body := pageFence("etudes", map[string]any{
		"components": map[string]any{"component": "Table", "columns": []map[string]any{{"key": "title", "label": "Étude"}}, "rows": map[string]any{"@path": "/etudes/nodes"}},
		"sources":    map[string]any{"etudes": map[string]any{"tool": "eriphe-core/node_find", "args": map[string]any{"tags": []string{"etude"}}}},
	})
	out := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Mes études", "aside": true, "body": body})
	r := reportOf(t, out, "etudes")
	if r["state"] != pageOK || r["rows"].(map[string]any)["root"] != float64(2) {
		t.Fatalf("report %v", r)
	}
	// None found: empty, said so.
	id, _ := out["id"].(string)
	d, _ := c.nodes.Read(id, 0)
	none := strings.Replace(body, `"etude"`, `"cuisine"`, 1)
	w, err := c.brainNodeWrite(id, d.Version.Hash, func(n *nodefile.Node) error { return n.AuthorBody(none) })
	if err != nil {
		t.Fatal(err)
	}
	if r := reportOf(t, w, "etudes"); r["state"] != pageEmpty || r["rows"].(map[string]any)["root"] != float64(0) {
		t.Fatalf("report %v", r)
	}
}

// What a setup button sets up, read from code's records: a tool
// connected (one Eriphe provides only when it offers something), a
// monitor that runs, a skill approved.
func TestSetUp(t *testing.T) {
	c, _, _ := viewCore(t)
	tool := func(name string) bool { return c.setUp(map[string]any{"tool": name}) }
	if !tool("monitor") {
		t.Error("a connected server is not set up")
	}
	if tool("github") {
		t.Error("a server not in the catalog is set up")
	}
	u := c.pool.get("monitor")
	u.mu.Lock()
	u.status = statusNeedsAuth
	u.mu.Unlock()
	if tool("monitor") {
		t.Error("a server waiting for its authorization is set up")
	}
	c.pool.mu.Lock()
	c.pool.ups["drive"] = &upstream{p: c.pool, name: "drive", status: statusReady, provided: &providedUpstream{offered: func() bool { return false }}}
	c.pool.mu.Unlock()
	if tool("drive") {
		t.Error("a provided server offering nothing is set up")
	}
	if c.setUp(map[string]any{"monitor": "mails"}) {
		t.Error("a monitor that does not exist is set up")
	}
	k, _, err := c.store.addSkillVersion(skillFiles(skillMD), "upload")
	if err != nil {
		t.Fatal(err)
	}
	if c.setUp(map[string]any{"skill": k.Name}) {
		t.Error("a skill waiting for its approval is set up")
	}
	if err := c.store.approveSkill(k.Name, k.Pending); err != nil {
		t.Fatal(err)
	}
	if !c.setUp(map[string]any{"skill": k.Name}) {
		t.Error("an approved skill is not set up")
	}
	if c.setUp(map[string]any{}) {
		t.Error("nothing named is set up")
	}
}
