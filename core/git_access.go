package main

import (
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// git on the path of an access. A task lent a GitHub repository holds no
// token: eriphe-cred still asks (credentials.go), and gets a placeholder
// once the decision is made, approval included. The node's relay decrypts
// github.com for that task and hands each request here, as for any access
// (lent_access.go), where the real token goes in:
//
//	git → the node's relay → eriphe-core (decides, mints) → GitHub
//
// The decision is made again on each request, from what the request is
// rather than what the helper said: a placeholder is no proof, the task can
// write it down without asking.
//
// As for any access, what is decided is the lend: a repository, read or
// write. What is pushed is not read. The branch lent is what the task is
// told to push to, and the rest is the provider's, a token for that one
// repository and the repository's own protection rules. The shell rules
// (policy.go) stop a push to main or a forced one by mistake, no more.

const (
	gitTarget = "github.com:443"
	// gitAccessName and gitAccessEnv are how the node gets github.com, as an
	// access of the task's. Nothing reads the variable: git asks its helper.
	gitAccessName = "github"
	gitAccessEnv  = "ERIPHE_GIT_PLACEHOLDER"

	gitLogTool = "git"

	gitReceivePack = "git-receive-pack"
	gitUploadPack  = "git-upload-pack"
)

// gitPlaceholder is what a task's git holds in place of a token.
func gitPlaceholder(taskID string) string { return "eriphe-git-" + taskID }

func isGitHubRepo(e ResourceRef) bool { return e.Kind == resRepo && githubRepo(e.Ref) != "" }

// lentGit is github.com as the node gets it, for a task lent a repository
// there. Reads go this way too: one path for all of a repository.
func lentGit(taskID string, list []ResourceRef) []nodeAccess {
	if !slices.ContainsFunc(list, isGitHubRepo) {
		return nil
	}
	return []nodeAccess{{Name: gitAccessName, Hosts: []string{gitTarget}, Env: gitAccessEnv, Placeholder: gitPlaceholder(taskID)}}
}

// gitRelayed: the relay of the task asking decrypts github.com, as its node
// says. Not so for a task started before, or on an older node: it gets its
// token as it always did, or it could not work at all.
func gitRelayed(req credRequest) bool {
	return req.Tool != "gh" && slices.Contains(req.Relayed, gitTarget)
}

// gitCall is what a request to github.com is, for git.
type gitCall struct {
	repo   string // owner/name, if it is a call of git's
	access string
	lfs    bool // Large File Storage: its path does not tell an upload
}

// readGitCall reads a request's path.
func readGitCall(method string, uri *url.URL) gitCall {
	parts := strings.SplitN(strings.TrimPrefix(uri.Path, "/"), "/", 3)
	if len(parts) != 3 {
		return gitCall{}
	}
	repo := githubRepo(parts[0] + "/" + parts[1])
	switch rest := parts[2]; {
	case rest == gitReceivePack && method == http.MethodPost:
		return gitCall{repo: repo, access: accessWrite}
	case rest == "info/refs" && uri.Query().Get("service") == gitReceivePack:
		return gitCall{repo: repo, access: accessWrite}
	case strings.HasPrefix(rest, "info/lfs/"):
		return gitCall{repo: repo, access: accessRead, lfs: true}
	case rest == "info/refs", rest == gitUploadPack:
		return gitCall{repo: repo, access: accessRead}
	}
	return gitCall{}
}

// gitLent: this request is one for the git path of the task.
func gitLent(t *TaskRow, target string) bool {
	return target == gitTarget && slices.ContainsFunc(t.Resources, isGitHubRepo)
}

// gitRequest decides a request to github.com and puts the token in. It tells
// what takes the token out of the answer's headers, and false when it has
// answered itself. A request that carries no placeholder asks for no token
// and goes as it is.
func (c *core) gitRequest(w http.ResponseWriter, t *TaskRow, r *http.Request, uri *url.URL) (*strings.Replacer, bool) {
	none := strings.NewReplacer()
	call := readGitCall(r.Method, uri)
	value := r.Header.Get("Authorization")
	placeholder := gitPlaceholder(t.ID)
	if _, carried := swapPlaceholder(value, placeholder, ""); !carried || call.repo == "" {
		return none, true
	}
	refuse := func(why string) {
		line := truncate(oneLine(why), 300)
		c.store.logCredential(t.ID, gitLogTool, "github.com", call.repo, call.access, decideDeny, byPolicy, "", line)
		log.Printf("git: task %s: %s %s refused: %s", t.ID, r.Method, call.repo, line)
	}
	facts := c.credentialFacts(t)
	decide := func(access string) credDecision {
		return decideCredential(c.conf().Policy, credRequest{Protocol: "https", Host: "github.com", Path: call.repo, Access: access, Tool: gitLogTool}, facts)
	}
	if call.lfs && decide(accessWrite).Decision == decideAllow {
		call.access = accessWrite // it may be an upload: what the task may write, it may store
	}
	d := decide(call.access)
	if d.Decision != decideAllow {
		why := d.Reason
		if d.Decision == decideAsk {
			why = "this needs the user's approval, which this task did not get: " + why
		}
		refuse(why)
		http.Error(w, why, http.StatusForbidden)
		return none, false
	}
	m, err := c.mintGitHub(r.Context(), t.ID, d.Repo, call.access, false)
	if err != nil {
		refuse("cannot give a credential: " + err.Error())
		http.Error(w, "eriphe-core cannot get a token for "+d.Repo, http.StatusBadGateway)
		return none, false
	}
	swapped, _ := swapPlaceholder(value, placeholder, m.Password)
	r.Header.Set("Authorization", swapped)
	c.store.logCredential(t.ID, gitLogTool, "github.com", d.Repo, call.access, decideAllow, byPolicy, m.Source, d.Reason)
	pairs := []string{swapped, value, m.Password, placeholder}
	if _, encoded, ok := strings.Cut(swapped, " "); ok {
		_, original, _ := strings.Cut(value, " ")
		pairs = append(pairs, encoded, original)
	}
	return strings.NewReplacer(pairs...), true
}
