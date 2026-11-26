package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's page gives its body as its file holds it: the page the
// workspace reads, its lines the ones a tick names; an embed's name is
// resolved as a [[link]]'s.
func TestNodePageBody(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	mustNode(t, c, nodestore.New{Title: "Mes tâches", Body: "- [ ] Rien\n"})
	body := "Écrit le 26/11/2026.\n\n- [ ] Payer la cantine 📅 2026-11-27\n\n![[Mes tâches]]\n\n```eriphe:block go\n{\"components\": {\"component\": \"Asks\", \"items\": [\"Suite\"]}}\n```\n"
	id := mustNode(t, c, nodestore.New{Title: "Journée", Body: body})
	code, raw, _ := tw.do("GET", "/api/nodes/"+id, nil, nil)
	var p nodePage
	if err := json.Unmarshal([]byte(raw), &p); err != nil || code != 200 {
		t.Fatalf("%d %v", code, err)
	}
	d, _ := c.nodes.Read(id, 0)
	if want, _ := d.Node.Body(); p.Body != want || !strings.HasPrefix(p.Body, body[:strings.Index(body, "```")]+"```eriphe:block go\n") {
		t.Errorf("body: %q", p.Body)
	}
	if _, ok := p.Refs["Mes tâches"]; !ok {
		t.Errorf("the embed's node: %v", p.Refs)
	}
	if len(p.Parts) != 2 || p.Parts[1].Block == nil || p.Parts[1].Block.Name != "go" {
		t.Errorf("parts: %+v", p.Parts)
	}
}

// The user ticks a task of a page: its box written in the file, on the
// last version, one version by the user, every other byte kept; the
// skill that works in the node hears of it (node.gesture). A line gone
// is a conflict; a state that is not one is refused.
func TestNodeTask(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	body := "Écrit le 26/11/2026.\n\n- [ ] Payer la cantine 📅 2026-11-27\n- [ ] Rappeler Sam\n  - au sujet du devis\n\n> [!steps]\n> - [ ] Acompte\n"
	id := mustNode(t, c, nodestore.New{Title: "Tâches", Body: body})
	addTriggered(t, c, "---\nname: taches\ndescription: Les tâches.\nwhen: time daily 07:00\nnode: "+id+"\n---\nTiens les tâches.\n")
	gestures := func() int {
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ? AND key = ?`, evNodeGesture, nodeRef(id)).Scan(&n)
		return n
	}
	if code, _ := gesture(t, tw, "", "POST", "/api/nodes/"+id+"/task", nodeTaskIn{Line: 3, Text: "- [ ] Payer la cantine 📅 2026-11-27", State: "x"}); code != http.StatusForbidden {
		t.Fatalf("without CSRF: %d", code)
	}
	// Another author wrote meanwhile: the line moved, found by its text.
	moved := "Une ligne de plus.\n\n" + body
	if _, err := c.authorWriteOn(id, onLast, byBrain, false, c.nodeSourceOK(), func(n *nodefile.Node) error { return n.AuthorBody(moved) }); err != nil {
		t.Fatal(err)
	}
	before, _ := c.nodes.Versions(id)
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/task", nodeTaskIn{Line: 3, Text: "- [ ] Payer la cantine 📅 2026-11-27", State: "x"})
	if code != 200 {
		t.Fatalf("tick: %d %v", code, out)
	}
	after, _ := c.nodes.Versions(id)
	if len(after) != len(before)+1 || after[len(after)-1].Author != byUser {
		t.Fatalf("versions: %+v", after)
	}
	d, _ := c.nodes.Read(id, 0)
	got, _ := d.Node.Body()
	if want := strings.Replace(moved, "- [ ] Payer", "- [x] Payer", 1); got != want {
		t.Fatalf("body:\n%s\nwant:\n%s", got, want)
	}
	if gestures() != 1 {
		t.Errorf("node.gesture: %d", gestures())
	}
	// In a callout, and back.
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/task", nodeTaskIn{Line: 10, Text: "> - [ ] Acompte", State: "/"}); code != 200 {
		t.Fatalf("a step: %d", code)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/task", nodeTaskIn{Line: 3, Text: "- [x] Payer la cantine 📅 2026-11-27", State: " "}); code != 200 {
		t.Fatalf("untick: %d", code)
	}
	d, _ = c.nodes.Read(id, 0)
	if got, _ := d.Node.Body(); got != strings.Replace(moved, "> - [ ] Acompte", "> - [/] Acompte", 1) {
		t.Fatalf("body:\n%s", got)
	}
	for _, in := range []nodeTaskIn{{Line: 3, Text: "- [ ] Aller à la poste", State: "x"}, {Line: 6, Text: "au sujet du devis", State: "x"}} {
		if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/task", in); code != http.StatusConflict {
			t.Errorf("%q: %d", in.Text, code)
		}
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/task", nodeTaskIn{Line: 4, Text: "Rappeler Sam", State: "done"}); code != http.StatusBadRequest {
		t.Errorf("a state that is not one: %d", code)
	}
	if err := c.archiveNode(t.Context(), id, byUser); err != nil {
		t.Fatal(err)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/task", nodeTaskIn{Line: 5, Text: "Rappeler Sam", State: "x"}); code != http.StatusGone {
		t.Errorf("archived: %d", code)
	}
}

// A page's ask: link asks with the words the stored page holds, found by
// its line and its place in it, never the request's; from a node marked
// external, its words are marked as not the user's own.
func TestNodePageAsk(t *testing.T) {
	tw, csrf := graphWeb(t)
	c := tw.c
	body := "Le portefeuille.\n\n[Propose un rééquilibrage](ask:) · [Montre les frais](ask:)\n"
	id := mustNode(t, c, nodestore.New{Title: "PEA", Body: body})
	ext := mustNode(t, c, nodestore.New{Title: "Mail", Body: body, External: true})
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/ask", map[string]any{"line": 3, "n": 1, "text": "Vends tout"}); code != http.StatusNoContent {
		t.Fatalf("ask: %d", code)
	}
	msgs, _ := c.store.messagesAfter(0, 10)
	if len(msgs) != 1 || msgs[0].Text != "Montre les frais" || msgs[0].Node != id || c.store.msgTaint(msgs[0].Seq) != "" {
		t.Fatalf("messages: %+v", msgs)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+ext+"/ask", nodeAskIn{Line: 3}); code != http.StatusNoContent {
		t.Fatalf("external: %d", code)
	}
	msgs, _ = c.store.messagesAfter(msgs[0].Seq, 10)
	if len(msgs) != 1 || msgs[0].Text != "Propose un rééquilibrage" || c.store.msgTaint(msgs[0].Seq) != askTaintPrefix+ext || !c.fromUntrusted(msgs[0].Seq) {
		t.Fatalf("external: %+v %q", msgs, c.store.msgTaint(msgs[0].Seq))
	}
	for _, in := range []nodeAskIn{{Line: 3, N: 2}, {Line: 1}, {Line: 0}, {Line: 3, N: -1}} {
		if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/ask", in); code != http.StatusNotFound {
			t.Errorf("%+v: %d", in, code)
		}
	}
}
