package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// platformSnapshot copies the folder, each file cloned where the file
// system can (snapshot.go).
func platformSnapshot(src, dst string, limit int64) (string, int64, error) {
	return copyTree(src, dst, limit)
}

// cloneFileOS clones one file (FICLONE: btrfs, XFS); dst must not exist.
func cloneFileOS(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}
