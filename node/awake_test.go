package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The machine is held while a task is at work, once, and let go after;
// the file for the host's helper follows; a file left by a node that
// stopped is removed at start.
func TestKeepAwake(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, awakeFile)
	os.WriteFile(file, []byte("tasks=1\n"), 0o644)
	k := newKeepAwake(root)
	if _, err := os.Stat(file); err == nil {
		t.Fatal("a file left from before is kept")
	}
	holds, releases := 0, 0
	k.hold = func() (func(), error) {
		holds++
		return func() { releases++ }, nil
	}
	held := func() bool {
		_, err := os.Stat(file)
		return err == nil
	}
	k.set(0)
	if holds != 0 || held() {
		t.Fatal("held with no task at work")
	}
	k.set(1)
	k.set(2)
	if holds != 1 || releases != 0 || !held() {
		t.Fatalf("holds %d, releases %d, file %v", holds, releases, held())
	}
	k.set(0)
	if releases != 1 || held() {
		t.Fatalf("releases %d, file %v", releases, held())
	}
	// Without a way of its own, the file alone says it.
	k.hold = nil
	if k.set(1); !held() || holds != 1 {
		t.Fatal("no file for the host's helper")
	}
	k.set(0)
}

// Only an agent at work holds the machine: one waiting for a permission
// or for the user lets it sleep.
func TestWorking(t *testing.T) {
	n := newNode(options{root: t.TempDir()})
	for id, s := range map[string]string{"a": stateRunning, "b": stateWaitingPermission, "c": stateIdle, "d": stateRunning} {
		n.tasks[id] = &Task{ID: id, State: s}
	}
	if w := n.working(); w != 2 {
		t.Fatalf("working = %d", w)
	}
}
