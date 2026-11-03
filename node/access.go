package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Whether this node can open a shared folder, or write where it keeps a
// copy, so that a folder it cannot reach is never silent: GET /v1/node says
// why (sharedInfo.Problem, syncFolderState.Problem), and the workspace
// shows it on Settings → Folders and on the device's card.
//
// On a Mac, Documents, the Desktop, Downloads, iCloud Drive, the clouds'
// folders and removable volumes are protected (TCC): a program opens them
// once the user allows it, and macOS asks the first time it tries. The node
// asks when the folder is shared (eriphe-node share add, through the local
// socket, local.go), while the user is at the terminal; a folder that may
// be copied is also handed to Syncthing then and scanned (syncfolders.go,
// registerShared), so that macOS asks for Syncthing too, its own program
// with its own LaunchAgent, at the same moment. Afterwards the node never
// opens a folder of the Mac just to report on it: what it and Syncthing
// last saw is kept (access.json), and only a folder found blocked is tried
// again, which macOS answers at once once it said no.

// The problems a folder may have, as GET /v1/node names them.
const (
	problemDenied   = "denied"    // the system refuses this node (macOS's privacy, permissions)
	problemReadOnly = "read_only" // it may be read, not written (a read-only volume)
	problemMissing  = "missing"   // not there (an unplugged disk, a folder moved)
)

// accessFile keeps, in the node's root, what the node last saw of each
// folder it tried to open on a Mac.
const accessFile = "access.json"

// The programs that open a folder, as macOS's Files and Folders lists them:
// the one the user must allow when a folder is denied.
const (
	programNode      = "eriphe-node"
	programSyncthing = "syncthing"
)

// accessNote is what the node, or Syncthing (Program), last saw of a
// folder.
type accessNote struct {
	Problem string    `json:"problem,omitempty"`
	Error   string    `json:"error,omitempty"`
	Program string    `json:"program,omitempty"`
	At      time.Time `json:"at"`
}

// accessState is the notes, by shared folder id (or a copy's folder id).
type accessState struct {
	mu    sync.Mutex
	root  string
	notes map[string]accessNote
}

func newAccessState(root string) *accessState {
	a := &accessState{root: root, notes: map[string]accessNote{}}
	if raw, err := os.ReadFile(filepath.Join(root, accessFile)); err == nil {
		json.Unmarshal(raw, &a.notes)
	}
	return a
}

// note records what the node saw of id: err from a probe, nil when it
// opened.
func (a *accessState) note(id string, err error) accessNote {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return a.set(id, accessNote{Problem: accessProblem(err), Error: msg, Program: programNode})
}

// set records a note, kept on disk when it changed.
func (a *accessState) set(id string, n accessNote) accessNote {
	n.At = time.Now().UTC().Truncate(time.Second)
	a.mu.Lock()
	defer a.mu.Unlock()
	old, ok := a.notes[id]
	if !ok || old.Problem != n.Problem {
		if n.Problem != "" {
			log.Printf("folder %s: %s for %s (%s)", id, n.Problem, n.Program, n.Error)
		} else if ok {
			log.Printf("folder %s: open again for %s", id, n.Program)
		}
	}
	a.notes[id] = n
	if !ok || old.Problem != n.Problem || old.Error != n.Error || old.Program != n.Program {
		a.save()
	}
	return n
}

// get is what was last seen of id, if anything.
func (a *accessState) get(id string) (accessNote, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, ok := a.notes[id]
	return n, ok
}

// blocked are the ids last seen with a problem.
func (a *accessState) blocked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, n := range a.notes {
		if n.Problem != "" {
			out = append(out, id)
		}
	}
	return out
}

// forget drops the notes of folders no longer shared nor copied.
func (a *accessState) forget(keep func(id string) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for id := range a.notes {
		if !keep(id) {
			delete(a.notes, id)
			changed = true
		}
	}
	if changed {
		a.save()
	}
}

func (a *accessState) save() {
	raw, _ := json.Marshal(a.notes)
	tmp := filepath.Join(a.root, accessFile+".new")
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		os.Rename(tmp, filepath.Join(a.root, accessFile))
	}
}

// accessProblem names what an error of the file system means for the user:
// "" when there is none.
func accessProblem(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return problemMissing
	case errors.Is(err, syscall.EROFS):
		return problemReadOnly
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return problemDenied
	}
	return accessProblemText(err.Error())
}

// accessProblemText reads the same in an error's text, as Syncthing
// reports it ("open …: operation not permitted").
func accessProblemText(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "operation not permitted"), strings.Contains(m, "permission denied"), strings.Contains(m, "access is denied"):
		return problemDenied
	case strings.Contains(m, "read-only file system"):
		return problemReadOnly
	case strings.Contains(m, "no such file or directory"), strings.Contains(m, "folder path missing"), strings.Contains(m, "folder marker missing"):
		return problemMissing
	}
	return ""
}

// probeRead opens a folder and reads its first entry: on a Mac, what makes
// macOS ask, the first time, whether this program may open it.
func probeRead(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return &fs.PathError{Op: "open", Path: dir, Err: syscall.ENOTDIR}
	}
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// probeWrite writes, then removes, a file of its own in dir.
func probeWrite(dir string) error {
	f, err := os.CreateTemp(dir, ".eriphe-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	err = f.Close()
	if rerr := os.Remove(name); err == nil {
		err = rerr
	}
	return err
}

// probeShared is whether the node opens a shared folder now, noted, with
// the problem if any: on a Mac, the moment macOS asks the user, once.
func (n *node) probeShared(f sharedFolder) accessNote {
	if n.helper() {
		n.helperProbe([]sharedFolder{f})
		note, _ := n.access.get(f.ID)
		return note
	}
	return n.access.note(f.ID, probeRead(f.Path))
}

// folderProblem is a shared folder's problem now, for GET /v1/node, and
// the program that has it: on a Mac, what the node last saw (it never opens
// a folder of the Mac just to report); elsewhere, looked at now. A folder
// lent above read must also be writable. Then what Syncthing last said of
// it, if it may be copied.
func (n *node) folderProblem(f sharedFolder) (problem, program string) {
	if n.engine().traits().explores || n.helper() {
		// What was last seen: the node does not look into it to report.
		if note, _ := n.access.get(f.ID); note.Problem != "" {
			return note.Problem, programNode
		}
	} else if p := accessProblem(probeRead(f.Path)); p != "" {
		return p, programNode
	} else if f.Access != accessRead && !writableByUser(f.Path) {
		return problemReadOnly, programNode
	}
	if id := n.ownSyncID(f.ID); id != "" && f.Copyable {
		if note, _ := n.access.get(id); note.Problem != "" {
			return note.Problem, programSyncthing
		}
	}
	return "", ""
}

// watchAccess tries again, every so often, the folders last seen with a
// problem: once the user allows the node (or plugs the disk back), the
// problem goes. On a Mac a folder macOS refused answers at once, without
// asking again; one never tried is left alone.
func (n *node) watchAccess(every time.Duration) {
	for range time.Tick(every) {
		n.recheckAccess()
	}
}

func (n *node) recheckAccess() {
	shared, err := loadShared(n.opt.root)
	if err != nil {
		return
	}
	plan := n.loadPlan()
	own := n.ownSynced(shared) // the folders handed to Syncthing here
	n.access.forget(func(id string) bool {
		return indexShared(shared, id) >= 0 || planHas(plan, id) || own[id]
	})
	for _, id := range n.access.blocked() {
		if i := indexShared(shared, id); i >= 0 {
			n.probeShared(shared[i])
		} else if own[id] {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			n.rescanSync(ctx, id) // Syncthing tries it again (syncfolders.go)
			cancel()
		}
	}
}

func planHas(p syncPlan, id string) bool {
	for _, f := range p.Folders {
		if f.ID == id {
			return true
		}
	}
	return false
}
