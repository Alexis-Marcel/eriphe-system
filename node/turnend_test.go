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
	task := &Task{State: stateIdle, finishing: true}
	if s := task.reportedState(); s != stateRunning {
		t.Errorf("reported %q", s)
	}
	task.finishing = false
	if s := task.reportedState(); s != stateIdle {
		t.Errorf("reported %q", s)
	}
}
