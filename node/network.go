package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
)

// Task containers run on their own Docker network: with eriphe-node
// engine, one per task (engine_tasknet.go), else -task-network
// (eriphe-tasks by default): an internal bridge, with no route out, that
// holds the tasks and, on the NAS, eriphe-node. Services and eriphe-core are
// on other networks, which Docker keeps apart from it: a task cannot reach a
// service container (and bypass the grants, the policy and the tickets of
// eriphe-core) nor eriphe-core itself. It reaches only this node: its task
// listener (-listen), bound to the node's address on that network, and its
// egress relay (-egress-listen, egress.go), the one way to the Internet,
// which never dials the LAN.

// defaultBridge is Docker's own network, the one of a container without
// --network.
const defaultBridge = "bridge"

func dockerNetwork(name string) string {
	if name == "" {
		return defaultBridge
	}
	return name
}

// checkNetworks refuses a task network shared with services or eriphe-core.
func checkNetworks(o options) error {
	tasks := dockerNetwork(o.taskNetwork)
	if o.network != "" && dockerNetwork(o.network) == tasks {
		return fmt.Errorf("-task-network %s is also -network (eriphe-core and the services): give tasks a network of their own", tasks)
	}
	if o.serviceNetwork != "" && o.serviceNetwork == tasks {
		return fmt.Errorf("-task-network %s is also -service-network: give tasks a network of their own", tasks)
	}
	return nil
}

// listenAddr is where the task listener binds. An explicit host is kept; an
// empty one (":8790", the default) becomes this node's address on the task
// network: its interface there (the node is a container on it, the NAS) or
// the network's gateway (the node runs on the Docker host, WSL). Failing
// both (Docker Desktop, whose networks are in a VM), the loopback, which
// Docker Desktop forwards host.docker.internal to.
func (n *node) listenAddr(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("-listen %q: %w", listen, err)
	}
	if host != "" {
		return listen, nil
	}
	if n.engine().traits().ownNetworks {
		// Each task's network comes and goes with it (beginTask): every
		// address, and taskListener accepts only the tasks' networks.
		return net.JoinHostPort("0.0.0.0", port), nil
	}
	subnets, err := n.engine().taskSubnets()
	if err != nil {
		return "", fmt.Errorf("inspect the task network %s: %w", dockerNetwork(n.opt.taskNetwork), err)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	if ip, ok := pickListenIP(subnets, addrs); ok {
		return net.JoinHostPort(ip.String(), port), nil
	}
	log.Printf("no address of this machine is on the task network %s (%s): listening on the loopback",
		dockerNetwork(n.opt.taskNetwork), strings.Join(subnets, ", "))
	return net.JoinHostPort("127.0.0.1", port), nil
}

// pickListenIP is the first local address inside one of the subnets.
func pickListenIP(subnets []string, addrs []net.Addr) (net.IP, bool) {
	for _, s := range subnets {
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if ok && cidr.Contains(ipn.IP) {
				return ipn.IP, true
			}
		}
	}
	return nil, false
}

var errSharedBridge = errors.New("an http service with egress would share Docker's default bridge with the tasks: give the node -task-network, or -network for its services")

// callbackFor is how a task's container reaches this node. A task on two
// networks (the brain) could resolve the node's name to its address on the
// other one, where the listener is not bound: it gets the bound address.
func (n *node) callbackFor(t *Task) string {
	if n.engine().traits().ownNetworks {
		// The gateway of the task's own network is this machine.
		t.mu.Lock()
		gw := t.gateway
		t.mu.Unlock()
		_, port, _ := net.SplitHostPort(n.opt.listenAddr)
		if gw != "" && port != "" {
			return "http://" + net.JoinHostPort(gw, port)
		}
	}
	if t.Spec.SystemNetwork && n.opt.listenAddr != "" {
		if host, _, err := net.SplitHostPort(n.opt.listenAddr); err == nil {
			if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
				return "http://" + n.opt.listenAddr
			}
		}
	}
	return n.opt.callbackURL
}
