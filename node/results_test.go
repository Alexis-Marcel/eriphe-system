package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resultsEvents are the results_conflict events a task got.
func resultsEvents(t *Task) []map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []map[string]any
	for _, e := range t.events {
		if e.Type == "results_conflict" {
			out = append(out, e.Data)
		}
	}
	return out
}

// The results folder takes the assistant's name: Eriphe/ by default, its
// own when it has one, a former one renamed on first use, and, when the
// new name is taken, the former one kept and said.
func testResultsName(t *testing.T, n *node, root string, mounts func(*Task) ([]string, error)) {
	t.Helper()
	acme := filepath.Join(root, "Clients", "ACME")
	isDir := func(name string) bool {
		fi, err := os.Lstat(filepath.Join(acme, name))
		return err == nil && fi.IsDir()
	}
	run := func(tf TaskFolder) (string, *Task) {
		t.Helper()
		tf.Name, tf.Share, tf.Path, tf.Write = "ACME", "data", "Clients/ACME", true
		task := &Task{Spec: TaskSpec{Folders: []TaskFolder{tf}}}
		args, err := mounts(task)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(args, " "), task
	}
	mountOf := func(use, cur string) string { return acme + "/" + use + ",target=/work/folders/ACME/" + cur }

	// An older eriphe-core: Eriphe/.
	got, _ := run(TaskFolder{})
	if !strings.Contains(got, mountOf("Eriphe", "Eriphe")) || !isDir("Eriphe") {
		t.Fatalf("default: %s", got)
	}
	// Renamed Jarvis: Eriphe/ becomes Jarvis/, the task sees Jarvis/.
	os.WriteFile(filepath.Join(acme, "Eriphe", "devis.txt"), []byte("1"), 0o644)
	got, task := run(TaskFolder{Results: "Jarvis", Former: []string{"Eriphe"}})
	if !strings.Contains(got, mountOf("Jarvis", "Jarvis")) || isDir("Eriphe") || !isDir("Jarvis") || len(resultsEvents(task)) != 0 {
		t.Fatalf("renamed: %s", got)
	}
	if _, err := os.Stat(filepath.Join(acme, "Jarvis", "devis.txt")); err != nil {
		t.Fatalf("the results did not follow: %v", err)
	}
	// Again: nothing to rename.
	if got, _ = run(TaskFolder{Results: "Jarvis", Former: []string{"Eriphe"}}); !strings.Contains(got, mountOf("Jarvis", "Jarvis")) {
		t.Fatalf("again: %s", got)
	}
	// Renamed Alfred, but an Alfred/ of the user's is there: nothing is
	// merged, Jarvis/ stays in use under the new name, and it is said.
	os.Mkdir(filepath.Join(acme, "Alfred"), 0o755)
	os.WriteFile(filepath.Join(acme, "Alfred", "mine.txt"), []byte("2"), 0o644)
	got, task = run(TaskFolder{Results: "Alfred", Former: []string{"Jarvis", "Eriphe"}})
	ev := resultsEvents(task)
	if !strings.Contains(got, mountOf("Jarvis", "Alfred")) || !isDir("Jarvis") || len(ev) != 1 ||
		ev[0]["kept"] != "Jarvis" || ev[0]["wanted"] != "Alfred" || ev[0]["folder"] != "ACME" {
		t.Fatalf("conflict: %s %v", got, ev)
	}
	if _, err := os.Stat(filepath.Join(acme, "Alfred", "mine.txt")); err != nil {
		t.Fatalf("the user's folder was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(acme, "Alfred", "devis.txt")); err == nil {
		t.Fatal("merged")
	}
	// A name that cannot be a folder falls back to Eriphe.
	if got, _ = run(TaskFolder{Results: "../x"}); !strings.Contains(got, "target=/work/folders/ACME/Eriphe") {
		t.Fatalf("bad name: %s", got)
	}
}

func TestResultsName(t *testing.T) {
	n, root := shareFixture(t)
	chownResults = func(string, int, int) error { return nil }
	defer func() { chownResults = os.Lchown }()
	testResultsName(t, n, root, func(task *Task) ([]string, error) {
		args, err := n.folderMounts(task)
		// docker's --mount spelling, as the Mac's.
		for i, a := range args {
			a = strings.Replace(a, ",dst=", ",target=", 1)
			args[i] = strings.Replace(a, "src=", "source=", 1)
		}
		return args, err
	})
}

func TestAppleResultsName(t *testing.T) {
	n, root := appleShareFixture(t)
	testResultsName(t, n, root, n.folderMounts)
}
