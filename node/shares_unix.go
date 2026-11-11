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

// keepOwner gives a snapshot's copy the owner of its original, where the
// node may (root with CHOWN in its container); a link is not followed.
func keepOwner(p string, fi fs.FileInfo) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && (int(st.Uid) != os.Getuid() || int(st.Gid) != os.Getgid()) {
		os.Lchown(p, int(st.Uid), int(st.Gid))
	}
}
