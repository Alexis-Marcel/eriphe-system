package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// A Mac's snapshots (snapshot.go) are made by Apple's cp, which macOS lets
// into the protected places where it would hold eriphe-node back
// (apple_darwin.go): a clone (cp -c, APFS copy-on-write), or, on another
// volume, a copy within the limit, its size from Apple's du.
func platformSnapshot(src, dst string, limit int64) (string, int64, error) {
	if out, err := exec.Command("/bin/cp", "-c", "-pR", src, dst).CombinedOutput(); err == nil {
		return "clone", 0, nil
	} else if strings.Contains(string(out), "Operation not permitted") {
		return "", 0, fmt.Errorf("macOS does not let this Mac read the folder: %s", tail(strings.TrimSpace(string(out)), 300))
	}
	forceRemove(dst)
	out, err := exec.Command("/usr/bin/du", "-sk", src).Output()
	if err != nil {
		return "", 0, fmt.Errorf("cannot measure the folder: %v", err)
	}
	kb, err := strconv.ParseInt(strings.Fields(string(out) + " x")[0], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("cannot measure the folder: %q", out)
	}
	if limit > 0 && kb*1024 > limit {
		return "", 0, fmt.Errorf("%w: %s on another volume, more than the %s this machine copies (-snapshot-limit)", errSnapshotTooBig, size(kb*1024), size(limit))
	}
	if out, err := exec.Command("/bin/cp", "-pR", src, dst).CombinedOutput(); err != nil {
		forceRemove(dst)
		return "", 0, fmt.Errorf("cannot copy the folder: %s", tail(strings.TrimSpace(string(out)), 300))
	}
	return "copy", kb * 1024, nil
}

// cloneFileOS clones one file (APFS); dst must not exist.
func cloneFileOS(src, dst string) error {
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		return &os.PathError{Op: "clonefile", Path: src, Err: err}
	}
	return nil
}
