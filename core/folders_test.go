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
	"strings"
	"testing"

	"github.com/hashicorp/yamux"
)

// foldersCore is a core with one agent, a machine "nas" sharing the root
// "data" (writable) and "media" (read-only) through a fake node on a
// tunnel, and a machine "pc" sharing nothing.
func foldersCore(t *testing.T) (*core, *Store) {
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

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"runtimes":["claude-code"],"cpus":2,"shares":[{"name":"data","writable":true},{"name":"media","writable":false}]}`))
	})
	api.HandleFunc("GET /v1/shares/{share}/dirs", func(w http.ResponseWriter, r *http.Request) {
		p, check := r.URL.Query().Get("path"), r.URL.Query().Get("check") == "1"
		share := r.PathValue("share")
		switch {
		case share != "data" && share != "media":
			writeJSON(w, 404, map[string]string{"code": "unknown_share", "error": "no root"})
		case p == "escape":
			writeJSON(w, 403, map[string]string{"code": "outside", "error": "out of the root"})
		default:
			out := map[string]any{"share": share, "path": strings.TrimSuffix(strings.TrimPrefix(p, "link-to-"), "/"),
				"dirs": []string{"ACME"}, "files": 2, "writable": share == "data"}
			if check {
				out["shareable"] = p != "home"
				if p == "home" {
					out["code"], out["reason"] = "holds_sensitive", "holds .ssh"
				}
			}
			writeJSON(w, 200, out)
		}
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
			if row.Name == "nas" && row.Online && len(parseInfo(row).Shares) == 2 {
				return true
			}
		}
		return false
	})
	return c, store
}

func TestAddFolder(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	f, err := c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "link-to-work/Clients/", Access: folderDrop})
	if err != nil {
		t.Fatal(err)
	}
	// Kept under the path the machine resolved.
	if got, _ := store.folder("clients"); got == nil || got.Path != "work/Clients" || got.Access != folderDrop || f.Name != "Clients" {
		t.Fatalf("stored %+v", got)
	}
	if _, err := c.addFolder(ctx, Folder{Name: "Photos", Node: "nas", Share: "media", Path: "photos"}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]Folder{
		"exists":          {Name: "CLIENTS", Node: "nas", Share: "data", Path: "x"},
		"bad_name":        {Name: "../x", Node: "nas", Share: "data", Path: "x"},
		"bad_access":      {Name: "x", Node: "nas", Share: "data", Path: "x", Access: "write"},
		"holds_sensitive": {Name: "Home", Node: "nas", Share: "data", Path: "home"},
		"outside":         {Name: "Out", Node: "nas", Share: "data", Path: "escape"},
		"unknown_share":   {Name: "U", Node: "nas", Share: "nope", Path: ""},
		"read_only_share": {Name: "M", Node: "nas", Share: "media", Path: "m", Access: folderDrop},
		"offline":         {Name: "P", Node: "pc", Share: "data", Path: ""},
	} {
		_, err := c.addFolder(ctx, in)
		if fe, ok := err.(*folderError); !ok || fe.Code != name {
			t.Errorf("%s: %v", name, err)
		}
	}
	fs, _ := c.folderViews()
	if len(fs) != 2 || !fs[0].Online || fs[0].Mount != "/work/folders/Clients" {
		t.Fatalf("views %+v", fs)
	}
}

// A task lent a folder runs on its machine, gets it in its spec with the
// right of the folder, and is told where it is; removed, the folder fails it.
func TestTaskWithFolders(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop})
	c.addFolder(ctx, Folder{Name: "Photos", Node: "nas", Share: "media", Path: "Photos"})

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
	if len(spec.Folders) != 2 || !reflect.DeepEqual(spec.Folders[0], nodeFolder{Name: "Clients", Share: "data", Path: "Clients", Write: true, Results: "Eriphe"}) ||
		!reflect.DeepEqual(spec.Folders[1], nodeFolder{Name: "Photos", Share: "media", Path: "Photos"}) {
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

	store.deleteFolder("Clients")
	assignedSpec(t, c, "nas", "")
	if got, _ := store.task(task.ID); got.State != stateFailed {
		t.Fatalf("state %s after its folder was removed", got.State)
	}
}

// With the folder's machine offline, the task waits for it.
func TestFolderTaskWaitsForItsMachine(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients"})
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
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop})
	c.addFolder(ctx, Folder{Name: "Photos", Node: "nas", Share: "media", Path: "Photos"})
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

// Settings → Folders: the machine's roots, a folder's directories, sharing
// it, and no longer sharing it.
func TestWebFolders(t *testing.T) {
	c, store := foldersCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	if code != 200 || !strings.Contains(body, "Partager un dossier") || !strings.Contains(body, `<option value="nas" >nas</option>`) ||
		!strings.Contains(body, "Sans emplacement partagé : pc") {
		t.Fatalf("page: %d %s", code, body)
	}
	code, body, _ = tw.do("GET", "/settings/folders/browse?node=nas", nil, nil)
	if code != 200 || !strings.Contains(body, `hx-get="/settings/folders/browse?node=nas&amp;share=data"`) || !strings.Contains(body, "En lecture seule sur cet appareil") {
		t.Fatalf("roots: %d %s", code, body)
	}
	code, body, _ = tw.do("GET", "/settings/folders/browse?node=nas&share=data&path=Clients", nil, nil)
	for _, want := range []string{`path=Clients%2FACME`, "2 fichier(s) ici.", `name="path" value="Clients"`, `name="name" value="Clients"`, `value="drop" >`} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("listing lacks %q: %d %s", want, code, body)
		}
	}
	if _, body, _ = tw.do("GET", "/settings/folders/browse?node=nas&share=data&path=escape", nil, nil); !strings.Contains(body, "hors de l&#39;emplacement") {
		t.Fatalf("escape: %s", body)
	}
	code, body, h := tw.post("/settings/folders", url.Values{"node": {"nas"}, "share": {"data"}, "path": {"home"}, "name": {"Home"}, "access": {"read"}}, csrf)
	if code != 200 || !strings.Contains(body, "données sensibles") || h.Get("HX-Refresh") != "" {
		t.Fatalf("sensitive: %d %s", code, body)
	}
	code, _, h = tw.post("/settings/folders", url.Values{"node": {"nas"}, "share": {"data"}, "path": {"Clients"}, "name": {"Mes clients"}, "access": {"drop"}}, csrf)
	if f, _ := store.folder("Mes clients"); code != 200 || h.Get("HX-Refresh") != "true" || f == nil || f.Access != folderDrop {
		t.Fatalf("share: %d %+v", code, f)
	}
	if _, body, _ = tw.do("GET", "/settings/folders", nil, nil); !strings.Contains(body, "Mes clients") || !strings.Contains(body, "data:/Clients") {
		t.Fatalf("list: %s", body)
	}
	code, _, h = tw.post("/settings/folders/delete", url.Values{"name": {"Mes clients"}}, csrf)
	if _, err := store.folder("Mes clients"); code != 200 || h.Get("HX-Refresh") != "true" || err == nil {
		t.Fatalf("delete: %d %v", code, err)
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
	for _, r := range [][2]string{{"GET", "/v1/folders"}, {"POST", "/v1/folders"}, {"GET", "/v1/nodes/nas/shares/data/dirs"}} {
		if code, _ := call(r[0], r[1], `{"name":"All","node":"nas","share":"data","path":"","access":"read"}`, assistantToken("api-token")); code != http.StatusUnauthorized {
			t.Fatalf("%s %s with an assistant's token: %d", r[0], r[1], code)
		}
	}
	if code, body := call("POST", "/v1/folders", `{"name":"Clients","node":"nas","share":"data","path":"Clients","access":"drop"}`, "api-token"); code != http.StatusCreated || !strings.Contains(body, `"name":"Clients"`) {
		t.Fatalf("add: %d %s", code, body)
	}
	if code, body := call("GET", "/v1/folders", "", "api-token"); code != http.StatusOK || !strings.Contains(body, `"folders":[{`) || !strings.Contains(body, "Clients") {
		t.Fatalf("list: %d %s", code, body)
	}
	for _, x := range []struct {
		method, path, body string
		code               int
		want               string
	}{
		{"POST", "/v1/folders", `{"name":`, http.StatusBadRequest, ""},
		{"POST", "/v1/folders", `{"name":"clients","node":"nas","share":"data","path":"x"}`, http.StatusConflict, `"code":"exists"`},
		{"POST", "/v1/folders", `{"name":"Out","node":"nas","share":"data","path":"escape"}`, http.StatusForbidden, `"code":"outside"`},
		{"POST", "/v1/folders", `{"name":"P","node":"pc","share":"data","path":""}`, http.StatusServiceUnavailable, `"code":"offline"`},
		{"POST", "/v1/folders", `{"name":"U","node":"nas","share":"nope","path":""}`, http.StatusNotFound, `"code":"unknown_share"`},
		{"GET", "/v1/nodes/nas/shares", "", http.StatusOK, `{"shares":[{"name":"data","writable":true},{"name":"media","writable":false}]}`},
		{"GET", "/v1/nodes/pc/shares", "", http.StatusOK, `{"shares":[]}`},
		{"GET", "/v1/nodes/nas/shares/data/dirs?path=work", "", http.StatusOK, `"dirs":["ACME"]`},
		{"GET", "/v1/nodes/nas/shares/data/dirs?path=home&check=1", "", http.StatusOK, `"code":"holds_sensitive"`},
		{"GET", "/v1/nodes/nas/shares/data/dirs?path=escape", "", http.StatusForbidden, `"code":"outside"`},
		{"GET", "/v1/nodes/pc/shares/data/dirs", "", http.StatusServiceUnavailable, `"code":"offline"`},
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
		{&folderError{Code: "bad_name"}, http.StatusBadRequest},
		{&folderError{Code: "offline"}, http.StatusServiceUnavailable},
		{&folderError{Code: "unavailable"}, http.StatusServiceUnavailable},
		{&folderError{Code: "old_node"}, http.StatusServiceUnavailable},
		{&folderError{Code: "suspended"}, http.StatusServiceUnavailable},
		{&folderError{Code: "exists"}, http.StatusConflict},
		{&folderError{Code: "not_found"}, http.StatusNotFound},
		{&folderError{Code: "unknown_share"}, http.StatusNotFound},
		{&folderError{Code: "outside"}, http.StatusForbidden},
		{&folderError{Code: "sensitive"}, http.StatusForbidden},
		{&folderError{Code: "eriphe_data"}, http.StatusForbidden},
		{&folderError{Code: "holds_sensitive"}, http.StatusForbidden},
		{&folderError{Code: "unreadable"}, http.StatusForbidden},
		{&folderError{Code: "read_only_share"}, http.StatusForbidden},
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
