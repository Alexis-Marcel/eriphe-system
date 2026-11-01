package main

import (
	"context"
	"os/exec"
	"time"
)

// machineIdle on a Mac: ioreg, which reads IOKit without cgo.
func machineIdle(ctx context.Context) idleState {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-c", "IOHIDSystem", "-r", "-d", "1", "-k", "HIDIdleTime").Output()
	if err != nil {
		return idleState{}
	}
	secs, ok := parseHIDIdle(out)
	if !ok {
		return idleState{}
	}
	s := idleState{Known: true, Seconds: secs}
	if root, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-n", "Root", "-d", "1", "-k", "IOConsoleUsers").Output(); err == nil {
		s.Locked = parseLocked(root)
	}
	return s
}
