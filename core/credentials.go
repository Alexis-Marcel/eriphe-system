package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// A task and GitHub. A task lent a GitHub repository holds a placeholder; git
// and gh use it as a token, and each request crosses the node's relay to
// eriphe-core (git_access.go), which decides, deny by default (decideRepo):
// the repository must be lent to the task, as a repo resource (resources.go),
// with enough access. What is decided is the lend, at launch, as for any
// access (lent_access.go): a read needs nothing; a write on a declared branch
// needs a clean start (lend.go); anything else the user's yes on the start's
// card (task_start.go). What the task reads afterwards changes nothing, and
// nothing waits in a command: what is not lent is refused at once, with its
// reason. The token that goes in is short-lived, for that one repository and
// those rights only: a GitHub App installation token, one hour
// (github_app.go). Without the app, the GitHub tool's own token: it never
// leaves eriphe-core either, the same rules apply, and each request of git is
// checked against the repository lent. Every request is logged
// (credential_log) and shown in the task's details.

const (
	accessRead  = "read"
	accessWrite = "write"
)

const credentialSchema = `CREATE TABLE IF NOT EXISTS credential_log (
  time INTEGER NOT NULL, task_id TEXT NOT NULL, tool TEXT NOT NULL, host TEXT NOT NULL, repo TEXT NOT NULL,
  access TEXT NOT NULL, decision TEXT NOT NULL, by TEXT NOT NULL, source TEXT NOT NULL, reason TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS credential_log_task ON credential_log (task_id, time);
CREATE TABLE IF NOT EXISTS credential_grants (task_id TEXT NOT NULL, service TEXT NOT NULL, repo TEXT NOT NULL,
  access TEXT NOT NULL, time INTEGER NOT NULL, PRIMARY KEY (task_id, service, repo, access));`

func init() { extraSchema = append(extraSchema, credentialSchema) }

// credService is the service a host belongs to, and its canonical host
// (what rules and the log name); "" when eriphe-core has none for it.
func credService(host string) (service, canonical string) {
	switch strings.ToLower(strings.TrimSuffix(host, ":443")) {
	case "github.com", "api.github.com", "www.github.com":
		return "github", "github.com"
	}
	return "", ""
}

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// githubRepo reads owner/name from a path (owner/name.git), a GitHub URL
// (https://github.com/owner/name, git@github.com:owner/name.git) or owner/name.
func githubRepo(raw string) string {
	raw = strings.TrimSpace(raw)
	p := raw
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		if s, _ := credService(u.Hostname()); s != "github" {
			return ""
		}
		p = u.Path
	} else if host, rest, ok := strings.Cut(raw, ":"); ok && strings.Contains(host, "@") {
		_, h, _ := strings.Cut(host, "@")
		if s, _ := credService(h); s != "github" {
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

// credFacts is what the decision needs to know of the task.
type credFacts struct {
	Repos []repoGrant // the GitHub repositories lent to it
	Clean bool        // a clean start (lend.go)
	// Fallback: there is no GitHub App, so the token is the GitHub tool's
	// own, good for every repository: gh, whose requests do not always name
	// one, cannot use it.
	Fallback bool
	Granted  func(repo, access string) bool
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

// decideRepo applies the lend to a use of a repository, deny by default.
func decideRepo(repo, access string, f credFacts) repoDecision {
	g := covering(f.Repos, repo)
	switch {
	case g == nil:
		return repoDecision{decideDeny, repo + " is not the repository this task was asked to work on"}
	case access != accessWrite:
		return repoDecision{decideAllow, "the task's own repository"}
	case g.Access != accessWrite:
		return repoDecision{decideDeny, repo + " is lent to this task for read: it cannot push there"}
	case g.Branch != "" && f.Clean:
		return repoDecision{decideAllow, "lent for write to " + repo + " on " + g.Branch}
	case f.Granted != nil && f.Granted(repo, accessWrite):
		return repoDecision{decideAllow, "approved by the user at launch"}
	case g.Branch == "":
		return repoDecision{decideAsk, "a write to " + repo + " with no branch declared"}
	}
	return repoDecision{decideAsk, "a write to " + repo + " on a start that is not the user's own"}
}

// credentialFacts is what is known of a task when a use is decided.
func (c *core) credentialFacts(t *TaskRow) credFacts {
	facts := credFacts{Repos: lentRepos(t.Resources), Clean: c.store.get(lentByUserKey(t.ID)) != ""}
	facts.Granted = func(repo, access string) bool { return c.store.credentialGranted(t.ID, repo, access) }
	_, appErr := loadGitHubApp()
	facts.Fallback = appErr == errNoGitHubApp
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
		if r := githubRepo(e.Ref); e.Kind == resRepo && r != "" {
			out = append(out, repoGrant{Repo: r, Access: e.Access, Branch: e.Branch})
		}
	}
	return out
}

// isGitHubServer: GitHub's remote MCP server, one on the GitHub App, or the
// one named github.
func isGitHubServer(name string, s Server) bool {
	if s.Auth != nil && s.Auth.GitHubApp {
		return true
	}
	if u, err := url.Parse(s.URL); err == nil && strings.EqualFold(u.Hostname(), "api.githubcopilot.com") {
		return true
	}
	return name == "github"
}

// taskTaintKey marks a task started on a tainted turn of the brain.
func taskTaintKey(id string) string { return "task_taint:" + id }

// grantCredential keeps the user's yes on a start's card for the rest of the task.
func (s *Store) grantCredential(taskID, repo, access string) {
	s.db.Exec(`INSERT OR IGNORE INTO credential_grants (task_id, service, repo, access, time) VALUES (?, 'github', ?, ?, ?)`,
		taskID, strings.ToLower(repo), access, time.Now().Unix())
}

// credentialGranted: the user approved this access, or a write, to the
// repository in this task.
func (s *Store) credentialGranted(taskID, repo, access string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM credential_grants WHERE task_id = ? AND repo = ? AND (access = ? OR access = 'write')`,
		taskID, strings.ToLower(repo), access).Scan(&n)
	return n > 0
}

func (s *Store) logCredential(taskID, tool, host, repo, access, decision, by, source, reason string) {
	s.db.Exec(`INSERT INTO credential_log (time, task_id, tool, host, repo, access, decision, by, source, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, time.Now().Unix(), taskID, tool, host, repo, access, decision, by, source, reason)
}

// CredentialEntry is one line of a task's credential log.
type CredentialEntry struct {
	Time     time.Time `json:"time"`
	Tool     string    `json:"tool"`
	Host     string    `json:"host"`
	Repo     string    `json:"repo"`
	Access   string    `json:"access"`
	Decision string    `json:"decision"`
	By       string    `json:"by"`
	Source   string    `json:"source,omitempty"` // github-app or tool-token, when lent
	Reason   string    `json:"reason,omitempty"`
}

// Fallback: the long-lived token was lent.
func (e CredentialEntry) Fallback() bool { return e.Source == sourceToolToken }

func (s *Store) credentialLog(taskID string, limit int) ([]CredentialEntry, error) {
	rows, err := s.db.Query(`SELECT time, tool, host, repo, access, decision, by, source, reason FROM credential_log
		WHERE task_id = ? ORDER BY time DESC, rowid DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialEntry{}
	for rows.Next() {
		var e CredentialEntry
		var ts int64
		if err := rows.Scan(&ts, &e.Tool, &e.Host, &e.Repo, &e.Access, &e.Decision, &e.By, &e.Source, &e.Reason); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (c *core) handleTaskCredentials(w http.ResponseWriter, r *http.Request) {
	if _, err := c.store.task(r.PathValue("id")); err != nil {
		http.Error(w, "no such task", http.StatusNotFound)
		return
	}
	log, err := c.store.credentialLog(r.PathValue("id"), 200)
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
	sourceApp       = "github-app"
	sourceToolToken = "tool-token"
)

type minted struct {
	Username, Password string
	Expires            time.Time
	Source             string
}

// credCache keeps the tokens minted for a task, until ten minutes before
// they end: git asks more than once in one push.
type credCache struct {
	mu sync.Mutex
	m  map[string]minted
}

func (cc *credCache) get(key string, now time.Time) (minted, bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	m, ok := cc.m[key]
	if ok && m.Expires.Sub(now) < 10*time.Minute {
		delete(cc.m, key)
		ok = false
	}
	return m, ok
}

func (cc *credCache) put(key string, m minted) {
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

// mintGitHub makes the credential: an installation token of the GitHub
// App for that one repository and those rights, or else the GitHub tool's
// own token.
func (c *core) mintGitHub(ctx context.Context, taskID, repo, access string, api bool) (minted, error) {
	app, err := loadGitHubApp()
	if err == nil {
		perms := githubPermissions(access, api)
		key := fmt.Sprintf("%s|%s|%v", taskID, strings.ToLower(repo), perms)
		if m, ok := c.creds.get(key, time.Now()); ok {
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
		c.creds.put(key, m)
		return m, nil
	}
	if err != errNoGitHubApp {
		return minted{}, err
	}
	tok, err := c.githubToolToken()
	if err != nil {
		return minted{}, fmt.Errorf("GitHub is not connected for git: create Eriphe's GitHub App in Settings → Tools → GitHub")
	}
	return minted{Username: "x-access-token", Password: tok, Source: sourceToolToken}, nil
}

// githubToolToken is the bearer token of the GitHub catalog server, if it
// has one: the fallback without an app.
func (c *core) githubToolToken() (string, error) {
	conf := c.conf()
	names := make([]string, 0, len(conf.Servers))
	for name := range conf.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		s := conf.Servers[name]
		if isGitHubServer(name, s) && s.Auth != nil && s.Auth.BearerFile != "" {
			return readSecret(s.Auth.BearerFile)
		}
	}
	return "", errNoSecret
}
