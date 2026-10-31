package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A repository's own agent skills (.claude/skills, .agents/skills,
// .opencode/skills): instructions, and often scripts, that the agents load
// by themselves from the clone. They are loaded only from the user's own
// repositories, or from one the user said they trust (a repo resource's
// trust_skills); the node turns them off otherwise (node/skills.go). The
// spec tells the node with repo_skills.
//
// The user's own repository is one the user reached GitHub through:
//
//   - with Eriphe's GitHub App, a repository the app is installed on (only
//     the repository's owner, or an admin of its organization, installs it);
//   - without the app, one the GitHub tool's token may push to.
//
// Anything else (another host, an unknown repository, GitHub not reachable)
// is someone else's.

// repoSkills tells whether the skills of a task's repository load.
func (c *core) repoSkills(ctx context.Context, repo *ResourceRef) bool {
	if repo == nil {
		return false
	}
	if repo.TrustSkills {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	own, err := c.ownRepo(ctx, repo.Ref)
	if err != nil {
		log.Printf("repository %s: cannot tell whether it is the user's own (%v): its skills stay off", repo.Ref, err)
	}
	return own
}

// ownRepo tells whether a repository is the user's own (see above).
func (c *core) ownRepo(ctx context.Context, ref string) (bool, error) {
	repo := githubRepo(ref)
	if repo == "" {
		return false, nil
	}
	owner, name, _ := strings.Cut(repo, "/")
	app, err := loadGitHubApp()
	switch {
	case err == nil:
		_, err := c.github.installationFor(ctx, app, owner, name)
		if err != nil && strings.Contains(err.Error(), "is not installed on") {
			return false, nil
		}
		return err == nil, err
	case err != errNoGitHubApp:
		return false, err
	}
	tok, err := c.githubToolToken()
	if err != nil {
		return false, nil // no GitHub at all: nothing is known to be the user's
	}
	var out struct {
		Permissions struct {
			Admin bool `json:"admin"`
			Push  bool `json:"push"`
		} `json:"permissions"`
	}
	err = c.github.do(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), "token "+tok, nil, &out)
	if ge, ok := err.(*githubError); ok && ge.Status == http.StatusNotFound {
		return false, nil
	}
	return err == nil && (out.Permissions.Admin || out.Permissions.Push), err
}
