package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestAskErrors(t *testing.T) {
	f := &fakeNode{status: 500, answer: answer{Reason: "core unreachable"}}
	_, err := f.serve(t).ask(request{Host: "github.com"})
	var refused *refusedError
	if err == nil || errors.As(err, &refused) || !strings.Contains(err.Error(), "core unreachable") {
		t.Fatalf("err = %v", err)
	}
}
