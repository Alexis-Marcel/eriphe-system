package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshots: before a task that may change a folder (lent to write) starts,
// the node keeps a copy of the folder as it was, in its own data
// (snapshots/<id>/<time>/files under its root, which no task mounts and no
// folder can share). It is kept -snapshot-days (7 by default), then pruned;
// the user lists and restores them on the machine (eriphe-node share
// snapshots, share restore). A task whose snapshot failed does not start:
// nothing it would write could be undone.
//
// How the copy is made, the cheapest way that stays a copy:
//   - a Mac: Apple's cp -c, a clone (APFS copy-on-write): instant and free
//     until a file changes, the node's data being on the same volume as the
//     user's folders. Apple's cp, not the node itself, as the node never
//     reads a protected place (apple_explore.go). A folder on another volume
//     is copied, within -snapshot-limit.
//   - elsewhere (a NAS, Linux, WSL): each file cloned where the file system
//     can (btrfs, XFS: FICLONE), copied otherwise; what is copied, not
//     cloned, stays within -snapshot-limit, beyond which the snapshot fails.
//
// Never hard links: a task writing a file in place (most editors and agents
// do) would change the snapshot's file with it.

const (
	snapshotsDir         = "snapshots"
	defaultSnapshotDays  = 7
	defaultSnapshotLimit = "2g"
	snapshotStamp        = "20060102-150405"
	snapshotPartial      = ".partial"
)

// snapshotMeta is what a snapshot was taken of, in its meta.json.
type snapshotMeta struct {
	Stamp  string    `json:"-"`      // its name: when it was taken, UTC
	ID     string    `json:"id"`     // the shared folder's id
	Name   string    `json:"name"`   // its name then
	Path   string    `json:"path"`   // its path then
	Time   time.Time `json:"time"`   // when
	Task   string    `json:"task"`   // the task about to change it, or "restore"
	Method string    `json:"method"` // clone or copy
	Copied int64     `json:"copied"` // bytes copied, not cloned
}

// errSnapshotTooBig is a folder whose copy is more than -snapshot-limit.
var errSnapshotTooBig = errors.New("too big to snapshot")

// takeSnapshot copies the folder src to dst, which does not exist yet,
// copying (not cloning) at most limit bytes: how, and what it copied. A
// Mac's (snapshot_darwin.go) uses Apple's cp; tests replace it.
var takeSnapshot = platformSnapshot

// cloneFile clones one file where the file system can (cloneFileOS);
// tests replace it.
var cloneFile = cloneFileOS

// snapshotLent takes the snapshot of a folder lent to a task to write.
func (n *node) snapshotLent(t *Task, f *resolvedFolder) error {
	sf := f.shared
	if sf.Path == "" {
		sf.Path = f.src
	}
	m, err := n.snapshot(sf, f.real, t.ID)
	if err != nil {
		return fmt.Errorf("no snapshot of it could be taken before the task changes it, so the task is not started: %w", err)
	}
	t.emit("folder_snapshot", map[string]any{"folder": sf.Name, "snapshot": m.Stamp,
		"message": fmt.Sprintf("folder %s: a snapshot was taken before the task (%s), kept %d days: eriphe-node share restore on its machine puts it back", sf.Name, m.Stamp, n.snapshotDays())})
	return nil
}

// snapshot copies the folder at real (shared as sf) into the node's data,
// and prunes the old ones.
func (n *node) snapshot(sf sharedFolder, real, task string) (*snapshotMeta, error) {
	dir := filepath.Join(n.opt.root, snapshotsDir, sf.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	stamp := now.Format(snapshotStamp)
	for i := 2; ; i++ {
		_, err1 := os.Lstat(filepath.Join(dir, stamp))
		_, err2 := os.Lstat(filepath.Join(dir, stamp+snapshotPartial))
		if errors.Is(err1, fs.ErrNotExist) && errors.Is(err2, fs.ErrNotExist) {
			break
		}
		stamp = fmt.Sprintf("%s-%d", now.Format(snapshotStamp), i)
	}
	tmp := filepath.Join(dir, stamp+snapshotPartial)
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return nil, err
	}
	method, copied, err := n.copySnapshot(real, tmp, n.snapshotLimit())
	m := &snapshotMeta{Stamp: stamp, ID: sf.ID, Name: sf.Name, Path: sf.Path, Time: now, Task: task, Method: method, Copied: copied}
	if err == nil {
		raw, _ := json.MarshalIndent(m, "", "  ")
		err = os.WriteFile(filepath.Join(tmp, "meta.json"), append(raw, '\n'), 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, stamp))
	}
	if err != nil {
		os.RemoveAll(tmp)
		auditShare(n.opt.root, map[string]any{"event": "snapshot_failed", "id": sf.ID, "name": sf.Name, "task": task, "reason": err.Error()})
		return nil, err
	}
	auditShare(n.opt.root, map[string]any{"event": "snapshot", "id": sf.ID, "name": sf.Name, "task": task, "snapshot": stamp, "method": method, "copied": copied})
	n.pruneSnapshots()
	return m, nil
}

// copySnapshot copies the folder at real into tmp/files: here, or in a
// helper that sees the folder read-only and tmp, the snapshot's new folder
// in the node's data, writable (helper.go): the copy is made there, once.
func (n *node) copySnapshot(real, tmp string, limit int64) (string, int64, error) {
	dst := filepath.Join(tmp, "files")
	if !n.helper() {
		return takeSnapshot(real, dst, limit)
	}
	resp, err := n.helperCall(helperReq{Op: "snapshot", Folder: real, Dst: dst, Limit: limit},
		helperMount{real, true}, helperMount{tmp, false})
	if err != nil {
		return "", 0, err
	}
	return resp.Method, resp.Copied, nil
}

func (n *node) snapshotDays() int {
	if n.opt.snapshotDays > 0 {
		return n.opt.snapshotDays
	}
	return defaultSnapshotDays
}

func (n *node) snapshotLimit() int64 {
	if n.opt.snapshotLimit > 0 {
		return n.opt.snapshotLimit
	}
	l, _ := parseBytes(defaultSnapshotLimit)
	return l
}

// snapshots are a folder's snapshots, by its id, newest first.
func (n *node) snapshots(id string) []snapshotMeta {
	dir := filepath.Join(n.opt.root, snapshotsDir, id)
	entries, _ := os.ReadDir(dir)
	var out []snapshotMeta
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), snapshotPartial) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), "meta.json"))
		var m snapshotMeta
		if err != nil || json.Unmarshal(raw, &m) != nil {
			continue
		}
		m.Stamp = e.Name()
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stamp > out[j].Stamp })
	return out
}

// pruneSnapshots removes the snapshots older than -snapshot-days, and
// those left unfinished for a day.
func (n *node) pruneSnapshots() {
	base := filepath.Join(n.opt.root, snapshotsDir)
	ids, _ := os.ReadDir(base)
	keep := time.Duration(n.snapshotDays()) * 24 * time.Hour
	for _, id := range ids {
		if !id.IsDir() {
			continue
		}
		dir := filepath.Join(base, id.Name())
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if strings.HasSuffix(e.Name(), snapshotPartial) {
				if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
					forceRemove(p)
				}
				continue
			}
			raw, err := os.ReadFile(filepath.Join(p, "meta.json"))
			var m snapshotMeta
			if err != nil || json.Unmarshal(raw, &m) != nil || time.Since(m.Time) <= keep {
				continue
			}
			if err := forceRemove(p); err == nil {
				auditShare(n.opt.root, map[string]any{"event": "snapshot_pruned", "id": m.ID, "name": m.Name, "snapshot": e.Name()})
			}
		}
		os.Remove(dir) // once empty
	}
}

// pruneLoop prunes the snapshots, and the repositories kept from stopped
// tasks (keepwork.go), every so often.
func (n *node) pruneLoop(every time.Duration) {
	n.pruneSnapshots()
	n.pruneKept()
	for range time.Tick(every) {
		n.pruneSnapshots()
		n.pruneKept()
	}
}

// forceRemove removes a tree, its read-only directories made writable
// first (a snapshot keeps the folder's modes).
func forceRemove(p string) error {
	filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(q, 0o700)
		}
		return nil
	})
	return os.RemoveAll(p)
}

// copyTree copies the folder src to dst (made if missing), never following
// a symbolic link: directories, files and links, with their modes, times
// and, where allowed, owners. Each file is cloned where the system can
// (cloneFile), copied otherwise; what is copied stays within limit bytes
// (0: none), beyond which it fails with errSnapshotTooBig.
func copyTree(src, dst string, limit int64) (method string, copied int64, err error) {
	type dirTimes struct {
		path string
		fi   fs.FileInfo
	}
	var dirs []dirTimes
	cloned := 0
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // unreadable: no snapshot
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(to, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirTimes{to, fi})
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, to); err != nil {
				return err
			}
			keepOwner(to, fi)
		case fi.Mode().IsRegular():
			if cloneFile(p, to) == nil {
				cloned++
			} else {
				os.Remove(to)
				if limit > 0 && copied+fi.Size() > limit {
					return fmt.Errorf("%w: more than the %s this machine copies (-snapshot-limit)", errSnapshotTooBig, size(limit))
				}
				if err := copyFile(p, to); err != nil {
					return err
				}
				copied += fi.Size()
			}
			os.Chmod(to, fi.Mode().Perm())
			os.Chtimes(to, fi.ModTime(), fi.ModTime())
			keepOwner(to, fi)
		default:
			// Sockets, pipes, devices: nothing to keep.
		}
		return nil
	})
	if err != nil {
		return "", copied, err
	}
	// Directories last, deepest first: their times and modes, once filled.
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		keepOwner(d.path, d.fi)
		os.Chmod(d.path, d.fi.Mode().Perm())
		os.Chtimes(d.path, d.fi.ModTime(), d.fi.ModTime())
	}
	method = "copy"
	if copied == 0 && cloned > 0 {
		method = "clone"
	}
	return method, copied, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// restoreSnapshot puts a folder back as it was in a snapshot: a snapshot of
// it as it is now is taken first (so that the restore itself can be
// undone), then its content is replaced by the snapshot's. path is the
// folder, as this machine sees it.
func (n *node) restoreSnapshot(m snapshotMeta, path string) (*snapshotMeta, error) {
	files := filepath.Join(n.opt.root, snapshotsDir, m.ID, m.Stamp, "files")
	if fi, err := os.Stat(files); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("snapshot %s is incomplete", m.Stamp)
	}
	if !n.helper() {
		fi, err := os.Stat(path)
		if err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("%s is not a folder of this machine now", path)
		}
	}
	if own := n.ownData(); within(own, path) || within(path, own) {
		return nil, fmt.Errorf("%s is or holds eriphe-node's own data", path)
	}
	before, err := n.snapshot(sharedFolder{ID: m.ID, Name: m.Name, Path: path}, path, "restore")
	if err != nil {
		return nil, fmt.Errorf("no snapshot of the folder as it is now could be taken, so nothing was restored: %w", err)
	}
	if n.helper() {
		if _, err := n.helperCall(helperReq{Op: "restore", Folder: path, Dst: files}, helperMount{path, false}, helperMount{files, true}); err != nil {
			return before, fmt.Errorf("%v: the folder as it was before is snapshot %s", err, before.Stamp)
		}
	} else if err := refill(path, files); err != nil {
		return before, fmt.Errorf("%v: the folder as it was before is snapshot %s", err, before.Stamp)
	}
	auditShare(n.opt.root, map[string]any{"event": "restore", "id": m.ID, "name": m.Name, "path": path, "snapshot": m.Stamp, "before": before.Stamp, "by": "local"})
	return before, nil
}
