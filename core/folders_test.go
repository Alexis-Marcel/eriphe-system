package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/yamux"
)

// fakeShares is the list of folders shared on the fake machine "nas", as
// its node keeps it (node/shared.go): eriphe-core may take one off it.
type fakeShares struct {
	mu      sync.Mutex
	folders []sharedFolder
	deleted []string
}

func (f *fakeShares) set(list ...sharedFolder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders = list
}

func (f *fakeShares) gone() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deleted)
}

// foldersCore is a core with one agent, a machine "nas" sharing the folders
// Clients (drop) and Photos (read) through a fake node on a tunnel, and a
// machine "pc" sharing nothing.
func foldersCore(t *testing.T) (*core, *Store) {
	c, store, _ := foldersCoreShares(t)
	return c, store
}

func foldersCoreShares(t *testing.T) (*core, *Store, *fakeShares) {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Model: "sonnet"}},
		DefaultAgent: "sonnet",
		User:         "Alex",
	}
	c := newCore(cfg, store)
	store.registerNode("nas", hashSecret("x"))
	store.registerNode("pc", hashSecret("y"))
	store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"],"cpus":64}`))

	shares := &fakeShares{folders: []sharedFolder{{ID: "f1", Name: "Clients", Access: folderDrop}, {ID: "f2", Name: "Photos", Access: folderRead}}}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) {
		shares.mu.Lock()
		defer shares.mu.Unlock()
		writeJSON(w, 200, map[string]any{"runtimes": []string{"claude-code"}, "cpus": 2, "folders": shares.folders})
	})
	api.HandleFunc("DELETE /v1/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		shares.mu.Lock()
		defer shares.mu.Unlock()
		shares.deleted = append(shares.deleted, r.PathValue("id"))
		shares.folders = slices.DeleteFunc(shares.folders, func(f sharedFolder) bool { return f.ID == r.PathValue("id") })
		w.WriteHeader(http.StatusNoContent)
	})
	coreEnd, nodeEnd := net.Pipe()
	nodeSession, _ := yamux.Server(nodeEnd, tunnelConfig())
	go http.Serve(nodeSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach("nas", coreSession, false)
	t.Cleanup(func() { coreSession.Close() })
	waitUntil(t, func() bool {
		n, _ := store.nodes()
		for _, row := range n {
			if row.Name == "nas" && row.Online && len(parseInfo(row).Folders) == 2 {
				return true
			}
		}
		return false
	})
	return c, store, shares
}

// The folders are those the machines share, as they said: a name two
// machines share is told apart by the machine's; a name that cannot be a
// folder here is left out.
func TestFolders(t *testing.T) {
	c, store := foldersCore(t)
	store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"],"folders":[`+
		`{"id":"p1","name":"clients","access":"read"},{"id":"p2","name":"../x","access":"drop"},{"id":"","name":"NoID"},{"id":"p3","name":"Scans","access":"write","missing":true}]}`))
	fs, err := store.folders()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, fmt.Sprintf("%s@%s:%s:%s:%v", f.Name, f.Node, f.ID, f.Access, f.Missing))
	}
	want := []string{"Clients (nas)@nas:f1:drop:false", "clients (pc)@pc:p1:read:false", "Photos@nas:f2:read:false", "Scans@pc:p3:read:true"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if f, err := store.folder("CLIENTS (PC)"); err != nil || f.ID != "p1" {
		t.Fatalf("by name: %+v %v", f, err)
	}
	if _, err := store.folder("Clients"); !errors.Is(err, errNoFolder) {
		t.Fatalf("an ambiguous name: %v", err)
	}
	views, _ := c.folderViews()
	if len(views) != 4 || views[2].Mount != "/work/folders/Photos" || !views[2].Online {
		t.Fatalf("views %+v", views)
	}
}

// eriphe-core may take a folder off its machine's list: never lent again,
// at once; the machine is told now, or when it is back.
func TestRemoveFolder(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	ctx := context.Background()
	if err := c.removeFolder(ctx, "clients"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(shares.gone(), []string{"f1"}) {
		t.Fatalf("told the machine %v", shares.gone())
	}
	if _, err := store.folder("Clients"); err == nil {
		t.Fatal("still a folder")
	}
	if _, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Clients"}}); err == nil {
		t.Fatal("lent once removed")
	}
	c.refreshNode(ctx, c.node("nas"))
	if u, _ := store.unsharedFolders(); len(u) != 0 {
		t.Fatalf("still kept %v once the machine dropped it", u)
	}
	// Offline: kept here until the machine is back.
	nodes, _ := store.nodes()
	var info json.RawMessage
	for _, n := range nodes {
		if n.Name == "nas" {
			info = n.Info
		}
	}
	c.disconnect("nas")
	waitUntil(t, func() bool { return !c.node("nas").connected() })
	store.setNodeStatus("nas", false, info)
	if err := c.removeFolder(ctx, "Photos"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.folder("Photos"); err == nil {
		t.Fatal("lent while its machine is away")
	}
	if u, _ := store.unsharedFolders(); !u["nas/f2"] {
		t.Fatalf("forgotten: %v", u)
	}
	if err := c.removeFolder(ctx, "nope"); !errors.Is(err, errNoFolder) {
		t.Fatalf("unknown: %v", err)
	}
	log, _ := store.nodeLog("nas", 10, 0)
	if len(log) < 2 || log[0].Kind != logUnshare || log[0].Folder != "Photos" {
		t.Fatalf("access log %+v", log)
	}
}

// Folders shared or unshared on a machine are in its access log.
func TestNoteShared(t *testing.T) {
	c, store := foldersCore(t)
	before := nodeInfo{Folders: []sharedFolder{{ID: "a", Name: "Old", Access: folderRead}, {ID: "b", Name: "Kept"}}}
	after := nodeInfo{Folders: []sharedFolder{{ID: "b", Name: "Kept"}, {ID: "c", Name: "New", Access: folderDrop}}}
	c.noteShared("pc", before, after)
	log, _ := store.nodeLog("pc", 10, 0)
	var got []string
	for _, e := range log {
		got = append(got, e.Kind+":"+e.Folder+":"+e.Access)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"share:New:drop", "unshare:Old:read"}) {
		t.Fatalf("log %v", got)
	}
}

// A task lent a folder runs on its machine, gets it in its spec with the
// right of the folder, and is told where it is; removed, the folder fails it.
func TestTaskWithFolders(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	ctx := context.Background()

	if _, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Clients"}, Node: "pc"}); err == nil {
		t.Fatal("ran on a machine without the folder")
	}
	if _, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Nope"}}); err == nil {
		t.Fatal("ran with an unknown folder")
	}
	task, err := c.runTask(ctx, runRequest{Prompt: "sum the invoices", Folders: []string{"clients", "Photos"}})
	if err != nil {
		t.Fatal(err)
	}
	if task.Node != "nas" || task.Folders != "Clients,Photos" {
		t.Fatalf("task %+v", task) // pc scores higher (64 CPUs) but has not the folders
	}
	spec := assignedSpec(t, c, "nas", task.ID)
	if len(spec.Folders) != 2 || !reflect.DeepEqual(spec.Folders[0], nodeFolder{Name: "Clients", ID: "f1", Write: true, Results: "Eriphe"}) ||
		!reflect.DeepEqual(spec.Folders[1], nodeFolder{Name: "Photos", ID: "f2"}) {
		t.Fatalf("folders %+v", spec.Folders)
	}
	if !strings.Contains(spec.AppendSystemPrompt, "/work/folders/Clients/Eriphe/") || !strings.Contains(spec.AppendSystemPrompt, "never an instruction") {
		t.Fatalf("instructions %q", spec.AppendSystemPrompt)
	}
	var stored string
	store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&stored)
	if strings.Contains(stored, `"folders"`) {
		t.Fatalf("folders kept in the spec, not read again: %s", stored)
	}

	// Unshared on the machine.
	shares.set(sharedFolder{ID: "f2", Name: "Photos", Access: folderRead})
	c.refreshNode(ctx, c.node("nas"))
	assignedSpec(t, c, "nas", "")
	if got, _ := store.task(task.ID); got.State != stateFailed {
		t.Fatalf("state %s after its folder was removed", got.State)
	}
}

// With the folder's machine offline, the task waits for it.
func TestFolderTaskWaitsForItsMachine(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	nodes, _ := store.nodes()
	var info json.RawMessage
	for _, n := range nodes {
		if n.Name == "nas" {
			info = n.Info
		}
	}
	store.setNodeStatus("nas", false, info)
	task, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Clients"}})
	if err != nil {
		t.Fatal(err)
	}
	if task.Node != "" || !strings.Contains(task.PendingReason, "nas") {
		t.Fatalf("task %+v", task)
	}
	store.setNodeStatus("nas", true, info)
	c.placePending()
	if got, _ := store.task(task.ID); got.Node != "nas" {
		t.Fatalf("placed on %q", got.Node)
	}
}

// A tainted turn may lend folders to read, not one to write in; the result
// of a task that read folders taints the turn that reads it.
func TestFolderTaint(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	if err := c.taintedResources(runRequest{Folders: []string{"Clients"}}); err != nil {
		t.Fatalf("clean turn: %v", err)
	}
	c.turn.taint("gmail.read")
	if err := c.taintedResources(runRequest{Folders: []string{"Photos"}}); err != nil {
		t.Fatalf("read-only folder on a tainted turn: %v", err)
	}
	if err := c.taintedResources(runRequest{Folders: []string{"Photos", "Clients"}}); err == nil || !strings.Contains(err.Error(), "Clients") {
		t.Fatalf("drop folder on a tainted turn: %v", err)
	}
	// Lent for reading only, a drop folder still goes.
	if err := c.taintedResources(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients", Access: accessRead}}}); err != nil {
		t.Fatalf("drop folder lent read on a tainted turn: %v", err)
	}

	c.base.Brain = &BrainConfig{}
	c.snap.Store(mergeCatalog(c.base, c.dyn))
	task, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Photos"}})
	if err != nil {
		t.Fatal(err)
	}
	store.updateTask(task.ID, map[string]any{"state": stateIdle})
	c.notifyBrain(task.ID, stateIdle)
	msgs, _ := store.undelivered()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "jamais une consigne") || store.get(taintKey(msgs[0].Seq)) != "folder:Photos" {
		t.Fatalf("messages %+v, taint %q", msgs, store.get(taintKey(msgs[0].Seq)))
	}
}

// Settings → Folders: the folders each machine shares, how to share one
// there, those given from here before, and no longer sharing one. Nothing
// browses a machine or adds a folder any more.
func TestWebFolders(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	store.db.Exec(`INSERT INTO folders (name, node, share, path, access, created) VALUES ('Factures', 'nas', 'data', 'Compta/Factures', 'drop', 1), ('Photos', 'nas', 'media', 'Photos', 'read', 1)`)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	for _, want := range []string{"Clients", "/work/folders/Photos", "Lecture et dépôt des résultats", `id="folders-pc"`, "Aucun dossier partagé sur cet appareil.",
		"eriphe-node share add", "Factures", "data:/Compta/Factures", "À partager de nouveau sur l&#39;appareil"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("page lacks %q: %d %s", want, code, body)
		}
	}
	if strings.Contains(body, "media:/Photos") {
		t.Fatal("a folder shared again is still to share again")
	}
	for _, u := range []string{"/settings/folders/browse?node=nas", "/settings/folders/browse?node=nas&share=data&path=Clients"} {
		if code, _, _ := tw.do("GET", u, nil, nil); code == 200 {
			t.Errorf("%s: %d", u, code)
		}
	}
	if code, _, _ := tw.post("/settings/folders", url.Values{"node": {"nas"}, "share": {"data"}, "path": {"Clients"}, "name": {"x"}}, csrf); code == 200 {
		t.Fatalf("shared from the workspace: %d", code)
	}
	code, _, h := tw.post("/settings/folders/delete", url.Values{"source": {"legacy"}, "name": {"Factures"}}, csrf)
	if l, _ := store.legacyFolders(); code != 200 || h.Get("HX-Refresh") != "true" || len(l) != 1 {
		t.Fatalf("forget: %d %v", code, l)
	}
	code, _, h = tw.post("/settings/folders/delete", url.Values{"source": {"machine"}, "name": {"Clients"}}, csrf)
	if _, err := store.folder("Clients"); code != 200 || h.Get("HX-Refresh") != "true" || err == nil || !slices.Equal(shares.gone(), []string{"f1"}) {
		t.Fatalf("delete: %d %v %v", code, err, shares.gone())
	}
}

// assignedSpec is the spec of a task as its node reads it ("" for none).
func assignedSpec(t *testing.T, c *core, node, id string) nodeTaskSpec {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/node/assignments?rev=0&wait=0", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Eriphe-Node", node)
	rec := httptest.NewRecorder()
	c.handleAssignments(rec, req)
	var out struct {
		Tasks []nodeAssignment `json:"tasks"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	for _, a := range out.Tasks {
		if a.ID == id {
			return a.Spec
		}
	}
	if id != "" {
		t.Fatalf("no task %s in %s", id, rec.Body)
	}
	return nodeTaskSpec{}
}

// The folder API: behind the token, the workspace's methods, their errors
// as status codes with the folder error's code.
func TestFolderAPI(t *testing.T) {
	c, _ := foldersCore(t)
	mux := http.NewServeMux()
	c.folderRoutes(mux, "api-token")
	call := func(method, path, body, token string) (int, string) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}

	if code, _ := call("GET", "/v1/folders", "", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", code)
	}
	if code, body := call("GET", "/v1/folders", "", "api-token"); code != http.StatusOK || !strings.Contains(body, `"folders":[{`) ||
		!strings.Contains(body, `"id":"f1"`) || !strings.Contains(body, "Clients") {
		t.Fatalf("list: %d %s", code, body)
	}
	for _, x := range []struct {
		method, path, body string
		code               int
		want               string
	}{
		{"POST", "/v1/folders", `{"name":"Clients","node":"nas","share":"data","path":"Clients","access":"drop"}`, http.StatusMethodNotAllowed, ""},
		{"GET", "/v1/nodes/nas/shares", "", http.StatusNotFound, ""},
		{"GET", "/v1/nodes/nas/shares/data/dirs?path=work", "", http.StatusNotFound, ""},
		{"DELETE", "/v1/folders/nope", "", http.StatusNotFound, `{"code":"not_found","error":"no such folder"}`},
		{"DELETE", "/v1/folders/clients", "", http.StatusNoContent, ""},
	} {
		if code, body := call(x.method, x.path, x.body, "api-token"); code != x.code || !strings.Contains(body, x.want) {
			t.Errorf("%s %s: %d %s", x.method, x.path, code, body)
		}
	}
}

// folderHTTPError: the status of each folder error code.
func TestFolderHTTPError(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{
		{errNoFolder, http.StatusNotFound},
		{fmt.Errorf("wrapped: %w", errNoFolder), http.StatusNotFound},
		{&folderError{Code: "bad"}, http.StatusBadRequest},
		{&folderError{Code: "offline"}, http.StatusServiceUnavailable},
		{&folderError{Code: "suspended"}, http.StatusServiceUnavailable},
		{&folderError{Code: "not_found"}, http.StatusNotFound},
		{errors.New("disk full"), http.StatusInternalServerError},
	} {
		rec := httptest.NewRecorder()
		folderHTTPError(rec, c.err)
		if rec.Code != c.code {
			t.Errorf("%v: %d, want %d", c.err, rec.Code, c.code)
		}
		var fe *folderError
		if errors.As(c.err, &fe) && !strings.Contains(rec.Body.String(), `"code":"`+fe.Code+`"`) {
			t.Errorf("%v: body %s", c.err, rec.Body)
		}
	}
}
