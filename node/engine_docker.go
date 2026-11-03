package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// dockerEngine runs containers with the docker CLI: on a Linux machine, the
// NAS (through the node's socket proxy, dockerproxy.go), WSL, or Docker
// Desktop.
type dockerEngine struct{ o *options }

func (e dockerEngine) traits() engineTraits { return engineTraits{} }

// The tasks share one network (ensureNetwork): nothing to make for one.
func (e dockerEngine) beginTask(string) (string, string, error) { return "", "", nil }
func (e dockerEngine) endTask(string)                           {}

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
func (e dockerEngine) resourceLimits() []string {
	var args []string
	if e.o.taskMemory != "" {
		args = append(args, "--memory", e.o.taskMemory)
	}
	if e.o.taskCPUs != "" {
		args = append(args, "--cpus", e.o.taskCPUs)
	}
	return args
}

// hostPath is a path under -root as the Docker daemon sees it.
func (e dockerEngine) hostPath(p string) string {
	rel, err := filepath.Rel(e.o.root, p)
	if err != nil {
		return p
	}
	return filepath.Join(e.o.hostRoot, rel)
}

// mountArgs mounts a host folder: one of the node's root where the daemon
// has it, writable with -v, read-only with --mount; any other as it is.
func (e dockerEngine) mountArgs(m mount) []string {
	src := m.src
	if m.inRoot {
		src = e.hostPath(m.src)
		if !m.readOnly {
			return []string{"-v", src + ":" + m.dst}
		}
	}
	spec := []string{"type=bind", "src=" + src, "dst=" + m.dst}
	if m.readOnly {
		spec = append(spec, "readonly")
	}
	return []string{"--mount", mountSpec(spec...)}
}

func (e dockerEngine) mountable(string) error { return nil }

// taskArgs is a task's `docker run`: the container, its hardening and
// limits, its networks and the shared cache, then its mounts, groups and
// environment (by name), its entrypoint and command.
func (e dockerEngine) taskArgs(spec taskRunSpec) []string {
	// An image's own health check is for its standalone use, not for tasks.
	args := []string{"run", "-i", "--rm", "--no-healthcheck", "--name", spec.name, "--label", "eriphe.task=" + spec.taskID,
		"--add-host", "host.docker.internal:" + e.hostGateway(e.o.taskNetwork)}
	args = append(args, hardening...)
	args = append(args, e.resourceLimits()...)
	// Their own network, never eriphe-core's nor the services' (network.go).
	args = append(args, "--network", dockerNetwork(e.o.taskNetwork))
	if spec.systemNetwork && e.o.network != "" {
		// The brain calls eriphe-core and its MCP servers by name; workers never do.
		args = append(args, "--network", e.o.network)
	}
	if e.o.cacheVolume != "" {
		// Tools and package caches, shared by the node's tasks.
		args = append(args, "-v", e.o.cacheVolume+":/cache")
	}
	for _, m := range spec.mounts {
		args = append(args, e.mountArgs(m)...)
	}
	for _, g := range spec.groups {
		args = append(args, "--group-add", strconv.Itoa(g))
	}
	args = append(args, envArgs(spec.env)...)
	args = append(args, "--entrypoint", spec.entrypoint, spec.image)
	return append(args, spec.cmd...)
}

func (e dockerEngine) runTask(spec taskRunSpec) (*engineRun, error) {
	return startCLI(e.o.docker, e.taskArgs(spec), spec.env, true, spec.stderr)
}

func (e dockerEngine) execSync(ctx context.Context, container string, spec execSpec) (execResult, error) {
	return cliExec(ctx, e.o.docker, container, spec)
}

func (e dockerEngine) stop(ctx context.Context, container string) {
	exec.CommandContext(ctx, e.o.docker, "stop", "-t", "10", container).Run()
}

// serviceArgs is a service's `docker run` and, for http, the address the
// node reaches the container on.
func (e dockerEngine) serviceArgs(spec serviceRunSpec) (args []string, target string, err error) {
	args = []string{"run", "--rm", "--name", spec.name, "--label", "eriphe.service=" + spec.service}
	if spec.stdio {
		args = append(args, "-i")
	}
	args = append(args, "--user", spec.user, "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "256", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=256m")
	if !spec.writable {
		args = append(args, "--read-only")
	}
	if e.o.serviceMemory != "" {
		args = append(args, "--memory", e.o.serviceMemory)
	}

	// The network: none without egress, unless an http service needs one to
	// be reached, then the node's internal network. The node reaches an http
	// container by name on a network it shares (the NAS), or else on a
	// loopback port it publishes (a node outside Docker: WSL, a Mac).
	network := e.o.network
	switch {
	case !spec.egress && spec.stdio:
		network = "none"
	case !spec.egress:
		if e.o.serviceNetwork == "" {
			return nil, "", errors.New("an http service without egress needs the node's -service-network (an internal Docker network the node is on)")
		}
		network = e.o.serviceNetwork
	}
	// Never the tasks' network: they would reach the service directly.
	if !spec.stdio && dockerNetwork(network) == dockerNetwork(e.o.taskNetwork) {
		return nil, "", errSharedBridge
	}
	if network != "" {
		args = append(args, "--network", network)
	} else {
		args = append(args, "--add-host", "host.docker.internal:"+e.hostGateway(""))
	}
	if !spec.stdio {
		if network != "" {
			target = net.JoinHostPort(spec.name, strconv.Itoa(spec.port))
		} else {
			port, err := freePort()
			if err != nil {
				return nil, "", err
			}
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, spec.port))
			target = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		}
	}
	for _, m := range spec.mounts {
		args = append(args, e.mountArgs(m)...)
	}
	args = append(args, envArgs(spec.env)...)
	args = append(args, spec.image)
	return append(args, spec.args...), target, nil
}

func (e dockerEngine) runService(spec serviceRunSpec) (*engineRun, string, error) {
	args, target, err := e.serviceArgs(spec)
	if err != nil {
		return nil, "", err
	}
	r, err := startCLI(e.o.docker, args, spec.env, spec.stdio, spec.stderr)
	return r, target, err
}

func (e dockerEngine) serviceAddress(context.Context, string, chan error) (string, error) {
	return "", errors.New("no address for the service")
}

func (e dockerEngine) removeStray(label string) {
	out, err := exec.Command(e.o.docker, "ps", "-aq", "--filter", "label="+label).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return
	}
	args := append([]string{"rm", "-f"}, strings.Fields(string(out))...)
	exec.Command(e.o.docker, args...).Run()
}

// helperArgs is a helper's `docker run`: no network, a read-only root, as
// root with a task's three file capabilities (or as its user), few
// processes.
func (e dockerEngine) helperArgs(spec helperRunSpec) []string {
	args := []string{"run", "--rm"}
	if spec.stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, "--network", "none", "--read-only")
	if spec.user == "" {
		args = append(args, "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER")
	} else {
		args = append(args, "--user", spec.user, "--cap-drop", "ALL")
	}
	args = append(args, "--security-opt", "no-new-privileges", "--pids-limit", "64", "--label", spec.label,
		"--entrypoint", spec.entrypoint)
	for _, m := range spec.mounts {
		args = append(args, e.mountArgs(m)...)
	}
	args = append(args, spec.image)
	return append(args, spec.args...)
}

func (e dockerEngine) runHelper(ctx context.Context, spec helperRunSpec) ([]byte, []byte, error) {
	return runCLI(ctx, e.o.docker, e.helperArgs(spec), spec.stdin)
}

// runCLI runs an engine's CLI to its end.
func runCLI(ctx context.Context, cli string, args []string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, cli, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	return out.Bytes(), errs.Bytes(), err
}

var containerID = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// selfImage is the image of the node's own container, as Docker lists it
// (the socket proxy allows listing containers).
func (e dockerEngine) selfImage(ctx context.Context) (string, error) {
	id := ""
	if raw, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if m := containerID.FindSubmatch(raw); m != nil {
			id = string(m[1])
		}
	}
	if id == "" {
		id, _ = os.Hostname()
	}
	out, err := exec.CommandContext(ctx, e.o.docker, "ps", "--no-trunc", "--filter", "id="+id, "--format", "{{.Image}}").Output()
	img := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil || img == "" {
		return "", fmt.Errorf("cannot find this node's own image (%v)", err)
	}
	return img, nil
}

// wakeHostArgs is the `docker run` of the short-lived container that sends
// the packets from the host's network: the node's image, nothing mounted,
// no capability, read-only, gone once done.
func wakeHostArgs(image string, w wakeInfo) []string {
	args := []string{"run", "--rm", "--pull", "never", "--network", "host", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "--read-only", "--label", "eriphe.wake=1",
		"--entrypoint", "/usr/local/bin/eriphe-node", image, "wake-send", "-mac", strings.Join(w.MACs, ",")}
	if w.Broadcast != "" {
		args = append(args, "-broadcast", w.Broadcast)
	}
	return args
}

func (e dockerEngine) wake(ctx context.Context, w wakeInfo) error {
	img, err := e.selfImage(ctx)
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, e.o.docker, wakeHostArgs(img, w)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (e dockerEngine) pull(ctx context.Context, image string) error {
	return pullCLI(ctx, e.o.docker, "pull", "-q", image)
}

// pullCLI pulls an image with an engine's CLI.
func pullCLI(ctx context.Context, cli string, args ...string) error {
	out, err := exec.CommandContext(ctx, cli, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, tail(strings.TrimSpace(string(out)), 300))
	}
	return nil
}

func (e dockerEngine) info(ctx context.Context) engineInfo {
	var i engineInfo
	if out, err := exec.CommandContext(ctx, e.o.docker, "version", "--format", "{{.Server.Version}}").Output(); err == nil {
		i.docker = string(trimNL(out))
		i.version = i.docker
	}
	i.images = localImages(ctx, e.o.docker)
	return i
}

// localImages lists the images the Docker daemon has, as repo:tag.
func localImages(ctx context.Context, docker string) []string {
	out, err := exec.CommandContext(ctx, docker, "images", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return []string{}
	}
	images := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Contains(l, "<none>") || len(images) == maxImages {
			continue
		}
		images = append(images, l)
	}
	return images
}

// networkConfig is the subnets and gateways of a Docker network.
func (e dockerEngine) networkConfig(name string) (subnets, gateways []string, err error) {
	out, err := exec.Command(e.o.docker, "network", "inspect", dockerNetwork(name),
		"--format", "{{range .IPAM.Config}}{{.Subnet}} {{.Gateway}};{{end}}").Output()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range strings.Split(strings.TrimSpace(string(out)), ";") {
		f := strings.Fields(c)
		if len(f) > 0 {
			subnets = append(subnets, f[0])
		}
		if len(f) > 1 && net.ParseIP(f[1]) != nil {
			gateways = append(gateways, f[1])
		}
	}
	return subnets, gateways, nil
}

func (e dockerEngine) taskSubnets() ([]string, error) {
	subnets, _, err := e.networkConfig(e.o.taskNetwork)
	return subnets, err
}

// ensureNetwork creates the task network if it does not exist: an internal
// bridge on which containers cannot reach one another (the node, outside
// Docker, is reached on its gateway). The NAS's compose file creates it
// instead, with eriphe-node on it: there the node is a container the tasks
// must reach. Docker cannot make an existing network internal: one that is
// not is reported, the tasks on it can still bypass the relay.
func (e dockerEngine) ensureNetwork() error {
	name := dockerNetwork(e.o.taskNetwork)
	if name == defaultBridge {
		log.Printf("warning: tasks on Docker's default bridge have direct egress, the LAN included: give -task-network")
		return nil
	}
	if out, err := exec.Command(e.o.docker, "network", "inspect", name, "--format", "{{.Internal}}").Output(); err == nil {
		if strings.TrimSpace(string(out)) != "true" {
			log.Printf("warning: the task network %s is not internal: tasks can bypass the egress relay and reach the LAN. "+
				"Recreate it with no task running: docker network rm %s (this node creates it again), or internal: true in compose", name, name)
		}
		return nil
	}
	out, err := exec.Command(e.o.docker, "network", "create", "--driver", "bridge", "--internal",
		"-o", "com.docker.network.bridge.enable_icc=false", "--label", "eriphe.network=tasks", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("the task network %s does not exist and cannot be created (%v: %s): create it, or attach this node to it",
			name, err, strings.TrimSpace(string(out)))
	}
	log.Printf("created the task network %s", name)
	return nil
}

// hostGateway is the address containers on a network reach the host on:
// its gateway, asked of Docker, since dockerd's own "host-gateway" can be
// lost when it reloads its configuration (seen under WSL).
func (e dockerEngine) hostGateway(network string) string {
	if _, gw, err := e.networkConfig(network); err == nil && len(gw) > 0 {
		return gw[0]
	}
	return "host-gateway"
}
