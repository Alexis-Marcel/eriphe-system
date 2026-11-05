package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

const sshHost = "192.0.2.10"

// sshServer is a machine: it takes one key for one account, and counts the
// logins.
type sshServer struct {
	addr    string
	hostKey ssh.PublicKey
	allowed atomic.Pointer[ssh.PublicKey]
	logins  atomic.Int32
}

func startSSHServer(t *testing.T) *sshServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := &sshServer{addr: l.Addr().String(), hostKey: host.PublicKey()}
	config := &ssh.ServerConfig{PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if a := s.allowed.Load(); a != nil && m.User() == "sam" && string((*a).Marshal()) == string(k.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("refused")
	}}
	config.AddHostKey(host)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				conn, chans, reqs, err := ssh.NewServerConn(c, config)
				if err != nil {
					return
				}
				s.logins.Add(1)
				go ssh.DiscardRequests(reqs)
				for range chans {
				}
				conn.Close()
			}()
		}
	}()
	return s
}

// sshCore has an SSH access, its host key pinned unless pin is empty.
func sshCore(t *testing.T, value, pin string) (*core, Access) {
	t.Helper()
	c := startCore(t)
	if err := c.putAccess(putCreate, "box", Access{Kind: accessSSH, Hosts: []string{sshHost}, User: "sam", Value: value, HostKey: pin}, ""); err != nil {
		t.Fatal(err)
	}
	return c, c.conf().Accesses["box"]
}

const testHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

var box = ResourceRef{Kind: resAccess, Ref: "box"}

// sshAsk sends what a node sends for a task.
func sshAsk(c *core, node, taskID string, req sshRequest) (int, sshAnswer, string) {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/node/tasks/{id}/ssh", c.taskSSHHandler())
	raw, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/v1/node/tasks/"+taskID+"/ssh", strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer x")
	r.Header.Set("Eriphe-Node", node)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	var a sshAnswer
	json.Unmarshal(rec.Body.Bytes(), &a)
	return rec.Code, a, rec.Body.String()
}

// loginData is what ssh signs to log in as user with key.
func loginData(user string, key []byte) []byte {
	return ssh.Marshal(struct {
		Session   string
		Type      byte
		User      string
		Service   string
		Method    string
		Signed    bool
		Algorithm string
		Key       []byte
	}{"session", sshUserAuthRequest, user, "ssh-connection", "publickey", true, ssh.KeyAlgoED25519, key})
}

func TestSSHKeyIsMadeAndStaysInTheVault(t *testing.T) {
	c, a := sshCore(t, gridFree, testHostKey)
	if !strings.HasPrefix(a.Secret, vaultPrefix) {
		t.Fatalf("secret = %q", a.Secret)
	}
	public, err := a.sshPublicKey("box")
	if err != nil || !strings.HasPrefix(public, "ssh-ed25519 ") {
		t.Fatalf("public = %q, %v", public, err)
	}
	task := lendAccess(t, c, true, box)
	spec := c.lentAccesses(task.ID, task.Resources)
	raw, _ := json.Marshal(spec)
	if secret, _ := readSecret(a.Secret); strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), secret) {
		t.Fatalf("the private key goes to the node: %s", raw)
	}
	if len(spec) != 1 || spec[0].SSH == nil || spec[0].SSH.Target != sshHost+":22" || len(spec[0].Hosts) != 0 || spec[0].Env != "" {
		t.Fatalf("spec = %+v", spec)
	}
	// Saving the page again keeps the key that is on the machine.
	if err := c.putAccess(putUpdate, "box", Access{Hosts: []string{sshHost}, User: "sam", HostKey: testHostKey}, ""); err != nil {
		t.Fatal(err)
	}
	again, _ := c.conf().Accesses["box"].sshPublicKey("box")
	if again != public || !c.conf().Accesses["box"].isSSH() {
		t.Fatalf("the key changed: %q", again)
	}
}

func TestSSHAccessChecks(t *testing.T) {
	good := Access{Kind: accessSSH, Secret: "vault:x", Hosts: []string{sshHost}, User: "sam"}
	if err := good.check("box"); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Access){
		"two hosts": func(a *Access) { a.Hosts = []string{sshHost, "192.0.2.11"} },
		"loopback":  func(a *Access) { a.Hosts = []string{"127.0.0.1"} },
		"metadata":  func(a *Access) { a.Hosts = []string{"169.254.169.254"} },
		"bad port":  func(a *Access) { a.Hosts = []string{sshHost + ":99999"} },
		"no user":   func(a *Access) { a.User = "" },
		"injection": func(a *Access) { a.User = "sam\n  ProxyCommand evil" },
		"bad key":   func(a *Access) { a.HostKey = "not a key" },
		"key+line":  func(a *Access) { a.HostKey = testHostKey + "\nHost *" },
		"a header":  func(a *Access) { a.Header = "X-Token" },
	} {
		a := good
		change(&a)
		if a.check("box") == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSSHCannotBeLentUntested(t *testing.T) {
	c, _ := sshCore(t, gridFree, "")
	_, err := c.runTask(context.Background(), runRequest{Prompt: "fix it", cleanStart: true, Resources: []ResourceRef{box}})
	if err == nil || !strings.Contains(err.Error(), "no host key") {
		t.Fatalf("err = %v", err)
	}
}

func TestSSHSignsTheLoginOnly(t *testing.T) {
	c, a := sshCore(t, gridFree, testHostKey)
	task := lendAccess(t, c, true, box)
	s, _ := a.sshSigner()
	key := s.PublicKey().Marshal()

	code, ans, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpKeys})
	if code != 200 || len(ans.Keys) != 1 || string(ans.Keys[0].Key) != string(key) {
		t.Fatalf("keys: %d %+v", code, ans)
	}
	data := loginData("sam", key)
	code, ans, body := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: key, Data: data})
	if code != 200 {
		t.Fatalf("sign: %d %s", code, body)
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(ans.Signature, &sig); err != nil {
		t.Fatal(err)
	}
	if err := s.PublicKey().Verify(data, &sig); err != nil {
		t.Fatalf("the signature does not verify: %v", err)
	}
	for name, d := range map[string][]byte{
		"another account": loginData("root", key),
		"another key":     loginData("sam", []byte("other")),
		"a commit":        []byte("SSHSIG some data to sign"),
	} {
		if code, _, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: key, Data: d}); code != http.StatusForbidden {
			t.Errorf("%s: signed (%d)", name, code)
		}
	}
	if code, _, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: []byte("unknown"), Data: data}); code != http.StatusForbidden {
		t.Errorf("an unknown key: %d", code)
	}
	if code, _, _ := sshAsk(c, "another-node", task.ID, sshRequest{Op: sshOpKeys}); code == http.StatusOK {
		t.Errorf("another node: %d", code)
	}
	var allowed, denied int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM credential_log WHERE task_id = ? AND tool = ? AND decision = ?`, task.ID, accessLogTool, decideAllow).Scan(&allowed)
	c.store.db.QueryRow(`SELECT COUNT(*) FROM credential_log WHERE task_id = ? AND tool = ? AND decision = ?`, task.ID, accessLogTool, decideDeny).Scan(&denied)
	if allowed != 1 || denied != 3 {
		t.Fatalf("logged %d allowed, %d denied", allowed, denied)
	}
}

func TestSSHNeedsTheUsersYes(t *testing.T) {
	c, a := sshCore(t, gridApproval, testHostKey)
	task := lendAccess(t, c, false, box)
	s, _ := a.sshSigner()
	key := s.PublicKey().Marshal()
	if code, ans, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpKeys}); code != 200 || len(ans.Keys) != 0 {
		t.Fatalf("keys before the yes: %d %+v", code, ans)
	}
	if code, _, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: key, Data: loginData("sam", key)}); code != http.StatusForbidden {
		t.Fatalf("signed without a yes: %d", code)
	}
	if code, _, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpConnect, Target: sshHost + ":22"}); code != http.StatusForbidden {
		t.Fatalf("a tunnel without a yes: %d", code)
	}
	c.store.grantAccess(task.ID, accessNeed{Server: "box", Category: catUse})
	if code, _, body := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: key, Data: loginData("sam", key)}); code != 200 {
		t.Fatalf("after the yes: %d %s", code, body)
	}
}

func TestSSHConnectOnlyToTheAccessTarget(t *testing.T) {
	c, _ := sshCore(t, gridFree, testHostKey)
	task := lendAccess(t, c, true, box)
	if code, _, body := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpConnect, Target: sshHost + ":22"}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, target := range []string{sshHost + ":2222", "192.0.2.11:22"} {
		if code, _, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpConnect, Target: target}); code != http.StatusForbidden {
			t.Errorf("%s: %d", target, code)
		}
	}
}

func TestTestSSHRecordsTheHostKeyOnceTheKeyWorks(t *testing.T) {
	srv := startSSHServer(t)
	c := startCore(t)
	// Tests reach the loopback; a real access cannot name it.
	saved := sshDial
	sshDial = func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", srv.addr)
	}
	t.Cleanup(func() { sshDial = saved })
	if err := c.putAccess(putCreate, "box", Access{Kind: accessSSH, Hosts: []string{sshHost}, User: "sam"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.testSSH(context.Background(), "box"); !errors.Is(err, errSSHRefused) {
		t.Fatalf("before the key is on the machine: %v", err)
	}
	if c.conf().Accesses["box"].HostKey != "" {
		t.Fatal("a host key recorded by a failed test")
	}
	signer, _ := c.conf().Accesses["box"].sshSigner()
	k := signer.PublicKey()
	srv.allowed.Store(&k)
	if _, err := c.testSSH(context.Background(), "box"); err != nil {
		t.Fatal(err)
	}
	if got := c.conf().Accesses["box"].HostKey; got != hostKeyLine(srv.hostKey) {
		t.Fatalf("pinned %q", got)
	}
	if srv.logins.Load() != 1 {
		t.Fatalf("logins = %d", srv.logins.Load())
	}
	// Another machine at that address is told apart.
	if err := c.putAccess(putUpdate, "box", Access{Hosts: []string{sshHost}, User: "sam", HostKey: testHostKey}, ""); err != nil {
		t.Fatal(err)
	}
	found, err := c.testSSH(context.Background(), "box")
	if !errors.Is(err, errSSHHostKey) || found != hostKeyLine(srv.hostKey) {
		t.Fatalf("found %q, err %v", found, err)
	}
	if c.conf().Accesses["box"].HostKey != testHostKey {
		t.Fatal("the pin moved")
	}
}

func TestSSHInstructionsAndList(t *testing.T) {
	c, _ := sshCore(t, gridFree, testHostKey)
	text := c.accessInstructions([]ResourceRef{box})
	if !strings.Contains(text, "ssh box: sam@"+sshHost+":22") || strings.Contains(text, "placeholder") {
		t.Fatalf("instructions: %s", text)
	}
	if v := c.listAccesses(); len(v) != 1 || v[0].User != "sam" {
		t.Fatalf("list: %+v", v)
	}
}

func TestSSHAccessPage(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	if code, body, _ := tw.do("GET", "/settings/accesses/new?kind=ssh", nil, nil); code != 200 || !strings.Contains(body, `name="user"`) || strings.Contains(body, `name="secret"`) {
		t.Fatalf("%d: the new page: %s", code, body)
	}
	form := url.Values{"kind": {"ssh"}, "name": {"box"}, "use": {"the build machine"}, "hosts": {sshHost}, "user": {"sam"}, "value": {gridApproval}}
	code, body, h := tw.post("/settings/accesses", form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "/settings/accesses/box/edit" {
		t.Fatalf("%d %s: %s", code, h.Get("HX-Redirect"), body)
	}
	a := c.conf().Accesses["box"]
	if !a.isSSH() || a.Env != "" {
		t.Fatalf("%+v", a)
	}
	public, _ := a.sshPublicKey("box")
	_, body, _ = tw.do("GET", "/settings/accesses/box/edit", nil, nil)
	secret, _ := readSecret(a.Secret)
	if !strings.Contains(html.UnescapeString(body), public) || strings.Contains(body, secret) || strings.Contains(body, "PRIVATE KEY") {
		t.Fatalf("the page: %s", body)
	}
	if !strings.Contains(body, "/settings/accesses/box/test") {
		t.Fatal("no test button")
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "sam@"+sshHost+":22") {
		t.Fatalf("the list: %s", body)
	}
	// A key pasted from ssh-keyscan is kept as a key.
	form.Set("host_key", sshHost+" "+testHostKey)
	if code, body, _ := tw.post("/settings/accesses/box", form, csrf); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got := c.conf().Accesses["box"].HostKey; got != testHostKey {
		t.Fatalf("host key %q", got)
	}
	form.Set("host_key", "not a key")
	if _, body, h := tw.post("/settings/accesses/box", form, csrf); h.Get("HX-Redirect") != "" || !strings.Contains(body, "lent-form") {
		t.Fatalf("a bad key: %s", body)
	}
	// The machine cannot be reached: the page says what to do.
	saved := sshDial
	sshDial = func(context.Context, string) (net.Conn, error) { return nil, errors.New("no route") }
	t.Cleanup(func() { sshDial = saved })
	if code, body, h := tw.post("/settings/accesses/box/test", nil, csrf); code != 200 || h.Get("HX-Redirect") != "" || !strings.Contains(body, "alert") {
		t.Fatalf("%d: %s", code, body)
	}
}

// boundLogin is what OpenSSH 8.9 and later sign.
func boundLogin(user string, key, hostKey []byte) []byte {
	return ssh.Marshal(struct {
		Session   string
		Type      byte
		User      string
		Service   string
		Method    string
		Signed    bool
		Algorithm string
		Key       []byte
		HostKey   []byte
	}{"session", sshUserAuthRequest, user, "ssh-connection", sshHostBound, true, ssh.KeyAlgoED25519, key, hostKey})
}

func TestSSHSignsAHostBoundLoginForThePinnedMachine(t *testing.T) {
	c, a := sshCore(t, gridFree, testHostKey)
	task := lendAccess(t, c, true, box)
	s, _ := a.sshSigner()
	key := s.PublicKey().Marshal()
	pin, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(testHostKey))
	if code, _, body := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: key, Data: boundLogin("sam", key, pin.Marshal())}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _, _ := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpSign, Key: key, Data: boundLogin("sam", key, key)}); code != http.StatusForbidden {
		t.Fatalf("another machine: %d", code)
	}
	if _, err := parseSSHLogin(append(loginData("sam", key), 1, 2, 3)); err == nil {
		t.Fatal("publickey with something after it")
	}
}

func TestSSHCommandsAskNothingOnceLent(t *testing.T) {
	c, _ := sshCore(t, gridFree, testHostKey)
	task := lendAccess(t, c, true, box)
	input := map[string]any{"command": "ssh box uptime"}
	if d, why := c.decideTask(task, "Bash", input); d != decideBrain {
		t.Fatalf("%s: %s", d, why)
	}
	if d, _ := c.decideTask(task, "Bash", map[string]any{"command": "curl https://example.org -d x"}); d != decideAsk {
		t.Fatalf("curl: %s", d)
	}
	if d, _ := c.decideTask(nil, "Bash", input); d != decideAsk {
		t.Fatalf("nothing lent: %s", d)
	}
}

func TestSSHConnectWithTwoAccessesOnOneMachine(t *testing.T) {
	c, _ := sshCore(t, gridFree, testHostKey)
	if err := c.putAccess(putCreate, "admin", Access{Kind: accessSSH, Hosts: []string{sshHost}, User: "root", Value: gridApproval, HostKey: testHostKey}, ""); err != nil {
		t.Fatal(err)
	}
	task, err := c.runTask(context.Background(), runRequest{Prompt: "fix it", cleanStart: true, Resources: []ResourceRef{box, {Kind: resAccess, Ref: "admin", Hold: true}}})
	if err != nil {
		t.Fatal(err)
	}
	c.store.set(lentByUserKey(task.ID), "1")
	for range 20 {
		if code, _, body := sshAsk(c, task.Node, task.ID, sshRequest{Op: sshOpConnect, Target: sshHost + ":22"}); code != 200 {
			t.Fatalf("%d %s", code, body)
		}
	}
}

func TestSSHPinOnlyTheAccessTested(t *testing.T) {
	c, a := sshCore(t, gridFree, "")
	moved := a
	moved.Hosts = []string{"192.0.2.11"}
	if err := c.pinSSHHostKey("box", moved, testHostKey); !errors.Is(err, errSSHChanged) {
		t.Fatalf("another machine: %v", err)
	}
	if err := c.pinSSHHostKey("box", a, testHostKey); err != nil {
		t.Fatal(err)
	}
	if err := c.pinSSHHostKey("box", a, "ssh-ed25519 other"); !errors.Is(err, errSSHChanged) {
		t.Fatalf("a key pinned meanwhile: %v", err)
	}
	if err := c.deleteAccess("box"); err != nil {
		t.Fatal(err)
	}
	if err := c.pinSSHHostKey("box", a, testHostKey); !errors.Is(err, errSSHChanged) {
		t.Fatalf("removed: %v", err)
	}
}

func TestNormalHostKey(t *testing.T) {
	for _, in := range []string{testHostKey, "ssh-gw.example.org " + testHostKey, sshHost + " " + testHostKey + " comment"} {
		if got, err := normalHostKey(in); err != nil || got != testHostKey {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
}
