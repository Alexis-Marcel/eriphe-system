package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// privateDirs are whole places of this machine no folder is ever shared
// from, nor listed in, unless a root itself is inside one: on a Mac,
// ~/Library (mail, messages, browsers' cookies, every app's settings and
// tokens), which iCloud Drive and the clouds' folders (~/Library/Mobile
// Documents/com~apple~CloudDocs, ~/Library/CloudStorage/…) are roots of
// their own inside.
var privateDirs = sync.OnceValue(func() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	lib := filepath.Join(home, "Library")
	if real, err := filepath.EvalSymlinks(lib); err == nil {
		lib = real
	}
	return []string{lib}
})

// unraidMnt is where Unraid mounts its user shares, disks and pools
// (/mnt/user, /mnt/cache, /mnt/disk1…). In each, its own shares system
// (docker.img, libvirt.img: every container's and VM's settings) and
// domains (the VMs' disks) are private places too. Only there: a folder
// called system elsewhere is the user's.
var unraidMnt = func() string {
	if runtime.GOOS == "linux" {
		return "/mnt"
	}
	return ""
}()

// unraidPrivate is the Unraid private place p is in, or "".
func unraidPrivate(p string) string {
	if unraidMnt == "" {
		return ""
	}
	rel, err := filepath.Rel(unraidMnt, p)
	if err != nil || !within(unraidMnt, p) {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || (parts[1] != "system" && parts[1] != "domains") {
		return ""
	}
	return filepath.Join(unraidMnt, parts[0], parts[1])
}

// private tells whether real, a resolved path under the resolved root, is in
// a private place the root is not itself in.
func private(rootReal, real string) bool {
	for _, d := range privateDirs() {
		if within(d, real) && !within(d, rootReal) {
			return true
		}
	}
	if d := unraidPrivate(real); d != "" && !within(d, rootReal) {
		return true
	}
	return false
}
