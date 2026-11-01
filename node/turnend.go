package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"
)

// When an agent's turn ends, the node looks at the task's container before
// eriphe-core hears that it is idle: the files the repository's diff shows
// (a "diff" event), then the checks of the task's contract, each a "check"
// event. A failed check goes back to the agent, a bounded number of times,
// without eriphe-core or its brain: the agent can claim that the tests
// pass, only the node's own run says so. Until then the task is reported
// running, so that nothing reads the turn half done.

// TaskCheck is one check of a task's contract: a command that must exit 0
// within Timeout seconds, run in Dir, or a path that must exist.
type TaskCheck struct {
	Name    string `json:"name"`
	Run     string `json:"run,omitempty"`
	Exists  string `json:"exists,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// Limits of the checks.
const (
	checkOutputMax    = 4 << 10 // the end of a check's output kept in its event
	checkTimeoutMax   = 3600
	defaultCheckRound = 2  // failed checks sent back when the spec gives no number
	maxCheckRounds    = 5  // and never more
	checkGrace        = 30 // seconds the node waits beyond a check's own timeout
)

// diffScript lists the files changed in the task's repository since the
// commit it started from: committed or not, and the new ones, at most 200.
const diffScript = `cd /work/repo 2>/dev/null || exit 0
base=""
if [ -n "$ERIPHE_REF" ]; then base=$(git merge-base HEAD "origin/$ERIPHE_REF" 2>/dev/null || true); fi
if [ -z "$base" ]; then base=$(git merge-base HEAD origin/HEAD 2>/dev/null || true); fi
{ if [ -n "$base" ]; then git diff --name-only "$base"; else git diff --name-only HEAD; fi
  git ls-files --others --exclude-standard; } 2>/dev/null | sort -u | head -200`

// runCheckScript runs a check's command in its folder ($1), under timeout
// when the image has it ($2 seconds), as the shell's own argument ($3):
// nothing of it is quoted into the script.
const runCheckScript = `cd "$1" || exit 125
if command -v timeout >/dev/null 2>&1; then exec timeout -k 5 "$2" sh -c "$3"; fi
exec sh -c "$3"`

const existsCheckScript = `test -e "$1"`

// afterTurn runs once the task is idle, then lets it be reported so.
func (n *node) afterTurn(t *Task) {
	defer func() {
		t.mu.Lock()
		t.finishing--
		t.mu.Unlock()
		n.poke()
	}()
	t.mu.Lock()
	skip := t.skipChecks
	t.skipChecks = false
	t.mu.Unlock()
	if t.Spec.Repo != "" {
		n.reportDiff(t)
	}
	if len(t.Spec.Checks) == 0 || skip {
		return
	}
	failed, ok := n.runChecks(t)
	if ok {
		return
	}
	n.sendBack(t, failed)
}

func (n *node) reportDiff(t *Task) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, code, err := n.execTask(ctx, t, 64<<10, diffScript)
	if err != nil || code != 0 {
		return
	}
	files := []string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	t.emit("diff", map[string]any{"files": files})
}

// checkResult is a check as it ran.
type checkResult struct {
	check    TaskCheck
	ok       bool
	code     int
	output   string
	duration time.Duration
}

// runChecks runs the task's checks in its container, the files that must
// exist first, then the commands in their order, and stops at the first
// that fails. Each is a "check" event.
func (n *node) runChecks(t *Task) (checkResult, bool) {
	var exists, runs []TaskCheck
	for _, ch := range t.Spec.Checks {
		if ch.Exists != "" {
			exists = append(exists, ch)
		} else if ch.Run != "" {
			runs = append(runs, ch)
		}
	}
	for _, ch := range append(exists, runs...) {
		r := n.runCheck(t, ch)
		data := map[string]any{"name": ch.Name, "ok": r.ok, "exit_code": r.code, "duration_ms": r.duration.Milliseconds()}
		if ch.Run != "" {
			data["run"] = ch.Run
		} else {
			data["exists"] = ch.Exists
		}
		if r.output != "" {
			data["output"] = r.output
		}
		t.emit("check", data)
		if !r.ok {
			return r, false
		}
	}
	return checkResult{}, true
}

func (n *node) runCheck(t *Task, ch TaskCheck) checkResult {
	timeout := ch.Timeout
	if timeout <= 0 || timeout > checkTimeoutMax {
		timeout = 300
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+checkGrace)*time.Second)
	defer cancel()
	start := time.Now()
	var out string
	var code int
	var err error
	if ch.Exists != "" {
		out, code, err = n.execTask(ctx, t, checkOutputMax, existsCheckScript, checkPath(t, ch.Exists))
	} else {
		out, code, err = n.execTask(ctx, t, checkOutputMax, runCheckScript, checkPath(t, ch.Dir), fmt.Sprint(timeout), ch.Run)
	}
	r := checkResult{check: ch, code: code, output: strings.TrimSpace(out), duration: time.Since(start)}
	if err != nil {
		r.code, r.output = -1, strings.TrimSpace(r.output+"\n"+err.Error())
	}
	r.ok = err == nil && code == 0
	return r
}

// checkPath is a check's path in the container: under /work, or relative
// to where the agent works.
func checkPath(t *Task, p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(workDir(t), p)
}

// sendBack gives a failed check back to the agent as its next turn, unless
// the rounds are spent or it failed the same way as the last time: then
// the turn ends as it is, and eriphe-core reads the failure.
func (n *node) sendBack(t *Task, r checkResult) {
	sig := fmt.Sprintf("%s\x00%d\x00%s", r.check.Name, r.code, r.output)
	rounds := t.Spec.CheckRounds
	if rounds <= 0 {
		rounds = defaultCheckRound
	}
	rounds = min(rounds, maxCheckRounds)
	t.mu.Lock()
	run, same, spent := t.run, sig == t.lastFailure, t.checkRounds >= rounds
	t.lastFailure = sig
	t.mu.Unlock()
	if run == nil || same || spent {
		reason := "rounds"
		if same {
			reason = "same_failure"
		}
		t.emit("check_stop", map[string]any{"check": r.check.Name, "reason": reason})
		return
	}
	msg := fmt.Sprintf("Eriphe's check %q failed (exit code %d). The end of its output:\n```\n%s\n```\nFix the cause, then end your turn with your receipt.",
		r.check.Name, r.code, tail(r.output, 2000))
	t.mu.Lock()
	t.checkRounds++
	round := t.checkRounds
	t.mu.Unlock()
	t.emit("retry", map[string]any{"reason": "check", "check": r.check.Name, "round": round})
	// Running before the agent hears it: its next turn may end at once.
	t.setState(stateRunning)
	if err := run.send(msg); err != nil {
		t.mu.Lock()
		t.State = stateIdle // as it was, without looking at the turn again
		t.mu.Unlock()
		t.emit("state", map[string]any{"state": stateIdle})
	}
	t.save()
}

// execTask runs a shell script in the task's running container, with the
// container's own environment and rights (docker exec, or container exec
// with Apple's engine), its arguments as $1, $2…, and returns the end of
// its output (at most max bytes) and its exit code. err is for a script
// that could not run, or ran out of time.
func (n *node) execTask(ctx context.Context, t *Task, max int, script string, args ...string) (string, int, error) {
	argv := append([]string{"exec", containerName(t), "sh", "-c", script, "eriphe-check"}, args...)
	cmd := exec.CommandContext(ctx, n.opt.docker, argv...)
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	text := tail(out.String(), max)
	if ctx.Err() != nil {
		return text, -1, errors.New("timed out")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return text, exit.ExitCode(), nil
	}
	if err != nil {
		return text, -1, err
	}
	return text, 0, nil
}
