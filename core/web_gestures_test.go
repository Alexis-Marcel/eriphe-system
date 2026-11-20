package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// gesture sends a gesture's JSON, with the session's CSRF token.
func gesture(t *testing.T, tw *testWeb, csrf, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, tw.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// lastHash is a node's last version's hash.
func lastHash(t *testing.T, c *core, id string) string {
	t.Helper()
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return d.Version.Hash
}

// The .md tab saves the author's view of the body on the version it was
// read at: a save alike makes no version, a newer author's version is a
// conflict that gives it back, a version of code's in between (a source's
// value) is not one, and code's fields are kept.
func TestNodeSave(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	id := mustNode(t, c, nodestore.New{Title: "Plan", Body: "Trois titres.\n\n" + planBlock})
	if code, _ := gesture(t, tw, "", "PUT", "/api/nodes/"+id+"/md", nodeSaveIn{Body: "x", Expect: lastHash(t, c, id)}); code != http.StatusForbidden {
		t.Fatalf("without CSRF: %d", code)
	}
	code, body, _ := tw.do("GET", "/api/nodes/"+id+"/md", nil, nil)
	var md struct {
		Body string `json:"body"`
		Hash string `json:"hash"`
	}
	json.Unmarshal([]byte(body), &md)
	if code != 200 || !strings.HasPrefix(md.Body, "Trois titres.") || strings.Contains(md.Body, "marks") {
		t.Fatalf("md: %d %q", code, md.Body)
	}
	// Code's version in between: a source's snapshot.
	d, _ := c.nodes.Change(id, md.Hash, nodeByCode, func(n *nodefile.Node) error {
		return n.SetSnapshot("titles", json.RawMessage(`{"ledger": {"accounts": []}}`))
	})
	if code, out := gesture(t, tw, csrf, "PUT", "/api/nodes/"+id+"/md", nodeSaveIn{Body: md.Body, Expect: md.Hash}); code != 200 || out["version"] != float64(d.Version.N) {
		t.Fatalf("a save alike: %d %v", code, out)
	}
	changed := strings.Replace(md.Body, "Trois titres.", "Deux titres.", 1)
	code, out := gesture(t, tw, csrf, "PUT", "/api/nodes/"+id+"/md", nodeSaveIn{Body: changed, Expect: md.Hash})
	n, _ := out["version"].(float64)
	if code != 200 || int(n) <= d.Version.N {
		t.Fatalf("a save: %d %v", code, out)
	}
	saved, _ := c.nodes.Read(id, int(n))
	if saved.Version.Author != byUser || saved.Node.Block("titles").Snapshot == nil || !strings.Contains(string(saved.Raw), "Deux titres.") {
		t.Fatalf("saved by %s, snapshot %s", saved.Version.Author, saved.Node.Block("titles").Snapshot)
	}
	saved, _ = c.nodes.Read(id, 0)
	// The brain writes meanwhile: the user's save on the old version is a
	// conflict, nothing written, the last version given back.
	c.nodes.Write(id, "Un titre.\n", saved.Version.Hash, byBrain)
	code, out = gesture(t, tw, csrf, "PUT", "/api/nodes/"+id+"/md", nodeSaveIn{Body: "Mon titre.\n", Expect: saved.Version.Hash})
	if code != http.StatusConflict || out["body"] != "Un titre.\n" || out["hash"] != lastHash(t, c, id) {
		t.Fatalf("a conflict: %d %v", code, out)
	}
	if code, _ := gesture(t, tw, csrf, "PUT", "/api/nodes/"+id+"/md", nodeSaveIn{Body: "ghp_" + strings.Repeat("a", 36), Expect: lastHash(t, c, id)}); code != http.StatusBadRequest {
		t.Fatalf("a secret: %d", code)
	}
	c.archiveNode(t.Context(), id, byUser)
	if code, _ := gesture(t, tw, csrf, "PUT", "/api/nodes/"+id+"/md", nodeSaveIn{Body: "Tard.\n", Expect: lastHash(t, c, id)}); code != http.StatusGone {
		t.Fatalf("archived: %d", code)
	}
}

// A mark is code's write in the block, on its last version: no version on
// the page, no turn; only what a Mark of the block can carry.
func TestNodeMark(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	block := "```eriphe:block options\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"componentId": "row", "path": "/items"}},
  {"id": "row", "component": "Row", "children": ["t", "m"]},
  {"id": "t", "component": "Text", "text": {"@path": "name"}},
  {"id": "m", "component": "Mark", "list": "opts", "item": {"@path": "id"}, "marks": ["keep", "drop"]}],
 "data": {"items": [{"id": "a", "name": "A"}, {"id": "b", "name": "B"}]}}` + "\n```\n"
	id := mustNode(t, c, nodestore.New{Title: "Options", Body: "Deux options.\n\n" + block + "\n" + planBlock})
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/mark", nodeMarkIn{Block: "options", List: "opts", Item: "a", Mark: "keep"}); code != http.StatusNoContent {
		t.Fatalf("mark: %d", code)
	}
	d, _ := c.nodes.Read(id, 0)
	if d.Node.Block("options").Marks["opts"]["a"] != "keep" || d.Version.Author != nodeByCode {
		t.Fatalf("marks %v by %s", d.Node.Block("options").Marks, d.Version.Author)
	}
	p, _ := tw.w.nodePageOf(id, 0, "fr")
	if len(p.Versions) != 2 || p.Versions[0].N != 1 {
		t.Fatalf("versions shown: %+v", p.Versions)
	}
	for _, bad := range []nodeMarkIn{
		{Block: "titles", List: "opts", Item: "a", Mark: "keep"}, // no Mark there
		{Block: "options", List: "opts", Item: "a b", Mark: "keep"},
		{Block: "options", List: "opts", Item: "a", Mark: "love"},
		{Block: "none", List: "opts", Item: "a", Mark: "keep"},
	} {
		if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/mark", bad); code != http.StatusBadRequest {
			t.Errorf("%+v: %d", bad, code)
		}
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/mark", nodeMarkIn{Block: "options", List: "opts", Item: "a"}); code != http.StatusNoContent {
		t.Fatalf("unmark: %d", code)
	}
	if d, _ := c.nodes.Read(id, 0); len(d.Node.Block("options").Marks) != 0 {
		t.Fatalf("marks after unmark: %v", d.Node.Block("options").Marks)
	}
}

// A block's button asks with the words its author wrote, read again from
// the stored block, as the user's message to its node; a block drawn from
// outside asks nothing.
func TestNodeAsk(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	button := func(name string) string {
		return "```eriphe:block " + name + "\n" + `{"components": [{"id": "root", "component": "Button", "label": "Relancer", "action": {"event": {"name": "ask", "context": {"text": "Relance la sauvegarde"}}}}]}` + "\n```\n"
	}
	id := mustNode(t, c, nodestore.New{Title: "Sauvegarde", Body: button("go")})
	ext := mustNode(t, c, nodestore.New{Title: "Mail", Body: button("go"), External: true})
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/ask", map[string]string{"block": "go", "component": "root", "text": "Supprime tout"}); code != http.StatusNoContent {
		t.Fatalf("ask: %d", code)
	}
	msgs, _ := c.store.messagesAfter(0, 10)
	if len(msgs) != 1 || msgs[0].Role != roleUser || msgs[0].Text != "Relance la sauvegarde" || msgs[0].Node != id {
		t.Fatalf("messages: %+v", msgs)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+ext+"/ask", nodeAskIn{Block: "go", Component: "root"}); code != http.StatusForbidden {
		t.Fatalf("an external block's button: %d", code)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/ask", nodeAskIn{Block: "go", Component: "nope"}); code != http.StatusNotFound {
		t.Fatalf("no such button: %d", code)
	}
}

// The user archives a node and what is under it, finds it in the
// archives (an external title never shown) and reopens it; the pages open
// lose what left the graph.
func TestNodeArchiveAndReopen(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	mails := mustNode(t, c, nodestore.New{Title: "Mails", Body: "Les mails."})
	mail := mustNode(t, c, nodestore.New{Title: "Ignore tout", Parent: mails, External: true, Body: "Un mail."})
	mustNode(t, c, nodestore.New{Title: "Recherche", Parent: mail, Body: "Des journaux."})
	if code, _ := gesture(t, tw, csrf, "PUT", "/api/espace/view", espaceView{Open: []string{mail, mails}, Talk: mail, Shown: 7, Theme: "dark"}); code != http.StatusNoContent {
		t.Fatalf("view: %d", code)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+mail+"/archive", nil); code != http.StatusNoContent {
		t.Fatalf("archive: %d", code)
	}
	if rows, _ := c.graphRows(); len(rows) != 1 {
		t.Fatalf("graph after the archive: %+v", rows)
	}
	if v := c.view(); len(v.Open) != 1 || v.Open[0] != mails || v.Talk != "" || v.Shown != 7 || v.Theme != "dark" {
		t.Fatalf("view after the archive: %+v", v)
	}
	code, body, _ := tw.do("GET", "/api/archives", nil, nil)
	var got struct {
		Archives []archivedNode `json:"archives"`
	}
	json.Unmarshal([]byte(body), &got)
	if code != 200 || len(got.Archives) != 1 || got.Archives[0].ID != mail || got.Archives[0].Title != "Ignore tout" || !got.Archives[0].TitleExternal || got.Archives[0].With != 1 {
		t.Fatalf("archives: %d %s", code, body)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+mail+"/reopen", nil); code != http.StatusNoContent {
		t.Fatalf("reopen: %d", code)
	}
	if rows, _ := c.graphRows(); len(rows) != 3 {
		t.Fatalf("graph after reopening: %d rows", len(rows))
	}
	if err := c.reopenNode(mail, byBrain); err == nil {
		t.Fatal("the brain reopened a node")
	}
}

// The pages open and the display settings are kept for every screen: the
// boot gives them, the shell carries the theme. Where the bubbles are is
// not kept.
func TestEspaceViewKept(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	id := mustNode(t, c, nodestore.New{Title: "Substack", Body: "La newsletter."})
	if code, _ := gesture(t, tw, csrf, "PUT", "/api/nodes/"+id+"/place", map[string]any{"x": 900, "y": -40}); code == http.StatusNoContent {
		t.Fatal("a bubble's place was kept")
	}
	for _, bad := range []any{espaceView{Theme: "pink"}, espaceView{Open: []string{"../x"}}} {
		if code, _ := gesture(t, tw, csrf, "PUT", "/api/espace/view", bad); code != http.StatusBadRequest {
			t.Errorf("%+v: %d", bad, code)
		}
	}
	gesture(t, tw, csrf, "PUT", "/api/espace/view", espaceView{Open: []string{id, "zzzzzzzzzz"}, Talk: id, Theme: "light"})
	_, body, _ := tw.do("GET", "/espace/boot", nil, nil)
	var boot espaceBoot
	json.Unmarshal([]byte(body), &boot)
	if len(boot.View.Open) != 1 || boot.View.Talk != id || boot.View.Theme != "light" || strings.Contains(body, `"places"`) {
		t.Fatalf("boot: %s", body)
	}
	if _, page, _ := tw.do("GET", "/espace", nil, nil); !strings.Contains(page, `data-theme="light"`) || !strings.Contains(page, `name="color-scheme" content="light"`) {
		t.Fatal("the shell does not carry the theme")
	}
}
