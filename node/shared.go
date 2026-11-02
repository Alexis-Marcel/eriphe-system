package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The folders shared on this machine. The user shares a folder here, on the
// machine that holds it (eriphe-node share add, or a right click in the
// Finder or the Explorer), never from eriphe-core: the node keeps the list
// in its own data, shares.json under its root, which neither eriphe-core
// nor a task can reach. eriphe-core sees each folder's name, access and an
// opaque id, never its path; it lends a task a folder by that id, and the
// node mounts only what is in the list. eriphe-core may take a folder off
// the list (restricting is always allowed), never add one. The roots of the
// installation (-share) only bound where a folder may be shared from.
//
// The command and the running node meet in that file: the command changes
// it under a lock, the node reads it at every use and looks at it every
// few seconds, to stop the tasks that hold a folder no longer shared.
// shares.log next to it records, line by line, every folder shared or
// unshared, every mount and every refusal.

const (
	sharesFile = "shares.json"
	sharesLock = "shares.lock"
	sharesLog  = "shares.log"

	accessRead = "read" // read only
	accessDrop = "drop" // read, and drop results in its results subfolder
)

// sharedFolder is one folder shared on this machine.
type sharedFolder struct {
	ID     string    `json:"id"`     // random: what eriphe-core names it by
	Name   string    `json:"name"`   // the name Eriphe knows it by
	Path   string    `json:"path"`   // absolute, resolved, as this node sees it
	Access string    `json:"access"` // read or drop
	Added  time.Time `json:"added"`
}

// loadShared reads the list; none before the first share.
func loadShared(root string) ([]sharedFolder, error) {
	raw, err := os.ReadFile(filepath.Join(root, sharesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return []sharedFolder{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out struct {
		Folders []sharedFolder `json:"folders"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %v", sharesFile, err)
	}
	if out.Folders == nil {
		out.Folders = []sharedFolder{}
	}
	return out.Folders, nil
}

// changeShared changes the list under its lock, and writes it whole.
func changeShared(root string, change func([]sharedFolder) ([]sharedFolder, error)) error {
	unlock, err := lockShared(root)
	if err != nil {
		return err
	}
	defer unlock()
	list, err := loadShared(root)
	if err != nil {
		return err
	}
	if list, err = change(list); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(map[string]any{"folders": list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(root, sharesFile+".new")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, sharesFile))
}

// lockShared takes the list's lock, a file only one writer creates; one
// left by a writer that died is taken over after lockStale.
func lockShared(root string) (func(), error) {
	p := filepath.Join(root, sharesLock)
	for start := time.Now(); ; {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(p) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > lockStale {
			os.Remove(p)
			continue
		}
		if time.Since(start) > 5*time.Second {
			return nil, errors.New("the list of shared folders is locked: try again")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const lockStale = 30 * time.Second

// auditShare appends one line to shares.log: what happened to a shared
// folder, and who asked (here, or eriphe-core).
func auditShare(root string, entry map[string]any) {
	entry["time"] = time.Now().UTC().Format(time.RFC3339)
	raw, _ := json.Marshal(entry)
	f, err := os.OpenFile(filepath.Join(root, sharesLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("shares.log: %v", err)
		return
	}
	defer f.Close()
	f.Write(append(raw, '\n'))
}

func newShareID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// rootOf is the root a path is in, the deepest one (iCloud Drive is in the
// home's ~/Library), and the path relative to it.
func (o options) rootOf(p string) (string, string, bool) {
	best, rel := "", ""
	for name, r := range o.shares {
		if !within(r.Path, p) {
			continue
		}
		if best == "" || len(r.Path) > len(o.shares[best].Path) || (len(r.Path) == len(o.shares[best].Path) && name < best) {
			best = name
			rel, _ = filepath.Rel(r.Path, p)
		}
	}
	if best == "" {
		return "", "", false
	}
	if rel == "." {
		rel = ""
	}
	return best, filepath.ToSlash(rel), true
}

// checkShared runs every check of a folder about to be shared or mounted
// (shares.go): in a root, no "..", nothing hidden, sensitive or private on
// the way nor inside, no symlink out of its root, not the node's own data.
// It returns the folder resolved.
func (n *node) checkShared(path string) (*resolvedFolder, error) {
	if !filepath.IsAbs(path) {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "%s: not an absolute path", path)
	}
	path = filepath.Clean(path)
	share, rel, ok := n.opt.rootOf(path)
	if !ok && !n.apple() {
		// A path through a symlink to a root (a NAS's /mnt/user0 and /mnt/user).
		if real, err := filepath.EvalSymlinks(path); err == nil {
			share, rel, ok = n.opt.rootOf(real)
		}
	}
	if !ok {
		return nil, shareErr(http.StatusForbidden, "outside", "%s is in none of the places this machine shares folders from (%s)", path, n.opt.rootList())
	}
	if n.apple() {
		f, e, err := n.appleResolve(context.Background(), share, rel, "tree")
		if err == nil {
			err = n.holdsOwn(f.real)
		}
		if err == nil {
			err = checkTree(e.tree)
		}
		return f, err
	}
	f, err := n.resolveFolder(share, rel)
	if err == nil {
		err = n.shareable(f)
	}
	return f, err
}

// rootList names the roots, for the user.
func (o options) rootList() string {
	var out []string
	for _, r := range o.shares {
		out = append(out, r.Path)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "none: install again with --share"
	}
	return strings.Join(out, ", ")
}

// lentFolder is the folder a task is lent, checked again: in the list,
// under the path it was shared with (a symlink put there since is refused),
// and with the right to drop results only if shared so.
func (n *node) lentFolder(t *Task, tf TaskFolder) (*resolvedFolder, error) {
	list, err := loadShared(n.opt.root)
	if err != nil {
		return nil, err
	}
	i := indexShared(list, tf.ID)
	if tf.ID == "" || i < 0 {
		auditShare(n.opt.root, map[string]any{"event": "refused", "id": tf.ID, "task": t.ID, "reason": "not shared"})
		return nil, shareErr(http.StatusForbidden, "not_shared", "this folder is not shared on this machine")
	}
	sf := list[i]
	if tf.Write && sf.Access != accessDrop {
		auditShare(n.opt.root, map[string]any{"event": "refused", "id": sf.ID, "name": sf.Name, "task": t.ID, "reason": "read only"})
		return nil, shareErr(http.StatusForbidden, "read_only", "this folder is shared read-only on this machine")
	}
	f, err := n.checkShared(sf.Path)
	if err == nil && filepath.Clean(f.src) != filepath.Clean(sf.Path) {
		err = shareErr(http.StatusForbidden, "moved", "this folder leads elsewhere since it was shared: share it again")
	}
	if err != nil {
		auditShare(n.opt.root, map[string]any{"event": "refused", "id": sf.ID, "name": sf.Name, "task": t.ID, "reason": err.Error()})
		return nil, err
	}
	auditShare(n.opt.root, map[string]any{"event": "mount", "id": sf.ID, "name": sf.Name, "path": sf.Path, "task": t.ID, "write": tf.Write})
	return f, nil
}

func indexShared(list []sharedFolder, id string) int {
	for i, f := range list {
		if f.ID == id {
			return i
		}
	}
	return -1
}

// sharedInfo is a shared folder as eriphe-core sees it: never its path.
type sharedInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Access  string `json:"access"`
	Missing bool   `json:"missing,omitempty"` // not there now (an unplugged disk), or out of the roots
}

// sharedList is the list for eriphe-core.
func (n *node) sharedList() []sharedInfo {
	list, err := loadShared(n.opt.root)
	if err != nil {
		log.Printf("shared folders: %v", err)
	}
	out := []sharedInfo{}
	for _, f := range list {
		_, _, in := n.opt.rootOf(f.Path)
		missing := !in
		if in && !n.apple() { // a Mac's places are never looked at by the node (apple_explore.go)
			fi, err := os.Stat(f.Path)
			missing = err != nil || !fi.IsDir()
		}
		out = append(out, sharedInfo{ID: f.ID, Name: f.Name, Access: f.Access, Missing: missing})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// The node's API for shared folders, through the tunnel: the list, and
// taking one off it. Nothing adds one, nor looks into a folder.
func (n *node) shareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/shares", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"folders": n.sharedList()})
	})
	mux.HandleFunc("DELETE /v1/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		removed, err := n.unshare(func(f sharedFolder) bool { return f.ID == r.PathValue("id") }, "eriphe-core")
		if err != nil {
			writeShareError(w, err)
			return
		}
		if len(removed) == 0 {
			writeShareError(w, shareErr(http.StatusNotFound, "not_found", "no such shared folder"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// unshare takes the folders that match off the list, says so in the log,
// and stops the tasks that hold them.
func (n *node) unshare(match func(sharedFolder) bool, by string) ([]sharedFolder, error) {
	var removed []sharedFolder
	err := changeShared(n.opt.root, func(list []sharedFolder) ([]sharedFolder, error) {
		kept := list[:0]
		for _, f := range list {
			if match(f) {
				removed = append(removed, f)
			} else {
				kept = append(kept, f)
			}
		}
		return kept, nil
	})
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, f := range removed {
		auditShare(n.opt.root, map[string]any{"event": "remove", "id": f.ID, "name": f.Name, "path": f.Path, "by": by})
		ids[f.ID] = true
	}
	n.revoke(ids)
	return removed, nil
}

// revoke stops the tasks that hold a folder no longer shared: they keep no
// mount of it.
func (n *node) revoke(ids map[string]bool) {
	if len(ids) == 0 || n.tasks == nil {
		return
	}
	n.mu.Lock()
	var hold []*Task
	for _, t := range n.tasks {
		for _, tf := range t.Spec.Folders {
			if ids[tf.ID] {
				hold = append(hold, t)
				break
			}
		}
	}
	n.mu.Unlock()
	for _, t := range hold {
		if finished(t.state()) {
			continue
		}
		auditShare(n.opt.root, map[string]any{"event": "stop", "task": t.ID, "reason": "a folder it holds is no longer shared"})
		t.emit("folder_revoked", map[string]any{"message": "a folder lent to this task is no longer shared on its machine: the task is stopped"})
		go n.stopContainer(t)
	}
}

// watchShared looks at the list every few seconds: what the command took
// off it is revoked.
func (n *node) watchShared(every time.Duration) {
	ids := func() map[string]bool {
		list, err := loadShared(n.opt.root)
		if err != nil {
			return nil // unreadable now: nothing to compare
		}
		out := map[string]bool{}
		for _, f := range list {
			out[f.ID] = true
		}
		return out
	}
	known := ids()
	for range time.Tick(every) {
		now := ids()
		if now == nil {
			continue
		}
		gone := map[string]bool{}
		for id := range known {
			if !now[id] {
				gone[id] = true
			}
		}
		n.revoke(gone)
		known = now
	}
}
