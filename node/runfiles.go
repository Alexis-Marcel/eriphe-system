package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// runMount is where a task container finds the files its runtime hands its
// agent (fileRuntime), read-only.
const runMount = "/run/eriphe"

// runPath is a run file's path in the container.
func runPath(name string) string { return path.Join(runMount, name) }

// runDir is the task's run directory on the node: the node's own, 0700,
// with files of 0600 that only live while the container runs.
func runDir(t *Task) string { return filepath.Join(t.dir, "run") }

// writeRunFiles replaces the task's run directory with files: nothing of an
// earlier run is left. No files, no directory.
func writeRunFiles(t *Task, files map[string][]byte) error {
	dir := runDir(t)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, data := range files {
		if name != filepath.Base(name) || name == "." || name == ".." {
			return fmt.Errorf("bad run file name %q", name)
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// removeRunFiles deletes the task's run files once its container is gone.
func removeRunFiles(t *Task) {
	if t.dir != "" {
		os.RemoveAll(runDir(t))
	}
}
