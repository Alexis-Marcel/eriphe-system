package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestImageTag(t *testing.T) {
	for v, want := range map[string]string{
		"0.3.0": "0.3.0", "1.2.3-rc.1": "1.2.3-rc.1", "0.2.0-dev+abc1234": "sha-abc1234", "dev": "latest", "": "latest",
		"0.2.0-dev+nothex!": "latest",
	} {
		if got := imageTag(v); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestParseImageRef(t *testing.T) {
	for in, want := range map[string]imageRef{
		"ghcr.io/alexis-marcel/eriphe-agent-claude:0.3.0": {"ghcr.io", "alexis-marcel/eriphe-agent-claude", "0.3.0"},
		"localhost:5001/eriphe-agent-codex":               {"localhost:5001", "eriphe-agent-codex", "latest"},
		"alpine":                                          {"docker.io", "library/alpine", "latest"},
		"someone/image:1":                                 {"docker.io", "someone/image", "1"},
		"ghcr.io/x/y@sha256:" + strings.Repeat("a", 64):   {"ghcr.io", "x/y", "sha256:" + strings.Repeat("a", 64)},
	} {
		got, err := parseImageRef(in)
		if err != nil || got != want {
			t.Errorf("parseImageRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "x@sha256:12"} {
		if _, err := parseImageRef(bad); err == nil {
			t.Errorf("parseImageRef(%q): no error", bad)
		}
	}
	if (imageRef{Registry: "localhost:5001"}).base() != "http://localhost:5001" || (imageRef{Registry: "ghcr.io"}).base() != "https://ghcr.io" {
		t.Error("only the loopback is reached over plain HTTP")
	}
}

type tarEntry struct {
	name, body, link string
	typ              byte
	mode             int64
}

func layer(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body)), Linkname: e.link}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// fakeRegistry serves one multi-platform image that asks for a token, the
// way ghcr.io does.
type fakeRegistry struct {
	*httptest.Server
	blobs     map[string][]byte
	manifests map[string][]byte
	index     []byte
	pulls     atomic.Int32 // manifest requests by tag
}

func newFakeRegistry(t *testing.T, layers ...[]byte) *fakeRegistry {
	r := &fakeRegistry{blobs: map[string][]byte{}, manifests: map[string][]byte{}}
	var ls []map[string]any
	for _, l := range layers {
		r.blobs[digestOf(l)] = l
		ls = append(ls, map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": digestOf(l), "size": len(l)})
	}
	m, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": mediaOCIManifest, "layers": ls,
		"config": map[string]any{"digest": "sha256:" + strings.Repeat("0", 64)}})
	r.manifests[digestOf(m)] = m
	other := []byte(`{"schemaVersion":2,"mediaType":"` + mediaOCIManifest + `","layers":[]}`)
	r.manifests[digestOf(other)] = other
	arch := "amd64"
	if runtime.GOARCH == "amd64" {
		arch = "arm64"
	}
	r.index, _ = json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": mediaOCIIndex, "manifests": []map[string]any{
		{"digest": digestOf(other), "platform": map[string]string{"os": "linux", "architecture": arch}},
		{"digest": "sha256:" + strings.Repeat("1", 64), "platform": map[string]string{"os": "unknown", "architecture": "unknown"}},
		{"digest": digestOf(m), "platform": map[string]string{"os": "linux", "architecture": runtime.GOARCH}},
	}})
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/token" {
			if req.URL.Query().Get("scope") != "repository:x/agent:pull" {
				http.Error(w, "bad scope", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"token": "t0k"})
			return
		}
		if req.Header.Get("Authorization") != "Bearer t0k" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+r.URL+`/token",service="fake",scope="repository:x/agent:pull"`)
			w.WriteHeader(401)
			return
		}
		switch p := req.URL.Path; {
		case p == "/v2/x/agent/manifests/1.0":
			r.pulls.Add(1)
			w.Header().Set("Content-Type", mediaOCIIndex)
			w.Write(r.index)
		case strings.HasPrefix(p, "/v2/x/agent/manifests/"):
			body, ok := r.manifests[strings.TrimPrefix(p, "/v2/x/agent/manifests/")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Write(body)
		case strings.HasPrefix(p, "/v2/x/agent/blobs/"):
			body, ok := r.blobs[strings.TrimPrefix(p, "/v2/x/agent/blobs/")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Write(body)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.Close)
	return r
}

// The registry client follows a Bearer challenge to the realm on the
// loopback, as httptest's server is: reached as localhost.
func (r *fakeRegistry) image() string {
	return strings.Replace(strings.TrimPrefix(r.URL, "http://"), "127.0.0.1", "localhost", 1) + "/x/agent:1.0"
}

func TestPrepareUnpacksTheAgent(t *testing.T) {
	l := layer(t,
		tarEntry{name: "opt/", typ: tar.TypeDir, mode: 0o755},
		tarEntry{name: "opt/eriphe/", typ: tar.TypeDir, mode: 0o755},
		tarEntry{name: "opt/eriphe/bin/", typ: tar.TypeDir, mode: 0o755},
		tarEntry{name: "opt/eriphe/bin/claude", typ: tar.TypeReg, mode: 0o4755, body: "#!/bin/sh\necho 2.1\n"},
		tarEntry{name: "etc/passwd", typ: tar.TypeReg, mode: 0o644, body: "not ours"},
	)
	reg := newFakeRegistry(t, l)
	root := t.TempDir()
	s := newAgentStore(root, "")
	dir, err := s.prepare(context.Background(), "claude", reg.image())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != filepath.Join(root, "agents", "claude") || s.current("claude") != dir {
		t.Fatalf("prepared in %s, current %q", dir, s.current("claude"))
	}
	fi, err := os.Stat(filepath.Join(dir, "bin", "claude"))
	if err != nil || fi.Mode().Perm() != 0o755 || fi.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("bin/claude: %v, %v (executable, never setuid)", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "etc")); err == nil {
		t.Error("what is out of /opt/eriphe was unpacked")
	}
	// Ready: a task takes it without asking the registry.
	before := reg.pulls.Load()
	if d, err := s.ready(context.Background(), "claude", reg.image()); err != nil || d != dir || reg.pulls.Load() != before {
		t.Errorf("ready: %s, %v, pulls %d → %d", d, err, before, reg.pulls.Load())
	}
	// A new preparation of the same digest downloads nothing more.
	if d, err := s.prepare(context.Background(), "claude", reg.image()); err != nil || d != dir {
		t.Errorf("prepare again: %s, %v", d, err)
	}
	if got := s.readyRuntimes(); !slices.Equal(got, []string{"acp", "claude-code"}) {
		t.Errorf("ready runtimes %v", got)
	}
	// An older version no task runs from goes; one in use stays.
	old := filepath.Join(root, "agents", "claude", strings.Repeat("b", 64))
	used := filepath.Join(root, "agents", "claude", strings.Repeat("c", 64))
	os.MkdirAll(old, 0o755)
	os.MkdirAll(used, 0o755)
	s.acquire(used)
	s.prune("claude", dir)
	if _, err := os.Stat(old); err == nil {
		t.Error("an unused old version is kept")
	}
	if _, err := os.Stat(used); err != nil {
		t.Error("a version in use was removed")
	}
}

func TestPrepareRefusesWhatCouldPointElsewhere(t *testing.T) {
	for name, e := range map[string]tarEntry{
		"symlink":  {name: "opt/eriphe/bin", typ: tar.TypeSymlink, link: "/etc"},
		"hardlink": {name: "opt/eriphe/bin/x", typ: tar.TypeLink, link: "etc/shadow"},
		"escape":   {name: "opt/eriphe/../../etc/x", typ: tar.TypeReg, mode: 0o644, body: "x"},
		"device":   {name: "opt/eriphe/dev", typ: tar.TypeChar},
		"whiteout": {name: "opt/eriphe/.wh.bin", typ: tar.TypeReg, mode: 0o644},
	} {
		t.Run(name, func(t *testing.T) {
			reg := newFakeRegistry(t, layer(t, e))
			s := newAgentStore(t.TempDir(), "")
			if _, err := s.prepare(context.Background(), "claude", reg.image()); err == nil {
				t.Fatal("prepared")
			}
			if s.current("claude") != "" {
				t.Error("made current")
			}
		})
	}
}

func TestPrepareChecksTheLayer(t *testing.T) {
	l := layer(t, tarEntry{name: "opt/eriphe/bin/claude", typ: tar.TypeReg, mode: 0o755, body: "real"})
	reg := newFakeRegistry(t, l)
	// The registry now serves other bytes under the same digest.
	reg.blobs[digestOf(l)] = layer(t, tarEntry{name: "opt/eriphe/bin/claude", typ: tar.TypeReg, mode: 0o755, body: "evil"})
	s := newAgentStore(t.TempDir(), "")
	if _, err := s.prepare(context.Background(), "claude", reg.image()); err == nil || !strings.Contains(err.Error(), "its content is") {
		t.Fatalf("got %v, want a digest mismatch", err)
	}
}

// The node's helpers land in bin/ next to the agent, replaced when they
// change.
func TestTaskHelpers(t *testing.T) {
	helpers := t.TempDir()
	src := filepath.Join(helpers, "linux-"+runtime.GOARCH)
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "eriphe-cred"), []byte("v1"), 0o644)
	os.WriteFile(filepath.Join(src, ".hidden"), []byte("no"), 0o755)
	os.Symlink("/etc/passwd", filepath.Join(src, "link"))
	dir := t.TempDir()
	s := newAgentStore(t.TempDir(), helpers)
	if err := s.taskHelpers(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "bin", "eriphe-cred"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("eriphe-cred: %v, %v", fi, err)
	}
	for _, no := range []string{".hidden", "link"} {
		if _, err := os.Lstat(filepath.Join(dir, "bin", no)); err == nil {
			t.Errorf("%s copied", no)
		}
	}
	os.WriteFile(filepath.Join(src, "eriphe-cred"), []byte("v2"), 0o755)
	s.taskHelpers(dir)
	if raw, _ := os.ReadFile(filepath.Join(dir, "bin", "eriphe-cred")); string(raw) != "v2" {
		t.Errorf("not replaced: %q", raw)
	}
	// No helpers directory: nothing to do.
	if err := newAgentStore(t.TempDir(), filepath.Join(helpers, "none")).taskHelpers(dir); err != nil {
		t.Error(err)
	}
}

// eriphe-core's list wakes the preparer only when it changes.
func TestWantedWakesOnChange(t *testing.T) {
	n := newNode(options{})
	drain := func() bool {
		select {
		case <-n.prep.wake:
			return true
		default:
			return false
		}
	}
	n.wanted(nil) // an older eriphe-core: the default image still
	if !drain() {
		t.Error("the first assignments do not wake it")
	}
	n.wanted(nil)
	if drain() {
		t.Error("woken again for the same list")
	}
	n.wanted(&prepareSpec{Runtimes: []string{"codex", "claude-code"}})
	if !drain() {
		t.Error("a new list does not wake it")
	}
	n.wanted(&prepareSpec{Runtimes: []string{"claude-code", "codex"}})
	if drain() {
		t.Error("the same list in another order wakes it")
	}
}
