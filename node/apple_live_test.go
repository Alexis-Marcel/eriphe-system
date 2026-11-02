package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAppleLive runs real task containers with Apple's container on this
// Mac, through startContainer, as a node with -engine container does:
// hardened, on a network of their own, with folders of the user's mounted.
// A probe stands for the agent and reports what the task can reach.
//
//	ERIPHE_LIVE_CONTAINER=1 go test -run AppleLive -v
//
// ERIPHE_LIVE_FOLDERS lists folders of the home to lend (relative to it,
// separated by commas; the first is lent read-only, the others with
// Eriphe/), ERIPHE_LIVE_ICLOUD a folder of iCloud Drive lent with Eriphe/,
// and ERIPHE_LIVE_LAN an address of the LAN (host:port) a task must not
// reach. The image is alpine:3.22 unless ERIPHE_LIVE_IMAGE says otherwise.
func TestAppleLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_CONTAINER") == "" {
		t.Skip("ERIPHE_LIVE_CONTAINER not set")
	}
	image := envOr("ERIPHE_LIVE_IMAGE", "alpine:3.22")
	home, _ := os.UserHomeDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	n := newNode(options{root: root, hostRoot: root, docker: "container", engine: engineContainer,
		taskMemory: "1g", taskCPUs: "2", cacheVolume: "eriphe-cache", downloadLimit: 100 << 20,
		shares: shareFlag{"home": {Path: home}, "icloud": {Path: filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")}}})

	// The task listener and the relay, as main starts them.
	l, err := n.listenTasks("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	n.opt.listenAddr = net.JoinHostPort("0.0.0.0", port)
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "eriphe-node\n") }))
	el, err := n.listenTasks("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer el.Close()
	_, n.opt.egressPort, _ = net.SplitHostPort(el.Addr().String())
	relay := newEgressProxy(n.egressAuthorizer, nil, net.JoinHostPort("127.0.0.1", port))
	relay.isSelf = func(hp string) bool { return n.nets.gateway(hp, port) }
	go http.Serve(el, relay)

	// From the LAN (this Mac's own address on it), the listener answers nothing.
	if lan := lanAddr(); lan != "" {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(lan, port), 2*time.Second)
		if err == nil {
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, rerr := c.Read(make([]byte, 1))
			c.Close()
			if rerr == nil {
				t.Errorf("the listener answers on the LAN address %s", lan)
			}
		}
		t.Logf("listener from the LAN address %s: closed", lan)
	}

	// The explorer lists the home's root, its own data hidden.
	if _, e, err := n.appleResolve(context.Background(), "home", "", "list"); err != nil || len(e.dirs) == 0 {
		t.Errorf("the home's root: %v", err)
	}
	var folders []TaskFolder
	for i, f := range strings.Split(os.Getenv("ERIPHE_LIVE_FOLDERS"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			folders = append(folders, lend(t, n, "home", f, fmt.Sprintf("f%d", i), i > 0))
		}
	}
	if f := os.Getenv("ERIPHE_LIVE_ICLOUD"); f != "" {
		folders = append(folders, lend(t, n, "icloud", f, "icloud", true))
		// Files only in iCloud (brctl evict makes one): over the limit, a
		// clear refusal; within, the node downloads them before the task.
		start := time.Now()
		r, _, err := n.appleResolve(context.Background(), "icloud", f, "list")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("explorer: %s resolved in %s", f, time.Since(start).Round(time.Millisecond))
		count, need, _ := cloudOnly(r.real)
		t.Logf("in iCloud only: %d files, %d bytes", count, need)
		if count > 0 {
			if _, err := download(r.real, 1, 1); !errors.Is(err, errTooBig) {
				t.Errorf("over the limit: %v", err)
			} else {
				t.Logf("over the limit: %v", err)
			}
		}
	}
	// A LAN address (host:port) a task must not reach, e.g. the router's.
	lanTarget := os.Getenv("ERIPHE_LIVE_LAN")
	if lanTarget == "" {
		t.Fatal("ERIPHE_LIVE_LAN not set: an address of the LAN, e.g. the router's, host:port")
	}
	lanHost, lanPort, _ := net.SplitHostPort(lanTarget)

	// A first task listens; the second, its probe, must not reach it.
	other := runProbe(t, n, "other", "alpine:3.22", nil, `ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1 > /work/ip; while true; do echo hi | nc -l -p 9000; done`, false)
	var otherIP string
	for i := 0; i < 100 && otherIP == ""; i++ {
		raw, _ := os.ReadFile(filepath.Join(other.dir, "work", "ip"))
		otherIP = strings.TrimSpace(string(raw))
		time.Sleep(100 * time.Millisecond)
	}
	if otherIP == "" {
		t.Fatalf("the other task has no address: %v", other.events)
	}

	probe := `
check() { if eval "$2" >/dev/null 2>&1; then echo "$1=yes"; else echo "$1=no"; fi; }
# get URL (through the relay, as the task's environment says); direct: without it.
get() { if command -v wget >/dev/null; then wget -q -T 8 -O - "$1"; else curl -fsS -m 8 "$1"; fi; }
direct() { HTTP_PROXY= http_proxy= HTTPS_PROXY= https_proxy= "$@"; }
# reach HOST PORT: a TCP connection opens.
reach() { if command -v nc >/dev/null; then nc -z -w 3 "$1" "$2"; else timeout 3 bash -c "</dev/tcp/$1/$2"; fi; }
gw=$(echo "$HTTP_PROXY" | sed 's/.*@//; s/:.*//')
echo "uid=$(id -u)"
echo "home=$HOME"
check rootfs_write 'touch /x'
check tmp_write 'touch /tmp/x'
check work_write 'touch /work/x'
check cache_write 'touch /cache/x'
check listener 'direct get http://$gw:` + port + `/ | grep -q eriphe-node'
check relay_internet 'get http://example.com/'
check relay_lan 'get http://` + lanTarget + `/'
check direct_internet 'reach 1.1.1.1 80'
check direct_dns 'direct get http://example.com/'
check direct_lan 'reach ` + lanHost + ` ` + lanPort + `'
check other_task 'reach ` + otherIP + ` 9000'
check mac_lan_address 'reach ` + lanAddr() + ` 8800'
for d in /work/folders/*; do
  [ -d "$d" ] || continue
  n=$(basename "$d")
  check "${n}_read" 'cat "$d"/*.txt'
  check "${n}_write_root" 'touch "$d/written-by-task"'
  check "${n}_write_results" 'echo result > "$d/Eriphe/result-$n.txt"'
done
`
	task := runProbe(t, n, "probe", image, folders, probe, true)
	out := task.dir + "/out"
	raw, _ := os.ReadFile(out)
	got := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	t.Logf("probe:\n%s", raw)
	// Root, on a root filesystem of its own (the agent installs packages there).
	want := map[string]string{"uid": "0", "rootfs_write": "yes", "tmp_write": "yes", "work_write": "yes", "cache_write": "yes",
		"listener": "yes", "relay_internet": "yes", "relay_lan": "no", "direct_internet": "no", "direct_dns": "no",
		"direct_lan": "no", "other_task": "no"}
	for _, f := range folders {
		want[f.Name+"_read"] = "yes"
		want[f.Name+"_write_root"] = "no"
		if f.Write {
			want[f.Name+"_write_results"] = "yes"
		} else {
			want[f.Name+"_write_results"] = "no"
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%s, want %s", k, got[k], v)
		}
	}
	// Known gap, reported: the Mac's services on all its addresses.
	t.Logf("a service of this Mac on all its addresses is reachable from a task: %s", got["mac_lan_address"])

	n.stopContainer(other)
	waitDone(t, other)
	for _, tk := range []*Task{task, other} {
		if l, err := n.appleNetworks(taskNetwork(tk)); err == nil && len(l) > 0 {
			t.Errorf("the network of %s is still there", tk.ID)
		}
	}
	for _, f := range folders {
		if f.Write {
			// Seen, then removed, by a container: this test never opens the
			// user's folders itself (macOS would ask for it).
			r, err := n.lentFolder(task, f)
			if err != nil {
				t.Fatal(err)
			}
			name := "result-" + f.Name + ".txt"
			out, err := exec.Command("container", "run", "--rm", "--progress", "none", "--network", "none",
				"--mount", "type=bind,source="+filepath.Join(r.real, resultsDir)+",target=/e", "alpine:3.22",
				"sh", "-c", `ls -ln /e/"$1" && rm -f /e/"$1"`, "sh", name).CombinedOutput()
			if err != nil {
				t.Errorf("no %s in %s/%s: %v %s", name, f.Name, resultsDir, err, out)
			} else {
				t.Logf("%s/%s/%s, written by the task: %s", f.Name, resultsDir, name, strings.TrimSpace(string(out)))
			}
		}
	}
}

// probeRuntime stands for an agent: it runs a shell script and keeps its
// output in the task's folder.
type probeRuntime struct{ script string }

func (p probeRuntime) command(*node, *Task) ([]string, map[string]string) {
	return []string{"sh", "-c", p.script}, nil
}

func (p probeRuntime) attach(t *Task, stdin io.WriteCloser) session {
	return &probeSession{t: t, stdin: stdin}
}

type probeSession struct {
	t     *Task
	stdin io.WriteCloser
	once  sync.Once
}

func (s *probeSession) run(_ string, stdout io.Reader) error {
	var b bytes.Buffer
	io.Copy(&b, stdout)
	return os.WriteFile(s.t.dir+"/out", b.Bytes(), 0o600)
}
func (s *probeSession) send(string) error { return errNotIdle }
func (s *probeSession) stop()             { s.once.Do(func() { s.stdin.Close() }) }

func runProbe(t *testing.T, n *node, id, image string, folders []TaskFolder, script string, wait bool) *Task {
	t.Helper()
	runtimes["probe-"+id] = probeRuntime{script}
	t.Cleanup(func() { delete(runtimes, "probe-"+id) })
	task := &Task{ID: "live" + id, Spec: TaskSpec{Runtime: "probe-" + id, Image: image, Folders: folders},
		State: stateRunning, dir: filepath.Join(n.opt.root, "tasks", id), secret: newID() + newID(), perms: map[string]*permission{}}
	os.MkdirAll(task.dir, 0o700)
	n.adopt(task)
	start := time.Now()
	if err := n.startContainer(task, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopContainer(task) })
	if wait {
		waitDone(t, task)
		t.Logf("task %s: %s in %s", id, task.state(), time.Since(start).Round(time.Millisecond))
	}
	return task
}

func waitDone(t *testing.T, task *Task) {
	t.Helper()
	for i := 0; !finished(task.state()); i++ {
		if i > 1200 {
			t.Fatalf("task %s still %s", task.ID, task.state())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// lanAddr is this Mac's address on its LAN, "" if none.
func lanAddr() string {
	c, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
