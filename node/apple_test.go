package main

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeContainer is a container CLI that records its calls, one per line in
// <dir>/calls, the run's arguments one per line in <dir>/run, and answers
// for a network named in <dir>/nets.
func fakeContainer(t *testing.T) (cli, dir string) {
	t.Helper()
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	dir = t.TempDir()
	script := `#!/bin/sh
d=` + dir + `
echo "$*" >> "$d/calls"
case "$1 $2" in
"network inspect")
  if grep -qx "$3" "$d/nets" 2>/dev/null; then
    printf '[{"id":"%s","configuration":{"mode":"hostOnly","labels":{}},"status":{"ipv4Gateway":"192.168.130.1","ipv4Subnet":"192.168.130.0/24"}}]' "$3"
  else
    echo "network $3 not found" >&2; exit 1
  fi ;;
"network create") for a; do last=$a; done; echo "$last" >> "$d/nets"; echo "$last" ;;
"network delete") grep -vx "$3" "$d/nets" > "$d/nets.new"; mv "$d/nets.new" "$d/nets" ;;
"network ls") printf '[{"id":"eriphe-task-old","configuration":{"mode":"hostOnly","labels":{"eriphe.task":"old"}},"status":{}},{"id":"default","configuration":{"mode":"nat","labels":{}},"status":{}}]' ;;
"ls -a") printf '[{"configuration":{"id":"eriphe-task-old","labels":{"eriphe.task":"old"}},"status":{"state":"running"}},{"configuration":{"id":"mine","labels":{}},"status":{"state":"running"}}]' ;;
run*)
  printf '%s\n' "$@" TZ=$TZ HTTPS_PROXY=$HTTPS_PROXY NO_PROXY=$NO_PROXY > "$d/run"
  cat >/dev/null ;;
esac
`
	cli = filepath.Join(dir, "container")
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli, dir
}

// A task's `container run`: the same contract as Docker's, in the container
// tool's words, on a network of its own.
func TestAppleTaskArgs(t *testing.T) {
	cli, dir := fakeContainer(t)
	root := filepath.Join(dir, "root")
	n := newNode(options{root: root, hostRoot: root, docker: cli, engine: engineContainer, taskMemory: "8g",
		cacheVolume: "eriphe-cache", listenAddr: "0.0.0.0:8790", egressPort: "8791"})
	task := &Task{ID: "t-1", secret: "s3cret", dir: filepath.Join(root, "tasks", "t-1"),
		Spec: TaskSpec{Image: "worker", Prompt: "hi", Runtime: "claude-code", TZ: "Europe/Paris", SystemNetwork: true}}
	if err := n.startContainer(task, "hi"); err != nil {
		t.Fatal(err)
	}
	// While it runs, its network may reach the node, and only its network.
	if !n.nets.allowed(netip.MustParseAddr("192.168.130.7")) || n.nets.allowed(netip.MustParseAddr("192.168.0.10")) {
		t.Error("the listener's filter does not follow the task's network")
	}
	if !n.nets.gateway("192.168.130.1:8790", "8790") || n.nets.gateway("192.168.130.1:22", "8790") {
		t.Error("the relay does not know the task's gateway")
	}
	task.run.stop()
	waitFor(t, func() bool { return finished(task.state()) })
	raw, _ := os.ReadFile(filepath.Join(dir, "run"))
	args := strings.Split(string(raw), "\n")
	for _, want := range [][]string{
		{"run", "-i", "--rm", "--progress", "none", "--name", "eriphe-task-t-1"}, {"--label", "eriphe.task=t-1"},
		{"--network", "eriphe-task-t-1"},
		{"--user", "1000:1000"}, {"--cap-drop", "ALL"}, {"--ulimit", "nproc=1024"}, {"--read-only"},
		{"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g"},
		{"--memory", "8g"}, {"--cpus", strconv.Itoa(runtime.NumCPU())},
		{"--mount", "type=bind,source=" + filepath.Join(root, "cache") + ",target=/cache"},
		{"--mount", "type=bind,source=" + filepath.Join(root, "tasks", "t-1", "work") + ",target=/work"},
		{"-e", "TZ"}, {"TZ=Europe/Paris"},
		// The relay and the listener on the gateway of its network.
		{"HTTPS_PROXY=http://t-1:s3cret@192.168.130.1:8791"},
		{"--entrypoint", "sh", "worker", "-c"},
	} {
		if !hasSeq(args, want...) {
			t.Errorf("missing %v in %v", want, args)
		}
	}
	for _, docker := range []string{"--security-opt", "--pids-limit", "--add-host", "--no-healthcheck", "-v"} {
		if slices.Contains(args, docker) {
			t.Errorf("%s is Docker's, not container's: %v", docker, args)
		}
	}
	if c := strings.Count(strings.Join(args, " "), "--network"); c != 1 {
		t.Errorf("%d networks, want the task's own only (the brain too)", c)
	}
	// Its permission endpoint is the gateway's.
	if cb := n.callbackFor(task); cb != "http://192.168.130.1:8790" {
		t.Errorf("callback %s", cb)
	}
	// Gone with the task: its network, and the listener's filter for it.
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(calls), "network create --internal --label eriphe.task=t-1 eriphe-task-t-1") ||
		!strings.Contains(string(calls), "network delete eriphe-task-t-1") {
		t.Errorf("calls:\n%s", calls)
	}
	if n.nets.allowed(netip.MustParseAddr("192.168.130.7")) {
		t.Error("the task's network still reaches the node")
	}
}

// What a previous run left: its task containers and their networks, not the
// user's own containers.
func TestAppleRemoveStray(t *testing.T) {
	cli, dir := fakeContainer(t)
	n := newNode(options{docker: cli, engine: engineContainer})
	n.removeStrayContainers()
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(calls), "delete --force eriphe-task-old\n") || strings.Contains(string(calls), "mine") ||
		!strings.Contains(string(calls), "network delete eriphe-task-old") || strings.Contains(string(calls), "network delete default") {
		t.Errorf("calls:\n%s", calls)
	}
}

// A service's `container run`: hardened, on the right network.
func TestAppleServiceArgs(t *testing.T) {
	cli, dir := fakeContainer(t)
	root := filepath.Join(dir, "root")
	n := newNode(options{root: root, hostRoot: root, docker: cli, engine: engineContainer, serviceMemory: "1g"})
	for _, tc := range []struct {
		spec    serviceSpec
		network []string
	}{
		{serviceSpec{Image: "mcp/x", Transport: "stdio", Egress: true}, nil},
		{serviceSpec{Image: "mcp/x", Transport: "stdio"}, []string{"--network", "none"}},
		{serviceSpec{Image: "mcp/x", Transport: "http", Port: 8080}, []string{"--network", "eriphe-svc-x"}},
	} {
		args, _, target, err := n.serviceArgs("x", tc.spec)
		if err != nil {
			t.Fatal(err)
		}
		if target != "" {
			t.Errorf("target %q before it runs", target)
		}
		for _, want := range [][]string{{"--user", "1000:1000"}, {"--cap-drop", "ALL"}, {"--read-only"}, {"--memory", "1g"}, {"--label", "eriphe.service=x"}} {
			if !hasSeq(args, want...) {
				t.Errorf("missing %v in %v", want, args)
			}
		}
		if tc.network == nil && slices.Contains(args, "--network") || tc.network != nil && !hasSeq(args, tc.network...) {
			t.Errorf("network %v in %v", tc.network, args)
		}
	}
	// A service's network never reaches the task listener.
	if n.nets.allowed(netip.MustParseAddr("192.168.130.7")) {
		t.Error("a service's network reaches the node's listener")
	}
}

// The listener closes connections from outside the tasks' networks.
func TestTaskListener(t *testing.T) {
	nets := &taskNets{}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := taskListener{inner, nets}
	defer l.Close()
	if !nets.allowed(netip.MustParseAddr("127.0.0.1")) || !nets.allowed(netip.MustParseAddr("::1")) {
		t.Error("the loopback is refused")
	}
	nets.add("a", netip.MustParsePrefix("192.168.131.0/24"), netip.MustParseAddr("192.168.131.1"))
	for addr, want := range map[string]bool{"192.168.131.9": true, "::ffff:192.168.131.9": true, "192.168.132.9": false, "10.0.0.1": false} {
		if got := nets.allowed(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
	nets.remove("a")
	if nets.allowed(netip.MustParseAddr("192.168.131.9")) {
		t.Error("a removed network still reaches the node")
	}
	go func() {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err == nil {
			c.Write([]byte("x"))
			c.Close()
		}
	}()
	done := make(chan struct{})
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loopback was not accepted")
	}
}

func TestParseBytes(t *testing.T) {
	for s, want := range map[string]int64{"2g": 2 << 30, "500m": 500 << 20, "1024": 1024, "0": 0, "3K": 3 << 10} {
		if got, err := parseBytes(s); err != nil || got != want {
			t.Errorf("%s: %d, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "x", "-1g", "1.5g"} {
		if _, err := parseBytes(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
