package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func containerName(t *Task) string { return "eriphe-task-" + t.ID }

// The container puts the injected agent first in PATH, makes the cache's
// apt directories, writes the bundle of a task with accesses (egress_access.go), sets up ssh and its agent for SSH accesses (egress_ssh.go), clones the repository if asked (once: /work is kept across
// restarts; git is installed first if the image lacks it), makes what it
// writes group-writable when it has folders (the user's group shares
// them), asks the node for the toolchains the repository names and puts
// them first in PATH (toolchain.go), then becomes the agent its runtime names. Nothing it prints
// before goes to stdout, the agent's channel.
const entrypoint = `set -e
case ":$PATH:" in *":` + injectDir + `/bin:"*) ;; *) export PATH="` + injectDir + `/bin:$PATH" ;; esac
if [ ! -w "${HOME:-/}" ]; then export HOME=/tmp/home; mkdir -p "$HOME"; fi
if [ -w /cache ]; then mkdir -p /cache/apt/archives/partial /cache/apt/lists/partial 2>/dev/null || true; fi
cd /work
if [ -n "$ERIPHE_FOLDERS" ]; then umask 002; fi
` + accessBundleScript + `
` + sshAccessScript + `
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
if [ -n "$ERIPHE_TOOLCHAIN_URL" ] && command -v curl >/dev/null 2>&1; then
  if tc=$(curl -fsS -X POST --oauth2-bearer "$ERIPHE_TASK_SECRET" "$ERIPHE_TOOLCHAIN_URL"); then
    while IFS= read -r kv; do case "$kv" in [A-Za-z_]*=*) export "$kv" ;; esac; done <<EOF
$tc
EOF
  else
    echo "eriphe: the node could not prepare the repository's toolchains" >&2
  fi
fi
` + toolchainPathScript + `
exec "$@"`

// toolchainPathScript puts the task's toolchains first in PATH, in its
// entrypoint and in its checks alike (toolchain.go, turnend.go).
const toolchainPathScript = `if [ -n "$ERIPHE_TOOLCHAIN_PATH" ]; then export PATH="$ERIPHE_TOOLCHAIN_PATH:$PATH"; fi`

// gitCredentials makes git use GH_TOKEN for HTTPS, through the environment
// only: nothing is written to disk. For a task lent a GitHub repository it is
// a placeholder, which eriphe-core replaces past the relay.
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

// bindMount mounts a directory of the node's root into the task container,
// and refuses anything outside it: tasks see no other host path.
func (n *node) bindMount(p, target string) (mount, error) {
	if !n.inRoot(p) {
		return mount{}, fmt.Errorf("refusing to mount %s: outside the node root %s", p, n.opt.root)
	}
	return mount{src: p, dst: target, inRoot: true}, nil
}

// inRoot tells whether p is in the node's root.
func (n *node) inRoot(p string) bool {
	rel, err := filepath.Rel(n.opt.root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func repoURL(repo string) string {
	if name, ok := strings.CutPrefix(repo, "git@github.com:"); ok {
		return "https://github.com/" + name // the relay carries no git over SSH
	}
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
			removeRunFiles(t)
		}
	}()
	// Its own network first, with an engine that makes one: the agent's
	// configuration names its gateway.
	if err := n.startTaskNetwork(t); err != nil {
		return err
	}
	agentArgs, agentEnv := rt.command(n, t)
	var files map[string][]byte
	if fr, ok := rt.(fileRuntime); ok {
		files = fr.files(n, t)
	}
	if files, err = withAccessCA(t, files); err != nil { // egress_access.go
		n.endTaskNetwork(t)
		return err
	}
	files = withSSHFiles(t, files) // egress_ssh.go
	if err := writeRunFiles(t, files); err != nil {
		n.endTaskNetwork(t)
		return err
	}
	n.mountSkills(t) // skills_user.go
	run, err := n.taskRun(t, agentArgs, agentEnv)
	if err != nil {
		n.endTaskNetwork(t)
		return err
	}
	stderr := &tailBuffer{}
	run.stderr = stderr
	ctr, err := n.runTaskWhenFree(t, run)
	if errors.Is(err, errStoppedWaiting) {
		n.endTaskNetwork(t)
		t.setState(stateStopped)
		return nil
	}
	if err != nil {
		n.endTaskNetwork(t)
		return err
	}
	started = true
	sess := rt.attach(t, ctr.stdin)
	done := make(chan struct{})
	t.mu.Lock()
	t.run = sess
	t.onTurnEnd = func() { n.handBack(t) }
	t.onIdle = func() { n.afterTurn(t) } // turnend.go
	t.mu.Unlock()
	go n.handBackWhileRunning(t, done)
	go func() {
		err := sess.run(first, ctr.stdout)
		if werr := ctr.Wait(); werr != nil {
			err = werr
		}
		close(done)
		n.toolchains.release(t.ID)
		removeRunFiles(t)
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

// busyRetry and busyWait: how often, and how long, a task whose folder
// another task may write waits for it (errEngineBusy).
var busyRetry, busyWait = 15 * time.Second, 12 * time.Hour

var errStoppedWaiting = errors.New("stopped while it waited for its folder")

// runTaskWhenFree runs the task's container, once the engine can make it:
// while one of its folders is under a place another task can write, it
// waits, says so once, and stops waiting if the task is stopped.
func (n *node) runTaskWhenFree(t *Task, run taskRunSpec) (*engineRun, error) {
	ctr, err := n.engine().runTask(run)
	for waited := time.Duration(0); errors.Is(err, errEngineBusy) && waited < busyWait; waited += busyRetry {
		if waited == 0 {
			t.emit("retry", map[string]any{"reason": "folder_busy", "message": err.Error()})
		}
		time.Sleep(busyRetry)
		t.mu.Lock()
		stopping := t.stopping
		t.mu.Unlock()
		if stopping {
			return nil, errStoppedWaiting
		}
		ctr, err = n.engine().runTask(run)
	}
	return ctr, err
}

// startTaskNetwork gives the task its own network, and the node's address
// on it, with an engine that makes one (beginTask).
func (n *node) startTaskNetwork(t *Task) error {
	name, gw, err := n.engine().beginTask(t.ID)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.network, t.gateway = name, gw
	t.mu.Unlock()
	return nil
}

// endTaskNetwork removes the task's network once its container is gone.
func (n *node) endTaskNetwork(t *Task) {
	t.mu.Lock()
	name := t.network
	t.network = ""
	t.mu.Unlock()
	if name != "" {
		n.engine().endTask(name)
	}
}

// taskRun is a task's container: its image, its mounts, its environment,
// and the entrypoint that prepares it before the agent runs.
func (n *node) taskRun(t *Task, agentArgs []string, agentEnv map[string]string) (taskRunSpec, error) {
	env := n.taskEnv(t, agentEnv)
	mounts, groups, err := n.taskMounts(t)
	if err != nil {
		return taskRunSpec{}, err
	}
	if len(t.Spec.Folders) > 0 {
		env["ERIPHE_FOLDERS"] = folderNames(t.Spec.Folders)
	}
	t.mu.Lock()
	network := t.network
	t.mu.Unlock()
	return taskRunSpec{
		name: containerName(t), taskID: t.ID, image: t.Spec.Image,
		network: network, systemNetwork: t.Spec.SystemNetwork, untrusted: t.Spec.Untrusted,
		mounts: mounts, groups: groups, env: env,
		entrypoint: "sh", cmd: append([]string{"-c", entrypoint, "eriphe"}, agentArgs...),
	}, nil
}

// taskEnv is the environment of a task's container: what the entrypoint
// reads, the node's own variables, then the agent's, then the identity
// eriphe-core lends it, each over the ones before.
func (n *node) taskEnv(t *Task, agentEnv map[string]string) map[string]string {
	spec := t.Spec
	env := accessEnv(t) // first: nothing of the node's gives way to a placeholder (egress_access.go)
	if env == nil {
		env = map[string]string{}
	}
	for k, v := range map[string]string{
		"ERIPHE_REPO": repoURL(spec.Repo), "ERIPHE_REF": spec.Ref, "ERIPHE_BRANCH": spec.Branch, "ERIPHE_DIR": spec.Dir,
		"GIT_AUTHOR_NAME": "Eriphe", "GIT_AUTHOR_EMAIL": "eriphe@eriphe.local",
		"GIT_COMMITTER_NAME": "Eriphe", "GIT_COMMITTER_EMAIL": "eriphe@eriphe.local",
	} {
		env[k] = v
	}
	if spec.TZ != "" {
		env["TZ"] = spec.TZ // the agent's clock is the user's, not the node's
	}
	for k, v := range n.egressEnv(t) { // their only way out (egress.go)
		env[k] = v
	}
	env["ERIPHE_TASK_SECRET"] = t.secret // for what it asks this node: ssh, toolchains
	for k, v := range n.sshEnv(t) {      // egress_ssh.go
		env[k] = v
	}
	if n.toolchainsFor(t) {
		env["ERIPHE_TOOLCHAIN_URL"] = n.callbackFor(t) + toolchainPath // toolchain.go
	}
	// The env reaches the container through the docker CLI's own (-e NAME),
	// and the CLI honours the proxy variables too: it must reach its daemon
	// directly, never through the relay, which refuses its private address.
	if h := dockerDaemonHost(os.Getenv("DOCKER_HOST")); h != "" && env["NO_PROXY"] != "" && n.opt.engineURL == "" {
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
	return env
}

// taskMounts is what a task's container mounts: its agent and run files
// read-only, its workspace and agent config, and the folders lent to it.
func (n *node) taskMounts(t *Task) (mounts []mount, groups []int, err error) {
	if mounts, err = n.injectedMounts(t); err != nil {
		return nil, nil, err
	}
	work, err := n.workMounts(t)
	if err != nil {
		return nil, nil, err
	}
	mounts = append(mounts, work...)
	// The node's toolchains, read-only (toolchain.go).
	tc, err := n.toolchainMounts(t)
	if err != nil {
		return nil, nil, err
	}
	mounts = append(mounts, tc...)
	// The folders of this machine lent to it, checked again now.
	if len(t.Spec.Folders) > 0 {
		folders, gids, err := n.folderMounts(t)
		if err != nil {
			return nil, nil, err
		}
		if err := os.MkdirAll(filepath.Join(t.dir, "work", "folders"), 0o700); err != nil {
			return nil, nil, err
		}
		mounts, groups = append(mounts, folders...), gids
	}
	return mounts, groups, nil
}

// injectedMounts mounts, read-only, the agent and the node's helpers
// (inject.go), and the files the runtime hands its agent (runfiles.go).
func (n *node) injectedMounts(t *Task) ([]mount, error) {
	var args []mount
	t.mu.Lock()
	agentDir := t.agentDir
	t.mu.Unlock()
	if agentDir != "" {
		mount, err := n.readOnlyMount(agentDir, injectDir)
		if err != nil {
			return nil, err
		}
		args = append(args, mount)
	}
	if t.dir != "" {
		if _, err := os.Stat(runDir(t)); err == nil {
			mount, err := n.readOnlyMount(runDir(t), runMount)
			if err != nil {
				return nil, err
			}
			args = append(args, mount)
			for sub, target := range runSubMounts {
				dir := filepath.Join(runDir(t), sub)
				if _, err := os.Stat(dir); err != nil {
					continue
				}
				mount, err := n.readOnlyMount(dir, target)
				if err != nil {
					return nil, err
				}
				args = append(args, mount)
			}
		}
	}
	return args, nil
}

// workMounts mounts the workspace and the agent's config, which live in
// the task's folder, so a restarted container finds its work and resumes
// its session. A named profile is shared between tasks instead (the
// brain's).
func (n *node) workMounts(t *Task) ([]mount, error) {
	var args []mount
	work := filepath.Join(t.dir, "work")
	config := filepath.Join(t.dir, "agent")
	if t.Spec.Profile != "" {
		config = filepath.Join(n.opt.root, "profiles", t.Spec.Profile)
	}
	for _, m := range [][2]string{{work, "/work"}, {config, "/profile"}} {
		mount, err := n.bindMount(m[0], m[1])
		if err != nil {
			return nil, err
		}
		// The node's own, 0700: no user of the machine reaches what a root
		// task leaves there. Root in the container writes to it anyway.
		if err := os.MkdirAll(m[0], 0o700); err != nil {
			return nil, err
		}
		args = append(args, mount)
	}
	return args, nil
}

// folderNames is ERIPHE_FOLDERS, the names of the folders lent to a task,
// one per line.
func folderNames(folders []TaskFolder) string {
	names := make([]string, len(folders))
	for i, f := range folders {
		names[i] = f.Name
	}
	return strings.Join(names, "\n")
}

// readOnlyMount mounts a directory of the node's root read-only at target:
// an agent's at /opt/eriphe, the task's run files at /run/eriphe.
func (n *node) readOnlyMount(dir, target string) (mount, error) {
	if !n.inRoot(dir) {
		return mount{}, fmt.Errorf("refusing to mount %s: outside the node root %s", dir, n.opt.root)
	}
	return mount{src: dir, dst: target, readOnly: true, inRoot: true}, nil
}

// stopContainer stops a task's agent and its container. A task with a
// repository first pushes or keeps the commits no remote has (keepwork.go),
// once: the stop may be asked again meanwhile. Stopping is set before, so
// that a credential the user would have to approve is refused, not waited
// for.
func (n *node) stopContainer(t *Task) {
	t.mu.Lock()
	again := t.stopping
	t.stopping = true
	run := t.run
	t.mu.Unlock()
	keep := false
	if !again && run != nil && t.Spec.Repo != "" && !finished(t.state()) {
		keep = n.saveWork(t)
	}
	if run != nil {
		run.stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n.engine().stop(ctx, containerName(t))
	if keep {
		n.keepRepo(t)
	}
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
