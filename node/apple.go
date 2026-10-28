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
// the worker image, the task's folders, its user, a read-only root, no
// capability, its resource limits, its stdin and stdout for the agent.
//
// Its network: each task gets one of its own, `container network create
// --internal` (vmnet's host-only mode): no route out, no DNS, nothing of the
// LAN, and no other task on it. The Mac is its gateway, where the node's
// task listener and egress relay answer. The bridge only exists while a
// container is on it, so the node listens on every address and accepts a
// connection only from a task's network (or the loopback): taskListener.
//
// What Docker does and this does not: --security-opt no-new-privileges (no
// such option: with every capability dropped, even a setuid program gains
// none), --pids-limit (an nproc rlimit instead), --add-host (the callback is
// the gateway's address), --group-add (the Mac's file sharing maps every
// file to the task's user), named volumes (the cache is a folder of the
// node's root).

const (
	engineDocker    = "docker"
	engineContainer = "container"
)

// apple tells whether this node runs its tasks with Apple's container.
func (n *node) apple() bool { return n.opt.engine == engineContainer }

// appleHardening is the container engine's version of hardening.
var appleHardening = []string{
	"--user", fmt.Sprintf("%d:%d", taskUID, taskGID),
	"--cap-drop", "ALL",
	"--ulimit", "nproc=1024",
	"--read-only",
	"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g",
}

// appleMount is a --mount of a host folder; container reads it as fields
// split on commas, without quoting.
func appleMount(src, dst string, readOnly bool) ([]string, error) {
	if strings.ContainsAny(src+dst, ",\n") {
		return nil, fmt.Errorf("cannot mount %s: a comma in the path", src)
	}
	spec := "type=bind,source=" + src + ",target=" + dst
	if readOnly {
		spec += ",readonly"
	}
	return []string{"--mount", spec}, nil
}

// appleResources are the task's memory and CPU limits. Without them a
// container gets the tool's defaults (1 GB, 4 CPUs): no limit given means the
// machine's CPUs.
func (n *node) appleResources(memory, cpus string) []string {
	if cpus == "" {
		cpus = strconv.Itoa(runtime.NumCPU())
	}
	args := []string{"--cpus", cpus}
	if memory != "" {
		args = append(args, "--memory", memory)
	}
	return args
}

// appleTaskArgs is the start of a task's `container run`: what
// taskRunArgs adds after it (mounts, environment, image, command) is the
// same for both engines.
func (n *node) appleTaskArgs(t *Task) ([]string, error) {
	t.mu.Lock()
	network := t.network
	t.mu.Unlock()
	if network == "" {
		return nil, errors.New("the task has no network")
	}
	args := []string{"run", "-i", "--rm", "--progress", "none", "--name", containerName(t), "--label", "eriphe.task=" + t.ID,
		"--network", network}
	args = append(args, appleHardening...)
	args = append(args, n.appleResources(n.opt.taskMemory, n.opt.taskCPUs)...)
	if n.opt.cacheVolume != "" {
		// A folder of the root, not a volume: a volume belongs to root, and
		// the task's user could not write to it.
		dir := filepath.Join(n.opt.root, "cache")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		mount, err := appleMount(dir, "/cache", false)
		if err != nil {
			return nil, err
		}
		args = append(args, mount...)
	}
	return args, nil
}

// taskNetwork is the name of a task's own network.
func taskNetwork(t *Task) string { return "eriphe-task-" + t.ID }

// startTaskNetwork gives the task its network, and the node's address on it.
func (n *node) startTaskNetwork(t *Task) error {
	name := taskNetwork(t)
	gw, err := n.createNetwork(name, "eriphe.task="+t.ID, true)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.network, t.gateway = name, gw
	t.mu.Unlock()
	return nil
}

// endTaskNetwork deletes the task's network once its container is gone.
func (n *node) endTaskNetwork(t *Task) {
	if !n.apple() {
		return
	}
	t.mu.Lock()
	name := t.network
	t.network = ""
	t.mu.Unlock()
	if name != "" {
		n.deleteNetwork(name)
	}
}

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

func (n *node) appleNetworks(names ...string) ([]appleNetwork, error) {
	args := []string{"network", "ls", "--format", "json"}
	if len(names) > 0 {
		args = append([]string{"network", "inspect"}, names...)
	}
	out, err := exec.Command(n.opt.docker, args...).Output()
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
func (n *node) createNetwork(name, label string, forTask bool) (string, error) {
	list, err := n.appleNetworks(name)
	if err != nil || len(list) == 0 {
		out, cerr := exec.Command(n.opt.docker, "network", "create", "--internal", "--label", label, name).CombinedOutput()
		if cerr != nil {
			return "", fmt.Errorf("container network create %s: %v: %s", name, cerr, strings.TrimSpace(string(out)))
		}
		if list, err = n.appleNetworks(name); err != nil {
			return "", err
		}
	}
	if len(list) != 1 || list[0].Config.Mode != "hostOnly" {
		n.deleteNetwork(name)
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
		n.nets.add(name, p, gw)
	}
	return gw.String(), nil
}

func (n *node) deleteNetwork(name string) {
	n.nets.remove(name)
	// The container may still be going away: a network in use is not deleted.
	for i := 0; i < 10; i++ {
		if exec.Command(n.opt.docker, "network", "delete", name).Run() == nil {
			return
		}
		if l, err := n.appleNetworks(name); err == nil && len(l) == 0 {
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

func (n *node) appleContainers() ([]appleContainer, error) {
	out, err := exec.Command(n.opt.docker, "ls", "-a", "--format", "json").Output()
	if err != nil {
		return nil, err
	}
	var list []appleContainer
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("container ls: %w", err)
	}
	return list, nil
}

// appleRemoveLabelled removes the containers carrying a label, and the
// networks the node made for them.
func (n *node) appleRemoveLabelled(label string) {
	if list, err := n.appleContainers(); err == nil {
		var ids []string
		for _, c := range list {
			if _, ok := c.Config.Labels[label]; ok {
				ids = append(ids, c.id())
			}
		}
		if len(ids) > 0 {
			exec.Command(n.opt.docker, append([]string{"delete", "--force"}, ids...)...).Run()
		}
	}
	if nets, err := n.appleNetworks(); err == nil {
		for _, nw := range nets {
			if _, ok := nw.Config.Labels[label]; ok {
				n.deleteNetwork(nw.ID)
			}
		}
	}
}

// appleAddress is a running container's address, once it has one.
func (n *node) appleAddress(ctx context.Context, name string, exited chan error) (string, error) {
	for {
		if list, err := n.appleContainers(); err == nil {
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
	Variants []struct {
		Config struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"config"`
		} `json:"config"`
	} `json:"variants"`
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

// appleImageRuntimes reads an image's eriphe.runtimes label.
func appleImageRuntimes(ctx context.Context, cli, image string) []string {
	out, err := exec.CommandContext(ctx, cli, "image", "inspect", image).Output()
	var list []appleImage
	if err == nil {
		json.Unmarshal(out, &list)
	}
	var runtimes []string
	for _, im := range list {
		for _, v := range im.Variants {
			if l := v.Config.Config.Labels["eriphe.runtimes"]; l != "" {
				for _, r := range strings.Split(l, ",") {
					if r = strings.TrimSpace(r); r != "" {
						runtimes = append(runtimes, r)
					}
				}
				return runtimes
			}
		}
	}
	return []string{"claude-code"}
}
