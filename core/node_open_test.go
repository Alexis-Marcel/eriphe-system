package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// askBody is a body whose block asks the brain: refused once its writer
// read something external.
const askBody = "Une question.\n\n```eriphe:block q\n" +
	`{"components": {"component": "Button", "label": "Oui", "action": {"event": {"name": "ask", "context": {"text": "Oui"}}}}}` +
	"\n```\n"

// The brain and a task open nodes by one path (openNode): a work node,
// its body checked as their write, external when they are tainted, the
// brain's under the parent it names, a task's under its own node.
func TestOneNodeOpen(t *testing.T) {
	c := nodeCore(t)
	subject, err := c.nodes.Create(nodestore.New{Title: "Substack", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	sid := subject.Node.Meta.ID
	task, own := startInNode(t, c, runRequest{Title: "Style", Prompt: "Analyse le style.", Node: sid})

	// Clean, both open a work node with the same body.
	b := mustBrainNodes(t, c.core, "node_open", map[string]any{"title": "Plan", "parent": sid, "body": askBody, "aside": true})
	k := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Pages", "body": askBody})
	for id, parent := range map[string]string{b["id"].(string): sid, k["id"].(string): own} {
		sum, _ := c.nodes.Get(id)
		if sum.Parent != parent || sum.Lifetime != nodefile.Work || sum.External {
			t.Errorf("node %s: %+v", id, sum)
		}
	}

	// Tainted, both refuse the same block, and nothing is made.
	c.turn.taint("web.fetch")
	c.store.set(taskTaintKey(task.ID), "external:mail")
	n := len(mustGraph(t, c.core))
	_, bmsg := brainNodes(t, c.core, "node_open", map[string]any{"title": "Plan 2", "body": askBody, "aside": true})
	_, kmsg := callNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Pages 2", "body": askBody})
	for who, msg := range map[string]string{"brain": bmsg, "task": kmsg} {
		if !strings.Contains(msg, "leave the button out") {
			t.Errorf("%s: a tainted ask: %q", who, msg)
		}
	}
	if len(mustGraph(t, c.core)) != n {
		t.Error("a refused node_open made a node")
	}

	// Tainted, what they open is external.
	b = mustBrainNodes(t, c.core, "node_open", map[string]any{"title": "Plan 3", "aside": true})
	k = mustNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Pages 3"})
	for _, id := range []string{b["id"].(string), k["id"].(string)} {
		if sum, _ := c.nodes.Get(id); !sum.External {
			t.Errorf("node %s opened tainted is not external", id)
		}
	}
}

// run_task with no node works under the turn's node; at the home, at the
// top; under an archived turn's node, at the top too.
func TestRunTaskUnderTurnNode(t *testing.T) {
	c := nodeCore(t)
	ctx := context.Background()
	run := func() nodestore.Summary {
		t.Helper()
		out, err := c.brainRunTask(ctx, runRequest{Title: "Recherche", Prompt: "Cherche."})
		if err != nil {
			t.Fatal(err)
		}
		sum, err := c.nodes.Get(out["node"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	if sum := run(); sum.Parent != "" {
		t.Errorf("at the home: under %q", sum.Parent)
	}
	d, err := c.nodes.Create(nodestore.New{Title: "Vacances", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	c.turnNode(id)
	sum := run()
	if sum.Parent != id {
		t.Errorf("in the turn's node: under %q", sum.Parent)
	}
	if task := c.nodeTasks(sum.ID); len(task) != 1 {
		t.Fatalf("tasks %v", task)
	} else if tk, _ := c.store.task(task[0]); tk.About != nodeRef(id) {
		t.Errorf("about %q", tk.About)
	}
	if err := c.nodes.Archive(id, byUser); err != nil {
		t.Fatal(err)
	}
	if sum := run(); sum.Parent != "" {
		t.Errorf("an archived turn's node: under %q", sum.Parent)
	}
}

// node_set moves a node under another, or to the top, never under itself
// nor below it, never a step; node_read gives the nodes that link it.
func TestBrainNodeReparentAndBacklinks(t *testing.T) {
	c := graphCore(t)
	home := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Maison", "aside": true})["id"].(string)
	works := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Travaux", "parent": home, "aside": true})["id"].(string)
	quote := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Devis", "aside": true, "body": "Pour [[Travaux]]."})["id"].(string)

	if _, msg := brainNodes(t, c, "node_set", map[string]any{"id": home, "parent": works}); !strings.Contains(msg, "under itself") {
		t.Errorf("a cycle: %q", msg)
	}
	out := mustBrainNodes(t, c, "node_set", map[string]any{"id": quote, "parent": works})
	if out["parent"] != works {
		t.Errorf("moved %v", out)
	}
	if sum, _ := c.nodes.Get(quote); sum.Parent != works {
		t.Errorf("under %q", sum.Parent)
	}
	mustBrainNodes(t, c, "node_set", map[string]any{"id": works, "parent": ""})
	if sum, _ := c.nodes.Get(works); sum.Parent != "" {
		t.Errorf("to the top: under %q", sum.Parent)
	}

	read := mustBrainNodes(t, c, "node_read", map[string]any{"id": works})
	back, _ := read["backlinks"].([]any)
	if len(back) != 1 || back[0].(map[string]any)["id"] != quote {
		t.Errorf("backlinks %v", read["backlinks"])
	}

	work := workNode(t, c, "")
	ids, err := c.openSteps(context.Background(), work, []stepSpec{{Key: "a", Title: "A", Prompt: "Fais A."}}, 0, stepOpener{author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	if _, msg := brainNodes(t, c, "node_set", map[string]any{"id": ids["a"], "parent": ""}); !strings.Contains(msg, "step stays") {
		t.Errorf("a step moved: %q", msg)
	}
}
