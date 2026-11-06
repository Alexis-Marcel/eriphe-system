package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecideCredential(t *testing.T) {
	granted := func(repos ...string) func(string, string) bool {
		return func(repo, access string) bool {
			for _, r := range repos {
				if strings.EqualFold(r, repo+"@"+access) {
					return true
				}
			}
			return false
		}
	}
	own := []repoGrant{{Repo: "owner/repo", Access: accessRead}}
	write := []repoGrant{{Repo: "owner/repo", Access: accessWrite}}
	worker := credFacts{Kind: "worker", Repos: own, Lent: true}
	cases := []struct {
		name   string
		req    credRequest
		facts  credFacts
		want   string
		reason string
	}{
		{"read of the task's repo", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"}, worker, decideAllow, "own repository"},
		{"case does not matter", credRequest{Host: "github.com", Path: "Owner/Repo.git", Access: "read"}, worker, decideAllow, ""},
		{"a repository lent for read takes no push", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"}, worker, decideDeny, "lent to this task for read"},
		{"not even with a yes", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"},
			credFacts{Kind: "worker", Repos: own, Lent: true, Granted: granted("owner/repo@write")}, decideDeny, "for read"},
		{"approved push", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"},
			credFacts{Kind: "worker", Repos: write, Lent: true, Granted: granted("owner/repo@write")}, decideAllow, "approved"},
		{"a read approval is not a write", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"},
			credFacts{Kind: "worker", Repos: write, Lent: true, Granted: granted("owner/repo@read")}, decideAsk, ""},
		{"gh on the API host", credRequest{Host: "api.github.com", Path: "owner/repo", Access: "read", Tool: "gh"}, worker, decideAllow, ""},
		{"another repo", credRequest{Host: "github.com", Path: "owner/other.git", Access: "read"}, worker, decideDeny, "not the repository"},
		{"no repo for the task", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "worker", Lent: true}, decideDeny, "not the repository"},
		{"not lent", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "worker", Repos: own}, decideDeny, "no GitHub repository"},
		{"the brain", credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "brain", Repos: own, Lent: true}, decideDeny, "brain"},
		{"no path", credRequest{Host: "github.com", Access: "read"}, worker, decideDeny, "useHttpPath"},
		{"bad access", credRequest{Host: "github.com", Path: "owner/repo", Access: "admin"}, worker, decideDeny, "read or write"},
		{"not https", credRequest{Protocol: "http", Host: "github.com", Path: "owner/repo", Access: "read"}, worker, decideDeny, "https"},
		{"unknown host", credRequest{Host: "gitlab.com", Path: "owner/repo", Access: "read"}, worker, "none", "gitlab.com"},
	}
	for _, c := range cases {
		d := decideCredential(c.req, c.facts)
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
		if got := githubRepo(in); got != want {
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
		Dir: "owner/repo", Prompt: "fix it", State: stateRunning}
	if err := store.insertTask(task); err != nil {
		t.Fatal(err)
	}
	store.setSpec(task.ID, nodeTaskSpec{Runtime: "claude-code", Repo: "owner/repo", Branch: "eriphe/fix"})
	return c, task
}

// Without the GitHub App the rules are the same: the tool's token stays in
// eriphe-core, so nothing more is asked, and the log says which token it was.
func TestCredentialFallbackToolToken(t *testing.T) {
	c, task := credCore(t, "github.get_*")
	read := credRequest{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: "read", Tool: "git"}
	a := c.credential(context.Background(), task, read)
	if a.Decision != decideAllow || a.Password != gitPlaceholder(task.ID) || a.Username != "x-access-token" || !strings.Contains(a.Reason, "fallback") {
		t.Fatalf("fallback: %+v", a)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) != 1 || !log[0].Fallback() || log[0].Repo != "owner/repo" || log[0].Decision != decideAllow {
		t.Fatalf("log %+v", log)
	}
	// With no token to fall back on either, it is refused.
	currentVault().remove("server.github.token")
	if a := c.credential(context.Background(), task, read); a.Decision != decideDeny || !strings.Contains(a.Reason, "GitHub App") {
		t.Fatalf("nothing to lend: %+v", a)
	}
}

func TestCredentialNotLent(t *testing.T) {
	c, task := credCore(t, "")
	a := c.credential(context.Background(), task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"})
	if a.Decision != decideDeny || a.Password != "" {
		t.Fatalf("not lent: %+v", a)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) != 1 || log[0].Decision != decideDeny || log[0].Source != "" {
		t.Fatalf("log %+v", log)
	}
}

// Nothing waits in a command: a write the user did not approve at launch is
// refused at once, and once approved it holds, whatever the task reads next.
func TestCredentialNeverWaits(t *testing.T) {
	c, task := credCore(t, "github")
	withApp(t, c, newFakeGitHub(t))
	task.Resources = []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite}}
	ctx := context.Background()
	push := credRequest{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: "write", Tool: "git"}
	a := c.credential(ctx, task, push)
	if a.Decision != decideDeny || a.Password != "" || !strings.Contains(a.Reason, "user's approval") {
		t.Fatalf("not approved: %+v", a)
	}
	c.store.grantCredential(task.ID, "owner/repo", accessWrite)
	if a := c.credential(ctx, task, push); a.Decision != decideAllow {
		t.Fatalf("approved: %+v", a)
	}
	if !c.store.credentialGranted(task.ID, "Owner/Repo", accessRead) {
		t.Fatal("a write approval does not cover a read")
	}
	c.store.addEvents(task.ID, []EventRow{{Seq: 1, Type: "tool_use", Data: map[string]any{"name": "WebFetch", "input": map[string]any{"url": "https://example.com"}}}})
	if a := c.credential(ctx, task, push); a.Decision != decideAllow {
		t.Fatalf("after reading a page: %+v", a)
	}
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/other.git", Access: "write"}); a.Decision != decideDeny {
		t.Fatalf("other repo: %+v", a)
	}
}

func TestCredCache(t *testing.T) {
	var cc credCache
	now := time.Now()
	cc.put("k", minted{Password: "a", Expires: now.Add(time.Hour)})
	if m, ok := cc.get("k", now); !ok || m.Password != "a" {
		t.Fatal("not cached")
	}
	if _, ok := cc.get("k", now.Add(55*time.Minute)); ok {
		t.Fatal("a token about to end was reused")
	}
}

func TestCredentialEndpointNeedsTheNode(t *testing.T) {
	c, task := credCore(t, "github")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/node/tasks/"+task.ID+"/credential", strings.NewReader(`{"host":"github.com","path":"owner/repo","access":"read"}`))
	req.SetPathValue("id", task.ID)
	c.handleTaskCredential(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without the node's secret: %d", rec.Code)
	}
}

// The task's details show what it asked for, and which token it was.
func TestCredentialsInTaskDetails(t *testing.T) {
	c, task := credCore(t, "github")
	c.credential(context.Background(), task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read", Tool: "git"})
	c.credential(context.Background(), task, credRequest{Host: "github.com", Path: "owner/other.git", Access: "read", Tool: "git"})
	tw := newTestWebFor(t, c)
	tw.setup()
	code, body, _ := tw.do("GET", "/tasks/"+task.ID, nil, nil)
	if code != 200 || !strings.Contains(body, "Accès demandés") || !strings.Contains(body, "read github.com/owner/other") ||
		!strings.Contains(body, "refusé") || !strings.Contains(body, "outil GitHub (pas") {
		t.Fatalf("details: %d %s", code, body)
	}
}
