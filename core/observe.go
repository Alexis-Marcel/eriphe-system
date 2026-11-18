package main

import (
	"sync"
	"time"
)

// What Eriphe observes of the user's machines, for the router (route.go)
// and where_now (where_now.go): per device, whether its machine is locked,
// how long it has been idle, and whether it is not to be disturbed. Told
// by the device with its status (GET /v1/device "idle" and "context"), kept in
// memory only, a few minutes at most, never in the database. These are
// observations, not a context: closed values, no app name, no window, and
// they only make Eriphe quieter (a locked, idle or busy machine is skipped).
// The user's situation ("in Lyon until Sunday") is dated memory
// (situations.go); the room they are in is what a voice channel says
// (channel_out.go); anything else, Eriphe reads from a tool when it needs
// it.

// dndReasons are the reasons a device may give for not to be disturbed.
var dndReasons = []string{"game", "meeting", "fullscreen", "mic", "focus"}

// machineObs is what a device said of its machine, when.
type machineObs struct {
	IdleKnown bool
	Idle      time.Duration
	Locked    bool
	DND       bool
	Reason    string // one of dndReasons, or ""
	At        time.Time
}

// presence is the machine as a word: locked, idle (two minutes or more) or
// active; "" when the device cannot tell.
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
	machines map[string]machineObs // by device
}

func (o *observations) put(device string, m machineObs) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.machines == nil {
		o.machines = map[string]machineObs{}
	}
	o.machines[device] = m
}

func (o *observations) forget(device string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.machines, device)
}

// machine is what a device said lately of its machine; an observation older
// than idleFresh is unknown.
func (o *observations) machine(device string, now time.Time) (machineObs, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	m, ok := o.machines[device]
	if !ok || now.Before(m.At) || now.Sub(m.At) >= idleFresh {
		return machineObs{}, false
	}
	return m, true
}
