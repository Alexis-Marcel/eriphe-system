package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// apiDaemon is a Docker daemon's API, enough for eriphe-node engine: a
// container echoes its input on stdout, says "bye" on stderr when its
// input ends, and exits with its exit code (0 unless set); a command run in
// one prints "checked" and exits 3.
type apiDaemon struct {
	mu      sync.Mutex
	got     []string
	created map[string]apiContainer
	labels  map[string]map[string]string // the containers there, by name
	done    map[string]chan struct{}
	exit    map[string]int
	missing map[string]bool // images it does not have until pulled
	nets    map[string]*fakeNet
}

// fakeNet is a network of the fake daemon: who is on it, at which address.
type fakeNet struct {
	internal bool
	subnet   string
	labels   map[string]string
	options  map[string]string
	members  map[string]string // container name: its address, with the prefix
}

func (n *fakeNet) json(name string) map[string]any {
	members := map[string]any{}
	for c, ip := range n.members {
		members["id-"+c] = map[string]any{"Name": c, "IPv4Address": ip}
	}
	gw := netip.MustParsePrefix(n.subnet).Addr().Next().String()
	return map[string]any{"Name": name, "Internal": n.internal, "Labels": n.labels, "Options": n.options,
		"IPAM": map[string]any{"Config": []map[string]string{{"Subnet": n.subnet, "Gateway": gw}}}, "Containers": members}
}

func newAPIDaemon(t *testing.T) (*apiDaemon, *dockerAPI) {
	t.Helper()
	d := &apiDaemon{created: map[string]apiContainer{}, labels: map[string]map[string]string{},
		done: map[string]chan struct{}{}, exit: map[string]int{}, missing: map[string]bool{},
		nets: map[string]*fakeNet{"eriphe-tasks": {internal: true, subnet: "172.30.0.0/16", members: map[string]string{},
			options: map[string]string{"com.docker.network.bridge.gateway_mode_ipv4": "isolated"}},
			"eriphe-system":   {subnet: "172.29.0.0/16", members: map[string]string{}},
			"eriphe-services": {internal: true, subnet: "172.28.0.0/16", members: map[string]string{}}}}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	return d, newDockerAPI(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	})
}

func (d *apiDaemon) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.got)
}

func (d *apiDaemon) container(name string) (apiContainer, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.created[name]
	return c, ok
}

func (d *apiDaemon) creates() int {
	n := 0
	for _, c := range d.calls() {
		if strings.HasPrefix(c, "POST /containers/create") {
			n++
		}
	}
	return n
}

func frame(kind byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = kind
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func (d *apiDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	call := r.Method + " " + p
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	d.mu.Lock()
	d.got = append(d.got, call)
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	last := parts[len(parts)-1]
	notFound := func(what string) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message":"No such %s"}`, what)
	}
	switch {
	case p == "/version":
		io.WriteString(w, `{"Version":"27.0.0","ApiVersion":"1.47"}`)
	case p == "/images/json":
		io.WriteString(w, `[{"RepoTags":["example.org/eriphe-base:1"]},{"RepoTags":["<none>:<none>"]}]`)
	case p == "/images/create":
		img := r.URL.Query().Get("fromImage") + ":" + r.URL.Query().Get("tag")
		if strings.Contains(img, "denied") {
			io.WriteString(w, `{"status":"Pulling"}`+"\n"+`{"error":"pull access denied"}`)
			return
		}
		d.mu.Lock()
		delete(d.missing, img)
		d.mu.Unlock()
		io.WriteString(w, `{"status":"Downloaded"}`)
	case p == "/containers/create":
		var c apiContainer
		json.NewDecoder(r.Body).Decode(&c)
		name := r.URL.Query().Get("name")
		d.mu.Lock()
		missing := d.missing[c.Image]
		if !missing {
			d.created[name], d.labels[name], d.done[name] = c, c.Labels, make(chan struct{})
		}
		d.mu.Unlock()
		if missing {
			notFound("image: " + c.Image)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"Id":%q}`, name)
	case p == "/containers/json":
		var f map[string][]string
		json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
		var out []map[string]string
		d.mu.Lock()
		for name, labels := range d.labels {
			if _, ok := labels[f["label"][0]]; ok {
				out = append(out, map[string]string{"Id": name})
			}
		}
		d.mu.Unlock()
		json.NewEncoder(w).Encode(out)
	case p == "/networks" && r.Method == http.MethodGet:
		var f map[string][]string
		json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
		var out []map[string]any
		d.mu.Lock()
		for name, n := range d.nets {
			k, v, _ := strings.Cut(strings.Join(f["label"], ""), "=")
			if k == "" || n.labels[k] == v {
				out = append(out, n.json(name))
			}
		}
		d.mu.Unlock()
		json.NewEncoder(w).Encode(out)
	case p == "/networks/create":
		var c struct {
			Name     string
			Internal bool
			Options  map[string]string
			Labels   map[string]string
			IPAM     struct{ Config []struct{ Subnet string } }
		}
		json.NewDecoder(r.Body).Decode(&c)
		d.mu.Lock()
		_, there := d.nets[c.Name]
		if !there {
			n := &fakeNet{internal: c.Internal, labels: c.Labels, options: c.Options, members: map[string]string{}}
			if len(c.IPAM.Config) > 0 {
				n.subnet = c.IPAM.Config[0].Subnet
			}
			d.nets[c.Name] = n
		}
		d.mu.Unlock()
		if there {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"message":"network exists"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{}`)
	case parts[0] == "networks":
		d.mu.Lock()
		n, ok := d.nets[parts[1]]
		d.mu.Unlock()
		if !ok {
			notFound("network")
			return
		}
		var c struct{ Container string }
		json.NewDecoder(r.Body).Decode(&c)
		d.mu.Lock()
		switch {
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(n.json(parts[1]))
		case last == "connect":
			a := netip.MustParsePrefix(n.subnet).Addr().Next().Next()
			for range n.members {
				a = a.Next()
			}
			n.members[c.Container] = netip.PrefixFrom(a, netip.MustParsePrefix(n.subnet).Bits()).String()
			w.WriteHeader(http.StatusOK)
		case last == "disconnect":
			delete(n.members, c.Container)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			if len(n.members) > 0 {
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"message":"has active endpoints"}`)
			} else {
				delete(d.nets, parts[1])
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
		d.mu.Unlock()
	case parts[0] == "containers" && last == "json":
		d.mu.Lock()
		labels, ok := d.labels[parts[1]]
		d.mu.Unlock()
		if !ok {
			notFound("container")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": parts[1], "Name": "/" + parts[1],
			"Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": true}})
	case parts[0] == "containers" && last == "exec":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"Id":"e-%s"}`, parts[1])
	case parts[0] == "exec" && last == "json":
		io.WriteString(w, `{"Running":false,"ExitCode":3}`)
	case parts[0] == "exec" && last == "start":
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		conn.Write(append(frame(frameStdout, "checked\n"), frame(frameStderr, "warned\n")...))
		conn.Close()
	case last == "attach":
		d.mu.Lock()
		done := d.done[parts[1]]
		d.mu.Unlock()
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer close(done)
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		if r.URL.Query().Get("stdin") != "1" {
			conn.Write(frame(frameStdout, "sent\n"))
			return
		}
		b := make([]byte, 4096)
		for {
			n, err := buf.Read(b)
			if n > 0 {
				conn.Write(frame(frameStdout, string(b[:n])))
			}
			if err != nil {
				break
			}
		}
		conn.Write(frame(frameStderr, "bye\n"))
	case last == "wait":
		d.mu.Lock()
		done, exit := d.done[parts[1]], d.exit[parts[1]]
		d.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		fmt.Fprintf(w, `{"StatusCode":%d}`, exit)
	case r.Method == http.MethodDelete:
		d.mu.Lock()
		delete(d.labels, parts[1])
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// engineFixture is a node's root and shared places, a daemon, and
// eriphe-node engine in front of it.
type engineFixture struct {
	root, share, ro, outside string
	d                        *apiDaemon
	s                        *engineServer
	url                      string
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &engineFixture{root: filepath.Join(base, "node"), share: filepath.Join(base, "share"),
		ro: filepath.Join(base, "photos"), outside: filepath.Join(base, "outside")}
	for _, d := range []string{"agents/a", "tasks/t1/work", "tasks/t1/agent", "services/s1"} {
		os.MkdirAll(filepath.Join(f.root, d), 0o755)
	}
	for _, d := range []string{f.share, f.ro, f.outside} {
		os.MkdirAll(d, 0o755)
	}
	os.Symlink(f.outside, filepath.Join(f.share, "escape"))
	binds := filepath.Join(base, "binds")
	os.WriteFile(binds, []byte(f.ro+":ro\n"), 0o644)
	var api *dockerAPI
	f.d, api = newAPIDaemon(t)
	f.s = &engineServer{d: api, policy: &dockerPolicy{bindFrom: []string{f.root, f.share}, bindFile: binds, resolve: filepath.EvalSymlinks},
		nodeImage: "example.org/eriphe-node:1", taskNetwork: "eriphe-tasks", network: "eriphe-system", serviceNetwork: "eriphe-services",
		cacheVolume: "eriphe-cache", lookup: net.LookupHost}
	srv := httptest.NewServer(f.s.handler())
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

// TestEngineRunsTheNode runs the node's own functions, as the node calls
// them, through eriphe-node engine, to a daemon: the node never speaks
// Docker, and every container it asks for is built by the engine, with the
// docker CLI engine's hardening. A new verb of the node belongs here.
func TestEngineRunsTheNode(t *testing.T) {
	f := newEngineFixture(t)
	refused := &lockedBuffer{}
	log.SetOutput(io.MultiWriter(os.Stderr, refused))
	defer log.SetOutput(os.Stderr)

	o := options{root: f.root, hostRoot: f.root, engine: engineDocker, engineURL: f.url, docker: "/no/docker/cli",
		taskMemory: "8g", taskCPUs: "2", serviceMemory: "1g", folderHelper: "example.org/eriphe-node:1",
		callbackURL: "http://eriphe-node:8790", listenAddr: "172.30.0.2:8790", egressPort: "8791"}
	if err := loadEngineConfig(&o, 1, 0); err != nil {
		t.Fatal(err)
	}
	if o.taskNetwork != "eriphe-tasks" || o.network != "eriphe-system" || o.serviceNetwork != "eriphe-services" || o.cacheVolume != "eriphe-cache" {
		t.Fatalf("the engine's settings: %+v", o)
	}
	n := newNode(o)
	if _, ok := n.engine().(remoteEngine); !ok {
		t.Fatalf("engine %T", n.engine())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// A task: its container, its checks, its stop.
	task := &Task{ID: "t1", secret: "s3cret", dir: filepath.Join(f.root, "tasks", "t1"),
		Spec: TaskSpec{Image: "worker", Prompt: "hi", Runtime: "claude-code", SystemNetwork: true}}
	if err := n.startContainer(task, "hi"); err != nil {
		t.Fatal(err)
	}
	task.run.stop()
	waitFor(t, func() bool { return finished(task.state()) })
	if out, code, err := n.execTask(ctx, task, 1024, "test -e \"$1\"", "/work/x"); err != nil || code != 3 || out != "checked\nwarned\n" {
		t.Errorf("exec: %q %d %v", out, code, err)
	}
	n.stopContainer(task)
	c, ok := f.d.container("eriphe-task-t1")
	if !ok {
		t.Fatal("no task container")
	}
	hc := c.HostConfig
	if c.User != "0:0" || c.Labels["eriphe.task"] != "t1" || !c.OpenStdin || !c.StdinOnce || c.Healthcheck == nil || c.Healthcheck.Test[0] != "NONE" ||
		!slices.Equal(hc.CapDrop, []string{"ALL"}) || !slices.Equal(hc.CapAdd, []string{"CHOWN", "DAC_OVERRIDE", "FOWNER"}) ||
		!slices.Equal(hc.SecurityOpt, []string{"no-new-privileges"}) || hc.PidsLimit != 1024 || !hc.AutoRemove ||
		hc.Memory != 8<<30 || hc.NanoCpus != 2e9 || hc.NetworkMode != "eriphe-tasks" || hc.ReadonlyRootfs ||
		!slices.Equal(hc.Binds, []string{"eriphe-cache:/cache"}) || hc.Tmpfs["/tmp"] != "rw,exec,nosuid,nodev,size=2g" ||
		!slices.Equal(hc.ExtraHosts, []string{"host.docker.internal:172.30.0.1"}) {
		t.Errorf("task container: %+v", c)
	}
	for _, m := range hc.Mounts {
		if m.Type != "bind" || !strings.HasPrefix(m.Source, f.root+"/") {
			t.Errorf("task mount %+v", m)
		}
	}
	if !slices.Contains(c.Env, "ERIPHE_TASK_SECRET=s3cret") {
		t.Errorf("task env: %v", c.Env)
	}

	// Services: run, stopped, the stray ones removed.
	for name, spec := range map[string]serviceSpec{
		"time": {Image: "mcp/time", Transport: "stdio"},
		"web":  {Image: "example.org/web", Transport: "http", Port: 80},
	} {
		run, err := n.serviceRun(name, spec)
		if err != nil {
			t.Fatal(err)
		}
		r, target, err := n.engine().runService(run)
		if err != nil {
			t.Fatalf("service %s: %v", name, err)
		}
		if r.stdin != nil {
			r.stdin.Close()
		}
		if err := r.Wait(); err != nil {
			t.Errorf("service %s: %v", name, err)
		}
		if want := map[string]string{"time": "", "web": "eriphe-svc-web:80"}[name]; target != want {
			t.Errorf("service %s target %q", name, target)
		}
		n.stopServiceContainer(name)
	}
	if c, _ := f.d.container("eriphe-svc-time"); c.HostConfig.NetworkMode != "none" || !c.HostConfig.ReadonlyRootfs || c.User != "1000:1000" ||
		c.HostConfig.Memory != 1<<30 || !c.OpenStdin {
		t.Errorf("stdio service: %+v", c)
	}
	if c, _ := f.d.container("eriphe-svc-web"); c.HostConfig.NetworkMode != "eriphe-services" || c.OpenStdin {
		t.Errorf("http service: %+v", c)
	}
	f.d.mu.Lock()
	f.d.labels["stray"] = map[string]string{"eriphe.service": "old"}
	f.d.mu.Unlock()
	n.removeStrayServices()

	// A folder helper, a wake, a pull, what the machine offers, the task network.
	runHelper(n, helperReq{Op: "check", Folder: f.share}, []helperMount{{path: f.share}})
	if err := runWakeHost(ctx, n, wakeInfo{MACs: []string{"02:00:00:00:00:01"}, Broadcast: "192.0.2.255"}); err != nil {
		t.Errorf("wake: %v", err)
	}
	if err := n.pullImage(ctx, "example.org/eriphe-base:1"); err != nil {
		t.Errorf("pull: %v", err)
	}
	w := httptest.NewRecorder()
	n.handleNode(w, httptest.NewRequest("GET", "/v1/node", nil))
	if !strings.Contains(w.Body.String(), `"docker":"27.0.0"`) || !strings.Contains(w.Body.String(), `"images":["example.org/eriphe-base:1"]`) {
		t.Errorf("node: %s", w.Body)
	}
	if err := n.engine().ensureNetwork(); err != nil {
		t.Errorf("task network: %v", err)
	}
	if s, err := n.engine().taskSubnets(); err != nil || !slices.Equal(s, []string{"172.30.0.0/16"}) {
		t.Errorf("subnets: %v %v", s, err)
	}

	if s := refused.String(); strings.Contains(s, "refused") {
		t.Errorf("the engine refused the node:\n%s", s)
	}
	if err := n.pullImage(ctx, "example.org/denied:1"); err == nil || !strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("a failed pull: %v", err)
	}
	got := f.d.calls()
	for _, want := range []string{
		"POST /containers/create?name=eriphe-task-t1", "POST /networks/eriphe-system/connect", "POST /containers/eriphe-task-t1/attach",
		"POST /containers/eriphe-task-t1/wait?condition=removed", "POST /containers/eriphe-task-t1/start",
		"POST /containers/eriphe-task-t1/exec", "POST /exec/e-eriphe-task-t1/start", "GET /exec/e-eriphe-task-t1/json",
		"POST /containers/eriphe-task-t1/stop?t=10", "POST /containers/create?name=eriphe-svc-time", "POST /containers/create?name=eriphe-svc-web",
		"DELETE /containers/stray?force=1", "POST /containers/create?name=eriphe-helper-", "POST /containers/create?name=eriphe-wake-",
		"POST /images/create?fromImage=example.org%2Feriphe-base&tag=1", "GET /version", "GET /images/json", "GET /networks/eriphe-tasks",
	} {
		if !slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, want) }) {
			t.Errorf("the daemon never got %s, got:\n%s", want, strings.Join(got, "\n"))
		}
	}
	f.d.mu.Lock()
	var helper, wake apiContainer
	for name, c := range f.d.created {
		switch {
		case strings.HasPrefix(name, "eriphe-helper-"):
			helper = c
		case strings.HasPrefix(name, "eriphe-wake-"):
			wake = c
		}
	}
	f.d.mu.Unlock()
	if helper.Image != "example.org/eriphe-node:1" || !slices.Equal(helper.Cmd, []string{"folder-helper"}) || helper.HostConfig.NetworkMode != "none" ||
		!helper.HostConfig.ReadonlyRootfs || len(helper.HostConfig.Mounts) != 1 || helper.HostConfig.Mounts[0].Source != f.share {
		t.Errorf("helper: %+v", helper)
	}
	if wake.Image != "example.org/eriphe-node:1" || wake.HostConfig.NetworkMode != "host" || len(wake.HostConfig.CapAdd) != 0 ||
		!slices.Equal(wake.Cmd, []string{"wake-send", "-mac", "02:00:00:00:00:01", "-broadcast", "192.0.2.255"}) || len(wake.HostConfig.Mounts) != 0 {
		t.Errorf("wake: %+v", wake)
	}
}

// A container's end comes back as its exit code; an image the daemon does
// not have is pulled first; a container that cannot be created is the
// run's error, not its exit.
func TestEngineTaskStream(t *testing.T) {
	f := newEngineFixture(t)
	e := remoteEngine{o: &options{engineURL: f.url, root: f.root, hostRoot: f.root}}
	f.d.mu.Lock()
	f.d.exit["eriphe-task-t2"] = 2
	f.d.missing["example.org/new:1"] = true
	f.d.mu.Unlock()
	stderr := &lockedBuffer{}
	r, err := e.runTask(taskRunSpec{name: "eriphe-task-t2", taskID: "t2", image: "example.org/new:1", entrypoint: "sh", stderr: stderr})
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(r.stdin, "hello")
	r.stdin.Close()
	out, _ := io.ReadAll(r.stdout)
	if err := r.Wait(); err == nil || err.Error() != "exit status 2" {
		t.Errorf("wait: %v", err)
	}
	if string(out) != "hello" || stderr.String() != "bye\n" {
		t.Errorf("stdout %q stderr %q", out, stderr.String())
	}
	if !slices.ContainsFunc(f.d.calls(), func(c string) bool {
		return strings.HasPrefix(c, "POST /images/create?fromImage=example.org%2Fnew&tag=1")
	}) {
		t.Error("the missing image was not pulled")
	}
	f.d.mu.Lock()
	f.d.missing["example.org/denied:1"] = true
	f.d.mu.Unlock()
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	if _, err := e.runTask(taskRunSpec{name: "eriphe-task-t3", taskID: "t3", image: "example.org/denied:1"}); err == nil ||
		!strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("an image that cannot be pulled: %v", err)
	}
	if _, err := e.runTask(taskRunSpec{name: "other", taskID: "t4", image: "x"}); err == nil {
		t.Error("a task's container of another name")
	}
}

// A compromised node, calling the engine directly, gets nothing but its
// own tasks: no privilege can be expressed, no network, folder or
// container of its choosing.
func TestEngineRefusesWhatTheNodeMayNotDo(t *testing.T) {
	f := newEngineFixture(t)
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	f.d.mu.Lock()
	f.d.labels["eriphe-task-wake"] = map[string]string{"eriphe.wake": "1"}
	f.d.labels["eriphe-task-helper"] = map[string]string{"eriphe.helper": "check"}
	f.d.labels["eriphe-task-svc"] = map[string]string{"eriphe.service": "svc"}
	f.d.labels["eriphe-task-foreign"] = map[string]string{"com.docker.compose.service": "core"}
	f.d.labels["eriphe-task-t2"] = map[string]string{"eriphe.task": "t1"}
	f.d.labels["eriphe-svc-db"] = map[string]string{"eriphe.task": "db"}
	f.d.mu.Unlock()
	task := func(edit string) string {
		return `{"task":"t1","image":"worker","entrypoint":"sh"` + edit + `}`
	}
	mount := func(src string, ro bool) string {
		return fmt.Sprintf(`,"mounts":[{"src":%q,"dst":"/work","read_only":%v}]`, src, ro)
	}
	for _, c := range []struct {
		name, path, body string
		status           int
	}{
		{"privileged", "/tasks", task(`,"privileged":true`), 400},
		{"a capability", "/tasks", task(`,"cap_add":["SYS_ADMIN"]`), 400},
		{"a network of its own", "/tasks", task(`,"network":"host"`), 400},
		{"a security option", "/tasks", task(`,"security_opt":["seccomp=unconfined"]`), 400},
		{"a device", "/tasks", task(`,"devices":["/dev/sda"]`), 400},
		{"a bad task", "/tasks", `{"task":"../x","image":"worker","entrypoint":"sh"}`, 403},
		{"a bad image", "/tasks", `{"task":"t1","image":"--privileged","entrypoint":"sh"}`, 403},
		{"a folder outside", "/tasks", task(mount(f.outside, false)), 403},
		{"the host's /", "/tasks", task(mount("/", true)), 403},
		{"a symlink out", "/tasks", task(mount(filepath.Join(f.share, "escape"), true)), 403},
		{"a dot-dot out", "/tasks", task(mount(f.share+"/../outside", true)), 403},
		{"a read-only place writable", "/tasks", task(mount(f.ro, false)), 403},
		{"a mount on /", "/tasks", `{"task":"t1","image":"worker","entrypoint":"sh","mounts":[{"src":"` + f.share + `","dst":"/"}]}`, 403},
		{"a bad env", "/tasks", task(`,"env":{"A=B":"x"}`), 403},
		{"a bad limit", "/tasks", task(`,"memory":"lots"`), 403},
		{"exec in a wake", "/exec", `{"task":"wake","script":"id","name":"x","max":10}`, 403},
		{"exec in a helper", "/exec", `{"task":"helper","script":"id","name":"x","max":10}`, 403},
		{"exec in a service", "/exec", `{"task":"svc","script":"id","name":"x","max":10}`, 403},
		{"exec in another's", "/exec", `{"task":"foreign","script":"id","name":"x","max":10}`, 403},
		{"exec in another task's", "/exec", `{"task":"t2","script":"id","name":"x","max":10}`, 403},
		{"exec as root", "/exec", `{"task":"t2","script":"id","name":"x","max":10,"user":"root"}`, 400},
		{"exec in none", "/exec", `{"task":"gone","script":"id","name":"x","max":10}`, 404},
		{"stop eriphe-core", "/stop", `{"container":"eriphe-core"}`, 403},
		{"stop another's", "/stop", `{"container":"eriphe-task-foreign"}`, 403},
		{"stop a service by a task's label", "/stop", `{"container":"eriphe-svc-db"}`, 403},
		{"remove another's", "/remove-stray", `{"label":"com.docker.compose.project"}`, 403},
		{"a root service", "/services", `{"service":"s","image":"x","user":"0:0","stdio":true}`, 403},
		{"a service on the host", "/services", `{"service":"s","image":"x","user":"1000:1000","stdio":true,"network":"host"}`, 400},
		{"a service's folder outside", "/services", `{"service":"s","image":"x","user":"1000:1000","stdio":true` + mount(f.outside, true) + `}`, 403},
		{"a helper of its own", "/helpers", `{"op":"check","image":"evil"}`, 400},
		{"a bad helper", "/helpers", `{"op":"check; rm"}`, 403},
		{"a root helper by name", "/helpers", `{"op":"check","user":"0:0"}`, 403},
		{"a helper's folder outside", "/helpers", `{"op":"check"` + mount(f.outside, true) + `}`, 403},
		{"a wake of its own image", "/wake", `{"macs":["02:00:00:00:00:01"],"image":"evil"}`, 400},
		{"a wake with arguments", "/wake", `{"macs":["02:00:00:00:00:01 -x"]}`, 403},
		{"a wake of nothing", "/wake", `{"macs":[]}`, 403},
		{"a bad pull", "/pull", `{"image":"-x"}`, 403},
		{"nothing of the API", "/containers/create", `{}`, 404},
	} {
		req, _ := http.NewRequest(http.MethodPost, f.url+engineAPI+c.path, strings.NewReader(c.body))
		if c.path == "/tasks" || c.path == "/services" {
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "eriphe-engine")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.status || !bytes.Contains(body, []byte(`"error"`)) {
			t.Errorf("%s: %d %s, want %d", c.name, resp.StatusCode, body, c.status)
		}
	}
	if n := f.d.creates(); n != 0 {
		t.Errorf("%d containers created", n)
	}
	for _, c := range f.d.calls() {
		if strings.Contains(c, "/exec") || strings.Contains(c, "/stop") || strings.HasPrefix(c, "DELETE") {
			t.Errorf("the daemon got %s", c)
		}
	}

	// Only the node is answered.
	f.s.allowFrom = "eriphe-node"
	f.s.lookup = func(string) ([]string, error) { return []string{"192.0.2.7"}, nil }
	resp, err := http.Get(f.url + engineAPI + "/config")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("another host: %d", resp.StatusCode)
	}
}

// What a refusal says reaches the node; a check that could not run is an
// error, never a failed check.
func TestRemoteEngineErrors(t *testing.T) {
	f := newEngineFixture(t)
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	e := remoteEngine{o: &options{engineURL: f.url}}
	ctx := context.Background()
	if _, err := e.execSync(ctx, "eriphe-task-gone", execSpec{script: "true"}); err == nil || !strings.Contains(err.Error(), "did not run") {
		t.Errorf("exec in none: %v", err)
	}
	if r, err := e.execSync(ctx, "eriphe-svc-x", execSpec{script: "true"}); err == nil || r.exit != -1 {
		t.Errorf("exec in a service: %v", err)
	}
	if _, _, err := e.runHelper(ctx, helperRunSpec{image: "alpine", entrypoint: "sh", label: "eriphe.helper=x"}); err == nil {
		t.Error("a helper that is not a folder helper")
	}
	if _, _, err := e.runService(serviceRunSpec{name: "eriphe-svc-s", service: "s", image: "x", user: "0:0", stdio: true}); err == nil ||
		!strings.Contains(err.Error(), "not root") {
		t.Errorf("a root service: %v", err)
	}
	gone := remoteEngine{o: &options{engineURL: "http://127.0.0.1:1"}}
	if err := gone.pull(ctx, "x"); err == nil {
		t.Error("no engine")
	}
	o := &options{engineURL: "http://127.0.0.1:1"}
	if err := loadEngineConfig(o, 2, time.Millisecond); err == nil || !strings.Contains(err.Error(), "eriphe-node engine at") {
		t.Errorf("config: %v", err)
	}
}

// splitImage gives the daemon a tag, always: without one it pulls them all.
func TestSplitImage(t *testing.T) {
	for in, want := range map[string][2]string{
		"alpine":                         {"alpine", "latest"},
		"example.org:5000/a/b":           {"example.org:5000/a/b", "latest"},
		"example.org:5000/a/b:1.2":       {"example.org:5000/a/b", "1.2"},
		"ghcr.io/a/b@sha256:0123abcd":    {"ghcr.io/a/b", "sha256:0123abcd"},
		"ghcr.io/a/b:1@sha256:0123abcd":  {"ghcr.io/a/b:1", "sha256:0123abcd"},
		"example.org/eriphe-base:latest": {"example.org/eriphe-base", "latest"},
	} {
		if n, tag := splitImage(in); n != want[0] || tag != want[1] {
			t.Errorf("%s: %s %s", in, n, tag)
		}
	}
}

// A task's subnet is the first of the pool no network uses; a pool used
// up, or smaller than one, says so.
func TestFreeSubnet(t *testing.T) {
	nets := func(subnets ...string) []apiNetworkFull {
		var out []apiNetworkFull
		for _, s := range subnets {
			var n apiNetworkFull
			n.IPAM.Config = append(n.IPAM.Config, struct {
				Subnet string `json:"Subnet"`
			}{s})
			out = append(out, n)
		}
		return out
	}
	pool := netip.MustParsePrefix("10.213.0.0/16")
	if got, err := freeSubnet(pool, nets("172.17.0.0/16", "10.213.0.0/28", "10.213.0.16/28")); err != nil || got.String() != "10.213.0.32/28" {
		t.Errorf("next free: %v %v", got, err)
	}
	if _, err := freeSubnet(pool, nets("10.0.0.0/8")); err == nil {
		t.Error("a pool used up")
	}
	if _, err := freeSubnet(netip.MustParsePrefix("10.213.0.0/30"), nil); err == nil {
		t.Error("a pool smaller than a task's subnet")
	}
}

// Each task on a network of its own: made by the engine like the task
// network (internal, its options), the node on it at an address of its
// subnet, which the node accepts; the task's container on it; once over,
// the node leaves and the network goes, and so do those a run left.
func TestEngineTaskNetworks(t *testing.T) {
	f := newEngineFixture(t)
	f.s.nodeName, f.s.taskPool = "eriphe-node", netip.MustParsePrefix("10.213.0.0/16")
	o := &options{engineURL: f.url, root: f.root, hostRoot: f.root}
	if err := loadEngineConfig(o, 1, 0); err != nil || !o.engineTaskNets {
		t.Fatalf("the engine does not say it makes task networks: %v", err)
	}
	nets := &taskNets{}
	e := remoteEngine{o: o, nets: nets}
	if !e.traits().ownNetworks {
		t.Error("no own networks")
	}
	name, node, err := e.beginTask("t1")
	if err != nil || name != "eriphe-task-t1" || node != "10.213.0.2" {
		t.Fatalf("begin: %q %q %v", name, node, err)
	}
	f.d.mu.Lock()
	n := f.d.nets[name]
	_, onIt := n.members["eriphe-node"]
	f.d.mu.Unlock()
	if !n.internal || n.subnet != "10.213.0.0/28" || n.labels["eriphe.task"] != "t1" ||
		n.options["com.docker.network.bridge.gateway_mode_ipv4"] != "isolated" || !onIt {
		t.Errorf("the task's network: %+v", n)
	}
	if !nets.allowed(netip.MustParseAddr("10.213.0.3")) || nets.allowed(netip.MustParseAddr("172.30.0.3")) {
		t.Error("the node accepts other networks than the tasks'")
	}
	r, err := e.runTask(taskRunSpec{name: "eriphe-task-t1", taskID: "t1", image: "x"})
	if err != nil {
		t.Fatal(err)
	}
	r.stdin.Close()
	if c, _ := f.d.container("eriphe-task-t1"); c.HostConfig.NetworkMode != "eriphe-task-t1" ||
		!slices.Contains(c.HostConfig.ExtraHosts, "host.docker.internal:10.213.0.1") {
		t.Errorf("the task's container: %s %v", c.HostConfig.NetworkMode, c.HostConfig.ExtraHosts)
	}
	if name2, _, err := e.beginTask("t2"); err != nil || f.d.nets[name2].subnet != "10.213.0.16/28" {
		t.Errorf("a second task's network: %v", err)
	}
	e.endTask(name)
	f.d.mu.Lock()
	_, left := f.d.nets[name]
	f.d.mu.Unlock()
	if left || nets.allowed(netip.MustParseAddr("10.213.0.3")) {
		t.Error("the network is still there once the task is over")
	}
	// A network a run left goes with the tasks' stray containers; the
	// others stay.
	e.removeStray("eriphe.task")
	f.d.mu.Lock()
	_, stray := f.d.nets["eriphe-task-t2"]
	_, shared := f.d.nets["eriphe-tasks"]
	f.d.mu.Unlock()
	if stray || !shared {
		t.Errorf("stray task network left %v, shared one gone %v", stray, !shared)
	}
	// Not a task's network: never removed.
	if _, _, err := e.beginTask("../x"); err == nil {
		t.Error("a bad task id")
	}
}
