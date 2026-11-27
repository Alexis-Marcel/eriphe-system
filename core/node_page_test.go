package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

const mailsPage = `## À archiver
[Archiver ces mails](do:docs/archive)
- [Facture de novembre][m1] : payée
  - Prélevée le 28/11.

## À supprimer
[Jeter ces mails](do:mail/trash)
- [Newsletter][m2] : jamais ouverte

[m1]: mail:aWQtMQ
[m2]: mail:aWQtMg
`

// problemCodes are the codes of a write's problems.
func problemCodes(t *testing.T, out map[string]any) []string {
	t.Helper()
	raw, _ := json.Marshal(out["problems"])
	var pr []nodefile.Problem
	_ = json.Unmarshal(raw, &pr)
	var codes []string
	for _, p := range pr {
		codes = append(codes, p.Code)
	}
	return codes
}

// A task writes a Markdown page: the write is kept, and its result says
// what is wrong in it, a do: link on a tool it is not lent included.
// Its line edits need no version: they apply while their line is there,
// are refused once it is gone, and move an item with its detail.
func TestTaskWritesAMarkdownPage(t *testing.T) {
	c := nodeCore(t)
	parent := mustNode(t, c.core, nodestore.New{Title: "Mes mails", Author: byBrain})
	task, _ := startInNode(t, c, runRequest{Title: "Tri", Prompt: "Trie.", Node: parent, Tools: []string{"docs.archive"}})
	cur := mustNodes(t, c.core, task.ID, "node_read", map[string]any{"id": parent})
	out := mustNodes(t, c.core, task.ID, "node_write", map[string]any{"id": parent, "body": mailsPage, "expect": cur["hash"]})
	if codes := problemCodes(t, out); strings.Join(codes, " ") != nodefile.ProblemDoTool {
		t.Fatalf("problems %v", out["problems"])
	}
	if read := mustNodes(t, c.core, task.ID, "node_read", map[string]any{"id": parent}); read["body"] != mailsPage {
		t.Fatalf("read %q", read["body"])
	}
	// A line edit, made with no version.
	out = mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": parent, "old": "- [Newsletter][m2] : jamais ouverte", "new": "- [Newsletter][m3] : jamais ouverte"})
	if codes := problemCodes(t, out); !strings.Contains(strings.Join(codes, " "), nodefile.ProblemRefUndefined) {
		t.Errorf("problems %v", out["problems"])
	}
	if _, err := callNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": parent, "old": "[Newsletter][m2]", "new": "x"}); !strings.Contains(err, "read it again") {
		t.Errorf("an edit of a line gone: %q", err)
	}
	// An item moved with its detail, under the other heading.
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": parent, "move": "[Facture de novembre]", "after": "## À supprimer"})
	d, _ := c.nodes.Read(parent, 0)
	want := "## À archiver\n[Archiver ces mails](do:docs/archive)\n\n## À supprimer\n[Jeter ces mails](do:mail/trash)\n- [Facture de novembre][m1] : payée\n  - Prélevée le 28/11.\n- [Newsletter][m3]"
	if body, _ := d.Node.Body(); !strings.HasPrefix(body, want) {
		t.Errorf("moved:\n%s", body)
	}
	if _, err := callNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": parent, "move": "[Facture de novembre]", "after": "## Ailleurs"}); !strings.Contains(err, "read it again") {
		t.Errorf("a move after a line gone: %q", err)
	}
	// node_append writes over nothing, and says the page's problems too.
	out = mustNodes(t, c.core, task.ID, "node_append", map[string]any{"id": parent, "text": "- [ ] Relancer 📅 2026-02-30"})
	if codes := problemCodes(t, out); !strings.Contains(strings.Join(codes, " "), nodefile.ProblemDateImpossible) {
		t.Errorf("problems %v", out["problems"])
	}
}

// A mail's id, as the mail server gives it: long, random-looking.
const mailID = "bTEfc2FtQGV4YW1wbGUub3JnH0lOQk9YHzEfMjkwMjY"

// A page whose references name mails by their long ids is written, and
// a line of it edited (the store scans the whole page again): an item's
// id is not taken for a credential. A credential's shape as an id, and a
// long random string in the text, are still refused.
func TestPageItemIDsAreNotSecrets(t *testing.T) {
	c := nodeCore(t)
	parent := mustNode(t, c.core, nodestore.New{Title: "Mes mails", Author: byBrain})
	task, _ := startInNode(t, c, runRequest{Title: "Tri", Prompt: "Trie.", Node: parent})
	page := "## À lire\n- [Devis terrasse][m1]\n- [Facture][m2]\n\n[m1]: mail:" + mailID + "\n[m2]: mail:" + mailID + "Zz9\n"
	cur := mustNodes(t, c.core, task.ID, "node_read", map[string]any{"id": parent})
	mustNodes(t, c.core, task.ID, "node_write", map[string]any{"id": parent, "body": page, "expect": cur["hash"]})
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": parent, "old": "- [Facture][m2]", "new": "- [Facture payée][m2]"})
	for _, bad := range []string{
		"[m3]: mail:eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzYW0ifQ.c2lnbmF0dXJlLWhlcmU",
		"[m3]: mail:AKIAABCDEFGHIJKLMNOP",
		"- Clé aB3dE5gH7jK9mN1pQ3sT5vW7yZ9bC1dF3hJ5kL7nP9",
	} {
		if _, err := callNodes(t, c.core, task.ID, "node_append", map[string]any{"id": parent, "text": bad}); !strings.Contains(err, "refused") {
			t.Errorf("%q passed: %q", bad, err)
		}
	}
}

// Only a reference's target, as the page reader reads it, is left out of
// the long random strings: not one in a code block, nor text after it, nor
// a link's query, nor a target in another form.
func TestRefuseSecretItemIDs(t *testing.T) {
	for _, ok := range []string{
		"[m1]: mail:" + mailID,
		"  [Devis terrasse]: calendar:" + mailID + "\n",
		"[m1]: https://example.org/d/" + mailID,
	} {
		if err := refuseSecret(ok); err != nil {
			t.Errorf("refused %q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"```\n[m1]: mail:" + mailID + "\n```",
		"[m1]: mail:aWQtMQ " + mailID,
		"[m1]: https://example.org/d?x=" + mailID,
		"[m1]: Mail:" + mailID,
		"[m1]: " + mailID,
		"mail:" + mailID,
		"[Devis](mail:" + mailID + ")",
	} {
		if err := refuseSecret(bad); err == nil {
			t.Errorf("passed %q", bad)
		}
	}
}

// The brain's writes say the page's problems; its do: links name tools of
// the catalog. node_find finds a page by a reference's id and by its
// words.
func TestBrainWritesAMarkdownPage(t *testing.T) {
	c := nodeCore(t).core
	out := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Mes mails", "goal": "Les mails triés.", "body": mailsPage})
	if codes := problemCodes(t, out); strings.Join(codes, " ") != nodefile.ProblemDoTool {
		t.Errorf("problems %v", out["problems"]) // no mail server in this catalog: only docs
	}
	id := out["id"].(string)
	out = mustBrainNodes(t, c, "node_edit", map[string]any{"id": id, "old": "## À supprimer\n[Jeter ces mails](do:mail/trash)\n", "new": "## À supprimer\n"})
	if _, ok := out["problems"]; ok {
		t.Errorf("problems %v", out["problems"])
	}
	for _, q := range []string{"aWQtMQ", "Facture novembre"} {
		found := mustBrainNodes(t, c, "node_find", map[string]any{"query": q})
		raw, _ := json.Marshal(found)
		if !strings.Contains(string(raw), id) {
			t.Errorf("node_find %q: %s", q, raw)
		}
	}
}

// A task writes a page with its own front matter at its top, as Obsidian
// writes a note: node_open and node_write take its fields as the node's
// properties, indexed, and the page keeps none of it; a field that is
// not a property leaves the block in the page, and the write says so.
func TestTaskWritesAFrontMatterBlock(t *testing.T) {
	c := nodeCore(t)
	parent := mustNode(t, c.core, nodestore.New{Title: "Recettes", Author: byBrain})
	task, _ := startInNode(t, c, runRequest{Title: "Recette", Prompt: "Écris.", Node: parent})
	page := "Écrit le 27/11/2026.\n\nUne tarte.\n"
	kid := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Tarte", "goal": "Une recette.",
		"body": "---\ntags: [cuisine/recette]\nnote: 9\ntype: plaisir\n---\n\n" + page})
	id := kid["id"].(string)
	d, _ := c.nodes.Read(id, 0)
	if d.Node.Text() != page || bytes.Count(d.Raw, []byte("---\n")) != 2 {
		t.Fatalf("opened:\n%s", d.Raw)
	}
	props, err := c.nodes.Props([]string{id}, []string{"tags", "note", "type"})
	if err != nil || fmt.Sprint(props[id]) != "map[note:9 tags:[cuisine/recette] type:plaisir]" {
		t.Fatalf("props %v %v", props, err)
	}
	out := mustNodes(t, c.core, task.ID, "node_write", map[string]any{"id": id, "expect": d.Version.Hash,
		"body": "---\nnote: 8\ncout: 7\n---\n" + page})
	d, _ = c.nodes.Read(id, 0)
	if _, ok := out["problems"]; ok || d.Node.Text() != page {
		t.Fatalf("problems %v, written:\n%s", out["problems"], d.Raw)
	}
	if props, _ = c.nodes.Props([]string{id}, []string{"tags", "note", "cout"}); fmt.Sprint(props[id]) != "map[cout:7 note:8 tags:[cuisine/recette]]" {
		t.Fatalf("props %v", props)
	}
	// title is node_set's: the block stays text, nothing taken from it.
	body := "---\ntitle: Autre\nnote: 1\n---\n" + page
	out = mustNodes(t, c.core, task.ID, "node_write", map[string]any{"id": id, "expect": d.Version.Hash, "body": body})
	d, _ = c.nodes.Read(id, 0)
	if codes := problemCodes(t, out); strings.Join(codes, " ") != nodefile.ProblemFrontBlock || d.Node.Text() != body || d.Node.Meta.Title != "Tarte" {
		t.Fatalf("problems %v, written:\n%s", out["problems"], d.Raw)
	}
	if props, _ = c.nodes.Props([]string{id}, []string{"note"}); fmt.Sprint(props[id]) != "map[note:8]" {
		t.Fatalf("props %v", props)
	}
}
