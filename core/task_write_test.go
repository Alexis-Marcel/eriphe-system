package main

import (
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// rewrite writes a node's body as author, as the brain or the user do.
func rewrite(t *testing.T, c *core, id, author, body string) {
	t.Helper()
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.nodes.Change(id, d.Version.Hash, author, func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		t.Fatal(err)
	}
}

// appendAs is node_append on node id as task: its error's text.
func appendAs(t *testing.T, c *core, task, id string) string {
	t.Helper()
	_, err := callNodes(t, c, task, "node_append", map[string]any{"id": id, "text": "Ajouté par la tâche."})
	return err
}

// A task writes where it works: its node and the nodes under it, the
// node it was launched for, the nodes someone else linked to or from
// these, read at each call; elsewhere it is refused with a hint. A link
// it wrote itself opens nothing, nor does a link two hops away.
func TestTaskWritesWhereItWorks(t *testing.T) {
	c := nodeCore(t)
	guide := mustNode(t, c.core, nodestore.New{Title: "Guide de style", Author: byBrain})
	far := mustNode(t, c.core, nodestore.New{Title: "Lointain", Author: byBrain})
	other := mustNode(t, c.core, nodestore.New{Title: "Ailleurs", Author: byBrain})
	gone := mustNode(t, c.core, nodestore.New{Title: "Ancien", Author: byBrain})
	parent := mustNode(t, c.core, nodestore.New{Title: "Substack", Body: "Voir [[Guide de style]] et [[Ancien]].\n", Author: byBrain})
	sibling := mustNode(t, c.core, nodestore.New{Title: "Abonnés", Parent: parent, Author: byBrain})
	pointing := mustNode(t, c.core, nodestore.New{Title: "Écriture", Body: "La lettre : [[Substack]].\n", Author: byUser})
	rewrite(t, c.core, guide, byBrain, "Le ton. Voir [[Lointain]].\n")
	task, own := startInNode(t, c, runRequest{Title: "Style des articles", Prompt: "Analyse le style.", Node: parent})

	// The node it works for, by the version it read: a stale one refused;
	// the trace's author is the task.
	read := mustNodes(t, c.core, task.ID, "node_read", map[string]any{"id": parent})
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": parent, "old": "Voir", "new": "Lire"})
	if _, err := callNodes(t, c.core, task.ID, "node_write", map[string]any{"id": parent, "body": "x", "expect": read["hash"]}); !strings.Contains(err, "changed since") {
		t.Fatalf("a stale write: %q", err)
	}
	if d, _ := c.nodes.Read(parent, 0); d.Version.Author != "task:"+task.ID || !strings.Contains(d.Node.Text(), "Lire [[Guide de style]]") {
		t.Fatalf("the node it works for, by %s:\n%s", d.Version.Author, d.Raw)
	}
	// Under its node: a child it opens, and a child of that.
	kid := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Plan", "goal": "Un plan.", "body": "À faire."})
	grand := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"parent": kid["id"], "title": "Intro", "goal": "Une intro.", "body": "x"})
	for _, id := range []string{own, kid["id"].(string), grand["id"].(string)} {
		if err := appendAs(t, c.core, task.ID, id); err != "" {
			t.Errorf("under its node, %s: %s", id, err)
		}
	}
	// Linked from the node it works for, and linking to it.
	for _, id := range []string{guide, pointing} {
		if err := appendAs(t, c.core, task.ID, id); err != "" {
			t.Errorf("linked %s: %s", id, err)
		}
	}
	// A child of the node it works for, under it: opened there.
	beside := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"parent": parent, "title": "Idées", "goal": "Des idées.", "body": "x"})
	if k, _ := c.nodes.Get(beside["id"].(string)); k.Parent != parent {
		t.Fatalf("opened under the node it works for: %+v", k)
	}
	// What it opened there is its own work: it writes it, and what is
	// under it; another task under the same node does not.
	besideKid := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"parent": beside["id"], "title": "Idée 1", "goal": "Une idée.", "body": "x"})
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": beside["id"], "old": "x", "new": "Trois idées."})
	if err := appendAs(t, c.core, task.ID, besideKid["id"].(string)); err != "" {
		t.Fatalf("under a node it opened: %s", err)
	}
	second, _ := startInNode(t, c, runRequest{Title: "Autre", Prompt: "Autre chose.", Node: parent})
	for _, id := range []string{beside["id"].(string), besideKid["id"].(string)} {
		if err := appendAs(t, c.core, second.ID, id); !strings.Contains(err, "you write where you work") {
			t.Fatalf("another task wrote %s: %q", id, err)
		}
	}
	// Elsewhere: refused, with the hint, the node untouched.
	for _, id := range []string{other, far, sibling} {
		err := appendAs(t, c.core, task.ID, id)
		if !strings.Contains(err, "you write where you work") || !strings.Contains(err, own) || !strings.Contains(err, parent) || !strings.Contains(err, "Say what you found in your node") {
			t.Errorf("%s written: %q", id, err)
		}
		if d, _ := c.nodes.Read(id, 0); strings.Contains(d.Node.Text(), "Ajouté") {
			t.Errorf("%s was written", id)
		}
	}
	if _, err := callNodes(t, c.core, task.ID, "node_open", map[string]any{"parent": other, "title": "X", "goal": "X.", "body": "x"}); !strings.Contains(err, "you write where you work") {
		t.Errorf("opened under another node: %q", err)
	}
	// A link it writes itself opens nothing: in its node, or in the node
	// it works for.
	cur := mustNodes(t, c.core, task.ID, "node_read", nil)
	mustNodes(t, c.core, task.ID, "node_write", map[string]any{"body": "Voir [[Ailleurs]].\n", "expect": cur["hash"]})
	mustNodes(t, c.core, task.ID, "node_append", map[string]any{"id": parent, "text": "Et [[Ailleurs]]."})
	if err := appendAs(t, c.core, task.ID, other); !strings.Contains(err, "you write where you work") {
		t.Fatalf("its own link opened a node: %q", err)
	}
	// Kept by someone else who writes that node after it, it is still its.
	d, _ := c.nodes.Read(parent, 0)
	rewrite(t, c.core, parent, byBrain, d.Node.Text()+"\nMis à jour.\n")
	if err := appendAs(t, c.core, task.ID, other); !strings.Contains(err, "you write where you work") {
		t.Fatalf("its own link, kept by the brain, opened a node: %q", err)
	}
	// A link someone else adds, it may follow; once taken away, no more.
	rewrite(t, c.core, parent, byUser, "Lire [[Guide de style]], [[Ancien]], [[Lointain]].\n")
	if err := appendAs(t, c.core, task.ID, far); err != "" {
		t.Fatalf("a link the user added: %s", err)
	}
	rewrite(t, c.core, parent, byUser, "Lire [[Guide de style]] et [[Ancien]].\n")
	if err := appendAs(t, c.core, task.ID, far); !strings.Contains(err, "you write where you work") {
		t.Fatalf("a link taken away: %q", err)
	}
	// An archived node is never written, linked or not.
	c.nodes.Archive(gone, byUser)
	if err := appendAs(t, c.core, task.ID, gone); !strings.Contains(err, "archived") {
		t.Fatalf("an archived node: %q", err)
	}
	// It never closes a node: it has no tool for it.
	for _, name := range nodesTools {
		if name == "node_set" || name == "node_archive" {
			t.Fatalf("a task has %s", name)
		}
	}
}

// A task tainted now marks external the node it writes, wherever it is.
func TestTaintedTaskWritesExternalWhereItWorks(t *testing.T) {
	c := nodeCore(t)
	parent := mustNode(t, c.core, nodestore.New{Title: "Mes mails", Author: byBrain})
	task, _ := startInNode(t, c, runRequest{Title: "Tri", Prompt: "Trie.", Node: parent})
	mustNodes(t, c.core, task.ID, "node_append", map[string]any{"id": parent, "text": "Propre."})
	if sum, _ := c.nodes.Get(parent); sum.External {
		t.Fatal("a clean write marked external")
	}
	c.taskRead(task.ID, "mail")
	mustNodes(t, c.core, task.ID, "node_append", map[string]any{"id": parent, "text": "Après un mail."})
	if sum, _ := c.nodes.Get(parent); !sum.External {
		t.Fatal("a tainted write left the node clean")
	}
}

// A step, or a skill's run, works in a node it did not open: the node
// above it is not the node it works for, and it does not write it.
func TestTaskInAGivenNodeKeepsToIt(t *testing.T) {
	c := nodeCore(t)
	top := mustNode(t, c.core, nodestore.New{Title: "Travail", Author: byBrain})
	step := mustNode(t, c.core, nodestore.New{Title: "Étape", Parent: top, Author: byBrain})
	task, id := startInNode(t, c, runRequest{Title: "Étape", Prompt: "Fais l'étape.", inNode: step})
	if id != step {
		t.Fatalf("its node %s", id)
	}
	if err := appendAs(t, c.core, task.ID, step); err != "" {
		t.Fatalf("its node: %s", err)
	}
	if err := appendAs(t, c.core, task.ID, top); !strings.Contains(err, "you write where you work") || strings.Contains(err, "the node you work for") {
		t.Fatalf("the node above: %q", err)
	}
}

// A [[Name]] waiting in another node resolves to a node the task opens
// with that name: it does not open that node to the task.
func TestWaitingLinkOpensNothing(t *testing.T) {
	c := nodeCore(t)
	victim := mustNode(t, c.core, nodestore.New{Title: "Notes", Body: "À faire : [[Fantôme]].\n", Author: byUser})
	task, _ := startInNode(t, c, runRequest{Title: "Recherche", Prompt: "Cherche."})
	kid := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Fantôme", "goal": "Un piège.", "body": "x"})
	if back, _ := c.nodes.Backlinks(kid["id"].(string)); len(back) != 1 || back[0].ID != victim {
		t.Fatalf("the waiting link did not resolve: %+v", back)
	}
	if err := appendAs(t, c.core, task.ID, victim); !strings.Contains(err, "you write where you work") {
		t.Fatalf("a waiting link opened a node: %q", err)
	}
}
