package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// attachMac connects a fake Mac "mac" on a tunnel: it shares Notes, which
// macOS keeps from its node, and counts the times it is asked to open
// System Settings.
func attachMac(t *testing.T, c *core, store *Store) *atomic.Int32 {
	t.Helper()
	store.registerNode("mac", hashSecret("z"))
	opened := &atomic.Int32{}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"os": "darwin", "install": "macos", "runtimes": []string{"claude-code"},
			"folders": []sharedFolder{{ID: "m1", Name: "Notes", Access: accessRead, Problem: "denied"},
				{ID: "m2", Name: "Music", Access: accessRead, Copyable: true, Problem: "denied", Program: "syncthing"}}})
	})
	api.HandleFunc("POST /v1/privacy/open", func(w http.ResponseWriter, _ *http.Request) {
		opened.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	coreEnd, nodeEnd := net.Pipe()
	nodeSession, _ := yamux.Server(nodeEnd, tunnelConfig())
	go http.Serve(nodeSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach("mac", coreSession, false)
	t.Cleanup(func() { coreSession.Close() })
	waitUntil(t, func() bool {
		f, err := store.folder("Notes")
		return err == nil && f.Problem == "denied"
	})
	return opened
}

// A folder macOS keeps from its node is said on Settings → Folders and on
// the device's card, with "Allow", which has that Mac open System
// Settings; on another system, a plain explanation, no button.
func TestFolderBlocked(t *testing.T) {
	c, store := foldersCore(t)
	opened := attachMac(t, c, store)
	store.setNodeStatus("pc", true, []byte(`{"os":"linux","runtimes":["claude-code"],"folders":[{"id":"p1","name":"Scans","access":"write","problem":"read_only"}]}`))
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, body, _ := tw.do("GET", "/settings/folders", nil, nil)
	for _, want := range []string{"macOS bloque l&#39;accès à ce dossier", `hx-post="/settings/devices/mac/privacy"`, "Autoriser",
		"Confidentialité et sécurité › Fichiers et dossiers", "Ce dossier est en lecture seule sur son appareil",
		"Active eriphe-node, et le dossier", "Active syncthing, et le dossier", `name="program" value="syncthing"`} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("folders page lacks %q: %d %s", want, code, body)
		}
	}
	if strings.Contains(body, `hx-post="/settings/devices/pc/privacy"`) {
		t.Error("Allow offered for a machine that is not a Mac")
	}
	code, body, _ = tw.do("GET", "/settings/devices", nil, nil)
	for _, want := range []string{"macOS bloque l&#39;accès à ce dossier", `hx-post="/settings/devices/mac/privacy"`, "bloqué"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("devices page lacks %q: %d", want, code)
		}
	}
	code, _, h := tw.post("/settings/devices/mac/privacy", nil, csrf)
	if code != 200 || opened.Load() != 1 || !strings.Contains(h.Get("HX-Trigger"), "mac") {
		t.Fatalf("allow: %d, opened %d, %v", code, opened.Load(), h)
	}
	// For Syncthing, the toast names it.
	if _, _, h := tw.post("/settings/devices/mac/privacy", url.Values{"program": {"syncthing"}}, csrf); opened.Load() != 2 || !strings.Contains(h.Get("HX-Trigger"), "active syncthing") {
		t.Fatalf("allow syncthing: opened %d, %v", opened.Load(), h)
	}
	// Not a Mac, or not connected: nothing asked.
	tw.post("/settings/devices/pc/privacy", nil, csrf)
	if opened.Load() != 2 {
		t.Error("a machine that is not a Mac was asked")
	}
	if err := c.openPrivacy(t.Context(), "pc"); err != errNotMac {
		t.Errorf("pc: %v", err)
	}
	if err := c.openPrivacy(t.Context(), "ghost"); err != errNoNode {
		t.Errorf("ghost: %v", err)
	}
	c.disconnect("mac")
	waitUntil(t, func() bool { return !c.node("mac").connected() })
	if err := c.openPrivacy(t.Context(), "mac"); err != errOffline {
		t.Errorf("offline: %v", err)
	}
}

// A copy its machine cannot write is an error with its problem, and says
// whether that machine is a Mac.
func TestCopyBlocked(t *testing.T) {
	v := &syncView{nodes: map[string]NodeRow{
		"nas": {Name: "nas", Online: true, LastSeen: time.Now()},
		"mac": {Name: "mac", Online: true, OS: "darwin", Info: []byte(`{"syncthing":{"device_id":"X","folders":[` +
			`{"id":"e-nas-f1","share":"f1","source":"nas","role":"copy","state":"missing","error":"this machine cannot write the copy: permission denied","problem":"denied","program":"eriphe-node"}]}}`)},
	}}
	cv := v.copyStatus(&Folder{Node: "nas", ID: "f1"}, "mac")
	if cv.Status != "error" || cv.Problem != "denied" || cv.Program != "eriphe-node" || !cv.Mac || !strings.Contains(cv.Error, "cannot write") {
		t.Fatalf("copy %+v", cv)
	}
}
