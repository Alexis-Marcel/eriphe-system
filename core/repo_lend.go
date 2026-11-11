package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A task and GitHub. A task lent a GitHub repository holds a placeholder; git
// and gh use it as a token, and each request crosses the node's relay to
// eriphe-core (git_access.go), which decides, deny by default (decideRepo):
// the repository must be lent to the task, as a repo resource (resources.go),
// for write to push, and the push is held to the repository's case
// (connection_places.go): Modifier for a branch, Supprimer to delete one,
// Envoyer for the default branch or a tag. What is decided is the lend, at
// launch, as for any access (lent_access.go): a case never is refused; a
// free one needs a clean start (lend.go); one on approval, or a free one
// otherwise, the user's yes on the start's card (task_start.go). What the
// task reads afterwards changes nothing, and nothing waits in a command:
// what is not lent is refused at once, with its reason. The token that goes
// in is short-lived, for that one repository and those rights only: a
// GitHub App installation token, one hour (github_app.go). Every request is
// logged (access_log) and shown in the task's details.

const (
	accessRead  = "read"
	accessWrite = "write"
)

const repoLendSchema = `CREATE TABLE IF NOT EXISTS access_log (
  time INTEGER NOT NULL, task_id TEXT NOT NULL, tool TEXT NOT NULL, host TEXT NOT NULL, target TEXT NOT NULL,
  action TEXT NOT NULL, decision TEXT NOT NULL, by TEXT NOT NULL, source TEXT NOT NULL, reason TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS access_log_task ON access_log (task_id, time);
CREATE TABLE IF NOT EXISTS repo_grants (task_id TEXT NOT NULL, service TEXT NOT NULL, repo TEXT NOT NULL,
  access TEXT NOT NULL, time INTEGER NOT NULL, PRIMARY KEY (task_id, service, repo, access));`

func init() { extraSchema = append(extraSchema, repoLendSchema) }

// isGitHubHost: a host of GitHub's.
func isGitHubHost(host string) bool {
	switch strings.ToLower(strings.TrimSuffix(host, ":443")) {
	case "github.com", "api.github.com", "www.github.com":
		return true
	}
	return false
}

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// repoOf reads owner/name from a path (owner/name.git), a GitHub URL
// (https://github.com/owner/name, git@github.com:owner/name.git) or owner/name.
func repoOf(raw string) string {
	raw = strings.TrimSpace(raw)
	p := raw
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		if !isGitHubHost(u.Hostname()) {
			return ""
		}
		p = u.Path
	} else if host, rest, ok := strings.Cut(raw, ":"); ok && strings.Contains(host, "@") {
		_, h, _ := strings.Cut(host, "@")
		if !isGitHubHost(h) {
			return ""
		}
		p = rest
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(p, "/"), ".git"), "/")
	if len(parts) != 2 || !repoPart.MatchString(parts[0]) || !repoPart.MatchString(parts[1]) {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// repoFacts is what the decision needs to know of the task.
type repoFacts struct {
	Repos   []repoGrant // the GitHub repositories lent to it
	Clean   bool        // a clean start (lend.go)
	Granted func(repo, category string) bool
	// Case is a repository's value of a case (repoCase); nil: never.
	Case func(repo, category string) string
}

// repoGrant is a GitHub repository lent to a task: owner/name, its access
// (read or write) and its declared work branch.
type repoGrant struct {
	Repo, Access, Branch string
}

// covering is the repository grant for repo, if any.
func covering(repos []repoGrant, repo string) *repoGrant {
	for i := range repos {
		if strings.EqualFold(repos[i].Repo, repo) {
			return &repos[i]
		}
	}
	return nil
}

// repoDecision is the answer: allow, deny, or ask for what only the user's
// yes on the start's card can lend.
type repoDecision struct {
	Decision, Reason string
}

// repoCaseWords say a case of a repository in the log and to the task.
var repoCaseWords = map[string]string{catChange: "a push to a branch", catDelete: "a branch deletion", catSend: "a push to the default branch or a tag"}

// decideRepo applies the lend and the repository's case to a use of it
// (read, or one of repoWriteCases), deny by default.
func decideRepo(repo, category string, f repoFacts) repoDecision {
	g := covering(f.Repos, repo)
	what := repoCaseWords[category]
	value := gridNever
	if f.Case != nil {
		value = f.Case(repo, category)
	}
	switch {
	case g == nil:
		return repoDecision{decideDeny, repo + " is not the repository this task was asked to work on"}
	case category == catRead:
		return repoDecision{decideAllow, "the task's own repository"}
	case g.Access != accessWrite:
		return repoDecision{decideDeny, repo + " is lent to this task for read: it cannot push there"}
	case value == gridNever:
		return repoDecision{decideDeny, what + " of " + repo + " is never allowed to Eriphe (its rules, Settings → Tools → GitHub)"}
	case value == gridFree && f.Clean:
		return repoDecision{decideAllow, what + " of " + repo + ", free, on the user's own start"}
	case f.Granted != nil && f.Granted(repo, category):
		return repoDecision{decideAllow, "approved by the user at launch"}
	case value == gridApproval:
		return repoDecision{decideAsk, what + " of " + repo + " is on approval"}
	}
	return repoDecision{decideAsk, what + " of " + repo + " on a start that is not the user's own"}
}

// repoFacts is what is known of a task when a use is decided.
func (c *core) repoFacts(t *TaskRow) repoFacts {
	facts := repoFacts{Repos: lentRepos(t.Resources), Clean: c.store.get(lentByUserKey(t.ID)) != "", Case: c.repoCase}
	facts.Granted = func(repo, category string) bool { return c.store.repoGranted(t.ID, repo, category) }
	return facts
}

// notApproved is what a task is told of a write the user did not approve.
func notApproved(why string) string {
	return "this needs the user's approval, which this task did not get: " + why
}

// lentRepos are the GitHub repositories of a list of resources.
func lentRepos(list []ResourceRef) []repoGrant {
	var out []repoGrant
	for _, e := range list {
		if r := repoOf(e.Ref); e.Kind == resRepo && r != "" {
			out = append(out, repoGrant{Repo: r, Access: e.Access, Branch: e.Branch})
		}
	}
	return out
}

// isGitHubServer: GitHub's remote MCP server (the app's connections put
// theirs there), or the one named github.
func isGitHubServer(name string, s Server) bool {
	if u, err := url.Parse(s.URL); err == nil && strings.EqualFold(u.Hostname(), "api.githubcopilot.com") {
		return true
	}
	return name == "github"
}

// taskTaintKey marks a task started on a tainted turn of the brain.
func taskTaintKey(id string) string { return "task_taint:" + id }

// grantRepo keeps the user's yes on a start's card, for one case of a
// repository, for the rest of the task.
func (s *Store) grantRepo(taskID, repo, category string) {
	s.db.Exec(`INSERT OR IGNORE INTO repo_grants (task_id, service, repo, access, time) VALUES (?, 'github', ?, ?, ?)`,
		taskID, strings.ToLower(repo), category, time.Now().Unix())
}

// repoGranted: the user approved this case of the repository in this task.
func (s *Store) repoGranted(taskID, repo, category string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM repo_grants WHERE task_id = ? AND repo = ? AND access = ?`,
		taskID, strings.ToLower(repo), category).Scan(&n)
	return n > 0
}

func (s *Store) logAccess(taskID, tool, host, target, action, decision, by, source, reason string) {
	s.db.Exec(`INSERT INTO access_log (time, task_id, tool, host, target, action, decision, by, source, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, time.Now().Unix(), taskID, tool, host, target, action, decision, by, source, reason)
}

// AccessEntry is one line of a task's access log: what it reached (Target, a
// repository or an access) and how (Action: read, write, a method or ssh).
type AccessEntry struct {
	Time     time.Time `json:"time"`
	Tool     string    `json:"tool"`
	Host     string    `json:"host"`
	Target   string    `json:"target"`
	Action   string    `json:"action"`
	Decision string    `json:"decision"`
	By       string    `json:"by"`
	Source   string    `json:"source,omitempty"` // github-app, when lent
	Reason   string    `json:"reason,omitempty"`
}

func (s *Store) accessLog(taskID string, limit int) ([]AccessEntry, error) {
	rows, err := s.db.Query(`SELECT time, tool, host, target, action, decision, by, source, reason FROM access_log
		WHERE task_id = ? ORDER BY time DESC, rowid DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessEntry{}
	for rows.Next() {
		var e AccessEntry
		var ts int64
		if err := rows.Scan(&ts, &e.Tool, &e.Host, &e.Target, &e.Action, &e.Decision, &e.By, &e.Source, &e.Reason); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (c *core) handleTaskAccessLog(w http.ResponseWriter, r *http.Request) {
	if _, err := c.store.task(r.PathValue("id")); err != nil {
		http.Error(w, "no such task", http.StatusNotFound)
		return
	}
	log, err := c.store.accessLog(r.PathValue("id"), 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, log)
}

// taskSpec is a task's stored spec (no secrets in it).
func (s *Store) taskSpec(id string) (nodeTaskSpec, error) {
	var raw string
	var spec nodeTaskSpec
	if err := s.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, id).Scan(&raw); err != nil {
		return spec, err
	}
	err := json.Unmarshal([]byte(raw), &spec)
	return spec, err
}

// Where a lent credential comes from.
const (
	sourceApp = "github-app"
)

type minted struct {
	Username, Password string
	Expires            time.Time
	Source             string
}

// tokenCache keeps the tokens minted for a task, until ten minutes before
// they end: git asks more than once in one push.
type tokenCache struct {
	mu sync.Mutex
	m  map[string]minted
}

func (cc *tokenCache) get(key string, now time.Time) (minted, bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	m, ok := cc.m[key]
	if ok && m.Expires.Sub(now) < 10*time.Minute {
		delete(cc.m, key)
		ok = false
	}
	return m, ok
}

func (cc *tokenCache) put(key string, m minted) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.m == nil {
		cc.m = map[string]minted{}
	}
	now := time.Now()
	for k, v := range cc.m {
		if v.Expires.Before(now) {
			delete(cc.m, k)
		}
	}
	cc.m[key] = m
}

// githubPermissions are the rights a token gets: git needs the contents
// only; gh reads or writes pull requests and issues, and reads the rest.
func githubPermissions(access string, api bool) map[string]string {
	switch {
	case !api && access == accessWrite:
		return map[string]string{"contents": "write"}
	case !api:
		return map[string]string{"contents": "read"}
	case access == accessWrite:
		return map[string]string{"contents": "read", "pull_requests": "write", "issues": "write", "checks": "read"}
	default:
		return map[string]string{"contents": "read", "pull_requests": "read", "issues": "read", "checks": "read"}
	}
}

// gitConnected tells whether git can be lent anything: Eriphe's GitHub App
// is there.
func (c *core) gitConnected() bool {
	_, err := loadGitHubApp()
	return err == nil
}

// mintGitHub makes the credential: an installation token of the GitHub
// App for that one repository and those rights.
func (c *core) mintGitHub(ctx context.Context, taskID, repo, access string, api bool) (minted, error) {
	app, err := loadGitHubApp()
	if err == nil {
		perms := githubPermissions(access, api)
		key := fmt.Sprintf("%s|%s|%v", taskID, strings.ToLower(repo), perms)
		if m, ok := c.tokens.get(key, time.Now()); ok {
			return m, nil
		}
		owner, name, _ := strings.Cut(repo, "/")
		inst, err := c.github.installationFor(ctx, app, owner, name)
		if err != nil {
			return minted{}, err
		}
		tok, exp, err := c.github.installationToken(ctx, app, inst, []string{name}, perms)
		if err != nil {
			return minted{}, err
		}
		m := minted{Username: "x-access-token", Password: tok, Expires: exp, Source: sourceApp}
		c.tokens.put(key, m)
		return m, nil
	}
	if err == errNoGitHubApp {
		return minted{}, fmt.Errorf("GitHub is not connected for git: create Eriphe's GitHub App in Settings → Tools → GitHub")
	}
	return minted{}, err
}
