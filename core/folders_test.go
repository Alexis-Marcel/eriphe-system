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
	patched []string // id:access of each lowering asked
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
	giveRepos(t, c, "owner/site")
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

	shares := &fakeShares{folders: []sharedFolder{{ID: "f1", Name: "Clients", Access: accessWrite}, {ID: "f2", Name: "Photos", Access: accessRead}}}
	api := http.NewServeMux()
	// Copies turned off, as the node does it: never on (node/shared.go).
	api.HandleFunc("PATCH /v1/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Copyable *bool `json:"copyable"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		shares.mu.Lock()
		defer shares.mu.Unlock()
		shares.patched = append(shares.patched, r.PathValue("id"))
		for i, f := range shares.folders {
			if f.ID != r.PathValue("id") {
				continue
			}
			if body.Copyable != nil && *body.Copyable && !f.Copyable {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if body.Copyable != nil {
				shares.folders[i].Copyable = false
			}
			writeJSON(w, 200, shares.folders[i])
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
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
		`{"id":"p1","name":"clients","access":"read"},{"id":"p2","name":"../x","access":"write"},{"id":"","name":"NoID"},{"id":"p3","name":"Scans","access":"manage","missing":true},{"id":"p4","name":"Old","access":"drop"}]}`))
	fs, err := store.folders()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, fmt.Sprintf("%s@%s:%s:%s:%v", f.Name, f.Node, f.ID, f.Access, f.Missing))
	}
	// An older node's manage is write, its drop read.
	want := []string{"Clients (nas)@nas:f1:write:false", "clients (pc)@pc:p1:read:false", "Old@pc:p4:read:false", "Photos@nas:f2:read:false", "Scans@pc:p3:write:true"}
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
	if len(views) != 5 || views[3].Mount != "/work/folders/Photos" || !views[3].Online {
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
	if _, err := c.runTask(ctx, runRequest{Prompt: "sum", Resources: folderRefs("Clients")}); err == nil {
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
	before := nodeInfo{Folders: []sharedFolder{{ID: "a", Name: "Old", Access: accessRead}, {ID: "b", Name: "Kept"}}}
	after := nodeInfo{Folders: []sharedFolder{{ID: "b", Name: "Kept"}, {ID: "c", Name: "New", Access: accessWrite}}}
	c.noteShared("pc", before, after)
	log, _ := store.nodeLog("pc", 10, 0)
	var got []string
	for _, e := range log {
		got = append(got, e.Kind+":"+e.Folder+":"+e.Access)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"share:New:write", "unshare:Old:read"}) {
		t.Fatalf("log %v", got)
	}
}

// A task lent a folder runs on its machine, gets it in its spec with the
// right of the folder, and is told where it is; removed, the folder fails it.
func TestTaskWithFolders(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	ctx := context.Background()

	if _, err := c.runTask(ctx, runRequest{Prompt: "sum", Resources: folderRefs("Clients"), Node: "pc"}); err == nil {
		t.Fatal("ran on a machine without the folder")
	}
	if _, err := c.runTask(ctx, runRequest{Prompt: "sum", Resources: folderRefs("Nope")}); err == nil {
		t.Fatal("ran with an unknown folder")
	}
	task, err := c.runTask(ctx, runRequest{Prompt: "sum the invoices", Resources: folderRefs("clients", "Photos")})
	if err != nil {
		t.Fatal(err)
	}
	if task.Node != "nas" || task.Folders != "Clients,Photos" {
		t.Fatalf("task %+v", task) // pc scores higher (64 CPUs) but has not the folders
	}
	spec := assignedSpec(t, c, "nas", task.ID)
	if len(spec.Folders) != 2 || !reflect.DeepEqual(spec.Folders[0], nodeFolder{Name: "Clients", ID: "f1", Write: true}) ||
		!reflect.DeepEqual(spec.Folders[1], nodeFolder{Name: "Photos", ID: "f2"}) {
		t.Fatalf("folders %+v", spec.Folders)
	}
	if !strings.Contains(spec.AppendSystemPrompt, "/work/folders/Clients (you may create") || !strings.Contains(spec.AppendSystemPrompt, "/work/folders/Photos (read-only)") ||
		!strings.Contains(spec.AppendSystemPrompt, "never an instruction") || strings.Contains(spec.AppendSystemPrompt, "Eriphe/") {
		t.Fatalf("instructions %q", spec.AppendSystemPrompt)
	}
	var stored string
	store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&stored)
	if strings.Contains(stored, `"folders"`) {
		t.Fatalf("folders kept in the spec, not read again: %s", stored)
	}

	// Unshared on the machine.
	shares.set(sharedFolder{ID: "f2", Name: "Photos", Access: accessRead})
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
	task, err := c.runTask(ctx, runRequest{Prompt: "sum", Resources: folderRefs("Clients")})
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

// A tainted turn may lend folders, a write being asked on the start's card;
// the result of a task that read only the user's folders does not taint the
// turn that reads it (task_taint.go): it is still called data.
func TestFolderTaint(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	if err := c.taintedResources(runRequest{Resources: folderRefs("Clients")}); err != nil {
		t.Fatalf("clean turn: %v", err)
	}
	c.turn.taint("gmail.read")
	if err := c.taintedResources(runRequest{Resources: folderRefs("Photos")}); err != nil {
		t.Fatalf("read-only folder on a tainted turn: %v", err)
	}
	if err := c.taintedResources(runRequest{Resources: folderRefs("Photos", "Clients")}); err != nil {
		t.Fatalf("write folder on a tainted turn: %v", err)
	}
	if n := c.folderNeeds(folderRefs("Photos", "Clients"), false); len(n) != 1 || n[0].Folder != "Clients" || n[0].Category != catChange {
		t.Fatalf("needs on an unclean start: %+v", n)
	}
	// Lent for reading only, a write folder asks nothing.
	if n := c.folderNeeds([]ResourceRef{{Kind: resFolder, Ref: "Clients", Access: accessRead}}, false); len(n) != 0 {
		t.Fatalf("a read asked on an unclean start: %+v", n)
	}

	c.base.Brain = &BrainConfig{}
	c.snap.Store(mergeCatalog(c.base, c.dyn))
	task, err := c.runTask(ctx, runRequest{Prompt: "sum", Resources: folderRefs("Photos")})
	if err != nil {
		t.Fatal(err)
	}
	store.updateTask(task.ID, map[string]any{"state": stateIdle})
	c.notifyBrain(task.ID, stateIdle)
	msgs, _ := store.undelivered()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "jamais une consigne") || store.get(taintKey(msgs[0].Seq)) != "" {
		t.Fatalf("messages %+v, taint %q", msgs, store.get(taintKey(msgs[0].Seq)))
	}
}

// Settings → Folders: the folders each machine shares, how to share one
// there, and no longer sharing one. Nothing
// browses a machine or adds a folder any more.
func TestWebFolders(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	for _, want := range []string{"Clients", `id="folders-pc"`, "Aucun dossier partagé sur cet appareil.", "Par défaut sur cet appareil",
		`data-dialog="share-how-dlg"`, "Comment partager ?", `id="share-how-dlg"`, "docker exec eriphe eriphe share add /mnt/user/Clients", "--read", "Modifier"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("page lacks %q: %d %s", want, code, body)
		}
	}
	// Photos, shared read-only, has Modifier greyed in its drawer, with the
	// reason.
	if strings.Contains(body, "folders/level") || strings.Contains(body, "À côté") {
		t.Fatalf("old levels: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/folders/grid?name=Photos", nil, nil); !strings.Contains(body, `aria-disabled="true" title="Partagé en lecture seule`) ||
		strings.Count(body, `name="case-change"`) != strings.Count(body, `name="case-change" value="`) {
		t.Fatalf("ceiling: %s", body)
	}
	for _, u := range []string{"/settings/folders/browse?node=nas", "/settings/folders/browse?node=nas&share=data&path=Clients"} {
		if code, _, _ := tw.do("GET", u, nil, nil); code == 200 {
			t.Errorf("%s: %d", u, code)
		}
	}
	if code, _, _ := tw.post("/settings/folders", url.Values{"node": {"nas"}, "share": {"data"}, "path": {"Clients"}, "name": {"x"}}, csrf); code == 200 {
		t.Fatalf("shared from the workspace: %d", code)
	}
	code, _, h := tw.post("/settings/folders/delete", url.Values{"source": {"machine"}, "name": {"Clients"}}, csrf)
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
	// An assistant's token (self_url) opens eriphe-core's MCP server only.
	if code, _ := call("GET", "/v1/folders", "", assistantToken("api-token")); code != http.StatusUnauthorized {
		t.Fatalf("with an assistant's token: %d", code)
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

// A folder's copies may be turned off from here, at once, then on its
// machine; never on. Its level is its machine's alone.
func TestNoCopies(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	ctx := context.Background()
	shares.set(sharedFolder{ID: "f1", Name: "Clients", Access: accessWrite, Copyable: true}, sharedFolder{ID: "f2", Name: "Photos", Access: accessRead})
	c.refreshNode(ctx, c.node("nas"))
	if f, _ := store.folder("Clients"); f.Access != accessWrite || !f.Copyable {
		t.Fatalf("shared %+v", f)
	}
	if err := c.noCopies(ctx, "clients"); err != nil {
		t.Fatal(err)
	}
	if f, _ := store.folder("Clients"); f.Copyable || f.Access != accessWrite {
		t.Fatalf("copies off: %+v", f)
	}
	shares.mu.Lock()
	patched := slices.Clone(shares.patched)
	shares.mu.Unlock()
	if !slices.Equal(patched, []string{"f1"}) {
		t.Fatalf("told the machine %v", patched)
	}
	c.refreshNode(ctx, c.node("nas"))
	if l, _ := store.noCopyFolders(); len(l) != 0 {
		t.Fatalf("kept %v once the machine turned them off", l)
	}
	// Offline, or an older node: off here all the same.
	store.db.Exec(`INSERT INTO folders_lowered (node, id, nocopy) VALUES ('nas', 'f2', 1)`)
	shares.set(sharedFolder{ID: "f1", Name: "Clients", Access: accessWrite}, sharedFolder{ID: "f2", Name: "Photos", Access: accessRead, Copyable: true})
	store.setNodeStatus("nas", true, []byte(`{"runtimes":["claude-code"],"folders":[{"id":"f2","name":"Photos","access":"read","copyable":true}]}`))
	if f, _ := store.folder("Photos"); f.Copyable {
		t.Fatalf("copies on: %+v", f)
	}
	log, _ := store.nodeLog("nas", 10, 0)
	if len(log) == 0 || log[0].Kind != logLower {
		t.Fatalf("access log %+v", log)
	}
}

// A folder shared write is lent writable whole only to a task lent it with
// write, and while its Modifier is not never; read otherwise.
func TestWriteFolderSpec(t *testing.T) {
	c, _ := foldersCore(t)
	ctx := context.Background()
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Photos", Access: accessWrite}}}); err == nil || !strings.Contains(err.Error(), "shared read-only") {
		t.Fatalf("a read folder lent to write: %v", err)
	}
	task, err := c.runTask(ctx, runRequest{Prompt: "tidy", Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients", Access: accessWrite}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := assignedSpec(t, c, "nas", task.ID)
	if len(spec.Folders) != 1 || !spec.Folders[0].Write {
		t.Fatalf("folders %+v", spec.Folders)
	}
	if raw, _ := json.Marshal(spec.Folders[0]); !strings.Contains(string(raw), `"manage":true`) || strings.Contains(string(raw), `"write"`) {
		t.Fatalf("on the wire: %s", raw) // an older node's write was drop
	}
	if !strings.Contains(spec.AppendSystemPrompt, "rename and delete files anywhere in it") || !strings.Contains(spec.AppendSystemPrompt, "snapshot") {
		t.Fatalf("instructions %q", spec.AppendSystemPrompt)
	}
	c.turn.taint("gmail.read")
	if err := c.taintedResources(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients", Access: accessWrite}}}); err != nil {
		t.Fatalf("written on a tainted turn: %v", err)
	}
	// Modifier never since: the task is mounted read-only, and none is lent to write.
	c.setFolderCase("Clients", catChange, gridNever)
	spec = assignedSpec(t, c, "nas", task.ID)
	if len(spec.Folders) != 1 || spec.Folders[0].Write {
		t.Fatalf("never: %+v", spec.Folders)
	}
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients", Access: accessWrite}}}); err == nil || !strings.Contains(err.Error(), "never") {
		t.Fatalf("lent to write under never: %v", err)
	}
}

// Turning copies off from the workspace and the API; nothing raises.
func TestWebNoCopies(t *testing.T) {
	c, store, shares := foldersCoreShares(t)
	shares.set(sharedFolder{ID: "f1", Name: "Clients", Access: accessWrite, Copyable: true}, sharedFolder{ID: "f2", Name: "Photos", Access: accessRead})
	c.refreshNode(context.Background(), c.node("nas"))
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	for _, want := range []string{"Peut être copié", `hx-post="/settings/folders/nocopy"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	tw.post("/settings/folders/nocopy", url.Values{"name": {"Clients"}}, csrf)
	if f, _ := store.folder("Clients"); f.Copyable {
		t.Fatal("copies still allowed")
	}

	mux := http.NewServeMux()
	c.folderRoutes(mux, "api-token")
	call := func(body string) (int, string) {
		r := httptest.NewRequest("PATCH", "/v1/folders/Photos", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer api-token")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	for body, want := range map[string]int{`{"copyable":true}`: 403, `nope`: 400, `{"copyable":false}`: 200} {
		if code, out := call(body); code != want {
			t.Errorf("%s: %d %s", body, code, out)
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

// A folder's rights: its machine's default, Free and Free until set, and
// the folder's own cases above or below it; a read-only share greys
// Modifier. On approval waits for the user's yes on the start card, a
// change on an unclean start too; never is refused.
func TestFolderRights(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	clients, _ := store.folder("Clients")
	photos, _ := store.folder("Photos")
	if r := c.folderRights(clients); r[catRead] != gridFree || r[catChange] != gridFree {
		t.Fatalf("default %v", r)
	}
	if r := c.folderRights(photos); r[catChange] != gridNever {
		t.Fatalf("read-only share: %v", r)
	}
	if n := c.folderNeeds(folderRefs("Clients"), true); len(n) != 0 {
		t.Fatalf("free on a clean start: %+v", n)
	}
	for _, bad := range [][3]string{{"Clients", catDelete, gridFree}, {"Clients", catChange, "maybe"}, {"Nowhere", catChange, gridFree}} {
		if err := c.setFolderCase(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if c.setDeviceCase("nas", catChange, "") == nil {
		t.Error("a device default that follows nothing")
	}

	// The machine's default asks; a folder's own case goes above it.
	c.setDeviceCase("nas", catChange, gridApproval)
	c.setDeviceCase("nas", catRead, gridApproval)
	if r := c.folderRights(clients); r[catRead] != gridApproval || r[catChange] != gridApproval {
		t.Fatalf("device default %v", r)
	}
	if r := c.folderRights(photos); r[catChange] != gridNever {
		t.Fatalf("ceiling under the default: %v", r)
	}
	c.setFolderCase("Clients", catRead, gridFree)
	if r := c.folderRights(clients); r[catRead] != gridFree || r[catChange] != gridApproval {
		t.Fatalf("exception %v", r)
	}
	if n := c.folderNeeds(folderRefs("Photos", "Clients"), true); len(n) != 2 || n[0].Folder != "Photos" || n[0].Category != catRead || n[1].Category != catChange {
		t.Fatalf("approval on a clean start: %+v", n)
	}

	// The start waits for the card, which says what it asks.
	task, err := c.runTask(ctx, runRequest{Prompt: "sort", Resources: folderRefs("Clients"), askStart: true, cleanStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if !c.heldTask(task.ID) {
		t.Fatal("not held")
	}
	ids := c.proposedStarts()
	if len(ids) != 1 {
		t.Fatalf("starts %v", ids)
	}
	s, _ := c.taskStart(ids[0])
	if got := startText(s.Needs, "fr"); got != "Pour modifier et supprimer dans le dossier Clients, il me faut ton accord." {
		t.Fatalf("text %q", got)
	}
	if got := startText(s.Needs, "en"); got != "To change and delete in the folder Clients, I need your approval." {
		t.Fatalf("text %q", got)
	}
	if err := c.decideTaskStart(ids[0], true); err != nil || c.heldTask(task.ID) {
		t.Fatalf("yes: %v", err)
	}

	// Its own Free again, then following the machine again.
	c.setFolderCase("Clients", catChange, gridFree)
	if n := c.folderNeeds(folderRefs("Clients"), true); len(n) != 0 {
		t.Fatalf("free again: %+v", n)
	}
	c.setFolderCase("Clients", catChange, "")
	c.setFolderCase("Clients", catRead, "")
	if store.get(folderGridKey(clients)) != "" || c.folderCase(clients, catChange) != gridApproval {
		t.Fatal("not following its machine")
	}
	c.setDeviceCase("nas", catRead, gridNever)
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients"}}}); err == nil || !strings.Contains(err.Error(), "read is never") {
		t.Fatalf("read never: %v", err)
	}
}

// The NAS's values from before rights: manage on approval is Modifier on
// approval; drop's go.
func TestLegacyFolderValues(t *testing.T) {
	c, store := foldersCore(t)
	store.set("folder_value:nas:f1:manage", gridApproval)
	store.set("folder_value:nas:f1:drop", gridApproval)
	store.set("folder_value:nas:f2:manage", "")
	legacyFolderValues(store)
	clients, _ := store.folder("Clients")
	if c.folderCase(clients, catChange) != gridApproval || c.folderCase(clients, catRead) != gridFree {
		t.Fatalf("moved %v", c.folderRights(clients))
	}
	var n int
	store.db.QueryRow(`SELECT COUNT(*) FROM kv WHERE key LIKE 'folder_value:%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d old values left", n)
	}
}

// Settings → Dossiers sets a machine's default and a folder's own cases.
func TestWebFolderCases(t *testing.T) {
	c, store := foldersCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	if !strings.Contains(body, `name="node" value="nas"`) || !strings.Contains(body, `name="case-read" value="free"`) ||
		!strings.Contains(body, "/settings/folders/grid?name=Clients") || !strings.Contains(body, "Comme l&#39;appareil") {
		t.Fatalf("page: %s", mainPart(t, body))
	}
	if code, body, _ := tw.do("GET", "/settings/folders/grid?name=Clients", nil, nil); code != 200 || !strings.Contains(body, `name="case-change" value=""`) {
		t.Fatalf("drawer: %d %s", code, body)
	}
	code, body, _ := tw.post("/settings/folders/case", url.Values{"name": {"Clients"}, "case-read": {""}, "case-change": {"approval"}}, csrf)
	if code != 200 || !strings.Contains(body, "Modifier : Sur accord") || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Fatalf("set: %d %s", code, body)
	}
	clients, _ := store.folder("Clients")
	if c.folderCase(clients, catChange) != gridApproval {
		t.Fatal("not kept")
	}
	code, _, h := tw.post("/settings/folders/case", url.Values{"node": {"nas"}, "case-read": {"never"}, "case-change": {"free"}}, csrf)
	if code != 200 || h.Get("HX-Refresh") != "true" || c.deviceGrid("nas").Read != gridNever {
		t.Fatalf("device: %d %v", code, h)
	}
	if _, _, h := tw.post("/settings/folders/case", url.Values{"name": {"Gone"}, "case-change": {"approval"}}, csrf); strings.Contains(h.Get("HX-Trigger"), `"ok":true`) {
		t.Fatalf("unknown folder: %v", h)
	}
}
