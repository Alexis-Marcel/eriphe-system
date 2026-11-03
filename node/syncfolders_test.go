package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const peerDevice = "MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYY-LTMRWAD"

// syncFixture is a node with shared folders, its Syncthing a fake.
func syncFixture(t *testing.T) (*node, *fakeSyncthing, string) {
	t.Helper()
	n, root := shareFixture(t)
	f := newFakeSyncthing(t)
	n.syncNet = newSyncNet(n.opt.root, "127.0.0.1:22100", 0, nil)
	t.Cleanup(n.syncNet.close)
	n.st, n.stID = f.client(), fakeDeviceID
	n.opt.hostRoot = n.opt.root
	return n, f, root
}

// shareCopyable shares rel at a level, copyable or not, and returns its id.
func shareCopyable(t *testing.T, n *node, rel, name, access string, copyable bool) string {
	t.Helper()
	tf := lendAt(t, n, rel, name, access)
	changeShared(n.opt.root, func(l []sharedFolder) ([]sharedFolder, error) {
		l[indexShared(l, tf.ID)].Copyable = copyable
		return l, nil
	})
	return tf.ID
}

func putPlan(t *testing.T, n *node, p syncPlan) map[string]string {
	t.Helper()
	if err := p.check(); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := n.savePlan(p); err != nil {
		t.Fatal(err)
	}
	problems, err := n.applySync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

func sourcePlan(name, share, access string) planFolder {
	return planFolder{ID: "e-mac-" + share, Share: share, Name: name, Source: "mac", Role: roleSource, Access: access,
		Results: "Eriphe", Peers: []string{"nas"}}
}

// On the folder's machine, the folder is sent as its level says: read and
// drop send only (drop's results subfolder, a folder of its own, both
// ways), manage both ways with 30 days of versions out of the folder; the
// peer's device ID is pinned at a loopback port of the node's.
func TestSyncSourceLevels(t *testing.T) {
	n, f, root := syncFixture(t)
	read := shareCopyable(t, n, "Photos", "Photos", accessRead, true)
	drop := shareCopyable(t, n, "Clients/ACME", "ACME", accessDrop, true)
	manage := shareCopyable(t, n, "Clients/Beta", "Beta", accessManage, true)
	peers := []planPeer{{Name: "nas", DeviceID: peerDevice}}
	problems := putPlan(t, n, syncPlan{Peers: peers, Folders: []planFolder{
		sourcePlan("Photos", read, accessRead), sourcePlan("ACME", drop, accessDrop), sourcePlan("Beta", manage, accessManage)}})
	if len(problems) > 0 {
		t.Fatalf("problems %v", problems)
	}
	d, ok := f.devices[peerDevice]
	if !ok || d.Name != "nas" || len(d.Addresses) != 1 || !strings.HasPrefix(d.Addresses[0], "tcp://127.0.0.1:") || d.Introducer || d.AutoAcceptFolders {
		t.Fatalf("device %+v", d)
	}
	for id, want := range map[string]struct{ typ, path string }{
		"e-mac-" + read:                 {"sendonly", filepath.Join(root, "Photos")},
		"e-mac-" + drop:                 {"sendonly", filepath.Join(root, "Clients/ACME")},
		"e-mac-" + drop + resultsSuffix: {"sendreceive", filepath.Join(root, "Clients/ACME/Eriphe")},
		"e-mac-" + manage:               {"sendreceive", filepath.Join(root, "Clients/Beta")},
	} {
		got, ok := f.folders[id]
		if !ok || got.Type != want.typ || got.Path != want.path || len(got.Devices) != 1 || got.Devices[0].DeviceID != peerDevice {
			t.Errorf("%s: %+v, want %+v", id, got, want)
		}
	}
	if ig := f.ignores["e-mac-"+drop]; len(ig) != 1 || ig[0] != "/Eriphe" {
		t.Errorf("drop's ignores %v", ig)
	}
	v := f.folders["e-mac-"+manage].Versioning
	if v.Type != "staggered" || v.Params["maxAge"] != "2592000" || !strings.HasPrefix(v.FSPath, n.syncDir()) {
		t.Errorf("manage's versions %+v", v)
	}
	if f.folders["e-mac-"+read].Versioning.Type != "" || f.folders["e-mac-"+manage].MaxConflicts != -1 {
		t.Error("versions on a read folder, or conflicts not kept")
	}
}

// The machine decides: a folder not copyable here, or no longer shared, is
// not sent, and one asked above its level here is sent at its level.
func TestSyncSourceCeiling(t *testing.T) {
	n, f, _ := syncFixture(t)
	closed := shareCopyable(t, n, "Photos", "Photos", accessManage, false)
	low := shareCopyable(t, n, "Clients/ACME", "ACME", accessRead, true)
	peers := []planPeer{{Name: "nas", DeviceID: peerDevice}}
	problems := putPlan(t, n, syncPlan{Peers: peers, Folders: []planFolder{
		sourcePlan("Photos", closed, accessManage), sourcePlan("ACME", low, accessManage),
		sourcePlan("Gone", "0123456789abcdef", accessRead)}})
	if _, ok := f.folders["e-mac-"+closed]; ok || !strings.Contains(problems["e-mac-"+closed], "not to be copied") {
		t.Errorf("not copyable: sent (%v)", problems)
	}
	if !strings.Contains(problems["e-mac-0123456789abcdef"], "not shared") {
		t.Errorf("not shared: %v", problems)
	}
	if got := f.folders["e-mac-"+low]; got.Type != "sendonly" || got.Versioning.Type != "" {
		t.Errorf("asked manage of a read folder: %+v", got)
	}
	// Unshared here: its Syncthing folder goes at the next application.
	n.unshare(func(sf sharedFolder) bool { return sf.ID == low }, "local")
	n.applySync(context.Background())
	if _, ok := f.folders["e-mac-"+low]; ok {
		t.Error("an unshared folder still synced")
	}
}

// A copy is kept in the node's data, receive only for read, both ways for
// manage; a copy no longer planned is removed from Syncthing and deleted,
// after the tasks working on it are stopped, and the peer's device goes.
func TestSyncCopies(t *testing.T) {
	n, f, _ := syncFixture(t)
	share := "00112233445566aa"
	cp := planFolder{ID: "e-mac-" + share, Share: share, Name: "Photos", Source: "mac", Role: roleCopy, Access: accessRead, Peers: []string{"mac"}}
	plan := syncPlan{Peers: []planPeer{{Name: "mac", DeviceID: peerDevice}}, Folders: []planFolder{cp}}
	putPlan(t, n, plan)
	got := f.folders[cp.ID]
	if got.Type != "receiveonly" || got.Path != n.copyPath(cp.ID) || !strings.HasPrefix(got.Path, n.opt.root) {
		t.Fatalf("copy %+v", got)
	}
	if fi, err := os.Stat(got.Path); err != nil || !fi.IsDir() {
		t.Fatalf("copy folder: %v", err)
	}
	os.WriteFile(filepath.Join(got.Path, "a.jpg"), []byte("x"), 0o644)

	// Lent to a task: as planned, never above its level, only that folder.
	tf := TaskFolder{Name: "Photos", ID: share, Copy: cp.ID}
	task := &Task{ID: "t1", State: stateRunning, Spec: TaskSpec{Folders: []TaskFolder{tf}}}
	if rf, err := n.lentFolder(task, tf); err != nil || rf.real != got.Path || rf.src != got.Path {
		t.Fatalf("lent copy: %+v %v", rf, err)
	}
	for name, bad := range map[string]TaskFolder{
		"above its level": {Name: "Photos", ID: share, Copy: cp.ID, Write: true},
		"another folder":  {Name: "Photos", ID: "ffeeddccbbaa9988", Copy: cp.ID},
		"no such copy":    {Name: "Photos", ID: share, Copy: "e-mac-ffeeddccbbaa9988"},
	} {
		if _, err := n.lentFolder(task, bad); err == nil {
			t.Errorf("%s: lent", name)
		}
	}

	// Paused (a machine of the pair suspended): kept, not synced.
	paused := cp
	paused.Paused, paused.Peers = true, []string{}
	putPlan(t, n, syncPlan{Folders: []planFolder{paused}})
	if got := f.folders[cp.ID]; !got.Paused || len(got.Devices) != 0 {
		t.Fatalf("paused copy %+v", got)
	}
	if _, err := os.Stat(filepath.Join(n.copyPath(cp.ID), "a.jpg")); err != nil {
		t.Fatalf("a paused copy lost its files: %v", err)
	}

	// manage: both ways on the copy too, without versions (the folder's
	// machine keeps them).
	plan.Folders[0].Access = accessManage
	putPlan(t, n, plan)
	if got := f.folders[cp.ID]; got.Type != "sendreceive" || got.Versioning.Type != "" {
		t.Fatalf("manage copy %+v", got)
	}
	// Lent to manage: mounted writable after a snapshot of the copy, taken
	// here, on the machine running the task.
	managed := &Task{ID: "t-m", State: stateRunning, Spec: TaskSpec{Folders: []TaskFolder{{Name: "Photos", ID: share, Copy: cp.ID, Manage: true}}}}
	if args, err := n.folderMounts(managed); err != nil || strings.Contains(strings.Join(args, " "), "readonly") {
		t.Fatalf("managed copy: %v %v", args, err)
	}
	if snaps := n.snapshots(cp.ID); len(snaps) != 1 || snaps[0].Task != "t-m" {
		t.Fatalf("snapshots of the copy: %+v", snaps)
	}

	// Copies off: out of Syncthing, deleted, the task stopped first.
	n.tasks[task.ID] = task
	putPlan(t, n, syncPlan{})
	if _, ok := f.folders[cp.ID]; ok {
		t.Error("the copy is still in Syncthing")
	}
	if _, err := os.Stat(n.copyPath(cp.ID)); !os.IsNotExist(err) {
		t.Errorf("the copy is still there: %v", err)
	}
	if _, ok := f.devices[peerDevice]; ok {
		t.Error("the peer's device is still pinned")
	}
	found := false
	for _, e := range task.events {
		found = found || e.Type == "folder_revoked"
	}
	if !found {
		t.Error("the task on the copy was not stopped")
	}
}

// A plan that names anything out of shape is refused whole.
func TestSyncPlanCheck(t *testing.T) {
	share := "00112233445566aa"
	ok := planFolder{ID: "e-mac-" + share, Share: share, Name: "Photos", Source: "mac", Role: roleCopy, Access: accessRead, Peers: []string{"mac"}}
	peers := []planPeer{{Name: "mac", DeviceID: peerDevice}}
	if err := (syncPlan{Peers: peers, Folders: []planFolder{ok}}).check(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*syncPlan){
		"a path as id":      func(p *syncPlan) { p.Folders[0].ID = "../../etc" },
		"id of another":     func(p *syncPlan) { p.Folders[0].ID = "e-pc-" + share },
		"unknown role":      func(p *syncPlan) { p.Folders[0].Role = "mirror" },
		"unknown level":     func(p *syncPlan) { p.Folders[0].Access = "write" },
		"drop, bad results": func(p *syncPlan) { p.Folders[0].Access, p.Folders[0].Results = accessDrop, "../x" },
		"unknown peer":      func(p *syncPlan) { p.Folders[0].Peers = []string{"pc"} },
		"copy of two": func(p *syncPlan) {
			p.Peers = append(p.Peers, planPeer{"pc", fakeDeviceID})
			p.Folders[0].Peers = []string{"mac", "pc"}
		},
		"bad device":           func(p *syncPlan) { p.Peers[0].DeviceID = "nope" },
		"twice the same id":    func(p *syncPlan) { p.Folders = append(p.Folders, p.Folders[0]) },
		"copy from a stranger": func(p *syncPlan) { p.Folders[0].Peers = nil },
	} {
		p := syncPlan{Peers: append([]planPeer(nil), peers...), Folders: []planFolder{ok}}
		change(&p)
		if err := p.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Through the tunnel.
	n, _, _ := syncFixture(t)
	mux := http.NewServeMux()
	n.syncRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/v1/sync", strings.NewReader(`{"folders":[{"id":"../x"}]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad plan: %d", rec.Code)
	}
}

// The state of each synced folder: a copy's progress and when it was last
// complete; on a Mac, a folder in iCloud Drive whose files are in the cloud
// only gets the hint.
func TestSyncState(t *testing.T) {
	n, f, root := syncFixture(t)
	share := "00112233445566aa"
	cp := planFolder{ID: "e-mac-" + share, Share: share, Name: "Photos", Source: "mac", Role: roleCopy, Access: accessRead, Peers: []string{"mac"}}
	icloud := filepath.Join(root, "Library", "Mobile Documents", "com~apple~CloudDocs", "Notes")
	os.MkdirAll(icloud, 0o755)
	src := shareCopyable(t, n, "Photos", "Photos", accessRead, true)
	changeShared(n.opt.root, func(l []sharedFolder) ([]sharedFolder, error) {
		l[indexShared(l, src)].Path = icloud
		return l, nil
	})
	n.savePlan(syncPlan{Peers: []planPeer{{Name: "mac", DeviceID: peerDevice}, {Name: "nas", DeviceID: fakeDeviceID}},
		Folders: []planFolder{cp, {ID: "e-pc-" + src, Share: src, Name: "Notes", Source: "pc", Role: roleSource, Access: accessRead, Peers: []string{"nas"}}}})
	f.status[cp.ID] = stFolderStatus{State: "syncing", GlobalBytes: 200, NeedBytes: 50, LocalBytes: 150}
	f.status["e-pc-"+src] = stFolderStatus{State: "idle"}
	f.errs["e-pc-"+src] = []stFileError{{Path: "a.txt", Error: "open a.txt: resource deadlock avoided"}}
	f.complete["e-pc-"+src+"/"+fakeDeviceID] = 40
	states := n.syncState(context.Background())
	if len(states) != 2 || states[0].Done != 75 || states[0].Bytes != 150 || !states[0].SyncedAt.IsZero() {
		t.Fatalf("copy state %+v", states)
	}
	if states[1].Done != 40 || states[1].ICloud != (runtime.GOOS == "darwin") || !strings.Contains(states[1].Error, "deadlock") {
		t.Fatalf("source state %+v", states[1])
	}
	f.status[cp.ID] = stFolderStatus{State: "idle", GlobalBytes: 200, LocalBytes: 200}
	if s := n.syncState(context.Background()); s[0].Done != 100 || s[0].SyncedAt.IsZero() {
		t.Fatalf("complete copy %+v", s[0])
	}
}
