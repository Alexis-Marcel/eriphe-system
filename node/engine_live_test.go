package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEngineLive runs eriphe-node engine against a real Docker daemon:
// ERIPHE_LIVE_ENGINE is its socket, a throwaway daemon's with nothing of
// Eriphe on it (the test removes the containers carrying the node's
// labels). ERIPHE_LIVE_ENGINE_IMAGE, the node's image built from this
// tree, adds a folder helper and a wake.
func TestEngineLive(t *testing.T) {
	sock := os.Getenv("ERIPHE_LIVE_ENGINE")
	if sock == "" {
		t.Skip("ERIPHE_LIVE_ENGINE=<a throwaway Docker daemon's socket> runs the engine against it")
	}
	api := newDockerAPI(func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", sock) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, l := range nodeLabels {
		if ids, err := api.listLabelled(ctx, l); err != nil {
			t.Fatal(err)
		} else if len(ids) > 0 {
			t.Fatalf("this daemon runs containers labelled %s: use a throwaway one", l)
		}
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work, ro := filepath.Join(base, "work"), filepath.Join(base, "photos")
	os.MkdirAll(work, 0o777)
	os.MkdirAll(ro, 0o755)
	os.Chmod(work, 0o777)
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("hello\n"), 0o644)
	binds := filepath.Join(base, "binds")
	os.WriteFile(binds, []byte(ro+":ro\n"), 0o644)

	const tasks, system, cache = "eriphe-live-tasks", "eriphe-live-system", "eriphe-live-cache"
	if err := api.call(ctx, http.MethodPost, "/networks/create", nil, map[string]any{"Name": system, "Driver": "bridge"}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, l := range nodeLabels {
			ids, _ := api.listLabelled(ctx, l)
			for _, id := range ids {
				api.remove(ctx, id)
			}
		}
		api.call(ctx, http.MethodDelete, "/networks/"+tasks, nil, nil, nil)
		api.call(ctx, http.MethodDelete, "/networks/"+system, nil, nil, nil)
		api.call(ctx, http.MethodDelete, "/volumes/"+cache, nil, nil, nil)
	})

	image := os.Getenv("ERIPHE_LIVE_ENGINE_IMAGE")
	s := &engineServer{d: api, policy: &dockerPolicy{bindFrom: []string{base}, bindFile: binds, resolve: filepath.EvalSymlinks},
		nodeImage: image, taskNetwork: tasks, network: system, cacheVolume: cache, lookup: net.LookupHost}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	o := &options{engineURL: srv.URL, root: base, hostRoot: base, taskMemory: "512m", taskCPUs: "1", serviceMemory: "256m", folderHelper: image}
	e := remoteEngine{o}

	if err := e.ensureNetwork(); err != nil {
		t.Fatalf("task network: %v", err)
	}
	if sn, err := e.taskSubnets(); err != nil || len(sn) == 0 {
		t.Fatalf("subnets: %v %v", sn, err)
	}
	if err := e.pull(ctx, "alpine:3.20"); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := e.pull(ctx, "example.org/eriphe-none:0"); err == nil {
		t.Error("an image that does not exist was pulled")
	}
	if i := e.info(ctx); i.docker == "" || !strings.Contains(strings.Join(i.images, " "), "alpine:3.20") {
		t.Errorf("info: %+v", i)
	}

	// A task to its end: its input, output, folders, hardening and exit.
	stderr := &lockedBuffer{}
	r, err := e.runTask(taskRunSpec{name: "eriphe-task-live1", taskID: "live1", image: "alpine:3.20", entrypoint: "sh",
		cmd: []string{"-c", `cat; echo "$GREETING"; cat /work/hello.txt; id -u; grep CapEff /proc/self/status; ls /cache >/dev/null && echo cache; echo err >&2; exit 3`},
		env: map[string]string{"GREETING": "hi"}, mounts: []mount{{src: work, dst: "/work", readOnly: true}}, stderr: stderr})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	io.WriteString(r.stdin, "in\n")
	r.stdin.Close()
	out, _ := io.ReadAll(r.stdout)
	if err := r.Wait(); err == nil || err.Error() != "exit status 3" {
		t.Errorf("task's end: %v", err)
	}
	if want := "in\nhi\nhello\n0\nCapEff:\t000000000000000b\ncache\n"; string(out) != want {
		t.Errorf("task's output %q, want %q", out, want)
	}
	if stderr.String() != "err\n" {
		t.Errorf("task's stderr %q", stderr.String())
	}

	// A running task: its checks, on both networks, a folder written, stopped.
	r2, err := e.runTask(taskRunSpec{name: "eriphe-task-live2", taskID: "live2", image: "alpine:3.20", entrypoint: "sh",
		cmd: []string{"-c", "sleep 300"}, systemNetwork: true, mounts: []mount{{src: work, dst: "/work"}}})
	if err != nil {
		t.Fatalf("task 2: %v", err)
	}
	go io.Copy(io.Discard, r2.stdout)
	var res execResult
	for i := 0; i < 50; i++ {
		if res, err = e.execSync(ctx, "eriphe-task-live2", execSpec{env: []string{"X=1"}, setup: "export Y=2",
			script: `echo "$1 $X $Y"; touch /work/made; exit 4`, name: "check", args: []string{"a"}, max: 1000}); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || res.exit != 4 || res.output != "a 1 2\n" {
		t.Errorf("check: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(work, "made")); err != nil {
		t.Errorf("the check's file: %v", err)
	}
	var info struct {
		NetworkSettings struct{ Networks map[string]any }
	}
	api.call(ctx, http.MethodGet, "/containers/eriphe-task-live2/json", nil, nil, &info)
	if _, ok := info.NetworkSettings.Networks[tasks]; !ok || len(info.NetworkSettings.Networks) != 2 {
		t.Errorf("task 2's networks: %v", info.NetworkSettings.Networks)
	}
	if _, err := e.execSync(ctx, "eriphe-task-gone", execSpec{script: "true", max: 10}); err == nil {
		t.Error("a check in no container")
	}
	e.stop(ctx, "eriphe-task-live2")
	r2.stdin.Close()
	if err := r2.Wait(); err == nil {
		t.Error("a stopped task ended well")
	}

	// A stdio service.
	rs, target, err := e.runService(serviceRunSpec{name: "eriphe-svc-live", service: "live", image: "alpine:3.20", args: []string{"cat"},
		user: "1000:1000", stdio: true})
	if err != nil || target != "" {
		t.Fatalf("service: %q %v", target, err)
	}
	io.WriteString(rs.stdin, "ping\n")
	if line, err := bufio.NewReader(rs.stdout).ReadString('\n'); err != nil || line != "ping\n" {
		t.Errorf("service: %q %v", line, err)
	}
	rs.stdin.Close()
	if err := rs.Wait(); err != nil {
		t.Errorf("service's end: %v", err)
	}

	// The stray ones removed.
	r3, err := e.runTask(taskRunSpec{name: "eriphe-task-live3", taskID: "live3", image: "alpine:3.20", entrypoint: "sleep", cmd: []string{"300"}})
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, r3.stdout)
	e.removeStray("eriphe.task")
	r3.Wait()
	if ids, _ := api.listLabelled(ctx, "eriphe.task"); len(ids) != 0 {
		t.Errorf("stray tasks left: %v", ids)
	}

	if image == "" {
		t.Log("no ERIPHE_LIVE_ENGINE_IMAGE: no helper nor wake")
		return
	}
	n := newNode(*o)
	if resp, err := runHelper(n, helperReq{Op: "check", Folder: work}, []helperMount{{path: work, readOnly: true}}); err != nil {
		t.Errorf("folder helper: %v", err)
	} else if resp.Error != "" {
		t.Errorf("folder helper: %+v", resp)
	}
	if err := e.wake(ctx, wakeInfo{MACs: []string{"02:00:00:00:00:01"}, Broadcast: "192.0.2.255"}); err != nil {
		t.Errorf("wake: %v", err)
	}
}
