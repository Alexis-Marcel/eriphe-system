package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// When an agent's turn ends, the node looks at the task's container before
// eriphe-core hears that it is idle: the files the repository's diff shows
// (a "diff" event), which eriphe-core compares with the agent's receipt.
// Until then the task is reported running, so that nothing reads the turn
// half done.

// diffScript lists the files changed in the task's repository since the
// commit it started from: committed or not, and the new ones, at most 200.
const diffScript = `cd /work/repo 2>/dev/null || exit 0
base=""
if [ -n "$ERIPHE_REF" ]; then base=$(git merge-base HEAD "origin/$ERIPHE_REF" 2>/dev/null || true); fi
if [ -z "$base" ]; then base=$(git merge-base HEAD origin/HEAD 2>/dev/null || true); fi
{ if [ -n "$base" ]; then git diff --name-only "$base"; else git diff --name-only HEAD; fi
  git ls-files --others --exclude-standard; } 2>/dev/null | sort -u | head -200`

// afterTurn runs once the task is idle, then lets it be reported so.
func (n *node) afterTurn(t *Task) {
	defer func() {
		t.mu.Lock()
		t.finishing = false
		t.mu.Unlock()
		n.poke()
	}()
	if t.Spec.Repo != "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		out, code, err := n.execTask(ctx, t, diffScript, 64<<10)
		cancel()
		if err == nil && code == 0 {
			files := []string{}
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					files = append(files, l)
				}
			}
			t.emit("diff", map[string]any{"files": files})
		}
	}
}

// execTask runs a shell script in the task's running container, with the
// container's own environment and rights (docker exec, or container exec
// with Apple's engine), and returns the end of its output (at most max
// bytes) and its exit code. err is for a script that could not run.
func (n *node) execTask(ctx context.Context, t *Task, script string, max int) (string, int, error) {
	cmd := exec.CommandContext(ctx, n.opt.docker, "exec", containerName(t), "sh", "-c", script)
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	text := tail(out.String(), max)
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return text, exit.ExitCode(), nil
	}
	if err != nil {
		return text, -1, err
	}
	return text, 0, nil
}
