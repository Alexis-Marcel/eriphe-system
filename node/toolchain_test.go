package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeFiles makes a tree of files under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func detectIn(t *testing.T, work string, dirs ...string) map[string]wanted {
	t.Helper()
	root, err := os.OpenRoot(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	got := map[string]wanted{}
	for _, w := range detectToolchains(root, dirs) {
		got[w.kind.kind()] = w
	}
	return got
}

// Each kind's file names the version wanted; the nearest file wins.
func TestDetectToolchains(t *testing.T) {
	work := t.TempDir()
	writeFiles(t, work, map[string]string{
		"repo/go.mod":             "module example.org/app\n\ngo 1.24.2 // the language\n\ntoolchain go1.25.7\n",
		"repo/package.json":       `{"name":"app","engines":{"node":">=20 <23"}}`,
		"repo/web/package.json":   `{"engines":{"node":"^18.2"}}`,
		"repo/web/.nvmrc":         "# pinned\nv22.11.0\n",
		"repo/tools/go.mod":       "module example.org/tools\n\ngo 1.23\n",
		"repo/other/package.json": `{"engines":{"npm":">=9"}}`,
	})
	got := detectIn(t, work, "repo")
	if w := got["go"]; w.want != "1.25.7" || w.source != "repo/go.mod" {
		t.Errorf("go: %+v, want the toolchain line 1.25.7", w)
	}
	if w := got["node"]; w.want != ">=20 <23" || w.source != "repo/package.json" {
		t.Errorf("node: %+v", w)
	}
	got = detectIn(t, work, "repo/web", "repo")
	if w := got["node"]; w.want != "v22.11.0" || w.source != "repo/web/.nvmrc" {
		t.Errorf("node in web: %+v, want .nvmrc before package.json", w)
	}
	if w := got["go"]; w.want != "1.25.7" {
		t.Errorf("go from web: %+v, want the repository's", w)
	}
	got = detectIn(t, work, "repo/tools", "repo")
	if w := got["go"]; w.want != "1.23" || w.source != "repo/tools/go.mod" {
		t.Errorf("go in tools: %+v", w)
	}
	got = detectIn(t, work, "repo/other")
	if _, ok := got["node"]; ok {
		t.Errorf("engines without node: %+v", got["node"])
	}

	// An older toolchain line than the go line: the go line.
	k := goToolchain{}
	read := func(b string) func(string) ([]byte, bool) {
		return func(name string) ([]byte, bool) { return []byte(b), name == "go.mod" }
	}
	if v, _ := k.detect(read("go 1.25.1\ntoolchain go1.24.0\n")); v != "1.25.1" {
		t.Errorf("go 1.25.1 / toolchain go1.24.0: %q", v)
	}
	if v, _ := k.detect(read("go 1.22\ntoolchain default\n")); v != "1.22" {
		t.Errorf("toolchain default: %q", v)
	}
	if v, _ := k.detect(read("go 1.22; rm -rf /\n")); v != "" {
		t.Errorf("a bad go line: %q", v)
	}
}

// The repository's files are the task's: a link out of its work tree, or
// anything but a regular file, is not read.
func TestDetectToolchainsStaysInWork(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	writeFiles(t, dir, map[string]string{"outside/go.mod": "go 1.25.7\n"})
	if err := os.MkdirAll(filepath.Join(work, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "outside", "go.mod"), filepath.Join(work, "repo", "go.mod")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := os.Mkdir(filepath.Join(work, "repo", "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectIn(t, work, "repo"); len(got) != 0 {
		t.Errorf("read through a link or a directory: %+v", got)
	}
}

func TestSearchDirs(t *testing.T) {
	for _, tc := range []struct {
		repo, dir string
		want      []string
	}{
		{"example/app", "", []string{"repo"}},
		{"example/app", "svc/api", []string{"repo/svc/api", "repo/svc", "repo"}},
		{"example/app", "/work/folders/docs", nil},
		{"example/app", "../..", nil},
		{"", "", nil},
	} {
		task := &Task{dir: t.TempDir(), Spec: TaskSpec{Repo: tc.repo, Dir: tc.dir}}
		if got := searchDirs(task); !slices.Equal(got, tc.want) {
			t.Errorf("repo %q dir %q: %v, want %v", tc.repo, tc.dir, got, tc.want)
		}
	}
}

// npm's ranges, as engines.node writes them.
func TestSemverRange(t *testing.T) {
	for _, tc := range []struct {
		r     string
		in    []string
		notIn []string
	}{
		{">=20", []string{"20.0.0", "24.1.0"}, []string{"19.9.9"}},
		{">= 18 < 21", []string{"18.0.0", "20.9.9"}, []string{"21.0.0", "17.0.0"}},
		{"^20.11", []string{"20.11.0", "20.18.1"}, []string{"20.10.9", "21.0.0"}},
		{"~20.11.1", []string{"20.11.1", "20.11.9"}, []string{"20.12.0", "20.11.0"}},
		{"20", []string{"20.0.0", "20.99.1"}, []string{"21.0.0"}},
		{"20.x || 22.x", []string{"20.1.0", "22.3.0"}, []string{"21.0.0", "23.0.0"}},
		{"18 - 20", []string{"18.0.0", "20.5.0"}, []string{"21.0.0"}},
		{">20", []string{"21.0.0"}, []string{"20.5.0"}},
		{"<=20.1", []string{"20.1.9"}, []string{"20.2.0"}},
		{"v22.11.0", []string{"22.11.0"}, []string{"22.11.1"}},
		{"*", []string{"1.0.0"}, nil},
		{"^0.2.3", []string{"0.2.9"}, []string{"0.3.0"}},
	} {
		r, err := parseSemverRange(tc.r)
		if err != nil {
			t.Errorf("%q: %v", tc.r, err)
			continue
		}
		for _, v := range tc.in {
			if sv, _ := parseSemver(v); !r.match(sv) {
				t.Errorf("%q does not match %s", tc.r, v)
			}
		}
		for _, v := range tc.notIn {
			if sv, _ := parseSemver(v); r.match(sv) {
				t.Errorf("%q matches %s", tc.r, v)
			}
		}
	}
	for _, bad := range []string{"latest-ish", ">=a", "1.2.3.4", "<*"} {
		if _, err := parseSemverRange(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestGoVersionOrder(t *testing.T) {
	order := []string{"1.24.9", "1.25", "1.25rc1", "1.25rc2", "1.25.0", "1.25.7", "1.26rc1"}
	for i := 1; i < len(order); i++ {
		if !goVersionLess(order[i-1], order[i]) || goVersionLess(order[i], order[i-1]) {
			t.Errorf("%s < %s expected", order[i-1], order[i])
		}
	}
}

// targz is a release archive: files under top, with their mode, and links.
func targz(t *testing.T, top string, files map[string]string, links map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		mode := int64(0o644)
		if strings.HasPrefix(name, "bin/") {
			mode = 0o4755 // setuid, which must not survive
		}
		tw.WriteHeader(&tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	for name, target := range links {
		tw.WriteHeader(&tar.Header{Name: top + "/" + name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777})
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// fakeReleases is go.dev/dl and nodejs.org/dist, with one Go and one Node
// release for each architecture, and the requests it got.
type fakeReleases struct {
	srv      *httptest.Server
	archives atomic.Int32
	goSum    string // the sum go.dev announces for the arm64 archive; the true one if empty
}

func newFakeReleases(t *testing.T) *fakeReleases {
	t.Helper()
	f := &fakeReleases{}
	goArchive := map[string][]byte{}
	nodeArchive := map[string][]byte{}
	for _, a := range []string{"amd64", "arm64"} {
		goArchive[a] = targz(t, "go", map[string]string{"bin/go": "#!/bin/sh\necho go " + a + "\n", "VERSION": "go1.25.7"}, nil)
		na := nodeArch(a)
		top := "node-v22.11.0-linux-" + na
		nodeArchive[na] = targz(t, top, map[string]string{"bin/node": "#!/bin/sh\n", "lib/node_modules/npm/bin/npm-cli.js": "//"},
			map[string]string{"bin/npm": "../lib/node_modules/npm/bin/npm-cli.js"})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl/" {
			var rels []map[string]any
			for _, v := range []string{"go1.25.7", "go1.25.6", "go1.26rc1", "go1.24.9"} {
				files := []map[string]any{}
				for _, a := range []string{"amd64", "arm64"} {
					s := sha(goArchive[a])
					if a == "arm64" && f.goSum != "" {
						s = f.goSum
					}
					files = append(files, map[string]any{"filename": v + ".linux-" + a + ".tar.gz", "os": "linux", "arch": a, "sha256": s, "kind": "archive"})
				}
				files = append(files, map[string]any{"filename": v + ".darwin-arm64.tar.gz", "os": "darwin", "arch": "arm64", "sha256": strings.Repeat("0", 64), "kind": "archive"})
				rels = append(rels, map[string]any{"version": v, "stable": !strings.Contains(v, "rc"), "files": files})
			}
			json.NewEncoder(w).Encode(rels)
			return
		}
		f.archives.Add(1)
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		for a, b := range goArchive {
			if strings.HasSuffix(name, ".linux-"+a+".tar.gz") {
				w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/dist/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"v25.1.0","files":["linux-x64","linux-arm64"],"lts":false},
{"version":"v22.11.0","files":["linux-x64","linux-arm64"],"lts":"Jod"},
{"version":"v22.10.0","files":["linux-x64","linux-arm64"],"lts":false},
{"version":"v20.18.0","files":["linux-x64","linux-arm64"],"lts":"Iron"}]`)
	})
	mux.HandleFunc("/dist/v22.11.0/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHASUMS256.txt") {
			for a, b := range nodeArchive {
				fmt.Fprintf(w, "%s  node-v22.11.0-linux-%s.tar.gz\n", sha(b), a)
			}
			return
		}
		f.archives.Add(1)
		for a, b := range nodeArchive {
			if strings.HasSuffix(r.URL.Path, "-linux-"+a+".tar.gz") {
				w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeReleases) store(root, arch string) *toolchainStore {
	s := newToolchainStore(root)
	s.arch, s.client = arch, f.srv.Client()
	s.goDL, s.nodeDist = f.srv.URL+"/dl/", f.srv.URL+"/dist/"
	return s
}

// The release is the one for the tasks' architecture, kept under its own
// name; the next task takes it without a download.
func TestToolchainEnsure(t *testing.T) {
	f := newFakeReleases(t)
	ctx := context.Background()
	for _, tc := range []struct {
		arch, want, kind, name string
	}{
		{"arm64", "1.25.7", "go", "go1.25.7-linux-arm64"},
		{"amd64", "1.25", "go", "go1.25.7-linux-amd64"},      // the newest stable 1.25.x
		{"amd64", ">=20", "node", "node-v22.11.0-linux-x64"}, // the newest LTS, not 25
		{"arm64", "lts/*", "node", "node-v22.11.0-linux-arm64"},
	} {
		root := t.TempDir()
		s := f.store(root, tc.arch)
		var k toolchainKind = goToolchain{}
		if tc.kind == "node" {
			k = nodeToolchain{}
		}
		before := f.archives.Load()
		name, err := s.ensure(ctx, wanted{kind: k, want: tc.want})
		if err != nil {
			t.Fatalf("%s %s on %s: %v", tc.kind, tc.want, tc.arch, err)
		}
		if name != tc.name {
			t.Errorf("%s %s on %s: %s, want %s", tc.kind, tc.want, tc.arch, name, tc.name)
		}
		dir := filepath.Join(root, "toolchains", name)
		bin := filepath.Join(dir, "bin", tc.kind)
		fi, err := os.Stat(bin)
		if err != nil {
			t.Fatalf("%s: %v", bin, err)
		}
		if fi.Mode().Perm() != 0o755 || fi.Mode()&os.ModeSetuid != 0 {
			t.Errorf("%s: mode %v, want 0755 without setuid", bin, fi.Mode())
		}
		if tc.kind == "go" {
			b, _ := os.ReadFile(bin)
			if !strings.Contains(string(b), tc.arch) {
				t.Errorf("go for the wrong arch: %q", b)
			}
		} else if target, err := os.Readlink(filepath.Join(dir, "bin", "npm")); err != nil || target != "../lib/node_modules/npm/bin/npm-cli.js" {
			t.Errorf("npm link: %q %v", target, err)
		}
		if f.archives.Load() != before+1 {
			t.Errorf("%d archives downloaded, want 1", f.archives.Load()-before)
		}
		if again, err := s.ensure(ctx, wanted{kind: k, want: tc.want}); err != nil || again != name {
			t.Errorf("again: %s %v", again, err)
		}
		if f.archives.Load() != before+1 {
			t.Error("downloaded again")
		}
		if left, _ := filepath.Glob(filepath.Join(root, "toolchains", ".partial-*")); len(left) != 0 {
			t.Errorf("left behind: %v", left)
		}
	}
}

// An archive whose checksum is not the publisher's is refused, and nothing
// of it is kept.
func TestToolchainChecksumMismatch(t *testing.T) {
	f := newFakeReleases(t)
	f.goSum = strings.Repeat("ab", 32)
	root := t.TempDir()
	s := f.store(root, "arm64")
	_, err := s.ensure(context.Background(), wanted{kind: goToolchain{}, want: "1.25.7"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "toolchains"))
	if len(entries) != 0 {
		t.Errorf("kept: %v", entries)
	}
}

// A release unpacks into its own tree only.
func TestUntarToolchainRefuses(t *testing.T) {
	for name, archive := range map[string][]byte{
		"a link out":       targz(t, "go", map[string]string{"bin/go": "x"}, map[string]string{"bin/evil": "../../../etc/passwd"}),
		"an absolute link": targz(t, "go", nil, map[string]string{"bin/evil": "/etc/passwd"}),
		"a path out":       targz(t, "go", map[string]string{"../../escape": "x"}, nil),
		"another top":      targz(t, "other", map[string]string{"bin/go": "x"}, nil),
	} {
		dst := t.TempDir()
		if err := untarToolchain(bytes.NewReader(archive), dst, "go"); err == nil {
			t.Errorf("%s: unpacked", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape")); err == nil {
			t.Errorf("%s: wrote out of the tree", name)
		}
	}
}

func TestPickNode(t *testing.T) {
	rels := []nodeRelease{
		{Version: "v25.1.0", Files: []string{"linux-x64"}},
		{Version: "v22.11.0", Files: []string{"linux-x64"}, LTS: "Jod"},
		{Version: "v20.18.0", Files: []string{"linux-x64"}, LTS: "Iron"},
		{Version: "v20.19.0", Files: []string{"linux-arm64"}, LTS: "Iron"}, // not for x64
	}
	for want, ver := range map[string]string{
		"20": "v20.18.0", "lts/iron": "v20.18.0", "node": "v25.1.0", ">=23": "v25.1.0", "^22": "v22.11.0", "v20.18.0": "v20.18.0",
	} {
		got, err := pickNode(rels, want, "linux-x64")
		if err != nil || got != ver {
			t.Errorf("%q: %s %v, want %s", want, got, err, ver)
		}
	}
	if _, err := pickNode(rels, "19", "linux-x64"); err == nil {
		t.Error("19 found")
	}
}

// The node prepares what the repository names, keeps the environment for
// the task, and its checks run with it: the same PATH and caches as the
// agent.
func TestToolchainsReachChecks(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	f := newFakeReleases(t)
	cli, cliDir := fakeCLI(t, `printf '%s\n' "$@" > "$d/exec"; printf '\n`+execExit+`0\n'`)
	root := filepath.Join(t.TempDir(), "root")
	n := newNode(options{root: root, hostRoot: root, docker: cli, cacheVolume: "eriphe-cache"})
	n.toolchains = f.store(root, "arm64")
	task := &Task{ID: "t-1", secret: "s3cret", dir: filepath.Join(root, "tasks", "t-1"), State: stateRunning,
		Spec: TaskSpec{Repo: "example/app", Runtime: "claude-code", Checks: []TaskCheck{{Name: "build", Run: "go build ./..."}}}}
	writeFiles(t, filepath.Join(task.dir, "work"), map[string]string{
		"repo/go.mod":  "module example.org/app\n\ngo 1.25.7\n",
		"repo/.nvmrc":  "22.11.0\n",
		"repo/main.go": "package main\n",
	})
	n.tasks[task.ID] = task

	// The entrypoint's request, with the task's secret only.
	h := n.toolchainHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, toolchainPath, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without the secret: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, toolchainPath, nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Eriphe-Toolchain-Error") != "" {
		t.Fatalf("%d %s: %s", rec.Code, rec.Header().Get("X-Eriphe-Toolchain-Error"), rec.Body)
	}
	want := []string{
		"ERIPHE_TOOLCHAIN_PATH=/opt/eriphe-toolchains/go1.25.7-linux-arm64/bin:/opt/eriphe-toolchains/node-v22.11.0-linux-arm64/bin",
		"GOCACHE=/cache/go/build", "GOMODCACHE=/cache/go/mod", "npm_config_cache=/cache/npm",
	}
	got := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if !slices.Equal(got, want) {
		t.Errorf("environment:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The check: the same variables, and the same PATH put first.
	if _, ok := n.runChecks(task); !ok {
		t.Fatal("the check failed")
	}
	raw, _ := os.ReadFile(filepath.Join(cliDir, "exec"))
	args := strings.Split(string(raw), "\n")
	for _, kv := range want {
		if i := slices.Index(args, kv); i < 1 || args[i-1] != "-e" {
			t.Errorf("the check misses -e %s: %q", kv, args)
		}
	}
	if !strings.Contains(string(raw), "\n-c\n"+toolchainPathScript+"\n(\ncd \"$1\"") {
		t.Errorf("the check's script does not put the toolchains first in PATH: %q", args)
	}
	if slices.Index(args, containerName(task)) < slices.Index(args, "-e") {
		t.Errorf("-e after the container's name: %q", args)
	}

	// The task's container mounts the toolchains read-only and knows where
	// to ask.
	ms, groups, err := n.taskMounts(task)
	if err != nil {
		t.Fatal(err)
	}
	mounts := n.render(t, ms, groups)
	if !slices.Contains(mounts, mountSpec("type=bind", "src="+filepath.Join(root, "toolchains"), "dst="+toolchainMount, "readonly")) {
		t.Errorf("no read-only toolchains mount: %q", mounts)
	}
	if env := n.taskEnv(task, nil); env["ERIPHE_TOOLCHAIN_URL"] == "" || !strings.HasSuffix(env["ERIPHE_TOOLCHAIN_URL"], toolchainPath) {
		t.Errorf("ERIPHE_TOOLCHAIN_URL = %q", env["ERIPHE_TOOLCHAIN_URL"])
	}
	noRepo := &Task{ID: "t-2", dir: filepath.Join(root, "tasks", "t-2"), Spec: TaskSpec{Runtime: "claude-code"}}
	if env := n.taskEnv(noRepo, nil); env["ERIPHE_TOOLCHAIN_URL"] != "" {
		t.Error("a task without a repository asks for toolchains")
	}
}

// The entrypoint's part: the answer's lines become the environment, and
// the toolchains come first in PATH; anything else in the answer is not
// run.
func TestEntrypointToolchainEnv(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	start := strings.Index(entrypoint, "if [ -n \"$ERIPHE_TOOLCHAIN_URL\" ]")
	end := strings.Index(entrypoint, "exec \"$@\"")
	if start < 0 || end < start {
		t.Fatal("the entrypoint has no toolchain step")
	}
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	writeFiles(t, bin, map[string]string{"curl": "#!/bin/sh\nprintf '%s\\n' 'ERIPHE_TOOLCHAIN_PATH=/opt/eriphe-toolchains/go/bin' 'GOMODCACHE=/cache/go mod' '$(touch " + marker + ")' 'X=$(touch " + marker + ")' 'not a variable'\n"})
	os.Chmod(filepath.Join(bin, "curl"), 0o755)
	script := entrypoint[start:end] + `printf '%s|%s\n' "$PATH" "$GOMODCACHE"`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "ERIPHE_TOOLCHAIN_URL=http://192.0.2.1/v1/task/toolchains", "ERIPHE_TASK_SECRET=s"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "/opt/eriphe-toolchains/go/bin:"+bin+":/usr/bin:/bin|/cache/go mod"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the answer ran as a command")
	}
}

// A toolchain unused for 30 days goes, unless a running task has it.
func TestToolchainPrune(t *testing.T) {
	s := newToolchainStore(t.TempDir())
	old := time.Now().Add(-31 * 24 * time.Hour)
	for _, name := range []string{"go1.24.0-linux-arm64", "go1.25.7-linux-arm64", "node-v20.18.0-linux-arm64"} {
		if err := os.MkdirAll(filepath.Join(s.dir, name, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if name != "go1.25.7-linux-arm64" {
			os.Chtimes(filepath.Join(s.dir, name), old, old)
		}
	}
	s.acquire("t-1", []string{"node-v20.18.0-linux-arm64"})
	s.prune()
	for name, kept := range map[string]bool{"go1.24.0-linux-arm64": false, "go1.25.7-linux-arm64": true, "node-v20.18.0-linux-arm64": true} {
		if s.has(name) != kept {
			t.Errorf("%s kept: %v, want %v", name, s.has(name), kept)
		}
	}
	s.release("t-1")
	s.prune()
	if s.has("node-v20.18.0-linux-arm64") {
		t.Error("kept once its task ended")
	}
}
