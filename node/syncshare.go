package main

import (
	"context"
	"slices"
	"time"
)

// A folder shared here that may be copied goes to this machine's Syncthing
// as soon as it is shared, before any copy is planned: on a Mac, Syncthing
// is a program of its own (its LaunchAgent, Kastelo AB's signature), which
// macOS asks about separately, and it should ask while the user is sharing
// the folder, not when its copies start (access.go, local.go).

// ownSyncID is the Syncthing folder id of a folder shared here, the one
// eriphe-core plans it under (e-<machine>-<share id>); "" while this node
// does not know its name.
func (n *node) ownSyncID(share string) string {
	id := "e-" + n.name + "-" + share
	if n.name == "" || !syncFolderID.MatchString(id) {
		return ""
	}
	return id
}

// ownSynced are the Syncthing ids of the folders shared here that may be
// copied: Syncthing may hold them before any copy is planned.
func (n *node) ownSynced(shared []sharedFolder) map[string]bool {
	out := map[string]bool{}
	for _, f := range shared {
		if id := n.ownSyncID(f.ID); id != "" && f.Copyable {
			out[id] = true
		}
	}
	return out
}

// syncthingNote is what Syncthing says of a folder it cannot read or
// write: its folder's error, or its files'.
func syncthingNote(fs stFolderStatus, errs []stFileError) accessNote {
	n := accessNote{Program: programSyncthing}
	if p := accessProblemText(fs.Error); p != "" {
		n.Problem, n.Error = p, fs.Error
		return n
	}
	for _, e := range errs {
		if p := accessProblemText(e.Error); p != "" {
			n.Problem, n.Error = p, e.Path+": "+e.Error
			return n
		}
	}
	return n
}

// registerShared hands a folder just shared, that may be copied, to this
// machine's Syncthing at once, with no device yet, and has it scanned: on
// a Mac, macOS asks the user then whether Syncthing may open it. It waits
// for the scan (macOS holds it until the user answers) and says what
// Syncthing saw; nil when there is nothing to hand over (no Syncthing here
// yet, or the folder may not be copied). The plan reuses the same folder,
// by its id, once copies are set up (applySync keeps it meanwhile).
func (n *node) registerShared(ctx context.Context, sf sharedFolder) *accessNote {
	n.mu.Lock()
	st := n.st
	n.mu.Unlock()
	id := n.ownSyncID(sf.ID)
	if st == nil || id == "" || !sf.Copyable {
		return nil
	}
	n.syncMu.Lock()
	have, err := st.folders(ctx)
	if err == nil && !slices.ContainsFunc(have, func(f stFolder) bool { return f.ID == id }) {
		err = st.putFolder(ctx, stFolder{ID: id, Label: sf.Name, Path: sf.Path, Type: "sendonly", Devices: []stFolderDevice{},
			RescanIntervalS: 3600, FSWatcherEnabled: true, FSWatcherDelayS: 10, MaxConflicts: -1})
	}
	n.syncMu.Unlock()
	if err != nil {
		note := accessNote{Program: programSyncthing, Error: err.Error()}
		return &note
	}
	note := n.rescanSync(ctx, id)
	return &note
}

// rescanSync has Syncthing scan a folder, waits for the scan to end, and
// notes what it saw.
func (n *node) rescanSync(ctx context.Context, id string) accessNote {
	n.mu.Lock()
	st := n.st
	n.mu.Unlock()
	if st == nil {
		return accessNote{}
	}
	st.scan(ctx, id)
	for {
		fs, err := st.folderStatus(ctx, id)
		if err != nil {
			return accessNote{Program: programSyncthing, Error: err.Error()}
		}
		if fs.State != "scanning" && fs.State != "scan-waiting" {
			errs, _ := st.folderErrors(ctx, id)
			return n.access.set(id, syncthingNote(fs, errs))
		}
		select {
		case <-ctx.Done():
			return accessNote{Program: programSyncthing, Error: "Syncthing is still scanning the folder"}
		case <-time.After(500 * time.Millisecond):
		}
	}
}
