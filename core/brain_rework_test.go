package main

import (
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The headline case of #770, end to end: in « Mes mails », a page that
// shows someone else's words (external), the user asks to rework it. The
// brain, its budget spent, still launches a task on the page; lending the
// read tool its blocks read starts it with no card, on a clean turn and
// on one that read the external page (a free read: today's rules ask a
// card only for what may act, TestLaunchTaintedStart), the task tainted
// then. The task rewrites the page's sourced blocks when it is lent their
// tool, adds a block on node_find, and is refused a source it is not lent,
// saying so.
func TestReworkAPageEndToEnd(t *testing.T) {
	c := launchCore(t)
	inbox := func(text string) string {
		return "```eriphe:block mails\n{\"components\": {\"component\": \"Text\", \"text\": \"" + text + "\"}, \"sources\": {\"inbox\": {\"tool\": \"github/get_issue\", \"args\": {\"number\": 3}}}}\n```\n"
	}
	found := "```eriphe:block suivis\n{\"components\": {\"component\": \"Text\", \"text\": \"Suivis\"}, \"sources\": {\"suivis\": {\"tool\": \"eriphe-core/node_find\", \"args\": {\"tags\": [\"courrier\"]}}}}\n```\n"
	page := mustNode(t, c, nodestore.New{Title: "Mes mails", Goal: "Les mails à traiter.", Body: "Les mails qui comptent.\n" + inbox("Mails"), External: true, Author: byBrain})
	ask := "Refais cette page : regroupe les mails par expéditeur."
	lend := []any{map[string]any{"kind": "tool", "ref": "github.get_issue"}}

	// The user's message, said in the page. The brain's budget spent on
	// gestures: launching passes all the same.
	c.turn.start(1, 1, 0)
	c.turnNode(page)
	for range brainGestures {
		c.turn.charge("remember", nil)
	}
	if why, _ := c.turn.charge("node_write", map[string]any{"id": page, "body": strings.Repeat("Une page. ", 100)}); why == "" {
		t.Fatal("a whole page passed the spent budget")
	}
	if why, _ := c.turn.charge("run_task", map[string]any{"node": page, "ask": ask}); why != "" {
		t.Fatalf("the launch refused: %s", why)
	}

	// Clean turn (the brain did not read the page: the task does): the
	// read tool is lent and the task starts with no card.
	task, _ := launched(t, c, map[string]any{"ask": ask, "node": page, "resources": lend})
	if st := c.startState(task); st["start"] != "started" {
		t.Fatalf("a clean launch lending a read tool: %v", st)
	}
	read := mustNodes(t, c, task.ID, "node_read", map[string]any{"id": page})
	mustNodes(t, c, task.ID, "node_write", map[string]any{"id": page, "expect": read["hash"],
		"body": "Regroupés par expéditeur.\n" + inbox("Par expéditeur") + found})
	d, err := c.nodes.Read(page, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version.Author != "task:"+task.ID || !strings.Contains(string(d.Raw), "Par expéditeur") || d.Node.Block("suivis") == nil {
		t.Fatalf("the page rewritten by %s:\n%s", d.Version.Author, d.Raw)
	}

	// Launched without the tool: its rewrite of a sourced block is
	// refused, and the error says which tool it lacks.
	bare, _ := launched(t, c, map[string]any{"ask": ask, "node": page})
	read = mustNodes(t, c, bare.ID, "node_read", map[string]any{"id": page})
	if _, msg := callNodes(t, c, bare.ID, "node_write", map[string]any{"id": page, "expect": read["hash"],
		"body": "Autrement.\n" + inbox("Autrement") + found}); !strings.Contains(msg, "github/get_issue is not one of the tools you are lent") {
		t.Fatalf("a source not lent: %q", msg)
	}

	// The brain read the external page first: its turn is tainted. A
	// read tool still needs no card (only what may act does), and the
	// task is tainted: what it writes is external.
	c.turn.start(2, 2, 0)
	c.turnNode(page)
	mustBrainNodes(t, c, "node_read", map[string]any{"id": page})
	if len(c.turnTaint()) == 0 {
		t.Fatal("reading the external page left the turn clean")
	}
	tainted, _ := launched(t, c, map[string]any{"ask": ask, "node": page, "resources": lend})
	if st := c.startState(tainted); st["start"] != "started" || len(c.taskTaint(tainted.ID)) == 0 {
		t.Fatalf("a tainted launch lending a read tool: %v, taint %v", st, c.taskTaint(tainted.ID))
	}
}
