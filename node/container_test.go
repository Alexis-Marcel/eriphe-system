package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBindMountStaysInRoot(t *testing.T) {
	n := &node{opt: options{root: "/data/node", hostRoot: "/mnt/node"}}
	got, err := n.bindMount("/data/node/tasks/t-1/work", "/work")
	if err != nil || strings.Join(got, " ") != "-v /mnt/node/tasks/t-1/work:/work" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, p := range []string{"/data/node/../etc", "/etc", "/data/nodeX/x", "/data"} {
		if _, err := n.bindMount(p, "/x"); err == nil {
			t.Errorf("%s: mounted, want refused", p)
		}
	}
}

// The docker run command of a task carries the hardening and the limits.
func TestStartContainerArgs(t *testing.T) {
	for _, rt := range []string{"claude-code", "opencode"} {
		t.Run(rt, func(t *testing.T) { testStartContainerArgs(t, rt) })
	}
}

func testStartContainerArgs(t *testing.T, rt string) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nif [ \"$1\" = network ]; then echo \"172.17.0.0/16 172.17.0.1;\"; exit; fi\nprintf '%s\\n' \"$@\" TZ=$TZ HTTPS_PROXY=$HTTPS_PROXY NO_PROXY=$NO_PROXY > " + argsFile + "\ncat >/dev/null\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	n := newNode(options{root: root, hostRoot: root, docker: fake, taskMemory: "8g", taskCPUs: "2", cacheVolume: "eriphe-cache",
		taskNetwork: "eriphe-tasks", network: "eriphe-system", serviceNetwork: "eriphe-services",
		callbackURL: "http://eriphe-node:8790", listenAddr: "172.17.0.2:8790", egressPort: "8791"})
	task := &Task{ID: "t-1", secret: "s3cret", dir: filepath.Join(root, "tasks", "t-1"), Spec: TaskSpec{Image: "worker", Prompt: "hi", Runtime: rt, TZ: "Europe/Paris"}}
	if err := n.startContainer(task, "hi"); err != nil {
		t.Fatal(err)
	}
	task.run.stop()
	waitFor(t, func() bool { return finished(task.state()) })
	raw, _ := os.ReadFile(argsFile)
	args := strings.Split(string(raw), "\n")
	for _, want := range [][]string{
		{"--user", "1000:1000"}, {"--cap-drop", "ALL"}, {"--security-opt", "no-new-privileges"},
		{"--pids-limit", "1024"}, {"--read-only"}, {"--memory", "8g"}, {"--cpus", "2"},
		{"-v", filepath.Join(root, "tasks", "t-1", "work") + ":/work"},
		{"eriphe", map[string]string{"claude-code": "claude", "opencode": "sh"}[rt]},
		{"--network", "eriphe-tasks"}, {"--add-host", "host.docker.internal:172.17.0.1"},
		{"-e", "TZ"}, {"TZ=Europe/Paris"}, // by name, the value in docker's environment
		// The egress relay, with the task's credentials; the node itself direct.
		{"-e", "HTTPS_PROXY"}, {"HTTPS_PROXY=http://t-1:s3cret@eriphe-node:8791"},
		{"NO_PROXY=eriphe-node,localhost,127.0.0.1,172.17.0.2"},
	} {
		found := false
		for i := range args {
			found = found || slices.Equal(args[i:min(i+len(want), len(args))], want)
		}
		if !found {
			t.Errorf("missing %v in %v", want, args)
		}
	}
}

// A task is on the task network only, never on the services' or eriphe-core's.
func TestTaskNetworkOnly(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nif [ \"$1\" = network ]; then echo \"10.9.0.0/24 10.9.0.1;\"; exit; fi\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat >/dev/null\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	for _, tc := range []struct{ taskNetwork, want string }{{"eriphe-tasks", "eriphe-tasks"}, {"", "bridge"}} {
		n := newNode(options{root: root, hostRoot: root, docker: fake, network: "eriphe-system",
			serviceNetwork: "eriphe-services", taskNetwork: tc.taskNetwork})
		task := &Task{ID: "t-1", dir: filepath.Join(root, "tasks", "t-1"), Spec: TaskSpec{Image: "worker", Prompt: "hi", Runtime: "claude-code"}}
		if err := n.startContainer(task, "hi"); err != nil {
			t.Fatal(err)
		}
		task.run.stop()
		waitFor(t, func() bool { return finished(task.state()) })
		raw, _ := os.ReadFile(argsFile)
		args := strings.Split(string(raw), "\n")
		var nets []string
		for i, a := range args {
			if a == "--network" {
				nets = append(nets, args[i+1])
			}
		}
		if !slices.Equal(nets, []string{tc.want}) {
			t.Errorf("task networks %v, want [%s]", nets, tc.want)
		}
		if !slices.Contains(args, "host.docker.internal:10.9.0.1") {
			t.Errorf("host.docker.internal is not the task network's gateway: %v", args)
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
