package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	oidA    = "1111111111111111111111111111111111111111"
	oidB    = "2222222222222222222222222222222222222222"
	oidZero = "0000000000000000000000000000000000000000"
)

var lentRepo = ResourceRef{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/fix"}

func pushBody(refs ...string) string {
	var b strings.Builder
	for i, ref := range refs {
		line := oidA + " " + oidB + " " + ref
		if i == 0 {
			line += "\x00report-status side-band-64k agent=git/2"
		}
		b.Write(pktLine([]byte(line + "\n")))
	}
	return b.String() + "0000PACKdata"
}

func TestReadPushCommands(t *testing.T) {
	sha256 := strings.Repeat("a", 64)
	for name, tc := range map[string]struct {
		body string
		refs []string
		bad  bool
	}{
		"one":         {body: pushBody("refs/heads/x"), refs: []string{"refs/heads/x"}},
		"several":     {body: pushBody("refs/heads/x", "refs/tags/v1"), refs: []string{"refs/heads/x", "refs/tags/v1"}},
		"probe":       {body: "0000"},
		"empty":       {body: ""},
		"sha256":      {body: string(pktLine([]byte(sha256+" "+sha256+" refs/heads/x\n"))) + "0000", refs: []string{"refs/heads/x"}},
		"shallow":     {body: string(pktLine([]byte("shallow "+oidA+"\n"))) + pushBody("refs/heads/x"), refs: []string{"refs/heads/x"}},
		"no flush":    {body: string(pktLine([]byte(oidA + " " + oidB + " refs/heads/x\n"))), bad: true},
		"cut":         {body: "00ff" + oidA, bad: true},
		"cut length":  {body: "00", bad: true},
		"not hex":     {body: "zzzz", bad: true},
		"signed sign": {body: "+0ff", bad: true},
		"delimiter":   {body: "0001", bad: true},
		"response":    {body: "0002", bad: true},
		"empty line":  {body: "0004", bad: true},
		"too long":    {body: "fff5", bad: true},
		"signed":      {body: string(pktLine([]byte("push-cert\x00report-status\n"))) + "0000", bad: true},
		"other":       {body: string(pktLine([]byte("hello there friend\n"))) + "0000", bad: true},
		"one id":      {body: string(pktLine([]byte(oidA+" refs/heads/x\n"))) + "0000", bad: true},
		"two sizes":   {body: string(pktLine([]byte(oidA+" "+sha256+" refs/heads/x\n"))) + "0000", bad: true},
		"no ref":      {body: string(pktLine([]byte(oidA+" "+oidB+" \n"))) + "0000", bad: true},
		"upper":       {body: string(pktLine([]byte(strings.ToUpper(sha256)+" "+sha256+" x\n"))) + "0000", bad: true},
		"endless":     {body: strings.Repeat(string(pktLine([]byte(oidA+" "+oidB+" refs/heads/x\n"))), 30000), bad: true},
	} {
		cmds, _, err := readPushCommands(strings.NewReader(tc.body))
		if (err != nil) != tc.bad {
			t.Errorf("%s: %v", name, err)
		}
		var refs []string
		for _, c := range cmds {
			refs = append(refs, c.Ref)
		}
		if !slices.Equal(refs, tc.refs) {
			t.Errorf("%s: refs %v", name, refs)
		}
	}
	// A NUL on a later line hides what follows it, at GitHub as here.
	cmds, _, err := readPushCommands(strings.NewReader(string(pktLine([]byte(oidA+" "+oidB+" refs/heads/a\n"))) +
		string(pktLine([]byte(oidA+" "+oidB+" refs/heads/b\x00refs/heads/c\n"))) + "0000"))
	if err != nil || len(cmds) != 2 || cmds[1].Ref != "refs/heads/b" {
		t.Fatalf("%v %v", cmds, err)
	}
}

func TestPushRefusal(t *testing.T) {
	branch := []repoGrant{{Repo: "owner/repo", Access: accessWrite, Branch: "eriphe/fix"}}
	two := append(branch, repoGrant{Repo: "Owner/Repo", Access: accessWrite, Branch: "eriphe/docs"})
	open := []repoGrant{{Repo: "owner/repo", Access: accessWrite}}
	cmd := func(ref string) pushCommand { return pushCommand{oidA, oidB, ref} }
	remove := func(ref string) pushCommand { return pushCommand{oidA, oidZero, ref} }
	for name, tc := range map[string]struct {
		repos []repoGrant
		cmds  []pushCommand
		says  string
	}{
		"its branch":       {branch, []pushCommand{cmd("refs/heads/eriphe/fix")}, ""},
		"nothing":          {branch, nil, ""},
		"another":          {branch, []pushCommand{cmd("refs/heads/other")}, "refs/heads/other"},
		"one of two":       {branch, []pushCommand{cmd("refs/heads/eriphe/fix"), cmd("refs/heads/other")}, "refs/heads/other"},
		"a tag":            {branch, []pushCommand{cmd("refs/tags/v1")}, "refs/tags/v1"},
		"a like name":      {branch, []pushCommand{cmd("refs/heads/eriphe/fix2")}, "fix2"},
		"not under heads":  {branch, []pushCommand{cmd("eriphe/fix")}, "eriphe/fix"},
		"deleted":          {branch, []pushCommand{remove("refs/heads/eriphe/fix")}, "deletes"},
		"main":             {branch, []pushCommand{cmd("refs/heads/main")}, "main"},
		"both loans":       {two, []pushCommand{cmd("refs/heads/eriphe/fix"), cmd("refs/heads/eriphe/docs")}, ""},
		"no branch":        {open, []pushCommand{cmd("refs/heads/other"), cmd("refs/tags/v1")}, ""},
		"no branch, main":  {open, []pushCommand{cmd("refs/heads/main")}, "main"},
		"master":           {open, []pushCommand{cmd("refs/heads/master")}, "master"},
		"MAIN":             {open, []pushCommand{cmd("refs/heads/MAIN")}, "MAIN"},
		"delete main":      {open, []pushCommand{remove("refs/heads/main")}, "main"},
		"not lent":         {nil, []pushCommand{cmd("refs/heads/other")}, ""},
		"not lent, main":   {nil, []pushCommand{cmd("refs/heads/main")}, "main"},
		"another's branch": {[]repoGrant{{Repo: "owner/other", Branch: "x"}}, []pushCommand{cmd("refs/heads/y")}, ""},
	} {
		got := pushRefusal(tc.cmds, tc.repos, "owner/repo")
		if tc.says == "" && got != "" || !strings.Contains(got, tc.says) {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestReadGitCall(t *testing.T) {
	for raw, want := range map[string]gitCall{
		"GET /o/r.git/info/refs?service=git-upload-pack":  {repo: "o/r", access: accessRead},
		"GET /o/r/info/refs?service=git-upload-pack":      {repo: "o/r", access: accessRead},
		"GET /o/r.git/info/refs":                          {repo: "o/r", access: accessRead},
		"POST /o/r.git/git-upload-pack":                   {repo: "o/r", access: accessRead},
		"GET /o/r.git/info/refs?service=git-receive-pack": {repo: "o/r", access: accessWrite},
		"POST /o/r.git/git-receive-pack":                  {repo: "o/r", access: accessWrite, push: true},
		"POST /o/r.git/info/lfs/objects/batch":            {repo: "o/r", access: accessRead},
		"GET /o/r/archive/main.zip":                       {},
		"GET /login":                                      {},
		"PUT /o/r.git/git-receive-pack":                   {},
	} {
		method, target, _ := strings.Cut(raw, " ")
		uri, err := url.ParseRequestURI(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := readGitCall(method, uri); got != want {
			t.Errorf("%s: %+v", raw, got)
		}
	}
}

// gitCore is a core with the GitHub App, and a task lent the repository.
func gitCore(t *testing.T, clean bool, e ResourceRef) (*core, *TaskRow, *fakeService) {
	t.Helper()
	c := startCore(t)
	f := newFakeGitHub(t)
	withApp(t, c, f)
	task := lendAccess(t, c, clean, e)
	return c, task, startService(t)
}

func gitAuth(taskID string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+gitPlaceholder(taskID)))
}

func viaRelayBody(c *core, node, taskID, method, target, auth, body string, more map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("/v1/node/tasks/{id}/access", c.taskAccessHandler())
	req := httptest.NewRequest(method, "/v1/node/tasks/"+taskID+"/access", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Eriphe-Node", node)
	req.Header.Set(accessHostHeader, gitTarget)
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

// The helper gets a placeholder where the relay decrypts github.com, and
// nothing a task can name changes that.
func TestGitGetsAPlaceholder(t *testing.T) {
	c, task, _ := gitCore(t, true, lentRepo)
	ctx := context.Background()
	relayed := []string{gitTarget}
	for _, host := range []string{"github.com", "www.github.com", "api.github.com", "github.com:443"} {
		for _, tool := range []string{"git", "", "anything"} {
			a := c.credential(ctx, task, credRequest{Protocol: "https", Host: host, Path: "owner/repo.git", Access: accessWrite, Tool: tool, Relayed: relayed})
			if a.Decision != decideAllow || a.Password != gitPlaceholder(task.ID) || !a.Expires.IsZero() {
				t.Fatalf("%s %s: %+v", host, tool, a)
			}
		}
	}
	// A task from before, or on an older node: its token, or it stops working.
	if a := c.credential(ctx, task, credRequest{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: accessRead, Tool: "git"}); !strings.HasPrefix(a.Password, "ghs_") {
		t.Fatalf("not relayed: %+v", a)
	}
	// gh keeps its token, with no right to the contents.
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: accessWrite, Tool: "gh", Relayed: relayed}); !strings.HasPrefix(a.Password, "ghs_") {
		t.Fatalf("gh: %+v", a)
	}
	// A refusal stays one.
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "other/repo.git", Access: accessRead, Tool: "git", Relayed: relayed}); a.Decision != decideDeny || a.Password != "" {
		t.Fatalf("other: %+v", a)
	}
}

func TestGitReachesTheNodeWithoutAToken(t *testing.T) {
	got := lentGit("t-1", []ResourceRef{lentRepo})
	if len(got) != 1 || !slices.Equal(got[0].Hosts, []string{gitTarget}) || got[0].Placeholder != gitPlaceholder("t-1") || got[0].Env == "" {
		t.Fatalf("%+v", got)
	}
	if got := lentGit("t-1", []ResourceRef{{Kind: resRepo, Ref: "https://gitlab.example.org/o/r"}, npm}); got != nil {
		t.Fatalf("not GitHub: %+v", got)
	}
}

func TestGitTokenGoesInOnTheWayOut(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	rec := viaRelayBody(c, "nas", task.ID, "GET", "/owner/repo.git/info/refs?service=git-upload-pack", gitAuth(task.ID), "", nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := svc.last(t)
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_x")); got.Header.Get("Authorization") != want {
		t.Errorf("Authorization = %q", got.Header.Get("Authorization"))
	}
	if got.Host != "github.com" {
		t.Errorf("Host = %q", got.Host)
	}
	if echo := rec.Header().Get("X-Echo"); strings.Contains(echo, base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_x"))) || echo != gitAuth(task.ID) {
		t.Errorf("the answer holds %q", echo)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) == 0 || log[0].Tool != "git" || log[0].Repo != "owner/repo" || log[0].Decision != decideAllow || log[0].Source != sourceApp {
		t.Fatalf("log %+v", log)
	}
}

func TestGitPushOfItsBranchGoesOnWhole(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	body := pushBody("refs/heads/eriphe/fix")
	rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/git-receive-pack", gitAuth(task.ID), body, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if svc.body != body {
		t.Fatalf("GitHub got %q", svc.body)
	}
}

func TestGitPushElsewhereIsRefused(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	for _, auth := range []string{gitAuth(task.ID), "Basic " + base64.StdEncoding.EncodeToString([]byte("x:a-token-of-gh")), ""} {
		for _, ref := range []string{"refs/heads/other", "refs/heads/main", "refs/tags/v1"} {
			rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/git-receive-pack", auth, pushBody(ref), nil)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ng "+ref) {
				t.Fatalf("%s: %d %q", ref, rec.Code, rec.Body)
			}
			if rec.Header().Get("Content-Type") != "application/x-git-receive-pack-result" {
				t.Fatalf("%s: %v", ref, rec.Header())
			}
		}
	}
	if svc.calls() != 0 {
		t.Fatal("a push reached GitHub")
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) == 0 || log[0].Decision != decideDeny || log[0].Repo != "owner/repo" || !strings.Contains(log[0].Reason, "refs/tags/v1") {
		t.Fatalf("log %+v", log)
	}
}

func TestGitPushThatCannotBeReadIsRefused(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	push := "/owner/repo.git/git-receive-pack"
	for name, tc := range map[string]struct {
		method, target, body string
		more                 map[string]string
	}{
		"compressed":  {"POST", push, pushBody("refs/heads/eriphe/fix"), map[string]string{"Content-Encoding": "gzip"}},
		"signed":      {"POST", push, string(pktLine([]byte("push-cert\x00report-status\n"))) + "0000", nil},
		"cut":         {"POST", push, "00", nil},
		"encoded":     {"POST", "/owner/repo.git/git-receive%252Dpack", pushBody("refs/heads/main"), nil},
		"slashes":     {"POST", "/owner/repo.git//git-receive-pack", pushBody("refs/heads/main"), nil},
		"trailing":    {"POST", push + "/", pushBody("refs/heads/main"), nil},
		"climbing":    {"POST", "/owner/repo.git/x/../git-receive-pack", pushBody("refs/heads/main"), nil},
		"upper":       {"POST", "/owner/repo.git/GIT-RECEIVE-PACK", pushBody("refs/heads/main"), nil},
		"other verb":  {"PUT", push, pushBody("refs/heads/main"), nil},
		"no repo":     {"POST", "/owner/re!po/git-receive-pack", pushBody("refs/heads/main"), nil},
		"nested repo": {"POST", "/a/b/c/git-receive-pack", pushBody("refs/heads/main"), nil},
	} {
		rec := viaRelayBody(c, "nas", task.ID, tc.method, tc.target, gitAuth(task.ID), tc.body, tc.more)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	if svc.calls() != 0 {
		t.Fatal("a push reached GitHub")
	}
	// Decoded once, it says what it is, and is read.
	rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/git%2Dreceive%2Dpack", gitAuth(task.ID), pushBody("refs/heads/main"), nil)
	if !strings.Contains(rec.Body.String(), "ng refs/heads/main") || svc.calls() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

// A placeholder proves nothing: each request is decided for what it is.
func TestGitDecidesEachRequest(t *testing.T) {
	c, task, svc := gitCore(t, false, ResourceRef{Kind: resRepo, Ref: "owner/repo", Access: accessRead})
	auth := gitAuth(task.ID)
	if rec := viaRelayBody(c, "nas", task.ID, "GET", "/owner/repo.git/info/refs?service=git-upload-pack", auth, "", nil); rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	calls := svc.calls()
	for _, tc := range [][2]string{
		{"GET", "/owner/repo.git/info/refs?service=git-receive-pack"},
		{"POST", "/owner/repo.git/git-receive-pack"},
		{"GET", "/other/repo.git/info/refs?service=git-upload-pack"},
	} {
		rec := viaRelayBody(c, "nas", task.ID, tc[0], tc[1], auth, pushBody("refs/heads/x"), nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", tc[1], rec.Code, rec.Body)
		}
	}
	if svc.calls() != calls {
		t.Fatal("it reached GitHub")
	}
	// Approved by the user, the same push goes.
	c.store.grantCredential(task.ID, "github", "owner/repo", accessWrite)
	if rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/git-receive-pack", auth, pushBody("refs/heads/x"), nil); rec.Code != 200 {
		t.Fatalf("approved: %d %s", rec.Code, rec.Body)
	}
}

// Elsewhere on github.com, the placeholder is of no use, and a request that
// carries none goes as it is.
func TestGitOtherRequestsGoAsTheyAre(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	auth := gitAuth(task.ID)
	if rec := viaRelayBody(c, "nas", task.ID, "GET", "/owner/repo/archive/main.zip", auth, "", nil); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if v := svc.last(t).Header.Get("Authorization"); v != auth {
		t.Fatalf("Authorization = %q", v)
	}
	if rec := viaRelayBody(c, "nas", task.ID, "GET", "/public/tool.git/info/refs?service=git-upload-pack", "", "", nil); rec.Code != 200 {
		t.Fatalf("a public read: %d %s", rec.Code, rec.Body)
	}
	if v := svc.last(t).Header.Get("Authorization"); v != "" {
		t.Fatalf("Authorization = %q", v)
	}
}

// Another task's placeholder, and github.com for a task with no repository,
// get nothing.
func TestGitStaysWithItsTask(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	rec := viaRelayBody(c, "nas", task.ID, "GET", "/owner/repo.git/info/refs", gitAuth("t-other"), "", nil)
	if rec.Code != 200 || svc.last(t).Header.Get("Authorization") != gitAuth("t-other") {
		t.Fatalf("%d %q", rec.Code, svc.last(t).Header.Get("Authorization"))
	}
	if err := c.putAccess(putCreate, "npm", Access{Hosts: []string{accessHost}, Env: "NPM_TOKEN", Value: gridFree}, accessSecret); err != nil {
		t.Fatal(err)
	}
	other := lendAccess(t, c, true, npm)
	if rec := viaRelayBody(c, "nas", other.ID, "GET", "/owner/repo.git/info/refs", gitAuth(other.ID), "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no repository: %d %s", rec.Code, rec.Body)
	}
}

// gitServer is GitHub for real git: the relay and eriphe-core in front of
// git's own http-backend, on a bare repository.
type gitServer struct {
	url, bare string
	mu        sync.Mutex
	auth      []string
}

func startGitServer(t *testing.T, c *core, taskID string) *gitServer {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	s := &gitServer{bare: filepath.Join(root, "owner", "repo.git")}
	if out, err := exec.Command(gitBin, "init", "--quiet", "--bare", "--initial-branch=main", s.bare).CombinedOutput(); err != nil {
		t.Skipf("git init: %s", out)
	}
	exec.Command(gitBin, "-C", s.bare, "config", "http.receivepack", "true").Run()
	backend := &cgi.Handler{Path: gitBin, Args: []string{"http-backend"}, Root: "/",
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=sam"}}
	github := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		// The CGI wants a length, which a chunked push does not have.
		body, _ := io.ReadAll(r.Body)
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		r.TransferEncoding = nil
		r.Header.Del("Authorization")
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(github.Close)
	old := accessTransport
	accessTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, github.Listener.Addr().String())
		},
	}
	t.Cleanup(func() { accessTransport = old })

	mux := http.NewServeMux()
	mux.Handle("/v1/node/tasks/{id}/access", c.taskAccessHandler())
	// What the node's relay does to a request, node/egress_access.go.
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// git sends nothing until it is told to authenticate.
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.Header.Set(accessAuthHeader, r.Header.Get("Authorization"))
		r.Header.Set(accessHostHeader, gitTarget)
		r.Header.Set(accessTargetHeader, r.URL.RequestURI())
		r.Header.Set("Authorization", "Bearer x")
		r.Header.Set("Eriphe-Node", "nas")
		r.URL.Path, r.URL.RawQuery = "/v1/node/tasks/"+taskID+"/access", ""
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(relay.Close)
	s.url = relay.URL + "/owner/repo.git"
	return s
}

// git runs git the way a task's does: its helper hands the placeholder.
func (s *gitServer) git(t *testing.T, dir, taskID string, args ...string) (string, error) {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "helper")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = get ] && printf 'username=x-access-token\\npassword=%s\\n'\nexit 0\n", gitPlaceholder(taskID))
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	all := append([]string{"-c", "credential.helper=" + helper, "-c", "user.name=Sam", "-c", "user.email=sam@example.org",
		"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", all...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *gitServer) refs(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", s.bare, "for-each-ref", "--format=%(refname)").CombinedOutput()
	return string(out)
}

// A client that asked for no side band, or no report at all, is answered in
// its own terms.
func TestRejectPushForms(t *testing.T) {
	cmds := []pushCommand{{oidA, oidB, "refs/heads/x"}}
	plain := httptest.NewRecorder()
	rejectPush(plain, cmds, []string{"report-status"}, "no")
	if want := "000eunpack ok\n0017ng refs/heads/x no\n0000"; plain.Body.String() != want {
		t.Fatalf("%q", plain.Body)
	}
	banded := httptest.NewRecorder()
	rejectPush(banded, cmds, []string{"report-status-v2", "side-band"}, "no")
	if !strings.HasSuffix(banded.Body.String(), "00000000") || banded.Body.String()[4] != 1 {
		t.Fatalf("%q", banded.Body)
	}
	none := httptest.NewRecorder()
	rejectPush(none, cmds, nil, "no")
	if none.Code != http.StatusForbidden {
		t.Fatalf("%d", none.Code)
	}
}

// Real git, end to end but for the relay's TLS: it clones and pushes its
// branch with a placeholder, and what else it pushes, by whatever command,
// stays out, with git saying why.
func TestGitWithARealClient(t *testing.T) {
	c, task, _ := gitCore(t, true, lentRepo)
	s := startGitServer(t, c, task.ID)
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	run := func(args ...string) string {
		t.Helper()
		out, err := s.git(t, repo, task.ID, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return out
	}
	if out, err := s.git(t, work, task.ID, "clone", "--quiet", s.url, "repo"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	run("checkout", "--quiet", "-b", "eriphe/fix")
	os.WriteFile(filepath.Join(repo, "a"), []byte("one"), 0o644)
	run("add", "a")
	run("commit", "--quiet", "-m", "one")
	run("push", "--quiet", "origin", "eriphe/fix")
	if refs := s.refs(t); !strings.Contains(refs, "refs/heads/eriphe/fix") {
		t.Fatalf("refs: %s", refs)
	}
	// Above http.postBuffer, a push is chunked.
	big := make([]byte, 200<<10)
	for i := range big {
		big[i] = byte(i * 7 >> 3)
	}
	os.WriteFile(filepath.Join(repo, "big"), big, 0o644)
	run("add", "big")
	run("commit", "--quiet", "-m", "big")
	run("-c", "http.postBuffer=1024", "push", "--quiet", "origin", "eriphe/fix")

	for name, args := range map[string][]string{
		"another":        {"push", "origin", "eriphe/fix:other"},
		"main":           {"push", "origin", "eriphe/fix:main"},
		"tag":            {"push", "origin", "eriphe/fix:refs/tags/v1"},
		"delete":         {"push", "origin", ":eriphe/fix"},
		"chunked":        {"-c", "http.postBuffer=1024", "push", "origin", "HEAD:other"},
		"along with":     {"push", "origin", "eriphe/fix", "eriphe/fix:other"},
		"through a URL":  {"push", s.url, "HEAD:refs/heads/other"},
		"by its plumber": {"send-pack", "--remote=origin", s.url, "HEAD:refs/heads/other"},
	} {
		out, err := s.git(t, repo, task.ID, args...)
		if err == nil {
			t.Errorf("%s: it went\n%s", name, out)
		}
		if name != "by its plumber" && !strings.Contains(out, "remote rejected") {
			t.Errorf("%s: git does not say why:\n%s", name, out)
		}
	}
	refs := s.refs(t)
	if strings.Contains(refs, "other") || strings.Contains(refs, "main") || strings.Contains(refs, "v1") || !strings.Contains(refs, "eriphe/fix") {
		t.Fatalf("refs: %s", refs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.auth {
		if a == "" {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "Basic "))
		if !strings.HasPrefix(string(raw), "x-access-token:ghs_") {
			t.Fatalf("GitHub got %q", raw)
		}
	}
}
