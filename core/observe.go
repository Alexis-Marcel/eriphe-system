package main

import (
	"slices"
	"sync"
	"time"
)

// What Eriphe observes of the user's machines, for the router (route.go)
// and where_now (where_now.go): per node, whether its machine is locked,
// how long it has been idle, and whether it is not to be disturbed. Told
// by the node with its status (GET /v1/node "idle" and "context"), kept in
// memory only, a few minutes at most, never in the database. These are
// observations, not a context: closed values, no app name, no window, and
// they only make Eriphe quieter (a locked, idle or busy machine is skipped).
// The user's situation ("in Lyon until Sunday") is dated memory
// (situations.go); the house is what Home Assistant says of the entities
// the user follows (ha_follow.go).

// dndReasons are the reasons a node may give for not to be disturbed.
var dndReasons = []string{"game", "meeting", "fullscreen", "mic", "focus"}

// machineObs is what a node said of its machine, when.
type machineObs struct {
	IdleKnown bool
	Idle      time.Duration
	Locked    bool
	DND       bool
	Reason    string // one of dndReasons, or ""
	At        time.Time
}

// presence is the machine as a word: locked, idle (two minutes or more) or
// active; "" when the node cannot tell.
func (m machineObs) presence() string {
	switch {
	case !m.IdleKnown:
		return ""
	case m.Locked:
		return "locked"
	case m.Idle >= activeIdle:
		return "idle"
	}
	return "active"
}

// observations keeps the machines' observations. Its zero value is ready.
type observations struct {
	mu       sync.Mutex
	machines map[string]machineObs // by node
}

func (o *observations) put(node string, m machineObs) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.machines == nil {
		o.machines = map[string]machineObs{}
	}
	o.machines[node] = m
}

func (o *observations) forget(node string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.machines, node)
}

// machine is what a node said lately of its machine; an observation older
// than idleFresh is unknown.
func (o *observations) machine(node string, now time.Time) (machineObs, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	m, ok := o.machines[node]
	if !ok || now.Before(m.At) || now.Sub(m.At) >= idleFresh {
		return machineObs{}, false
	}
	return m, true
}

// nodes are the nodes with a fresh observation, sorted.
func (o *observations) nodes(now time.Time) []string {
	o.mu.Lock()
	var out []string
	for n := range o.machines {
		out = append(out, n)
	}
	o.mu.Unlock()
	out = slices.DeleteFunc(out, func(n string) bool { _, ok := o.machine(n, now); return !ok })
	slices.Sort(out)
	return out
}

// anyDND tells whether a machine of the user's is not to be disturbed now.
func (o *observations) anyDND(now time.Time) bool {
	for _, n := range o.nodes(now) {
		if m, _ := o.machine(n, now); m.DND {
			return true
		}
	}
	return false
}
