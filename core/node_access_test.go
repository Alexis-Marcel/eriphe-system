package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The access log keeps what Eriphe did on a device, newest first, without
// repeating the same entry at once, and forgets what is past 90 days.
func TestNodeLogStore(t *testing.T) {
	_, store := foldersCore(t)
	store.logNode("pc", NodeLogEntry{Kind: logList, Detail: "data:/"})
	store.logNode("pc", NodeLogEntry{Kind: logList, Detail: "data:/"}) // the same, just after: not repeated
	store.logNode("pc", NodeLogEntry{Kind: logTask, Task: "t1", Detail: "sonnet"})
	store.logNode("nas", NodeLogEntry{Kind: logCut})
	got, err := store.nodeLog("pc", 10, 0)
	if err != nil || len(got) != 2 || got[0].Kind != logTask || got[0].Task != "t1" || got[1].Kind != logList {
		t.Fatalf("log %+v, %v", got, err)
	}
	if older, _ := store.nodeLog("pc", 10, got[0].ID); len(older) != 1 || older[0].Kind != logList {
		t.Fatalf("before %d: %+v", got[0].ID, older)
	}
	// An entry past the retention is no longer shown, and goes at the next one.
	store.db.Exec(`UPDATE node_log SET time = ? WHERE kind = ?`, time.Now().Add(-nodeLogRetention-time.Hour).Unix(), logList)
	if got, _ := store.nodeLog("pc", 10, 0); len(got) != 1 {
		t.Fatalf("old entries shown: %+v", got)
	}
	store.logNode("pc", NodeLogEntry{Kind: logShare, Folder: "Clients", Access: folderRead})
	var n int
	store.db.QueryRow(`SELECT COUNT(*) FROM node_log WHERE node = 'pc'`).Scan(&n)
	if n != 2 {
		t.Fatalf("%d entries kept, want 2", n)
	}
	// Forgetting a device forgets its log.
	store.deleteNode("pc")
	if got, _ := store.nodeLog("pc", 10, 0); len(got) != 0 {
		t.Fatalf("log of a forgotten device: %+v", got)
	}
}

// Eriphe logs a folder listed, opened, lent to a task with its right, the
// task itself, a result dropped in Eriphe/, and the folder closed.
func TestNodeLogRecordsAccess(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	if _, err := c.browse(ctx, "nas", "data", "Clients", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop}); err != nil {
		t.Fatal(err)
	}
	task, err := c.runTask(ctx, runRequest{Prompt: "sum the invoices", Folders: []string{"Clients"}})
	if err != nil {
		t.Fatal(err)
	}
	c.logResults(task, []EventRow{
		{Type: "tool_use", Data: map[string]any{"name": "Write", "input": map[string]any{"file_path": "/work/folders/Clients/Eriphe/summary.md"}}},
		{Type: "tool_use", Data: map[string]any{"name": "Write", "input": map[string]any{"file_path": "/work/out.md"}}},
		{Type: "tool_use", Data: map[string]any{"name": "Bash", "input": map[string]any{"command": "ls"}}},
	})
	if err := c.removeFolder("clients"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.nodeLog("nas", 20, 0)
	var kinds []string
	for _, e := range got {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "unshare,result,lend,task,share,list" {
		t.Fatalf("kinds %v", kinds)
	}
	if e := got[1]; e.Task != task.ID || e.Folder != "Clients" || e.Detail != "Eriphe/summary.md" {
		t.Fatalf("result %+v", e)
	}
	if e := got[2]; e.Task != task.ID || e.Folder != "Clients" || e.Access != folderDrop {
		t.Fatalf("lend %+v", e)
	}
	if e := got[3]; e.Task != task.ID || e.Detail != "sonnet" {
		t.Fatalf("task %+v", e)
	}
	if e := got[5]; e.Detail != "data:/Clients" {
		t.Fatalf("list %+v", e)
	}
	if other, _ := store.nodeLog("pc", 20, 0); len(other) != 0 {
		t.Fatalf("logged on another device: %+v", other)
	}
}

// Cutting a device's access stops its tasks, sends it no new work, refuses
// to list or lend its folders, drops its tunnel and refuses it again until
// access is restored; nothing is forgotten.
func TestSuspendNode(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients"})
	running, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Clients"}})
	if err != nil || running.Node != "nas" {
		t.Fatalf("task %+v, %v", running, err)
	}

	if err := c.suspendNode("nas"); err != nil {
		t.Fatal(err)
	}
	if err := c.suspendNode("ghost"); err != errNoNode {
		t.Fatalf("unknown device: %v", err)
	}
	// Its tunnel is dropped, its task stopped.
	if c.node("nas").connected() {
		t.Fatal("the tunnel is still up")
	}
	if got, _ := store.task(running.ID); got.State != stateStopped {
		t.Fatalf("its task is %s", got.State)
	}
	nodes, _ := store.nodes()
	for _, n := range nodes {
		if n.Name == "nas" && (!n.Suspended || n.Online) {
			t.Fatalf("nas %+v", n)
		}
	}
	// No work: neither asked for by name, nor chosen, nor for its folders.
	if _, err := c.runTask(ctx, runRequest{Prompt: "x", Node: "nas"}); err == nil || !strings.Contains(err.Error(), "cut") {
		t.Fatalf("by name: %v", err)
	}
	if task, err := c.runTask(ctx, runRequest{Prompt: "x"}); err != nil || task.Node != "pc" {
		t.Fatalf("chosen: %+v, %v", task, err)
	}
	if _, err := c.runTask(ctx, runRequest{Prompt: "x", Folders: []string{"Clients"}}); err == nil || !strings.Contains(err.Error(), "cut") {
		t.Fatalf("its folders lent: %v", err)
	}
	// Its folders are neither listed nor opened.
	if _, err := c.browse(ctx, "nas", "data", "", false); err == nil || err.(*folderError).Code != "suspended" {
		t.Fatalf("listed: %v", err)
	}
	if _, err := c.addFolder(ctx, Folder{Name: "Other", Node: "nas", Share: "data", Path: "x"}); err == nil {
		t.Fatal("opened a folder of a cut device")
	}
	// It is let in neither through its tunnel nor for its assignments,
	// even with its right secret.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/nodes/connect", c.handleConnect)
	mux.HandleFunc("GET /v1/node/assignments", c.handleAssignments)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(path string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer x")
		req.Header.Set("Eriphe-Node", "nas")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", tunnelProto)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := call("/v1/nodes/connect"); code != http.StatusForbidden {
		t.Fatalf("reconnection: %d", code)
	}
	if code := call("/v1/node/assignments"); code != http.StatusUnauthorized {
		t.Fatalf("assignments: %d", code)
	}
	// Nothing is forgotten; restored, it may connect again.
	if f, err := store.folder("Clients"); err != nil || f.Node != "nas" || !store.checkNode("nas", hashSecret("x")) {
		t.Fatalf("forgotten: %+v, %v", f, err)
	}
	if err := c.resumeNode("nas"); err != nil {
		t.Fatal(err)
	}
	if code := call("/v1/nodes/connect"); code == http.StatusForbidden || code == http.StatusUnauthorized {
		t.Fatalf("restored, connection: %d", code)
	}
	got, _ := store.nodeLog("nas", 10, 0)
	if len(got) < 2 || got[0].Kind != logRestore || got[1].Kind != logCut {
		t.Fatalf("log %+v", got)
	}
}

// The API: the log, and cutting and restoring access.
func TestNodeAccessAPI(t *testing.T) {
	c, store := foldersCore(t)
	mux := http.NewServeMux()
	c.nodeAccessRoutes(mux, "tok")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(method, path string, out any) int {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	if code := call("POST", "/v1/nodes/nas/suspend", nil); code != http.StatusOK || !store.nodeSuspended("nas") {
		t.Fatalf("suspend: %d", code)
	}
	var out struct {
		Suspended bool           `json:"suspended"`
		Log       []NodeLogEntry `json:"log"`
	}
	if code := call("GET", "/v1/nodes/nas/log", &out); code != http.StatusOK || !out.Suspended || len(out.Log) != 1 || out.Log[0].Kind != logCut {
		t.Fatalf("log: %d %+v", code, out)
	}
	if code := call("POST", "/v1/nodes/nas/resume", nil); code != http.StatusOK || store.nodeSuspended("nas") {
		t.Fatalf("resume: %d", code)
	}
	if code := call("GET", "/v1/nodes/ghost/log", nil); code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}
	if code := call("POST", "/v1/nodes/ghost/suspend", nil); code != http.StatusNotFound {
		t.Fatalf("unknown suspend: %d", code)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/v1/nodes/nas/suspend", nil)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", resp.StatusCode)
	}
}

// Settings → Devices: each card says what Eriphe sees and can do there,
// and opens the device's page as a whole (its name is a stretched link);
// a device's page shows its log; cutting access shows on the card.
func TestWebDevices(t *testing.T) {
	c, _ := foldersCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	c.addFolder(context.Background(), Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop})

	_, body, _ := tw.do("GET", "/settings/devices", nil, nil)
	for _, want := range []string{"Appareils", "Connecter un appareil", "Ce qu&#39;Eriphe voit ici", "Ce qu&#39;Eriphe peut faire ici",
		"Faire travailler des agents", `<a href="/settings/folders">`, "Clients", "Lecture &#43; dépôt", "Rien d&#39;ouvert",
		`href="/settings/folders?node=pc"`, `<a href="/settings/devices/nas" class="stretched">nas</a>`, `<span class="device-more" aria-hidden="true">Détails ›</span>`, "Couper l&#39;accès", `hx-post="/settings/devices/nas/suspend"`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/devices lacks %q", want)
		}
	}
	if _, old, _ := tw.do("GET", "/settings/machines", nil, nil); !strings.Contains(old, "Ce qu&#39;Eriphe voit ici") {
		t.Error("the old address no longer shows the devices")
	}
	if code, _, h := tw.do("GET", "/settings/machines/nas", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/settings/devices/nas" {
		t.Errorf("old device address: %d %s", code, h.Get("Location"))
	}

	_, body, _ = tw.do("GET", "/settings/devices/nas", nil, nil)
	for _, want := range []string{"Journal des accès", "Dossier ouvert à Eriphe", "data:/Clients", "Couper l&#39;accès"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/devices/nas lacks %q", want)
		}
	}
	if code, _, h := tw.do("GET", "/settings/devices/ghost", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/settings/devices" {
		t.Errorf("unknown device: %d", code)
	}

	if code, _, h := tw.post("/settings/devices/nas/suspend", url.Values{}, csrf); code != http.StatusOK || h.Get("HX-Refresh") != "true" {
		t.Fatalf("suspend: %d", code)
	}
	_, body, _ = tw.do("GET", "/settings/devices", nil, nil)
	for _, want := range []string{"Accès coupé", `class="tile device is-cut"`, "Rétablir l&#39;accès", `hx-post="/settings/devices/nas/resume"`} {
		if !strings.Contains(body, want) {
			t.Errorf("cut: GET /settings/devices lacks %q", want)
		}
	}
	// A cut device is no longer offered to share a folder from.
	if _, body, _ = tw.do("GET", "/settings/folders", nil, nil); strings.Contains(body, `<option value="nas"`) {
		t.Error("the folders page offers a cut device")
	}
	if code, _, _ := tw.post("/settings/devices/nas/resume", url.Values{}, csrf); code != http.StatusOK || c.store.nodeSuspended("nas") {
		t.Fatalf("resume: %d", code)
	}
}
