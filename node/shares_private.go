package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// private tells whether real, a resolved path under the resolved root, is in
// a private place the root is not itself in.
func private(rootReal, real string) bool {
	for _, d := range privateDirs() {
		if within(d, real) && !within(d, rootReal) {
			return true
		}
	}
	return false
}
