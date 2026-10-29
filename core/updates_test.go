package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRegistry answers like GHCR for the images' latest tag: an anonymous
// token, an index with an image per architecture and their attestations,
// the images' manifests and their configuration with the version label.
type fakeRegistry struct {
	srv      *httptest.Server
	versions map[string]string // image → the version of latest; missing: no such image
	single   bool              // latest is a single image, not an index
	down     atomic.Bool       // every answer is a 503
}

func newFakeRegistry(t *testing.T, versions map[string]string) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{versions: versions}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRegistry) serve(w http.ResponseWriter, r *http.Request) {
	if f.down.Load() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/token" {
		image := strings.TrimSuffix(strings.TrimPrefix(r.URL.Query().Get("scope"), "repository:alexis-marcel/"), ":pull")
		fmt.Fprintf(w, `{"token":"tok-%s"}`, image)
		return
	}
	// /v2/alexis-marcel/<image>/(manifests|blobs)/<ref>
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v2/alexis-marcel/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	image, kind, ref := parts[0], parts[1], parts[2]
	v, ok := f.versions[image]
	if !ok || r.Header.Get("Authorization") != "Bearer tok-"+image {
		http.Error(w, "denied", http.StatusUnauthorized)
		return
	}
	switch {
	case kind == "manifests" && ref == "latest" && !f.single:
		if !strings.Contains(r.Header.Get("Accept"), mediaOCIIndex) {
			http.Error(w, "no index accepted", http.StatusNotAcceptable)
			return
		}
		fmt.Fprintf(w, `{"schemaVersion":2,"mediaType":%q,"manifests":[
			{"digest":"sha256:arm64","platform":{"os":"linux","architecture":"arm64"}},
			{"digest":"sha256:att-arm64","platform":{"os":"unknown","architecture":"unknown"}},
			{"digest":"sha256:amd64","platform":{"os":"linux","architecture":"amd64"}},
			{"digest":"sha256:att-amd64","platform":{"os":"unknown","architecture":"unknown"}}]}`, mediaOCIIndex)
	case kind == "manifests" && (ref == "latest" || ref == "sha256:arm64" || ref == "sha256:amd64"):
		fmt.Fprintf(w, `{"schemaVersion":2,"mediaType":%q,"config":{"digest":"sha256:config-%s"}}`, mediaOCIManifest, image)
	case kind == "blobs" && ref == "sha256:config-"+image:
		labels := `{}`
		if v != "" {
			labels = fmt.Sprintf(`{%q:%q}`, versionLabel, v)
		}
		fmt.Fprintf(w, `{"architecture":"amd64","config":{"Labels":%s}}`, labels)
	default:
		http.NotFound(w, r)
	}
}

// The latest versions come from the images' labels, through the index or
// a single image; a build is behind when it is an older release, never when
// it is a local one.
func TestUpdatesLatestVersions(t *testing.T) {
	for _, single := range []bool{false, true} {
		f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.3.0"})
		f.single = single
		u := newUpdates(f.srv.URL + "/")
		u.check(context.Background())
		if got := u.version("eriphe-core"); got != "0.2.0" {
			t.Fatalf("single %t: eriphe-core latest %q", single, got)
		}
		if got := u.version("eriphe-node"); got != "0.3.0" {
			t.Fatalf("single %t: eriphe-node latest %q", single, got)
		}
	}
	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.3.0"})
	u := newUpdates(f.srv.URL)
	u.check(context.Background())
	for _, c := range []struct {
		image, running string
		behind         bool
	}{
		{"eriphe-core", "0.1.0", true},
		{"eriphe-core", "0.2.0", false},
		{"eriphe-core", "dev", false},
		{"eriphe-node", "", false}, // an older node, without a version
		{"eriphe-node", "0.2.0", true},
		{"eriphe-worker", "0.1.0", false}, // not checked: unknown
	} {
		if got := u.behind(c.image, c.running); got != c.behind {
			t.Errorf("behind(%s, %s) = %t", c.image, c.running, got)
		}
	}
	// Before any check, and without updates at all, nothing is behind.
	if newUpdates(f.srv.URL).behind("eriphe-core", "0.1.0") || (*updates)(nil).behind("eriphe-core", "0.1.0") {
		t.Error("behind with no latest version known")
	}
}

// The registry down or an image without its label: nothing is said, the
// last answer stays, and the failure is logged once until a check works.
func TestUpdatesRegistryErrors(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.3.0"})
	u := newUpdates(f.srv.URL)
	u.check(context.Background())
	f.down.Store(true)
	u.check(context.Background())
	u.check(context.Background())
	if n := strings.Count(logs.String(), "updates:"); n != 1 {
		t.Fatalf("%d log lines for a registry down twice:\n%s", n, logs.String())
	}
	if u.version("eriphe-core") != "0.2.0" || !u.behind("eriphe-node", "0.1.0") {
		t.Error("the registry down, the last answer is lost")
	}
	f.down.Store(false)
	u.check(context.Background())
	f.down.Store(true)
	u.check(context.Background())
	if n := strings.Count(logs.String(), "updates:"); n != 2 {
		t.Fatalf("a new failure after a check that worked is not logged: %d lines", n)
	}

	// An image of before the label, and one the registry does not have.
	logs.Reset()
	f2 := newFakeRegistry(t, map[string]string{"eriphe-core": ""})
	u2 := newUpdates(f2.srv.URL)
	u2.check(context.Background())
	if u2.version("eriphe-core") != "" || u2.version("eriphe-node") != "" {
		t.Error("a version without a label")
	}
	if !strings.Contains(logs.String(), "no "+versionLabel+" label") || !strings.Contains(logs.String(), "401") {
		t.Errorf("log: %s", logs.String())
	}

	// Nothing answers at all.
	logs.Reset()
	f2.srv.Close()
	newUpdates(f2.srv.URL).check(context.Background())
	if !strings.Contains(logs.String(), "updates:") {
		t.Error("an unreachable registry is not logged")
	}
}

// The installers say how they installed a machine: its command comes back.
func TestUpdateCommands(t *testing.T) {
	for install, want := range map[string]string{
		"linux":   "curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s",
		"macos":   "curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s",
		"windows": "irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex",
		"docker":  "docker exec eriphe eriphe-launcher update",
	} {
		if got := updateCommands(install); len(got) != 1 || got[0].Cmd != want {
			t.Errorf("%s: %v", install, got)
		}
	}
	// By hand (a compose project like the NAS's): compose first, then the scripts.
	if got := updateCommands(""); len(got) != 3 || got[0].Cmd != "docker compose pull && docker compose up -d" {
		t.Errorf("by hand: %v", got)
	}
}

// GET /v1/nodes and GET /v1/version tell the CLI what is behind, and how
// to update it.
func TestUpdatesAPI(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "0.1.0"
	c, _ := newTestAPI(t)
	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.3.0"})
	c.updates = newUpdates(f.srv.URL)
	c.updates.check(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/nodes", c.handleNodes)
	mux.HandleFunc("GET /v1/version", c.handleVersion)
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	c.store.registerNode("pc", "h")
	c.store.setNodeStatus("pc", true, []byte(`{"version":"0.1.0","install":"windows"}`))
	c.store.registerNode("nas", "h2")
	c.store.setNodeStatus("nas", true, []byte(`{"version":"0.3.0"}`))

	var nodes []struct {
		Name     string   `json:"name"`
		Version  string   `json:"version"`
		Latest   string   `json:"latest"`
		Behind   bool     `json:"update_available"`
		Commands []string `json:"update_commands"`
	}
	if code := call(t, api, "GET", "/v1/nodes", "", &nodes); code != 200 || len(nodes) != 2 {
		t.Fatalf("GET /v1/nodes: %d %+v", code, nodes)
	}
	for _, n := range nodes {
		switch n.Name {
		case "pc":
			if !n.Behind || n.Version != "0.1.0" || n.Latest != "0.3.0" ||
				len(n.Commands) != 1 || !strings.Contains(n.Commands[0], "install.ps1") {
				t.Errorf("pc: %+v", n)
			}
		case "nas":
			if n.Behind || n.Commands != nil || n.Latest != "0.3.0" {
				t.Errorf("nas: %+v", n)
			}
		}
	}

	var v struct {
		Core versionStatus     `json:"core"`
		Node map[string]string `json:"node"`
	}
	if code := call(t, api, "GET", "/v1/version", "", &v); code != 200 {
		t.Fatalf("GET /v1/version: %d", code)
	}
	if !v.Core.Behind || v.Core.Version != "0.1.0" || v.Core.Latest != "0.2.0" || len(v.Core.Commands) == 0 ||
		v.Node["latest"] != "0.3.0" {
		t.Errorf("GET /v1/version: %+v", v)
	}
}

// The workspace marks what is behind, discreetly, with the command for the
// machine's system; nothing when everything is up to date.
func TestWebUpdateNotice(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "0.1.0"
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.registerNode("pc", "h")
	tw.c.store.setNodeStatus("pc", true, []byte(`{"version":"0.1.0","install":"macos"}`))
	tw.c.store.registerNode("nas", "h2")
	tw.c.store.setNodeStatus("nas", true, []byte(`{"version":"0.3.0"}`))

	// No answer from the registry yet: nothing.
	for _, path := range []string{"/settings/machines", "/settings/account"} {
		if _, body, _ := tw.do("GET", path, nil, nil); strings.Contains(body, "Mise à jour disponible") {
			t.Errorf("GET %s tells of a new version before any check", path)
		}
	}

	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.3.0"})
	tw.c.updates = newUpdates(f.srv.URL)
	tw.c.updates.check(context.Background())
	_, body, _ := tw.do("GET", "/settings/machines", nil, nil)
	// eriphe-core and pc are behind, neither has an updater: one command each, for its system.
	if n := strings.Count(body, `<span class="update-available" title=`); n != 2 {
		t.Errorf("%d machines told to update, want eriphe-core and pc", n)
	}
	for _, want := range []string{"Mise à jour disponible", "0.3.0", "curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s<",
		"docker compose pull &amp;&amp; docker compose up -d", `class="new-dot"`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/machines lacks %q", want)
		}
	}
	if strings.Contains(body, "install.ps1 | iex") {
		t.Error("a macOS machine is given the Windows command")
	}
	_, body, _ = tw.do("GET", "/settings/account", nil, nil)
	for _, want := range []string{"eriphe-core 0.1.0", "Mise à jour disponible", `href="/settings/devices#updates"`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/account lacks %q", want)
		}
	}

	// A local build never claims to be behind.
	version = "dev"
	tw.c.store.setNodeStatus("pc", true, []byte(`{"version":"0.3.0"}`))
	for _, path := range []string{"/", "/settings/machines", "/settings/account"} {
		if _, body, _ := tw.do("GET", path, nil, nil); strings.Contains(body, "new-dot") || strings.Contains(body, "update-available") {
			t.Errorf("GET %s tells of a new version with everything up to date", path)
		}
	}
}
