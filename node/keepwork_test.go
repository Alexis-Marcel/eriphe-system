package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// configExec is a container's exec for the checks of a misconfigured
// contract: a folder named missing is not there, the command nocmd is not
// found, any other command passes. Each check is logged in checks.
const configExec = `  d=$(dirname "$0")
  case "$5" in
  *timeout*) echo "run $9 in $7" >> "$d/checks"
    case "$7" in */missing) echo "eriphe-check: no folder $7"; exit 125 ;; esac
    case "$9" in nocmd) echo "sh: 1: nocmd: not found"; exit 127 ;; esac
    exit 0 ;;
  esac`

// A check whose folder is not there could not run: a config failure, not
// sent back to the agent, the task left idle for eriphe-core and the brain.
// Once the brain fixes the checks, the next turn runs the new ones.
func TestCheckMissingDirIsConfig(t *testing.T) {
	n, dir := execNode(t, agentRun, configExec)
	a := assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi",
		Checks: []TaskCheck{{Name: "build", Run: "go build ./...", Dir: "missing"}}}}
	n.reconcile([]assignment{a})
	task := n.get("t-1")
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check_stop")) == 1 })
	checks := typedEvents(task, "check")
	if len(checks) != 1 || checks[0].Data["ok"] != false {
		t.Fatalf("checks %+v", checks)
	}
	cfg, _ := checks[0].Data["config"].(string)
	if !strings.Contains(cfg, "/work/missing does not exist") || !strings.Contains(cfg, `dir "missing" is relative to /work`) {
		t.Errorf("config %q", cfg)
	}
	if stop := typedEvents(task, "check_stop")[0]; stop.Data["reason"] != "config" || stop.Data["detail"] != cfg {
		t.Errorf("stop %+v", stop.Data)
	}
	if len(typedEvents(task, "retry")) != 0 || len(fileLines(filepath.Join(dir, "stdin"))) != 1 {
		t.Error("a misconfigured check went back to the agent")
	}
	if task.state() != stateIdle {
		t.Errorf("state %s", task.state())
	}
	// The brain fixes the contract: the message carries the new checks.
	a.Inbox = []inboxItem{{Seq: 1, Kind: "message", Data: map[string]any{"text": "carry on",
		"checks": []any{map[string]any{"name": "build", "run": "go build ./..."}}}}}
	n.reconcile([]assignment{a})
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check")) == 2 })
	if c := typedEvents(task, "check")[1]; c.Data["ok"] != true {
		t.Errorf("fixed check %+v", c.Data)
	}
	if got := fileLines(filepath.Join(dir, "checks")); len(got) != 2 || got[1] != "run go build ./... in /work" {
		t.Errorf("checks ran %q", got)
	}
}

// A command that is not found (exit code 127) is a config failure too.
func TestCheckCommandNotFoundIsConfig(t *testing.T) {
	n, _ := execNode(t, agentRun, configExec)
	n.reconcile([]assignment{{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi",
		Checks: []TaskCheck{{Name: "lint", Run: "nocmd"}}}}})
	task := n.get("t-1")
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check_stop")) == 1 })
	cfg, _ := typedEvents(task, "check")[0].Data["config"].(string)
	if !strings.Contains(cfg, "not found (exit code 127)") || len(typedEvents(task, "retry")) != 0 {
		t.Errorf("config %q, events %v", cfg, eventTypes(task))
	}
}

// A check that ran and failed is not a config failure.
func TestCheckConfigFault(t *testing.T) {
	task := &Task{Spec: TaskSpec{Repo: "owner/repo"}}
	for _, c := range []struct {
		ch   TaskCheck
		r    checkResult
		err  error
		want string
	}{
		{TaskCheck{Run: "go test", Dir: "repo"}, checkResult{code: 125, output: noFolderMark + "/work/repo/repo"}, nil, "/work/repo/repo does not exist (dir \"repo\" is relative to /work/repo"},
		{TaskCheck{Run: "git bisect run x"}, checkResult{code: 125, output: "bisect skipped"}, nil, ""},
		{TaskCheck{Run: "go test"}, checkResult{code: 1, output: "FAIL"}, nil, ""},
		{TaskCheck{Run: "go test"}, checkResult{code: -1}, errTimedOut, ""},
		{TaskCheck{Run: "go test"}, checkResult{code: -1}, os.ErrNotExist, "could not be started"},
		{TaskCheck{Exists: "/work/out.txt"}, checkResult{code: 1}, nil, ""},
	} {
		got := checkConfigFault(task, c.ch, c.r, c.err)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%+v: %q, want %q", c.ch, got, c.want)
		}
	}
}

// saveExec is a container's exec for saveWorkScript: it logs the branch
// given and prints out; the turn's diff prints nothing.
func saveExec(out string) string {
	return `  d=$(dirname "$0")
  case "$5" in
  *rev-list*) echo "$7" >> "$d/saved"; printf '` + out + `' ;;
  esac`
}

// keepTask runs a task with a repository and a lent branch to its first
// turn's end, its /work/repo made, then stops it as eriphe-core asks.
func keepTask(t *testing.T, out string) (*node, *Task, string) {
	t.Helper()
	n, dir := execNode(t, agentRun, saveExec(out))
	a := assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi", Repo: "owner/repo", Branch: "eriphe/fix"}}
	n.reconcile([]assignment{a})
	task := n.get("t-1")
	waitFor(t, func() bool { return settled(task) })
	if err := os.MkdirAll(filepath.Join(n.taskDir("t-1"), "work", "repo", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	a.Desired = "stopped"
	n.reconcile([]assignment{a})
	waitFor(t, func() bool { return finished(task.state()) && len(typedEvents(task, "work")) > 0 })
	if got := fileLines(filepath.Join(dir, "saved")); len(got) != 1 || got[0] != "eriphe/fix" {
		t.Errorf("saved with branch %q", got)
	}
	return n, task, dir
}

// Commits on the lent branch are pushed before the container stops, and
// the repository is not kept.
func TestStopPushesLentBranch(t *testing.T) {
	n, task, _ := keepTask(t, `ahead=2\npushed=1\nleft=0\n`)
	w := typedEvents(task, "work")[0].Data
	if w["pushed"] != true || w["branch"] != "eriphe/fix" || w["commits"] != 2 || w["kept"] != nil {
		t.Errorf("work %+v", w)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(n.opt.root, keptDir, "t-1")); err == nil {
		t.Error("a pushed repository was kept")
	}
}

// A push that is not possible keeps the repository on the node, under
// kept/<task>, and says so; old ones are pruned like the snapshots.
func TestStopKeepsUnpushedWork(t *testing.T) {
	n, task, _ := keepTask(t, `ahead=1\nremote: Permission denied\nleft=1\n`)
	w := typedEvents(task, "work")[0].Data
	if w["kept"] != "kept/t-1" || w["pushed"] != nil || !strings.Contains(w["reason"].(string), "Permission denied") {
		t.Errorf("work %+v", w)
	}
	kept := filepath.Join(n.opt.root, keptDir, "t-1")
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(kept, "repo", ".git"))
		return err == nil
	})
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(kept, "meta.json"))
		return err == nil
	})
	n.pruneKept()
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("a fresh kept repository was pruned")
	}
	n.opt.snapshotDays = 1
	old := time.Now().Add(-48 * time.Hour)
	os.WriteFile(filepath.Join(kept, "meta.json"), []byte(`{"task":"t-1","time":"`+old.Format(time.RFC3339)+`"}`), 0o600)
	n.pruneKept()
	if _, err := os.Stat(kept); err == nil {
		t.Error("an old kept repository was not pruned")
	}
}

// Without commits no remote has, nothing is said or kept.
func TestStopNothingToSave(t *testing.T) {
	n, dir := execNode(t, agentRun, saveExec(`ahead=0\n`))
	a := assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi", Repo: "owner/repo"}}
	n.reconcile([]assignment{a})
	task := n.get("t-1")
	waitFor(t, func() bool { return settled(task) })
	a.Desired = "stopped"
	n.reconcile([]assignment{a})
	waitFor(t, func() bool { return finished(task.state()) })
	if len(fileLines(filepath.Join(dir, "saved"))) != 1 || len(typedEvents(task, "work")) != 0 {
		t.Errorf("events %v", eventTypes(task))
	}
}

func TestParseSavedWork(t *testing.T) {
	w := parseSavedWork("ahead=3\nerror: failed to push\nleft=3\n")
	if w.ahead != 3 || w.left != 3 || w.pushed || w.output != "error: failed to push" {
		t.Errorf("%+v", w)
	}
	if w := parseSavedWork("ahead=0\n"); w.ahead != 0 || w.left != 0 {
		t.Errorf("%+v", w)
	}
}
