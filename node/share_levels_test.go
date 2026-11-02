package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lendAt puts a folder in the list at a level, with its ceiling, and
// returns it as a task asks for it at that level.
func lendAt(t *testing.T, n *node, rel, name, access string) TaskFolder {
	t.Helper()
	tf := lend(t, n, "data", rel, name, false)
	if err := changeShared(n.opt.root, func(l []sharedFolder) ([]sharedFolder, error) {
		l[indexShared(l, tf.ID)].Access = access
		l[indexShared(l, tf.ID)].Max = access
		return l, nil
	}); err != nil {
		t.Fatal(err)
	}
	tf.Write, tf.Manage = access == accessDrop, access == accessManage
	return tf
}

// eriphe-core may lower a folder's level and turn off its copies through
// the tunnel, never raise either.
func TestShareLower(t *testing.T) {
	n, _ := shareFixture(t)
	mux := http.NewServeMux()
	n.routes(mux)
	tf := lendAt(t, n, "Photos", "Photos", accessManage)
	changeShared(n.opt.root, func(l []sharedFolder) ([]sharedFolder, error) { l[0].Copyable = true; return l, nil })
	patch := func(body string) (int, string) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/v1/shares/"+tf.ID, strings.NewReader(body)))
		return rec.Code, rec.Body.String()
	}
	if code, out := patch(`{"access":"drop"}`); code != 200 || !strings.Contains(out, `"access":"drop","max":"manage","copyable":true`) {
		t.Fatalf("lower: %d %s", code, out)
	}
	for body, want := range map[string]int{
		`{"access":"manage"}`: 403, // back up, even to its ceiling: only the machine
		`{"access":"write"}`:  400,
		`not json`:            400,
	} {
		if code, out := patch(body); code != want {
			t.Errorf("%s: %d %s, want %d", body, code, out, want)
		}
	}
	if code, out := patch(`{"copyable":false}`); code != 200 || strings.Contains(out, "copyable") {
		t.Fatalf("copies off: %d %s", code, out)
	}
	if code, out := patch(`{"copyable":true}`); code != 403 {
		t.Fatalf("copies on again: %d %s", code, out)
	}
	if code, _ := patch(`{"access":"read"}`); code != 200 {
		t.Fatal("lower to read")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/v1/shares/nope", strings.NewReader(`{"access":"read"}`)))
	if rec.Code != 404 {
		t.Fatalf("unknown: %d", rec.Code)
	}
	list, _ := loadShared(n.opt.root)
	if list[0].Access != accessRead || list[0].Max != accessManage || list[0].Copyable {
		t.Fatalf("kept %+v", list[0])
	}
	// eriphe-core sees the ceiling, and the level now.
	raw, _ := json.Marshal(n.sharedList())
	if !strings.Contains(string(raw), `"access":"read","max":"manage"`) {
		t.Fatalf("for eriphe-core: %s", raw)
	}
	logged, _ := os.ReadFile(filepath.Join(n.opt.root, sharesLog))
	if strings.Count(string(logged), `"event":"lower"`) != 3 || !strings.Contains(string(logged), `"reason":"raise"`) {
		t.Fatalf("shares.log:\n%s", logged)
	}
	// And a task asking more than the level now is refused.
	if _, err := n.lentFolder(&Task{ID: "t"}, tf); err == nil {
		t.Fatal("lent to manage once read only")
	}
}

// A task holding a folder above its new level is stopped, whoever lowered
// it: eriphe-core, or the command in the file.
func TestRevokeLowered(t *testing.T) {
	n, _ := shareFixture(t)
	n.opt.docker = "true"
	m := lendAt(t, n, "Photos", "Photos", accessManage)
	r := m
	r.Manage = false
	manager := &Task{ID: "manager", State: stateRunning, Spec: TaskSpec{Folders: []TaskFolder{m}}}
	reader := &Task{ID: "reader", State: stateRunning, Spec: TaskSpec{Folders: []TaskFolder{r}}}
	n.tasks[manager.ID], n.tasks[reader.ID] = manager, reader
	known := n.sharedLevels()
	changeShared(n.opt.root, func(l []sharedFolder) ([]sharedFolder, error) { l[0].Access = accessDrop; return l, nil })
	n.compareShared(known)
	stopped := func(t *Task) bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.stopping
	}
	deadline := time.Now().Add(2 * time.Second)
	for !stopped(manager) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !stopped(manager) || stopped(reader) {
		t.Fatalf("manager stopped %v, reader %v", stopped(manager), stopped(reader))
	}
}

// A folder lent to manage is mounted writable whole, once its snapshot is
// taken; without a snapshot, the task does not start.
func TestManageMount(t *testing.T) {
	n, root := shareFixture(t)
	tf := lendAt(t, n, "Clients", "Clients", accessManage)
	task := &Task{ID: "t-1", Spec: TaskSpec{Folders: []TaskFolder{tf}}}
	args, err := n.folderMounts(task)
	if err != nil {
		t.Fatal(err)
	}
	clients := filepath.Join(root, "Clients")
	if got := strings.Join(args, " "); !strings.Contains(got, "--mount type=bind,src="+clients+",dst=/work/folders/Clients") || strings.Contains(got, "readonly") {
		t.Fatalf("mounts: %s", got)
	}
	if len(task.results) != 1 || task.results[0] != clients {
		t.Fatalf("handed back: %v", task.results)
	}
	snaps := n.snapshots(tf.ID)
	if len(snaps) != 1 || snaps[0].Task != "t-1" || snaps[0].Name != "Clients" {
		t.Fatalf("snapshots: %+v", snaps)
	}
	if raw, err := os.ReadFile(filepath.Join(n.opt.root, snapshotsDir, tf.ID, snaps[0].Stamp, "files", "notes.md")); err != nil || string(raw) != "x" {
		t.Fatalf("snapshot content: %q %v", raw, err)
	}
	// A read-only place is never managed.
	n.opt.shares["data"] = shareRoot{Path: root, ReadOnly: true}
	if _, err := n.folderMounts(&Task{ID: "t-ro", Spec: TaskSpec{Folders: []TaskFolder{tf}}}); err == nil {
		t.Fatal("managed on a read-only place")
	}
	n.opt.shares["data"] = shareRoot{Path: root}
	// Fail closed.
	takeSnapshot = func(src, dst string, limit int64) (string, int64, error) { return "", 0, errSnapshotTooBig }
	defer func() { takeSnapshot = platformSnapshot }()
	if _, err := n.folderMounts(&Task{ID: "t-2", Spec: TaskSpec{Folders: []TaskFolder{tf}}}); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("without a snapshot: %v", err)
	}
	if len(n.snapshots(tf.ID)) != 1 {
		t.Fatal("a failed snapshot is listed")
	}
	logged, _ := os.ReadFile(filepath.Join(n.opt.root, sharesLog))
	if !strings.Contains(string(logged), `"event":"snapshot"`) || !strings.Contains(string(logged), `"event":"snapshot_failed"`) || !strings.Contains(string(logged), `"access":"manage"`) {
		t.Fatalf("shares.log:\n%s", logged)
	}
	// A folder lent to drop gets no snapshot.
	d := lendAt(t, n, "Photos", "Photos", accessDrop)
	chownResults = func(string, int, int) error { return nil }
	defer func() { chownResults = os.Lchown }()
	takeSnapshot = platformSnapshot
	if _, err := n.folderMounts(&Task{ID: "t-3", Spec: TaskSpec{Folders: []TaskFolder{d}}}); err != nil {
		t.Fatal(err)
	}
	if len(n.snapshots(d.ID)) != 0 {
		t.Fatal("a snapshot for drop")
	}
}

// A copy keeps the tree, links as links, modes and times; what is copied
// and not cloned stays within the limit.
func TestCopyTree(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(src, "a", "b", "f.txt"), []byte("hello"), 0o640)
	os.WriteFile(filepath.Join(src, "big"), make([]byte, 4096), 0o644)
	os.Symlink("a/b/f.txt", filepath.Join(src, "link"))
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	os.Chtimes(filepath.Join(src, "big"), old, old)
	os.Chmod(filepath.Join(src, "a"), 0o555)
	defer os.Chmod(filepath.Join(src, "a"), 0o755)

	cloneFile = func(string, string) error { return errors.New("no clone") }
	defer func() { cloneFile = cloneFileOS }()
	dst := filepath.Join(t.TempDir(), "dst")
	defer forceRemove(dst)
	method, copied, err := copyTree(src, dst, 1<<20)
	if err != nil || method != "copy" || copied != 4101 {
		t.Fatalf("%s %d %v", method, copied, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dst, "a", "b", "f.txt")); string(raw) != "hello" {
		t.Fatalf("content %q", raw)
	}
	if target, _ := os.Readlink(filepath.Join(dst, "link")); target != "a/b/f.txt" {
		t.Fatalf("link %q", target)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "a", "b", "f.txt")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(dst, "a")); fi.Mode().Perm() != 0o555 {
		t.Fatalf("dir mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(dst, "big")); !fi.ModTime().Equal(old) {
		t.Fatalf("time %v", fi.ModTime())
	}
	forceRemove(dst)
	if _, _, err := copyTree(src, dst, 1000); !errors.Is(err, errSnapshotTooBig) {
		t.Fatalf("over the limit: %v", err)
	}
	// Cloned files count for nothing.
	forceRemove(dst)
	cloneFile = func(s, d string) error { return copyFile(s, d) }
	if method, copied, err := copyTree(src, dst, 1000); err != nil || method != "clone" || copied != 0 {
		t.Fatalf("cloned: %s %d %v", method, copied, err)
	}
}

// Snapshots are listed newest first, restored (after a snapshot of the
// folder as it is now), and pruned after their days.
func TestSnapshotRestorePrune(t *testing.T) {
	n, root := shareFixture(t)
	tf := lendAt(t, n, "Clients", "Clients", accessManage)
	clients := filepath.Join(root, "Clients")
	sf := sharedFolder{ID: tf.ID, Name: "Clients", Path: clients}
	first, err := n.snapshot(sf, clients, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	// The task changes the folder.
	os.WriteFile(filepath.Join(clients, "notes.md"), []byte("changed"), 0o644)
	os.Remove(filepath.Join(clients, "contract.txt"))
	os.WriteFile(filepath.Join(clients, "new.txt"), []byte("new"), 0o644)

	out, err := shareOut(t, n, "snapshots", "clients")
	if err != nil || !strings.Contains(out, first.Stamp) || !strings.Contains(out, "t-1") {
		t.Fatalf("snapshots: %v %s", err, out)
	}
	if out, err = shareOut(t, n, "restore", "Clients", "20000101-000000"); err == nil {
		t.Fatalf("no such snapshot: %s", out)
	}
	if out, err = shareOut(t, n, "restore", "Clients", "latest"); err != nil || !strings.Contains(out, "is back as it was") {
		t.Fatalf("restore: %v %s", err, out)
	}
	if raw, _ := os.ReadFile(filepath.Join(clients, "notes.md")); string(raw) != "x" {
		t.Fatalf("notes.md %q", raw)
	}
	if _, err := os.Stat(filepath.Join(clients, "contract.txt")); err != nil {
		t.Fatal("contract.txt not back")
	}
	if _, err := os.Stat(filepath.Join(clients, "new.txt")); err == nil {
		t.Fatal("new.txt still there")
	}
	snaps := n.snapshots(tf.ID)
	if len(snaps) != 2 || snaps[0].Task != "restore" {
		t.Fatalf("snapshots after restore: %+v", snaps)
	}
	// What the restore replaced is in the snapshot taken before it.
	if raw, _ := os.ReadFile(filepath.Join(n.opt.root, snapshotsDir, tf.ID, snaps[0].Stamp, "files", "new.txt")); string(raw) != "new" {
		t.Fatalf("before restore: %q", raw)
	}
	// A folder no longer shared is still found by its snapshots.
	shareOut(t, n, "remove", "Clients")
	if out, err = shareOut(t, n, "snapshots", "Clients"); err != nil || !strings.Contains(out, first.Stamp) {
		t.Fatalf("unshared: %v %s", err, out)
	}
	// Pruned after their days.
	meta := filepath.Join(n.opt.root, snapshotsDir, tf.ID, first.Stamp, "meta.json")
	raw, _ := os.ReadFile(meta)
	var m snapshotMeta
	json.Unmarshal(raw, &m)
	m.Time = time.Now().Add(-8 * 24 * time.Hour)
	raw, _ = json.Marshal(m)
	os.WriteFile(meta, raw, 0o600)
	partial := filepath.Join(n.opt.root, snapshotsDir, tf.ID, "x"+snapshotPartial)
	os.Mkdir(partial, 0o700)
	os.Chtimes(partial, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))
	n.pruneSnapshots()
	if snaps := n.snapshots(tf.ID); len(snaps) != 1 || snaps[0].Stamp == first.Stamp {
		t.Fatalf("after pruning: %+v", snaps)
	}
	if _, err := os.Stat(partial); err == nil {
		t.Fatal("an unfinished snapshot kept")
	}
	logged, _ := os.ReadFile(filepath.Join(n.opt.root, sharesLog))
	if !strings.Contains(string(logged), `"event":"restore"`) || !strings.Contains(string(logged), `"event":"snapshot_pruned"`) {
		t.Fatalf("shares.log:\n%s", logged)
	}
}

// A restore that cannot first snapshot the folder as it is changes nothing.
func TestRestoreFailsClosed(t *testing.T) {
	n, root := shareFixture(t)
	tf := lendAt(t, n, "Clients", "Clients", accessManage)
	clients := filepath.Join(root, "Clients")
	first, err := n.snapshot(sharedFolder{ID: tf.ID, Name: "Clients", Path: clients}, clients, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(clients, "new.txt"), []byte("new"), 0o644)
	takeSnapshot = func(src, dst string, limit int64) (string, int64, error) { return "", 0, errSnapshotTooBig }
	defer func() { takeSnapshot = platformSnapshot }()
	if _, err := n.restoreSnapshot(*first, clients); err == nil || !strings.Contains(err.Error(), "nothing was restored") {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clients, "new.txt")); err != nil {
		t.Fatal("the folder was changed")
	}
}

// On a Mac, a folder lent to manage is mounted writable whole, once its
// snapshot is taken.
func TestAppleManageMount(t *testing.T) {
	n, root := appleShareFixture(t)
	tf := lendAt(t, n, "Photos", "Photos", accessManage)
	args, err := n.folderMounts(&Task{ID: "t-1", Spec: TaskSpec{Folders: []TaskFolder{tf}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "type=bind,source=" + filepath.Join(root, "Photos") + ",target=/work/folders/Photos"; len(args) != 2 || args[1] != want {
		t.Fatalf("got %v", args)
	}
	if len(n.snapshots(tf.ID)) != 1 {
		t.Fatal("no snapshot")
	}
	takeSnapshot = func(src, dst string, limit int64) (string, int64, error) { return "", 0, errSnapshotTooBig }
	defer func() { takeSnapshot = platformSnapshot }()
	if _, err := n.folderMounts(&Task{ID: "t-2", Spec: TaskSpec{Folders: []TaskFolder{tf}}}); err == nil {
		t.Fatal("mounted without a snapshot")
	}
}

// The right click passes the level and the copies always: --read and
// --copyable=false set them back on a folder shared already.
func TestShareCmdExplicitFlags(t *testing.T) {
	n, root := shareFixture(t)
	p := filepath.Join(root, "Photos")
	if out, err := shareOut(t, n, "add", "--manage", "--copyable", p); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := shareOut(t, n, "add", "--read", "--copyable=false", p); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	list, _ := loadShared(n.opt.root)
	if len(list) != 1 || list[0].Access != accessRead || list[0].Max != accessRead || list[0].Copyable {
		t.Fatalf("%+v", list)
	}
	if out, err := shareOut(t, n, "add", "--read", "--drop", p); err == nil {
		t.Fatalf("--read and --drop: %s", out)
	}
}
