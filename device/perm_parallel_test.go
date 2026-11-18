package main

import (
	"context"
	"testing"
)

// An agent asks for two permissions at once: answering the first leaves the
// task waiting for the second, so eriphe-core still sees it pending.
func TestTwoPermissionsAtOnce(t *testing.T) {
	task := &Task{ID: "t-1", State: stateRunning, perms: map[string]*permission{}}
	done := make(chan string, 2)
	for _, tool := range []string{"mcp__mail__search_messages", "mcp__mail__list_folders"} {
		go func() {
			task.decide(context.Background(), tool, nil)
			done <- tool
		}()
	}
	waitFor(t, func() bool { task.mu.Lock(); defer task.mu.Unlock(); return len(task.perms) == 2 })

	task.mu.Lock()
	var first, second *permission
	for _, p := range task.perms {
		if first == nil {
			first = p
		} else {
			second = p
		}
	}
	task.mu.Unlock()

	first.answer <- permAnswer{Allow: true}
	<-done
	if s := task.state(); s != stateWaitingPermission {
		t.Fatalf("state %s with a permission still pending", s)
	}
	second.answer <- permAnswer{Allow: true}
	<-done
	if s := task.state(); s != stateRunning {
		t.Errorf("state %s once both are answered", s)
	}
}
