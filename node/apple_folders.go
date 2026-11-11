package main

import (
	"errors"
	"fmt"
	"strings"
)

// Folders with the container engine (apple.go), on a Mac. The checks are
// the shares' (shares.go), made through the explorer (apple_explore.go): the
// node never reads a shared place itself. The Mac's file sharing (virtiofs)
// shows every file of a mounted folder as the task's user's, and what the
// task writes lands as the Mac user's, the one eriphe-node runs as: no group
// to add, no owner to give.
//
// iCloud Drive (and the File Provider clouds: Google Drive for desktop,
// OneDrive, Dropbox) keep files "dataless" on a Mac that optimises its
// storage: their content is only in the cloud. A task cannot have them
// downloaded (a read from the container fails, EDEADLK); a read on the Mac
// does. So before a task starts, the node has every dataless file of its
// folders downloaded (apple_darwin.go), up to -download-limit in all; beyond,
// the task fails with what to do (make the folder "Keep Downloaded" in the
// Finder).

// exploredFolderMounts are the mounts of a task's folders, with an engine
// that explores them (folderMounts): each read-only, or writable whole
// when lent with Write, once a snapshot of it is taken (snapshot.go).
func (n *node) exploredFolderMounts(t *Task) ([]mount, error) {
	var args []mount
	seen := map[string]bool{}
	budget := n.opt.downloadLimit
	for _, tf := range t.Spec.Folders {
		if !validFolderName(tf.Name) || seen[strings.ToLower(tf.Name)] {
			return nil, fmt.Errorf("folder %q: invalid or repeated name", tf.Name)
		}
		if strings.Contains(tf.Name, ",") {
			return nil, fmt.Errorf("folder %q: a name with a comma cannot be mounted on this Mac", tf.Name)
		}
		seen[strings.ToLower(tf.Name)] = true
		f, err := n.lentFolder(t, tf) // checked by the explorer (shared.go)
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		if tf.Write && f.root.ReadOnly {
			return nil, fmt.Errorf("folder %s: its place is read-only on this machine", tf.Name)
		}
		if budget, err = download(f.real, budget, n.opt.downloadLimit); err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		dst := "/work/folders/" + tf.Name
		if tf.Write {
			// After the download: the snapshot holds the files themselves.
			if err := n.snapshotLent(t, f); err != nil {
				return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
			}
			if err := n.engine().mountable(f.real); err != nil {
				return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
			}
			args = append(args, mount{src: f.real, dst: dst})
			continue
		}
		if err := n.engine().mountable(f.real); err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		args = append(args, mount{src: f.real, dst: dst, readOnly: true})
	}
	return args, nil
}

// errTooBig is a folder whose files in the cloud are more than the node
// downloads before a task.
var errTooBig = errors.New("too much to download")

// download has the files of dir kept only in the cloud downloaded while
// their sizes fit in budget, and returns what is left of it.
func download(dir string, budget, limit int64) (int64, error) {
	count, need, err := cloudOnly(dir)
	if err != nil || count == 0 {
		return budget, err
	}
	if need > budget {
		return budget, fmt.Errorf("%w: files of this folder only in the cloud (%d, %s) are more than the %s this machine downloads before a task: "+
			"in the Finder, choose \"Keep Downloaded\" on the folder, or share a smaller one", errTooBig, count, size(need), size(limit))
	}
	if err := fetchCloud(dir); err != nil {
		return budget, fmt.Errorf("cannot download this folder's files from the cloud: %v", err)
	}
	return budget - need, nil
}

// size is a byte count for people.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d kB", (n+1023)/1024)
}
