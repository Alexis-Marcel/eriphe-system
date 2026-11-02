package main

import (
	"context"
	"strings"
	"testing"
)

// What is certainly wrong in a contract's checks is refused at launch,
// with a reason the brain can act on.
func TestContractCertainlyWrong(t *testing.T) {
	for name, c := range map[string]struct {
		ch   taskCheck
		want string
	}{
		"absolute outside": {taskCheck{Name: "a", Run: "make", Dir: "/repo"}, "outside /work"},
		"climbs":           {taskCheck{Name: "a", Run: "make", Dir: "core/../../x"}, `climbs with ".."`},
		"climbs inside":    {taskCheck{Name: "a", Run: "make", Dir: "/work/repo/../repo"}, `climbs with ".."`},
		"exists outside":   {taskCheck{Name: "a", Exists: "/tmp/out"}, "outside /work"},
		"empty run":        {taskCheck{Name: "a", Run: "   "}, "non-empty shell command"},
		"dir with exists":  {taskCheck{Name: "a", Exists: "out.txt", Dir: "core"}, "takes none"},
		"two lines":        {taskCheck{Name: "a", Run: "make", Dir: "core\nweb"}, "one line"},
	} {
		k := taskContract{Objective: "x", Checks: []taskCheck{c.ch}}
		if err := k.normalize(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	ok := taskContract{Objective: "x", Checks: []taskCheck{{Name: "build", Run: "go build ./...", Dir: "core"}, {Name: "w", Run: "true", Dir: "/work/repo/web"}}}
	if err := ok.normalize(); err != nil {
		t.Errorf("a good contract refused: %v", err)
	}
	// What the task is not lent: no repository, or another folder.
	k := taskContract{Objective: "x", Checks: []taskCheck{{Name: "build", Run: "go build", Dir: "/work/repo/core"}}}
	if err := k.checkLent(false, nil); err == nil || !strings.Contains(err.Error(), "no repository is lent") {
		t.Errorf("no repo: %v", err)
	}
	if err := k.checkLent(true, nil); err != nil {
		t.Errorf("with a repo: %v", err)
	}
	k = taskContract{Objective: "x", Checks: []taskCheck{{Name: "report", Exists: "/work/folders/Invoices/Eriphe/r.pdf"}}}
	if err := k.checkLent(false, []string{"Photos"}); err == nil || !strings.Contains(err.Error(), `"Invoices", which is not lent`) {
		t.Errorf("other folder: %v", err)
	}
	if err := k.checkLent(false, []string{"Invoices"}); err != nil {
		t.Errorf("its folder: %v", err)
	}
	// run_task refuses it.
	c := newCatalogCore(t)
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	bad := &taskContract{Objective: "x", Checks: []taskCheck{{Name: "build", Run: "go build", Dir: "/work/repo"}}}
	if _, err := c.runTask(context.Background(), runRequest{Prompt: "x", Contract: bad}); err == nil || !strings.Contains(err.Error(), "no repository is lent") {
		t.Errorf("run_task: %v", err)
	}
}

// configTurn is a turn that ended with a check that could not run.
func configTurn(seq int) []EventRow {
	return []EventRow{
		ev(seq, "text", map[string]any{"text": doneReceipt}),
		ev(seq+1, "turn_end", map[string]any{"subtype": "success"}),
		ev(seq+2, "check", map[string]any{"name": "build", "ok": false, "exit_code": 125, "run": "go build ./...",
			"output": "eriphe-check: no folder /work/repo/repo",
			"config": `its folder /work/repo/repo does not exist (dir "repo" is relative to /work/repo, the task's working directory)`}),
		ev(seq+3, "check_stop", map[string]any{"check": "build", "reason": "config", "detail": "its folder /work/repo/repo does not exist"}),
	}
}

// A check that could not run is the contract's fault: twice the same way,
// the task is not stopped, the agent is not blamed, and the brain is told
// the exact reason and how to fix it. Once fixed, the checks go to the
// node with the next turn.
func TestConfigFailureGoesToBrain(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.setContract("t-1", &taskContract{Objective: "x", Verify: "auto", Checks: []taskCheck{{Name: "build", Run: "go build ./...", Dir: "repo", Timeout: 300}}})
	c.store.setSpec("t-1", nodeTaskSpec{Runtime: "claude-code", Repo: "owner/repo", Checks: []taskCheck{{Name: "build", Run: "go build ./...", Dir: "repo"}}})
	idleTask(t, c, "t-1", configTurn(1)...)
	for round := range 2 {
		h := c.store.harness("t-1")
		if h.StopReason != "" || h.LastFailure != "" || desiredOf(t, c, "t-1") == desiredStopped {
			t.Fatalf("round %d: stopped or counted: %+v", round, h)
		}
		if h.Failures[len(h.Failures)-1] != classConfig {
			t.Errorf("failures %v", h.Failures)
		}
		msg := lastEvent(t, c)
		for _, want := range []string{"Contrat mal configuré", `"build"`, "/work/repo/repo does not exist", "answer_task(task_id t-1, checks", "contrôle build mal configuré"} {
			if !strings.Contains(msg, want) {
				t.Errorf("round %d: brain event lacks %q: %s", round, want, msg)
			}
		}
		if strings.Contains(msg, "se dit finie") || strings.Contains(msg, "échoué (code") {
			t.Errorf("the agent is blamed: %s", msg)
		}
		if items, _ := c.store.inbox("t-1"); len(items) != 0 {
			t.Fatalf("sent back to the agent: %+v", items)
		}
		idleTask(t, c, "t-1", configTurn(10*(round+1))...)
	}
	if r := c.store.harness("t-1").Receipt; r == nil || r.Unsupported {
		t.Errorf("receipt %+v", r)
	}
	// The brain fixes the checks.
	if _, err := c.fixChecks(context.Background(), "t-1", []taskCheck{{Name: "build", Run: "go build ./...", Dir: "/work/repo/../x"}}, ""); err == nil {
		t.Error("a bad fix accepted")
	}
	res, err := c.fixChecks(context.Background(), "t-1", []taskCheck{{Name: "build", Run: "go build ./..."}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "checks fixed") {
		t.Errorf("result %q", res)
	}
	items, _ := c.store.inbox("t-1")
	if len(items) != 1 || !strings.Contains(items[0].Data["text"].(string), "fixed the checks") || items[0].Data["checks"] == nil {
		t.Fatalf("inbox %+v", items)
	}
	if k := c.store.contract("t-1"); len(k.Checks) != 1 || k.Checks[0].Dir != "" || k.Checks[0].Timeout != checkTimeout {
		t.Errorf("contract %+v", k)
	}
	if spec, _ := c.store.taskSpec("t-1"); len(spec.Checks) != 1 || spec.Checks[0].Dir != "" {
		t.Errorf("spec %+v", spec.Checks)
	}
	if task, _ := c.store.task("t-1"); task.State != stateRunning {
		t.Errorf("state %s", task.State)
	}
	if _, err := c.fixChecks(context.Background(), "t-1", []taskCheck{{Name: "build", Run: "true"}}, ""); err == nil {
		t.Error("fixed while running")
	}
}

// A real failure twice in a row still stops the task.
func TestRealCheckFailureTwiceStops(t *testing.T) {
	c := receiptCore(t)
	failing := func(seq int) []EventRow {
		return []EventRow{ev(seq, "text", map[string]any{"text": doneReceipt}), ev(seq+1, "turn_end", map[string]any{"subtype": "success"}),
			ev(seq+2, "check", map[string]any{"name": "tests", "ok": false, "exit_code": 1, "output": "FAIL"})}
	}
	idleTask(t, c, "t-1", failing(1)...)
	if h := c.store.harness("t-1"); h.StopReason != "" {
		t.Fatal("stopped at the first failure")
	}
	idleTask(t, c, "t-1", failing(10)...)
	if h := c.store.harness("t-1"); h.StopReason != stopSameFailure {
		t.Errorf("not stopped: %+v", h)
	}
}

// The brain hears what became of a stopped task's unpushed commits:
// pushed to its branch, or kept on the node; even when it stopped the task
// itself, if they were kept.
func TestStoppedWorkNote(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateStopped, Kind: "worker"})
	c.store.addEvents("t-1", []EventRow{ev(1, "work", map[string]any{"commits": 2, "branch": "eriphe/fix", "pushed": true})})
	c.notifyBrain("t-1", stateStopped)
	if msg := lastEvent(t, c); !strings.Contains(msg, "poussés sur la branche eriphe/fix") {
		t.Errorf("brain event %q", msg)
	}
	c.store.insertTask(&TaskRow{ID: "t-2", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateStopped, Kind: "worker"})
	c.store.set("brain_stopped:t-2", "1")
	c.store.addEvents("t-2", []EventRow{ev(1, "work", map[string]any{"commits": 1, "branch": "eriphe/fix", "kept": "kept/t-2", "reason": "the push was not possible: denied"})})
	c.notifyBrain("t-2", stateStopped)
	if msg := lastEvent(t, c); !strings.Contains(msg, "t-2") || !strings.Contains(msg, "gardé sur le nœud, dans kept/t-2") || !strings.Contains(msg, "denied") {
		t.Errorf("brain event %q", msg)
	}
}
