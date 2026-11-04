package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Each task on a network of its own, as with Apple's container
// (apple.go): eriphe-node engine makes it before the task's container
// (POST /networks/task), made like the task network (internal, its driver
// options: on the NAS, a gateway with no address on the host), with a
// small subnet of -task-pool, and joins the node to it. A task reaches its
// node, nothing else: no other task, though the tasks share a machine.
// Once the task is over, the node leaves and the network goes
// (POST /networks/task/end); remove-stray removes those of tasks gone.
//
// Docker's own pools of subnets are few (some thirty networks), and a
// machine running other stacks has used most: the engine picks each task's
// subnet itself, the first of -task-pool that no network uses.

// taskNetPrefix is a task's own network: taskNetPrefix + the task's id.
const taskNetPrefix = "eriphe-task-"

// taskNetBits is the size of a task's subnet: the gateway, the node, the
// task, and room for the brain's second address.
const taskNetBits = 28

type (
	wireTaskNet struct {
		Task string `json:"task"`
	}
	wireTaskNetResult struct {
		Network string `json:"network"`
		Subnet  string `json:"subnet"`
		Node    string `json:"node"` // the node's address on it
	}
)

// apiNetworkFull is a network as the daemon lists it, with what a task's
// network is made of and who is on it.
type apiNetworkFull struct {
	Name     string            `json:"Name"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	Options  map[string]string `json:"Options"`
	IPAM     struct {
		Config []struct {
			Subnet string `json:"Subnet"`
		} `json:"Config"`
	} `json:"IPAM"`
	Containers map[string]struct {
		Name string `json:"Name"`
		IPv4 string `json:"IPv4Address"`
	} `json:"Containers"`
}

func (s *engineServer) handleTaskNet(w http.ResponseWriter, r *http.Request) {
	var in wireTaskNet
	if err := decode(w, r, &in); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	if !engineID.MatchString(in.Task) {
		s.refuse(w, r, http.StatusForbidden, fmt.Errorf("bad task %q", in.Task))
		return
	}
	out, err := s.beginTaskNet(r.Context(), in.Task)
	if err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *engineServer) handleTaskNetEnd(w http.ResponseWriter, r *http.Request) {
	var in wireTaskNet
	if err := decode(w, r, &in); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	if !engineID.MatchString(in.Task) {
		s.refuse(w, r, http.StatusForbidden, fmt.Errorf("bad task %q", in.Task))
		return
	}
	if err := s.endTaskNet(r.Context(), taskNetPrefix+in.Task); err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// beginTaskNet makes the task's network, one left by an earlier run gone
// first, and joins the node to it.
func (s *engineServer) beginTaskNet(ctx context.Context, task string) (wireTaskNetResult, error) {
	if s.nodeName == "" || !s.taskPool.IsValid() {
		return wireTaskNetResult{}, errors.New("this engine makes no task network (no -node-container or -task-pool)")
	}
	name := taskNetPrefix + task
	s.networking.Lock()
	defer s.networking.Unlock()
	if err := s.endTaskNet(ctx, name); err != nil {
		return wireTaskNetResult{}, err
	}
	var all []apiNetworkFull
	if err := s.d.call(ctx, http.MethodGet, "/networks", nil, nil, &all); err != nil {
		return wireTaskNetResult{}, fmt.Errorf("list the networks: %v", err)
	}
	subnet, err := freeSubnet(s.taskPool, all)
	if err != nil {
		return wireTaskNetResult{}, err
	}
	// Made like the task network: its driver options (an isolated
	// gateway), but its bridge's name, which is its own.
	opts := map[string]string{}
	for _, n := range all {
		if n.Name == dockerNetwork(s.taskNetwork) {
			for k, v := range n.Options {
				if k != "com.docker.network.bridge.name" {
					opts[k] = v
				}
			}
		}
	}
	err = s.d.call(ctx, http.MethodPost, "/networks/create", nil, map[string]any{
		"Name": name, "Driver": "bridge", "Internal": true, "Options": opts,
		"IPAM":   map[string]any{"Config": []map[string]string{{"Subnet": subnet.String()}}},
		"Labels": map[string]string{"eriphe.network": "task", "eriphe.task": task},
	}, nil)
	if err != nil {
		return wireTaskNetResult{}, fmt.Errorf("make the network %s: %v", name, err)
	}
	if err := s.d.connect(ctx, name, s.nodeName); err != nil {
		s.d.call(context.Background(), http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, nil)
		return wireTaskNetResult{}, fmt.Errorf("join %s to %s: %v", s.nodeName, name, err)
	}
	var n apiNetworkFull
	if err := s.d.call(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &n); err != nil {
		return wireTaskNetResult{}, err
	}
	for _, c := range n.Containers {
		if c.Name == s.nodeName {
			if p, err := netip.ParsePrefix(c.IPv4); err == nil {
				return wireTaskNetResult{Network: name, Subnet: subnet.String(), Node: p.Addr().String()}, nil
			}
		}
	}
	s.endTaskNet(context.Background(), name)
	return wireTaskNetResult{}, fmt.Errorf("%s has no address on %s", s.nodeName, name)
}

// endTaskNet removes a task's network, if it is one: what is still on it
// leaves first (the node; a task's container is gone by then).
func (s *engineServer) endTaskNet(ctx context.Context, name string) error {
	var n apiNetworkFull
	err := s.d.call(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &n)
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.Labels["eriphe.network"] != "task" || !strings.HasPrefix(name, taskNetPrefix) {
		return fmt.Errorf("%s is not a task's network", name)
	}
	for _, c := range n.Containers {
		s.d.call(ctx, http.MethodPost, "/networks/"+url.PathEscape(name)+"/disconnect", nil,
			map[string]any{"Container": c.Name, "Force": true}, nil)
	}
	if err := s.d.call(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("remove the network %s: %v", name, err)
	}
	return nil
}

// removeStrayTaskNets removes the networks of tasks no longer there.
func (s *engineServer) removeStrayTaskNets(ctx context.Context) {
	var nets []apiNetworkFull
	q := url.Values{"filters": {`{"label":["eriphe.network=task"]}`}}
	if s.d.call(ctx, http.MethodGet, "/networks", q, nil, &nets) != nil {
		return
	}
	s.networking.Lock()
	defer s.networking.Unlock()
	for _, n := range nets {
		s.endTaskNet(ctx, n.Name)
	}
}

// taskNetOf is the task's own network, if the engine made one, else "".
func (s *engineServer) taskNetOf(ctx context.Context, task string) string {
	var n apiNetworkFull
	if s.d.call(ctx, http.MethodGet, "/networks/"+url.PathEscape(taskNetPrefix+task), nil, nil, &n) != nil {
		return ""
	}
	if n.Labels["eriphe.network"] != "task" || n.Labels["eriphe.task"] != task {
		return ""
	}
	return taskNetPrefix + task
}

// freeSubnet is the first subnet of the pool's size taskNetBits that no
// network uses.
func freeSubnet(pool netip.Prefix, nets []apiNetworkFull) (netip.Prefix, error) {
	var used []netip.Prefix
	for _, n := range nets {
		for _, c := range n.IPAM.Config {
			if p, err := netip.ParsePrefix(c.Subnet); err == nil && p.Addr().Is4() {
				used = append(used, p.Masked())
			}
		}
	}
	pool = pool.Masked()
	if pool.Bits() > taskNetBits {
		return netip.Prefix{}, fmt.Errorf("-task-pool %s is smaller than a task's subnet (/%d)", pool, taskNetBits)
	}
	step := uint32(1) << (32 - taskNetBits)
	start := pool.Addr().As4()
	base := uint32(start[0])<<24 | uint32(start[1])<<16 | uint32(start[2])<<8 | uint32(start[3])
	count := uint32(1) << (taskNetBits - pool.Bits())
	for i := uint32(0); i < count; i++ {
		a := base + i*step
		cand := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)}), taskNetBits)
		free := true
		for _, u := range used {
			if u.Overlaps(cand) {
				free = false
				break
			}
		}
		if free {
			return cand, nil
		}
	}
	return netip.Prefix{}, fmt.Errorf("no subnet left in -task-pool %s: every one is used", pool)
}
