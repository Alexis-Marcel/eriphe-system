package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project's secrets and password databases are sensitive, whatever their
// case; a folder named like an Unraid share elsewhere is not.
func TestSensitiveFiles(t *testing.T) {
	for _, name := range []string{".env", ".envrc", ".ENV", "Passwords.kdbx", "vault.KDBX", ".ssh"} {
		if !sensitive(name) {
			t.Errorf("%s is not sensitive", name)
		}
	}
	for _, name := range []string{"env", ".env.example", "kdbx-notes", "system", "domains", "Documents"} {
		if sensitive(name) {
			t.Errorf("%s is sensitive", name)
		}
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app", "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", ".env"), []byte("TOKEN=x"), 0o600)
	os.MkdirAll(filepath.Join(dir, "keys"), 0o755)
	os.WriteFile(filepath.Join(dir, "keys", "Family.kdbx"), nil, 0o600)
	for _, p := range []string{"app", "keys", ""} {
		if err := checkShareable(filepath.Join(dir, p)); err == nil || err.(*shareError).Code != "holds_sensitive" {
			t.Errorf("%q: %v, want holds_sensitive", p, err)
		}
	}
	if err := checkShareable(filepath.Join(dir, "app", "src")); err != nil {
		t.Errorf("app/src: %v", err)
	}
}

// What the node cannot search whole is not shared: a folder it cannot read,
// or too many of them.
func TestCheckShareableFailsClosed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every folder")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "Photos", "Locked")
	os.MkdirAll(locked, 0o755)
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if err := checkShareable(dir); err == nil || err.(*shareError).Code != "unreadable_sub" || !strings.Contains(err.Error(), "Photos/Locked") {
		t.Errorf("a locked subfolder: %v", err)
	}
	if err := checkShareable(locked); err == nil || err.(*shareError).Code != "unreadable" {
		t.Errorf("a locked folder: %v", err)
	}
	// Below scanDepth, not searched: not a refusal.
	deep := filepath.Join(dir, "Deep", "a", "b", "c", "Locked")
	os.MkdirAll(deep, 0o755)
	os.Chmod(deep, 0)
	t.Cleanup(func() { os.Chmod(deep, 0o755) })
	if err := checkShareable(filepath.Join(dir, "Deep")); err != nil {
		t.Errorf("a locked folder below scanDepth: %v", err)
	}

	big := filepath.Join(t.TempDir(), "big")
	for i := range scanDirs {
		os.MkdirAll(filepath.Join(big, fmt.Sprint(i/100), fmt.Sprint(i%100)), 0o755)
	}
	if err := checkShareable(big); err == nil || err.(*shareError).Code != "too_large" {
		t.Errorf("%d folders: %v", scanDirs, err)
	}
	if err := checkShareable(filepath.Join(big, "7")); err != nil {
		t.Errorf("one of them: %v", err)
	}
}

// Unraid's system and domains shares, in each of /mnt's user shares, disks
// and pools, are private: not listed, not shared, nor a folder holding them.
// A folder called system elsewhere is the user's; a root inside one is
// shared from as declared.
func TestUnraidPrivate(t *testing.T) {
	n, _ := shareFixture(t)
	mnt, _ := filepath.EvalSymlinks(t.TempDir())
	old := unraidMnt
	unraidMnt = mnt
	t.Cleanup(func() { unraidMnt = old })
	user := filepath.Join(mnt, "user")
	for _, d := range []string{"system/docker", "domains/vm1", "Docs", "Projects/app/system"} {
		os.MkdirAll(filepath.Join(user, d), 0o755)
	}
	os.MkdirAll(filepath.Join(mnt, "disk1", "domains", "vm2"), 0o755)
	n.opt.shares = shareFlag{"shares": {Path: user}, "mnt": {Path: mnt}, "vms": {Path: filepath.Join(user, "domains")}}
	// What the node would refuse to add: "sensitive" on the way, or false
	// when it holds one.
	check := func(share, rel string) any {
		f, err := n.resolveFolder(share, rel)
		if err != nil {
			return err.(*shareError).Code
		}
		return n.shareable(f) == nil
	}
	for c, want := range map[[2]string]any{
		{"shares", "system"}:      "sensitive",
		{"shares", "domains/vm1"}: "sensitive",
		{"mnt", "disk1/domains"}:  "sensitive",
		{"shares", ""}:            false, // holds system
		{"mnt", "disk1"}:          false, // holds domains
		{"shares", "Projects"}:    true,  // its system is the user's
		{"vms", "vm1"}:            true,  // a root inside domains
		{"shares", "Docs"}:        true,
		{"shares", "Projects/a"}:  "not_found",
	} {
		if got := check(c[0], c[1]); got != want {
			t.Errorf("%s/%s: %v, want %v", c[0], c[1], got, want)
		}
	}
}

// On a Mac, the explorer's tree is held to the same: whole, or not shared.
func TestAppleTreeFailsClosed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every folder")
	}
	n, root := appleShareFixture(t)
	locked := filepath.Join(root, "Clients", "ACME", "Locked")
	os.MkdirAll(locked, 0o755)
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	_, e, err := n.appleResolve(context.Background(), "data", "Clients", "tree")
	if err == nil {
		err = checkExplored(e)
	}
	if err == nil || err.(*shareError).Code != "unreadable_sub" {
		t.Errorf("a locked subfolder: %v", err)
	}
	if err := checkExplored(&exploration{tree: make([]string, treeMax+1)}); err == nil || err.(*shareError).Code != "too_large" {
		t.Errorf("a tree too large: %v", err)
	}
	if err := checkExplored(&exploration{tree: []string{"a", "a/b"}}); err != nil {
		t.Errorf("a whole tree: %v", err)
	}
}
