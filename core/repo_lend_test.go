package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A use of a repository is held to the lend and to the repository's case:
// never is refused, free goes on the user's own start, approval (or free on
// another start) needs the yes of the start's card, for that case only.
func TestDecideRepo(t *testing.T) {
	granted := func(repos ...string) func(string, string) bool {
		return func(repo, cat string) bool { return slices.Contains(repos, repo+"@"+cat) }
	}
	cases := func(change, delete, send string) func(string, string) string {
		return func(_, cat string) string {
			return map[string]string{catChange: change, catDelete: delete, catSend: send}[cat]
		}
	}
	read := []repoGrant{{Repo: "owner/repo", Access: accessRead}}
	write := []repoGrant{{Repo: "owner/repo", Access: accessWrite}}
	free := cases(gridFree, gridApproval, gridNever)
	for _, c := range []struct {
		name           string
		repo, category string
		facts          repoFacts
		want, reason   string
	}{
		{"a read of its repository", "owner/repo", catRead, repoFacts{Repos: read}, decideAllow, "own repository"},
		{"case does not matter", "Owner/Repo", catRead, repoFacts{Repos: read}, decideAllow, ""},
		{"lent for read takes no push", "owner/repo", catChange, repoFacts{Repos: read, Case: free}, decideDeny, "for read"},
		{"not even with a yes", "owner/repo", catChange, repoFacts{Repos: read, Case: free, Granted: granted("owner/repo@change")}, decideDeny, "for read"},
		{"free on a clean start", "owner/repo", catChange, repoFacts{Repos: write, Case: free, Clean: true}, decideAllow, "free"},
		{"free, not the user's own start", "owner/repo", catChange, repoFacts{Repos: write, Case: free}, decideAsk, "not the user's own"},
		{"on approval, even clean", "owner/repo", catDelete, repoFacts{Repos: write, Case: free, Clean: true}, decideAsk, "on approval"},
		{"approved", "owner/repo", catDelete, repoFacts{Repos: write, Case: free, Granted: granted("owner/repo@delete")}, decideAllow, "approved"},
		{"another case's yes is not this one's", "owner/repo", catDelete, repoFacts{Repos: write, Case: free, Granted: granted("owner/repo@change")}, decideAsk, ""},
		{"never, even approved", "owner/repo", catSend, repoFacts{Repos: write, Case: free, Clean: true, Granted: granted("owner/repo@send")}, decideDeny, "never"},
		{"no rules: never", "owner/repo", catChange, repoFacts{Repos: write, Clean: true}, decideDeny, "never"},
		{"another repository", "owner/other", catRead, repoFacts{Repos: read}, decideDeny, "not the repository"},
		{"nothing lent", "owner/repo", catRead, repoFacts{}, decideDeny, "not the repository"},
	} {
		d := decideRepo(c.repo, c.category, c.facts)
		if d.Decision != c.want || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: %s (%s), want %s (%q)", c.name, d.Decision, d.Reason, c.want, c.reason)
		}
	}
}

func TestGitHubRepo(t *testing.T) {
	for in, want := range map[string]string{
		"owner/repo":                        "owner/repo",
		"owner/repo.git":                    "owner/repo",
		"/owner/repo.git/":                  "owner/repo",
		"https://github.com/owner/repo.git": "owner/repo",
		"git@github.com:owner/repo.git":     "owner/repo",
		"https://gitlab.com/owner/repo":     "",
		"owner/repo/extra":                  "",
		"owner":                             "",
		"own er/repo":                       "",
	} {
		if got := repoOf(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// credCore is a core with a vault, a GitHub tool on a token, and a worker
// task on owner/repo lent it.
func credCore(t *testing.T, tools string) (*core, *TaskRow) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := vaultKey(filepath.Join(dir, "vault.key"))
	v, err := openVault(store, key)
	if err != nil {
		t.Fatal(err)
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	v.set("server.github.token", "github_pat_long_lived")
	cfg := &Config{User: "Sam",
		Servers: map[string]Server{"github": {URL: "https://api.githubcopilot.com/mcp/",
			Auth: &ServerAuth{BearerFile: "vault:server.github.token"}, Use: "GitHub"}}}
	c := newCore(cfg, store)
	task := &TaskRow{ID: "t-1", Node: "pc", Agent: "sonnet", Runtime: "claude-code", Kind: "worker", Tools: tools,
		Dir: "owner/repo", Prompt: "fix it", State: stateRunning,
		Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessRead}}}
	if err := store.insertTask(task); err != nil {
		t.Fatal(err)
	}
	store.setSpec(task.ID, nodeTaskSpec{Runtime: "claude-code", Repo: "owner/repo", Branch: "eriphe/fix"})
	return c, task
}

// Without the GitHub App, git gets nothing: the GitHub tool's own token is
// never lent.
func TestMintNeedsTheApp(t *testing.T) {
	c, task := credCore(t, "github.get_*")
	if _, err := c.mintGitHub(context.Background(), task.ID, "owner/repo", accessRead, false); err == nil || !strings.Contains(err.Error(), "GitHub App") {
		t.Fatalf("without the app: %v", err)
	}
}

func TestTokenCache(t *testing.T) {
	var cc tokenCache
	now := time.Now()
	cc.put("k", minted{Password: "a", Expires: now.Add(time.Hour)})
	if m, ok := cc.get("k", now); !ok || m.Password != "a" {
		t.Fatal("not cached")
	}
	if _, ok := cc.get("k", now.Add(55*time.Minute)); ok {
		t.Fatal("a token about to end was reused")
	}
}

// The task's details show what it asked for, and which token it was.
func TestCredentialsInTaskDetails(t *testing.T) {
	c, task := credCore(t, "github")
	c.store.logAccess(task.ID, gitLogTool, "github.com", "owner/repo", accessRead, decideAllow, byPolicy, sourceApp, "")
	c.store.logAccess(task.ID, gitLogTool, "github.com", "owner/other", accessRead, decideDeny, byPolicy, "", "not lent")
	tw := newTestWebFor(t, c)
	tw.setup()
	code, body, _ := tw.do("GET", "/tasks/"+task.ID, nil, nil)
	if code != 200 || !strings.Contains(body, "Accès demandés") || !strings.Contains(body, "read github.com/owner/other") ||
		!strings.Contains(body, "refusé") {
		t.Fatalf("details: %d %s", code, body)
	}
}
