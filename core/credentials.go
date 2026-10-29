package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Credentials on demand. A task asks for the access it needs when it needs
// it, through the standard extension points of its tools: eriphe-cred is
// git's credential helper and gh's wrapper in every task (node/cred). It
// asks the task's node, which asks eriphe-core:
//
//	POST /v1/node/tasks/{id}/credential   {protocol, host, path, access, tool}   (node secret)
//
// eriphe-core decides, deny by default (decideCredential): the repository
// must be lent to the task, as a repo resource (resources.go;
// policy.credentials adds or removes some), with enough access. A write
// needs the user's approval the first time (a permission only the user
// answers, shown in the workspace like any other), unless the user's own
// turn lent the repository for write on a declared branch, and whenever
// the task read external content. Then it mints a short-lived credential
// for that one repository and those rights only: a GitHub App installation
// token, one hour (github_app.go). Without the app, the GitHub tool's own
// token, long-lived and not limited to the repository: the user approves
// it first. Every request is logged (credential_log) and shown in the
// task's details.
//
// A task from before resources has none kept: its repository is the one it
// was asked to work on (spec.Repo), lent for read, and the service is lent
// when its grant names the GitHub tool (githubLent), as it was then.

// credentialTool is the permission a credential asks for; only the user
// may answer it.
const credentialTool = "Credential"

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

// credRequest is what a task's tool asks for.
type credRequest struct {
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host"`
	Path     string `json:"path,omitempty"`
	Access   string `json:"access"`
	Tool     string `json:"tool,omitempty"` // git or gh
}

// credAnswer is eriphe-core's answer to the node.
type credAnswer struct {
	Decision   string         `json:"decision"` // allow, deny, ask, or none: no service for this host
	Reason     string         `json:"reason,omitempty"`
	Username   string         `json:"username,omitempty"`
	Password   string         `json:"password,omitempty"`
	Expires    time.Time      `json:"expires,omitzero"`
	Permission map[string]any `json:"permission,omitempty"` // with ask: the input of the permission the node asks
}

// CredentialRule is a line of policy.credentials, first match wins: a host
// and a repository (globs, any when empty), read or write (both when
// empty), and the decision. It replaces the default (the task's own
// repository, a write approved once): allow lends without asking (a write
// still asks on a tainted task), ask always asks the first time, deny refuses.
type CredentialRule struct {
	Host     string `json:"host,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Access   string `json:"access,omitempty"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func checkCredentialRules(rules []CredentialRule) error {
	for i, r := range rules {
		if r.Decision != decideAllow && r.Decision != decideDeny && r.Decision != decideAsk {
			return fmt.Errorf("policy credential rule %d: decision must be allow, deny or ask", i+1)
		}
		if r.Access != "" && r.Access != accessRead && r.Access != accessWrite {
			return fmt.Errorf("policy credential rule %d: access must be read or write", i+1)
		}
		for _, g := range []string{r.Host, r.Repo} {
			if _, err := path.Match(g, ""); err != nil {
				return fmt.Errorf("policy credential rule %d: bad glob %q", i+1, g)
			}
		}
	}
	return nil
}

func (r CredentialRule) matches(host, repo, access string) bool {
	glob := func(g, s string) bool {
		if g == "" {
			return true
		}
		ok, _ := path.Match(strings.ToLower(g), strings.ToLower(s))
		return ok
	}
	return glob(r.Host, host) && glob(r.Repo, repo) && (r.Access == "" || r.Access == access)
}

func (p *Policy) credentialRules() []CredentialRule {
	if p == nil {
		return nil
	}
	return p.Credentials
}

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
	Kind    string      // worker or brain
	Repos   []repoGrant // the GitHub repositories lent to it
	Lent    bool        // the service is lent to it
	Tainted []string    // why it may have been talked into something, if it may
	// Fallback: there is no GitHub App, so what would be lent is the
	// GitHub tool's own token, long-lived and good for every repository.
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

// credDecision is the policy's answer: allow, deny, ask, or none.
type credDecision struct {
	Decision, Reason, Service, Host, Repo string
}

// decideCredential applies the rules to a request, deny by default.
func decideCredential(p *Policy, req credRequest, f credFacts) credDecision {
	service, host := credService(req.Host)
	d := credDecision{Service: service, Host: host, Repo: githubRepo(req.Path)}
	if service == "" {
		d.Decision, d.Reason, d.Host = "none", "no credential service for "+req.Host, req.Host
		return d
	}
	deny := func(why string) credDecision { d.Decision, d.Reason = decideDeny, why; return d }
	switch {
	case req.Access != accessRead && req.Access != accessWrite:
		return deny("access must be read or write")
	case req.Protocol != "" && req.Protocol != "https":
		return deny("only https")
	case d.Repo == "":
		return deny("which repository? git must name it (credential.useHttpPath=true)")
	case f.Kind == "brain":
		return deny("the brain works through its tools, not with git")
	case !f.Lent:
		return deny("no GitHub repository is lent to this task: lend it one (run_task's resources, {kind: repo})")
	}
	ask := false
	d.Reason = "the task's own repository"
	rule := -1
	for i, r := range p.credentialRules() {
		if r.matches(host, d.Repo, req.Access) {
			rule = i
			break
		}
	}
	if rule >= 0 {
		r := p.credentialRules()[rule]
		switch r.Decision {
		case decideDeny:
			return deny(orStr(r.Reason, "refused by the policy"))
		case decideAsk:
			ask, d.Reason = true, orStr(r.Reason, "the policy asks for "+req.Access+" access to "+d.Repo)
		default:
			d.Reason = orStr(r.Reason, "allowed by the policy")
		}
	} else {
		g := covering(f.Repos, d.Repo)
		switch {
		case g == nil:
			return deny(d.Repo + " is not the repository this task was asked to work on")
		case req.Access != accessWrite:
		case g.Access == accessWrite && g.Branch != "" && orStr(req.Tool, "git") == "git":
			// Lent for write on a branch by my own (untainted) turn: that was
			// my approval of the first push. A push elsewhere asks (policy.go).
			d.Reason = "lent for write to " + d.Repo + " on " + g.Branch
		case g.Access == accessWrite:
			ask, d.Reason = true, "first push to "+d.Repo+" in this task"
		default:
			ask, d.Reason = true, "first push to "+d.Repo+" in this task (lent for read)"
		}
	}
	if req.Access == accessWrite && !ask && len(f.Tainted) > 0 {
		ask, d.Reason = true, "the task read external content ("+strings.Join(f.Tainted, ", ")+")"
	}
	if f.Fallback {
		why := "no GitHub App: the GitHub tool's own long-lived token, not limited to " + d.Repo
		if ask {
			d.Reason += "; " + why
		} else {
			ask, d.Reason = true, why
		}
	}
	if ask {
		if f.Granted == nil || !f.Granted(d.Repo, req.Access) {
			d.Decision = decideAsk
			return d
		}
		d.Reason = "approved by the user earlier in this task"
	}
	d.Decision = decideAllow
	return d
}

func (c *core) handleTaskCredential(w http.ResponseWriter, r *http.Request) {
	node, ok := c.nodeFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, err := c.store.task(r.PathValue("id"))
	if err != nil || t.Node != node || finished(t.State) {
		http.Error(w, "no such task on this node", http.StatusNotFound)
		return
	}
	var req credRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, c.credential(r.Context(), t, req))
}

// credential decides a request, mints the credential if allowed, and logs it.
func (c *core) credential(ctx context.Context, t *TaskRow, req credRequest) credAnswer {
	tool := orStr(req.Tool, "git")
	facts := credFacts{Kind: t.Kind, Tainted: c.taskTaint(t),
		Granted: func(repo, access string) bool { return c.store.credentialGranted(t.ID, "github", repo, access) }}
	facts.Repos, facts.Lent = c.taskRepos(t)
	_, appErr := loadGitHubApp()
	facts.Fallback = appErr == errNoGitHubApp
	d := decideCredential(c.conf().Policy, req, facts)
	if d.Decision == decideAsk && facts.Fallback {
		if _, err := c.githubToolToken(); err != nil {
			d.Decision, d.Reason = decideDeny, "GitHub is not connected for git: create Eriphe's GitHub App in Settings → Tools → GitHub"
		}
	}
	a := credAnswer{Decision: d.Decision, Reason: d.Reason}
	source := ""
	switch d.Decision {
	case decideAsk:
		a.Permission = map[string]any{"host": d.Host, "repo": d.Repo, "access": req.Access, "tool": tool, "reason": d.Reason}
	case decideAllow:
		m, err := c.mintGitHub(ctx, t.ID, d.Repo, req.Access, tool == "gh")
		if err != nil {
			a = credAnswer{Decision: decideDeny, Reason: "cannot give a credential: " + err.Error()}
			source = "error"
			break
		}
		a.Username, a.Password, a.Expires, source = m.Username, m.Password, m.Expires, m.Source
		if m.Source == sourceToolToken {
			a.Reason = "fallback: the GitHub tool's own token (no GitHub App): long-lived and not limited to " + d.Repo
		}
	}
	c.store.logCredential(t.ID, tool, d.Host, d.Repo, req.Access, a.Decision, byPolicy, source, a.Reason)
	log.Printf("credential: task %s %s %s %s/%s: %s (%s)%s", t.ID, tool, req.Access, d.Host, d.Repo, a.Decision, a.Reason,
		map[bool]string{true: " WARNING: long-lived token lent", false: ""}[source == sourceToolToken])
	return a
}

// taskRepos are the GitHub repositories lent to a task, and whether the
// service is: its repo resources; for a task from before resources, the
// repository it was asked to work on, for read, lent with the GitHub tool.
func (c *core) taskRepos(t *TaskRow) ([]repoGrant, bool) {
	var out []repoGrant
	if t.Resources != nil {
		for _, e := range t.Resources {
			if r := githubRepo(e.Ref); e.Kind == resRepo && r != "" {
				out = append(out, repoGrant{Repo: r, Access: e.Access, Branch: e.Branch})
			}
		}
		return out, len(out) > 0
	}
	spec, _ := c.store.taskSpec(t.ID)
	if r := githubRepo(spec.Repo); r != "" {
		out = append(out, repoGrant{Repo: r, Access: accessRead, Branch: spec.Branch})
	}
	return out, c.githubLent(t)
}

// githubLent tells whether the GitHub service is lent to a task: its grant
// names a GitHub catalog server, whole or some of its tools.
func (c *core) githubLent(t *TaskRow) bool {
	servers := grantServers(c.taskGrant(t))
	for name, s := range c.conf().Servers {
		if isGitHubServer(name, s) && slices.Contains(servers, name) {
			return true
		}
	}
	return false
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

// taskTaint is why a task may have been talked into something: it was
// started on a tainted turn of the brain, or it called an external catalog
// tool, fetched a page or searched the web.
func (c *core) taskTaint(t *TaskRow) []string {
	var out []string
	if why := c.store.get(taskTaintKey(t.ID)); why != "" {
		out = append(out, why)
	}
	trusted := c.conf().memoryConfig().TrustedServers
	for after := 0; ; {
		page, err := c.store.eventsAfter(t.ID, after, 500)
		if err != nil {
			return append(out, "events.unreadable")
		}
		for _, e := range page {
			if e.Type != "tool_use" {
				continue
			}
			name, _ := e.Data["name"].(string)
			why := ""
			switch {
			case strings.HasPrefix(name, "mcp__eriphe"):
			case strings.HasPrefix(name, "mcp__"):
				server, _, _ := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
				if !containsStr(trusted, server) {
					why = name
				}
			case name == "WebFetch" || name == "WebSearch":
				why = name
			}
			if why != "" && !containsStr(out, why) {
				out = append(out, why)
			}
		}
		if len(page) < 500 {
			return out
		}
		after = page[len(page)-1].Seq
	}
}

// taskTaintKey marks a task started on a tainted turn of the brain.
func taskTaintKey(id string) string { return "task_taint:" + id }

// credentialAnswered is the policy's hook when the user answers a
// credential's permission: an approval is kept for the rest of the task.
func (s *Store) credentialAnswered(taskID string, input map[string]any, decision, by, reason string) {
	repo, _ := input["repo"].(string)
	access, _ := input["access"].(string)
	host, _ := input["host"].(string)
	tool, _ := input["tool"].(string)
	if decision == decideAllow && by == byUser && repo != "" && (access == accessRead || access == accessWrite) {
		s.db.Exec(`INSERT OR IGNORE INTO credential_grants (task_id, service, repo, access, time) VALUES (?, 'github', ?, ?, ?)`,
			taskID, strings.ToLower(repo), access, time.Now().Unix())
	}
	s.logCredential(taskID, orStr(tool, "git"), host, repo, access, decision, by, "", reason)
}

// credentialGranted: the user approved this access, or a write, to the
// repository in this task.
func (s *Store) credentialGranted(taskID, service, repo, access string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM credential_grants WHERE task_id = ? AND service = ? AND repo = ? AND (access = ? OR access = 'write')`,
		taskID, service, strings.ToLower(repo), access).Scan(&n)
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
