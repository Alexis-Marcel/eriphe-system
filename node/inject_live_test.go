package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestInjectLive runs real task containers with Docker, as the NAS does:
// the node (this test) is a container on an internal task network, tasks
// reach only it, its listener and its egress relay. Each task starts from
// the base image with Claude Code injected, installs ImageMagick with apt
// (the second task from the node's cache), writes to a lent folder's
// Eriphe/, and probes the network.
//
//	ERIPHE_LIVE_DOCKER=1 ERIPHE_LIVE_IMAGE=eriphe-base:dev \
//	ERIPHE_LIVE_AGENT_IMAGE=registry:5000/eriphe-agent-{agent}:dev \
//	ERIPHE_LIVE_SHARE=/tmp/share ERIPHE_LIVE_SELF=<this container's name> \
//	ERIPHE_LIVE_LAN=<router>:80 go test -run InjectLive -v
//
// ERIPHE_LIVE_PLAIN_REGISTRY names a registry reached over plain HTTP (a
// local one). Run it in a container that has the docker CLI, the Docker
// socket, the task network (ERIPHE_LIVE_TASK_NETWORK, internal) and a
// network with egress; ERIPHE_LIVE_SHARE at the same path for it and the
// daemon.
func TestInjectLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_DOCKER") == "" {
		t.Skip("ERIPHE_LIVE_DOCKER not set")
	}
	if r := os.Getenv("ERIPHE_LIVE_PLAIN_REGISTRY"); r != "" {
		plainRegistries[r] = true
		t.Cleanup(func() { delete(plainRegistries, r) })
	}
	share := os.Getenv("ERIPHE_LIVE_SHARE")
	self := os.Getenv("ERIPHE_LIVE_SELF")
	lan := os.Getenv("ERIPHE_LIVE_LAN")
	if share == "" || self == "" || lan == "" {
		t.Fatal("ERIPHE_LIVE_SHARE, ERIPHE_LIVE_SELF and ERIPHE_LIVE_LAN are needed")
	}
	root := filepath.Join(share, ".eriphe-node") // under the share's path: the daemon sees it at the same path
	os.RemoveAll(root)
	os.MkdirAll(root, 0o700)
	t.Cleanup(func() { os.RemoveAll(root) })
	folder := filepath.Join(share, "Clients")
	os.MkdirAll(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "brief.txt"), []byte("a brief\n"), 0o644)
	os.RemoveAll(filepath.Join(folder, resultsDir))
	taskNet := envOr("ERIPHE_LIVE_TASK_NETWORK", "eriphe-tasks")
	n := newNode(options{root: root, hostRoot: root, docker: "docker", image: os.Getenv("ERIPHE_LIVE_IMAGE"),
		agentImage: os.Getenv("ERIPHE_LIVE_AGENT_IMAGE"), taskNetwork: taskNet, taskMemory: "2g",
		cacheVolume: envOr("ERIPHE_LIVE_CACHE", "eriphe-live-cache"), callbackURL: "http://" + self + ":8790",
		shares: shareFlag{"share": {Path: share}}})

	// The task listener and the relay, on this container's address on the
	// task network, as main starts them.
	addr, err := n.listenAddr(":8790")
	if err != nil {
		t.Fatal(err)
	}
	n.opt.listenAddr = addr
	host, _, _ := net.SplitHostPort(addr)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "eriphe-node\n") }))
	el, err := net.Listen("tcp", net.JoinHostPort(host, "8791"))
	if err != nil {
		t.Fatal(err)
	}
	defer el.Close()
	n.opt.egressPort = "8791"
	go http.Serve(el, newEgressProxy(n.egressAuthorizer, selfTargets(n.opt.callbackURL, addr), addr))

	// Prepared ahead, as at a join: the base image and Claude Code.
	start := time.Now()
	n.prepareNow(&prepareSpec{Runtimes: []string{"claude-code"}})
	if n.agents.current("claude") == "" {
		t.Fatal("Claude Code was not prepared")
	}
	t.Logf("prepared in %s: %v", time.Since(start).Round(time.Millisecond), n.agents.readyRuntimes())

	agentPackages["probe-first"], agentPackages["probe-second"] = "claude", "claude"
	t.Cleanup(func() { delete(agentPackages, "probe-first"); delete(agentPackages, "probe-second") })
	folders := []TaskFolder{{Name: "clients", Share: "share", Path: "Clients", Write: true}}
	lanHost, lanPort, _ := net.SplitHostPort(lan)
	results := map[string]map[string]string{}
	for _, id := range []string{"first", "second"} {
		task := runProbe(t, n, id, n.opt.image, folders, liveProbe(lan, lanHost, lanPort, id), true)
		raw, _ := os.ReadFile(task.dir + "/out")
		t.Logf("probe %s:\n%s", id, raw)
		results[id] = probeValues(raw)
	}
	want := map[string]string{"uid": "0", "rootfs_write": "yes", "agent_write": "no", "apt_rc": "0", "magick": "yes",
		"listener": "yes", "relay_internet": "yes", "relay_lan": "no", "direct_internet": "no", "direct_dns": "no",
		"direct_lan": "no", "clients_read": "yes", "clients_write_root": "no", "clients_write_results": "yes"}
	for id, got := range results {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s=%s, want %s", id, k, got[k], v)
			}
		}
		if !strings.Contains(got["claude"], "Claude Code") {
			t.Errorf("%s: claude --version: %q", id, got["claude"])
		}
	}
	logApt(t, results)
	// The results are Eriphe/'s owner's, not root's.
	for _, id := range []string{"first", "second"} {
		fi, err := os.Lstat(filepath.Join(folder, resultsDir, "result-"+id+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		t.Logf("Eriphe/result-%s.txt: UID %d GID %d %v", id, st.Uid, st.Gid, fi.Mode())
		if st.Uid != taskUID {
			t.Errorf("result-%s.txt belongs to UID %d", id, st.Uid)
		}
	}
}

// liveProbe is the script a probe task runs in place of an agent.
func liveProbe(lan, lanHost, lanPort, id string) string {
	return `
check() { if eval "$2" >/dev/null 2>&1; then echo "$1=yes"; else echo "$1=no"; fi; }
get() { if command -v wget >/dev/null; then wget -q -T 8 -O - "$1"; else curl -fsS -m 8 "$1"; fi; }
direct() { HTTP_PROXY= http_proxy= HTTPS_PROXY= https_proxy= "$@"; }
reach() { if command -v nc >/dev/null; then nc -z -w 3 "$1" "$2"; else timeout 3 bash -c "</dev/tcp/$1/$2"; fi; }
gw=$(echo "$HTTP_PROXY" | sed 's/.*@//; s/:.*//')
echo "uid=$(id -u)"
echo "claude=$(claude --version 2>&1 | head -n 1)"
echo "claude_path=$(command -v claude)"
check rootfs_write 'touch /usr/local/x'
check agent_write 'touch /opt/eriphe/bin/x'
s=$(date +%s%N)
{ apt-get update -qq && apt-get install -y imagemagick; } > /tmp/apt.log 2>&1
echo "apt_rc=$?"
e=$(date +%s%N)
echo "apt_ms=$(( (e - s) / 1000000 ))"
echo "apt_fetched=$(grep -o 'Fetched [^ ]* [^ ]*' /tmp/apt.log | tail -n 1)"
check magick 'magick -version'
check listener 'direct get http://$gw:8790/ | grep -q eriphe-node'
check relay_internet 'get http://example.com/'
check relay_lan 'get http://` + lan + `/'
check direct_internet 'reach 1.1.1.1 80'
check direct_dns 'direct get http://example.com/'
check direct_lan 'reach ` + lanHost + ` ` + lanPort + `'
for d in /work/folders/*; do
  [ -d "$d" ] || continue
  n=$(basename "$d")
  check "${n}_read" 'cat "$d"/*.txt'
  check "${n}_write_root" 'touch "$d/written-by-task"'
  check "${n}_write_results" 'echo result > "$d/Eriphe/result-` + id + `.txt"'
done
`
}

func probeValues(raw []byte) map[string]string {
	got := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

func logApt(t *testing.T, results map[string]map[string]string) {
	t.Logf("apt-get update and install imagemagick: %s ms with an empty cache (%s), then %s ms from the cache (%s)",
		results["first"]["apt_ms"], orNone(results["first"]["apt_fetched"]), results["second"]["apt_ms"], orNone(results["second"]["apt_fetched"]))
}

// TestAppleInjectLive is TestInjectLive with Apple's container on this Mac:
// each task a micro-VM on a network of its own, a folder of the home lent.
//
//	ERIPHE_LIVE_CONTAINER=1 ERIPHE_LIVE_IMAGE=localhost:5001/eriphe-base:dev \
//	ERIPHE_LIVE_AGENT_IMAGE=localhost:5001/eriphe-agent-{agent}:dev \
//	ERIPHE_LIVE_LAN=<router>:80 go test -run AppleInjectLive -v
func TestAppleInjectLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_CONTAINER") == "" || os.Getenv("ERIPHE_LIVE_AGENT_IMAGE") == "" {
		t.Skip("ERIPHE_LIVE_CONTAINER or ERIPHE_LIVE_AGENT_IMAGE not set")
	}
	lan := os.Getenv("ERIPHE_LIVE_LAN")
	if lan == "" {
		t.Fatal("ERIPHE_LIVE_LAN not set")
	}
	home, _ := os.UserHomeDir()
	place, err := os.MkdirTemp(home, "eriphe-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(place) })
	os.MkdirAll(filepath.Join(place, "Clients"), 0o755)
	os.WriteFile(filepath.Join(place, "Clients", "brief.txt"), []byte("a brief\n"), 0o644)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	n := newNode(options{root: root, hostRoot: root, docker: "container", engine: engineContainer,
		image: os.Getenv("ERIPHE_LIVE_IMAGE"), agentImage: os.Getenv("ERIPHE_LIVE_AGENT_IMAGE"),
		taskMemory: "2g", taskCPUs: "2", cacheVolume: "eriphe-cache", downloadLimit: 100 << 20,
		shares: shareFlag{"live": {Path: place}}})
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

	start := time.Now()
	n.prepareNow(&prepareSpec{Runtimes: []string{"claude-code"}})
	if n.agents.current("claude") == "" {
		t.Fatal("Claude Code was not prepared")
	}
	t.Logf("prepared in %s", time.Since(start).Round(time.Millisecond))
	agentPackages["probe-first"], agentPackages["probe-second"] = "claude", "claude"
	t.Cleanup(func() { delete(agentPackages, "probe-first"); delete(agentPackages, "probe-second") })
	folders := []TaskFolder{{Name: "clients", Share: "live", Path: "Clients", Write: true}}
	lanHost, lanPort, _ := net.SplitHostPort(lan)
	results := map[string]map[string]string{}
	for _, id := range []string{"first", "second"} {
		// The probe reaches the listener on its own port.
		script := strings.ReplaceAll(liveProbe(lan, lanHost, lanPort, id), ":8790/", ":"+port+"/")
		task := runProbe(t, n, id, n.opt.image, folders, script, true)
		raw, _ := os.ReadFile(task.dir + "/out")
		t.Logf("probe %s:\n%s", id, raw)
		results[id] = probeValues(raw)
	}
	want := map[string]string{"uid": "0", "rootfs_write": "yes", "agent_write": "no", "apt_rc": "0", "magick": "yes",
		"listener": "yes", "relay_internet": "yes", "relay_lan": "no", "direct_internet": "no", "direct_dns": "no",
		"direct_lan": "no", "clients_read": "yes", "clients_write_root": "no", "clients_write_results": "yes"}
	for id, got := range results {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s=%s, want %s", id, k, got[k], v)
			}
		}
		if !strings.Contains(got["claude"], "Claude Code") {
			t.Errorf("%s: claude --version: %q", id, got["claude"])
		}
	}
	logApt(t, results)
	for _, id := range []string{"first", "second"} {
		fi, err := os.Lstat(filepath.Join(place, "Clients", resultsDir, "result-"+id+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		t.Logf("Eriphe/result-%s.txt: UID %d GID %d %v", id, st.Uid, st.Gid, fi.Mode())
		if int(st.Uid) != os.Getuid() {
			t.Errorf("result-%s.txt belongs to UID %d, not this Mac's user", id, st.Uid)
		}
	}
}
