package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
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

var lentRepo = ResourceRef{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/fix"}

const pushBody = "0000PACKdata"

func TestReadGitCall(t *testing.T) {
	for raw, want := range map[string]gitCall{
		"GET /o/r.git/info/refs?service=git-upload-pack":  {repo: "o/r", access: accessRead},
		"GET /o/r/info/refs?service=git-upload-pack":      {repo: "o/r", access: accessRead},
		"GET /o/r.git/info/refs":                          {repo: "o/r", access: accessRead},
		"POST /o/r.git/git-upload-pack":                   {repo: "o/r", access: accessRead},
		"GET /o/r.git/info/refs?service=git-receive-pack": {repo: "o/r", access: accessWrite},
		"POST /o/r.git/git-receive-pack":                  {repo: "o/r", access: accessWrite},
		"POST /o/r.git/info/lfs/objects/batch":            {repo: "o/r", access: accessRead, lfs: true},
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

func TestGitReachesTheNodeWithoutAToken(t *testing.T) {
	got := lentGit("t-1", []ResourceRef{lentRepo})
	if len(got) != 1 || !slices.Equal(got[0].Hosts, []string{gitTarget, ghTarget}) || got[0].Placeholder != gitPlaceholder("t-1") || got[0].Env != "GH_TOKEN" {
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
	body := pushBody
	rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/git-receive-pack", gitAuth(task.ID), body, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if svc.body != body {
		t.Fatalf("GitHub got %q", svc.body)
	}
}

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
		rec := viaRelayBody(c, "nas", task.ID, tc[0], tc[1], auth, pushBody, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", tc[1], rec.Code, rec.Body)
		}
	}
	if svc.calls() != calls {
		t.Fatal("it reached GitHub")
	}
	// Lent for read, no yes makes it a write: the lend is what is decided.
	c.store.grantCredential(task.ID, "owner/repo", accessWrite)
	if rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/git-receive-pack", auth, pushBody, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("lent for read: %d %s", rec.Code, rec.Body)
	}
}

// A write lent on a start that is not the user's own waits for their yes on
// the card; once lent, it goes, whatever the task reads next.
func TestGitWriteIsDecidedAtLaunch(t *testing.T) {
	c, task, _ := gitCore(t, false, lentRepo)
	auth := gitAuth(task.ID)
	const push = "/owner/repo.git/git-receive-pack"
	if rec := viaRelayBody(c, "nas", task.ID, "POST", push, auth, pushBody, nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "user's approval") {
		t.Fatalf("not approved: %d %s", rec.Code, rec.Body)
	}
	c.store.grantCredential(task.ID, "owner/repo", accessWrite)
	if rec := viaRelayBody(c, "nas", task.ID, "POST", push, auth, pushBody, nil); rec.Code != 200 {
		t.Fatalf("approved: %d %s", rec.Code, rec.Body)
	}
	c.store.addEvents(task.ID, []EventRow{{Seq: 1, Type: "tool_use", Data: map[string]any{"name": "WebFetch", "input": map[string]any{"url": "https://example.org"}}}})
	if rec := viaRelayBody(c, "nas", task.ID, "POST", push, auth, pushBody, nil); rec.Code != 200 {
		t.Fatalf("after reading a page: %d %s", rec.Code, rec.Body)
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

	// What is pushed is not read: GitHub's protection rules are the limit.
	run("push", "--quiet", "origin", "eriphe/fix:other")
	if refs := s.refs(t); !strings.Contains(refs, "refs/heads/other") {
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

func TestGitLFSWritesWhereTheTaskMayWrite(t *testing.T) {
	c, task, _ := gitCore(t, true, lentRepo)
	rec := viaRelayBody(c, "nas", task.ID, "POST", "/owner/repo.git/info/lfs/objects/batch", gitAuth(task.ID), "{}", nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) == 0 || log[0].Access != accessWrite {
		t.Fatalf("log %+v", log)
	}
}

func TestGitBranchIsToldToTheTask(t *testing.T) {
	c, _, _ := gitCore(t, true, lentRepo)
	if text := c.accessInstructions([]ResourceRef{lentRepo}); !strings.Contains(text, "the branch eriphe/fix") {
		t.Fatalf("instructions: %q", text)
	}
}

// viaRelayGh sends what the relay sends for a request of gh.
func viaRelayGh(c *core, taskID, method, target string) *httptest.ResponseRecorder {
	return viaRelayBody(c, "nas", taskID, method, target, "token "+gitPlaceholder(taskID), "{}", map[string]string{accessHostHeader: ghTarget})
}

// gh holds the placeholder and its token goes in on the way out, on /graphql
// too, which is not classified: the task never sees the token.
func TestGhTokenGoesInOnTheWayOut(t *testing.T) {
	c := startCore(t)
	f := newFakeGitHub(t)
	withApp(t, c, f)
	task := lendAccess(t, c, true, lentRepo)
	svc := startService(t)
	rec := viaRelayGh(c, task.ID, "POST", "/graphql")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := svc.last(t)
	if got.Header.Get("Authorization") != "token ghs_x" || got.Host != "api.github.com" {
		t.Fatalf("Authorization %q, Host %q", got.Header.Get("Authorization"), got.Host)
	}
	if echo := rec.Header().Get("X-Echo"); strings.Contains(echo, "ghs_x") {
		t.Fatalf("the answer holds %q", echo)
	}
	if perms, _ := json.Marshal(f.mints()[0]["permissions"]); string(perms) != `{"checks":"read","contents":"read","issues":"write","pull_requests":"write"}` {
		t.Fatalf("permissions: %s", perms)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) == 0 || log[0].Tool != ghLogTool || log[0].Repo != "owner/repo" || log[0].Access != accessWrite || log[0].Decision != decideAllow {
		t.Fatalf("log %+v", log)
	}
}

// A repository not lent is refused when the request names it, and nothing
// goes out; a request with no placeholder goes as it is, with no token.
func TestGhStaysOnItsRepository(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	if rec := viaRelayGh(c, task.ID, "GET", "/repos/owner/elsewhere/pulls"); rec.Code != http.StatusForbidden {
		t.Fatalf("another repository: %d %s", rec.Code, rec.Body)
	}
	if n := svc.calls(); n != 0 {
		t.Fatalf("%d requests went out", n)
	}
	rec := viaRelayBody(c, "nas", task.ID, "GET", "/repos/owner/repo", "", "", map[string]string{accessHostHeader: ghTarget})
	if rec.Code != 200 || svc.last(t).Header.Get("Authorization") != "" {
		t.Fatalf("no placeholder: %d %q", rec.Code, svc.last(t).Header.Get("Authorization"))
	}
}

// Without the App, only the tool's own token could go: gh is told why, and
// nothing leaves.
func TestGhNeedsTheApp(t *testing.T) {
	c, task, svc := gitCore(t, true, lentRepo)
	if err := currentVault().remove(githubAppVault); err != nil {
		t.Fatal(err)
	}
	rec := viaRelayGh(c, task.ID, "GET", "/repos/owner/repo")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "GitHub App") || svc.calls() != 0 {
		t.Fatalf("%d %s, %d calls", rec.Code, rec.Body, svc.calls())
	}
}

func TestGhRepoOf(t *testing.T) {
	for path, want := range map[string]string{
		"/repos/o/r":          "o/r",
		"/repos/o/r/pulls/12": "o/r",
		"/graphql":            "",
		"/user":               "",
		"/repos/o":            "",
	} {
		if got := ghRepoOf(path); got != want {
			t.Errorf("%s: %q", path, got)
		}
	}
}

// A token that writes holds only what may be written; one owner per token.
