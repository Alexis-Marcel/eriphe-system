package main

import (
	"context"
	"errors"
	"io"
	"net"
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

// A session goes through eriphe-core: the node dials nothing, unless
// eriphe-core cannot reach the machine or does not carry sessions yet.
func TestTunnelThroughCore(t *testing.T) {
	var asked []string
	r := sshRelay(t, func(target string) error {
		asked = append(asked, target)
		return nil
	})
	var answer error
	r.p.sshCore = func(context.Context, string, string) (net.Conn, error) {
		if answer != nil {
			return nil, answer
		}
		mine, theirs := net.Pipe()
		t.Cleanup(func() { theirs.Close() })
		return mine, nil
	}
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusOK {
		t.Fatalf("through eriphe-core: %d", code)
	}
	if len(r.endpointDialed) != 0 || len(asked) != 0 {
		t.Fatalf("dialed %v, asked %v", r.endpointDialed, asked)
	}
	answer = refused("the access box is held back")
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusForbidden {
		t.Fatalf("refused: %d", code)
	}
	if len(r.endpointDialed) != 0 {
		t.Fatalf("a refused session was dialed: %v", r.endpointDialed)
	}
	answer = errCoreCannotReach
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusOK {
		t.Fatalf("a machine only the node reaches: %d", code)
	}
	if !slices.Equal(r.endpointDialed, []string{sshLAN}) || len(asked) != 0 {
		t.Fatalf("dialed %v, asked %v", r.endpointDialed, asked)
	}
	answer = errNoCoreSession
	if code, _, _ := r.connect(t, sshLAN, basic("t-1", "s")); code != http.StatusOK {
		t.Fatalf("an older eriphe-core: %d", code)
	}
	if !slices.Equal(asked, []string{sshLAN}) {
		t.Fatalf("an older eriphe-core was not asked: %v", asked)
	}
}

// What eriphe-core answers to a session, as the relay reads it, and the
// connection staying up once the request that opened it is over.
func TestSSHCoreAnswers(t *testing.T) {
	status := http.StatusForbidden
	var got *http.Request
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		if status != http.StatusSwitchingProtocols {
			http.Error(w, "held back", status)
			return
		}
		conn, rw, _ := http.NewResponseController(w).Hijack()
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + sshProto + "\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		b := make([]byte, 4)
		io.ReadFull(rw, b)
		conn.Write(b)
	}))
	t.Cleanup(core.Close)
	s := &state{Core: core.URL, Name: "n1", Secret: "x"}
	var denied *egressError
	if _, err := s.sshCore(context.Background(), "t-1", sshLAN); !errors.As(err, &denied) || denied.status != http.StatusForbidden || denied.msg != "held back" {
		t.Fatalf("refused: %v", err)
	}
	if got.URL.Path != "/v1/node/tasks/t-1/ssh/connect" || got.URL.Query().Get("target") != sshLAN || got.Header.Get("Upgrade") != sshProto {
		t.Fatalf("asked %s %v", got.URL, got.Header)
	}
	status = http.StatusBadGateway
	if _, err := s.sshCore(context.Background(), "t-1", sshLAN); !errors.Is(err, errCoreCannotReach) {
		t.Fatalf("unreachable: %v", err)
	}
	status = http.StatusNotFound
	if _, err := s.sshCore(context.Background(), "t-1", sshLAN); !errors.Is(err, errNoCoreSession) {
		t.Fatalf("an older eriphe-core: %v", err)
	}
	status = http.StatusSwitchingProtocols
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := s.sshCore(ctx, "t-1", sshLAN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(conn, b); err != nil || string(b) != "ping" {
		t.Fatalf("through the session: %q %v", b, err)
	}
	cancel()
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
