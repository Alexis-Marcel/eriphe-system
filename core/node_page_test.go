package main

import (
	"encoding/json"
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
