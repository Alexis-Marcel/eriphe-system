package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// eriphe-core's version is a release, a build of main, one from before the
// releases, a local build, or none.
func TestUpdateRequestVersions(t *testing.T) {
	for _, v := range []string{"", "dev", "sha-1234567", "0.1.0", "12.3.40", "0.1.0-dev+d06ac28", "1.0.0-rc.1"} {
		if !(updateRequest{ID: "u-1", Action: "update", Core: v}).valid() {
			t.Errorf("refused: %q", v)
		}
	}
	for _, v := range []string{"v0.1.0", "01.2.3", "0.1.0+", "0.1.0 x", "edge", "0.1.0-dev+a/b"} {
		if (updateRequest{ID: "u-1", Action: "update", Core: v}).valid() {
			t.Errorf("taken: %q", v)
		}
	}
}

func newUpdateNode(t *testing.T, updater bool) *node {
	t.Helper()
	root := t.TempDir()
	if updater {
		os.WriteFile(filepath.Join(root, updaterFile), []byte("version=1\npin=\n"), 0o600)
	}
	return newNode(options{root: root, docker: filepath.Join(root, "no-docker")})
}

// The node hands eriphe-core's request to the updater only once no task is
// at work: an idle one resumes after the restart, a running one would lose
// its turn.
func TestUpdateWaitsForRunningTasks(t *testing.T) {
	n := newUpdateNode(t, true)
	busy := &Task{ID: "t1", State: stateRunning, perms: map[string]*permission{}}
	n.adopt(busy)
	n.adopt(&Task{ID: "t2", State: stateIdle, perms: map[string]*permission{}})
	if !n.upd.request(&updateRequest{ID: "u-1", Action: "update", Core: "sha-1234567"}) {
		t.Fatal("a new request is not taken")
	}
	if n.upd.handOver(n.running()) {
		t.Fatal("handed over while a task runs")
	}
	path := filepath.Join(n.opt.root, requestFile)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("request written while a task runs")
	}
	if got := n.upd.info()["waiting"]; got != "u-1" {
		t.Fatalf("waiting = %v", got)
	}
	busy.setState(stateIdle)
	if !n.upd.handOver(n.running()) {
		t.Fatal("not handed over once idle")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "id=u-1\naction=update\ncore=sha-1234567\nauto=0\n" {
		t.Fatalf("request file:\n%s", raw)
	}
	// The same request again (eriphe-core sends it until it sees the outcome).
	if n.upd.request(&updateRequest{ID: "u-1", Action: "update", Core: "sha-1234567"}) {
		t.Fatal("the same request taken twice")
	}
}

// eriphe-core can only say update or rollback, with its own version: the
// rest is refused, and nothing is written without an updater.
func TestUpdateRequestChecks(t *testing.T) {
	n := newUpdateNode(t, true)
	for _, r := range []updateRequest{
		{ID: "u-1", Action: "install"},
		{ID: "u-1", Action: "update", Core: "ghcr.io/evil/image:latest"},
		{ID: "u-1", Action: "update", Core: "sha-12\nimage=x"},
		{ID: "u-1", Action: "update", Core: "0.2.0\nimage=x"},
		{ID: "u-1", Action: "update", Core: "0.2"},
		{ID: "u-1", Action: "update", Core: "latest"},
		{ID: "../x", Action: "update"},
		{ID: "", Action: "rollback"},
	} {
		if n.upd.request(&r) {
			t.Errorf("taken: %+v", r)
		}
	}
	if !n.upd.request(&updateRequest{ID: "u-2", Action: "rollback", Auto: true}) || !n.upd.handOver(0) {
		t.Fatal("a rollback is not handed over")
	}
	raw, _ := os.ReadFile(filepath.Join(n.opt.root, requestFile))
	if !strings.Contains(string(raw), "action=rollback\n") || !strings.Contains(string(raw), "auto=1\n") {
		t.Fatalf("request file:\n%s", raw)
	}

	bare := newUpdateNode(t, false)
	bare.upd.request(&updateRequest{ID: "u-3", Action: "update"})
	if bare.upd.handOver(0) {
		t.Fatal("handed over without an updater")
	}
	// eriphe-core took it back.
	n.upd.request(&updateRequest{ID: "u-4", Action: "update"})
	n.upd.request(nil)
	if n.upd.handOver(0) {
		t.Fatal("a request taken back is handed over")
	}
}

// The node reports its updater, its pin and the updater's last outcome; a
// request already done (before a restart) is not taken again.
func TestUpdateInfo(t *testing.T) {
	n := newUpdateNode(t, false)
	os.WriteFile(filepath.Join(n.opt.root, updaterFile), []byte("version=1\npin=sha-abcdef0\n"), 0o600)
	os.WriteFile(filepath.Join(n.opt.root, statusFile),
		[]byte("id=u-9\naction=update\noutcome=rolled_back\nmessage=eriphe-node did not connect\nfrom=sha-1111111\nto=sha-2222222\nat=1761600000\n"), 0o600)
	rec := httptest.NewRecorder()
	n.handleNode(rec, httptest.NewRequest(http.MethodGet, "/v1/node", nil))
	var info struct {
		Update struct {
			Updater bool   `json:"updater"`
			Pin     string `json:"pin"`
			Status  struct {
				ID, Outcome, Message, From, To string
				At                             string `json:"at"`
			} `json:"status"`
		} `json:"update"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	u := info.Update
	if !u.Updater || u.Pin != "sha-abcdef0" || u.Status.ID != "u-9" || u.Status.Outcome != "rolled_back" ||
		u.Status.Message != "eriphe-node did not connect" || u.Status.To != "sha-2222222" || !strings.HasPrefix(u.Status.At, "2025-") {
		t.Fatalf("update info: %s", rec.Body)
	}
	if n.upd.request(&updateRequest{ID: "u-9", Action: "update"}) {
		t.Fatal("a request already done is taken again")
	}
}

// The install's layout this build is made for is the installers' own: an
// updater compares theirs with the machine's.
func TestInstallLayout(t *testing.T) {
	for _, c := range []struct{ file, re string }{
		{"../install/install.sh", `(?m)^LAYOUT=([0-9]+)$`},
		{"../install/install.ps1", `(?m)^\$layout = ([0-9]+)\r?$`},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(c.re).FindSubmatch(raw)
		if m == nil {
			t.Fatalf("%s: no layout", c.file)
		}
		if got := string(m[1]); got != strconv.Itoa(installLayout) {
			t.Errorf("%s: layout %s, installLayout %d", c.file, got, installLayout)
		}
	}
}

// What the install waits for from its user: the step the updater left, or
// the install command once on an install from before the layouts, which
// its updater cannot bring; nothing once at this layout, without an
// updater, or under eriphe-launcher, which brings its own.
func TestUpdateNeeds(t *testing.T) {
	needs := func(install, updater, needsFile string) any {
		t.Helper()
		root := t.TempDir()
		if updater != "" {
			os.WriteFile(filepath.Join(root, updaterFile), []byte(updater), 0o600)
		}
		if needsFile != "" {
			os.WriteFile(filepath.Join(root, "update-needs"), []byte(needsFile), 0o600)
		}
		u := &updates{root: root, install: install}
		return u.info()["needs"]
	}
	now := fmt.Sprintf("version=1\npin=\nchannel=\nlayout=%d\n", installLayout)
	for _, c := range []struct {
		name, install, updater, needs, want string
	}{
		{"at its layout", "macos", now, "", ""},
		{"an updater from before the layouts", "linux", "version=1\npin=\n", "", "layout"},
		{"a layout not all there", "macos", "version=1\nlayout=1\n", "", "layout"},
		{"eriphe-launcher", "docker", "version=1\npin=\n", "", ""},
		{"no updater", "", "", "", ""},
		{"the password", "macos", now, "why=password\nlayout=2\n", "password"},
		{"an administrator", "windows", now, "why=admin\n", "admin"},
		{"something else", "macos", now, "why=$(reboot)\n", "layout"},
	} {
		got := needs(c.install, c.updater, c.needs)
		if c.want == "" {
			if got != nil {
				t.Errorf("%s: needs %v", c.name, got)
			}
			continue
		}
		if m, ok := got.(map[string]any); !ok || m["why"] != c.want {
			t.Errorf("%s: needs %v, want %s", c.name, got, c.want)
		}
	}
	// The node reports the layout its updater says.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, updaterFile), []byte(now), 0o600)
	if got := (&updates{root: root}).info()["layout"]; got != installLayout {
		t.Errorf("layout = %v", got)
	}
}
