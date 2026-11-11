package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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
// write, and, for a push, the case of each ref it updates (decideRepo):
// a deletion is Supprimer, the default branch or a tag Envoyer, any other
// branch Modifier. The commands are read at the head of the push; what is
// pushed is not. The rest is the provider's: a token for that one
// repository and the repository's own protection rules (a forced push is
// Modifier, left to them).

// gitTokenEnvs are the variables git and gh read their token from: a
// task's placeholder, never a credential's of its own.
var gitTokenEnvs = []string{"GH_TOKEN", "GITHUB_TOKEN"}

const (
	gitHost   = "github.com"
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

func isRepoRef(e ResourceRef) bool { return e.Kind == resRepo && repoOf(e.Ref) != "" }

// lentGit is GitHub as the node gets it, for a task lent a repository there.
// Reads go this way too: one path for all of a repository.
func lentGit(taskID string, list []ResourceRef) []nodeAccess {
	if !slices.ContainsFunc(list, isRepoRef) {
		return nil
	}
	return []nodeAccess{{Name: gitAccessName, Hosts: []string{gitTarget, ghTarget}, Env: gitAccessEnv, Placeholder: gitPlaceholder(taskID)}}
}

// gitCall is what a request to github.com is, for git.
type gitCall struct {
	repo   string // owner/name, if it is a call of git's
	access string
	lfs    bool // Large File Storage: its path does not tell an upload
	push   bool // the push itself, whose commands say what it updates
}

// readGitCall reads a request's path.
func readGitCall(method string, uri *url.URL) gitCall {
	// What is forwarded is what was read: no way around it in the path.
	if uri.RawPath != "" || slices.ContainsFunc(strings.Split(uri.Path, "/"), func(s string) bool { return s == "." || s == ".." }) {
		return gitCall{}
	}
	parts := strings.SplitN(strings.TrimPrefix(uri.Path, "/"), "/", 3)
	if len(parts) != 3 {
		return gitCall{}
	}
	repo := repoOf(parts[0] + "/" + parts[1])
	switch rest := parts[2]; {
	case rest == gitReceivePack && method == http.MethodPost:
		return gitCall{repo: repo, access: accessWrite, push: true}
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
	return (target == gitTarget || target == ghTarget) && slices.ContainsFunc(t.Resources, isRepoRef)
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
		c.store.logAccess(t.ID, gitLogTool, "github.com", call.repo, call.access, decideDeny, byPolicy, "", line)
		log.Printf("git: task %s: %s %s refused: %s", t.ID, r.Method, call.repo, line)
	}
	facts := c.repoFacts(t)
	deny := func(d repoDecision) (*strings.Replacer, bool) {
		why := d.Reason
		if d.Decision == decideAsk {
			why = notApproved(why)
		}
		refuse(why)
		http.Error(w, why, http.StatusForbidden)
		return none, false
	}
	if call.lfs {
		if mayWrite(call.repo, facts) {
			call.access = accessWrite // it may be an upload: what the task may write, it may store
		}
	}
	d := decideRepo(call.repo, catRead, facts)
	if call.access == accessWrite {
		// The discovery of a push, or an upload: one case at least must go.
		if d = decideRepo(call.repo, catChange, facts); !mayWrite(call.repo, facts) {
			return deny(d)
		}
		d = repoDecision{decideAllow, "a write the repository's cases allow"}
	}
	if d.Decision != decideAllow {
		return deny(d)
	}
	m, err := c.mintGitHub(r.Context(), t.ID, call.repo, call.access, false)
	if err != nil {
		refuse("cannot give a credential: " + err.Error())
		http.Error(w, "eriphe-core cannot get a token for "+call.repo, http.StatusBadGateway)
		return none, false
	}
	if call.push {
		cmds, body, err := pushCommands(r.Body)
		if err != nil || r.Header.Get("Content-Encoding") != "" {
			return deny(repoDecision{decideDeny, "eriphe-core cannot read which branches this push updates"})
		}
		r.Body = struct {
			io.Reader
			io.Closer
		}{body, r.Body}
		main, err := c.github.defaultBranch(r.Context(), call.repo, m.Password)
		if err != nil {
			return deny(repoDecision{decideDeny, "eriphe-core cannot read the default branch of " + call.repo + ": " + err.Error()})
		}
		for _, cmd := range cmds {
			if d = decideRepo(call.repo, cmd.category(main), facts); d.Decision != decideAllow {
				return deny(repoDecision{d.Decision, cmd.Ref + ": " + d.Reason})
			}
		}
	}
	c.store.logAccess(t.ID, gitLogTool, "github.com", call.repo, call.access, decideAllow, byPolicy, m.Source, d.Reason)
	return putGitHubToken(r, placeholder, m.Password), true
}

// mayWrite: one of the repository's write cases goes for this task.
func mayWrite(repo string, f repoFacts) bool {
	return slices.ContainsFunc(repoWriteCases, func(cat string) bool { return decideRepo(repo, cat, f).Decision == decideAllow })
}

// pushCommand is one ref a push updates.
type pushCommand struct{ Old, New, Ref string }

// category is the case of the repository the command is: a deletion
// Supprimer, the default branch or a tag Envoyer, any other ref Modifier.
func (p pushCommand) category(defaultBranch string) string {
	switch {
	case strings.Trim(p.New, "0") == "":
		return catDelete
	case p.Ref == "refs/heads/"+defaultBranch, strings.HasPrefix(p.Ref, "refs/tags/"):
		return catSend
	}
	return catChange
}

// pushCommands reads the commands at the head of a push (git's pkt-lines,
// up to the flush), and gives the body back whole to forward.
func pushCommands(body io.Reader) ([]pushCommand, io.Reader, error) {
	var head bytes.Buffer
	in := io.TeeReader(body, &head)
	var cmds []pushCommand
	for range 10000 {
		var size [4]byte
		if _, err := io.ReadFull(in, size[:]); err != nil {
			return nil, nil, err
		}
		n, err := strconv.ParseUint(string(size[:]), 16, 16)
		switch {
		case err != nil:
			return nil, nil, err
		case n == 0: // the flush; alone, git's probe of a large push
			return cmds, io.MultiReader(&head, body), nil
		case n < 4:
			return nil, nil, errors.New("not a push")
		}
		line := make([]byte, n-4)
		if _, err := io.ReadFull(in, line); err != nil {
			return nil, nil, err
		}
		text, _, _ := strings.Cut(string(line), "\x00") // the first line's capabilities
		if strings.HasPrefix(text, "shallow ") {
			continue
		}
		f := strings.Fields(text)
		if len(f) != 3 {
			return nil, nil, errors.New("not a push")
		}
		cmds = append(cmds, pushCommand{f[0], f[1], f[2]})
	}
	return nil, nil, errors.New("too many refs in one push")
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
	return repoOf(parts[1] + "/" + parts[2])
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
		c.store.logAccess(t.ID, ghLogTool, "api.github.com", repo, accessRead, decideDeny, byPolicy, "", line)
		log.Printf("gh: task %s: %s %s refused: %s", t.ID, r.Method, repo, line)
		http.Error(w, why, status)
		return none, false
	}
	facts := c.repoFacts(t)
	named := ghRepoOf(uri.Path)
	repos := facts.Repos
	if len(repos) != 1 || named != "" && !strings.EqualFold(named, repos[0].Repo) {
		return refuse(http.StatusForbidden, named, orStr(named, "GitHub")+" is not lent to this task")
	}
	repo, access := repos[0].Repo, accessRead
	if decideRepo(repo, catChange, facts).Decision == decideAllow {
		access = accessWrite // pull requests and issues: Modifier
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
	c.store.logAccess(t.ID, ghLogTool, "api.github.com", repo, access, decideAllow, byPolicy, m.Source, truncate(r.Method+" "+uri.Path, 200))
	return putGitHubToken(r, placeholder, m.Password), true
}
