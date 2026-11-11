package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

// The copies of shared folders, as eriphe-core sets them up: which peers
// this machine syncs with (their device IDs, handed over by eriphe-core),
// which of its shared folders it sends, and which copies it keeps. The
// node turns that plan into Syncthing's configuration (syncthing.go); it is
// the one that decides what Syncthing may touch:
//
//   - a folder is sent only if it is shared here and the user allowed
//     copies of it when sharing it (copyable), and never above the level it
//     is shared with now: eriphe-core can only restrict;
//   - a copy is kept only in this node's data, <root>/sync/copies/<id>,
//     never inside a shared folder nor anywhere eriphe-core names;
//   - the direction follows the level. read: the folder is sent only, the
//     copies receive only. write: both ways, and on the folder's own machine Syncthing
//     keeps for 30 days what a copy deleted or replaced (staggered
//     versioning, in <root>/sync/versions/<id>, out of the folder);
//     conflicts keep both files (.sync-conflict).
//
// A copy no longer in the plan (copies turned off, the folder unshared, the
// machine taken off) is removed from Syncthing and deleted, once the tasks
// working on it are stopped. The plan is kept in <root>/sync/plan.json, so
// that it holds across a restart while eriphe-core is away.

const (
	syncPlanFile  = "plan.json"
	syncCopiesDir = "copies"
	syncVersions  = "versions"
	// syncVersionDays is how long the folder's machine keeps what a copy
	// deleted or replaced, with the write level.
	syncVersionDays = 30
)

// syncPlan is what eriphe-core asks of this machine (PUT /v1/sync).
type syncPlan struct {
	Peers   []planPeer   `json:"peers"`
	Folders []planFolder `json:"folders"`
}

// planPeer is a machine this one syncs with.
type planPeer struct {
	Name     string `json:"name"`
	DeviceID string `json:"device_id"`
}

// planFolder is one folder synced: sent from here (role source), or a copy
// kept here (role copy).
type planFolder struct {
	ID     string   `json:"id"`     // Syncthing's folder id, the same on every machine: e-<machine>-<share id>
	Share  string   `json:"share"`  // the folder's id on its machine (node/shared.go)
	Name   string   `json:"name"`   // its name, for Syncthing's label and the tasks
	Source string   `json:"source"` // the machine it is shared on
	Role   string   `json:"role"`   // source or copy
	Access string   `json:"access"` // its level: read or write
	Peers  []string `json:"peers"`  // the machines it syncs with, among Peers
	// Paused: a copy kept as it is, not synced, while a machine of the
	// pair is suspended or has no Syncthing for now.
	Paused bool `json:"paused,omitempty"`
}

const (
	roleSource = "source"
	roleCopy   = "copy"
)

var syncFolderID = regexp.MustCompile(`^e-[a-z0-9][a-z0-9-]{0,31}-[0-9a-f]{16}$`)

// check refuses a plan that names anything out of shape; a level of before
// the two (legacyLevel) is read as its new one.
func (p syncPlan) check() error {
	peers := map[string]bool{}
	for _, pe := range p.Peers {
		if !validPeer(pe.Name) || !validDeviceID(pe.DeviceID) || peers[pe.Name] {
			return fmt.Errorf("peer %q: a node name and a device ID, once", pe.Name)
		}
		peers[pe.Name] = true
	}
	ids := map[string]bool{}
	for i := range p.Folders {
		p.Folders[i].Access = legacyLevel(p.Folders[i].Access)
		f := p.Folders[i]
		switch {
		case !syncFolderID.MatchString(f.ID) || ids[f.ID]:
			return fmt.Errorf("folder %q: not a folder id, or twice", f.ID)
		case f.ID != "e-"+f.Source+"-"+f.Share:
			return fmt.Errorf("folder %q: not the id of %s's folder %s", f.ID, f.Source, f.Share)
		case f.Role != roleSource && f.Role != roleCopy:
			return fmt.Errorf("folder %s: role %q", f.ID, f.Role)
		case accessRank[f.Access] == 0:
			return fmt.Errorf("folder %s: level %q", f.ID, f.Access)
		case !validFolderName(f.Name):
			return fmt.Errorf("folder %s: name %q", f.ID, f.Name)
		}
		for _, pe := range f.Peers {
			if !peers[pe] {
				return fmt.Errorf("folder %s: peer %q is not among the peers", f.ID, pe)
			}
		}
		if f.Role == roleCopy && !f.Paused && (len(f.Peers) != 1 || f.Peers[0] != f.Source) {
			return fmt.Errorf("folder %s: a copy syncs with its folder's machine only", f.ID)
		}
		ids[f.ID] = true
	}
	return nil
}

func (n *node) syncDir() string { return filepath.Join(n.opt.root, "sync") }

// copyPath is where this machine keeps a copy.
func (n *node) copyPath(id string) string { return filepath.Join(n.syncDir(), syncCopiesDir, id) }

// loadPlan reads the plan kept; none before the first.
func (n *node) loadPlan() syncPlan {
	var p syncPlan
	raw, err := os.ReadFile(filepath.Join(n.syncDir(), syncPlanFile))
	if err == nil && json.Unmarshal(raw, &p) == nil && p.check() == nil {
		return p
	}
	return syncPlan{}
}

func (n *node) savePlan(p syncPlan) error {
	if err := os.MkdirAll(n.syncDir(), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(p, "", "  ")
	tmp := filepath.Join(n.syncDir(), syncPlanFile+".new")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(n.syncDir(), syncPlanFile))
}

// syncRoutes: eriphe-core sets the plan; the state is in GET /v1/node.
func (n *node) syncRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/sync", func(w http.ResponseWriter, r *http.Request) {
		var p syncPlan
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			http.Error(w, "a plan: {peers, folders}", http.StatusBadRequest)
			return
		}
		if err := p.check(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := n.savePlan(p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		problems, err := n.applySync(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"problems": problems})
	})
}

// applySync makes Syncthing's configuration what the plan and the shared
// folders say, and deletes the copies no longer planned. It returns what
// it left out, and why, by folder id.
func (n *node) applySync(ctx context.Context) (map[string]string, error) {
	n.syncMu.Lock()
	defer n.syncMu.Unlock()
	plan := n.loadPlan()
	problems := map[string]string{}
	n.mu.Lock()
	st := n.st
	n.mu.Unlock()
	// The copies no longer planned go first, Syncthing or not.
	keep := map[string]bool{}
	for _, f := range plan.Folders {
		if f.Role == roleCopy {
			keep[f.ID] = true
		}
	}
	if st == nil {
		n.dropCopies(keep)
		if len(plan.Folders) > 0 {
			return problems, errors.New("no Syncthing on this machine yet")
		}
		return problems, nil
	}
	// Peers: a loopback port each, their device ID pinned.
	var names []string
	for _, pe := range plan.Peers {
		names = append(names, pe.Name)
	}
	ports, err := n.syncNet.setPeers(names)
	if err != nil {
		log.Printf("sync: %v", err)
	}
	ids := map[string]string{}
	wantDev := map[string]bool{}
	for _, pe := range plan.Peers {
		ids[pe.Name] = pe.DeviceID
		port, ok := ports[pe.Name]
		if !ok {
			continue
		}
		wantDev[pe.DeviceID] = true
		d := stDevice{DeviceID: pe.DeviceID, Name: pe.Name, Addresses: []string{fmt.Sprintf("tcp://127.0.0.1:%d", port)},
			Compression: "metadata"}
		if err := st.putDevice(ctx, d); err != nil {
			return problems, err
		}
	}
	// Folders: what the plan and the list of shared folders allow.
	shared, err := loadShared(n.opt.root)
	if err != nil {
		return problems, err
	}
	want := map[string]bool{}
	for _, pf := range plan.Folders {
		folders, ignore, why := n.stFolders(pf, shared, ids)
		if why != "" {
			problems[pf.ID] = why
			continue
		}
		// What Syncthing would fail on, said rather than left to it: the
		// folder this machine sends must open (on a Mac, the node asks
		// macOS here if it never did: Syncthing is its child and gets the
		// same answer), and a copy must be writable before it receives
		// anything (access.go).
		if why := n.syncAccess(pf, shared); why != "" {
			problems[pf.ID] = why
			continue
		}
		for i, f := range folders {
			if f.Path == "" {
				continue
			}
			if pf.Role == roleCopy {
				if err := n.makeCopyDir(f.Path); err != nil {
					problems[pf.ID] = err.Error()
					break
				}
			}
			if err := st.putFolder(ctx, f); err != nil {
				return problems, err
			}
			want[f.ID] = true
			if i == 0 {
				if err := st.setIgnores(ctx, f.ID, ignore); err != nil {
					return problems, err
				}
			}
		}
	}
	// What Syncthing holds that the plan does not: removed. This Syncthing
	// is Eriphe's own; anything else in it is not to be kept, but a folder
	// shared here that may be copied, handed to it when it was shared
	// (registerShared): the plan reuses it once copies are set up.
	have, err := st.folders(ctx)
	if err != nil {
		return problems, err
	}
	own := n.ownSynced(shared)
	for _, f := range have {
		if !want[f.ID] && !own[f.ID] {
			if err := st.deleteFolder(ctx, f.ID); err != nil {
				return problems, err
			}
			log.Printf("sync: folder %s no longer synced here", f.ID)
		}
	}
	devs, err := st.devices(ctx)
	if err != nil {
		return problems, err
	}
	n.mu.Lock()
	self := n.stID
	n.mu.Unlock()
	for _, d := range devs {
		if d.DeviceID != self && !wantDev[d.DeviceID] {
			if err := st.deleteDevice(ctx, d.DeviceID); err != nil {
				return problems, err
			}
		}
	}
	n.dropCopies(keep)
	n.pruneVersions(want)
	return problems, nil
}

// stFolders is a planned folder as Syncthing's folders and the first one's
// ignore patterns (none now), or why it is left out.
func (n *node) stFolders(pf planFolder, shared []sharedFolder, ids map[string]string) ([]stFolder, []string, string) {
	var devices []stFolderDevice
	for _, p := range pf.Peers {
		devices = append(devices, stFolderDevice{DeviceID: ids[p]})
	}
	level := pf.Access
	var path string
	switch pf.Role {
	case roleSource:
		i := indexShared(shared, pf.Share)
		if i < 0 {
			return nil, nil, "not shared on this machine"
		}
		sf := shared[i]
		if !sf.Copyable {
			return nil, nil, "not to be copied: allow it when sharing the folder on this machine"
		}
		if accessRank[sf.Access] < accessRank[level] {
			level = sf.Access // never above its level here
		}
		if level != accessRead {
			if root, _, ok := n.opt.rootOf(sf.Path); ok && n.opt.shares[root].ReadOnly {
				level = accessRead // a read-only place sends only
			}
		}
		path = sf.Path
	case roleCopy:
		path = n.copyPath(pf.ID)
	}
	main := stFolder{ID: pf.ID, Label: pf.Name, Path: path, Devices: devices, RescanIntervalS: 3600, Paused: pf.Paused,
		FSWatcherEnabled: true, FSWatcherDelayS: 10, MaxConflicts: -1,
		// As root on Linux, what it writes takes its folder's owner.
		CopyOwnershipFromParent: runtime.GOOS == "linux" && os.Geteuid() == 0}
	source := pf.Role == roleSource
	switch level {
	case accessRead:
		main.Type = map[bool]string{true: "sendonly", false: "receiveonly"}[source]
		return []stFolder{main}, nil, ""
	default: // write
		main.Type = "sendreceive"
		if source {
			main.Versioning = stVersioning{Type: "staggered", CleanupIntervalS: 3600,
				Params: map[string]string{"maxAge": fmt.Sprint(syncVersionDays * 24 * 3600)},
				FSPath: filepath.Join(n.syncDir(), syncVersions, pf.ID), FSType: "basic"}
		}
		return []stFolder{main}, nil, ""
	}
}

// syncAccess is why a planned folder cannot be synced here now, noted
// (access.go): the folder sent from here does not open, or the copy kept
// here cannot be written. "" when it can.
func (n *node) syncAccess(pf planFolder, shared []sharedFolder) string {
	switch pf.Role {
	case roleSource:
		i := indexShared(shared, pf.Share)
		if i < 0 || n.engine().traits().explores {
			// On a Mac, Syncthing reads the folder itself, with its own
			// permission: what it says of it is reported (syncState).
			return ""
		}
		if note := n.probeShared(shared[i]); note.Problem != "" {
			return "this machine cannot open the folder (" + note.Problem + "): " + note.Error
		}
	case roleCopy:
		p := n.copyPath(pf.ID)
		err := n.makeCopyDir(p)
		if err == nil {
			err = probeWrite(p)
		}
		if n.access.note(pf.ID, err); err != nil {
			return "this machine cannot write the copy: " + err.Error()
		}
	}
	return ""
}

// makeCopyDir makes a copy's folder, the tasks' user's on Linux (what
// Syncthing writes there takes its owner, and tasks lent the copy may
// write as far as their level allows).
func (n *node) makeCopyDir(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o711); err != nil {
		return err
	}
	if err := os.Mkdir(p, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 && !n.engine().traits().mapsOwners {
		return chownResults(p, taskUID, taskGID)
	}
	return nil
}

// dropCopies deletes the copies kept here that are not in keep, once the
// tasks working on them are stopped.
func (n *node) dropCopies(keep map[string]bool) {
	dir := filepath.Join(n.syncDir(), syncCopiesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		id := e.Name()
		if keep[id] || !syncFolderID.MatchString(id) {
			continue
		}
		n.stopCopyTasks(id)
		if err := os.RemoveAll(filepath.Join(dir, id)); err != nil {
			log.Printf("sync: copy %s: %v", id, err)
			continue
		}
		auditShare(n.opt.root, map[string]any{"event": "copy_deleted", "id": id})
		log.Printf("sync: copy %s deleted", id)
	}
}

// stopCopyTasks stops the tasks lent a copy: they keep no mount of it.
func (n *node) stopCopyTasks(id string) {
	n.mu.Lock()
	var hold []*Task
	for _, t := range n.tasks {
		if slices.ContainsFunc(t.Spec.Folders, func(tf TaskFolder) bool { return tf.Copy == id }) {
			hold = append(hold, t)
		}
	}
	n.mu.Unlock()
	for _, t := range hold {
		if finished(t.state()) {
			continue
		}
		t.emit("folder_revoked", map[string]any{"message": "the copy of a folder lent to this task is deleted on its machine: the task is stopped"})
		n.stopContainer(t)
	}
}

// pruneVersions deletes the versions kept for folders no longer synced
// once they are older than syncVersionDays.
func (n *node) pruneVersions(want map[string]bool) {
	dir := filepath.Join(n.syncDir(), syncVersions)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if want[e.Name()] || !syncFolderID.MatchString(e.Name()) {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > syncVersionDays*24*time.Hour {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// lentCopy is a copy lent to a task: kept here, planned with that folder,
// and at most at the copy's level.
func (n *node) lentCopy(t *Task, tf TaskFolder) (*resolvedFolder, error) {
	plan := n.loadPlan()
	i := slices.IndexFunc(plan.Folders, func(f planFolder) bool { return f.ID == tf.Copy && f.Role == roleCopy })
	if i < 0 || plan.Folders[i].Share != tf.ID {
		auditShare(n.opt.root, map[string]any{"event": "refused", "copy": tf.Copy, "task": t.ID, "reason": "no such copy"})
		return nil, shareErr(http.StatusForbidden, "not_shared", "no copy of this folder on this machine")
	}
	pf := plan.Folders[i]
	if accessRank[tf.level()] > accessRank[pf.Access] {
		return nil, shareErr(http.StatusForbidden, "level", "this copy is kept for %s only, not %s", pf.Access, tf.level())
	}
	p := n.copyPath(pf.ID)
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() {
		return nil, shareErr(http.StatusConflict, "missing", "the copy of this folder is not there yet")
	}
	rel := filepath.Join("sync", syncCopiesDir, pf.ID)
	auditShare(n.opt.root, map[string]any{"event": "mount", "copy": pf.ID, "name": pf.Name, "task": t.ID, "access": tf.level()})
	return &resolvedFolder{
		root:   shareRoot{Path: n.copyPath(pf.ID)},
		real:   p,
		src:    filepath.Join(n.opt.hostRoot, rel),
		shared: sharedFolder{ID: pf.ID, Name: pf.Name, Path: p, Access: pf.Access},
	}, nil
}

// syncFolderState is a synced folder's state here, for eriphe-core.
type syncFolderState struct {
	ID     string `json:"id"`
	Share  string `json:"share"`
	Source string `json:"source"`
	Role   string `json:"role"`
	State  string `json:"state"` // idle, scanning, syncing, error… as Syncthing says; "missing" when left out
	// Done is how much of the folder is here, in % (a copy), or the least
	// any copy has (the folder's machine).
	Done      float64 `json:"done"`
	NeedBytes int64   `json:"need_bytes"`
	Bytes     int64   `json:"bytes"` // what it takes here
	Error     string  `json:"error,omitempty"`
	// ICloud: Syncthing cannot read files iCloud Drive (or a cloud's
	// folder) keeps in the cloud only: the user should keep it downloaded.
	ICloud bool `json:"icloud,omitempty"`
	// Problem: why this machine cannot read the folder it sends, or write
	// the copy it keeps (access.go): denied, read_only, missing.
	Problem string `json:"problem,omitempty"`
	// Program: the one the problem is of, eriphe-node or syncthing.
	Program string `json:"program,omitempty"`
	// SyncedAt is when a copy was last seen complete.
	SyncedAt time.Time `json:"synced_at,omitzero"`
}

// syncState is the state of every planned folder, asked of Syncthing.
func (n *node) syncState(ctx context.Context) []syncFolderState {
	n.mu.Lock()
	st := n.st
	n.mu.Unlock()
	plan := n.loadPlan()
	if st == nil || len(plan.Folders) == 0 {
		return nil
	}
	ids := map[string]string{}
	for _, pe := range plan.Peers {
		ids[pe.Name] = pe.DeviceID
	}
	shared, _ := loadShared(n.opt.root)
	var out []syncFolderState
	for _, pf := range plan.Folders {
		s := syncFolderState{ID: pf.ID, Share: pf.Share, Source: pf.Source, Role: pf.Role}
		noteID := pf.ID
		if pf.Role == roleSource {
			noteID = pf.Share
		}
		note, _ := n.access.get(noteID)
		if note.Program == programSyncthing {
			note = accessNote{} // Syncthing's own: said again below
		}
		fs, err := st.folderStatus(ctx, pf.ID)
		if err != nil {
			s.State, s.Error = "missing", "not synced here"
			if note.Problem != "" { // left out of Syncthing for it (syncAccess)
				s.Error, s.Problem, s.Program = note.Error, note.Problem, programNode
			}
			out = append(out, s)
			continue
		}
		if note.Problem != "" {
			s.Problem, s.Program = note.Problem, programNode
		}
		s.State, s.NeedBytes, s.Bytes = fs.State, fs.NeedBytes, fs.LocalBytes
		if fs.Error != "" {
			s.Error = fs.Error
		}
		errs, _ := st.folderErrors(ctx, pf.ID)
		if s.Error == "" && len(errs) > 0 {
			s.Error = fmt.Sprintf("%s: %s", errs[0].Path, errs[0].Error)
		}
		stNote := syncthingNote(fs, errs)
		if s.Problem == "" && stNote.Problem != "" {
			s.Problem, s.Program = stNote.Problem, programSyncthing
		}
		if pf.Role == roleSource {
			n.access.set(pf.ID, stNote) // the shared folder says it too (folderProblem)
		}
		if pf.Role == roleSource && n.inCloudDrive(shared, pf.Share) && slices.ContainsFunc(errs, func(e stFileError) bool {
			return strings.Contains(e.Error, "resource deadlock avoided")
		}) {
			s.ICloud = true
		}
		if pf.Role == roleCopy {
			s.Done = 100
			if fs.GlobalBytes > 0 {
				s.Done = float64(fs.GlobalBytes-fs.NeedBytes) * 100 / float64(fs.GlobalBytes)
			}
			if fs.NeedBytes == 0 && fs.State == "idle" && fs.PullErrors == 0 {
				n.noteSynced(pf.ID, time.Now())
			}
			s.SyncedAt = n.syncedAt(pf.ID)
		} else {
			s.Done = 100
			for _, p := range pf.Peers {
				if c, _, err := st.completion(ctx, pf.ID, ids[p]); err == nil && c < s.Done {
					s.Done = c
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// inCloudDrive tells whether a shared folder of this Mac is in iCloud Drive
// or a cloud's folder, where macOS may keep files in the cloud only.
func (n *node) inCloudDrive(shared []sharedFolder, id string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	i := indexShared(shared, id)
	if i < 0 {
		return false
	}
	p := filepath.ToSlash(shared[i].Path)
	return strings.Contains(p, "/Library/Mobile Documents/") || strings.Contains(p, "/Library/CloudStorage/")
}

// watchSync applies the plan again whenever the list of shared folders
// changes (a folder unshared, lowered or no longer copyable here: its
// Syncthing folder goes at once) or the plan does; and, while a folder is
// left out for what this machine cannot open or write, every syncRetry,
// so that it comes in once the user allows it.
func (n *node) watchSync(every time.Duration) {
	last, retry := "", time.Time{}
	for {
		sig := n.syncSignature()
		if sig != last || (!retry.IsZero() && time.Now().After(retry)) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			if problems, err := n.applySync(ctx); err != nil {
				log.Printf("sync: %v", err)
			} else {
				retry = time.Time{}
				for id, why := range problems {
					if sig != last {
						log.Printf("sync: folder %s left out: %s", id, why)
					}
					retry = time.Now().Add(syncRetry)
				}
				last = sig
			}
			cancel()
		}
		time.Sleep(every)
	}
}

// syncRetry is how often a folder left out is tried again.
const syncRetry = 30 * time.Second

// syncSignature is what applySync depends on: the plan, and each shared
// folder's id, path, level and copyable flag.
func (n *node) syncSignature() string {
	plan, _ := os.ReadFile(filepath.Join(n.syncDir(), syncPlanFile))
	shared, _ := loadShared(n.opt.root)
	var b strings.Builder
	b.Write(plan)
	for _, f := range shared {
		fmt.Fprintf(&b, "\n%s %s %s %v", f.ID, f.Path, f.Access, f.Copyable)
	}
	return b.String()
}

// noteSynced and syncedAt keep when each copy was last complete, in memory
// (a restart starts over: the next state says it again).
func (n *node) noteSynced(id string, t time.Time) {
	n.syncMu.Lock()
	defer n.syncMu.Unlock()
	if n.synced == nil {
		n.synced = map[string]time.Time{}
	}
	n.synced[id] = t
}

func (n *node) syncedAt(id string) time.Time {
	n.syncMu.Lock()
	defer n.syncMu.Unlock()
	return n.synced[id]
}
