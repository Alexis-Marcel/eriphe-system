package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// execNode is lifecycleNode whose docker also answers exec with body.
func execNode(t *testing.T, run, body string) (*node, string) {
	t.Helper()
	n, dir := lifecycleNode(t, run)
	raw, err := os.ReadFile(n.opt.docker)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(raw), "case \"$1\" in\n", "case \"$1\" in\nexec)\n"+body+"\n  ;;\n", 1)
	if err := os.WriteFile(n.opt.docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return n, dir
}

func typedEvents(task *Task, typ string) []Event {
	task.mu.Lock()
	defer task.mu.Unlock()
	var out []Event
	for _, e := range task.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// A task with a repository reports the files of its diff when its turn
// ends, in the container, before it is reported idle.
func TestTurnEndDiff(t *testing.T) {
	n, dir := execNode(t, agentRun, `  printf '%s\n' "$@" > `+"\"$(dirname \"$0\")/exec\""+`
  printf 'core/a.go\nREADME.md\n'`)
	n.reconcile([]assignment{{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi", Repo: "owner/repo"}}})
	task := n.get("t-1")
	waitFor(t, func() bool { return task.state() == stateIdle && !task.busyFinishing() })
	diffs := typedEvents(task, "diff")
	if len(diffs) != 1 {
		t.Fatalf("diff events %v", eventTypes(task))
	}
	if files, _ := diffs[0].Data["files"].([]string); !slices.Equal(files, []string{"core/a.go", "README.md"}) {
		t.Errorf("files %v", diffs[0].Data["files"])
	}
	args := fileLines(filepath.Join(dir, "exec"))
	if len(args) < 4 || args[0] != "exec" || args[1] != "eriphe-task-t-1" || args[2] != "sh" || args[3] != "-c" {
		t.Errorf("exec args %q", args)
	}
	// The diff comes after the turn's end, and before the task says it is idle.
	types := eventTypes(task)
	if i, j := slices.Index(types, "turn_end"), slices.Index(types, "diff"); i < 0 || j < i {
		t.Errorf("events %v", types)
	}
}

// Without a repository, nothing runs in the container.
func TestTurnEndNoRepo(t *testing.T) {
	n, dir := execNode(t, agentRun, `  touch "$(dirname "$0")/exec"`)
	n.reconcile([]assignment{{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi"}}})
	task := n.get("t-1")
	waitFor(t, func() bool { return task.state() == stateIdle && !task.busyFinishing() })
	if _, err := os.Stat(filepath.Join(dir, "exec")); err == nil {
		t.Error("exec ran without a repository")
	}
}

// While the end of a turn is looked at, eriphe-core hears "running".
func TestReportedStateWhileFinishing(t *testing.T) {
	task := &Task{State: stateIdle, finishing: 1}
	if s := task.reportedState(); s != stateRunning {
		t.Errorf("reported %q", s)
	}
	task.finishing = 0
	if s := task.reportedState(); s != stateIdle {
		t.Errorf("reported %q", s)
	}
}

// checkExec is a container's exec for the checks: a file check passes for
// /work/out.txt; a command passes if it is "pass", fails the same way if
// "same", differently each time if "vary". Each check is logged in checks.
const checkExec = `  d=$(dirname "$0")
  case "$5" in
  *"test -e"*) echo "exists $7" >> "$d/checks"; [ "$7" = "/work/out.txt" ] && exit 0; exit 1 ;;
  *timeout*) echo "run $9 in $7 for $8" >> "$d/checks"
    case "$9" in
    pass) exit 0 ;;
    same) echo "same output"; exit 1 ;;
    vary) echo "output $(wc -l < "$d/checks")"; exit 2 ;;
    esac ;;
  esac`

func settled(task *Task) bool { return task.state() == stateIdle && !task.busyFinishing() }

func checkTask(t *testing.T, spec TaskSpec) (*Task, string) {
	t.Helper()
	n, dir := execNode(t, agentRun, checkExec)
	spec.Runtime, spec.Prompt = "claude-code", "hi"
	n.reconcile([]assignment{{ID: "t-1", Desired: "running", Spec: spec}})
	task := n.get("t-1")
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check")) > 0 })
	return task, dir
}

// The checks run when the turn ends, the files first, each an event.
func TestChecksPass(t *testing.T) {
	task, dir := checkTask(t, TaskSpec{Checks: []TaskCheck{
		{Name: "tests", Run: "pass", Dir: "core", Timeout: 60},
		{Name: "report", Exists: "/work/out.txt"},
	}})
	if got := fileLines(filepath.Join(dir, "checks")); !slices.Equal(got, []string{"exists /work/out.txt", "run pass in /work/core for 60"}) {
		t.Errorf("checks ran %q", got)
	}
	checks := typedEvents(task, "check")
	if len(checks) != 2 || checks[0].Data["name"] != "report" || checks[1].Data["ok"] != true || checks[1].Data["run"] != "pass" {
		t.Errorf("check events %+v", checks)
	}
	if len(typedEvents(task, "retry")) != 0 || len(fileLines(filepath.Join(dir, "stdin"))) != 1 {
		t.Error("a passing check went back to the agent")
	}
}

// A failed check goes back to the agent with the end of its output; the
// same failure twice is not sent back again, and the checks after the
// failed one do not run.
func TestCheckFailsSameTwice(t *testing.T) {
	task, dir := checkTask(t, TaskSpec{CheckRounds: 3, Checks: []TaskCheck{{Name: "tests", Run: "same"}, {Name: "lint", Run: "pass"}}})
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check_stop")) == 1 })
	in := fileLines(filepath.Join(dir, "stdin"))
	if len(in) != 2 || !strings.Contains(in[1], `Eriphe's check \"tests\" failed (exit code 1)`) || !strings.Contains(in[1], "same output") {
		t.Fatalf("agent input %q", in)
	}
	if stop := typedEvents(task, "check_stop")[0]; stop.Data["reason"] != "same_failure" {
		t.Errorf("stop %+v", stop.Data)
	}
	if retries := typedEvents(task, "retry"); len(retries) != 1 || retries[0].Data["round"] != 1 {
		t.Errorf("retries %+v", retries)
	}
	for _, l := range fileLines(filepath.Join(dir, "checks")) {
		if strings.Contains(l, "run pass") {
			t.Error("a check ran after one failed")
		}
	}
}

// The rounds bound how many times failures go back.
func TestCheckRoundsSpent(t *testing.T) {
	task, dir := checkTask(t, TaskSpec{CheckRounds: 1, Checks: []TaskCheck{{Name: "tests", Run: "vary"}}})
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check_stop")) == 1 })
	if stop := typedEvents(task, "check_stop")[0]; stop.Data["reason"] != "rounds" {
		t.Errorf("stop %+v", stop.Data)
	}
	if in := fileLines(filepath.Join(dir, "stdin")); len(in) != 2 {
		t.Errorf("agent input %q", in)
	}
	if checks := typedEvents(task, "check"); len(checks) != 2 || checks[1].Data["exit_code"] != 2 {
		t.Errorf("checks %+v", checks)
	}
}

// A turn asked with skip_checks (its receipt again) is not checked.
func TestSkipChecks(t *testing.T) {
	n, dir := execNode(t, agentRun, checkExec)
	a := assignment{ID: "t-1", Desired: "running", Spec: TaskSpec{Runtime: "claude-code", Prompt: "hi", Checks: []TaskCheck{{Name: "tests", Run: "pass"}}}}
	n.reconcile([]assignment{a})
	task := n.get("t-1")
	waitFor(t, func() bool { return settled(task) && len(typedEvents(task, "check")) == 1 })
	a.Inbox = []inboxItem{{Seq: 1, Kind: "message", Data: map[string]any{"text": "your receipt?", "skip_checks": true}}}
	n.reconcile([]assignment{a})
	waitFor(t, func() bool { return len(fileLines(filepath.Join(dir, "stdin"))) == 2 && settled(task) })
	if got := fileLines(filepath.Join(dir, "checks")); len(got) != 1 {
		t.Errorf("checks ran %q", got)
	}
}
