package main

import (
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// git and gh on the path of an access. A task lent a GitHub repository holds
// no token: GH_TOKEN holds a placeholder, which gh reads and git's credential
// helper hands over. The node's relay decrypts github.com and api.github.com
// for that task and hands each request here, as for any access
// (lent_access.go), where the real token goes in:
//
//	git, gh → the node's relay → eriphe-core (decides, mints) → GitHub
//
// The decision is made on each request, from what the request is: a push is
// git-receive-pack, and a placeholder is no proof.
//
// A request of gh does not always name a repository (much of it is one POST
// /graphql), and is not classified: what it may do is the token's, which
// writes pull requests and issues, never contents, minted as the lend says,
// for the task's one repository (ghRequest).
//
// As for any access, what is decided is the lend: a repository, read or
// write. What is pushed is not read. The branch lent is what the task is
// told to push to, and the rest is the provider's, a token for that one
// repository and the repository's own protection rules. The shell rules
// (policy.go) stop a push to main or a forced one by mistake, no more.

const (
	gitTarget = "github.com:443"
	ghTarget  = "api.github.com:443"
	// gitAccessName and gitAccessEnv are how the node gets GitHub, as an
	// access of the task's.
	gitAccessName = "github"
	gitAccessEnv  = "GH_TOKEN"

	gitLogTool = "git"
	ghLogTool  = "gh"

	gitReceivePack = "git-receive-pack"
	gitUploadPack  = "git-upload-pack"
)

// gitPlaceholder is what a task's git holds in place of a token.
func gitPlaceholder(taskID string) string { return "eriphe-git-" + taskID }

func isGitHubRepo(e ResourceRef) bool { return e.Kind == resRepo && githubRepo(e.Ref) != "" }

// lentGit is GitHub as the node gets it, for a task lent a repository there.
// Reads go this way too: one path for all of a repository.
func lentGit(taskID string, list []ResourceRef) []nodeAccess {
	if !slices.ContainsFunc(list, isGitHubRepo) {
		return nil
	}
	return []nodeAccess{{Name: gitAccessName, Hosts: []string{gitTarget, ghTarget}, Env: gitAccessEnv, Placeholder: gitPlaceholder(taskID)}}
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
	if call.lfs && decideRepo(call.repo, accessWrite, facts).Decision == decideAllow {
		call.access = accessWrite // it may be an upload: what the task may write, it may store
	}
	d := decideRepo(call.repo, call.access, facts)
	if d.Decision != decideAllow {
		why := d.Reason
		if d.Decision == decideAsk {
			why = notApproved(why)
		}
		refuse(why)
		http.Error(w, why, http.StatusForbidden)
		return none, false
	}
	m, err := c.mintGitHub(r.Context(), t.ID, call.repo, call.access, false)
	if err != nil {
		refuse("cannot give a credential: " + err.Error())
		http.Error(w, "eriphe-core cannot get a token for "+call.repo, http.StatusBadGateway)
		return none, false
	}
	c.store.logCredential(t.ID, gitLogTool, "github.com", call.repo, call.access, decideAllow, byPolicy, m.Source, d.Reason)
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

// ghRepoOf is the repository of a REST path of api.github.com, if it names one.
func ghRepoOf(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "repos" {
		return ""
	}
	return githubRepo(parts[1] + "/" + parts[2])
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
	named := ghRepoOf(uri.Path)
	repos := facts.Repos
	if len(repos) != 1 || named != "" && !strings.EqualFold(named, repos[0].Repo) {
		return refuse(http.StatusForbidden, named, orStr(named, "GitHub")+" is not lent to this task")
	}
	repo, access := repos[0].Repo, accessRead
	if decideRepo(repo, accessWrite, facts).Decision == decideAllow {
		access = accessWrite
	}
	m, err := c.mintGitHub(r.Context(), t.ID, repo, access, true)
	if err != nil && access == accessRead {
		// A public repository the App is not installed on reads without one.
		log.Printf("gh: task %s: no token for %s, going without: %v", t.ID, repo, err)
		r.Header.Del("Authorization")
		return none, true
	}
	if err != nil {
		return refuse(http.StatusBadGateway, repo, "eriphe-core cannot get a token: "+err.Error())
	}
	c.store.logCredential(t.ID, ghLogTool, "api.github.com", repo, access, decideAllow, byPolicy, m.Source, truncate(r.Method+" "+uri.Path, 200))
	return putGitHubToken(r, placeholder, m.Password), true
}
