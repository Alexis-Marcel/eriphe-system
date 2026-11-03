package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDockerProxyLetsTheNodeThrough runs the node's own docker commands, as
// the node makes them (its functions, not copies of their arguments), with
// the docker CLI, through the proxy, to a fake daemon: a call the node
// makes and the proxy refuses fails here, not on a machine. A new docker
// command of the node belongs here.
//
// It needs the docker CLI, not a daemon: it skips without one, but in CI.
func TestDockerProxyLetsTheNodeThrough(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs Unix sockets")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("the docker CLI is needed in CI")
		}
		t.Skip("no docker CLI")
	}
	f := newProxyFixture(t)
	d := newCLIDaemon(t)
	front := httptest.NewServer(newDockerProxy(f.policy, d.socket, ""))
	defer front.Close()
	f.policy.networks["eriphe-services"] = true
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(front.URL, "http://"))
	t.Setenv("DOCKER_CONFIG", t.TempDir()) // no context nor plugin of this machine's
	t.Setenv("DOCKER_CONTEXT", "")
	refused := &lockedBuffer{}
	log.SetOutput(io.MultiWriter(os.Stderr, refused))
	defer log.SetOutput(os.Stderr)

	n := newNode(options{root: f.root, hostRoot: f.root, docker: docker, taskMemory: "8g", taskCPUs: "2", cacheVolume: "eriphe-cache",
		taskNetwork: "eriphe-tasks", network: "eriphe-system", serviceNetwork: "eriphe-services", folderHelper: f.policy.wakeImage,
		callbackURL: "http://eriphe-node:8790", listenAddr: "172.17.0.2:8790", egressPort: "8791"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// A task: its container, its checks, its stop.
	task := &Task{ID: "t1", secret: "s3cret", dir: filepath.Join(f.root, "tasks", "t1"), Spec: TaskSpec{Image: "worker", Prompt: "hi", Runtime: "claude-code"}}
	if err := n.startContainer(task, "hi"); err != nil {
		t.Fatal(err)
	}
	task.run.stop()
	waitFor(t, func() bool { return finished(task.state()) })
	if out, code, err := n.execTask(ctx, task, 1024, "test -e \"$1\"", "/work/x"); err != nil || code != 3 || out != "checked\n" {
		t.Errorf("exec: %q %d %v", out, code, err)
	}
	n.stopContainer(task)

	// Services: run, stopped, the stray ones removed.
	for name, spec := range map[string]serviceSpec{
		"time": {Image: "mcp/time", Transport: "stdio"},
		"web":  {Image: "example.org/web", Transport: "http", Port: 80},
	} {
		args, env, _, err := n.serviceArgs(name, spec)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, docker, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("service %s: %v %s", name, err, out)
		}
		n.stopServiceContainer(name)
	}
	n.removeStrayServices()

	// A folder helper, a wake, a pull, what the machine offers, the task network.
	runHelper(n, helperReq{Op: "check", Folder: f.share}, []helperMount{{path: f.share}})
	if err := runWakeHost(ctx, n, wakeInfo{MACs: []string{"02:00:00:00:00:01"}}); err != nil {
		t.Errorf("wake: %v", err)
	}
	if err := n.pullImage(ctx, "example.org/eriphe-base:1"); err != nil {
		t.Errorf("pull: %v", err)
	}
	w := httptest.NewRecorder()
	n.handleNode(w, httptest.NewRequest("GET", "/v1/node", nil))
	if !strings.Contains(w.Body.String(), `"docker":"27.0.0"`) {
		t.Errorf("node: %s", w.Body)
	}
	if err := n.ensureTaskNetwork(); err != nil {
		t.Errorf("task network: %v", err)
	}

	if s := refused.String(); strings.Contains(s, "blocked") {
		t.Errorf("the proxy refused the node:\n%s", s)
	}
	// And each reached the daemon.
	got := d.calls()
	for _, want := range []string{
		"POST /containers/create?name=eriphe-task-t1", "POST /containers/eriphe-task-t1/attach", "POST /containers/eriphe-task-t1/start",
		"POST /containers/eriphe-task-t1/exec", "POST /exec/e-eriphe-task-t1/start", "GET /exec/e-eriphe-task-t1/json",
		"POST /containers/eriphe-task-t1/stop", "POST /containers/create?name=eriphe-svc-time", "POST /containers/create?name=eriphe-svc-web",
		"DELETE /containers/stray", "POST /containers/create?name=", "POST /images/create?fromImage=example.org%2Feriphe-base&tag=1",
		"GET /version", "GET /images/json", "GET /networks/eriphe-tasks",
	} {
		if !slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, want) }) {
			t.Errorf("the daemon never got %s, got:\n%s", want, strings.Join(got, "\n"))
		}
	}
	if c := len(slices.DeleteFunc(slices.Clone(got), func(c string) bool { return !strings.HasPrefix(c, "POST /containers/create") })); c != 5 {
		t.Errorf("%d containers created, want 5 (a task, two services, a helper, a wake)", c)
	}
}

// cliDaemon is a Docker daemon on a Unix socket, enough for the docker
// CLI's commands the node runs: every container is the node's but
// "other", every command run in one prints "checked" and exits 3.
type cliDaemon struct {
	socket string
	mu     sync.Mutex
	got    []string
}

func newCLIDaemon(t *testing.T) *cliDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dp")
	if err != nil {
		t.Skip("no short temporary directory for a socket:", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := &cliDaemon{socket: filepath.Join(dir, "d.sock")}
	ln, err := net.Listen("unix", d.socket)
	if err != nil {
		t.Skip("no Unix socket:", err)
	}
	srv := &http.Server{Handler: d}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return d
}

func (d *cliDaemon) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.got)
}

func (d *cliDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if loc := apiVersion.FindStringIndex(p); loc != nil {
		p = p[loc[1]-1:]
	}
	call := r.Method + " " + p
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	d.mu.Lock()
	d.got = append(d.got, call)
	d.mu.Unlock()
	w.Header().Set("Api-Version", "1.47")
	w.Header().Set("Content-Type", "application/json")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	last := parts[len(parts)-1]
	switch {
	case p == "/_ping":
		io.WriteString(w, "OK")
	case p == "/version":
		io.WriteString(w, `{"Version":"27.0.0","ApiVersion":"1.47","MinAPIVersion":"1.24","Os":"linux","Arch":"amd64"}`)
	case p == "/containers/create":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"Id":%q,"Warnings":[]}`, cmp(r.URL.Query().Get("name"), "anon"))
	case p == "/containers/json":
		fmt.Fprintf(w, `[{"Id":"stray","Names":["/eriphe-svc-stray"],"Image":%q,"Labels":{"eriphe.service":"stray"}}]`, "example.org/eriphe-node:1")
	case p == "/images/json":
		io.WriteString(w, `[{"Id":"sha256:1","RepoTags":["example.org/eriphe-base:1"]}]`)
	case p == "/images/create":
		io.WriteString(w, `{"status":"Downloaded"}`)
	case parts[0] == "networks":
		io.WriteString(w, `{"Name":"eriphe-tasks","Id":"n1","Internal":true,"IPAM":{"Config":[{"Subnet":"172.30.0.0/16","Gateway":"172.30.0.1"}]}}`)
	case parts[0] == "containers" && last == "json":
		labels := `{"eriphe.task":"t1"}`
		if parts[1] == "other" {
			labels = `{"com.docker.compose.service":"core"}`
		}
		fmt.Fprintf(w, `{"Id":%q,"Name":"/%s","Config":{"Labels":%s},"State":{"Running":true},"HostConfig":{}}`, parts[1], parts[1], labels)
	case parts[0] == "containers" && last == "exec":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"Id":"e-%s"}`, parts[1])
	case parts[0] == "exec" && last == "json":
		fmt.Fprintf(w, `{"ID":%q,"ContainerID":%q,"Running":false,"ExitCode":3}`, parts[1], strings.TrimPrefix(parts[1], "e-"))
	case last == "attach", parts[0] == "exec" && last == "start":
		out := []byte(nil)
		if parts[0] == "exec" {
			out = stdoutFrame("checked\n\n" + execExit + "3\n") // as execTask's wrapped script prints it
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		conn.Write(out)
		conn.Close()
	case last == "wait":
		io.WriteString(w, `{"StatusCode":0}`)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func cmp(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

// stdoutFrame is s on stdout, in the stream of a command without a tty.
func stdoutFrame(s string) []byte {
	h := make([]byte, 8)
	h[0] = 1
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

// lockedBuffer is a buffer written from several goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
