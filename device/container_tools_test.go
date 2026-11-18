package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runRepoTools runs the script with a PATH holding only a fake apt-get and
// the given programs, and returns what apt-get was asked to install.
func runRepoTools(t *testing.T, repo string, present ...string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fake := "#!/bin/sh\necho \"$*\" >> " + calls + "\n"
	for _, name := range append(present, "apt-get") {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("/bin/sh", "-c", repoToolsScript)
	cmd.Env = []string{"PATH=" + dir, "ERIPHE_REPO=" + repo}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got, _ := os.ReadFile(calls)
	for _, line := range strings.Split(string(got), "\n") {
		if strings.HasPrefix(line, "install") {
			return strings.TrimPrefix(line, "install -y -qq ")
		}
	}
	return ""
}

func TestRepoTools(t *testing.T) {
	const repo = "https://github.com/owner/repo.git"
	if got := runRepoTools(t, repo); strings.Join(strings.Fields(got), " ") != "git gh" {
		t.Errorf("with neither: %q", got)
	}
	if got := runRepoTools(t, repo, "git"); strings.TrimSpace(got) != "gh" {
		t.Errorf("with git: %q", got)
	}
	if got := runRepoTools(t, repo, "git", "gh"); got != "" {
		t.Errorf("with both: %q", got)
	}
	if got := runRepoTools(t, ""); got != "" {
		t.Errorf("no repository, still: %q", got)
	}
}
