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
	worker := credFacts{Kind: "worker", Repo: "owner/repo", Lent: true}
	rules := &Policy{Credentials: []CredentialRule{
		{Repo: "owner/docs", Access: accessRead, Decision: decideAllow},
		{Repo: "owner/site", Decision: decideAllow, Reason: "the site is Eriphe's to publish"},
		{Repo: "owner/secret-*", Decision: decideDeny, Reason: "never"},
		{Host: "github.com", Repo: "owner/infra", Access: accessRead, Decision: decideAsk},
	}}
	cases := []struct {
		name   string
		policy *Policy
		req    credRequest
		facts  credFacts
		want   string
		reason string
	}{
		{"read of the task's repo", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"}, worker, decideAllow, "own repository"},
		{"case does not matter", nil, credRequest{Host: "github.com", Path: "Owner/Repo.git", Access: "read"}, worker, decideAllow, ""},
		{"first push asks", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"}, worker, decideAsk, "first push"},
		{"approved push", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"},
			credFacts{Kind: "worker", Repo: "owner/repo", Lent: true, Granted: granted("owner/repo@write")}, decideAllow, "approved"},
		{"a read approval is not a write", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"},
			credFacts{Kind: "worker", Repo: "owner/repo", Lent: true, Granted: granted("owner/repo@read")}, decideAsk, ""},
		{"gh on the API host", nil, credRequest{Host: "api.github.com", Path: "owner/repo", Access: "read", Tool: "gh"}, worker, decideAllow, ""},
		{"another repo", nil, credRequest{Host: "github.com", Path: "owner/other.git", Access: "read"}, worker, decideDeny, "not the repository"},
		{"no repo for the task", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "worker", Lent: true}, decideDeny, "not the repository"},
		{"not lent", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "worker", Repo: "owner/repo"}, decideDeny, "not lent"},
		{"the brain", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "brain", Repo: "owner/repo", Lent: true}, decideDeny, "brain"},
		{"no path", nil, credRequest{Host: "github.com", Access: "read"}, worker, decideDeny, "useHttpPath"},
		{"bad access", nil, credRequest{Host: "github.com", Path: "owner/repo", Access: "admin"}, worker, decideDeny, "read or write"},
		{"not https", nil, credRequest{Protocol: "http", Host: "github.com", Path: "owner/repo", Access: "read"}, worker, decideDeny, "https"},
		{"unknown host", nil, credRequest{Host: "gitlab.com", Path: "owner/repo", Access: "read"}, worker, "none", "gitlab.com"},
		{"tainted read", nil, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"},
			credFacts{Kind: "worker", Repo: "owner/repo", Lent: true, Tainted: []string{"WebFetch"}}, decideAllow, ""},
		{"rule allows a read elsewhere", rules, credRequest{Host: "github.com", Path: "owner/docs.git", Access: "read"}, worker, decideAllow, "policy"},
		{"that rule is for reads only", rules, credRequest{Host: "github.com", Path: "owner/docs.git", Access: "write"}, worker, decideDeny, "not the repository"},
		{"rule allows a write", rules, credRequest{Host: "github.com", Path: "owner/site.git", Access: "write"}, worker, decideAllow, "publish"},
		{"tainted write asks despite the rule", rules, credRequest{Host: "github.com", Path: "owner/site.git", Access: "write"},
			credFacts{Kind: "worker", Repo: "owner/repo", Lent: true, Tainted: []string{"mcp__mail__read"}}, decideAsk, "external content"},
		{"rule denies", rules, credRequest{Host: "github.com", Path: "owner/secret-keys.git", Access: "read"}, worker, decideDeny, "never"},
		{"rule asks for a read", rules, credRequest{Host: "github.com", Path: "owner/infra.git", Access: "read"}, worker, decideAsk, "policy asks"},
		{"rules still need the service lent", rules, credRequest{Host: "github.com", Path: "owner/docs.git", Access: "read"},
			credFacts{Kind: "worker", Repo: "owner/repo"}, decideDeny, "not lent"},
	}
	for _, c := range cases {
		d := decideCredential(c.policy, c.req, c.facts)
		if d.Decision != c.want || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: %s (%s), want %s (%q)", c.name, d.Decision, d.Reason, c.want, c.reason)
		}
	}
}

func TestCheckCredentialRules(t *testing.T) {
	for _, bad := range [][]CredentialRule{
		{{Repo: "a/b", Decision: "maybe"}},
		{{Repo: "a/b", Access: "admin", Decision: decideAllow}},
		{{Repo: "a/[", Decision: decideAllow}},
	} {
		if (&Policy{Credentials: bad}).compile() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := (&Policy{Credentials: []CredentialRule{{Repo: "me/*", Access: "write", Decision: "allow"}}}).compile(); err != nil {
		t.Fatal(err)
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

func TestCredentialFallbackToolToken(t *testing.T) {
	c, task := credCore(t, "github.get_*")
	a := c.credential(context.Background(), task, credRequest{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: "read", Tool: "git"})
	if a.Decision != decideAllow || a.Password != "github_pat_long_lived" || a.Username != "x-access-token" || !strings.Contains(a.Reason, "fallback") {
		t.Fatalf("fallback: %+v", a)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	if len(log) != 1 || !log[0].Fallback() || log[0].Repo != "owner/repo" || log[0].Decision != decideAllow {
		t.Fatalf("log %+v", log)
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

// A first push asks the user through the permission flow; the brain cannot
// answer it; once the user allows it, the task gets its token for the rest
// of its life.
func TestCredentialApprovalFlow(t *testing.T) {
	c, task := credCore(t, "github")
	ctx := context.Background()
	push := credRequest{Protocol: "https", Host: "github.com", Path: "owner/repo.git", Access: "write", Tool: "git"}
	a := c.credential(ctx, task, push)
	if a.Decision != decideAsk || a.Permission["repo"] != "owner/repo" || a.Permission["access"] != "write" || a.Password != "" {
		t.Fatalf("first push: %+v", a)
	}
	// The node asks with that input, as a permission of the task.
	c.store.updateTask(task.ID, map[string]any{"state": stateWaitingPermission})
	ev := []EventRow{{Seq: 1, Type: "permission", Data: map[string]any{"id": "p-1", "tool_name": credentialTool, "input": a.Permission}}}
	c.store.addEvents(task.ID, ev)
	if !c.applyPolicy(task.ID, ev) {
		t.Fatal("the policy answered a credential itself")
	}
	if why := c.store.userOnly(task.ID, "p-1"); !strings.Contains(why, "first push") {
		t.Fatalf("not the user's: %q", why)
	}
	allow := true
	if _, err := c.answer(ctx, task.ID, "p-1", "", &allow, byBrain); err == nil {
		t.Fatal("the brain approved a push")
	}
	if c.store.credentialGranted(task.ID, "github", "owner/repo", accessWrite) {
		t.Fatal("granted before the user answered")
	}
	if _, err := c.answer(ctx, task.ID, "p-1", "", &allow, byUser); err != nil {
		t.Fatal(err)
	}
	a = c.credential(ctx, task, push)
	if a.Decision != decideAllow || a.Password == "" {
		t.Fatalf("after approval: %+v", a)
	}
	// A write approval covers reads too; another repository is still refused.
	if !c.store.credentialGranted(task.ID, "github", "Owner/Repo", accessRead) {
		t.Fatal("a write approval does not cover a read")
	}
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/other.git", Access: "write"}); a.Decision != decideDeny {
		t.Fatalf("other repo: %+v", a)
	}
	log, _ := c.store.credentialLog(task.ID, 10)
	var seq []string
	for i := len(log) - 1; i >= 0; i-- {
		seq = append(seq, log[i].Decision+"/"+log[i].By)
	}
	if strings.Join(seq, " ") != "ask/policy allow/user allow/policy deny/policy" {
		t.Fatalf("log: %v", seq)
	}
	// What the approval card shows.
	if got := subject(credentialTool, map[string]any{"host": "github.com", "repo": "owner/repo", "access": "write"}); got != "write github.com/owner/repo" {
		t.Fatalf("subject %q", got)
	}
}

// A task that read external content asks before writing, even where a
// rule allows it; and a task started on a tainted turn is tainted.
func TestCredentialTaintedTask(t *testing.T) {
	c, task := credCore(t, "github")
	c.base.Policy = &Policy{Credentials: []CredentialRule{{Repo: "owner/repo", Decision: decideAllow}}}
	c.snap.Load().Policy = c.base.Policy
	ctx := context.Background()
	push := credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write"}
	if a := c.credential(ctx, task, push); a.Decision != decideAllow {
		t.Fatalf("clean task, rule allows: %+v", a)
	}
	c.store.addEvents(task.ID, []EventRow{{Seq: 1, Type: "tool_use", Data: map[string]any{"name": "WebFetch", "input": map[string]any{"url": "https://example.com"}}}})
	if a := c.credential(ctx, task, push); a.Decision != decideAsk || !strings.Contains(a.Reason, "WebFetch") {
		t.Fatalf("tainted: %+v", a)
	}
	if got := c.taskTaint(&TaskRow{ID: "t-2"}); len(got) != 0 {
		t.Fatalf("clean: %v", got)
	}
	c.store.set(taskTaintKey("t-2"), "started on a tainted turn: mcp__mail__read")
	if got := c.taskTaint(&TaskRow{ID: "t-2"}); len(got) != 1 {
		t.Fatalf("launch taint: %v", got)
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

// The task's details show what it asked for, and the fallback's warning.
func TestCredentialsInTaskDetails(t *testing.T) {
	c, task := credCore(t, "github")
	c.credential(context.Background(), task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read", Tool: "git"})
	c.credential(context.Background(), task, credRequest{Host: "github.com", Path: "owner/other.git", Access: "read", Tool: "git"})
	tw := newTestWebFor(t, c)
	tw.setup()
	code, body, _ := tw.do("GET", "/tasks/"+task.ID, nil, nil)
	if code != 200 || !strings.Contains(body, "Accès demandés") || !strings.Contains(body, "read github.com/owner/other") ||
		!strings.Contains(body, "refusé") || !strings.Contains(body, "Jeton durable") {
		t.Fatalf("details: %d %s", code, body)
	}
}
