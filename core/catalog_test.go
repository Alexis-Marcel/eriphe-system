package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// catalogCore is a core with one configuration server, provider and agent,
// an open vault, and a pool whose servers are in-memory MCP servers
// created on demand.
type catalogCore struct {
	*core
	mu    sync.Mutex
	dials map[string]int
}

func newDynamicCore(t *testing.T, mutate func(*Config)) *catalogCore {
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
	cfg := &Config{
		Providers:    map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: "/secrets/claude"}},
		Agents:       map[string]Agent{"sonnet": {Runtime: "claude-code", Provider: "anthropic", Use: "default"}},
		DefaultAgent: "sonnet",
		Servers:      map[string]Server{"docs": {URL: "https://docs.example/mcp", Auth: &ServerAuth{None: true}, Use: "docs"}},
	}
	if mutate != nil {
		mutate(cfg)
	}
	return startDynamicCore(t, cfg, store)
}

func startDynamicCore(t *testing.T, cfg *Config, store *Store) *catalogCore {
	t.Helper()
	cc := &catalogCore{core: newCore(cfg, store), dials: map[string]int{}}
	cc.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		cc.mu.Lock()
		cc.dials[u.name]++
		cc.mu.Unlock()
		s := mcp.NewServer(&mcp.Implementation{Name: u.name}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "ping"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"pong": u.srv.URL}, nil
		})
		ct, st := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cc.pool.run(ctx)
	return cc
}

func wantKey(t *testing.T, err error, key string) {
	t.Helper()
	var ce *catalogError
	if !errors.As(err, &ce) || ce.Key != key {
		t.Fatalf("error = %v, want key %q", err, key)
	}
}

func TestCatalogServers(t *testing.T) {
	c := newDynamicCore(t, nil)
	home := Server{URL: "http://ha.local:8123/api/mcp", Auth: &ServerAuth{}, Use: "the house", TrustAnnotations: true}
	if err := c.putServer(putCreate, "home", home, credentials{"token": "ha-long-lived"}); err != nil {
		t.Fatal(err)
	}
	s := c.conf().Servers["home"]
	if s.Auth.BearerFile != "vault:server.home.token" {
		t.Fatalf("bearer = %q", s.Auth.BearerFile)
	}
	if got, _ := readSecret(s.Auth.BearerFile); got != "ha-long-lived" {
		t.Fatalf("secret = %q", got)
	}
	if c.source(kindServer, "home") != sourceWorkspace || c.source(kindServer, "docs") != sourceConfig {
		t.Fatal("sources")
	}
	// Live: the pool connects it.
	waitStatus(t, c.pool.get("home"), statusReady)
	if l := c.listServers("home", false); len(l) != 1 || l[0].Source != sourceWorkspace {
		t.Fatalf("list = %+v", l)
	}
	// A task can be granted it now.
	if _, err := c.conf().checkGrant([]string{"home"}); err != nil {
		t.Fatal(err)
	}

	// Refusals: the configuration's entries, duplicates, bad entries, files.
	wantKey(t, c.putServer(putUpsert, "docs", home, nil), "catalog.readonly")
	wantKey(t, c.deleteServer("docs"), "catalog.readonly")
	wantKey(t, c.putServer(putCreate, "home", home, nil), "catalog.exists")
	wantKey(t, c.putServer(putUpdate, "nope", home, nil), "catalog.notfound")
	wantKey(t, c.putServer(putCreate, "Bad_Name", home, nil), "catalog.badname")
	wantKey(t, c.putServer(putCreate, "eriphe-core", home, nil), "catalog.badname")
	bad := []Server{
		{URL: "http://x/mcp", Auth: &ServerAuth{None: true}},                          // no use
		{URL: "ftp://x", Auth: &ServerAuth{None: true}, Use: "x"},                     // bad url
		{URL: "http://x/mcp", Auth: &ServerAuth{BearerFile: "/etc/shadow"}, Use: "x"}, // a file
		{URL: "http://x/mcp", Auth: &ServerAuth{}, Use: "x"},                          // bearer without token
		{Run: json.RawMessage(`{"image":"x","transport":"stdio","env":{"K":{"file":"/vault.key"}}}`), Use: "x"},
		{Run: json.RawMessage(`{"image":"x","transport":"carrier-pigeon"}`), Use: "x"},
		{Run: json.RawMessage(`{"image":"x","transport":"stdio","bogus":1}`), Use: "x"},
	}
	for i, b := range bad {
		var ce *catalogError
		if err := c.putServer(putCreate, fmt.Sprintf("bad%d", i), b, nil); !errors.As(err, &ce) || ce.Status != http.StatusBadRequest {
			t.Errorf("bad %d: %v", i, err)
		}
		if _, ok := c.conf().Servers[fmt.Sprintf("bad%d", i)]; ok {
			t.Errorf("bad %d made it into the catalog", i)
		}
	}

	// Edit without a new token keeps it; the session starts over.
	before := c.pool.get("home")
	home.Auth.BearerFile = c.conf().Servers["home"].Auth.BearerFile
	home.Use = "lights and heating"
	if err := c.putServer(putUpdate, "home", home, nil); err != nil {
		t.Fatal(err)
	}
	after := c.pool.get("home")
	if after == before {
		t.Fatal("a changed server keeps its old upstream")
	}
	waitStatus(t, after, statusReady)
	if st, _, _ := before.snapshot(); st != statusError {
		t.Fatalf("old upstream = %s", st)
	}
	if got, _ := readSecret("vault:server.home.token"); got != "ha-long-lived" {
		t.Fatal("token lost on edit")
	}

	// Switching to OAuth drops the token; removing forgets everything.
	c.store.saveOAuthToken("home", &oauth2.Config{ClientID: "x"}, &oauth2.Token{AccessToken: "old"})
	home.Auth = &ServerAuth{OAuth: &OAuthConfig{}}
	if err := c.putServer(putUpdate, "home", home, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret("vault:server.home.token"); err == nil {
		t.Fatal("the bearer token outlived its use")
	}
	if _, tok, _ := c.store.oauthToken("home"); tok != nil {
		t.Fatal("an auth change kept the old OAuth token")
	}
	if err := c.deleteServer("home"); err != nil {
		t.Fatal(err)
	}
	if c.pool.get("home") != nil || c.conf().Servers["home"].URL != "" {
		t.Fatal("removed server still there")
	}
	if st, _, _ := after.snapshot(); st != statusError {
		t.Fatalf("removed upstream = %s", st)
	}
	wantKey(t, c.deleteServer("home"), "catalog.notfound")
}

func TestCatalogPersistsAndSkips(t *testing.T) {
	c := newDynamicCore(t, nil)
	if err := c.putServer(putCreate, "notes", Server{URL: "https://n.example/mcp", Auth: &ServerAuth{None: true}, Use: "notes"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.putProvider(putCreate, "openrouter", Provider{Kind: "openrouter-api"}, "sk-or"); err != nil {
		t.Fatal(err)
	}
	// An entry the configuration now defines too, and one that no longer checks.
	c.store.putCatalog(kindServer, "docs", Server{URL: "https://other/mcp", Auth: &ServerAuth{None: true}, Use: "x"})
	c.store.putCatalog(kindAgent, "ghost", Agent{Runtime: "claude-code", Provider: "gone", Use: "x"})
	c2 := startDynamicCore(t, c.base, c.store)
	if c2.conf().Servers["notes"].URL == "" || c2.conf().Providers["openrouter"].Secret != "vault:provider.openrouter.key" {
		t.Fatalf("not restored: %+v", c2.conf().Servers)
	}
	if c2.conf().Servers["docs"].URL != "https://docs.example/mcp" {
		t.Fatal("a workspace entry shadowed the configuration")
	}
	// A secret of a stored entry left out is kept through later changes.
	c2.store.putCatalog(kindProvider, "gone", Provider{Kind: "openrouter-api", Secret: "vault:provider.gone.key"})
	currentVault().set("provider.gone.key", "kept")
	c2.store.putCatalog(kindAgent, "ghost2", Agent{Runtime: "claude-code", Provider: "nope", Use: "x"})
	c2.putServer(putCreate, "other", Server{URL: "https://o.example/mcp", Auth: &ServerAuth{None: true}, Use: "o"}, nil)
	if got, _ := readSecret("vault:provider.gone.key"); got != "kept" {
		t.Fatal("a stored entry's secret was dropped")
	}
	sk := c2.skippedEntries()
	if !strings.Contains(sk["agent/ghost"], "unknown provider") || !strings.Contains(sk["server/docs"], "shadowed") {
		t.Fatalf("skipped = %v", sk)
	}
	waitStatus(t, c2.pool.get("notes"), statusReady)
}

func TestCatalogAgents(t *testing.T) {
	c := newDynamicCore(t, nil)
	if err := c.putProvider(putCreate, "or", Provider{Kind: "openrouter-api"}, ""); err == nil {
		t.Fatal("a provider without a key")
	}
	wantKey(t, c.putProvider(putCreate, "anthropic", Provider{Kind: "anthropic-api"}, "k"), "catalog.readonly")
	if err := c.putProvider(putCreate, "or", Provider{Kind: "openrouter-api", Secret: "/run/secrets/x"}, ""); err == nil {
		t.Fatal("a file secret from the workspace")
	}
	if err := c.putProvider(putCreate, "or", Provider{Kind: "telepathy-api"}, "k"); err == nil {
		t.Fatal("unknown kind")
	}
	if err := c.putProvider(putCreate, "or", Provider{Kind: "openrouter-api"}, "sk-or-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.putAgent(putCreate, "kimi", Agent{Runtime: "opencode", Provider: "or", Model: "moonshotai/kimi", Use: "x"}); err == nil {
		t.Fatal("opencode model without its provider prefix")
	}
	if err := c.putAgent(putCreate, "kimi", Agent{Runtime: "claude-code", Provider: "or", Use: "x"}); err == nil {
		t.Fatal("claude-code on openrouter")
	}
	kimi := Agent{Runtime: "opencode", Provider: "or", Model: "openrouter/moonshotai/kimi-k2", Use: "second opinion", Prefers: []string{"cpu"}}
	if err := c.putAgent(putCreate, "kimi", kimi); err != nil {
		t.Fatal(err)
	}
	// The scheduler and list_agents see it at once; its key is lent.
	if _, a, err := c.conf().agent("kimi"); err != nil || a.Model != kimi.Model {
		t.Fatal(err)
	}
	env, err := c.lend("worker", "kimi", "")
	if err != nil || env["OPENROUTER_API_KEY"] != "sk-or-1" {
		t.Fatalf("lend = %v, %v", env, err)
	}
	agents, _ := c.listAgents()
	if len(agents) != 2 {
		t.Fatalf("agents = %+v", agents)
	}
	wantKey(t, c.deleteProvider("or"), "catalog.inuse")
	wantKey(t, c.setDefaultAgent("kimi"), "catalog.readonly") // the file sets it
	// A new key replaces the old one in place.
	if err := c.putProvider(putUpdate, "or", c.conf().Providers["or"], "sk-or-2"); err != nil {
		t.Fatal(err)
	}
	if env, _ := c.lend("worker", "kimi", ""); env["OPENROUTER_API_KEY"] != "sk-or-2" {
		t.Fatal("key not replaced")
	}
	if err := c.deleteAgent("kimi"); err != nil {
		t.Fatal(err)
	}
	if err := c.deleteProvider("or"); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret("vault:provider.or.key"); err == nil {
		t.Fatal("provider key outlived its provider")
	}
}

// Without agents in the file, the workspace sets them up from scratch.
func TestCatalogDefaultAgent(t *testing.T) {
	c := newDynamicCore(t, func(cfg *Config) {
		cfg.Providers, cfg.Agents, cfg.DefaultAgent = nil, nil, ""
	})
	if err := c.putProvider(putCreate, "claude", Provider{Kind: "claude-subscription"}, "oauth-tok"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"sonnet", "haiku"} {
		if err := c.putAgent(putCreate, n, Agent{Runtime: "claude-code", Provider: "claude", Model: n, Use: n}); err != nil {
			t.Fatal(err)
		}
	}
	if d := c.conf().DefaultAgent; d != "haiku" {
		t.Fatalf("default = %q, want the first by name", d)
	}
	if err := c.setDefaultAgent("sonnet"); err != nil {
		t.Fatal(err)
	}
	if c.conf().DefaultAgent != "sonnet" {
		t.Fatal("default not set")
	}
	wantKey(t, c.setDefaultAgent("nobody"), "catalog.notfound")
	if err := c.deleteAgent("sonnet"); err != nil {
		t.Fatal(err)
	}
	if c.conf().DefaultAgent != "haiku" {
		t.Fatalf("default after removing it = %q", c.conf().DefaultAgent)
	}
}

func TestCatalogLocalServer(t *testing.T) {
	c := newDynamicCore(t, nil)
	run := RunSpec{Image: "ghcr.io/alexis-marcel/eriphe-mail", Transport: transportStdio,
		Env: map[string]EnvValue{"MAIL_CONFIG": {Value: `{"accounts":[]}`}}}
	raw, _ := json.Marshal(run)
	if err := c.putServer(putCreate, "mail", Server{Run: raw, Use: "mail"}, credentials{"env:MAIL_PASSWORD": "app-pw"}); err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.service("mail")
	if err != nil {
		t.Fatalf("service not registered: %v", err)
	}
	if svc.Spec.Env["MAIL_PASSWORD"].File != "vault:service.mail.mail_password" {
		t.Fatalf("env = %+v", svc.Spec.Env)
	}
	env, err := lendService(svc.Spec)
	if err != nil || env["MAIL_PASSWORD"] != "app-pw" || env["MAIL_CONFIG"] == "" {
		t.Fatalf("lend = %v %v", env, err)
	}
	if u := c.pool.get("mail"); u == nil || u.srv.authKind() != "local" {
		t.Fatal("not in the pool")
	}
	if err := c.deleteServer("mail"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.service("mail"); !errors.Is(err, errNoService) {
		t.Fatalf("service after removal: %v", err)
	}
	if _, err := readSecret("vault:service.mail.mail_password"); err == nil {
		t.Fatal("service secret kept")
	}
}

// The snapshot is read by many goroutines while the catalog changes.
func TestCatalogConcurrency(t *testing.T) {
	c := newDynamicCore(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				m := c.conf()
				for range m.Servers {
				}
				c.listServers("", true)
				c.listAgents()
				m.checkGrant([]string{"docs"})
				c.specServers(&TaskRow{Tools: "docs"})
				c.catalog()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("s%d", i%3)
		s := Server{URL: fmt.Sprintf("https://s%d.example/mcp", i), Auth: &ServerAuth{None: true}, Use: "x"}
		if err := c.putServer(putUpsert, name, s, nil); err != nil {
			t.Fatal(err)
		}
		if i%4 == 3 {
			c.deleteServer(name)
		}
		c.putAgent(putUpsert, "a", Agent{Runtime: "claude-code", Provider: "anthropic", Use: fmt.Sprint(i)})
	}
	cancel()
	wg.Wait()
}

func TestCatalogAPI(t *testing.T) {
	c := newDynamicCore(t, nil)
	mux := http.NewServeMux()
	c.catalogRoutes(mux, "secret")
	c.serverRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if code := call(t, srv, "PUT", "/v1/servers/gh", `{"url":"https://api.githubcopilot.com/mcp/","auth":{},"use":"github","secrets":{"token":"ghp_x"}}`, nil); code != 200 {
		t.Fatalf("put server: %d", code)
	}
	if code := call(t, srv, "PUT", "/v1/servers/docs", `{"url":"https://x/mcp","auth":{"none":true},"use":"x"}`, nil); code != http.StatusConflict {
		t.Fatalf("put config server: %d", code)
	}
	if code := call(t, srv, "PUT", "/v1/servers/x", `{"url":"https://x/mcp","use":"x","nonsense":1}`, nil); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}
	if code := call(t, srv, "PUT", "/v1/providers/or", `{"kind":"openrouter-api","key":"sk"}`, nil); code != 204 {
		t.Fatalf("put provider: %d", code)
	}
	if code := call(t, srv, "PUT", "/v1/agents/kimi", `{"runtime":"opencode","provider":"or","model":"openrouter/k2","use":"x"}`, nil); code != 204 {
		t.Fatalf("put agent: %d", code)
	}
	var cat catalogView
	if code := call(t, srv, "GET", "/v1/catalog", "", &cat); code != 200 {
		t.Fatal(code)
	}
	raw, _ := json.Marshal(cat)
	if strings.Contains(string(raw), "ghp_x") || strings.Contains(string(raw), `"sk"`) {
		t.Fatalf("a secret in the catalog: %s", raw)
	}
	if len(cat.Servers) != 2 || cat.Servers[0].Name != "docs" || cat.Servers[0].Source != sourceConfig || cat.Servers[1].Source != sourceWorkspace ||
		cat.DefaultSource != sourceConfig || len(cat.Agents) != 2 {
		t.Fatalf("catalog = %s", raw)
	}
	var list []serverInfo
	call(t, srv, "GET", "/v1/servers", "", &list)
	if len(list) != 2 || list[1].Source != sourceWorkspace {
		t.Fatalf("servers = %+v", list)
	}
	if code := call(t, srv, "DELETE", "/v1/providers/or", "", nil); code != http.StatusConflict {
		t.Fatalf("delete used provider: %d", code)
	}
	for _, p := range []string{"/v1/agents/kimi", "/v1/providers/or", "/v1/servers/gh"} {
		if code := call(t, srv, "DELETE", p, "", nil); code != 204 {
			t.Fatalf("delete %s: %d", p, code)
		}
	}
	if code := call(t, srv, "DELETE", "/v1/servers/gh", "", nil); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}

// The brain hears of what changed.
func TestCatalogTellsTheBrain(t *testing.T) {
	c := newDynamicCore(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet", Tools: []string{"notion"}} })
	if got := c.conf().brainTools(); len(got) != 0 {
		t.Fatalf("brain tools before = %v", got)
	}
	if err := c.putServer(putCreate, "notion", Server{URL: "https://mcp.notion.com/mcp", Auth: &ServerAuth{OAuth: &OAuthConfig{}}, Use: "notes"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.conf().brainTools(); len(got) != 1 || got[0] != "notion" {
		t.Fatalf("brain tools after = %v", got)
	}
	msgs, _ := c.store.messagesAfter(0, 10)
	if len(msgs) != 1 || msgs[0].Role != roleEvent || !strings.Contains(msgs[0].Text, "« notion » a été ajouté") {
		t.Fatalf("messages = %+v", msgs)
	}
	wantKey(t, c.deleteAgent("sonnet"), "catalog.readonly")
}

// Without a brain agent in the file, the workspace picks it: the default
// agent first, then my choice, kept across a restart; a harness that cannot
// be the brain, an unknown agent or the brain's own removal are refused.
func TestCatalogBrainAgent(t *testing.T) {
	c := newDynamicCore(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Node: "nas"} })
	if a := c.conf().Brain.Agent; a != "sonnet" {
		t.Fatalf("brain agent = %q, want the default one", a)
	}
	if err := c.putProvider(putCreate, "or", Provider{Kind: "openrouter-api"}, "sk-or-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.putAgent(putCreate, "free", Agent{Runtime: "opencode", Provider: "or", Model: "openrouter/example/model:free", Use: "x"}); err != nil {
		t.Fatal(err)
	}
	wantKey(t, c.setBrainAgent("nobody"), "catalog.notfound")
	if err := c.setBrainAgent("free"); err != nil {
		t.Fatal(err)
	}
	if a := c.conf().Brain.Agent; a != "free" || c.base.Brain.Agent != "" {
		t.Fatalf("brain agent = %q, file's = %q", a, c.base.Brain.Agent)
	}
	wantKey(t, c.deleteAgent("free"), "catalog.brainagent")
	if v := c.catalog(); v.BrainAgent != "free" || v.BrainSource != sourceWorkspace {
		t.Fatalf("catalog = %q %q", v.BrainAgent, v.BrainSource)
	}
	again := startDynamicCore(t, c.base, c.store)
	if a := again.conf().Brain.Agent; a != "free" || len(again.skipped) != 0 {
		t.Fatalf("after a restart: %q, left out %v", a, again.skipped)
	}

	file := newDynamicCore(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} })
	wantKey(t, file.setBrainAgent("sonnet"), "catalog.readonly")
	none := newDynamicCore(t, nil)
	wantKey(t, none.setBrainAgent("sonnet"), "catalog.nobrain")
}
