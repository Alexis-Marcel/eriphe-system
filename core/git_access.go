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
// once the decision is made. The node's relay decrypts
// github.com for that task and hands each request here, as for any access
// (lent_access.go), where the real token goes in:
//
//	git → the node's relay → eriphe-core (decides, mints) → GitHub
//
// The decision is made again on each request, from what the request is
// rather than what the helper said: a placeholder is no proof, the task can
// write it down without asking.
//
// gh goes the same way, to api.github.com: it reads GH_TOKEN, which holds the
// same placeholder. Its token writes pull requests and issues, never contents.
// A request of gh does not always name a repository (much of it is one POST
// /graphql), and is not classified: what it may do is the token's, minted as
// the lend says, for the repositories lent (ghRequest).
//
// As for any access, what is decided is the lend: a repository, read or
// write. What is pushed is not read. The branch lent is what the task is
// told to push to, and the rest is the provider's, a token for that one
// repository and the repository's own protection rules. The shell rules
// (policy.go) stop a push to main or a forced one by mistake, no more.

const (
	gitTarget = "github.com:443"
	ghTarget  = "api.github.com:443"
	// gitAccessName and gitAccessEnv are how the node gets github.com, as an
	// access of the task's. Nothing reads the variable: git asks its helper.
	gitAccessName = "github"
	gitAccessEnv  = "ERIPHE_GIT_PLACEHOLDER"
	ghAccessName  = "github-api"
	ghAccessEnv   = "GH_TOKEN"

	gitLogTool = "git"
	ghLogTool  = "gh"

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
	return []nodeAccess{
		{Name: gitAccessName, Hosts: []string{gitTarget}, Env: gitAccessEnv, Placeholder: gitPlaceholder(taskID)},
		{Name: ghAccessName, Hosts: []string{ghTarget}, Env: ghAccessEnv, Placeholder: gitPlaceholder(taskID)},
	}
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
	return (target == gitTarget || target == ghTarget) && slices.ContainsFunc(t.Resources, isGitHubRepo)
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
		return decideCredential(credRequest{Protocol: "https", Host: "github.com", Path: call.repo, Access: access, Tool: gitLogTool}, facts)
	}
	if call.lfs && decide(accessWrite).Decision == decideAllow {
		call.access = accessWrite // it may be an upload: what the task may write, it may store
	}
	d := decide(call.access)
	if d.Decision != decideAllow {
		why := d.Reason
		if d.Decision == decideAsk {
			why = notApproved(why)
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
	c.store.logCredential(t.ID, gitLogTool, "github.com", d.Repo, call.access, decideAllow, byPolicy, m.Source, d.Reason)
	return putGitHubToken(r, placeholder, m.Password), true
}

// putGitHubToken swaps the token in, and tells what takes it out of the
// answer's headers.
func putGitHubToken(r *http.Request, placeholder, token string) *strings.Replacer {
	value := r.Header.Get("Authorization")
	swapped, _ := swapPlaceholder(value, placeholder, token)
	r.Header.Set("Authorization", swapped)
	pairs := []string{swapped, value, token, placeholder}
	if _, encoded, ok := strings.Cut(swapped, " "); ok {
		_, original, _ := strings.Cut(value, " ")
		pairs = append(pairs, encoded, original)
	}
	return strings.NewReplacer(pairs...)
}

func repoOwner(repo string) string {
	owner, _, _ := strings.Cut(repo, "/")
	return owner
}

// ghRepoOf is the repository of a REST path of api.github.com, if it names one.
func ghRepoOf(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "repos" {
		return ""
	}
	return githubRepo(parts[1] + "/" + parts[2])
}

// ghRepos are the repositories a token of gh is for, and its access: the one
// the request names, or else those lent, of one owner as a token is. Those
// that may be written come first, and alone, so that a token that writes
// holds nothing lent for read. A write not allowed yet gets a token that
// reads, and GitHub refuses the write.
func ghRepos(named string, lent []repoGrant, allows func(repo, access string) bool) ([]string, string) {
	pick := func(access string) []string {
		var names []string
		for _, g := range lent {
			switch {
			case named != "" && !strings.EqualFold(g.Repo, named):
			case len(names) > 0 && !strings.EqualFold(repoOwner(names[0]), repoOwner(g.Repo)):
			case allows(g.Repo, access):
				names = append(names, g.Repo)
			}
		}
		return names
	}
	if names := pick(accessWrite); len(names) > 0 {
		return names, accessWrite
	}
	return pick(accessRead), accessRead
}

// ghRequest puts gh's token in a request to api.github.com, as gitRequest
// does for git. A request that carries no placeholder goes as it is.
func (c *core) ghRequest(w http.ResponseWriter, t *TaskRow, r *http.Request, uri *url.URL) (*strings.Replacer, bool) {
	none := strings.NewReplacer()
	placeholder := gitPlaceholder(t.ID)
	if _, carried := swapPlaceholder(r.Header.Get("Authorization"), placeholder, ""); !carried {
		return none, true
	}
	refuse := func(status int, repo, why string) (*strings.Replacer, bool) {
		line := truncate(oneLine(why), 300)
		c.store.logCredential(t.ID, ghLogTool, "api.github.com", repo, accessRead, decideDeny, byPolicy, "", line)
		log.Printf("gh: task %s: %s %s refused: %s", t.ID, r.Method, repo, line)
		http.Error(w, why, status)
		return none, false
	}
	facts := c.credentialFacts(t)
	if facts.Fallback {
		// The tool's own token holds everything, and these requests are not
		// classified: only a token of the App, limited as lent, goes here.
		return refuse(http.StatusForbidden, "", "gh needs Eriphe's GitHub App: create it in Settings → Tools → GitHub")
	}
	allows := func(repo, access string) bool {
		req := credRequest{Protocol: "https", Host: "api.github.com", Path: repo, Access: access, Tool: ghLogTool}
		return decideCredential(req, facts).Decision == decideAllow
	}
	named := ghRepoOf(uri.Path)
	repos, access := ghRepos(named, facts.Repos, allows)
	if len(repos) == 0 {
		return refuse(http.StatusForbidden, named, orStr(named, "GitHub")+" is not lent to this task")
	}
	m, err := c.mintGitHubFor(r.Context(), t.ID, repos, access, true)
	if err != nil && access == accessRead {
		// A public repository the App is not installed on reads without one.
		log.Printf("gh: task %s: no token for %s, going without: %v", t.ID, strings.Join(repos, ", "), err)
		r.Header.Del("Authorization")
		return none, true
	}
	if err != nil {
		return refuse(http.StatusBadGateway, repos[0], "eriphe-core cannot get a token: "+err.Error())
	}
	c.store.logCredential(t.ID, ghLogTool, "api.github.com", strings.Join(repos, ", "), access, decideAllow, byPolicy, m.Source, truncate(r.Method+" "+uri.Path, 200))
	return putGitHubToken(r, placeholder, m.Password), true
}
