package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// helperCall is one helper the node ran, faked.
type helperCall struct {
	req    helperReq
	mounts []helperMount
}

// fakeHelpers runs the helpers in this process, as `eriphe-node
// folder-helper` would in their containers, checking that what they work
// in is mounted for them; and turns the node to helper mode: it knows no
// place of its own.
func fakeHelpers(t *testing.T, n *node) *[]helperCall {
	t.Helper()
	var calls []helperCall
	saved := runHelper
	t.Cleanup(func() { runHelper = saved })
	runHelper = func(_ *node, req helperReq, mounts []helperMount) (*helperResp, error) {
		calls = append(calls, helperCall{req, mounts})
		for _, p := range slices.Concat([]string{req.Folder, req.Dst}, req.Dirs, req.Folders) {
			if p != "" && !slices.ContainsFunc(mounts, func(m helperMount) bool { return within(m.path, p) }) {
				t.Errorf("helper %s: %s is not mounted for it (%v)", req.Op, p, mounts)
			}
		}
		raw, _ := json.Marshal(req)
		var in bytes.Buffer
		in.Write(raw)
		var out bytes.Buffer
		runFolderHelper(&in, &out)
		var resp helperResp
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}
	n.opt.folderHelper = "example.org/eriphe-node:1"
	n.opt.shares = shareFlag{}
	return &calls
}

func ops(calls []helperCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.req.Op)
	}
	return out
}

// With the one-container install the node sees no shared folder: helpers
// check and prepare what is lent, take the snapshot straight into the
// node's data, and the task gets the folder from the daemon.
func TestHelperFolderMounts(t *testing.T) {
	n, root := shareFixture(t)
	write := lendAt(t, n, "Clients", "Clients", accessWrite)
	photos := lendAt(t, n, "Photos", "Photos", accessRead)
	read := lendAt(t, n, "Home/Docs", "Docs", accessRead)
	calls := fakeHelpers(t, n)

	task := &Task{ID: "t-1", Spec: TaskSpec{Folders: []TaskFolder{write, photos, read}}}
	args, err := n.folderArgs(t, task)
	if err != nil {
		t.Fatal(err)
	}
	clients, photosDir, docs := filepath.Join(root, "Clients"), filepath.Join(root, "Photos"), filepath.Join(root, "Home", "Docs")
	got := strings.Join(args, " ")
	for _, want := range []string{
		"--mount type=bind,src=" + clients + ",dst=/work/folders/Clients ",
		"--mount type=bind,src=" + photosDir + ",dst=/work/folders/Photos,readonly",
		"--mount type=bind,src=" + docs + ",dst=/work/folders/Docs,readonly",
	} {
		if !strings.Contains(got+" ", want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	if want := []string{"prepare", "snapshot", "prepare", "prepare"}; !slices.Equal(ops(*calls), want) {
		t.Fatalf("helpers %v, want %v", ops(*calls), want)
	}
	// The snapshot: the folder read-only, its new place in the node's data writable.
	snap := (*calls)[1]
	if len(snap.mounts) != 2 || snap.mounts[0] != (helperMount{clients, true}) || snap.mounts[1].readOnly || !within(n.opt.root, snap.mounts[1].path) {
		t.Fatalf("snapshot mounts: %v", snap.mounts)
	}
	snaps := n.snapshots(write.ID)
	if len(snaps) != 1 {
		t.Fatalf("snapshots: %+v", snaps)
	}
	if raw, err := os.ReadFile(filepath.Join(n.opt.root, snapshotsDir, write.ID, snaps[0].Stamp, "files", "notes.md")); err != nil || string(raw) != "x" {
		t.Fatalf("snapshot content: %q %v", raw, err)
	}
	// A prepare mounts its folder read-only: it only checks it.
	for _, c := range *calls {
		if c.req.Op == "prepare" && (len(c.mounts) != 1 || !c.mounts[0].readOnly) {
			t.Errorf("prepare %s: %v", c.req.Folder, c.mounts)
		}
	}
	if _, err := os.Stat(filepath.Join(photosDir, "Eriphe")); err == nil {
		t.Fatal("a results subfolder made")
	}
	// The handback runs in one helper, the folder written to mounted writable.
	*calls = nil
	n.handBack(task)
	if len(*calls) != 1 || (*calls)[0].req.Op != "handback" || len((*calls)[0].mounts) != 1 || (*calls)[0].mounts[0] != (helperMount{clients, false}) {
		t.Fatalf("handback: %+v", *calls)
	}

	// Checked again at each lend: a folder that leads elsewhere now is refused.
	os.Rename(docs, docs+"-old")
	os.Symlink(clients, docs)
	if _, err := n.folderArgs(t, &Task{ID: "t-2", Spec: TaskSpec{Folders: []TaskFolder{read}}}); err == nil {
		t.Fatal("lent where it leads now")
	}
	// A folder that cannot be shared is refused by the helper's checks.
	os.Remove(docs)
	os.Symlink(filepath.Join(root, "Home", ".ssh"), docs)
	if _, err := n.folderArgs(t, &Task{ID: "t-3", Spec: TaskSpec{Folders: []TaskFolder{read}}}); err == nil {
		t.Fatal("lent a folder of keys")
	}
}

// A restore runs in a helper with the folder writable and the snapshot's
// files read-only; the snapshot of the folder as it was, in another.
func TestHelperRestore(t *testing.T) {
	n, root := shareFixture(t)
	tf := lendAt(t, n, "Clients", "Clients", accessWrite)
	clients := filepath.Join(root, "Clients")
	calls := fakeHelpers(t, n)
	first, err := n.snapshot(sharedFolder{ID: tf.ID, Name: "Clients", Path: clients}, clients, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(clients, "new.txt"), []byte("new"), 0o644)
	*calls = nil
	if _, err := n.restoreSnapshot(*first, clients); err != nil {
		t.Fatal(err)
	}
	if want := []string{"snapshot", "restore"}; !slices.Equal(ops(*calls), want) {
		t.Fatalf("helpers %v, want %v", ops(*calls), want)
	}
	if m := (*calls)[1].mounts; len(m) != 2 || m[0] != (helperMount{clients, false}) || !m[1].readOnly {
		t.Fatalf("restore mounts: %v", m)
	}
	if _, err := os.Stat(filepath.Join(clients, "new.txt")); err == nil {
		t.Fatal("not restored")
	}
	if raw, _ := os.ReadFile(filepath.Join(clients, "notes.md")); string(raw) != "x" {
		t.Fatal("not restored")
	}
}

// Probes run in one helper for all the folders, one a folder if that fails.
func TestHelperProbe(t *testing.T) {
	n, root := shareFixture(t)
	a := lendAt(t, n, "Clients", "Clients", accessRead)
	b := lendAt(t, n, "Photos", "Photos", accessRead)
	calls := fakeHelpers(t, n)
	list, _ := loadShared(n.opt.root)
	n.helperProbe(list)
	if len(*calls) != 1 {
		t.Fatalf("helpers: %v", ops(*calls))
	}
	for _, id := range []string{a.ID, b.ID} {
		if note, _ := n.access.get(id); note.Problem != "" || note.At.IsZero() {
			t.Errorf("%s: %+v", id, note)
		}
	}
	// A folder gone fails the helper (its mount): each is probed alone.
	os.RemoveAll(filepath.Join(root, "Photos"))
	runFake := runHelper
	runHelper = func(n *node, req helperReq, mounts []helperMount) (*helperResp, error) {
		for _, m := range mounts {
			if _, err := os.Stat(m.path); err != nil {
				return nil, errors.New("exit status 125: invalid mount config: bind source path does not exist: " + m.path + ": no such file or directory")
			}
		}
		return runFake(n, req, mounts)
	}
	*calls = nil
	n.helperProbe(list)
	if note, _ := n.access.get(b.ID); note.Problem != problemMissing {
		t.Errorf("gone: %+v", note)
	}
	if note, _ := n.access.get(a.ID); note.Problem != "" {
		t.Errorf("there: %+v", note)
	}
	if p, _ := n.folderProblem(list[indexShared(list, b.ID)]); p != problemMissing {
		t.Errorf("reported: %q", p)
	}
}

// The proxy's refusals read as the node's own.
func TestHelperCallErrors(t *testing.T) {
	n, _ := shareFixture(t)
	fakeHelpers(t, n)
	for msg, code := range map[string]string{
		"Error response from daemon: bind source /mnt/user/X is outside the allowed places": "outside",
		"Error response from daemon: bind source /mnt/user/X cannot be resolved":            "not_found",
	} {
		runHelper = func(*node, helperReq, []helperMount) (*helperResp, error) { return nil, errors.New(msg) }
		var se *shareError
		if _, err := n.helperCheck("/mnt/user/X"); !errors.As(err, &se) || se.Code != code {
			t.Errorf("%s: %v", msg, err)
		}
	}
	runHelper = func(*node, helperReq, []helperMount) (*helperResp, error) {
		return &helperResp{Refused: &shareError{Code: "not_found", Msg: "no such folder"}}, nil
	}
	var se *shareError
	if _, err := n.helperCheck("/mnt/user/X"); !errors.As(err, &se) || se.status != 404 {
		t.Errorf("refusal: %v", err)
	}
}

// eriphe-node folder-helper answers one JSON object, and fails on what it
// does not know.
func TestRunFolderHelper(t *testing.T) {
	dir := t.TempDir()
	for in, rc := range map[string]int{
		`{"op":"check","folder":"` + dir + `"}`: 0,
		`{"op":"check","folder":"relative"}`:    1,
		`{"op":"rm"}`:                           1,
		`not json`:                              1,
	} {
		var out bytes.Buffer
		if got := runFolderHelper(strings.NewReader(in), &out); got != rc {
			t.Errorf("%s: %d (%s)", in, got, out.String())
		}
		var resp helperResp
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Errorf("%s: %q", in, out.String())
		}
	}
}
