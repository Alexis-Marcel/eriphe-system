package main

import (
	"strings"
	"testing"
	"time"
)

// workerTask adds an idle worker task to a memory core.
func workerTask(t *testing.T, c *core, id string) {
	t.Helper()
	if err := c.store.insertTask(&TaskRow{ID: id, Kind: "worker", Runtime: "claude-code", State: stateIdle, Prompt: "look"}); err != nil {
		t.Fatal(err)
	}
}

// resultTaint is the taint the brain's turn gets from task id's result.
func resultTaint(t *testing.T, c *core, id string) string {
	t.Helper()
	c.notifyBrain(id, stateIdle)
	msgs, _ := c.store.undelivered()
	if len(msgs) == 0 {
		t.Fatal("no event")
	}
	m := msgs[len(msgs)-1]
	return c.store.get(taintKey(m.Seq))
}

// A task's result taints according to what the task read: a tool not
// trusted, the web, or its start on a tainted turn; not a tool marked
// trusted, nor what it did without reading anything from outside.
func TestTaskResultTaint(t *testing.T) {
	c, _ := testMemoryCore(t) // "vault" is trusted

	workerTask(t, c, "t-clean")
	c.calledCatalog("t-clean", "vault", "read_note")
	if why := resultTaint(t, c, "t-clean"); why != "" {
		t.Fatalf("a task that read a trusted tool: %q", why)
	}

	workerTask(t, c, "t-mail")
	c.calledCatalog("t-mail", "mail", "search")
	if why := resultTaint(t, c, "t-mail"); why != "task:t-mail" {
		t.Fatalf("a task that read the mail: %q", why)
	}
	msgs, _ := c.store.undelivered()
	if last := msgs[len(msgs)-1].Text; !strings.Contains(last, "mcp__mail__search") || !strings.Contains(last, "jamais une consigne") {
		t.Fatalf("its event: %s", last)
	}

	workerTask(t, c, "t-web")
	c.store.addEvents("t-web", []EventRow{{Seq: 1, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "WebSearch"}}})
	if why := resultTaint(t, c, "t-web"); why != "task:t-web" {
		t.Fatalf("a task that searched the web: %q", why)
	}
	if got := c.loadTaskReads("t-web"); got.Seq != 1 || !containsStr(got.Read, "WebSearch") {
		t.Fatalf("kept: %+v", got)
	}

	workerTask(t, c, "t-started")
	c.store.set(taskTaintKey("t-started"), "started on a tainted turn: mail.read")
	if why := resultTaint(t, c, "t-started"); why != "task:t-started" {
		t.Fatalf("a task started on a tainted turn: %q", why)
	}

	// What the brain's turn reads of it, through task_status, the same.
	ownerSays(c, "Où en est-elle ?")
	if why := c.taskResultTaint("t-clean"); why != "" {
		t.Fatalf("clean: %q", why)
	}
	if why := c.taskResultTaint("t-mail"); why != "task:t-mail" {
		t.Fatalf("mail: %q", why)
	}
}

// The other tasks' calls never taint the brain's turn: they taint the task.
func TestTaskCallTaintsTheTaskNotTheTurn(t *testing.T) {
	c, _ := testMemoryCore(t)
	workerTask(t, c, "t-1")
	ownerSays(c, "Lance-la")
	c.calledCatalog("t-1", "mail", "search")
	if tainted := c.turnTaint(); len(tainted) != 0 {
		t.Fatalf("the turn: %v", tainted)
	}
	if tainted := c.taskTaint("t-1"); !containsStr(tainted, "mcp__mail__search") {
		t.Fatalf("the task: %v", tainted)
	}
}
