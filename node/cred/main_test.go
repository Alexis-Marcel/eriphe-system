package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeNode answers as a node's task listener: it checks the task's secret
// and records what it was asked.
type fakeNode struct {
	got    []request
	status int
	answer answer
}

func (f *fakeNode) serve(t *testing.T) *client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/task/credential" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer task-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req request
		json.NewDecoder(r.Body).Decode(&req)
		f.got = append(f.got, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		json.NewEncoder(w).Encode(f.answer)
	}))
	t.Cleanup(srv.Close)
	return &client{url: srv.URL + "/v1/task/credential", secret: "task-secret", http: srv.Client()}
}

func gitGet(t *testing.T, c *client, access, in string) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := runGit("get", strings.NewReader(in), &out, &errOut, c, func() string { return access }); code != 0 {
		t.Fatalf("exit %d", code)
	}
	return out.String(), errOut.String()
}

func TestGitGetGivesTheToken(t *testing.T) {
	exp := time.Unix(1893456000, 0)
	f := &fakeNode{status: 200, answer: answer{Username: "x-access-token", Password: "ghs_short", Expires: exp}}
	c := f.serve(t)
	out, _ := gitGet(t, c, "write", "protocol=https\nhost=github.com\npath=owner/repo.git\nwwwauth[]=Basic realm=\"GitHub\"\n\n")
	want := "username=x-access-token\npassword=ghs_short\npassword_expiry_utc=1893456000\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
	if len(f.got) != 1 || f.got[0] != (request{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: "write", Tool: "git"}) {
		t.Fatalf("asked %+v", f.got)
	}
}

func TestGitGetRefusedStopsGit(t *testing.T) {
	f := &fakeNode{status: 403, answer: answer{Reason: "the GitHub service is not lent to this task"}}
	c := f.serve(t)
	out, errOut := gitGet(t, c, "read", "protocol=https\nhost=github.com\npath=other/repo.git\n")
	if out != "quit=1\n" {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(errOut, "not lent") || !strings.Contains(errOut, "other/repo") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestGitGetNoPathSaysUseHttpPath(t *testing.T) {
	f := &fakeNode{status: 403, answer: answer{Reason: "which repository?"}}
	_, errOut := gitGet(t, f.serve(t), "read", "protocol=https\nhost=github.com\n")
	if !strings.Contains(errOut, "useHttpPath") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestGitGetOtherHostIsLeftToOthers(t *testing.T) {
	f := &fakeNode{status: 404, answer: answer{Reason: "no credential service for gitlab.com"}}
	c := f.serve(t)
	if out, _ := gitGet(t, c, "read", "protocol=https\nhost=gitlab.com\npath=a/b.git\n"); out != "" {
		t.Fatalf("stdout = %q: another helper must be able to answer", out)
	}
	// Not https: never asked.
	if out, _ := gitGet(t, c, "read", "protocol=http\nhost=github.com\npath=a/b.git\n"); out != "" || len(f.got) != 1 {
		t.Fatalf("http: %q, asked %d times", out, len(f.got))
	}
}

func TestGitStoreAndEraseKeepNothing(t *testing.T) {
	f := &fakeNode{status: 200, answer: answer{Password: "x"}}
	c := f.serve(t)
	for _, op := range []string{"store", "erase"} {
		var out bytes.Buffer
		runGit(op, strings.NewReader("protocol=https\nhost=github.com\nusername=x\npassword=y\n\n"), &out, &out, c, func() string { return "read" })
		if out.Len() != 0 {
			t.Fatalf("%s wrote %q", op, out.String())
		}
	}
	if len(f.got) != 0 {
		t.Fatalf("store/erase asked the node: %+v", f.got)
	}
}

func TestGitWithoutEriphe(t *testing.T) {
	var out bytes.Buffer
	runGit("get", strings.NewReader("protocol=https\nhost=github.com\npath=a/b\n"), &out, &out, &client{}, func() string { return "read" })
	if out.Len() != 0 {
		t.Fatalf("unconfigured helper answered %q", out.String())
	}
}

func TestAccessOfGit(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"git", "push", "origin", "feature"}, "write"},
		{[]string{"/usr/bin/git", "-C", "/work/repo", "-c", "x=y", "push"}, "write"},
		{[]string{"git", "fetch", "origin"}, "read"},
		{[]string{"git", "clone", "https://github.com/o/r"}, "read"},
		{[]string{"git", "--no-pager", "pull"}, "read"},
		{[]string{"git-remote-https", "origin", "https://github.com/o/r"}, ""},
		{[]string{"git", "remote-https", "https://github.com/o/r.git", "https://github.com/o/r.git"}, ""},
		{[]string{"git-send-pack", "--stateless-rpc"}, "write"},
		{[]string{"bash", "-c", "git push"}, ""},
	} {
		if got := accessOfGit(c.args); got != c.want {
			t.Errorf("%v: %q, want %q", c.args, got, c.want)
		}
	}
}

func TestGitAccessWalksUp(t *testing.T) {
	old := ancestors
	t.Cleanup(func() { ancestors = old })
	ancestors = func() [][]string {
		// As git runs it on Linux: a shell, the remote helper twice, then git push.
		return [][]string{{"/bin/sh", "-c", "/opt/eriphe/bin/eriphe-cred get"}, {"git-remote-https", "origin", "https://github.com/o/r"},
			{"git", "remote-https", "origin", "https://github.com/o/r"}, {"git", "push"}, {"bash"}}
	}
	if got := gitAccess(); got != "write" {
		t.Fatalf("push: %q", got)
	}
	ancestors = func() [][]string { return nil }
	if got := gitAccess(); got != "read" {
		t.Fatalf("unknown: %q", got)
	}
	t.Setenv(envAccess, "write")
	if got := gitAccess(); got != "write" {
		t.Fatalf("forced: %q", got)
	}
}

func TestRepoOfURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/owner/repo.git":     "owner/repo",
		"https://github.com/owner/repo":         "owner/repo",
		"git@github.com:owner/repo.git":         "owner/repo",
		"ssh://git@github.com/owner/repo":       "owner/repo",
		"owner/repo":                            "owner/repo",
		"github.com/owner/repo":                 "owner/repo",
		"https://gitlab.com/owner/repo.git":     "",
		"https://github.com/owner":              "",
		"https://x-access-token@github.com/o/r": "o/r",
		"":                                      "",
	} {
		if got := repoOfURL(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

type fakeGhEnv struct {
	vars   map[string]string
	origin string
	ran    []string
	env    []string
}

func (f *fakeGhEnv) getenv(k string) string  { return f.vars[k] }
func (f *fakeGhEnv) realGh() (string, error) { return "/usr/bin/gh", nil }
func (f *fakeGhEnv) originURL() string       { return f.origin }
func (f *fakeGhEnv) environ() []string       { return []string{"PATH=/usr/bin"} }
func (f *fakeGhEnv) exec(path string, args, env []string) error {
	f.ran, f.env = append([]string{path}, args...), env
	return nil
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestGhGetsATokenForItsRepo(t *testing.T) {
	f := &fakeNode{status: 200, answer: answer{Username: "x-access-token", Password: "ghs_pr"}}
	c := f.serve(t)
	e := &fakeGhEnv{origin: "https://github.com/owner/repo.git"}
	if code := runGh([]string{"pr", "create", "--draft", "--fill"}, c, e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(f.got) != 1 || f.got[0] != (request{Protocol: "https", Host: "api.github.com", Path: "owner/repo", Access: "write", Tool: "gh"}) {
		t.Fatalf("asked %+v", f.got)
	}
	if strings.Join(e.ran, " ") != "/usr/bin/gh gh pr create --draft --fill" || !hasEnv(e.env, "GH_TOKEN=ghs_pr") {
		t.Fatalf("ran %v with %v", e.ran, e.env)
	}

	// gh api names its repository in its endpoint.
	runGh([]string{"api", "repos/someone/thing/pulls", "--jq", ".[].title"}, c, e)
	if last := f.got[len(f.got)-1]; last.Path != "someone/thing" || last.Access != "read" {
		t.Fatalf("gh api asked %+v", last)
	}
	// Its version or help need no token.
	n := len(f.got)
	runGh([]string{"--version"}, c, e)
	runGh([]string{"pr", "create", "--help"}, c, e)
	if len(f.got) != n || e.ran[2] != "pr" {
		t.Fatalf("asked for gh's help: %+v", f.got[n:])
	}

	// -R wins over the remote; a read asks for read.
	runGh([]string{"pr", "view", "12", "-R", "other/thing"}, c, e)
	if last := f.got[len(f.got)-1]; last.Path != "other/thing" || last.Access != "read" {
		t.Fatalf("asked %+v", last)
	}
}

func TestGhRefused(t *testing.T) {
	f := &fakeNode{status: 403, answer: answer{Reason: "denied by the user"}}
	e := &fakeGhEnv{origin: "git@github.com:owner/repo.git"}
	if code := runGh([]string{"pr", "merge", "1"}, f.serve(t), e); code != 1 || e.ran != nil {
		t.Fatalf("exit %d, ran %v", code, e.ran)
	}
}

func TestGhWithOwnTokenOrNoRepo(t *testing.T) {
	f := &fakeNode{status: 200, answer: answer{Password: "ghs"}}
	c := f.serve(t)
	e := &fakeGhEnv{vars: map[string]string{"GH_TOKEN": "mine"}, origin: "https://github.com/o/r"}
	runGh([]string{"pr", "list"}, c, e)
	e2 := &fakeGhEnv{}
	runGh([]string{"api", "/user"}, c, e2)
	if len(f.got) != 0 || e.ran == nil || e2.ran == nil || hasEnv(e2.env, "GH_TOKEN=ghs") {
		t.Fatalf("asked %+v; ran %v and %v", f.got, e.ran, e2.ran)
	}
}

func TestGhAccess(t *testing.T) {
	for args, want := range map[string]string{
		"pr list":                         "read",
		"pr view 3 --comments":            "read",
		"pr create --fill":                "write",
		"pr merge 3":                      "write",
		"issue comment 4 -b hi":           "write",
		"issue list -R o/r":               "read",
		"run view 9 --log":                "read",
		"api repos/o/r/pulls":             "read",
		"api -X GET search/issues -f q=x": "read",
		"api repos/o/r/issues -f title=x": "write",
		"api --method PATCH repos/o/r":    "write",
		"repo clone o/r":                  "write",
		"auth status":                     "write",
		"":                                "read",
	} {
		if got := ghAccess(strings.Fields(args)); got != want {
			t.Errorf("gh %s: %q, want %q", args, got, want)
		}
	}
}

func TestAskErrors(t *testing.T) {
	f := &fakeNode{status: 500, answer: answer{Reason: "core unreachable"}}
	_, err := f.serve(t).ask(request{Host: "github.com"})
	var refused *refusedError
	if err == nil || errors.As(err, &refused) || !strings.Contains(err.Error(), "core unreachable") {
		t.Fatalf("err = %v", err)
	}
}

// The wrapper, copied as gh ahead of the real one, finds the real one
// further on the PATH (installed later by apt, say), never itself.
func TestRealGhSkipsTheWrapper(t *testing.T) {
	self, _ := os.Executable()
	selfDir := filepath.Dir(self)
	if err := os.WriteFile(filepath.Join(selfDir, "gh"), []byte("copy"), 0o755); err != nil {
		t.Skip("cannot write next to the test binary")
	}
	t.Cleanup(func() { os.Remove(filepath.Join(selfDir, "gh")) })
	usr := t.TempDir()
	t.Setenv("PATH", selfDir+string(os.PathListSeparator)+usr)
	if _, err := (osEnv{}).realGh(); err == nil || !strings.Contains(err.Error(), "apt-get install") {
		t.Fatalf("no real gh yet: %v", err)
	}
	os.WriteFile(filepath.Join(usr, "gh"), []byte("#!/bin/sh\n"), 0o755)
	if p, err := (osEnv{}).realGh(); err != nil || p != filepath.Join(usr, "gh") {
		t.Fatalf("real gh %q %v", p, err)
	}
}
