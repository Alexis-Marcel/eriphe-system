package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckNetworks(t *testing.T) {
	for _, tc := range []struct {
		o  options
		ok bool
	}{
		{options{taskNetwork: "eriphe-tasks", network: "eriphe-system", serviceNetwork: "eriphe-services"}, true},
		{options{taskNetwork: "eriphe-tasks"}, true},
		{options{}, true}, // tasks on the default bridge; http services with egress are refused there
		{options{taskNetwork: "eriphe-system", network: "eriphe-system"}, false},
		{options{taskNetwork: "", network: "bridge"}, false},
		{options{taskNetwork: "eriphe-services", serviceNetwork: "eriphe-services"}, false},
	} {
		if err := checkNetworks(tc.o); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc.o, err)
		}
	}
}

// A service is never put on the tasks' network.
func TestServiceNotOnTaskNetwork(t *testing.T) {
	docker, _ := fakeDocker(t, "true")
	n := newNode(options{root: t.TempDir(), docker: docker}) // tasks and services on the default bridge
	if _, _, _, err := n.serviceArgs("web", serviceSpec{Image: "x", Transport: "http", Port: 80, Egress: true}); err == nil {
		t.Error("an http service shares the default bridge with the tasks")
	}
	if _, _, _, err := n.serviceArgs("time", serviceSpec{Image: "x", Transport: "stdio", Egress: true}); err != nil {
		t.Errorf("stdio service: %v", err)
	}
	n.opt.taskNetwork = "eriphe-tasks"
	for _, spec := range []serviceSpec{
		{Image: "x", Transport: "http", Port: 80, Egress: true},
		{Image: "x", Transport: "stdio", Egress: true},
	} {
		args, _, _, err := n.serviceArgs("s", spec)
		if err != nil || strings.Contains(strings.Join(args, " "), "eriphe-tasks") {
			t.Errorf("%+v: %v %v", spec, args, err)
		}
	}
}

func TestPickListenIP(t *testing.T) {
	addr := func(s string) net.Addr {
		ip, n, _ := net.ParseCIDR(s)
		n.IP = ip
		return n
	}
	addrs := []net.Addr{addr("127.0.0.1/8"), addr("192.168.0.30/24"), addr("172.20.0.3/16"), addr("fe80::1/64")}
	if ip, ok := pickListenIP([]string{"172.20.0.0/16"}, addrs); !ok || ip.String() != "172.20.0.3" {
		t.Errorf("node on the network: %v %v", ip, ok)
	}
	// On the Docker host: the bridge's gateway is the host's address there.
	if ip, ok := pickListenIP([]string{"bad", "fd00::/64", "192.168.0.0/24"}, addrs); !ok || ip.String() != "192.168.0.30" {
		t.Errorf("gateway: %v %v", ip, ok)
	}
	if _, ok := pickListenIP([]string{"10.99.0.0/16"}, addrs); ok {
		t.Error("an address outside the subnets")
	}
}

// The task listener binds this node's address on the task network, never
// every interface, unless told so.
func TestListenAddr(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	fake := filepath.Join(t.TempDir(), "docker")
	// The loopback stands for the task network: every machine has it.
	os.WriteFile(fake, []byte("#!/bin/sh\necho '127.0.0.0/8 127.0.0.1;'\n"), 0o755)
	n := newNode(options{docker: fake, taskNetwork: "eriphe-tasks"})
	for in, want := range map[string]string{":8790": "127.0.0.1:8790", ":9000": "127.0.0.1:9000", "0.0.0.0:8790": "0.0.0.0:8790", "10.0.0.2:8790": "10.0.0.2:8790"} {
		if got, err := n.listenAddr(in); err != nil || got != want {
			t.Errorf("listen %q: %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := n.listenAddr("8790"); err == nil {
		t.Error("a listen address without a port: accepted")
	}
	// Nothing local on the network: the loopback, not every interface.
	os.WriteFile(fake, []byte("#!/bin/sh\necho '10.99.0.0/16 10.99.0.1;'\n"), 0o755)
	if got, err := n.listenAddr(":8790"); err != nil || got != "127.0.0.1:8790" {
		t.Errorf("fallback: %q %v", got, err)
	}
	os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if _, err := n.listenAddr(":8790"); err == nil {
		t.Error("an unknown task network: accepted")
	}
}

// A missing task network is created, as a bridge whose containers cannot
// reach one another, with no route out; an existing one is left as it is.
func TestEnsureTaskNetwork(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	fake, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$*\" >> "+calls+"\n[ \"$2\" = inspect ] && [ -f "+dir+"/exists ] && echo true && exit 0\n[ \"$2\" = inspect ] && exit 1\ntouch "+dir+"/exists\n"), 0o755)
	n := newNode(options{docker: fake, taskNetwork: "eriphe-tasks"})
	if err := n.ensureTaskNetwork(); err != nil {
		t.Fatal(err)
	}
	if err := n.ensureTaskNetwork(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(calls)
	if got := strings.Count(string(raw), "network create"); got != 1 ||
		!strings.Contains(string(raw), "network create --driver bridge --internal -o com.docker.network.bridge.enable_icc=false --label eriphe.network=tasks eriphe-tasks") {
		t.Errorf("calls:\n%s", raw)
	}
}

// The brain, on two networks, calls the node back on its bound address;
// workers keep the configured URL.
func TestCallbackForBrain(t *testing.T) {
	n := &node{opt: options{callbackURL: "http://eriphe-node:8790", listenAddr: "192.168.224.2:8790"}}
	if got := n.callbackFor(&Task{Spec: TaskSpec{SystemNetwork: true}}); got != "http://192.168.224.2:8790" {
		t.Fatalf("brain callback = %s", got)
	}
	if got := n.callbackFor(&Task{}); got != "http://eriphe-node:8790" {
		t.Fatalf("worker callback = %s", got)
	}
	n.opt.listenAddr = "127.0.0.1:8790"
	if got := n.callbackFor(&Task{Spec: TaskSpec{SystemNetwork: true}}); got != "http://eriphe-node:8790" {
		t.Fatalf("loopback must not be used: %s", got)
	}
}
