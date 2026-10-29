package main

import (
	"log"
	"time"
)

// Tasks run as root in their container (container.go): what they write in
// a folder's Eriphe/ belongs to root on the machine. The node gives it back
// to the owner of Eriphe/ (UID 1000 when the node made it, or whoever made
// it), so the user can open, change and delete the results as theirs, and
// no setuid program of root's stays there: at the end of each turn, every
// minute while the task runs, and when its container is gone. With Apple's
// container, the Mac's file sharing writes everything as the Mac's user
// already: nothing to do.

const handBackEvery = time.Minute

// handBack gives the task's results back to their folders' owners.
func (n *node) handBack(t *Task) {
	if n.apple() {
		return
	}
	t.mu.Lock()
	dirs := append([]string(nil), t.results...)
	t.mu.Unlock()
	for _, d := range dirs {
		if _, err := handBackDir(d); err != nil {
			log.Printf("task %s: results in %s: %v", t.ID, d, err)
		}
	}
}

// handBackWhileRunning hands back every handBackEvery until done is closed,
// then once more.
func (n *node) handBackWhileRunning(t *Task, done <-chan struct{}) {
	tick := time.NewTicker(handBackEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			n.handBack(t)
		case <-done:
			n.handBack(t)
			return
		}
	}
}
