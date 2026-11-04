package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The engine follows -engine, and says where it differs.
func TestEngineChoice(t *testing.T) {
	if e, ok := newNode(options{}).engine().(dockerEngine); !ok || e.traits() != (engineTraits{}) {
		t.Errorf("default: %T %+v", e, e.traits())
	}
	n := newNode(options{engine: engineContainer})
	if e, ok := n.engine().(appleEngine); !ok || e.traits() != (engineTraits{ownNetworks: true, explores: true, mapsOwners: true}) {
		t.Errorf("container: %T", n.engine())
	}
	n.opt.folderHelper = "eriphe-node:1"
	if n.helper() {
		t.Error("folder helpers with an engine that explores")
	}
	f := &fakeEngine{}
	n.eng = f
	if n.engine() != engine(f) {
		t.Error("a test's engine is not used")
	}
}

// A check runs in the task's container with its toolchains, and its exit
// code comes back apart from what could not run.
func TestExecTaskWithEngine(t *testing.T) {
	f := &fakeEngine{exec: func(container string, spec execSpec) (execResult, error) {
		if container != "eriphe-task-t1" {
			return execResult{exit: -1}, errors.New("not the task's container")
		}
		return execResult{output: "ran " + strings.Join(spec.args, " "), exit: 3}, nil
	}}
	n := newNode(options{})
	n.eng = f
	task := &Task{ID: "t1"}
	out, code, err := n.execTask(context.Background(), task, 100, "test -e \"$1\"", "/work/x")
	if err != nil || code != 3 || out != "ran /work/x" {
		t.Fatalf("got %q %d %v", out, code, err)
	}
	spec := f.execs[0]
	if spec.setup != toolchainPathScript || spec.script != "test -e \"$1\"" || spec.name != "eriphe-check" || spec.max != 100 {
		t.Errorf("spec %+v", spec)
	}
	f.exec = func(string, execSpec) (execResult, error) { return execResult{exit: -1}, errors.New("refused") }
	if _, code, err := n.execTask(context.Background(), task, 100, "true"); err == nil || code != -1 {
		t.Errorf("a check that could not run: %d %v", code, err)
	}
}

// A task's container is a typed specification: its workspace and profile
// in the node's root, its environment by name, the entrypoint before the
// agent; and its own network with an engine that makes one.
func TestTaskRunSpec(t *testing.T) {
	root := t.TempDir()
	for _, own := range []bool{false, true} {
		n := newNode(options{root: root, hostRoot: root})
		n.eng = &fakeEngine{tr: engineTraits{ownNetworks: own}}
		task := &Task{ID: "t1", dir: filepath.Join(root, "tasks", "t1"), Spec: TaskSpec{Image: "eriphe-base", Repo: "o/r", SystemNetwork: true}}
		if err := n.startTaskNetwork(task); err != nil {
			t.Fatal(err)
		}
		spec, err := n.taskRun(task, []string{"agent", "--go"}, map[string]string{"AGENT": "1"})
		if err != nil {
			t.Fatal(err)
		}
		if spec.name != "eriphe-task-t1" || spec.taskID != "t1" || spec.image != "eriphe-base" || !spec.systemNetwork {
			t.Errorf("spec %+v", spec)
		}
		if want := map[bool]string{false: "", true: "eriphe-task-t1"}[own]; spec.network != want {
			t.Errorf("own networks %v: network %q", own, spec.network)
		}
		if spec.entrypoint != "sh" || !slices.Equal(spec.cmd, []string{"-c", entrypoint, "eriphe", "agent", "--go"}) {
			t.Errorf("command %q %q", spec.entrypoint, spec.cmd)
		}
		if spec.env["AGENT"] != "1" || spec.env["ERIPHE_REPO"] != "https://github.com/o/r.git" {
			t.Errorf("env %v", spec.env)
		}
		work := mount{src: filepath.Join(task.dir, "work"), dst: "/work", inRoot: true}
		if !slices.Contains(spec.mounts, work) || len(spec.groups) != 0 {
			t.Errorf("mounts %+v, groups %v", spec.mounts, spec.groups)
		}
		for _, m := range spec.mounts {
			if !m.inRoot {
				t.Errorf("a mount outside the root: %+v", m)
			}
		}
		n.endTaskNetwork(task)
		if task.network != "" {
			t.Error("the network is kept")
		}
	}
}

// Stopping a task, and cleaning up after a restart, go to the engine.
func TestStopAndStrayWithEngine(t *testing.T) {
	f := &fakeEngine{}
	n := newNode(options{})
	n.eng = f
	n.stopContainer(&Task{ID: "t1"})
	n.stopServiceContainer("mail")
	n.removeStrayServices()
	if !slices.Equal(f.stopped, []string{"eriphe-task-t1", "eriphe-svc-mail"}) || !slices.Equal(f.stray, []string{"eriphe.service"}) {
		t.Errorf("stopped %v, stray %v", f.stopped, f.stray)
	}
}

// An engine without a way to the host's network wakes from here only,
// with no error.
func TestWakeWithoutHost(t *testing.T) {
	defer func(s func(wakeInfo) error, v func() hostView) { sendWake, wakeHost = s, v }(sendWake, wakeHost)
	sendWake = func(wakeInfo) error { return nil }
	wakeHost = func() hostView {
		return hostView{goos: "linux", getenv: func(string) string { return "" },
			readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
			exists:   func(p string) bool { return p == "/.dockerenv" }}
	}
	n := newNode(options{})
	n.eng = &fakeEngine{wakeErr: errors.ErrUnsupported}
	rec := httptest.NewRecorder()
	n.handleWake(rec, httptest.NewRequest(http.MethodPost, "/v1/wake", strings.NewReader(`{"macs":["00:00:5e:00:53:01"]}`)))
	var out struct {
		Sent   []string `json:"sent"`
		Errors []string `json:"errors"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || !slices.Equal(out.Sent, []string{"here"}) || len(out.Errors) != 0 {
		t.Errorf("%d %+v", rec.Code, out)
	}
}

// The node reports its engine's version and images; without a version, no
// runtimes nor services.
func TestNodeInfoFromEngine(t *testing.T) {
	n := newNode(options{engine: engineContainer})
	f := &fakeEngine{inf: engineInfo{version: "0.6.0", images: []string{"alpine:3.22"}}}
	n.eng = f
	get := func() (out struct {
		Docker   string   `json:"docker"`
		Version  string   `json:"engine_version"`
		Images   []string `json:"images"`
		Runtimes []string `json:"runtimes"`
		Services bool     `json:"services"`
	}) {
		rec := httptest.NewRecorder()
		n.handleNode(rec, httptest.NewRequest(http.MethodGet, "/v1/node", nil))
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if out := get(); out.Docker != "" || out.Version != "0.6.0" || !slices.Equal(out.Images, []string{"alpine:3.22"}) || len(out.Runtimes) == 0 || !out.Services {
		t.Errorf("%+v", out)
	}
	f.inf = engineInfo{}
	if out := get(); len(out.Runtimes) != 0 || out.Services {
		t.Errorf("no engine: %+v", out)
	}
}

// A helper is hardened by the engine, whatever its specification says: no
// network, a read-only root, every capability dropped but a task's three.
func TestHelperArgs(t *testing.T) {
	spec := helperRunSpec{image: "img", entrypoint: "/bin/x", args: []string{"go"}, stdin: []byte("{}"), label: "eriphe.helper=check",
		mounts: []mount{{src: "/data/a", dst: "/data/a", readOnly: true}}}
	o := options{}
	docker := strings.Join(dockerEngine{&o}.helperArgs(spec), " ")
	apple, err := appleEngine{&o, nil}.helperArgs(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{docker, strings.Join(apple, " ")} {
		for _, want := range []string{"run --rm", "-i", "--network none", "--read-only", "--user 0:0", "--cap-drop ALL",
			"--label eriphe.helper=check", "--entrypoint /bin/x"} {
			if !strings.Contains(args, want) {
				t.Errorf("%q: no %q", args, want)
			}
		}
		if !strings.HasSuffix(args, " img go") || strings.Contains(args, "--privileged") || strings.Contains(args, "SYS_ADMIN") {
			t.Errorf("privileges: %q", args)
		}
	}
	if !strings.Contains(docker, "--security-opt no-new-privileges") || !strings.Contains(docker, "--mount type=bind,src=/data/a,dst=/data/a,readonly") {
		t.Errorf("docker: %q", docker)
	}
	spec.user = "1000:1000"
	if args := strings.Join(dockerEngine{&o}.helperArgs(spec), " "); strings.Contains(args, "--cap-add") || !strings.Contains(args, "--user 1000:1000") {
		t.Errorf("as a user: %q", args)
	}
	spec.mounts[0].src = "/data/a,b"
	if _, err := (appleEngine{&o, nil}).helperArgs(spec); err == nil {
		t.Error("a comma in a path, mounted on a Mac")
	}
}

// Only the engines run their tools, and nothing asks which engine it has.
func TestEngineBoundaries(t *testing.T) {
	engineFiles := map[string]bool{"engine.go": true, "engine_docker.go": true, "apple.go": true, "apple_service.go": true}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || engineFiles[name] {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(x ast.Node) bool {
			sel, ok := x.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch {
			case sel.Sel.Name == "docker":
				// -docker is the engine's CLI: only the flags set it.
				if id, ok := sel.X.(*ast.SelectorExpr); ok && id.Sel.Name == "opt" {
					t.Errorf("%s: the engine's CLI outside the engines", fset.Position(sel.Pos()))
				}
			case sel.Sel.Name == "engine":
				if id, ok := sel.X.(*ast.SelectorExpr); ok && id.Sel.Name == "opt" && name != "task.go" {
					t.Errorf("%s: asks which engine it has", fset.Position(sel.Pos()))
				}
			}
			return true
		})
	}
}

// A task whose folder another task may write waits for it: it says so
// once, tries again, and runs once the engine can make it; stopped while
// it waits, it stops waiting.
func TestTaskWaitsForItsFolder(t *testing.T) {
	defer func(r, w time.Duration) { busyRetry, busyWait = r, w }(busyRetry, busyWait)
	busyRetry, busyWait = time.Millisecond, time.Minute
	root := t.TempDir()
	n := newNode(options{root: root, hostRoot: root})
	f := &fakeEngine{busy: 3}
	n.eng = f
	task := &Task{ID: "t1", perms: map[string]*permission{}}
	r, err := n.runTaskWhenFree(task, taskRunSpec{name: "eriphe-task-t1", taskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	r.stdin.Close()
	retries := 0
	task.mu.Lock()
	events := slices.Clone(task.events)
	task.mu.Unlock()
	for _, ev := range events {
		if ev.Type == "retry" {
			retries++
		}
	}
	if retries != 1 || len(f.tasks) != 1 {
		t.Errorf("said %d times, ran %d times", retries, len(f.tasks))
	}
	f.busy = 1000
	stopped := &Task{ID: "t2", stopping: true, perms: map[string]*permission{}}
	if _, err := n.runTaskWhenFree(stopped, taskRunSpec{name: "eriphe-task-t2", taskID: "t2"}); !errors.Is(err, errStoppedWaiting) {
		t.Errorf("stopped while it waits: %v", err)
	}
}
