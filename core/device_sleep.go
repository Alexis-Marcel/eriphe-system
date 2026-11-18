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
// eriphe-core cannot tell them apart at the moment a device leaves, so it
// says what it saw: a device that came back after an absence at least
// wakesForSleep times in the last wakeWindow is asleep while it is away;
// any other device away is offline. The always-on machine never sleeps.

const (
	napMin        = 2 * time.Minute // a shorter absence is a blip, not a sleep
	wakeWindow    = 24 * time.Hour
	wakesForSleep = 2
	// asleepDeviceGrace is how long a task not started yet waits for a device
	// asleep: it comes back, but maybe not for a while (placement.go).
	asleepDeviceGrace = residentDeviceGrace
)

func wakesKey(name string) string { return "device_wakes:" + name }

// wakes are the times a device came back after an absence, within wakeWindow.
func (c *core) wakes(name string, now time.Time) []int64 {
	var out []int64
	for _, f := range strings.Split(c.store.get(wakesKey(name)), ",") {
		if t, err := strconv.ParseInt(f, 10, 64); err == nil && now.Sub(time.Unix(t, 0)) < wakeWindow {
			out = append(out, t)
		}
	}
	return out
}

// noteConnect records a device's connection: a wake if it had been away
// for napMin at least, counted from eriphe-core's start at the earliest.
func (c *core) noteConnect(name string, now time.Time) {
	devices, err := c.store.devices()
	if err != nil {
		return
	}
	i := slices.IndexFunc(devices, func(n DeviceRow) bool { return n.Name == name })
	if i < 0 || devices[i].Online || now.Sub(c.since(devices[i])) < napMin {
		return
	}
	ws := append(c.wakes(name, now), now.Unix())
	fs := make([]string, len(ws))
	for j, t := range ws {
		fs[j] = strconv.FormatInt(t, 10)
	}
	c.store.set(wakesKey(name), strings.Join(fs, ","))
}

// asleep tells whether a device away is asleep rather than off.
func (c *core) asleep(n DeviceRow) bool {
	if n.Online || n.Suspended || parseInfo(n).AlwaysOn {
		return false
	}
	return len(c.wakes(n.Name, time.Now())) >= wakesForSleep
}
