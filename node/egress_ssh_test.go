package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const sshLAN = "192.168.1.20:22"

// sshRelay is a relay whose task has an SSH access on the LAN.
func sshRelay(t *testing.T, open func(target string) error) *testRelay {
	t.Helper()
	r := newTestRelay(t, upstreamServer(t).Listener.Addr().String(), func(int, string) []string { return []string{"192.168.1.30"} })
	r.tunnels = []string{sshLAN}
	r.p.sshOpen = func(_ context.Context, _, target string) error { return open(target) }
	return r
}

func TestTunnelReachesTheAccessOnly(t *testing.T) {
	var asked []string
	r := sshRelay(t, func(target string) error {
		asked = append(asked, target)
		return nil
	})
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusOK {
		t.Fatalf("the access's machine: %d", code)
	}
	if !slices.Equal(r.endpointDialed, []string{sshLAN}) || !slices.Equal(asked, []string{sshLAN}) {
		t.Fatalf("dialed %v, asked %v", r.endpointDialed, asked)
	}
	for _, target := range []string{"192.168.1.20:80", "192.168.1.20:2222", "192.168.1.21:22", "192.168.1.1:443", "example.org:22"} {
		if code, _, _ := r.connect(t, target, basic("t-1", "s")); code != http.StatusForbidden {
			t.Errorf("%s: %d", target, code)
		}
	}
	if len(r.endpointDialed) != 1 || len(asked) != 1 {
		t.Fatalf("dialed %v, asked %v", r.endpointDialed, asked)
	}
}

func TestTunnelWaitsForEriphe(t *testing.T) {
	r := sshRelay(t, func(string) error { return refused("the access box is held back") })
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusForbidden {
		t.Fatalf("a held access: %d", code)
	}
	if len(r.endpointDialed) != 0 {
		t.Fatalf("dialed %v", r.endpointDialed)
	}
	r.p.sshOpen = nil
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusForbidden {
		t.Fatalf("no answer: %d", code)
	}
}

func TestTunnelRefusedAtTheNodeAndWithoutEgress(t *testing.T) {
	r := sshRelay(t, func(string) error { return nil })
	r.tunnels = []string{"127.0.0.1:22"}
	if code, _, _ := r.connect(t, "127.0.0.1:22", basic("t-1", "s")); code != http.StatusForbidden {
		t.Errorf("the loopback: %d", code)
	}
	r.tunnels = []string{sshLAN}
	r.rule = EgressRule{Mode: egressNone}
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusForbidden {
		t.Errorf("egress none: %d", code)
	}
}

func sshTask() *Task {
	return &Task{ID: "t-1", secret: "s", State: stateRunning, Spec: TaskSpec{Accesses: []TaskAccess{{Name: "box",
		SSH: &TaskSSH{Target: "192.0.2.10:2222", User: "sam", HostKey: "ssh-ed25519 AAAA", PublicKey: "ssh-ed25519 BBBB eriphe-box"}}}}}
}

func TestSSHFilesHoldNoSecretAndPinTheHost(t *testing.T) {
	files := withSSHFiles(sshTask(), nil)
	config := string(files[sshConfigFile])
	for _, want := range []string{"Host box 192.0.2.10\n", "HostName 192.0.2.10", "Port 2222", "User sam", "HostKeyAlias box", "IdentitiesOnly yes",
		"StrictHostKeyChecking yes", injectDir + "/bin/eriphe-cred tunnel %h %p", runPath(sshPublicFile("box"))} {
		if !strings.Contains(config, want) {
			t.Errorf("config lacks %q:\n%s", want, config)
		}
	}
	if got := string(files[sshKnownFile]); got != "box ssh-ed25519 AAAA\n" {
		t.Errorf("known hosts: %q", got)
	}
	if got := string(files[sshPublicFile("box")]); got != "ssh-ed25519 BBBB eriphe-box\n" {
		t.Errorf("public key: %q", got)
	}
	if got := withSSHFiles(&Task{}, nil); got != nil {
		t.Errorf("no access: %v", got)
	}
}

func TestSSHEnvAndEntrypoint(t *testing.T) {
	n := newNode(options{})
	if env := n.sshEnv(&Task{}); env != nil {
		t.Errorf("no access: %v", env)
	}
	env := n.sshEnv(sshTask())
	if env["SSH_AUTH_SOCK"] != sshAgentSock || env["ERIPHE_SSH_CONFIG"] != runPath(sshConfigFile) || !strings.HasSuffix(env["ERIPHE_SSH_URL"], sshPath) {
		t.Errorf("env: %v", env)
	}
	if a := accessEnv(sshTask()); a[""] != "" {
		t.Errorf("an empty variable: %v", a)
	}
	if _, ok := accessEnv(sshTask())[""]; ok {
		t.Error("an access with no variable sets one")
	}
	if !strings.Contains(entrypoint, sshAccessScript) || strings.Index(entrypoint, sshAccessScript) > strings.Index(entrypoint, "git clone") {
		t.Error("the entrypoint does not set ssh up first")
	}
}

func TestSSHHandlerRelaysKeysAndSignaturesOnly(t *testing.T) {
	n := newNode(options{})
	n.tasks["t-1"] = sshTask()
	var got []string
	h := n.sshHandlerWith(func(_ context.Context, taskID string, body []byte) (int, []byte, error) {
		got = append(got, taskID+" "+string(body))
		if strings.Contains(string(body), "unavailable") {
			return 0, nil, errors.New("down")
		}
		return http.StatusOK, []byte(`{"keys":[]}`), nil
	})
	call := func(secret, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, sshPath, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("s", `{"op":"keys"}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(got) != 1 || got[0] != `t-1 {"op":"keys"}` {
		t.Fatalf("asked %v", got)
	}
	if w := call("other", `{"op":"keys"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong secret: %d", w.Code)
	}
	// A tunnel is the relay's to ask for, not the task's.
	if w := call("s", `{"op":"connect","target":"192.0.2.10:22"}`); w.Code != http.StatusBadRequest {
		t.Errorf("connect: %d", w.Code)
	}
	if w := call("s", `{"op":"sign","data":"unavailable"}`); w.Code != http.StatusBadGateway {
		t.Errorf("core down: %d", w.Code)
	}
	n.tasks["t-1"].State = stateStopped
	if w := call("s", `{"op":"keys"}`); w.Code != http.StatusGone {
		t.Errorf("a stopped task: %d", w.Code)
	}
}
