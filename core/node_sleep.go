package main

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// Asleep or off. A machine that sleeps (a laptop, a desktop set to sleep)
// disconnects and comes back by itself, often: a Mac in maintenance sleep
// wakes for a minute every quarter of an hour. One that is off stays away.
// eriphe-core cannot tell them apart at the moment a node leaves, so it
// says what it saw: a node that came back after an absence at least
// wakesForSleep times in the last wakeWindow is asleep while it is away;
// any other node away is offline. The always-on machine never sleeps.

const (
	napMin        = 2 * time.Minute // a shorter absence is a blip, not a sleep
	wakeWindow    = 24 * time.Hour
	wakesForSleep = 2
	// asleepNodeGrace is how long a task not started yet waits for a node
	// asleep: it comes back, but maybe not for a while (placement.go).
	asleepNodeGrace = residentNodeGrace
)

func wakesKey(name string) string { return "node_wakes:" + name }

// wakes are the times a node came back after an absence, within wakeWindow.
func (c *core) wakes(name string, now time.Time) []int64 {
	var out []int64
	for _, f := range strings.Split(c.store.get(wakesKey(name)), ",") {
		if t, err := strconv.ParseInt(f, 10, 64); err == nil && now.Sub(time.Unix(t, 0)) < wakeWindow {
			out = append(out, t)
		}
	}
	return out
}

// noteConnect records a node's connection: a wake if it had been away
// for napMin at least, counted from eriphe-core's start at the earliest.
func (c *core) noteConnect(name string, now time.Time) {
	nodes, err := c.store.nodes()
	if err != nil {
		return
	}
	i := slices.IndexFunc(nodes, func(n NodeRow) bool { return n.Name == name })
	if i < 0 || nodes[i].Online || now.Sub(c.since(nodes[i])) < napMin {
		return
	}
	ws := append(c.wakes(name, now), now.Unix())
	fs := make([]string, len(ws))
	for j, t := range ws {
		fs[j] = strconv.FormatInt(t, 10)
	}
	c.store.set(wakesKey(name), strings.Join(fs, ","))
}

// asleep tells whether a node away is asleep rather than off.
func (c *core) asleep(n NodeRow) bool {
	if n.Online || n.Suspended || parseInfo(n).AlwaysOn {
		return false
	}
	return len(c.wakes(n.Name, time.Now())) >= wakesForSleep
}
