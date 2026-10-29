package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func containerName(t *Task) string { return "eriphe-task-" + t.ID }

// The container puts the injected agent first in PATH, makes the cache's
// apt directories, makes git ask Eriphe for its credentials when
// eriphe-cred is there (credential.go; a legacy GH_TOKEN identity keeps its
// own way), clones the repository if asked (once: /work is kept across
// restarts; git is installed first if the image lacks it), makes what it
// writes group-writable when it has folders (the user's group shares
// them), then becomes the agent its runtime names. Nothing it prints
// before goes to stdout, the agent's channel.
const entrypoint = `set -e
case ":$PATH:" in *":` + injectDir + `/bin:"*) ;; *) export PATH="` + injectDir + `/bin:$PATH" ;; esac
if [ ! -w "${HOME:-/}" ]; then export HOME=/tmp/home; mkdir -p "$HOME"; fi
if [ -w /cache ]; then mkdir -p /cache/apt/archives/partial /cache/apt/lists/partial 2>/dev/null || true; fi
cd /work
if [ -n "$ERIPHE_FOLDERS" ]; then umask 002; fi
if [ -z "$GH_TOKEN" ] && [ -n "$ERIPHE_CRED_URL" ] && [ -x ` + injectDir + `/bin/eriphe-cred ]; then
  export GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0=` + injectDir + `/bin/eriphe-cred \
    GIT_CONFIG_KEY_1=credential.useHttpPath GIT_CONFIG_VALUE_1=true
fi
if [ -n "$ERIPHE_REPO" ] && [ ! -d repo ]; then
  if ! command -v git >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then
    echo "eriphe: installing git" >&2
    { apt-get update -qq && apt-get install -y -qq git; } >&2
  fi
  git clone --quiet ${ERIPHE_REF:+--branch "$ERIPHE_REF"} "$ERIPHE_REPO" repo >&2
  cd repo
  if [ -n "$ERIPHE_BRANCH" ]; then git checkout --quiet -B "$ERIPHE_BRANCH" >&2; fi
elif [ -d repo ]; then
  cd repo
fi
if [ -n "$ERIPHE_DIR" ]; then cd "$ERIPHE_DIR"; fi
exec "$@"`

// gitCredentials makes git use GH_TOKEN for HTTPS, through the environment
// only: nothing is written to disk.
var gitCredentials = []string{
	"GIT_CONFIG_COUNT=1",
	"GIT_CONFIG_KEY_0=credential.helper",
	`GIT_CONFIG_VALUE_0=!f() { echo username=x-access-token; echo "password=$GH_TOKEN"; }; f`,
}

// taskUID is the owner the node gives what it makes for tasks in the user's
// folders (Eriphe/), and what tasks write there (handback.go); the
// explorer and the services run as this user too. Tasks themselves run as
// root.
const (
	taskUID = 1000
	taskGID = 1000
)

// hardening is what every task container gets. It runs as root, whatever
// the image says, so that the agent installs what it needs (apt-get, in a
// container thrown away with the task), with no capability but three file
// ones: CHOWN and FOWNER, which dpkg needs (a package's files belong to its
// own users and groups), and DAC_OVERRIDE (the task's folders, a folder
// lent to it, files of earlier tasks of UID 1000: root reads and writes
// them as their owner did). No SETUID or SETGID (apt runs as root, without
// its _apt sandbox user, see worker/base/apt.conf), no network capability,
// no way to gain privileges (no-new-privileges), a process limit, and a
// tmpfs at /tmp (exec: builds and test binaries run from there). The root
// filesystem is writable: what is installed goes with the container. The
// mounts keep the rest out of reach: folders read-only but Eriphe/, the
// agent read-only.
var hardening = []string{
	"--user", "0:0",
	"--cap-drop", "ALL",
	"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
	"--security-opt", "no-new-privileges",
	"--pids-limit", "1024",
	"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g",
}

// resourceLimits are the node's memory and CPU limits for a task container.
func (n *node) resourceLimits() []string {
	var args []string
	if n.opt.taskMemory != "" {
		args = append(args, "--memory", n.opt.taskMemory)
	}
	if n.opt.taskCPUs != "" {
		args = append(args, "--cpus", n.opt.taskCPUs)
	}
	return args
}

// bindMount mounts a directory of the node's root into the task container,
// and refuses anything outside it: tasks see no other host path.
func (n *node) bindMount(p, target string) ([]string, error) {
	rel, err := filepath.Rel(n.opt.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("refusing to mount %s: outside the node root %s", p, n.opt.root)
	}
	if n.apple() {
		return appleMount(p, target, false)
	}
	return []string{"-v", n.hostPath(p) + ":" + target}, nil
}

func repoURL(repo string) string {
	if repo == "" || strings.Contains(repo, "://") || strings.HasPrefix(repo, "git@") {
		return repo
	}
	return "https://github.com/" + strings.TrimSuffix(repo, ".git") + ".git"
}

// startContainer runs the task in its own container and gives the agent its
// first turn: the prompt, or after a restart, a note to carry on. Identities
// are passed by name (-e NAME) so their values never appear on a command line.
func (n *node) startContainer(t *Task, first string) error {
	spec := t.Spec
	rt, err := runtimeFor(spec.Runtime)
	if err != nil {
		return err
	}
	// The agent, unpacked on this node (at once when it was prepared).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	agentDir, err := n.injection(ctx, t)
	cancel()
	if err != nil {
		return err
	}
	t.mu.Lock()
	stopping := t.stopping
	if !stopping {
		t.agentDir = agentDir
	}
	t.mu.Unlock()
	if stopping { // stopped while its agent was being prepared
		t.setState(stateStopped)
		return nil
	}
	n.agents.acquire(agentDir)
	started := false
	defer func() {
		if !started {
			n.agents.release(agentDir)
		}
	}()
	if n.apple() {
		// Its own network first: the agent's configuration names its gateway.
		if err := n.startTaskNetwork(t); err != nil {
			return err
		}
	}
	agentArgs, agentEnv := rt.command(n, t)
	args, cmdEnv, err := n.taskRunArgs(t, agentArgs, agentEnv)
	if err != nil {
		n.endTaskNetwork(t)
		return err
	}

	cmd := exec.Command(n.opt.docker, args...)
	cmd.Env = cmdEnv
	stdin, err := cmd.StdinPipe()
	if err != nil {
		n.endTaskNetwork(t)
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		n.endTaskNetwork(t)
		return err
	}
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		n.endTaskNetwork(t)
		return err
	}
	started = true
	sess := rt.attach(t, stdin)
	done := make(chan struct{})
	t.mu.Lock()
	t.run = sess
	t.onTurnEnd = func() { n.handBack(t) }
	t.mu.Unlock()
	go n.handBackWhileRunning(t, done)
	go func() {
		err := sess.run(first, stdout)
		if werr := cmd.Wait(); werr != nil {
			err = werr
		}
		close(done)
		n.agents.release(agentDir)
		n.endTaskNetwork(t)
		t.mu.Lock()
		stopping := t.stopping
		t.mu.Unlock()
		if err != nil && !stopping {
			t.emit("error", map[string]any{"message": err.Error(), "stderr": tail(stderr.String(), 2000)})
			t.setState(stateFailed)
			return
		}
		t.setState(stateStopped)
	}()
	return nil
}

// taskRunArgs is the command line of a task's container (`docker run`, or
// `container run`, apple.go) and the environment of that command: the
// task's own variables are passed by name (-e NAME), so their values never
// appear on a command line.
func (n *node) taskRunArgs(t *Task, agentArgs []string, agentEnv map[string]string) (args, cmdEnv []string, err error) {
	spec := t.Spec
	env := map[string]string{
		"ERIPHE_REPO": repoURL(spec.Repo), "ERIPHE_REF": spec.Ref, "ERIPHE_BRANCH": spec.Branch, "ERIPHE_DIR": spec.Dir,
		"GIT_AUTHOR_NAME": "Eriphe", "GIT_AUTHOR_EMAIL": "eriphe@eriphe.local",
		"GIT_COMMITTER_NAME": "Eriphe", "GIT_COMMITTER_EMAIL": "eriphe@eriphe.local",
	}
	if spec.TZ != "" {
		env["TZ"] = spec.TZ // the agent's clock is the user's, not the node's
	}
	for k, v := range n.egressEnv(t) { // their only way out (egress.go)
		env[k] = v
	}
	for k, v := range n.credentialEnv(t) { // where eriphe-cred asks (credential.go)
		env[k] = v
	}
	// The env reaches the container through the docker CLI's own (-e NAME),
	// and the CLI honours the proxy variables too: it must reach its daemon
	// directly, never through the relay, which refuses its private address.
	if h := dockerDaemonHost(os.Getenv("DOCKER_HOST")); h != "" && env["NO_PROXY"] != "" {
		env["NO_PROXY"] += "," + h
		env["no_proxy"] = env["NO_PROXY"]
	}
	for k, v := range agentEnv {
		env[k] = v
	}
	for k, v := range spec.Identity {
		env[k] = v
	}
	if _, ok := env["GH_TOKEN"]; ok {
		for _, kv := range gitCredentials {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
	}

	if n.apple() {
		if args, err = n.appleTaskArgs(t); err != nil {
			return nil, nil, err
		}
	} else {
		// An image's own health check is for its standalone use, not for tasks.
		args = []string{"run", "-i", "--rm", "--no-healthcheck", "--name", containerName(t), "--label", "eriphe.task=" + t.ID,
			"--add-host", "host.docker.internal:" + n.hostGateway(n.opt.taskNetwork)}
		args = append(args, hardening...)
		args = append(args, n.resourceLimits()...)
		// Their own network, never eriphe-core's nor the services' (network.go).
		args = append(args, "--network", dockerNetwork(n.opt.taskNetwork))
		if spec.SystemNetwork && n.opt.network != "" {
			// The brain calls eriphe-core and its MCP servers by name; workers never do.
			args = append(args, "--network", n.opt.network)
		}
		if n.opt.cacheVolume != "" {
			// Tools and package caches, shared by the node's tasks.
			args = append(args, "-v", n.opt.cacheVolume+":/cache")
		}
	}
	// The agent and the node's helpers, read-only (inject.go).
	t.mu.Lock()
	agentDir := t.agentDir
	t.mu.Unlock()
	if agentDir != "" {
		mount, err := n.injectMount(agentDir)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, mount...)
	}
	// The workspace and the agent's config live in the task's folder, so a
	// restarted container finds its work and resumes its session. A named
	// profile is shared between tasks instead (the brain's).
	work := filepath.Join(t.dir, "work")
	config := filepath.Join(t.dir, "agent")
	if spec.Profile != "" {
		config = filepath.Join(n.opt.root, "profiles", spec.Profile)
	}
	for _, m := range [][2]string{{work, "/work"}, {config, "/profile"}} {
		mount, err := n.bindMount(m[0], m[1])
		if err != nil {
			return nil, nil, err
		}
		// The node's own, 0700: no user of the machine reaches what a root
		// task leaves there. Root in the container writes to it anyway.
		if err := os.MkdirAll(m[0], 0o700); err != nil {
			return nil, nil, err
		}
		args = append(args, mount...)
	}
	// The folders of this machine lent to it, checked again now.
	if len(spec.Folders) > 0 {
		mounts, err := n.folderMounts(t)
		if err != nil {
			return nil, nil, err
		}
		if err := os.MkdirAll(filepath.Join(work, "folders"), 0o700); err != nil {
			return nil, nil, err
		}
		args = append(args, mounts...)
		names := make([]string, len(spec.Folders))
		for i, f := range spec.Folders {
			names[i] = f.Name
		}
		env["ERIPHE_FOLDERS"] = strings.Join(names, "\n")
	}
	cmdEnv = os.Environ()
	for k, v := range env {
		args = append(args, "-e", k)
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	args = append(args, "--entrypoint", "sh", spec.Image, "-c", entrypoint, "eriphe")
	args = append(args, agentArgs...)
	return args, cmdEnv, nil
}

// injectMount mounts an agent's directory of the node's root read-only at
// /opt/eriphe.
func (n *node) injectMount(dir string) ([]string, error) {
	rel, err := filepath.Rel(n.opt.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("refusing to mount %s: outside the node root %s", dir, n.opt.root)
	}
	if n.apple() {
		return appleMount(dir, injectDir, true)
	}
	return []string{"--mount", mountSpec("type=bind", "src="+n.hostPath(dir), "dst="+injectDir, "readonly")}, nil
}

// hostPath is a path under -root as the Docker daemon sees it.
func (n *node) hostPath(p string) string {
	rel, err := filepath.Rel(n.opt.root, p)
	if err != nil {
		return p
	}
	return filepath.Join(n.opt.hostRoot, rel)
}

func (n *node) stopContainer(t *Task) {
	t.mu.Lock()
	t.stopping = true
	run := t.run
	t.mu.Unlock()
	if run != nil {
		run.stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exec.CommandContext(ctx, n.opt.docker, "stop", "-t", "10", containerName(t)).Run()
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// dockerDaemonHost is the host of a tcp:// DOCKER_HOST, "" for a socket.
func dockerDaemonHost(dockerHost string) string {
	u, err := url.Parse(dockerHost)
	if err != nil || (u.Scheme != "tcp" && u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Hostname()
}
