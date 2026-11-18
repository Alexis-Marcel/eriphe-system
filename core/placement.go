package main

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// Placement classes, like Kubernetes' hard rules and preferences. Nothing
// here is a user setting: the class comes from what the task is.
//
//   - resident: an assistant that talks to the user or is woken by events
//     (the brain first). Hard rule: an online always-on device that fits; the
//     best other device only when none does, and it comes back as soon as it
//     can. Someone waits for its answer: a machine that sleeps is no home.
//   - bound: a task lent the user's folders (placement.Bound). It goes to
//     a machine holding them up to date (its folders' or one with copies,
//     folder_copies.go), the best by the scores, and waits for one.
//   - the others: the scores of scheduler.go, preferences only.
//
// A device chosen by hand (the task's or the assistant's device) always wins.

const (
	// residentDeviceGrace is how long a resident task waits for its device to
	// come back before it moves: short, someone may be waiting.
	residentDeviceGrace = 30 * time.Second
	// alwaysOnGrace is how long a resident assistant waits for an
	// always-on device that left before it starts or moves elsewhere: after
	// eriphe-core restarts, the device of its own machine connects a few
	// seconds later, and a blip passes. Started on a device that may sleep,
	// it would move back at its first idle and lose its session.
	alwaysOnGrace = time.Minute
	// pendingDeviceGrace is how long a task not started yet waits for its
	// device before it is placed again, like a pod on an unreachable device.
	pendingDeviceGrace = 5 * time.Minute
)

// resident tells whether an assistant of this role lives on an always-on
// device: one that talks to the user, hears the conversation, or is woken
// by triggers.
func (r assistantRole) resident() bool {
	return r.can(powerTalksToUser) || r.can(powerReceivesConversation) || len(r.Triggers) > 0 || len(r.Wakes) > 0
}

// residentKind tells whether tasks of this kind are resident.
func residentKind(kind string) bool {
	r, ok := assistantRoles[kind]
	return ok && r.resident()
}

// steadyDevices are the online always-on devices that can run p.
func steadyDevices(devices []DeviceRow, p placement) []DeviceRow {
	var out []DeviceRow
	for _, n := range devices {
		if n.Online && p.fits(n) && parseInfo(n).AlwaysOn {
			out = append(out, n)
		}
	}
	return out
}

// awaitedAlwaysOn is the always-on device that can run p and left less than
// alwaysOnGrace ago, counted from eriphe-core's start at the earliest,
// when no always-on device that can is online: a resident task waits for
// it rather than start on a device that may sleep. "" if there is none.
func (c *core) awaitedAlwaysOn(p placement) string {
	all, err := c.store.devices()
	if err != nil || len(steadyDevices(c.schedulable(all), p)) > 0 {
		return ""
	}
	for _, n := range all {
		if !n.Online && !n.Suspended && parseInfo(n).AlwaysOn && p.fits(n) && time.Since(c.since(n)) < alwaysOnGrace {
			return n.Name
		}
	}
	return ""
}

// placementOf is what a task asks of its device, from its agent and folders now.
func (c *core) placementOf(t *TaskRow) (placement, error) {
	_, agent, err := c.conf().agent(t.Agent)
	if err != nil {
		return placement{}, err
	}
	folders, _, err := c.folderGrant(splitNames(t.Folders))
	if err != nil {
		return placement{}, err
	}
	p := c.taskPlacement(agent, folders)
	p.Resident = residentKind(t.Kind)
	return p, nil
}

// noteFallback logs why a resident task went to a device that may sleep.
func noteFallback(id, kind, name string, devices []DeviceRow, p placement) {
	if !p.Resident || name == "" {
		return
	}
	var why []string
	for _, n := range devices {
		if n.Name == name && parseInfo(n).AlwaysOn {
			return
		}
		if !parseInfo(n).AlwaysOn {
			continue
		}
		switch {
		case !n.Online:
			why = append(why, n.Name+" is offline")
		case !p.fits(n):
			why = append(why, n.Name+" cannot run "+p.String())
		}
	}
	if len(why) == 0 {
		why = []string{"no device is always on"}
	}
	log.Printf("task %s (%s) placed on %s for now: %s", id, kind, name, strings.Join(why, ", "))
}

// since is when a device was last there, eriphe-core's start at the
// earliest: after a restart, devices need a moment to connect again.
func (c *core) since(n DeviceRow) time.Time {
	if n.LastSeen.Before(c.started) {
		return c.started
	}
	return n.LastSeen
}

// moveResident moves an assistant's task that should live elsewhere: its
// device has been offline past residentDeviceGrace (alwaysOnGrace for an
// always-on one) and another can take it, or it runs on a device that may
// sleep while an always-on one can take it now (only at idle, between two turns). It stops the task; the
// controller starts the next one, which the scheduler places, without
// resuming: the session is on the old device, and the conversation is eriphe-core's.
// It reports whether it moved it.
func (c *core) moveResident(a Assistant, r assistantRole, t *TaskRow) bool {
	if a.Device != "" || t.Device == "" || !r.resident() {
		return false
	}
	p, err := c.placementOf(t)
	if err != nil || p.Bound {
		return false // bound to its folders: it waits for its machine
	}
	all, err := c.store.devices()
	if err != nil {
		return false
	}
	devices := c.schedulable(all)
	i := slices.IndexFunc(all, func(n DeviceRow) bool { return n.Name == t.Device })
	var why string
	switch {
	case i < 0:
		return false // forgotten: its tasks are lost with it, and the controller starts another
	case !all[i].Online:
		grace := residentDeviceGrace
		if parseInfo(all[i]).AlwaysOn {
			grace = alwaysOnGrace // it comes back: eriphe-core's own machine, or a blip
		}
		if time.Since(c.since(all[i])) < grace {
			return false
		}
		if !slices.ContainsFunc(devices, func(n DeviceRow) bool { return n.Online && n.Name != t.Device && p.fits(n) }) {
			return false // nowhere else to go: it waits for its device
		}
		why = fmt.Sprintf("device %s has been offline since %s", t.Device, c.since(all[i]).Format(time.TimeOnly))
	case !parseInfo(all[i]).AlwaysOn && t.State == stateIdle && !c.hasUndelivered(t.ID):
		steady := steadyDevices(devices, p)
		if len(steady) == 0 {
			return false
		}
		why = fmt.Sprintf("device %s may sleep and the always-on %s can take it", t.Device, steady[0].Name)
	default:
		return false
	}
	log.Printf("%s: moving task %s off %s: %s", c.assistantLog(a), t.ID, t.Device, why)
	// What it was given but its device never received goes to the next task.
	if a.has(triggerConversation) && c.hasUndelivered(t.ID) {
		if from, to := c.turn.span(); from > 0 {
			c.store.undeliver(from, to)
		}
	}
	c.store.set("brain_stopped:"+t.ID, "1") // not an end to report
	c.store.updateTask(t.ID, map[string]any{"desired": desiredStopped, "state": stateStopped,
		"pending_reason": "moved: " + why})
	c.updateAssistant(a.Name, func(st *AssistantStatus) {
		st.Session, st.Resume, st.NextStart = "", "", 0
	})
	c.bump()
	return true
}

// replaceStranded places again the tasks not started yet whose device has
// been offline past pendingDeviceGrace (asleepDeviceGrace if it is asleep), unless they are bound to it (by
// hand or by their folders). Running tasks keep their device: their work
// is there. Assistants' tasks are moveResident's.
func (c *core) replaceStranded() {
	rows, err := c.store.stranded()
	if err != nil || len(rows) == 0 {
		return
	}
	devices, err := c.store.devices()
	if err != nil {
		return
	}
	moved := false
	for _, t := range rows {
		if isRoleKind(t.Kind) {
			continue
		}
		i := slices.IndexFunc(devices, func(n DeviceRow) bool { return n.Name == t.Device })
		asleep := i >= 0 && c.asleep(devices[i]) // device_sleep.go
		grace := pendingDeviceGrace
		if asleep {
			grace = asleepDeviceGrace // it may not wake for a while
		}
		if i >= 0 && (devices[i].Online || time.Since(c.since(devices[i])) < grace) {
			continue
		}
		if p, err := c.placementOf(t); err != nil ||
			p.Bound && !slices.ContainsFunc(c.schedulable(devices), func(n DeviceRow) bool { return n.Online && n.Name != t.Device && p.fits(n) }) {
			continue // bound to its folders, and no other machine holds them up to date now
		}
		reason := fmt.Sprintf("device %s is offline", t.Device)
		if asleep {
			reason = fmt.Sprintf("device %s is asleep", t.Device)
		}
		if ok, _ := c.store.unassign(t.ID, t.Device, reason); ok {
			log.Printf("task %s leaves device %s: %s, it has not started", t.ID, t.Device, reason)
			moved = true
		}
	}
	if moved {
		c.placePending()
		c.bump()
	}
}
