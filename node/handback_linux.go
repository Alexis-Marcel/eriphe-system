package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

// handBackDir gives what a task wrote as root in a folder's Eriphe/ to the
// owner of Eriphe/ (handback.go): every entry owned by root, and any file
// with a setuid or setgid bit, which the change of owner clears (it is
// cleared again if a file system kept it). It walks with file descriptors
// and never follows a symbolic link: a task changing the tree meanwhile
// cannot point the walk out of Eriphe/.
func handBackDir(p string) (int, error) {
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return 0, err
	}
	if st.Uid == 0 {
		unix.Close(fd)
		return 0, errors.New("Eriphe/ belongs to root: no one to give it to")
	}
	budget := maxHandBack
	n, err := handBackFD(fd, int(st.Uid), 0, &budget)
	unix.Close(fd)
	return n, err
}

const (
	maxHandBack      = 200_000 // entries looked at, at most, per hand-back
	maxHandBackDepth = 64
)

func handBackFD(dir, owner, depth int, budget *int) (int, error) {
	if depth > maxHandBackDepth {
		return 0, nil
	}
	names, err := readDirNames(dir)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, name := range names {
		if *budget--; *budget < 0 {
			return changed, errors.New("too many entries")
		}
		var st unix.Stat_t
		if err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue // gone meanwhile
		}
		typ := st.Mode & unix.S_IFMT
		special := typ != unix.S_IFDIR && st.Mode&(unix.S_ISUID|unix.S_ISGID) != 0
		if st.Uid == 0 || special {
			if unix.Fchownat(dir, name, owner, -1, unix.AT_SYMLINK_NOFOLLOW) == nil {
				changed++
			}
			if special && typ == unix.S_IFREG {
				clearSetID(dir, name)
			}
		}
		if typ == unix.S_IFDIR {
			sub, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				continue
			}
			n, err := handBackFD(sub, owner, depth+1, budget)
			unix.Close(sub)
			changed += n
			if err != nil {
				return changed, err
			}
		}
	}
	return changed, nil
}

// clearSetID drops the setuid and setgid bits of a regular file, through a
// descriptor of the file itself (never a link).
func clearSetID(dir int, name string) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return
	}
	if st.Mode&(unix.S_ISUID|unix.S_ISGID) != 0 {
		unix.Fchmod(fd, st.Mode&0o777)
	}
}

func readDirNames(fd int) ([]string, error) {
	// A duplicate, so that reading it to the end leaves fd as it was.
	d, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	defer unix.Close(d)
	if _, err := unix.Seek(d, 0, 0); err != nil {
		return nil, err
	}
	var names []string
	buf := make([]byte, 32<<10)
	for {
		n, err := unix.ReadDirent(d, buf)
		if err != nil {
			return names, err
		}
		if n <= 0 {
			return names, nil
		}
		_, _, names = unix.ParseDirent(buf[:n], -1, names)
	}
}
