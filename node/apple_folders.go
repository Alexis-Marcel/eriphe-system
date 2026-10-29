package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Folders with the container engine (apple.go), on a Mac. The checks are
// the shares' (shares.go), made through the explorer (apple_explore.go): the
// node never reads a shared place itself. The Mac's file sharing (virtiofs)
// shows every file of a mounted folder as the task's user's, and what the
// task writes lands as the Mac user's, the one eriphe-node runs as: no group
// to add, no owner to give, and the results folder is a plain folder of the user's.
//
// iCloud Drive (and the File Provider clouds: Google Drive for desktop,
// OneDrive, Dropbox) keep files "dataless" on a Mac that optimises its
// storage: their content is only in the cloud. A task cannot have them
// downloaded (a read from the container fails, EDEADLK); a read on the Mac
// does. So before a task starts, the node has every dataless file of its
// folders downloaded (apple_darwin.go), up to -download-limit in all; beyond,
// the task fails with what to do (make the folder "Keep Downloaded" in the
// Finder).

// appleFolderMounts are the container run arguments that mount a task's
// folders: each read-only, its results subfolder writable when lent with
// Write.
func (n *node) appleFolderMounts(t *Task) ([]string, error) {
	ctx := context.Background()
	var args []string
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
		f, e, err := n.appleResolve(ctx, tf.Share, tf.Path, "tree")
		if err == nil {
			err = n.holdsOwn(f.real)
		}
		if err == nil {
			err = checkTree(e.tree)
		}
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		if tf.Write && f.root.ReadOnly {
			return nil, fmt.Errorf("folder %s: its root %s is read-only on this machine", tf.Name, tf.Share)
		}
		if budget, err = download(f.real, budget, n.opt.downloadLimit); err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		dst := "/work/folders/" + tf.Name
		mount, err := appleMount(f.real, dst, true)
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		args = append(args, mount...)
		if !tf.Write {
			continue
		}
		use, err := n.appleResults(ctx, f.real, tf)
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		if cur := tf.resultsName(); use != cur {
			resultsConflict(t, tf, use)
		}
		if mount, err = appleMount(filepath.Join(f.real, use), dst+"/"+tf.resultsName(), false); err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		args = append(args, mount...)
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
