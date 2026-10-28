package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newUpdateCore is an eriphe-core at 0.1.0 whose registry says the
// latest eriphe-core is latestCore, with eriphe-core's own updater.
func newUpdateCore(t *testing.T, latestCore string) *core {
	t.Helper()
	old := version
	t.Cleanup(func() { version = old })
	version = "0.1.0"
	c, _ := newTestAPI(t)
	f := newFakeRegistry(t, map[string]string{"eriphe-core": latestCore, "eriphe-node": latestCore})
	c.updates = newUpdates(f.srv.URL)
	c.updates.check(context.Background())
	os.WriteFile(filepath.Join(c.store.dir, "updater"), []byte("version=1\npin=\n"), 0o600)
	return c
}

// setNode registers a node with its version and updater as it reports them.
func setNode(t *testing.T, c *core, name, info string) {
	t.Helper()
	if !c.store.hasNode(name) {
		c.store.registerNode(name, hashSecret(name+"-secret"))
	}
	if err := c.store.setNodeStatus(name, true, []byte(info)); err != nil {
		t.Fatal(err)
	}
}

func assignmentUpdate(t *testing.T, c *core, name string) *nodeUpdate {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/node/assignments?rev=0&wait=0", nil)
	req.Header.Set("Authorization", "Bearer "+name+"-secret")
	req.Header.Set("Eriphe-Node", name)
	w := httptest.NewRecorder()
	c.handleAssignments(w, req)
	var out struct {
		Update *nodeUpdate `json:"update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("assignments: %d %s", w.Code, w.Body)
	}
	return out.Update
}

// One choice for all machines, which each one can replace, and go back to.
func TestUpdatePolicy(t *testing.T) {
	c := newUpdateCore(t, "0.1.0")
	if own, eff := c.updatePolicy("node:pc"); own != "" || eff != updateNotify {
		t.Fatalf("default: %q %q", own, eff)
	}
	if err := c.setUpdatePolicy("", updateNight); err != nil {
		t.Fatal(err)
	}
	if _, eff := c.updatePolicy("node:pc"); eff != updateNight {
		t.Fatalf("all at night: %q", eff)
	}
	c.setUpdatePolicy("node:pc", updateNever)
	if own, eff := c.updatePolicy("node:pc"); own != updateNever || eff != updateNever {
		t.Fatalf("pc never: %q %q", own, eff)
	}
	if _, eff := c.updatePolicy(coreMachine); eff != updateNight {
		t.Fatalf("core: %q", eff)
	}
	c.setUpdatePolicy("node:pc", "")
	if own, eff := c.updatePolicy("node:pc"); own != "" || eff != updateNight {
		t.Fatalf("pc back to all: %q %q", own, eff)
	}
	for _, bad := range [][2]string{{"", ""}, {"", "sometimes"}, {"node:pc", "always"}} {
		if err := c.setUpdatePolicy(bad[0], bad[1]); err == nil {
			t.Errorf("setUpdatePolicy(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

// A request reaches the node in its assignments, with eriphe-core's own
// version as the most the node may get; no new task goes to the node
// meanwhile; the updater's outcome ends it.
func TestUpdateRequestFlow(t *testing.T) {
	c := newUpdateCore(t, "0.1.0")
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	setNode(t, c, "old", `{"version":"0.0.1"}`)
	if _, err := c.requestUpdate("node:old", "update", false); !errors.Is(err, errNoUpdater) {
		t.Fatalf("a node without updater: %v", err)
	}
	if u := assignmentUpdate(t, c, "pc"); u != nil {
		t.Fatalf("an update before any request: %+v", u)
	}
	r, err := c.requestUpdate("node:pc", "update", false)
	if err != nil {
		t.Fatal(err)
	}
	u := assignmentUpdate(t, c, "pc")
	if u == nil || u.ID != r.ID || u.Action != "update" || u.Core != "0.1.0" || u.Auto {
		t.Fatalf("assignment update = %+v", u)
	}
	if u := assignmentUpdate(t, c, "old"); u != nil {
		t.Fatalf("old gets pc's update: %+v", u)
	}
	nodes, _ := c.store.nodes()
	for _, n := range c.schedulable(nodes) {
		if n.Online != (n.Name != "pc") {
			t.Errorf("%s schedulable = %t while pc updates", n.Name, n.Online)
		}
	}

	// The updater is at it, then done.
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"running"}}}`)
	nodes, _ = c.store.nodes()
	c.resolveUpdates(nodes)
	if c.pendingUpdate("node:pc") == nil {
		t.Fatal("dropped while the updater is at it")
	}
	setNode(t, c, "pc", `{"version":"0.1.0","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"ok","to":"0.1.0"}}}`)
	nodes, _ = c.store.nodes()
	c.resolveUpdates(nodes)
	if c.pendingUpdate("node:pc") != nil || assignmentUpdate(t, c, "pc") != nil {
		t.Fatal("the request outlives its outcome")
	}
	for _, n := range c.schedulable(nodes) {
		if !n.Online {
			t.Errorf("%s still cordoned", n.Name)
		}
	}

	// A request nobody does is dropped after a day.
	c.requestUpdate("node:pc", "rollback", false)
	r2 := c.pendingUpdate("node:pc")
	r2.At = time.Now().Add(-requestTTL - time.Minute)
	raw, _ := json.Marshal(r2)
	c.store.set("update_request:node:pc", string(raw))
	c.resolveUpdates(nodes)
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("a stale request is kept")
	}
}

// eriphe-core's machine first: while a newer eriphe-core is out, a node is
// not updated (it would become newer than eriphe-core). eriphe-core's own
// request goes to its data folder, for its updater.
func TestUpdateCoreFirst(t *testing.T) {
	c := newUpdateCore(t, "0.2.0")
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	if _, err := c.requestUpdate("node:pc", "update", false); !errors.Is(err, errCoreFirst) {
		t.Fatalf("node before eriphe-core: %v", err)
	}
	// Going back is always allowed.
	if _, err := c.requestUpdate("node:pc", "rollback", false); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	r, err := c.requestUpdate(coreMachine, "update", false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(c.store.dir, "update-requested"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "id="+r.ID+"\naction=update\ncore=0.1.0\nauto=0\n" {
		t.Fatalf("eriphe-core's request:\n%s", raw)
	}
	if up := c.coreUpdater(); up.Requested != r.ID {
		t.Fatalf("core updater = %+v", up)
	}
	// Its updater wrote the outcome (eriphe-core restarted in between).
	os.Remove(filepath.Join(c.store.dir, "update-requested"))
	os.WriteFile(filepath.Join(c.store.dir, "update-status"),
		[]byte("id="+r.ID+"\naction=update\noutcome=rolled_back\nmessage=eriphe-core does not answer\nfrom=0.1.0\nto=0.2.0\nat=1761620000\n"), 0o600)
	c.resolveUpdates(nil)
	if c.pendingUpdate(coreMachine) != nil {
		t.Fatal("eriphe-core's request outlives its outcome")
	}
	if st := c.coreUpdater().Status; st == nil || st.Outcome != "rolled_back" || st.To != "0.2.0" || st.At.IsZero() {
		t.Fatalf("status = %+v", st)
	}

	os.Remove(filepath.Join(c.store.dir, "updater"))
	if _, err := c.requestUpdate(coreMachine, "update", false); !errors.Is(err, errNoUpdater) {
		t.Fatalf("eriphe-core without updater: %v", err)
	}
}

// At night, for the machines set to it: eriphe-core first, the nodes after
// it, each once a night, never one with a task at work nor a pinned one.
func TestAutoUpdatesAtNight(t *testing.T) {
	c := newUpdateCore(t, "0.2.0")
	c.setUpdatePolicy("", updateNight)
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	// The user's time zone: this configuration has none, so the machine's.
	day := time.Date(2026, 10, 28, 14, 0, 0, 0, time.Local)
	night := time.Date(2026, 10, 29, 3, 30, 0, 0, time.Local)

	c.autoUpdates(day)
	if c.pendingUpdate(coreMachine) != nil || c.pendingUpdate("node:pc") != nil {
		t.Fatal("an automatic update in the day")
	}
	c.autoUpdates(night)
	r := c.pendingUpdate(coreMachine)
	if r == nil || !r.Auto {
		t.Fatalf("eriphe-core not updated at night: %+v", r)
	}
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("a node updated before eriphe-core")
	}
	// Once a night: the request done (and failed), not again until the next.
	c.store.db.Exec(`DELETE FROM kv WHERE key = 'update_request:core'`)
	c.autoUpdates(night.Add(10 * time.Minute))
	if c.pendingUpdate(coreMachine) != nil {
		t.Fatal("eriphe-core tried twice a night")
	}

	// eriphe-core runs the latest version: the nodes now.
	version = "0.2.0"
	setNode(t, c, "busy", `{"version":"0.0.1","update":{"updater":true}}`)
	setNode(t, c, "pinned", `{"version":"0.0.1","update":{"updater":true,"pin":"0.0.1"}}`)
	setNode(t, c, "same", `{"version":"0.2.0","update":{"updater":true}}`)
	setNode(t, c, "mine", `{"version":"0.0.1","update":{"updater":true}}`)
	c.setUpdatePolicy("node:mine", updateNotify)
	c.store.insertTask(&TaskRow{ID: "t1", Node: "busy", Runtime: "claude-code", State: stateRunning, Created: time.Now(), Updated: time.Now()})
	c.autoUpdates(night.Add(20 * time.Minute))
	if r := c.pendingUpdate("node:pc"); r == nil || !r.Auto {
		t.Fatalf("pc not updated: %+v", r)
	}
	for _, n := range []string{"busy", "pinned", "same", "mine"} {
		if c.pendingUpdate("node:"+n) != nil {
			t.Errorf("%s updated automatically", n)
		}
	}
	// A pinned machine is never updated automatically, even when asked.
	if _, err := c.requestUpdate("node:pinned", "update", true); !errors.Is(err, errPinned) {
		t.Fatalf("pinned: %v", err)
	}
	if _, err := c.requestUpdate("node:pinned", "update", false); err != nil {
		t.Fatalf("pinned, by hand: %v", err)
	}
	// The busy one, once its task is over, the same night.
	c.store.updateTask("t1", map[string]any{"state": stateIdle})
	c.autoUpdates(night.Add(30 * time.Minute))
	if c.pendingUpdate("node:busy") == nil {
		t.Fatal("busy not updated once idle")
	}
}

// Settings → Machines: the choice, the button, and where each machine stands.
func TestWebUpdates(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "0.1.0"
	tw := newTestWeb(t)
	csrf := tw.setup()
	h := map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true"}
	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"u-1","outcome":"rolled_back","message":"eriphe-node did not connect","to":"0.0.2","at":"2026-10-28T03:10:00Z"}}}`)
	setNode(t, tw.c, "nas", `{"version":"0.1.0"}`)

	_, body, _ := tw.do("GET", "/settings/machines", nil, nil)
	for _, want := range []string{"Mises à jour", "Me prévenir", "Automatiquement la nuit", "Jamais",
		"Échec, version précédente remise", "eriphe-node did not connect", "Version 0.1.0 disponible",
		"ne sait pas encore se mettre à jour"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/machines lacks %q", want)
		}
	}

	if code, _, _ := tw.do("POST", "/settings/updates/policy", url.Values{"machine": {"node:pc"}, "policy": {"night"}}, h); code != 200 {
		t.Fatalf("policy: %d", code)
	}
	if own, _ := tw.c.updatePolicy("node:pc"); own != updateNight {
		t.Fatalf("pc's policy = %q", own)
	}
	if code, _, _ := tw.do("POST", "/settings/updates/policy", url.Values{"machine": {"node:ghost"}, "policy": {"night"}}, h); code != 200 ||
		tw.c.store.get("update_policy:node:ghost") != "" {
		t.Fatal("a policy set for an unknown machine")
	}
	_, _, hdr := tw.do("POST", "/settings/updates/request", url.Values{"machine": {"node:pc"}, "action": {"update"}}, h)
	if r := tw.c.pendingUpdate("node:pc"); r == nil || r.Auto || hdr.Get("HX-Refresh") != "true" {
		t.Fatalf("update now: %+v %v", r, hdr)
	}
	_, body, _ = tw.do("GET", "/settings/machines", nil, nil)
	if !strings.Contains(body, "Mise à jour demandée") {
		t.Error("the request does not show")
	}
	_, _, hdr = tw.do("POST", "/settings/updates/request", url.Values{"machine": {"node:nas"}, "action": {"update"}}, h)
	if tw.c.pendingUpdate("node:nas") != nil || !strings.Contains(hdr.Get("HX-Trigger"), "relance une fois") {
		t.Fatalf("a machine without updater: %v", hdr)
	}

	// Gone back, by the button: said as such.
	tw.c.store.db.Exec(`DELETE FROM kv WHERE key LIKE 'update_request:%'`)
	setNode(t, tw.c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"u-2","action":"rollback","outcome":"ok","to":"0.0.1"}}}`)
	if _, body, _ = tw.do("GET", "/settings/machines", nil, nil); !strings.Contains(body, "Version précédente remise · 0.0.1") {
		t.Error("a rollback does not show")
	}
}
