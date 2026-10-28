//go:build !windows

package main

import (
	"io/fs"
	"os"
	"syscall"
)

// ownerGID is the group of a file, -1 if unknown.
func ownerGID(p string) int {
	fi, err := os.Stat(p)
	if err != nil {
		return -1
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}

// writableByUser tells whether eriphe-node's own user can write in p.
func writableByUser(p string) bool { return syscall.Access(p, 2) == nil }

// writableByTasks tells whether the tasks' user can write in a directory:
// as its owner, through its group (other than root's, added to the task),
// or as anyone.
func writableByTasks(fi fs.FileInfo) bool {
	perm := fi.Mode().Perm()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	switch {
	case perm&0o002 != 0:
		return true
	case int(st.Uid) == taskUID:
		return perm&0o200 != 0
	case st.Gid != 0:
		return perm&0o020 != 0
	}
	return false
}
