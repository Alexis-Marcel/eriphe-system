package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const (
	accessSecret = "npm_real_secret"
	accessHost   = "registry.example.org"
)

// fakeService is the service behind an access, on a fake Internet: every
// host leads to it, whatever it is called.
type fakeService struct {
	mu   sync.Mutex
	got  []*http.Request
	body string
}

func (f *fakeService) last(t *testing.T) *http.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("nothing reached the service")
	}
	return f.got[len(f.got)-1]
}

func (f *fakeService) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func startService(t *testing.T) *fakeService {
	t.Helper()
	f := &fakeService{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.got = append(f.got, r.Clone(context.Background()))
		f.body = string(body)
		f.mu.Unlock()
		// A service that reflects what it was sent, in a header.
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	old := accessTransport
	accessTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}
	t.Cleanup(func() { accessTransport = old })
	return f
}

// accessCore is a core with an access, npm, of a value, and the service.
func accessCore(t *testing.T, a Access) (*core, *fakeService) {
	t.Helper()
	c := startCore(t)
	if a.Hosts == nil {
		a.Hosts = []string{accessHost}
	}
	if a.Env == "" {
		a.Env = "NPM_TOKEN"
	}
	if err := c.putAccess(putCreate, "npm", a, accessSecret); err != nil {
		t.Fatal(err)
	}
	return c, startService(t)
}

// lendAccess starts a task with the access, as a clean turn of the user's
// does, or not.
func lendAccess(t *testing.T, c *core, clean bool, e ResourceRef) *TaskRow {
	t.Helper()
	task, err := c.runTask(context.Background(), runRequest{Prompt: "publish it", askStart: true, cleanStart: clean, Resources: []ResourceRef{e}})
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		c.store.set(lentByUserKey(task.ID), "1")
	}
	if task.Node == "" {
		t.Fatal("no node")
	}
	return task
}

var npm = ResourceRef{Kind: resAccess, Ref: "npm"}

// viaRelay sends what the node's relay sends for one request of the task.
func viaRelay(c *core, node, taskID, host, method, target, auth string, more map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("/v1/node/tasks/{id}/access", c.taskAccessHandler())
	req := httptest.NewRequest(method, "/v1/node/tasks/"+taskID+"/access", strings.NewReader("a body"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Eriphe-Node", node)
	req.Header.Set(accessHostHeader, host)
	req.Header.Set(accessTargetHeader, target)
	if auth != "" {
		req.Header.Set(accessAuthHeader, auth)
	}
	for k, v := range more {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func placeholderAuth(taskID string) string { return "Bearer " + accessPlaceholder(taskID, "npm") }

func TestAccessInjectsTheSecret(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	rec := viaRelay(c, "nas", task.ID, accessHost+":443", "PUT", "/pkg%2Fname?tag=latest", placeholderAuth(task.ID), nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := svc.last(t)
	if v := got.Header.Get("Authorization"); v != "Bearer "+accessSecret {
		t.Errorf("Authorization = %q", v)
	}
	if got.Host != accessHost || got.URL.EscapedPath() != "/pkg%2Fname" || got.URL.RawQuery != "tag=latest" || got.Method != "PUT" {
		t.Errorf("request = %s %s %s", got.Method, got.Host, got.URL)
	}
	for k := range got.Header {
		if strings.HasPrefix(k, "Eriphe-") {
			t.Errorf("the service was sent %s", k)
		}
	}
	if svc.body != "a body" {
		t.Errorf("body = %q", svc.body)
	}
	// The log has the use, never the secret.
	entries, _ := c.store.accessLog(task.ID, 10)
	if len(entries) != 1 || entries[0].Tool != accessLogTool || entries[0].Target != "npm" || entries[0].Host != accessHost || entries[0].Decision != decideAllow {
		t.Fatalf("log = %+v", entries)
	}
	if strings.Contains(entries[0].Reason, accessSecret) {
		t.Error("the secret is in the log")
	}
}

// Without a placeholder, the node's own secret must not go out in its place.
func TestAccessNeverSendsTheNodesSecret(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/public", "", nil); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if v, ok := svc.last(t).Header["Authorization"]; ok {
		t.Errorf("Authorization = %q", v)
	}
	// It asked for no secret: nothing to log.
	if entries, _ := c.store.accessLog(task.ID, 10); len(entries) != 0 {
		t.Errorf("log = %+v", entries)
	}
}

// git sends its password as Basic.
func TestAccessInjectsInBasic(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+accessPlaceholder(task.ID, "npm")))
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", auth, nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if user, pass, ok := svc.last(t).BasicAuth(); !ok || user != "x-access-token" || pass != accessSecret {
		t.Errorf("Basic = %q %q", user, pass)
	}
}

// Only the declared header takes the secret.
func TestAccessInjectsInItsHeaderOnly(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree, Header: "x-api-key"})
	task := lendAccess(t, c, true, npm)
	p := accessPlaceholder(task.ID, "npm")
	rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", "Bearer "+p, map[string]string{"X-Api-Key": p, "X-Other": p})
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	got := svc.last(t)
	if got.Header.Get("X-Api-Key") != accessSecret || got.Header.Get("Authorization") != "Bearer "+p || got.Header.Get("X-Other") != p {
		t.Errorf("headers = %v", got.Header)
	}
}

// A task reaches the secret through its own placeholder, on its own hosts.
func TestAccessStaysWithItsTaskAndHosts(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	other := lendAccess(t, c, true, ResourceRef{Kind: resRepo, Ref: "owner/repo"})

	if rec := viaRelay(c, "nas", task.ID, "evil.example.org:443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 403 {
		t.Errorf("an undeclared host: %d", rec.Code)
	}
	if rec := viaRelay(c, "nas", task.ID, accessHost+":8443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 403 {
		t.Errorf("another port: %d", rec.Code)
	}
	if rec := viaRelay(c, "nas", other.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 403 {
		t.Errorf("a task it is not lent to: %d", rec.Code)
	}
	if svc.calls() != 0 {
		t.Fatalf("%d reached the service", svc.calls())
	}
	// Another task's placeholder is not a placeholder here.
	again := lendAccess(t, c, true, npm)
	if rec := viaRelay(c, "nas", again.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if v := svc.last(t).Header.Get("Authorization"); strings.Contains(v, accessSecret) {
		t.Errorf("Authorization = %q", v)
	}
}

func TestAccessAsksTheNodeWhoItIs(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	c.store.registerNode("pc", hashSecret("x"))
	if rec := viaRelay(c, "pc", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 404 {
		t.Errorf("another node: %d", rec.Code)
	}
	if rec := viaRelay(c, "ghost", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 401 {
		t.Errorf("no node: %d", rec.Code)
	}
	c.store.updateTask(task.ID, map[string]any{"state": stateStopped})
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 404 {
		t.Errorf("a task over: %d", rec.Code)
	}
	if svc.calls() != 0 {
		t.Errorf("%d reached the service", svc.calls())
	}
}

// A secret a service echoes in a header does not come back; a body is out
// of reach, which the access's page says.
func TestAccessScrubsResponseHeaders(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil)
	if v := rec.Header().Get("X-Echo"); v != placeholderAuth(task.ID) {
		t.Errorf("X-Echo = %q", v)
	}
}

// On approval: a card at launch, and no use without its yes.
func TestAccessOnApprovalAsksAtLaunch(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridApproval})
	task := lendAccess(t, c, true, npm)
	if !c.heldTask(task.ID) {
		t.Fatal("not held")
	}
	card, ok := startCard(t, c)
	if !ok {
		t.Fatal("no card")
	}
	if ids := assignedIDs(t, c); len(ids) != 0 {
		t.Fatalf("assigned before the yes: %v", ids)
	}
	_, id, _ := parseProposalRef(card.Ref)
	s, _ := c.taskStart(id)
	if got := startText(s.Needs, "en"); !strings.Contains(got, "use the credential npm") {
		t.Errorf("card = %q", got)
	}
	if got := startText(s.Needs, "fr"); !strings.Contains(got, "utiliser l'identifiant npm") {
		t.Errorf("card = %q", got)
	}
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 403 {
		t.Errorf("before the yes: %d", rec.Code)
	}
	if svc.calls() != 0 {
		t.Fatal("it went out before the yes")
	}
	entries, _ := c.store.accessLog(task.ID, 10)
	if len(entries) != 1 || entries[0].Decision != decideDeny {
		t.Errorf("log = %+v", entries)
	}
	if err := c.decideTaskStart(s.ID, true); err != nil {
		t.Fatal(err)
	}
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 200 {
		t.Errorf("after the yes: %d %s", rec.Code, rec.Body)
	}
}

// Free holds on a clean start; on any other, the card asks.
func TestAccessFreeAsksWhenNotClean(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree})
	if clean := lendAccess(t, c, true, npm); c.heldTask(clean.ID) {
		t.Fatal("a clean start was held")
	}
	if _, ok := startCard(t, c); ok {
		t.Fatal("a card for a clean start")
	}
	c.turn.taint("mail.read")
	if err := c.taintedResources(runRequest{Resources: []ResourceRef{npm}}); err != nil {
		t.Fatalf("a tainted turn may lend it, on a card: %v", err)
	}
	if unclean := lendAccess(t, c, false, npm); !c.heldTask(unclean.ID) {
		t.Fatal("not held")
	}
	if _, ok := startCard(t, c); !ok {
		t.Fatal("no card")
	}
}

// A watch's task was approved in advance.
func TestAccessApprovedInAdvance(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridApproval})
	task, err := c.runTask(context.Background(), runRequest{Prompt: "x", askStart: true, cleanStart: true, approvedInAdvance: true, Resources: []ResourceRef{npm}})
	if err != nil {
		t.Fatal(err)
	}
	c.grantLent(task)
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 200 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// Held back, it does nothing until the user's yes on its release.
func TestAccessHeldUntilReleased(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridApproval})
	held := npm
	held.Hold = true
	task := lendAccess(t, c, true, held)
	if c.heldTask(task.ID) {
		t.Fatal("a held access asks at its release, not at launch")
	}
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 403 || !strings.Contains(rec.Body.String(), "held back") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if svc.calls() != 0 {
		t.Fatal("it went out")
	}
	c.store.updateTask(task.ID, map[string]any{"state": stateRunning})
	if _, err := c.releaseTools(task.ID, []string{"npm"}); err != nil {
		t.Fatal(err)
	}
	ids := c.proposedAccess()
	if len(ids) != 1 {
		t.Fatalf("cards = %v", ids)
	}
	if err := c.decideAccess(context.Background(), ids[0], true); err != nil {
		t.Fatal(err)
	}
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 200 {
		t.Errorf("released: %d %s", rec.Code, rec.Body)
	}
}

func TestAccessRules(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree, Rules: []string{"GET /v1/*", "* /exact"}})
	task := lendAccess(t, c, true, npm)
	for _, ok := range []struct{ method, target string }{{"GET", "/v1/pkg"}, {"POST", "/exact"}} {
		if rec := viaRelay(c, "nas", task.ID, accessHost+":443", ok.method, ok.target, placeholderAuth(task.ID), nil); rec.Code != 200 {
			t.Errorf("%v: %d", ok, rec.Code)
		}
	}
	before := svc.calls()
	for _, no := range []struct{ method, target string }{
		{"POST", "/v1/pkg"}, {"GET", "/admin"}, {"GET", "/v1/../admin"}, {"GET", "/v1/%2e%2e/admin"}, {"GET", "/v1/..%5cadmin"}, {"GET", "/exact/more"},
		{"GET", "/v1/..;/admin"}, {"GET", "/v1/%252e%252e/admin"},
	} {
		if rec := viaRelay(c, "nas", task.ID, accessHost+":443", no.method, no.target, placeholderAuth(task.ID), nil); rec.Code != 403 {
			t.Errorf("%v: %d", no, rec.Code)
		}
	}
	if svc.calls() != before {
		t.Error("a refused request went out")
	}
}

// With rules, a GET stays a GET; a refusal's path stays on one line.
func TestAccessRulesKeepToTheirWord(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree, Rules: []string{"GET /v1/*"}})
	task := lendAccess(t, c, true, npm)
	rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/v1/x", placeholderAuth(task.ID), map[string]string{"X-HTTP-Method-Override": "DELETE"})
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if v := svc.last(t).Header.Get("X-Http-Method-Override"); v != "" {
		t.Errorf("override = %q", v)
	}
	viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/x%0aforged", placeholderAuth(task.ID), nil)
	entries, _ := c.store.accessLog(task.ID, 1)
	if len(entries) != 1 || entries[0].Decision != decideDeny || strings.Contains(entries[0].Reason, "\n") {
		t.Errorf("log = %+v", entries)
	}
}

// Basic reflected whole is its base64: it does not come back either.
func TestAccessScrubsBasic(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x:"+accessPlaceholder(task.ID, "npm")))
	rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", auth, nil)
	if got := rec.Header().Get("X-Echo"); got != auth {
		t.Errorf("X-Echo = %q", got)
	}
}

// The agent's own key must not take the placeholder's place.
func TestAccessCannotTakeTheAgentsVariable(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree, Env: "MY_KEY"})
	agentEnv := map[string]string{"MY_KEY": "the-agent-key"}
	if err := c.checkAgentEnv([]ResourceRef{npm}, agentEnv); err == nil {
		t.Error("accepted")
	}
	if err := c.checkAgentEnv([]ResourceRef{npm}, map[string]string{"OTHER": "x"}); err != nil {
		t.Error(err)
	}
}

// The vault page names an access's secret: it is not "unused".
func TestAccessSecretOnTheVaultPage(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree})
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, body, _ := tw.post("/settings/secrets", url.Values{"name": {"other"}, "value": {"x"}}, csrf)
	if code != 200 || !strings.Contains(body, "Identifiant npm") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAccessCheck(t *testing.T) {
	good := Access{Secret: "vault:x", Hosts: []string{"registry.npmjs.org", "r.example.org:8443"}, Env: "NPM_TOKEN"}
	if err := good.check("npm"); err != nil {
		t.Fatal(err)
	}
	edit := func(f func(*Access)) Access {
		a := good
		f(&a)
		return a
	}
	for name, a := range map[string]Access{
		"no secret":           edit(func(a *Access) { a.Secret = "" }),
		"no host":             edit(func(a *Access) { a.Hosts = nil }),
		"a URL":               edit(func(a *Access) { a.Hosts = []string{"https://example.org/x"} }),
		"a wildcard":          edit(func(a *Access) { a.Hosts = []string{"*.example.org"} }),
		"a port with a slash": edit(func(a *Access) { a.Hosts = []string{"example.org:8443/"} }),
		"a hex port":          edit(func(a *Access) { a.Hosts = []string{"example.org:0x1bb"} }),
		"NAT64":               edit(func(a *Access) { a.Hosts = []string{"[64:ff9b::a00:1]:443"} }),
		"git's":               edit(func(a *Access) { a.Env = "GIT_ASKPASS" }),
		"a hop":               edit(func(a *Access) { a.Header = "Proxy-Authorization" }),
		"the relay's":         edit(func(a *Access) { a.Header = "Eriphe-Foo" }),
		"a bad port":          edit(func(a *Access) { a.Hosts = []string{"example.org:99999"} }),
		"loopback":            edit(func(a *Access) { a.Hosts = []string{"127.0.0.1"} }),
		"the LAN":             edit(func(a *Access) { a.Hosts = []string{"192.168.1.10:8443"} }),
		"metadata":            edit(func(a *Access) { a.Hosts = []string{"169.254.169.254"} }),
		"no variable":         edit(func(a *Access) { a.Env = "" }),
		"lowercase":           edit(func(a *Access) { a.Env = "npm_token" }),
		"PATH":                edit(func(a *Access) { a.Env = "PATH" }),
		"a proxy":             edit(func(a *Access) { a.Env = "HTTPS_PROXY" }),
		"the task's":          edit(func(a *Access) { a.Env = "ERIPHE_TASK_SECRET" }),
		"a preload":           edit(func(a *Access) { a.Env = "LD_PRELOAD" }),
		"a value":             edit(func(a *Access) { a.Value = gridNever }),
		"a kind":              edit(func(a *Access) { a.Kind = "ssh" }),
		"a header":            edit(func(a *Access) { a.Header = "a b" }),
		"a rule":              edit(func(a *Access) { a.Rules = []string{"GET"} }),
		"a rule's path":       edit(func(a *Access) { a.Rules = []string{"GET v1"} }),
	} {
		if err := a.check("npm"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	gh := good
	gh.Env = "GH_TOKEN"
	if err := gh.check("gh"); err != nil {
		t.Errorf("GH_TOKEN: %v", err)
	}
	for _, a := range []string{"64:ff9b::a00:1", "2002:c0a8:1::1", "198.18.0.1", "240.0.0.1", "0.1.2.3", "10.0.0.1", "100.64.0.1", "fd00::1", "::1", "0.0.0.0", "224.0.0.1", "::ffff:192.168.0.1"} {
		if accessAddr(netip.MustParseAddr(a)) {
			t.Errorf("%s is not public", a)
		}
	}
	if !accessAddr(netip.MustParseAddr("140.82.121.4")) {
		t.Error("a public address refused")
	}
	if err := accessControl("tcp", "10.0.0.1:443", nil); err == nil {
		t.Error("a private address dialed")
	}
}

func TestAccessLending(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree})
	if err := c.putAccess(putCreate, "pypi", Access{Hosts: []string{"upload.pypi.org"}, Env: "NPM_TOKEN"}, "s"); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]runRequest{
		"unknown":  {Resources: []ResourceRef{{Kind: resAccess, Ref: "nope"}}},
		"a level":  {Resources: []ResourceRef{{Kind: resAccess, Ref: "npm", Access: accessWrite}}},
		"one var":  {Resources: []ResourceRef{npm, {Kind: resAccess, Ref: "pypi"}}},
		"a branch": {Resources: []ResourceRef{{Kind: resAccess, Ref: "npm", Branch: "x"}}},
	} {
		if _, err := c.resolveRequest(r); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The same one twice is lent once.
	lent, err := c.resolveRequest(runRequest{Resources: []ResourceRef{npm, npm}})
	if err != nil || len(lent.List) != 1 {
		t.Fatalf("lent = %+v, %v", lent, err)
	}
}

// A node gets the placeholder and the hosts, never the secret, and the task
// is told what it holds.
func TestAccessReachesTheNodeWithoutItsSecret(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	req := httptest.NewRequest("GET", "/v1/node/assignments?rev=0&wait=0", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Eriphe-Node", "nas")
	rec := httptest.NewRecorder()
	c.handleAssignments(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, accessSecret) {
		t.Fatal("the secret went to the node")
	}
	if !strings.Contains(body, accessPlaceholder(task.ID, "npm")) || !strings.Contains(body, accessHost+":443") || !strings.Contains(body, "NPM_TOKEN") {
		t.Fatalf("assignment = %s", body)
	}
	if !strings.Contains(body, "holds a placeholder") {
		t.Errorf("the task is not told: %s", body)
	}
}

// Its secret lives in the vault, survives a restart, stays when not typed
// again, and goes with the access.
func TestAccessInTheCatalog(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree, Use: "publish"})
	ref := "access.npm.secret"
	if got, err := vaultSecret(ref); err != nil || got != accessSecret {
		t.Fatalf("vault = %q, %v", got, err)
	}
	rows, _ := c.store.catalogRows()
	for _, r := range rows {
		if strings.Contains(string(r.Spec), accessSecret) {
			t.Fatalf("the secret is in the catalog: %s", r.Spec)
		}
	}
	d, skipped := loadCatalog(c.base, c.store)
	if len(skipped) != 0 || d.Accesses["npm"].Use != "publish" {
		t.Fatalf("reloaded = %+v, %v", d.Accesses, skipped)
	}
	if err := c.putAccess(putUpdate, "npm", Access{Use: "publish", Hosts: []string{accessHost}, Env: "NPM_TOKEN"}, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := vaultSecret(ref); got != accessSecret || c.conf().Accesses["npm"].value() != gridApproval {
		t.Fatalf("kept = %q, %+v", got, c.conf().Accesses["npm"])
	}
	if err := c.putAccess(putCreate, "nosecret", Access{Hosts: []string{accessHost}, Env: "X_TOKEN"}, ""); err == nil {
		t.Error("an access without a secret")
	}
	if err := c.putAccess(putCreate, "github", Access{Hosts: []string{accessHost}, Env: "X_TOKEN"}, "s"); err == nil {
		t.Error("an access named as a tool")
	}
	if err := c.deleteAccess("npm"); err != nil {
		t.Fatal(err)
	}
	if _, err := vaultSecret(ref); err == nil {
		t.Error("its secret stayed")
	}
	if got := c.listAccesses(); len(got) != 0 {
		t.Errorf("listed = %+v", got)
	}
}

// A lent access removed meanwhile lends nothing.
func TestAccessRemovedUnderATask(t *testing.T) {
	c, svc := accessCore(t, Access{Value: gridFree})
	task := lendAccess(t, c, true, npm)
	if err := c.deleteAccess("npm"); err != nil {
		t.Fatal(err)
	}
	if rec := viaRelay(c, "nas", task.ID, accessHost+":443", "GET", "/", placeholderAuth(task.ID), nil); rec.Code != 403 {
		t.Errorf("%d", rec.Code)
	}
	if svc.calls() != 0 {
		t.Error("it went out")
	}
}

func TestAccessListedForTheBrain(t *testing.T) {
	c, _ := accessCore(t, Access{Value: gridFree, Use: "publish"})
	got := c.listAccesses()
	if len(got) != 1 || got[0].Name != "npm" || got[0].Value != gridFree || got[0].Env != "NPM_TOKEN" {
		t.Fatalf("listed = %+v", got)
	}
	for _, v := range got {
		if strings.Contains(strings.Join(v.Hosts, " ")+v.Use+v.Env, accessSecret) {
			t.Error("the secret is listed")
		}
	}
}

func TestAccessPage(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	form := url.Values{"name": {"npm"}, "use": {"publish"}, "hosts": {accessHost + "\n\nupload.example.org:8443\n"}, "env": {"NPM_TOKEN"},
		"secret": {accessSecret}, "value": {gridFree}, "rules": {"GET /v1/*\n"}}
	if code, body, h := tw.post("/settings/credentials", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("add: %d %s", code, body)
	}
	a := c.conf().Accesses["npm"]
	if len(a.Hosts) != 2 || a.Value != gridFree || len(a.Rules) != 1 || a.Secret != "vault:access.npm.secret" {
		t.Fatalf("access = %+v", a)
	}
	if code, body, _ := tw.do("GET", "/settings/tools?savedaccess=npm", nil, nil); code != 200 || !strings.Contains(body, accessHost) ||
		strings.Contains(body, accessSecret) || !strings.Contains(html.UnescapeString(body), "Identifiant « npm » enregistré") {
		t.Fatalf("list: %d", code)
	}
	code, body, _ := tw.do("GET", "/settings/credentials/npm/edit", nil, nil)
	if code != 200 || !strings.Contains(body, "upload.example.org:8443") || strings.Contains(body, accessSecret) {
		t.Fatalf("edit: %d", code)
	}
	// Saved without its secret: it stays.
	form.Del("secret")
	form.Set("value", gridApproval)
	if code, body, h := tw.post("/settings/credentials/npm", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %d %s", code, body)
	}
	if got, _ := vaultSecret("access.npm.secret"); got != accessSecret || c.conf().Accesses["npm"].Value != gridApproval {
		t.Fatalf("after the edit: %q %+v", got, c.conf().Accesses["npm"])
	}
	// A refusal stays on the form.
	form.Set("env", "PATH")
	if _, body, h := tw.post("/settings/credentials/npm", form, csrf); h.Get("HX-Redirect") != "" || !strings.Contains(body, "lent-form") ||
		!strings.Contains(html.UnescapeString(body), "ne peut pas tenir le substitut") {
		t.Fatalf("refused: %s", body)
	}
	// Removed from its row, as a tool is.
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, `<form hx-post="/settings/credentials/npm/delete"`) {
		t.Fatal("no way to remove it from the list")
	}
	if code, body, h := tw.post("/settings/credentials/npm/delete", nil, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("delete: %d %s", code, body)
	}
	if _, ok := c.conf().Accesses["npm"]; ok {
		t.Fatal("still there")
	}
}

func TestAccessOfTheFileIsReadOnly(t *testing.T) {
	c := startCore(t)
	c.base.Accesses = map[string]Access{"npm": {Secret: "/nope", Hosts: []string{accessHost}, Env: "NPM_TOKEN"}}
	if err := c.reload(); err != nil {
		t.Fatal(err)
	}
	if err := c.putAccess(putUpsert, "npm", Access{Hosts: []string{accessHost}, Env: "NPM_TOKEN"}, "s"); err == nil {
		t.Error("changed")
	}
	if err := c.deleteAccess("npm"); err == nil {
		t.Error("deleted")
	}
	tw := newTestWebFor(t, c)
	tw.setup()
	if code, _, h := tw.do("GET", "/settings/credentials/npm/edit", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/settings/tools" {
		t.Errorf("edit: %d", code)
	}
}

// The credentials' former address leads to the new one, query and all.
func TestCredentialsMoved(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for from, to := range map[string]string{
		"/settings/accesses/new?kind=ssh": "/settings/credentials/new?kind=ssh",
		"/settings/accesses/npm/edit":     "/settings/credentials/npm/edit",
	} {
		if code, _, h := tw.do("GET", from, nil, nil); code != 301 || h.Get("Location") != to {
			t.Errorf("GET %s: %d %s", from, code, h.Get("Location"))
		}
	}
	// The tools page names them credentials, in both languages.
	for lang, want := range map[string]string{"fr": "<h3>Identifiants</h3>", "en": "<h3>Credentials</h3>"} {
		_, body, _ := tw.do("GET", "/settings/tools", nil, map[string]string{"Cookie": "lang=" + lang})
		if !strings.Contains(body, `id="credentials"`) || !strings.Contains(body, want) || !strings.Contains(body, `href="/settings/credentials/new"`) {
			t.Errorf("%s: the tools page does not name them credentials", lang)
		}
	}
}
