package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The create requests below are the docker CLI's own, recorded for the
// node's `docker run` of a task, a stdio and an http service, and the wake
// container (testdata/docker_create_*.json); @ROOT@ and @SHARE@ stand for the
// node's root and a shared place.

type proxyFixture struct {
	policy      *dockerPolicy
	root, share string
}

func newProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &proxyFixture{root: filepath.Join(base, "node"), share: filepath.Join(base, "share")}
	for _, d := range []string{"agents/a", "tasks/t1/work", "tasks/t1/agent", "services/s1"} {
		os.MkdirAll(filepath.Join(f.root, d), 0o755)
	}
	os.MkdirAll(filepath.Join(f.share, "docs", "Eriphe"), 0o755)
	os.MkdirAll(filepath.Join(base, "outside"), 0o755)
	f.policy = &dockerPolicy{bindFrom: []string{f.root, f.share}, networks: map[string]bool{"eriphe-system": true, "eriphe-tasks": true},
		wakeImage: "example.org/eriphe-node:1", resolve: filepath.EvalSymlinks}
	return f
}

// body is a recorded create request, changed by edit.
func (f *proxyFixture) body(t *testing.T, name string, edit func(c, hc map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/docker_create_" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(strings.ReplaceAll(string(raw), "@ROOT@", f.root), "@SHARE@", f.share)
	var c map[string]any
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(c, c["HostConfig"].(map[string]any))
	}
	out, _ := json.Marshal(c)
	return out
}

func TestDockerProxyAllowsTheNodesContainers(t *testing.T) {
	f := newProxyFixture(t)
	for _, name := range []string{"task", "service", "service_http", "wake"} {
		if _, err := f.policy.checkCreate(f.body(t, name, nil)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDockerProxyRefusesWhatGivesTheHost(t *testing.T) {
	f := newProxyFixture(t)
	os.Symlink("/", filepath.Join(f.root, "tasks", "t1", "escape"))
	cases := map[string]func(c, hc map[string]any){
		"privileged":        func(c, hc map[string]any) { hc["Privileged"] = true },
		"capability":        func(c, hc map[string]any) { hc["CapAdd"] = []any{"CAP_SYS_ADMIN"} },
		"all capabilities":  func(c, hc map[string]any) { hc["CapAdd"] = []any{"ALL"} },
		"caps kept":         func(c, hc map[string]any) { hc["CapDrop"] = nil },
		"device":            func(c, hc map[string]any) { hc["Devices"] = []any{map[string]any{"PathOnHost": "/dev/sda"}} },
		"gpu":               func(c, hc map[string]any) { hc["DeviceRequests"] = []any{map[string]any{"Count": -1}} },
		"device rule":       func(c, hc map[string]any) { hc["DeviceCgroupRules"] = []any{"b *:* rwm"} },
		"host pid":          func(c, hc map[string]any) { hc["PidMode"] = "host" },
		"host ipc":          func(c, hc map[string]any) { hc["IpcMode"] = "host" },
		"host uts":          func(c, hc map[string]any) { hc["UTSMode"] = "host" },
		"host userns":       func(c, hc map[string]any) { hc["UsernsMode"] = "host" },
		"host cgroupns":     func(c, hc map[string]any) { hc["CgroupnsMode"] = "host" },
		"host network":      func(c, hc map[string]any) { hc["NetworkMode"] = "host" },
		"another's network": func(c, hc map[string]any) { hc["NetworkMode"] = "container:eriphe-core" },
		"another network":   func(c, hc map[string]any) { hc["NetworkMode"] = "lan" },
		"another endpoint": func(c, hc map[string]any) {
			c["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{"lan": map[string]any{}}}
		},
		"seccomp off":         func(c, hc map[string]any) { hc["SecurityOpt"] = []any{"no-new-privileges", "seccomp=unconfined"} },
		"apparmor off":        func(c, hc map[string]any) { hc["SecurityOpt"] = []any{"no-new-privileges", "apparmor=unconfined"} },
		"privileges gained":   func(c, hc map[string]any) { hc["SecurityOpt"] = nil },
		"unmasked /proc":      func(c, hc map[string]any) { hc["MaskedPaths"] = []any{} },
		"writable /proc/sys":  func(c, hc map[string]any) { hc["ReadonlyPaths"] = []any{} },
		"sysctl":              func(c, hc map[string]any) { hc["Sysctls"] = map[string]any{"kernel.x": "1"} },
		"volumes of another":  func(c, hc map[string]any) { hc["VolumesFrom"] = []any{"eriphe-core"} },
		"runtime":             func(c, hc map[string]any) { hc["Runtime"] = "runc-unsafe" },
		"cgroup parent":       func(c, hc map[string]any) { hc["CgroupParent"] = "/" },
		"bind the host":       func(c, hc map[string]any) { hc["Binds"] = []any{"/:/host"} },
		"bind through a link": func(c, hc map[string]any) { hc["Binds"] = []any{f.root + "/tasks/t1/escape:/host"} },
		"bind propagation":    func(c, hc map[string]any) { hc["Binds"] = []any{f.root + "/tasks/t1/work:/work:rshared"} },
		"bind a prefix's sibling": func(c, hc map[string]any) {
			hc["Binds"] = []any{f.root + "-x:/work"}
		},
		"mount the host": func(c, hc map[string]any) {
			hc["Mounts"] = []any{map[string]any{"Type": "bind", "Source": "/etc", "Target": "/etc2"}}
		},
		"mount propagation": func(c, hc map[string]any) {
			hc["Mounts"] = []any{map[string]any{"Type": "bind", "Source": f.share, "Target": "/s", "BindOptions": map[string]any{"Propagation": "rshared"}}}
		},
		"volume bound to a path": func(c, hc map[string]any) {
			hc["Mounts"] = []any{map[string]any{"Type": "volume", "Source": "v", "Target": "/v",
				"VolumeOptions": map[string]any{"DriverConfig": map[string]any{"Name": "local", "Options": map[string]any{"device": "/", "o": "bind"}}}}}
		},
		"volume driver": func(c, hc map[string]any) { hc["VolumeDriver"] = "x" },
		"port on the LAN": func(c, hc map[string]any) {
			hc["PortBindings"] = map[string]any{"80/tcp": []any{map[string]any{"HostIp": "", "HostPort": "80"}}}
		},
		"no label":       func(c, hc map[string]any) { c["Labels"] = map[string]any{"x": "1"} },
		"restart always": func(c, hc map[string]any) { hc["RestartPolicy"] = map[string]any{"Name": "always"} },
		"log driver":     func(c, hc map[string]any) { hc["LogConfig"] = map[string]any{"Type": "syslog"} },
	}
	for name, edit := range cases {
		if _, err := f.policy.checkCreate(f.body(t, "task", edit)); err == nil {
			t.Errorf("%s: allowed", name)
		}
	}
	// The host's network: the node's own image sending wake-on-LAN packets only.
	wake := map[string]func(c, hc map[string]any){
		"another image":   func(c, hc map[string]any) { c["Image"] = "example.org/evil:1" },
		"another command": func(c, hc map[string]any) { c["Cmd"] = []any{"sh"} },
		"a shell":         func(c, hc map[string]any) { c["Entrypoint"] = []any{"sh"} },
		"a mount":         func(c, hc map[string]any) { hc["Binds"] = []any{f.share + ":/s"} },
		"writable":        func(c, hc map[string]any) { hc["ReadonlyRootfs"] = false },
	}
	for name, edit := range wake {
		if _, err := f.policy.checkCreate(f.body(t, "wake", edit)); err == nil {
			t.Errorf("wake, %s: allowed", name)
		}
	}
}

func TestDockerProxyResolvesBindSources(t *testing.T) {
	f := newProxyFixture(t)
	// A link inside the allowed places, to another of their folders: the
	// daemon gets the real path, not the link.
	os.Symlink(filepath.Join(f.share, "docs"), filepath.Join(f.root, "tasks", "t1", "docs"))
	out, err := f.policy.checkCreate(f.body(t, "task", func(c, hc map[string]any) {
		hc["Binds"] = []any{f.root + "/tasks/t1/docs:/work/docs:ro"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"`+filepath.Join(f.share, "docs")+`:/work/docs:ro"`) {
		t.Errorf("the source is not resolved: %s", out)
	}
	// Numbers go back as they came.
	if !strings.Contains(string(out), `"Memory":8589934592`) || !strings.Contains(string(out), `"MemorySwappiness":-1`) {
		t.Errorf("numbers changed: %s", out)
	}
}

func TestDockerProxyClassify(t *testing.T) {
	p := &dockerPolicy{networks: map[string]bool{"eriphe-tasks": true}}
	allowed := []string{
		"HEAD /_ping", "GET /v1.47/_ping", "GET /v1.47/version", "GET /v1.47/containers/json?all=1",
		"GET /v1.47/images/json", "GET /v1.47/images/example.org/eriphe-base:1/json",
		"GET /v1.47/networks/eriphe-tasks", "GET /v1.47/networks/bridge",
		"POST /v1.47/containers/create?name=eriphe-task-t1", "POST /v1.47/images/create?fromImage=example.org%2Fx&tag=1",
		"POST /v1.47/containers/abc/attach?stream=1", "POST /v1.47/containers/abc/start", "POST /v1.47/containers/abc/wait",
		"POST /v1.47/containers/eriphe-task-t1/stop?t=10", "DELETE /v1.47/containers/abc?force=1",
	}
	refused := []string{
		"GET /v1.47/info", "GET /v1.47/containers/abc/json", "POST /v1.47/containers/abc/exec",
		"POST /v1.47/containers/abc/kill", "POST /v1.47/containers/abc/update", "PUT /v1.47/containers/abc/archive",
		"POST /v1.47/images/create?fromSrc=http://example.org/x.tar&repo=example.org/eriphe-node", "POST /v1.47/images/create",
		"POST /v1.47/volumes/create", "POST /v1.47/networks/create", "POST /v1.47/build", "GET /v1.47/networks/lan",
		"POST /v1.47/containers/x/../create", "DELETE /v1.47/images/abc", "POST /v1.47/plugins/pull",
	}
	for _, s := range allowed {
		m, u, _ := strings.Cut(s, " ")
		if _, err := p.classify(httptest.NewRequest(m, u, nil)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range refused {
		m, u, _ := strings.Cut(s, " ")
		if _, err := p.classify(httptest.NewRequest(m, u, nil)); err == nil {
			t.Errorf("%s: allowed", s)
		}
	}
	// A start never takes a body: an old API's took a HostConfig.
	if _, err := p.classify(httptest.NewRequest("POST", "/v1.23/containers/abc/start", strings.NewReader(`{"Privileged":true}`))); err == nil {
		t.Error("a start with a body is allowed")
	}
}

// fakeDaemon is a Docker daemon on a Unix socket: two containers, the
// node's and another, and an attach that echoes its input once it ends.
func fakeDaemon(t *testing.T) (socket string, created chan []byte) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dp")
	if err != nil {
		t.Skip("no short temporary directory for a socket:", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skip("no Unix socket:", err)
	}
	created = make(chan []byte, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/mine/json":
			io.WriteString(w, `{"Config":{"Labels":{"eriphe.task":"t1"}}}`)
		case r.URL.Path == "/containers/other/json":
			io.WriteString(w, `{"Config":{"Labels":{"com.docker.compose.service":"core"}}}`)
		case strings.HasSuffix(r.URL.Path, "/json"):
			http.Error(w, `{"message":"no such container"}`, http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			b, _ := io.ReadAll(r.Body)
			created <- b
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"Id":"mine"}`)
		case strings.HasSuffix(r.URL.Path, "/attach"):
			conn, buf, _ := w.(http.Hijacker).Hijack()
			defer conn.Close()
			io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			in, _ := io.ReadAll(buf) // until the client's half-close
			conn.Write(append([]byte("echo:"), in...))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return socket, created
}

func TestDockerProxyForwards(t *testing.T) {
	f := newProxyFixture(t)
	socket, created := fakeDaemon(t)
	proxy := newDockerProxy(f.policy, socket, "")
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1.47/containers/create?name=eriphe-task-t1", "application/json", strings.NewReader(string(f.body(t, "task", nil))))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %v %v", err, resp)
	}
	if b := <-created; !strings.Contains(string(b), `"eriphe.task":"t1"`) {
		t.Errorf("the daemon got %s", b)
	}
	resp, _ = http.Post(front.URL+"/v1.47/containers/create", "application/json",
		strings.NewReader(string(f.body(t, "task", func(c, hc map[string]any) { hc["Privileged"] = true }))))
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a privileged create: %s", resp.Status)
	}
	// The node's containers only.
	for id, want := range map[string]int{"mine": http.StatusNoContent, "other": http.StatusForbidden, "gone": http.StatusNotFound} {
		resp, _ := http.Post(front.URL+"/v1.47/containers/"+id+"/stop", "", nil)
		if resp.StatusCode != want {
			t.Errorf("stop %s: %s, want %d", id, resp.Status, want)
		}
	}

	// attach: the stream goes both ways, the end of input passed on.
	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "POST /v1.47/containers/mine/attach?stdin=1&stdout=1&stream=1 HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err = http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("attach: %v %v", err, resp)
	}
	io.WriteString(conn, "hello")
	conn.(*net.TCPConn).CloseWrite()
	out, _ := io.ReadAll(br)
	if string(out) != "echo:hello" {
		t.Errorf("attach: %q", out)
	}
}

func TestDockerProxyAnswersTheNodeOnly(t *testing.T) {
	f := newProxyFixture(t)
	p := newDockerProxy(f.policy, "/nonexistent", "eriphe-node")
	p.lookup = func(string) ([]string, error) { return []string{"192.0.2.7"}, nil }
	r := httptest.NewRequest("GET", "/_ping", nil)
	r.RemoteAddr = "192.0.2.8:5000"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("another host: %d", w.Code)
	}
}
