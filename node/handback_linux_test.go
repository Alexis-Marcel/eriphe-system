package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// What a root task wrote in Eriphe/ goes to Eriphe/'s owner; a setuid
// program loses its bit; a symbolic link is changed itself, never what it
// points to; another owner's file stays theirs. Needs root (CI runs it in
// a container: docker run --rm -v $PWD:/src -w /src golang go test -run HandBack).
func TestHandBack(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	dir := t.TempDir()
	results := filepath.Join(dir, "Eriphe")
	outside := filepath.Join(dir, "outside")
	os.WriteFile(outside, []byte("host"), 0o600) // root's, out of Eriphe/
	os.Mkdir(results, 0o2775)
	os.Chown(results, 1000, 100)
	os.MkdirAll(filepath.Join(results, "sub", "deeper"), 0o775)
	os.WriteFile(filepath.Join(results, "report.pdf"), []byte("x"), 0o664)
	os.WriteFile(filepath.Join(results, "sub", "deeper", "a.txt"), []byte("x"), 0o664)
	os.WriteFile(filepath.Join(results, "shell"), []byte("x"), 0o755)
	os.Chmod(filepath.Join(results, "shell"), 0o4755)
	os.WriteFile(filepath.Join(results, "users"), []byte("x"), 0o644)
	os.Chown(filepath.Join(results, "users"), 1001, 1001)
	os.Symlink(outside, filepath.Join(results, "link"))

	n, err := handBackDir(results)
	if err != nil {
		t.Fatal(err)
	}
	owner := func(p string) (uint32, os.FileMode) {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Sys().(*syscall.Stat_t).Uid, fi.Mode()
	}
	for _, p := range []string{"report.pdf", "sub", "sub/deeper", "sub/deeper/a.txt", "shell", "link"} {
		if uid, _ := owner(filepath.Join(results, p)); uid != 1000 {
			t.Errorf("%s: UID %d, want 1000", p, uid)
		}
	}
	if _, mode := owner(filepath.Join(results, "shell")); mode&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Errorf("shell is still %v", mode)
	}
	if uid, _ := owner(filepath.Join(results, "users")); uid != 1001 {
		t.Errorf("another owner's file went to %d", uid)
	}
	if uid, _ := owner(outside); uid != 0 {
		t.Errorf("the link's target was changed: UID %d", uid)
	}
	if n != 6 {
		t.Errorf("%d entries handed back, want 6", n)
	}
	// Eriphe/ of root's own: no one to give it to.
	os.Chown(results, 0, 0)
	if _, err := handBackDir(results); err == nil {
		t.Error("handed back to root")
	}
}
