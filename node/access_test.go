package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What the file system says becomes one of three problems the workspace
// explains; Syncthing's texts too.
func TestAccessProblem(t *testing.T) {
	for err, want := range map[error]string{
		nil: "",
		&fs.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT}:  problemMissing,
		&fs.PathError{Op: "open", Path: "/x", Err: syscall.EPERM}:   problemDenied,
		&fs.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}:  problemDenied,
		&fs.PathError{Op: "open", Path: "/x", Err: syscall.EROFS}:   problemReadOnly,
		&fs.PathError{Op: "open", Path: "/x", Err: syscall.ENOTDIR}: problemMissing,
		errors.New("something else"):                                "",
	} {
		if got := accessProblem(err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
	for msg, want := range map[string]string{
		"open /Users/sam/Documents/a.txt: operation not permitted": problemDenied,
		"lstat /x: permission denied":                              problemDenied,
		"mkdir /Volumes/Disk/x: read-only file system":             problemReadOnly,
		"folder path missing":                                      problemMissing,
		"hashing: file changed during hashing":                     "",
	} {
		if got := accessProblemText(msg); got != want {
			t.Errorf("%q: %q, want %q", msg, got, want)
		}
	}
}

// skipAsRoot: root opens what a mode forbids.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("modes do not hold back root, nor exist here")
	}
}

// A shared folder the node cannot open, or write when it is lent above
// read, is reported with its problem; one gone, missing.
func TestSharedListProblems(t *testing.T) {
	skipAsRoot(t)
	n, root := shareFixture(t)
	locked := lendAt(t, n, "Clients/ACME", "ACME", accessRead)
	ro := lendAt(t, n, "Clients/Beta", "Beta", accessWrite)
	gone := lendAt(t, n, "Photos", "Photos", accessRead)
	os.Chmod(filepath.Join(root, "Clients", "ACME"), 0)
	os.Chmod(filepath.Join(root, "Clients", "Beta"), 0o555)
	t.Cleanup(func() {
		os.Chmod(filepath.Join(root, "Clients", "ACME"), 0o755)
		os.Chmod(filepath.Join(root, "Clients", "Beta"), 0o755)
	})
	os.Remove(filepath.Join(root, "Photos"))
	got := map[string]sharedInfo{}
	for _, s := range n.sharedList() {
		got[s.ID] = s
	}
	if got[locked.ID].Problem != problemDenied || got[locked.ID].Missing {
		t.Errorf("locked %+v", got[locked.ID])
	}
	if got[ro.ID].Problem != problemReadOnly {
		t.Errorf("read-only %+v", got[ro.ID])
	}
	if got[gone.ID].Problem != problemMissing || !got[gone.ID].Missing {
		t.Errorf("gone %+v", got[gone.ID])
	}
}

// On a Mac the node never opens a folder just to report on it: what it
// last saw is said, and kept across a restart; a folder found blocked is
// tried again, and the problem goes once it opens.
func TestAccessOnAMac(t *testing.T) {
	skipAsRoot(t)
	n, root := shareFixture(t)
	f := lendAt(t, n, "Clients/ACME", "ACME", accessRead)
	n.opt.engine = engineContainer
	dir := filepath.Join(root, "Clients", "ACME")
	os.Chmod(dir, 0)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if p := n.sharedList()[0].Problem; p != "" {
		t.Fatalf("a folder never tried: %q", p)
	}
	list, _ := loadShared(n.opt.root)
	if note := n.probeShared(list[0]); note.Problem != problemDenied || note.Error == "" {
		t.Fatalf("probe %+v", note)
	}
	if p := n.sharedList()[0].Problem; p != problemDenied {
		t.Fatalf("after the probe: %q", p)
	}
	// Kept across a restart.
	if note, ok := newAccessState(n.opt.root).get(f.ID); !ok || note.Problem != problemDenied {
		t.Fatalf("kept %+v", note)
	}
	// Allowed: tried again, it opens.
	os.Chmod(dir, 0o755)
	n.recheckAccess()
	if p := n.sharedList()[0].Problem; p != "" {
		t.Fatalf("after it was allowed: %q", p)
	}
	// Unshared: its note goes.
	n.unshare(func(sharedFolder) bool { return true }, "local")
	n.recheckAccess()
	if _, ok := n.access.get(f.ID); ok {
		t.Error("the note of a folder no longer shared stays")
	}
}

// A copy is received only where this machine can write it: otherwise it is
// left out of Syncthing, said in its state, and tried again; a folder sent
// from here that does not open, the same.
func TestSyncAccess(t *testing.T) {
	skipAsRoot(t)
	n, f, root := syncFixture(t)
	share := "00112233445566aa"
	cp := planFolder{ID: "e-mac-" + share, Share: share, Name: "Photos", Source: "mac", Role: roleCopy, Access: accessRead, Peers: []string{"mac"}}
	src := shareCopyable(t, n, "Clients/ACME", "ACME", accessRead, true)
	sp := planFolder{ID: "e-pc-" + src, Share: src, Name: "ACME", Source: "pc", Role: roleSource, Access: accessRead, Peers: []string{"mac"}}
	copies := filepath.Join(n.syncDir(), syncCopiesDir)
	os.MkdirAll(filepath.Join(copies, cp.ID), 0o755)
	os.Chmod(filepath.Join(copies, cp.ID), 0o555)
	acme := filepath.Join(root, "Clients", "ACME")
	os.Chmod(acme, 0)
	t.Cleanup(func() {
		os.Chmod(filepath.Join(copies, cp.ID), 0o755)
		os.Chmod(acme, 0o755)
	})
	plan := syncPlan{Peers: []planPeer{{Name: "mac", DeviceID: peerDevice}}, Folders: []planFolder{cp, sp}}
	problems := putPlan(t, n, plan)
	if !strings.Contains(problems[cp.ID], "cannot write") || !strings.Contains(problems[sp.ID], "cannot open") {
		t.Fatalf("problems %v", problems)
	}
	if _, ok := f.folders[cp.ID]; ok {
		t.Error("a copy that cannot be written is in Syncthing")
	}
	if _, ok := f.folders[sp.ID]; ok {
		t.Error("a folder that does not open is in Syncthing")
	}
	states := n.syncState(context.Background())
	if len(states) != 2 || states[0].Problem != problemDenied || states[1].Problem != problemDenied {
		t.Fatalf("states %+v", states)
	}
	// Allowed: in at the next try.
	os.Chmod(filepath.Join(copies, cp.ID), 0o755)
	os.Chmod(acme, 0o755)
	if problems := putPlan(t, n, plan); len(problems) != 0 {
		t.Fatalf("problems once allowed %v", problems)
	}
	if _, ok := f.folders[cp.ID]; !ok {
		t.Error("the copy is not in Syncthing once writable")
	}
	if _, err := os.Stat(filepath.Join(copies, cp.ID)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(copies, cp.ID)); len(entries) != 0 {
		t.Errorf("the write check left %v", entries)
	}
	// What Syncthing says of a folder it cannot open is a problem too.
	f.status[sp.ID] = stFolderStatus{State: "error", Error: "folder path missing"}
	if s := n.syncState(context.Background()); s[1].Problem != problemMissing {
		t.Errorf("Syncthing's error: %+v", s[1])
	}
}

// shortRoot is a node root short enough for a Unix socket's path (104
// bytes on a Mac, where t.TempDir is long).
func shortRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "en")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// eriphe-node share asks the running node, through its socket, to open a
// folder of its list, and only that: the node says whether it could.
func TestLocalAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix socket here")
	}
	skipAsRoot(t)
	n, root := shareFixture(t)
	n.opt.root = shortRoot(t)
	n.access = newAccessState(n.opt.root)
	ok := lendAt(t, n, "Clients/Beta", "Beta", accessRead)
	locked := lendAt(t, n, "Clients/ACME", "ACME", accessRead)
	if _, err := askLocalAccess(n.opt.root, ok.ID, time.Second); !errors.Is(err, errNoLocalNode) {
		t.Fatalf("no node: %v", err)
	}
	if err := n.listenLocal(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(n.opt.root, localSocket)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket %v %v", fi, err)
	}
	os.Chmod(filepath.Join(root, "Clients", "ACME"), 0)
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "Clients", "ACME"), 0o755) })
	if r, err := askLocalAccess(n.opt.root, ok.ID, 5*time.Second); err != nil || r.Node.Problem != "" || r.Syncthing != nil {
		t.Fatalf("ok: %+v %v", r, err)
	}
	if r, err := askLocalAccess(n.opt.root, locked.ID, 5*time.Second); err != nil || r.Node.Problem != problemDenied || r.Node.Program != programNode {
		t.Fatalf("locked: %+v %v", r, err)
	}
	if _, err := askLocalAccess(n.opt.root, "ffeeddccbbaa9988", 5*time.Second); err == nil {
		t.Fatal("a folder not shared was opened")
	}
}

// On a Mac, eriphe-node share add says first that macOS may ask, has the
// node open the folder, and if macOS refused, says how to allow it and
// opens System Settings there. The folder stays shared.
func TestShareAsksNode(t *testing.T) {
	n, root := shareFixture(t)
	os.WriteFile(filepath.Join(n.opt.root, "node.json"), []byte("{}"), 0o600)
	shareAsksNode = true
	opened := 0
	answer := localReply{}
	var askErr error
	askNode = func(string, string, time.Duration) (localReply, error) { return answer, askErr }
	was := openPrivacy
	openPrivacy = func() error { opened++; return nil }
	t.Cleanup(func() { shareAsksNode, askNode, openPrivacy = false, askLocalAccess, was })
	add := func(rel string) string {
		var out bytes.Buffer
		if err := n.shareCmd("add", []string{filepath.Join(root, rel)}, &out); err != nil {
			t.Fatalf("add %s: %v\n%s", rel, err, out.String())
		}
		return out.String()
	}
	out := add("Clients/ACME")
	if !strings.HasPrefix(out, "macOS may ask you to allow Eriphe") || opened != 0 {
		t.Fatalf("allowed:\n%s", out)
	}
	answer = localReply{Node: accessNote{Problem: problemDenied, Error: "operation not permitted", Program: programNode}}
	out = add("Clients/Beta")
	if !strings.Contains(out, "System Settings › Privacy & Security › Files and Folders") || opened != 1 {
		t.Fatalf("refused (opened %d):\n%s", opened, out)
	}
	if list, _ := loadShared(n.opt.root); len(list) != 2 {
		t.Fatalf("shared %d", len(list))
	}
	// Copyable: macOS may ask twice; Syncthing refused, it is named.
	answer = localReply{Syncthing: &accessNote{Problem: problemDenied, Error: "operation not permitted", Program: programSyncthing}}
	var buf bytes.Buffer
	if err := n.shareCmd("add", []string{"--copyable", filepath.Join(root, "Home", "Docs")}, &buf); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	if !strings.HasPrefix(out, "macOS may ask up to twice") || !strings.Contains(out, "macOS does not let syncthing open") ||
		!strings.Contains(out, "turn on syncthing") || strings.Contains(out, "turn on eriphe-node") || opened != 2 {
		t.Fatalf("Syncthing refused (opened %d):\n%s", opened, out)
	}
	answer, askErr = localReply{}, errNoLocalNode
	if out = add("Photos"); !strings.Contains(out, "not running") || opened != 2 {
		t.Fatalf("no node:\n%s", out)
	}
}

// A folder shared that may be copied goes to Syncthing at once, with no
// device, and is scanned: what Syncthing saw is said, for Syncthing, on
// the folder too. The plan keeps it before any copy, and reuses it, by
// its id, once copies are set up; not copyable, it goes.
func TestRegisterShared(t *testing.T) {
	n, f, _ := syncFixture(t)
	n.name = "mac"
	id := shareCopyable(t, n, "Clients/ACME", "ACME", accessRead, true)
	plain := shareCopyable(t, n, "Photos", "Photos", accessRead, false)
	list, _ := loadShared(n.opt.root)
	sid := "e-mac-" + id
	f.status[sid] = stFolderStatus{State: "idle"}
	f.errs[sid] = []stFileError{{Path: "a.txt", Error: "open a.txt: operation not permitted"}}
	note := n.registerShared(context.Background(), list[indexShared(list, id)])
	if note == nil || note.Problem != problemDenied || note.Program != programSyncthing {
		t.Fatalf("note %+v", note)
	}
	got, ok := f.folders[sid]
	if !ok || got.Type != "sendonly" || len(got.Devices) != 0 || got.Path != list[indexShared(list, id)].Path {
		t.Fatalf("registered %+v", got)
	}
	if !slices.Contains(f.calls, "POST /rest/db/scan") {
		t.Errorf("no scan: %v", f.calls)
	}
	if n.registerShared(context.Background(), list[indexShared(list, plain)]) != nil {
		t.Error("a folder that may not be copied was handed to Syncthing")
	}
	for _, s := range n.sharedList() {
		if s.ID == id && (s.Problem != problemDenied || s.Program != programSyncthing) {
			t.Errorf("shared %+v", s)
		}
	}
	// No copy planned yet: kept.
	putPlan(t, n, syncPlan{})
	if _, ok := f.folders[sid]; !ok {
		t.Fatal("the registration went before any copy")
	}
	// Allowed: tried again, the problem goes.
	f.errs[sid] = nil
	n.recheckAccess()
	if note, _ := n.access.get(sid); note.Problem != "" {
		t.Fatalf("after it was allowed: %+v", note)
	}
	// Copies set up: the same folder, now with its peer.
	plan := syncPlan{Peers: []planPeer{{Name: "nas", DeviceID: peerDevice}}, Folders: []planFolder{sourcePlan("ACME", id, accessRead)}}
	putPlan(t, n, plan)
	if got := f.folders[sid]; len(got.Devices) != 1 || got.Type != "sendonly" {
		t.Fatalf("reused %+v", got)
	}
	// No longer copyable: out of Syncthing.
	changeShared(n.opt.root, func(l []sharedFolder) ([]sharedFolder, error) {
		l[indexShared(l, id)].Copyable = false
		return l, nil
	})
	putPlan(t, n, syncPlan{})
	if _, ok := f.folders[sid]; ok {
		t.Error("a folder no longer copyable stays in Syncthing")
	}
}

// The workspace's "Allow" opens the pane on a Mac, and nothing elsewhere.
func TestPrivacyOpen(t *testing.T) {
	n := newNode(options{root: t.TempDir()})
	opened := 0
	was := openPrivacy
	t.Cleanup(func() { openPrivacy = was })
	openPrivacy = func() error { opened++; return nil }
	mux := http.NewServeMux()
	n.routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/privacy/open", nil))
	want, wantOpened := http.StatusNotFound, 0
	if runtime.GOOS == "darwin" {
		want, wantOpened = http.StatusNoContent, 1
	}
	if rec.Code != want || opened != wantOpened {
		t.Fatalf("%d, opened %d", rec.Code, opened)
	}
	if !strings.HasPrefix(privacyFilesURL, "x-apple.systempreferences:") || !strings.Contains(privacyFilesURL, "Privacy_FilesAndFolders") {
		t.Error(privacyFilesURL)
	}
}
