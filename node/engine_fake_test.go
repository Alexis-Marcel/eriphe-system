package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"
)

// The tests of the node's logic run against fakeEngine, in memory; those of
// what each engine says to its tool render the specifications with the
// helpers below (a fake CLI records the arguments).

func dockerOf(n *node) dockerEngine { return dockerEngine{&n.opt} }
func appleOf(n *node) appleEngine   { return appleEngine{&n.opt, &n.nets} }

// render is a mount's arguments with the node's engine.
func (n *node) render(t *testing.T, mounts []mount, groups []int) []string {
	t.Helper()
	switch e := n.engine().(type) {
	case appleEngine:
		args, err := mountArgs(mounts)
		if err != nil {
			t.Fatal(err)
		}
		return args
	case dockerEngine:
		var args []string
		for _, m := range mounts {
			args = append(args, e.mountArgs(m)...)
		}
		for _, g := range groups {
			args = append(args, "--group-add", strconv.Itoa(g))
		}
		return args
	}
	t.Fatalf("no arguments with %T", n.engine())
	return nil
}

// folderArgs are the arguments of a task's folders with the node's engine.
func (n *node) folderArgs(t *testing.T, task *Task) ([]string, error) {
	t.Helper()
	mounts, groups, err := n.folderMounts(task)
	if err != nil {
		return nil, err
	}
	return n.render(t, mounts, groups), nil
}

// serviceArgs is a service's command line with the node's engine, the
// environment of that command and, for http, its address.
func (n *node) serviceArgs(name string, spec serviceSpec) (args, env []string, target string, err error) {
	r, err := n.serviceRun(name, spec)
	if err != nil {
		return nil, nil, "", err
	}
	switch e := n.engine().(type) {
	case appleEngine:
		args, err = e.serviceArgs(r)
	case dockerEngine:
		args, target, err = e.serviceArgs(r)
	}
	if err != nil {
		return nil, nil, "", err
	}
	env = os.Environ()
	for k, v := range r.env {
		env = append(env, k+"="+v)
	}
	return args, env, target, nil
}

// fakeEngine is an engine in memory: it records what the node asks, and
// answers what a test set.
type fakeEngine struct {
	mu      sync.Mutex
	tr      engineTraits
	tasks   []taskRunSpec
	execs   []execSpec
	stopped []string
	stray   []string
	helpers []helperRunSpec
	pulled  []string
	// exec answers execSync; nil for exit code 0 and no output.
	exec func(container string, spec execSpec) (execResult, error)
	// agent plays a task's container: it reads the agent's input and
	// writes its output; nil for a container that exits at once.
	agent func(stdin io.Reader, stdout io.Writer)
	// running are the started containers' input, by name: stop closes it.
	running map[string]*io.PipeReader
	inf     engineInfo
	wakeErr error
}

func (f *fakeEngine) traits() engineTraits { return f.tr }

func (f *fakeEngine) beginTask(taskID string) (string, string, error) {
	if f.tr.ownNetworks {
		return taskNetwork(taskID), "192.0.2.1", nil
	}
	return "", "", nil
}

func (f *fakeEngine) endTask(string) {}

func (f *fakeEngine) runTask(spec taskRunSpec) (*engineRun, error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	f.mu.Lock()
	f.tasks = append(f.tasks, spec)
	if f.running == nil {
		f.running = map[string]*io.PipeReader{}
	}
	f.running[spec.name] = inR
	agent := f.agent
	f.mu.Unlock()
	done := make(chan struct{})
	go func() {
		if agent != nil {
			agent(inR, outW)
		}
		outW.Close()
		close(done)
	}()
	return &engineRun{stdin: inW, stdout: outR, wait: func() error { <-done; return nil }}, nil
}

func (f *fakeEngine) execSync(_ context.Context, container string, spec execSpec) (execResult, error) {
	f.mu.Lock()
	f.execs = append(f.execs, spec)
	answer := f.exec
	f.mu.Unlock()
	if answer == nil {
		return execResult{}, nil
	}
	return answer(container, spec)
}

func (f *fakeEngine) stop(_ context.Context, container string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, container)
	if in := f.running[container]; in != nil {
		in.Close()
	}
}

func (f *fakeEngine) runService(serviceRunSpec) (*engineRun, string, error) {
	return nil, "", errors.New("no services in the fake engine")
}

func (f *fakeEngine) serviceAddress(context.Context, string, chan error) (string, error) {
	return "", errors.New("no address")
}

func (f *fakeEngine) removeStray(label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stray = append(f.stray, label)
}

func (f *fakeEngine) runHelper(_ context.Context, spec helperRunSpec) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.helpers = append(f.helpers, spec)
	return nil, nil, errors.New("no helpers in the fake engine")
}

func (f *fakeEngine) wake(context.Context, wakeInfo) error { return f.wakeErr }

func (f *fakeEngine) pull(_ context.Context, image string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulled = append(f.pulled, image)
	return nil
}

func (f *fakeEngine) info(context.Context) engineInfo { return f.inf }
func (f *fakeEngine) ensureNetwork() error            { return nil }
func (f *fakeEngine) taskSubnets() ([]string, error)  { return nil, nil }
func (f *fakeEngine) mountable(string) error          { return nil }
