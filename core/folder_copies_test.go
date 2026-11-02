package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	macDevice = "AAAAAAA-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH"
	nasDevice = "MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYY-LTMRWAD"
)

// syncNode is a fake node with Syncthing: what it reports (GET /v1/node)
// and the plans it was handed (PUT /v1/sync).
type syncNode struct {
	mu    sync.Mutex
	info  map[string]any
	plans []nodePlan
}

func (s *syncNode) set(k string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info[k] = v
}

func (s *syncNode) last() (nodePlan, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.plans) == 0 {
		return nodePlan{}, 0
	}
	return s.plans[len(s.plans)-1], len(s.plans)
}

func attachFakeSync(t *testing.T, c *core, name string, info map[string]any) *syncNode {
	t.Helper()
	sn := &syncNode{info: info}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) {
		sn.mu.Lock()
		defer sn.mu.Unlock()
		writeJSON(w, 200, sn.info)
	})
	api.HandleFunc("PUT /v1/sync", func(w http.ResponseWriter, r *http.Request) {
		var p nodePlan
		json.NewDecoder(r.Body).Decode(&p)
		sn.mu.Lock()
		sn.plans = append(sn.plans, p)
		sn.mu.Unlock()
		writeJSON(w, 200, map[string]any{"problems": map[string]string{}})
	})
	coreEnd, nodeEnd := net.Pipe()
	nodeSession, _ := yamux.Server(nodeEnd, tunnelConfig())
	go http.Serve(nodeSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach(name, coreSession, false)
	t.Cleanup(func() { coreSession.Close() })
	waitUntil(t, func() bool {
		rows, _ := c.store.nodes()
		for _, r := range rows {
			if r.Name == name && r.Online && parseInfo(r).Syncthing != nil {
				return true
			}
		}
		return false
	})
	return sn
}

// copiesCore: a Mac sharing Docs (drop, copyable) and Private (manage, not
// copyable), a NAS, both with Syncthing, and a PC without.
func copiesCore(t *testing.T) (*core, *syncNode, *syncNode) {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{User: "Sam"}, store)
	for _, n := range []string{"mac", "nas", "pc"} {
		store.registerNode(n, hashSecret(n))
	}
	store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	mac := attachFakeSync(t, c, "mac", map[string]any{"runtimes": []string{"claude-code"},
		"folders": []sharedFolder{{ID: "00112233445566aa", Name: "Docs", Access: folderDrop, Copyable: true},
			{ID: "00112233445566bb", Name: "Private", Access: folderManage}},
		"syncthing": map[string]any{"device_id": macDevice}})
	nas := attachFakeSync(t, c, "nas", map[string]any{"runtimes": []string{"claude-code"},
		"syncthing": map[string]any{"device_id": nasDevice}})
	return c, mac, nas
}

// The user picks the machines that keep a copy of a copyable folder: each
// node is handed its plan, the folder's machine sending it at its level,
// the copy's keeping it, each pinning the other's device ID; a plan is
// handed again only when it changes.
func TestFolderCopiesPlan(t *testing.T) {
	c, mac, nas := copiesCore(t)
	ctx := context.Background()
	if err := c.setCopies(ctx, "Docs", []string{"nas"}); err != nil {
		t.Fatal(err)
	}
	mp, _ := mac.last()
	if len(mp.Peers) != 1 || mp.Peers[0] != (planPeer{"nas", nasDevice}) || len(mp.Folders) != 1 {
		t.Fatalf("mac's plan %+v", mp)
	}
	if f := mp.Folders[0]; f.ID != "e-mac-00112233445566aa" || f.Role != "source" || f.Access != folderDrop || f.Results != "Eriphe" ||
		len(f.Peers) != 1 || f.Peers[0] != "nas" {
		t.Fatalf("mac's folder %+v", f)
	}
	np, n := nas.last()
	if len(np.Peers) != 1 || np.Peers[0] != (planPeer{"mac", macDevice}) || len(np.Folders) != 1 || np.Folders[0].Role != "copy" || np.Folders[0].Peers[0] != "mac" {
		t.Fatalf("nas's plan %+v", np)
	}
	if !c.store.syncPaired("mac", "nas") || c.store.syncPaired("mac", "pc") {
		t.Fatal("pairs")
	}
	c.syncAll(ctx)
	if _, again := nas.last(); again != n {
		t.Error("an unchanged plan handed again")
	}

	for name, try := range map[string]func() error{
		"not copyable":      func() error { return c.setCopies(ctx, "Private", []string{"nas"}) },
		"without Syncthing": func() error { return c.setCopies(ctx, "Docs", []string{"pc"}) },
		"its own machine":   func() error { return c.setCopies(ctx, "Docs", []string{"mac"}) },
		"unknown machine":   func() error { return c.setCopies(ctx, "Docs", []string{"tv"}) },
		"unknown folder":    func() error { return c.setCopies(ctx, "Nope", nil) },
	} {
		if err := try(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if fcs, _ := c.store.copies(); len(fcs) != 1 {
		t.Fatalf("copies %+v", fcs)
	}

	// The Mac suspended: the copy is kept, paused, not deleted.
	c.store.db.Exec(`UPDATE nodes SET suspended = 1 WHERE name = 'mac'`)
	c.syncAll(ctx)
	if np, _ := nas.last(); len(np.Folders) != 1 || !np.Folders[0].Paused || len(np.Peers) != 0 {
		t.Fatalf("nas, the Mac suspended: %+v", np)
	}
	c.store.db.Exec(`UPDATE nodes SET suspended = 0 WHERE name = 'mac'`)
	c.syncAll(ctx)
	if np, _ := nas.last(); len(np.Folders) != 1 || np.Folders[0].Paused {
		t.Fatalf("nas, the Mac back: %+v", np)
	}

	// Unticked: the copy machine's plan no longer has it (it deletes it).
	if err := c.setCopies(ctx, "Docs", nil); err != nil {
		t.Fatal(err)
	}
	if np, _ := nas.last(); len(np.Folders) != 0 || len(np.Peers) != 0 {
		t.Fatalf("nas keeps %+v", np)
	}
	if c.store.syncPaired("mac", "nas") {
		t.Fatal("still paired")
	}
}

// Copies off, unshared, or a machine forgotten: the copies go everywhere.
func TestFolderCopiesGo(t *testing.T) {
	c, mac, nas := copiesCore(t)
	ctx := context.Background()
	for name, undo := range map[string]func(){
		"copies off on its machine": func() {
			mac.set("folders", []sharedFolder{{ID: "00112233445566aa", Name: "Docs", Access: folderDrop}})
			c.refreshNode(ctx, c.node("mac"))
			c.syncAll(ctx)
		},
		"unshared from here": func() { c.removeFolder(ctx, "Docs") },
		"forgotten machine":  func() { c.store.forgetMachineCopies("nas"); c.syncAll(ctx) },
	} {
		mac.set("folders", []sharedFolder{{ID: "00112233445566aa", Name: "Docs", Access: folderDrop, Copyable: true}})
		c.store.db.Exec(`DELETE FROM folders_unshared`)
		c.refreshNode(ctx, c.node("mac"))
		if err := c.setCopies(ctx, "Docs", []string{"nas"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		undo()
		if fcs, _ := c.store.copies(); len(fcs) != 0 {
			t.Errorf("%s: copies kept %+v", name, fcs)
		}
		if np, _ := nas.last(); len(np.Folders) != 0 {
			t.Errorf("%s: nas keeps %+v", name, np)
		}
	}
}

// A copy's state, as Settings → Folders shows it; and while the folder's
// machine is away, a task lent the folder goes to a machine whose copy was
// complete when it was last seen, which mounts the copy.
func TestFolderCopyStatusAndPlacement(t *testing.T) {
	c, mac, nas := copiesCore(t)
	ctx := context.Background()
	c.setCopies(ctx, "Docs", []string{"nas"})
	state := func(s syncState) {
		s.ID, s.Share, s.Source, s.Role = "e-mac-00112233445566aa", "00112233445566aa", "mac", "copy"
		nas.set("syncthing", map[string]any{"device_id": nasDevice, "folders": []syncState{s}})
		c.refreshNode(ctx, c.node("nas"))
	}
	view := func() folderView {
		vs, _ := c.folderViews()
		for _, v := range vs {
			if v.Name == "Docs" {
				return v
			}
		}
		t.Fatal("no Docs")
		return folderView{}
	}
	state(syncState{State: "syncing", Done: 42, NeedBytes: 10, Bytes: 100})
	if v := view(); len(v.Copies) != 1 || v.Copies[0].Status != "syncing" || v.Copies[0].Done != 42 || v.Copies[0].Bytes != 100 {
		t.Fatalf("syncing %+v", v.Copies)
	}
	state(syncState{State: "idle", Done: 100, Bytes: 120, SyncedAt: time.Now()})
	if v := view(); v.Copies[0].Status != "ok" {
		t.Fatalf("up to date %+v", v.Copies)
	}
	state(syncState{State: "idle", Error: "disk full"})
	if v := view(); v.Copies[0].Status != "error" || v.Copies[0].Error != "disk full" {
		t.Fatalf("error %+v", v.Copies)
	}
	// The Mac says its iCloud files are in the cloud only: the hint.
	mac.set("syncthing", map[string]any{"device_id": macDevice, "folders": []syncState{{ID: "e-mac-00112233445566aa", Role: "source", ICloud: true}}})
	c.refreshNode(ctx, c.node("mac"))
	if !view().ICloud {
		t.Fatal("no iCloud hint")
	}

	// The Mac goes away, its copy complete just before.
	state(syncState{State: "idle", Done: 100, SyncedAt: time.Now()})
	c.node("mac").session.Close()
	waitUntil(t, func() bool { return !c.node("mac").connected() })
	c.refreshNode(ctx, c.node("mac"))
	if host := c.copyHost([]*Folder{view().Folder}); host != "nas" {
		t.Fatalf("copy host %q", host)
	}
	if v := view(); v.Copies[0].Status != "ok" {
		t.Fatalf("complete before the Mac left: %+v", v.Copies)
	}
	p := c.taskPlacement(Agent{Runtime: "claude-code"}, []*Folder{view().Folder})
	if p.Machine != "nas" || len(p.Folders) != 0 {
		t.Fatalf("placement %+v", p)
	}
	fs, err := c.specFoldersOn("nas", "Docs", []ResourceRef{{Kind: resFolder, Ref: "Docs", Access: accessDrop}})
	if err != nil || len(fs) != 1 || fs[0].Copy != "e-mac-00112233445566aa" || !fs[0].Write {
		t.Fatalf("spec on the copy %+v %v", fs, err)
	}
	if _, err := c.specFoldersOn("pc", "Docs", nil); err == nil {
		t.Fatal("lent on a machine without a copy")
	}
	// Last complete long before the Mac left: behind, no task there.
	state(syncState{State: "idle", Done: 100, SyncedAt: time.Now().Add(-time.Hour)})
	if v := view(); v.Copies[0].Status != "behind" {
		t.Fatalf("behind %+v", v.Copies)
	}
	if host := c.copyHost([]*Folder{view().Folder}); host != "" {
		t.Fatalf("a copy behind hosts a task: %q", host)
	}
}

// The workspace's copies control, and the API's.
func TestWebFolderCopies(t *testing.T) {
	c, _, nas := copiesCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	for _, want := range []string{`hx-post="/settings/folders/copies"`, `name="node" value="nas"`, `value="pc"  disabled`, "à mettre à jour"} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	code, _, h := tw.post("/settings/folders/copies", url.Values{"name": {"Docs"}, "node": {"nas"}}, csrf)
	if code != 200 || h.Get("HX-Refresh") != "true" {
		t.Fatalf("copies: %d", code)
	}
	if np, _ := nas.last(); len(np.Folders) != 1 {
		t.Fatalf("nas's plan %+v", np)
	}
	if _, body, _ = tw.do("GET", "/settings/folders", nil, nil); !strings.Contains(body, "en attente") || !strings.Contains(body, `value="nas" checked`) {
		t.Fatal("the copy is not shown")
	}
	if _, _, h = tw.post("/settings/folders/copies", url.Values{"name": {"Private"}, "node": {"nas"}}, csrf); h.Get("HX-Refresh") == "true" {
		t.Fatal("a folder not copyable copied")
	}

	mux := http.NewServeMux()
	c.folderRoutes(mux, "api-token")
	call := func(name, body string) (int, string) {
		r := httptest.NewRequest("PUT", "/v1/folders/"+name+"/copies", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer api-token")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	if code, out := call("Docs", `{"nodes":[]}`); code != 200 || strings.Contains(out, `"copies"`) {
		t.Fatalf("API: %d %s", code, out)
	}
	for body, want := range map[string]int{`nope`: 400, `{"nodes":["pc"]}`: 409} {
		if code, out := call("Docs", body); code != want {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	if code, _ := call("Private", `{"nodes":["nas"]}`); code == 200 {
		t.Error("API: a folder not copyable copied")
	}
}
