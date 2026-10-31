package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// With the GitHub App, the user's own repositories are those it is
// installed on; trust_skills loads any repository's skills.
func TestRepoSkillsWithApp(t *testing.T) {
	c, _ := credCore(t, "")
	f := newFakeGitHub(t)
	withApp(t, c, f)
	ctx := context.Background()
	for _, tc := range []struct {
		repo *ResourceRef
		want bool
	}{
		{nil, false},
		{&ResourceRef{Kind: resRepo, Ref: "owner/repo"}, true},
		{&ResourceRef{Kind: resRepo, Ref: "https://github.com/owner/repo.git"}, true},
		{&ResourceRef{Kind: resRepo, Ref: "owner/elsewhere"}, false},
		{&ResourceRef{Kind: resRepo, Ref: "someone/tool"}, false},
		{&ResourceRef{Kind: resRepo, Ref: "someone/tool", TrustSkills: true}, true},
		{&ResourceRef{Kind: resRepo, Ref: "https://git.example.org/owner/repo.git"}, false},
	} {
		if got := c.repoSkills(ctx, tc.repo); got != tc.want {
			t.Errorf("%+v: repo skills %v, want %v", tc.repo, got, tc.want)
		}
	}
}

// Without the app, the user's own repositories are those the GitHub
// tool's token may push to; without any GitHub, none.
func TestRepoSkillsWithoutApp(t *testing.T) {
	c, _ := credCore(t, "")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token github_pat_long_lived" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/owner/repo":
			io.WriteString(w, `{"permissions": {"admin": false, "push": true, "pull": true}}`)
		case "/repos/someone/tool":
			io.WriteString(w, `{"permissions": {"admin": false, "push": false, "pull": true}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
		}
	}))
	defer api.Close()
	c.github.api = api.URL
	ctx := context.Background()
	for ref, want := range map[string]bool{"owner/repo": true, "someone/tool": false, "owner/gone": false} {
		if got := c.repoSkills(ctx, &ResourceRef{Kind: resRepo, Ref: ref}); got != want {
			t.Errorf("%s: repo skills %v, want %v", ref, got, want)
		}
	}
	currentVault().remove("server.github.token")
	if c.repoSkills(ctx, &ResourceRef{Kind: resRepo, Ref: "owner/repo"}) {
		t.Error("no GitHub at all: a repository is not known to be the user's")
	}
}
