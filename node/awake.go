package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Keeping the machine awake. While a task is at work (an agent running a
// turn), the node keeps the machine out of system sleep, and lets it go
// once none is: a laptop no longer cuts a task short by sleeping, and goes
// back to sleep when the work is done. A task waiting for a permission or
// for the user does not hold it: its container waits through a sleep.
//
// Two ways, as the node runs:
//
//   - by itself, where it runs on the system (systemHold): caffeinate on a
//     Mac (Apple's container), systemd-inhibit on a Linux with systemd;
//   - through <root>/awake, which it writes while it holds the machine and
//     removes after, for a helper on the host side when it runs in a
//     container or in WSL (install/README.md, Sleep).

const awakeFile = "awake"

// holdFunc holds the machine awake until the returned function is called.
type holdFunc func() (release func(), err error)

type keepAwake struct {
	file string
	hold holdFunc // nil where the node cannot do it itself
	how  string   // what holds it here, for GET /v1/node

	mu      sync.Mutex
	on      bool
	release func()
}

func newKeepAwake(root string) *keepAwake {
	hold, how := systemHold()
	k := &keepAwake{file: filepath.Join(root, awakeFile), hold: hold, how: how}
	os.Remove(k.file) // left by a node that stopped while it held the machine
	return k
}

// set holds the machine while working > 0, and lets it go at 0.
func (k *keepAwake) set(working int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if on := working > 0; on == k.on {
		return
	}
	k.on = working > 0
	if !k.on {
		os.Remove(k.file)
		if k.release != nil {
			k.release()
			k.release = nil
		}
		log.Printf("awake: no task at work, the machine may sleep")
		return
	}
	if err := os.WriteFile(k.file, fmt.Appendf(nil, "tasks=%d\n", working), 0o644); err != nil {
		log.Printf("awake: %v", err)
	}
	if k.hold != nil {
		release, err := k.hold()
		if err != nil {
			log.Printf("awake: %s: %v", k.how, err)
		}
		k.release = release
	}
	how := k.how
	if k.hold == nil {
		how = "by the host's helper, if any, through " + k.file
	}
	log.Printf("awake: %d task(s) at work, the machine stays awake: %s", working, how)
}

// working counts the tasks whose agent is at work now.
func (n *node) working() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, t := range n.tasks {
		if t.state() == stateRunning {
			c++
		}
	}
	return c
}
