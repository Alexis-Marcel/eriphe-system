package main

import (
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The headline case of #770, end to end: in « Mes mails », a page that
// shows someone else's words (external), the user asks to rework it. The
// brain, its budget spent, still launches a task on the page; lending a
// read tool starts it with no card, on a clean turn and on one that read
// the external page (a free read: today's rules ask a card only for what
// may act, TestLaunchTaintedStart), the task tainted then. The task
// rewrites the page.
func TestReworkAPageEndToEnd(t *testing.T) {
	c := launchCore(t)
	page := mustNode(t, c, nodestore.New{Title: "Mes mails", Goal: "Les mails à traiter.", Body: "Les mails qui comptent.\n\n- Mails\n", External: true, Author: byBrain})
	ask := "Refais cette page : regroupe les mails par expéditeur."
	lend := []any{map[string]any{"kind": "tool", "ref": "github.get_issue"}}

	// The user's message, said in the page. The brain's budget spent on
	// gestures: launching passes all the same.
	c.turn.start(1, 1, 0)
	c.turnNode(page)
	for range brainGestures {
		c.turn.charge("intend", nil)
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
		"body": "Regroupés par expéditeur.\n\n### Par expéditeur\n\n- Mails\n"})
	d, err := c.nodes.Read(page, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version.Author != "task:"+task.ID || !strings.Contains(string(d.Raw), "Par expéditeur") {
		t.Fatalf("the page rewritten by %s:\n%s", d.Version.Author, d.Raw)
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
