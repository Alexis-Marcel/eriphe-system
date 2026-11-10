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

// One switch for all machines, off by default.
func TestNightUpdatesSwitch(t *testing.T) {
	c := newUpdateCore(t, "0.1.0")
	if c.nightUpdates() {
		t.Fatal("on by default")
	}
	if err := c.setNightUpdates(true); err != nil || !c.nightUpdates() {
		t.Fatalf("on: %v", err)
	}
	if err := c.setNightUpdates(false); err != nil || c.nightUpdates() {
		t.Fatalf("off: %v", err)
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

// At night, once the switch is on: eriphe-core first, the nodes after it,
// each once a night, never one with a task at work nor a pinned one.
func TestAutoUpdatesAtNight(t *testing.T) {
	c := newUpdateCore(t, "0.2.0")
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	// The user's time zone: this configuration has none, so the machine's.
	day := time.Date(2026, 10, 28, 14, 0, 0, 0, time.Local)
	night := time.Date(2026, 10, 29, 3, 30, 0, 0, time.Local)

	c.autoUpdates(night)
	if c.pendingUpdate(coreMachine) != nil {
		t.Fatal("an automatic update with the switch off")
	}
	c.setNightUpdates(true)
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
	c.store.insertTask(&TaskRow{ID: "t1", Node: "busy", Runtime: "claude-code", State: stateRunning, Created: time.Now(), Updated: time.Now()})
	c.autoUpdates(night.Add(20 * time.Minute))
	if r := c.pendingUpdate("node:pc"); r == nil || !r.Auto {
		t.Fatalf("pc not updated: %+v", r)
	}
	for _, n := range []string{"busy", "pinned", "same"} {
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

// Settings → Machines: where each machine stands, one button per machine
// behind, its command for one without an updater, and the switch.
func TestWebUpdates(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "0.1.0"
	tw := newTestWeb(t)
	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.1.0", "eriphe-node": "0.1.0"})
	tw.c.updates = newUpdates(f.srv.URL)
	tw.c.updates.check(context.Background())
	csrf := tw.setup()
	h := map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true"}
	setNode(t, tw.c, "pc", `{"os":"linux","install":"linux","version":"0.0.1","update":{"updater":true,"status":{"id":"u-1","outcome":"rolled_back","message":"eriphe-node did not connect","to":"0.0.2","at":"2026-10-28T03:10:00Z"}}}`)
	setNode(t, tw.c, "nas", `{"os":"linux","version":"0.0.1"}`)

	_, body, _ := tw.do("GET", "/settings/machines", nil, nil)
	for _, want := range []string{"Mises à jour", "Mettre à jour automatiquement la nuit", "Mise à jour disponible",
		"La mise à jour a échoué, version précédente remise", "eriphe-node did not connect",
		`value="node:pc"`, "docker compose pull &amp;&amp; docker compose up -d", "--rollback"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/machines lacks %q", want)
		}
	}
	for _, gone := range []string{"Me prévenir", "Jamais", "Comme les autres", "Revenir à la version précédente", `value="node:nas"`,
		"curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s</pre>"} {
		if strings.Contains(body, gone) {
			t.Errorf("GET /settings/machines still has %q", gone)
		}
	}

	if code, _, _ := tw.do("POST", "/settings/updates/night", url.Values{"night": {"1"}}, h); code != 200 || !tw.c.nightUpdates() {
		t.Fatalf("switch on: %d", code)
	}
	if _, body, _ = tw.do("GET", "/settings/machines", nil, nil); !strings.Contains(body, `role="switch" name="night" value="1" hx-post="/settings/updates/night" hx-trigger="change" checked`) {
		t.Error("the switch does not show on")
	}
	if tw.do("POST", "/settings/updates/night", url.Values{}, h); tw.c.nightUpdates() {
		t.Fatal("switch off")
	}

	_, body, hdr := tw.do("POST", "/settings/updates/request", url.Values{"machine": {"node:pc"}}, h)
	if r := tw.c.pendingUpdate("node:pc"); r == nil || r.Auto || r.Action != "update" || !strings.Contains(body, `id="device-foot-pc"`) {
		t.Fatalf("update now: %+v %v\n%s", r, hdr, body)
	}
	_, body, _ = tw.do("GET", "/settings/machines", nil, nil)
	if !strings.Contains(body, "Mise à jour demandée") {
		t.Error("the request does not show")
	}
	_, _, hdr = tw.do("POST", "/settings/updates/request", url.Values{"machine": {"node:nas"}}, h)
	if tw.c.pendingUpdate("node:nas") != nil || !strings.Contains(hdr.Get("HX-Trigger"), "relance une fois") {
		t.Fatalf("a machine without updater: %v", hdr)
	}
}

// "Update all": eriphe-core first while it is behind, the nodes once it
// runs the latest version; the nodes at once when it does already.
func TestUpdateAll(t *testing.T) {
	c := newUpdateCore(t, "0.2.0")
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	setNode(t, c, "laptop", `{"version":"0.0.1","update":{"updater":true}}`)
	setNode(t, c, "nas", `{"version":"0.0.1"}`)
	setNode(t, c, "same", `{"version":"0.2.0","update":{"updater":true}}`)

	if n, err := c.updateAll(); err != nil || n != 1 || c.pendingUpdate(coreMachine) == nil || c.pendingUpdate("node:pc") != nil {
		t.Fatalf("eriphe-core behind: %d %v", n, err)
	}
	c.followUpdates()
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("a node asked before eriphe-core updated")
	}

	// eriphe-core updated (and restarted): the nodes follow.
	c.store.db.Exec(`DELETE FROM kv WHERE key = 'update_request:core'`)
	version = "0.2.0"
	c.followUpdates()
	for _, n := range []string{"pc", "laptop"} {
		if c.pendingUpdate("node:"+n) == nil {
			t.Errorf("%s not asked", n)
		}
	}
	for _, n := range []string{"nas", "same"} {
		if c.pendingUpdate("node:"+n) != nil {
			t.Errorf("%s asked", n)
		}
	}
	if c.store.get(updateAllKey) != "" {
		t.Error("update all still waiting")
	}

	c.store.db.Exec(`DELETE FROM kv WHERE key LIKE 'update_request:%'`)
	if n, err := c.updateAll(); err != nil || n != 2 || c.pendingUpdate("node:laptop") == nil {
		t.Fatalf("eriphe-core up to date: %d %v", n, err)
	}
}

// Each machine's system in words, remembered while it is offline.
func TestMachineSystem(t *testing.T) {
	for _, tc := range []struct{ install, goos, want string }{
		{"macos", "darwin", "system.macos-container"},
		{"macos", "linux", "system.macos-lima"},
		{"windows", "linux", "system.windows"},
		{"linux", "linux", "system.linux"},
		{"docker", "linux", "system.docker"},
		{"", "linux", "system.compose"},
		{"", "darwin", "system.darwin"},
		{"", "", ""},
	} {
		if got := systemKey(tc.install, tc.goos); got != tc.want {
			t.Errorf("systemKey(%q, %q) = %q, want %q", tc.install, tc.goos, got, tc.want)
		}
	}

	tw := newTestWeb(t)
	tw.setup()
	setNode(t, tw.c, "mac", `{"os":"darwin","arch":"arm64","install":"macos"}`)
	setNode(t, tw.c, "nas", `{"os":"linux","arch":"amd64"}`)
	tw.c.store.setNodeStatus("mac", false, nil)
	// An offline machine keeps what it said last, even if a report without it came between.
	tw.c.store.setNodeStatus("nas", true, []byte(`{"cpus":4}`))
	nodes, _ := tw.c.store.nodes()
	if len(nodes) != 2 || nodes[0].OS != "darwin" || nodes[0].Arch != "arm64" || nodes[0].Install != "macos" || nodes[1].OS != "linux" {
		t.Fatalf("nodes = %+v", nodes)
	}
	_, body, _ := tw.do("GET", "/settings/machines", nil, nil)
	for _, want := range []string{"macOS (Apple container)", "Linux (Docker Compose)"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/machines lacks %q", want)
		}
	}
}

// A node asked by hand while eriphe-core is behind: eriphe-core is asked
// first (once, for every node waiting), and the node follows only once
// eriphe-core runs the latest version; not when eriphe-core's update ends
// elsewhere, nor before eriphe-core, just restarted, read the latest
// versions.
func TestUpdateNowAfterCore(t *testing.T) {
	c := newUpdateCore(t, "0.2.0")
	setNode(t, c, "pc", `{"version":"0.1.0","update":{"updater":true}}`)
	setNode(t, c, "laptop", `{"version":"0.1.0","update":{"updater":true}}`)
	setNode(t, c, "old", `{"version":"0.1.0"}`)

	if _, err := c.updateNow("node:old"); !errors.Is(err, errNoUpdater) {
		t.Fatalf("a node without updater: %v", err)
	}
	after, err := c.updateNow("node:pc")
	r := c.pendingUpdate(coreMachine)
	if err != nil || !after || r == nil || c.pendingUpdate("node:pc") != nil || !c.afterCore("node:pc") {
		t.Fatalf("eriphe-core behind: %t %v %+v", after, err, r)
	}
	if _, err := c.updateNow("node:laptop"); err != nil || c.pendingUpdate(coreMachine).ID != r.ID {
		t.Fatalf("a second node asks eriphe-core again: %v", err)
	}
	c.followUpdates()
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("a node asked while eriphe-core updates")
	}

	// eriphe-core's update rolled back: it still runs 0.1.0, the nodes
	// would take that same build; they are not asked.
	os.WriteFile(filepath.Join(c.store.dir, "update-status"), []byte("id="+r.ID+"\naction=update\noutcome=rolled_back\nto=0.2.0\n"), 0o600)
	c.resolveUpdates(nil)
	c.followUpdates()
	if c.pendingUpdate("node:pc") != nil || c.pendingUpdate("node:laptop") != nil || c.afterCore("node:pc") || c.afterCore("node:laptop") {
		t.Fatal("the nodes asked after eriphe-core failed to update")
	}

	// Asked again; eriphe-core restarts on 0.2.0, before its first check:
	// the node waits for it, then follows.
	if _, err := c.updateNow("node:pc"); err != nil {
		t.Fatal(err)
	}
	r = c.pendingUpdate(coreMachine)
	os.WriteFile(filepath.Join(c.store.dir, "update-status"), []byte("id="+r.ID+"\naction=update\noutcome=ok\nto=0.2.0\n"), 0o600)
	c.resolveUpdates(nil)
	version = "0.2.0"
	checked := c.updates
	c.updates = newUpdates(checked.base)
	c.followUpdates()
	if c.pendingUpdate("node:pc") != nil || !c.afterCore("node:pc") {
		t.Fatal("followed before the latest versions were read")
	}
	c.updates = checked
	c.followUpdates()
	if c.pendingUpdate("node:pc") == nil || c.afterCore("node:pc") {
		t.Fatal("the node does not follow eriphe-core")
	}

	// eriphe-core without an updater: nothing here can update it.
	version = "0.1.0"
	os.Remove(filepath.Join(c.store.dir, "updater"))
	if _, err := c.updateNow("node:laptop"); !errors.Is(err, errCoreFirst) {
		t.Fatalf("eriphe-core by its command: %v", err)
	}
}

// A request whose updater stopped (handed to it, or "running", and nothing
// since) does not keep the node out of the scheduler for a day: it is
// dropped after stalledAfter, and the machine's line says it stalled. One
// left to a node that runs eriphe-core's version already (its install
// command was run again) is neither sent nor kept.
func TestUpdateStalledAndDone(t *testing.T) {
	c := newUpdateCore(t, "0.1.0")
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true}}`)
	r, err := c.requestUpdate("node:pc", "update", false)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-stalledAfter - time.Minute).UTC().Format(time.RFC3339)
	// Waiting for the node's tasks: not with the updater, kept.
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true,"waiting":"`+r.ID+`"}}`)
	age := func(r *machineUpdate) {
		r.At = time.Now().Add(-stalledAfter - time.Minute)
		raw, _ := json.Marshal(r)
		c.store.set("update_request:node:pc", string(raw))
	}
	age(r)
	c.resolveUpdates(mustNodes(t, c))
	if c.pendingUpdate("node:pc") == nil {
		t.Fatal("a request waiting for the tasks is dropped as stalled")
	}
	// Under way, recently: kept; under way for too long: dropped.
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"running","from":"0.0.1","at":"`+time.Now().UTC().Format(time.RFC3339)+`"}}}`)
	c.resolveUpdates(mustNodes(t, c))
	if c.pendingUpdate("node:pc") == nil {
		t.Fatal("a running update is dropped")
	}
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"running","from":"0.0.1","at":"`+old+`"}}}`)
	c.resolveUpdates(mustNodes(t, c))
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("a stalled update keeps the node")
	}
	for _, n := range c.schedulable(mustNodes(t, c)) {
		if !n.Online {
			t.Fatal("the node stays unschedulable")
		}
	}
	if st := c.stalledStatus("node:pc", parseInfo(mustNodes(t, c)[0]).Update.Status); st == nil || st.Outcome != "stalled" || st.ID != r.ID {
		t.Fatalf("stalled status = %+v", st)
	}
	// Handed to an updater that never takes it: the same.
	r, _ = c.requestUpdate("node:pc", "update", false)
	if st := c.stalledStatus("node:pc", nil); st != nil {
		t.Fatalf("a new request still says stalled: %+v", st)
	}
	setNode(t, c, "pc", `{"version":"0.0.1","update":{"updater":true,"requested":"`+r.ID+`"}}`)
	age(r)
	c.resolveUpdates(mustNodes(t, c))
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("a request the updater never took keeps the node")
	}

	// Reinstalled on eriphe-core's version meanwhile: not sent, dropped.
	r, _ = c.requestUpdate("node:pc", "update", false)
	setNode(t, c, "pc", `{"version":"0.1.0","update":{"updater":true}}`)
	if u := assignmentUpdate(t, c, "pc"); u != nil {
		t.Fatalf("an update sent to a node on eriphe-core's version: %+v", u)
	}
	c.resolveUpdates(mustNodes(t, c))
	if c.pendingUpdate("node:pc") != nil {
		t.Fatal("the request outlives the reinstall")
	}
	// Unless its updater is at it (the same version, recreated).
	r, _ = c.requestUpdate("node:pc", "update", false)
	setNode(t, c, "pc", `{"version":"0.1.0","update":{"updater":true,"status":{"id":"`+r.ID+`","outcome":"running"}}}`)
	c.resolveUpdates(mustNodes(t, c))
	if c.pendingUpdate("node:pc") == nil {
		t.Fatal("a request under way is dropped")
	}
}

// The brain is told once what a machine's update needs of the user, once
// it has been so for a while with no request under way, and again only
// for another need after it was met.
func TestTellNeedsOnce(t *testing.T) {
	c := newUpdateCore(t, "0.1.0")
	setBrain(c, &BrainConfig{})
	events := func() []string {
		t.Helper()
		ms, err := c.store.messagesAfter(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			if m.Role == roleEvent {
				out = append(out, m.Text)
			}
		}
		return out
	}
	needs := `{"os":"darwin","install":"macos","version":"0.1.0","update":{"updater":true,"layout":2,"needs":{"why":"password"}}}`
	setNode(t, c, "mac", needs)
	c.resolveUpdates(mustNodes(t, c))
	if got := events(); len(got) != 0 {
		t.Fatalf("told at once: %v", got)
	}
	// Seen a while ago: told, once.
	c.store.set(needsSeenKey+"node:mac", "password "+time.Now().Add(-needsQuiet-time.Minute).UTC().Format(time.RFC3339))
	c.resolveUpdates(mustNodes(t, c))
	c.resolveUpdates(mustNodes(t, c))
	got := events()
	if len(got) != 1 || !strings.Contains(got[0], "mac") || !strings.Contains(got[0], "Terminal du Mac") ||
		!strings.Contains(got[0], "Cette mise à jour a besoin de toi") {
		t.Fatalf("events: %v", got)
	}
	// Met (the user ran the command): forgotten; a later need is told again.
	setNode(t, c, "mac", `{"os":"darwin","install":"macos","version":"0.1.0","update":{"updater":true,"layout":2}}`)
	c.resolveUpdates(mustNodes(t, c))
	if c.store.get(needsToldKey+"node:mac") != "" || c.store.get(needsSeenKey+"node:mac") != "" {
		t.Fatal("a need met is not forgotten")
	}
	// A request under way: not told meanwhile.
	setNode(t, c, "mac", needs)
	c.store.set(needsSeenKey+"node:mac", "password "+time.Now().Add(-needsQuiet-time.Minute).UTC().Format(time.RFC3339))
	if _, err := c.requestUpdate("node:mac", "update", false); err != nil {
		t.Fatal(err)
	}
	c.tellNeeds(mustNodes(t, c))
	if got := events(); len(got) != 1 {
		t.Fatalf("told during an update: %v", got)
	}
}
