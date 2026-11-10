package main

import (
	"context"
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

// Without the app, no repository is known to be the user's own: the
// GitHub tool's token is not asked.
func TestRepoSkillsWithoutApp(t *testing.T) {
	c, _ := credCore(t, "")
	if c.repoSkills(context.Background(), &ResourceRef{Kind: resRepo, Ref: "owner/repo"}) {
		t.Error("no app: a repository is not known to be the user's")
	}
}
