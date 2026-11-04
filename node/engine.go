package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// The engine is how the node runs containers: a handful of verbs with typed
// specifications, the way Kubernetes' CRI or Nomad's task drivers do. The
// node says what it wants (a task with its image, its folders and their
// access, its environment; a service; a helper that looks into a folder);
// the engine says it to its tool, with the hardening built in: a caller never
// passes a capability, a security option or a network of its own.
//
// Three engines: Docker through its CLI (engine_docker.go), Apple's
// container through its own (apple.go), and eriphe-node engine
// (engine_remote.go), which holds the Docker socket for the node and builds
// the containers itself (engine_server.go). Where they differ in a way the node must
// know (Apple's tasks each on a network of their own, folders seen only
// through the explorer, files written as the Mac's user), the engine says
// so in its traits, never the node by asking which engine it has.

type engine interface {
	traits() engineTraits

	// beginTask makes what a task needs before its container starts: with
	// an engine whose tasks have networks of their own, the task's, and the
	// node's address on it. endTask removes it once the container is gone.
	beginTask(taskID string) (network, gateway string, err error)
	endTask(network string)
	// runTask starts a task's container, its stdin and stdout the agent's.
	runTask(spec taskRunSpec) (*engineRun, error)
	// execSync runs a shell script in a task's running container.
	execSync(ctx context.Context, container string, spec execSpec) (execResult, error)
	// stop stops a task's or a service's container.
	stop(ctx context.Context, container string)

	// runService starts a service's container and, for http, the address
	// the node reaches it on; "" when the engine only knows it once the
	// container runs (serviceAddress).
	runService(spec serviceRunSpec) (run *engineRun, target string, err error)
	serviceAddress(ctx context.Context, container string, exited chan error) (string, error)
	// removeStray removes the containers carrying a label, whatever its
	// value, and what the node made for them.
	removeStray(label string)

	// runHelper runs a short-lived helper container: no network, a
	// read-only root, its mounts, its input, gone once done.
	runHelper(ctx context.Context, spec helperRunSpec) (stdout, stderr []byte, err error)
	// wake sends Wake-on-LAN packets from the host's network, for a node in
	// a container; errors.ErrUnsupported where there is no such way.
	wake(ctx context.Context, w wakeInfo) error
	pull(ctx context.Context, image string) error
	info(ctx context.Context) engineInfo

	// ensureNetwork makes the network the tasks share, if the engine has one.
	ensureNetwork() error
	// taskSubnets are the subnets of the network the tasks share.
	taskSubnets() ([]string, error)
	// mountable refuses a host path the engine cannot mount.
	mountable(path string) error
}

// engineTraits are where an engine differs in a way the node must know.
type engineTraits struct {
	// ownNetworks: each task is on a network of its own (beginTask), which
	// comes and goes with it; the node listens on every address and accepts
	// only these networks (taskListener).
	ownNetworks bool
	// explores: the node never reads a shared place itself; a helper does,
	// in a container (apple_explore.go).
	explores bool
	// mapsOwners: what a task writes in a folder lands as the user's, and
	// every file it sees is its own: no group to add, no owner to give back.
	mapsOwners bool
}

// mount is a host folder in a container.
type mount struct {
	src, dst string
	readOnly bool
	// inRoot: src is in the node's root, as the node sees it; the engine
	// finds it where its host has it (-host-root).
	inRoot bool
}

// taskRunSpec is a task's container.
type taskRunSpec struct {
	name, taskID, image string
	// network is the task's own (beginTask), with an engine that makes one.
	network string
	// systemNetwork: on eriphe-core's network too (the brain).
	systemNetwork bool
	// untrusted: the task gets the untrusted cache (cache.go).
	untrusted bool
	mounts    []mount
	// groups are added to the task's user, to read what they may.
	groups     []int
	env        map[string]string
	entrypoint string
	cmd        []string
	stderr     io.Writer
}

// serviceRunSpec is a service's container.
type serviceRunSpec struct {
	name, service string
	image         string
	args          []string
	user          string // uid:gid, never root
	stdio         bool   // else http, on port
	port          int
	writable      bool
	egress        bool
	mounts        []mount
	env           map[string]string
	stderr        io.Writer
}

// helperRunSpec is a short-lived helper's container.
type helperRunSpec struct {
	image, entrypoint string
	args              []string
	mounts            []mount
	stdin             []byte
	label             string // name=value
	// user it runs as; "" for root with the three file capabilities of a
	// task (hardening).
	user string
}

// execSpec is a script run in a task's container, as `sh -c script name
// args…`, with env (NAME=value) over the container's own.
type execSpec struct {
	env []string
	// setup runs before the script, in the same shell (PATH, say).
	setup  string
	script string
	name   string
	args   []string
	max    int // at most this many bytes of output, its end
}

// execResult is what a script printed (its end) and its exit code.
type execResult struct {
	output string
	exit   int
}

// engineInfo is what the node reports of its engine.
type engineInfo struct {
	docker  string // the Docker daemon's version, with Docker
	version string // the engine's, "" if it does not answer
	images  []string
}

// engineRun is a started container: its input and output when asked for,
// and its end.
type engineRun struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	wait   func() error
}

// Wait waits for the container to end.
func (r *engineRun) Wait() error { return r.wait() }

// engine is the node's engine: Apple's container with -engine container,
// eriphe-node engine with -engine-url, the docker CLI otherwise, or a
// test's.
func (n *node) engine() engine {
	if n.eng != nil {
		return n.eng
	}
	if n.opt.engine == engineContainer {
		return appleEngine{&n.opt, &n.nets}
	}
	if n.opt.engineURL != "" {
		return remoteEngine{o: &n.opt, nets: &n.nets}
	}
	return dockerEngine{&n.opt}
}

// startCLI starts an engine's CLI. The environment carries the values of
// the -e NAME the arguments name: they never appear on a command line. With
// pipe, the container's stdin and stdout are the run's; without, stdout goes
// to stderr too.
func startCLI(cli string, args []string, env map[string]string, pipe bool, stderr io.Writer) (*engineRun, error) {
	cmd := exec.Command(cli, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = stderr
	r := &engineRun{wait: cmd.Wait}
	if pipe {
		var err error
		if r.stdin, err = cmd.StdinPipe(); err != nil {
			return nil, err
		}
		if r.stdout, err = cmd.StdoutPipe(); err != nil {
			return nil, err
		}
	} else {
		cmd.Stdout = stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return r, nil
}

// envArgs are the -e NAME of an environment.
func envArgs(env map[string]string) []string {
	var args []string
	for k := range env {
		args = append(args, "-e", k)
	}
	return args
}

// cliExec is execSync with an engine's CLI (`docker exec`, `container
// exec`). The script runs in a subshell, its exit code printed after it
// (execExit): the CLI's own failure (the container gone, a proxy's refusal)
// is never taken for the script's.
func cliExec(ctx context.Context, cli, container string, spec execSpec) (execResult, error) {
	argv := []string{"exec"}
	for _, kv := range spec.env {
		argv = append(argv, "-e", kv)
	}
	wrapped := "(\n" + spec.script + "\n)\nprintf '\\n" + execExit + "%d\\n' $?"
	if spec.setup != "" {
		wrapped = spec.setup + "\n" + wrapped
	}
	argv = append(argv, container, "sh", "-c", wrapped, spec.name)
	argv = append(argv, spec.args...)
	cmd := exec.CommandContext(ctx, cli, argv...)
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	text := out.String()
	if ctx.Err() != nil {
		return execResult{tail(text, spec.max), -1}, errTimedOut
	}
	if i := strings.LastIndex(text, "\n"+execExit); i >= 0 {
		if code, perr := strconv.Atoi(strings.TrimSpace(text[i+1+len(execExit):])); perr == nil {
			return execResult{tail(text[:i], spec.max), code}, nil
		}
	}
	if err == nil {
		err = errors.New("no exit code")
	}
	return execResult{tail(text, spec.max), -1}, fmt.Errorf("it did not run in the container (%v): %s", err, tail(strings.TrimSpace(text), 300))
}
