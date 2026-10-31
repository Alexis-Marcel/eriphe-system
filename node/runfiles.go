package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
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
		if !runFileName(name) {
			return fmt.Errorf("bad run file name %q", name)
		}
		if sub, _, ok := strings.Cut(name, "/"); ok {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
				return err
			}
		}
		f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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

// runFileName is a run file's name: a file, or a file in one subdirectory
// ("claude-managed/managed-settings.json"), never out of the run directory.
func runFileName(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || p != filepath.Base(p) {
			return false
		}
	}
	return true
}

// runSubMounts are run subdirectories mounted on their own, read-only, at
// the place their agent reads them.
var runSubMounts = map[string]string{claudeManaged: claudeManagedMount}

// removeRunFiles deletes the task's run files once its container is gone.
func removeRunFiles(t *Task) {
	if t.dir != "" {
		os.RemoveAll(runDir(t))
	}
}
