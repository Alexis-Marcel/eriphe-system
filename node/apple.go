package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The container engine: Apple's container tool (github.com/apple/container),
// on a Mac with Apple silicon and macOS 26, where eriphe-node runs on macOS
// itself. Every task is a micro-VM of its own, started with
// `container run`, with the same contract as a Docker task (container.go):
// the worker image, the injected agent, the task's folders, root with only
// three file capabilities, its resource limits, its stdin and stdout for
// the agent.
//
// Its network: each task gets one of its own, `container network create
// --internal` (vmnet's host-only mode): no route out, no DNS, nothing of the
// LAN, and no other task on it. The Mac is its gateway, where the node's
// task listener and egress relay answer. The bridge only exists while a
// container is on it, so the node listens on every address and accepts a
// connection only from a task's network (or the loopback): taskListener.
//
// What Docker does and this does not: --security-opt no-new-privileges (no
// such option: with every capability dropped from the bounding set but
// three, a setuid program gains no other), --pids-limit (an nproc rlimit instead), --add-host (the callback is
// the gateway's address), --group-add (the Mac's file sharing maps every
// file to the task's user), named volumes (the cache is a folder of the
// node's root).

const (
	engineDocker    = "docker"
	engineContainer = "container"
)

// appleEngine runs containers with Apple's container tool.
type appleEngine struct {
	o    *options
	nets *taskNets
}

func (e appleEngine) traits() engineTraits {
	return engineTraits{ownNetworks: true, explores: true, mapsOwners: true}
}

// appleHardening is the container engine's version of hardening: root
// with the same three file capabilities, in a micro-VM of its own.
var appleHardening = []string{
	"--user", "0:0",
	"--cap-drop", "ALL",
	"--cap-add", "CAP_CHOWN", "--cap-add", "CAP_DAC_OVERRIDE", "--cap-add", "CAP_FOWNER",
	"--ulimit", "nproc=1024",
	"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g",
}

// appleMount is a --mount of a host folder; container reads it as fields
// split on commas, without quoting.
func appleMount(src, dst string, readOnly bool) ([]string, error) {
	if err := (appleEngine{}).mountable(src + dst); err != nil {
		return nil, err
	}
	spec := "type=bind,source=" + src + ",target=" + dst
	if readOnly {
		spec += ",readonly"
	}
	return []string{"--mount", spec}, nil
}

func (e appleEngine) mountable(p string) error {
	if strings.ContainsAny(p, ",\n") {
		return fmt.Errorf("cannot mount %s: a comma in the path", p)
	}
	return nil
}

// mountArgs are the --mount of a container's folders.
func mountArgs(mounts []mount) ([]string, error) {
	var args []string
	for _, m := range mounts {
		a, err := appleMount(m.src, m.dst, m.readOnly)
		if err != nil {
			return nil, err
		}
		args = append(args, a...)
	}
	return args, nil
}

// appleResources are the task's memory and CPU limits. Without them a
// container gets the tool's defaults (1 GB, 4 CPUs): no limit given means the
// machine's CPUs.
func appleResources(memory, cpus string) []string {
	if cpus == "" {
		cpus = strconv.Itoa(runtime.NumCPU())
	}
	args := []string{"--cpus", cpus}
	if memory != "" {
		args = append(args, "--memory", memory)
	}
	return args
}

// taskArgs is a task's `container run`, with the same contract as a
// Docker task's (dockerEngine.taskArgs).
func (e appleEngine) taskArgs(spec taskRunSpec) ([]string, error) {
	if spec.network == "" {
		return nil, errors.New("the task has no network")
	}
	args := []string{"run", "-i", "--rm", "--progress", "none", "--name", spec.name, "--label", "eriphe.task=" + spec.taskID,
		"--network", spec.network}
	args = append(args, appleHardening...)
	args = append(args, appleResources(e.o.taskMemory, e.o.taskCPUs)...)
	if e.o.cacheVolume != "" {
		// A folder of the root, not a volume: a volume belongs to root, and
		// the task's user could not write to it.
		dir := filepath.Join(e.o.root, cacheFor("cache", spec.untrusted))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		mount, err := appleMount(dir, "/cache", false)
		if err != nil {
			return nil, err
		}
		args = append(args, mount...)
	}
	// No --group-add: the Mac's file sharing maps every file to the task's user.
	mounts, err := mountArgs(spec.mounts)
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	args = append(args, envArgs(spec.env)...)
	args = append(args, "--entrypoint", spec.entrypoint, spec.image)
	return append(args, spec.cmd...), nil
}

func (e appleEngine) runTask(spec taskRunSpec) (*engineRun, error) {
	args, err := e.taskArgs(spec)
	if err != nil {
		return nil, err
	}
	return startCLI(e.o.docker, args, spec.env, true, spec.stderr)
}

func (e appleEngine) execSync(ctx context.Context, container string, spec execSpec) (execResult, error) {
	return cliExec(ctx, e.o.docker, container, spec)
}

func (e appleEngine) stop(ctx context.Context, container string) {
	exec.CommandContext(ctx, e.o.docker, "stop", "-t", "10", container).Run()
}

// taskNetwork is the name of a task's own network.
func taskNetwork(taskID string) string { return "eriphe-task-" + taskID }

// beginTask gives the task its network, and the node's address on it.
func (e appleEngine) beginTask(taskID string) (string, string, error) {
	name := taskNetwork(taskID)
	gw, err := e.createNetwork(name, "eriphe.task="+taskID, true)
	if err != nil {
		return "", "", err
	}
	return name, gw, nil
}

// endTask deletes the task's network once its container is gone.
func (e appleEngine) endTask(network string) {
	if network != "" {
		e.deleteNetwork(network)
	}
}

func (e appleEngine) ensureNetwork() error { return nil } // one network per task (beginTask)

func (e appleEngine) taskSubnets() ([]string, error) { return nil, nil }

// appleNetwork is what the node needs of a container network.
type appleNetwork struct {
	ID     string `json:"id"`
	Config struct {
		Labels map[string]string `json:"labels"`
		Mode   string            `json:"mode"`
	} `json:"configuration"`
	Status struct {
		Gateway string `json:"ipv4Gateway"`
		Subnet  string `json:"ipv4Subnet"`
	} `json:"status"`
}

func (e appleEngine) appleNetworks(names ...string) ([]appleNetwork, error) {
	args := []string{"network", "ls", "--format", "json"}
	if len(names) > 0 {
		args = append([]string{"network", "inspect"}, names...)
	}
	out, err := exec.Command(e.o.docker, args...).Output()
	if err != nil {
		return nil, err
	}
	var list []appleNetwork
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("container network: %w", err)
	}
	return list, nil
}

// taskNets are the subnets of the tasks' networks, by network name: the
// only addresses, with the loopback, the task listener and the egress relay
// accept connections from.
type taskNets struct {
	mu   sync.Mutex
	nets map[string]taskNet
}

type taskNet struct {
	subnet  netip.Prefix
	gateway netip.Addr
}

func (s *taskNets) add(name string, subnet netip.Prefix, gateway netip.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nets == nil {
		s.nets = map[string]taskNet{}
	}
	s.nets[name] = taskNet{subnet.Masked(), gateway}
}

func (s *taskNets) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nets, name)
}

// allowed tells whether a connection from a may reach the listener.
func (s *taskNets) allowed(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLoopback() {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.nets {
		if n.subnet.Contains(a) {
			return true
		}
	}
	return false
}

// gateway tells whether host:port is a task network's gateway at port: the
// node itself, as a task names it through the relay.
func (s *taskNets) gateway(hostport, port string) bool {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil || p != port {
		return false
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.nets {
		if n.gateway == a {
			return true
		}
	}
	return false
}

// taskListener accepts only the connections taskNets allows; the others are
// closed at once: the LAN never reaches the node's task listener nor its
// relay, though they listen on every address.
type taskListener struct {
	net.Listener
	nets *taskNets
}

func (l taskListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if ap, err := netip.ParseAddrPort(c.RemoteAddr().String()); err == nil && l.nets.allowed(ap.Addr()) {
			return c, nil
		}
		c.Close()
	}
}

// createNetwork makes the internal network name (or takes it if it is there
// already, after a restart of the node) and returns its gateway. A task's
// network (forTask) may reach the node's listener and relay; a service's
// may not.
func (e appleEngine) createNetwork(name, label string, forTask bool) (string, error) {
	list, err := e.appleNetworks(name)
	if err != nil || len(list) == 0 {
		out, cerr := exec.Command(e.o.docker, "network", "create", "--internal", "--label", label, name).CombinedOutput()
		if cerr != nil {
			return "", fmt.Errorf("container network create %s: %v: %s", name, cerr, strings.TrimSpace(string(out)))
		}
		if list, err = e.appleNetworks(name); err != nil {
			return "", err
		}
	}
	if len(list) != 1 || list[0].Config.Mode != "hostOnly" {
		e.deleteNetwork(name)
		return "", fmt.Errorf("the network %s is not internal (host-only)", name)
	}
	p, err := netip.ParsePrefix(list[0].Status.Subnet)
	if err != nil {
		return "", fmt.Errorf("the network %s has no subnet: %v", name, err)
	}
	gw, err := netip.ParseAddr(list[0].Status.Gateway)
	if err != nil || !p.Contains(gw) {
		gw = p.Masked().Addr().Next()
	}
	if forTask {
		e.nets.add(name, p, gw)
	}
	return gw.String(), nil
}

func (e appleEngine) deleteNetwork(name string) {
	e.nets.remove(name)
	// The container may still be going away: a network in use is not deleted.
	for i := 0; i < 10; i++ {
		if exec.Command(e.o.docker, "network", "delete", name).Run() == nil {
			return
		}
		if l, err := e.appleNetworks(name); err == nil && len(l) == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("could not delete the network %s", name)
}

// appleContainer is what the node needs of a container in `container ls`.
type appleContainer struct {
	ID     string `json:"id"`
	Config struct {
		ID     string            `json:"id"`
		Labels map[string]string `json:"labels"`
	} `json:"configuration"`
	Status struct {
		State    string `json:"state"`
		Networks []struct {
			Address string `json:"ipv4Address"`
		} `json:"networks"`
	} `json:"status"`
}

func (c appleContainer) id() string {
	if c.ID != "" {
		return c.ID
	}
	return c.Config.ID
}

func (e appleEngine) appleContainers() ([]appleContainer, error) {
	out, err := exec.Command(e.o.docker, "ls", "-a", "--format", "json").Output()
	if err != nil {
		return nil, err
	}
	var list []appleContainer
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("container ls: %w", err)
	}
	return list, nil
}

// removeStray removes the containers carrying a label, and the
// networks the node made for them.
func (e appleEngine) removeStray(label string) {
	if list, err := e.appleContainers(); err == nil {
		var ids []string
		for _, c := range list {
			if _, ok := c.Config.Labels[label]; ok {
				ids = append(ids, c.id())
			}
		}
		if len(ids) > 0 {
			exec.Command(e.o.docker, append([]string{"delete", "--force"}, ids...)...).Run()
		}
	}
	if nets, err := e.appleNetworks(); err == nil {
		for _, nw := range nets {
			if _, ok := nw.Config.Labels[label]; ok {
				e.deleteNetwork(nw.ID)
			}
		}
	}
}

// serviceAddress is a running container's address, once it has one.
func (e appleEngine) serviceAddress(ctx context.Context, name string, exited chan error) (string, error) {
	for {
		if list, err := e.appleContainers(); err == nil {
			for _, c := range list {
				if c.id() == name && c.Status.State == "running" && len(c.Status.Networks) > 0 && c.Status.Networks[0].Address != "" {
					if p, err := netip.ParsePrefix(c.Status.Networks[0].Address); err == nil {
						return p.Addr().String(), nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err := <-exited:
			exited <- err
			return "", errors.New("the container exited before it had an address")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// appleVersion is the container tool's version, "" if it does not answer.
func appleVersion(ctx context.Context, cli string) string {
	out, err := exec.CommandContext(ctx, cli, "system", "version", "--format", "json").Output()
	if err != nil {
		return ""
	}
	var list []struct {
		App     string `json:"appName"`
		Version string `json:"version"`
	}
	json.Unmarshal(out, &list)
	server := false
	v := ""
	for _, c := range list {
		switch c.App {
		case "container":
			v = c.Version
		case "container-apiserver":
			server = true
		}
	}
	if !server { // its services are not running
		return ""
	}
	return v
}

// appleImage is what the node reads of an image.
type appleImage struct {
	Config struct {
		Name string `json:"name"`
	} `json:"configuration"`
}

// appleImages lists the images the container tool has, as repo:tag.
func appleImages(ctx context.Context, cli string) []string {
	images := []string{}
	out, err := exec.CommandContext(ctx, cli, "image", "ls", "--format", "json").Output()
	if err != nil {
		return images
	}
	var list []appleImage
	json.Unmarshal(out, &list)
	for _, im := range list {
		if im.Config.Name != "" && len(images) < maxImages {
			images = append(images, strings.TrimPrefix(im.Config.Name, "docker.io/library/"))
		}
	}
	return images
}

func (e appleEngine) info(ctx context.Context) engineInfo {
	return engineInfo{version: appleVersion(ctx, e.o.docker), images: appleImages(ctx, e.o.docker)}
}

func (e appleEngine) pull(ctx context.Context, image string) error {
	return pullCLI(ctx, e.o.docker, "image", "pull", "--progress", "none", image)
}

// wake: the node runs on the Mac itself, its own network is the host's.
func (e appleEngine) wake(context.Context, wakeInfo) error { return errors.ErrUnsupported }

// helperArgs is a helper's `container run`: no network, a read-only root,
// as root with a task's three file capabilities (or as its user), little
// memory.
func (e appleEngine) helperArgs(spec helperRunSpec) ([]string, error) {
	args := []string{"run", "--rm", "--progress", "none"}
	if spec.stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, "--network", "none", "--read-only")
	if spec.user == "" {
		args = append(args, "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "CAP_CHOWN", "--cap-add", "CAP_DAC_OVERRIDE", "--cap-add", "CAP_FOWNER")
	} else {
		args = append(args, "--user", spec.user, "--cap-drop", "ALL")
	}
	args = append(args, "--memory", "256m", "--cpus", "1", "--label", spec.label)
	mounts, err := mountArgs(spec.mounts)
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	args = append(args, "--entrypoint", spec.entrypoint, spec.image)
	return append(args, spec.args...), nil
}

func (e appleEngine) runHelper(ctx context.Context, spec helperRunSpec) ([]byte, []byte, error) {
	args, err := e.helperArgs(spec)
	if err != nil {
		return nil, nil, err
	}
	return runCLI(ctx, e.o.docker, args, spec.stdin)
}
