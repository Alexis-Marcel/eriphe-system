package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
)

// An agentRuntime is an agent program of the worker image and the way the node
// talks to it, over the task container's stdin and stdout. Whatever the
// runtime, the agent's work comes back as the task's events, and its
// permission requests go through Task.decide.
type agentRuntime interface {
	// command is the agent's command line in the container, and the
	// environment it needs on top of the task's.
	command(n *node, t *Task) (args []string, env map[string]string)
	// attach drives the agent through the container's stdin.
	attach(t *Task, stdin io.WriteCloser) session
}

// session is an agent at work in its container.
type session interface {
	// run gives the agent its first turn, then follows its output until it
	// ends. An error is a failure of the agent, not of the container.
	run(first string, stdout io.Reader) error
	// send gives the agent its next turn: errNotIdle during a turn.
	send(text string) error
	// stop interrupts the agent's turn and closes its input.
	stop()
}

var runtimes = map[string]agentRuntime{
	"claude-code": claudeCode{},
	"opencode":    opencodeRuntime,
	"codex":       codexRuntime{},
	"acp":         customACPRuntime,
}

func runtimeFor(name string) (agentRuntime, error) {
	if name == "" {
		name = "claude-code" // tasks from before runtimes were named
	}
	rt, ok := runtimes[name]
	if !ok {
		return nil, fmt.Errorf("unknown runtime %q", name)
	}
	return rt, nil
}

// workDir is where the entrypoint leaves the agent: the repository if there
// is one, then the task's dir. The same logic as the entrypoint, for agents
// that must be told.
func workDir(t *Task) string {
	dir := "/work"
	if _, err := os.Stat(filepath.Join(t.dir, "work", "repo")); t.Spec.Repo != "" || err == nil {
		dir = "/work/repo"
	}
	if d := t.Spec.Dir; d != "" {
		if path.IsAbs(d) {
			return path.Clean(d)
		}
		dir = path.Join(dir, d)
	}
	return dir
}

// tailBuffer keeps the end of what is written to it: an agent's stderr can
// be long, only its last words matter when it fails.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 64 << 10

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > tailMax {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-tailMax:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
