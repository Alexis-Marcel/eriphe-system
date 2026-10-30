package main

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setLevel changes a catalog server's access level and waits for it.
func setLevel(t *testing.T, c *core, name, level string) {
	t.Helper()
	servers := c.conf().Servers
	servers[name] = servers[name].withLevel(level)
	c.pool.sync(servers)
	waitStatus(t, c.pool.get(name), statusReady)
}

// Each level against a read tool (get_issue, readOnlyHint) and a write
// tool (create_issue) of github, and what the brain and tasks are lent.
func TestAccessLevels(t *testing.T) {
	c := newCatalogCore(t)
	setBrain(c, &BrainConfig{})
	read, write := "mcp__github__get_issue", "mcp__github__create_issue"
	cases := []struct {
		level, read, write string
		lent, freeReads    string // tools a task granted github sees, the brain's free tools
	}{
		{accessReadOnly, decideBrain, decideDeny, "get_issue", "mcp__github__get_issue"},
		{accessReads, decideBrain, decideAsk, "create_issue,get_issue", "mcp__github__get_issue"},
		{accessFree, decideBrain, decideBrain, "create_issue,get_issue", "mcp__github__create_issue,mcp__github__get_issue"},
		{accessAsk, decideAsk, decideAsk, "create_issue,get_issue", ""},
	}
	for _, tc := range cases {
		setLevel(t, c, "github", tc.level)
		if got, _ := c.decide(read, map[string]any{"number": 1}); got != tc.read {
			t.Errorf("%s: a read = %q, want %q", tc.level, got, tc.read)
		}
		if got, _ := c.decide(write, map[string]any{"title": "x"}); got != tc.write {
			t.Errorf("%s: a write = %q, want %q", tc.level, got, tc.write)
		}
		cs := connectTo(t, c.taskServer("t-1", []string{"github"}, "github"))
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range res.Tools {
			names = append(names, tl.Name)
		}
		if got := strings.Join(sortedCopy(names), ","); got != tc.lent {
			t.Errorf("%s: lent %s, want %s", tc.level, got, tc.lent)
		}
		if got := strings.Join(sortedCopy(c.brainFreeReads()), ","); got != tc.freeReads {
			t.Errorf("%s: brain's free tools %s, want %s", tc.level, got, tc.freeReads)
		}
		if info := c.listServers("github", true); len(info) != 1 || info[0].Access != tc.level || info[0].ToolCount != len(strings.Split(tc.lent, ",")) {
			t.Errorf("%s: list_tools %+v", tc.level, info)
		}
		// The built-in denials win whatever the level.
		if got, _ := c.decide("Bash", map[string]any{"command": "rm -rf /work"}); got != decideDeny {
			t.Errorf("%s: built-in denial = %q", tc.level, got)
		}
	}
	// Read only: a write is refused at the proxy even with a ticket.
	setLevel(t, c, "github", accessReadOnly)
	u := c.pool.get("github")
	c.store.recordDecision("t-1", "p1", write, map[string]any{"title": "x"}, decideAllow, byUser, "")
	raw, _ := json.Marshal(map[string]any{"title": "x"})
	if out := c.proxyCall(context.Background(), "t-1", []string{"github"}, u, "create_issue", raw); !out.IsError ||
		!strings.Contains(resultText(out), "read only") {
		t.Fatalf("a write on a read-only tool = %+v", out)
	}
	// The file's rules still come before the free level.
	setLevel(t, c, "github", accessFree)
	c.conf().Policy = &Policy{Rules: []Rule{{Tool: write, Decision: decideDeny, Reason: "no issues"}}}
	if err := c.conf().Policy.compile(); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decide(write, nil); got != decideDeny {
		t.Fatalf("a deny rule with everything free = %q", got)
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Older configurations: trust_annotations alone; forms that post "read" or
// "trust"; a bad level is refused.
func TestAccessCompatibility(t *testing.T) {
	if l := (Server{TrustAnnotations: true}).level(); l != accessReads {
		t.Errorf("trusted = %s", l)
	}
	if l := (Server{}).level(); l != accessAsk {
		t.Errorf("not trusted = %s", l)
	}
	if l := (Server{Access: accessReadOnly}).level(); l != accessReadOnly {
		t.Errorf("access = %s", l)
	}
	if s := (Server{}).withLevel(accessFree); !s.TrustAnnotations || s.Access != accessFree {
		t.Errorf("withLevel = %+v", s)
	}
	if s := (Server{TrustAnnotations: true}).withLevel(accessAsk); s.TrustAnnotations {
		t.Errorf("ask still trusts: %+v", s)
	}
	for _, tc := range []struct {
		v     string
		trust bool
		want  string
	}{{"read", false, accessReads}, {"ask", true, accessAsk}, {"", true, accessReads}, {"", false, accessAsk},
		{"free", false, accessFree}, {"read-only", false, accessReadOnly}, {"bogus", false, accessAsk}} {
		if got := formAccess(tc.v, tc.trust); got != tc.want {
			t.Errorf("formAccess(%q, %v) = %s, want %s", tc.v, tc.trust, got, tc.want)
		}
	}
	bad := &Config{Servers: map[string]Server{"x": {URL: "https://x.example/mcp", Auth: &ServerAuth{None: true}, Use: "x", Access: "all"}}}
	if err := bad.checkServers(); err == nil || !strings.Contains(err.Error(), "access") {
		t.Fatalf("a bad level: %v", err)
	}
	var s Server
	if err := json.Unmarshal([]byte(`{"url":"https://x","use":"x","trust_annotations":true}`), &s); err != nil || s.level() != accessReads {
		t.Fatalf("an older entry: %+v %v", s, err)
	}
}

// "Always allow": a rule of the workspace after the file's, never above the
// level's or the built-in denials, removable.
func TestRememberedActions(t *testing.T) {
	c := newCatalogCore(t)
	write := "mcp__github__create_issue"
	if got, _ := c.decide(write, nil); got != decideAsk {
		t.Fatalf("before = %q", got)
	}
	if err := c.rememberAction("Bash"); err == nil {
		t.Fatal("a shell command remembered")
	}
	if err := c.rememberAction("mcp__mail__send"); err == nil {
		t.Fatal("a tool out of the catalog remembered")
	}
	if err := c.rememberAction(write); err != nil {
		t.Fatal(err)
	}
	if got, why := c.decide(write, map[string]any{"title": "x"}); got != decideAllow || why != allowedReason {
		t.Fatalf("remembered = %q %q", got, why)
	}
	if a := c.store.allowedActions("github"); len(a) != 1 || a[0].Name != "create_issue" || a[0].Tool != write {
		t.Fatalf("listed = %+v", a)
	}
	// The file's rules win.
	c.conf().Policy = &Policy{Rules: []Rule{{Tool: write, Match: "secret", Decision: decideDeny}}}
	if err := c.conf().Policy.compile(); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decide(write, map[string]any{"title": "secret"}); got != decideDeny {
		t.Fatalf("file rule after a remembered action = %q", got)
	}
	c.conf().Policy = nil
	// Read only refuses the write, remembered or not, and offers no "always".
	setLevel(t, c, "github", accessReadOnly)
	if got, _ := c.decide(write, nil); got != decideDeny {
		t.Fatalf("remembered write on a read-only tool = %q", got)
	}
	if c.rememberable(write) {
		t.Fatal("always offered on a read-only tool's write")
	}
	setLevel(t, c, "github", accessReads)
	if err := c.store.forgetAction("github", write); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decide(write, nil); got != decideAsk {
		t.Fatalf("forgotten = %q", got)
	}
}

// The permission card offers "always" for a catalog tool, the answer
// allows it and remembers it, the tool's access page lists it and removes
// it, in French and in English.
func TestWebAlwaysAllow(t *testing.T) {
	c := newCatalogCore(t)
	c.conf().User = "Sam"
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	fixtures(t, c) // t-perm waits for a shell command: no "always"
	c.store.insertTask(&TaskRow{ID: "t-gh", Node: "box", Agent: "sonnet", Runtime: "claude-code", Prompt: "File the bug", State: stateWaitingPermission})
	c.store.addEvents("t-gh", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-1",
		"tool_name": "mcp__github__create_issue", "input": map[string]any{"title": "a bug"}}}})
	_, body, _ := tw.do("GET", "/tasks/t-gh", nil, nil)
	if !strings.Contains(body, `name="allow" value="always"`) || !strings.Contains(body, "Toujours autoriser") {
		t.Fatalf("no always on a catalog tool: %s", mainPart(t, body))
	}
	if _, body, _ := tw.do("GET", "/tasks/t-perm", nil, nil); strings.Contains(body, `value="always"`) {
		t.Fatal("always offered for a shell command")
	}
	code, _, h := tw.post("/tasks/t-gh/answer", url.Values{"permission": {"p-1"}, "allow": {"always"}}, csrf)
	if code != 200 || !strings.Contains(h.Get("HX-Trigger"), "ok") {
		t.Fatalf("answer: %d %v", code, h)
	}
	items, _ := c.store.inbox("t-gh")
	if len(items) != 1 || items[0].Data["allow"] != true || items[0].Data["by"] != byUser {
		t.Fatalf("inbox = %+v", items)
	}
	if got, _ := c.decide("mcp__github__create_issue", nil); got != decideAllow {
		t.Fatalf("not remembered: %q", got)
	}
	// The tool's page and its tile.
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	if b := html.UnescapeString(body); !strings.Contains(b, "/settings/tools/github/access") || !strings.Contains(b, "1 action autorisée") ||
		!strings.Contains(b, "Lecture libre, écriture sur accord") {
		t.Fatalf("tile: %s", mainPart(t, body))
	}
	_, body, _ = tw.do("GET", "/settings/tools/github/access", nil, nil)
	b := html.UnescapeString(body)
	for _, want := range []string{"Actions autorisées sans demander", "create_issue", "fichier de configuration", "Lecture seule", "Tout libre",
		"Tout sur accord", "Ce service ne propose pas de jeton en lecture seule"} {
		if !strings.Contains(b, want) {
			t.Fatalf("access page lacks %q: %s", want, mainPart(t, body))
		}
	}
	_, body, _ = tw.do("GET", "/settings/tools/github/access", nil, map[string]string{"Cookie": "lang=en"})
	b = html.UnescapeString(body)
	for _, want := range []string{"Actions allowed without asking", "Read only", "Free reads, writes on approval", "Everything free", "Everything on approval"} {
		if !strings.Contains(b, want) {
			t.Fatalf("English access page lacks %q", want)
		}
	}
	// A file's tool: its level is the file's.
	if _, body, _ := tw.post("/settings/tools/github/access", url.Values{"access": {"free"}}, csrf); !strings.Contains(html.UnescapeString(body), "configuration") {
		t.Fatalf("a file's tool changed: %s", body)
	}
	if c.conf().Servers["github"].level() != accessReads {
		t.Fatal("a file's level changed")
	}
	code, body, _ = tw.post("/settings/tools/github/allowed/delete", url.Values{"tool": {"mcp__github__create_issue"}}, csrf)
	if code != 200 || !strings.Contains(html.UnescapeString(body), "Aucune.") {
		t.Fatalf("remove: %d %s", code, body)
	}
	if got, _ := c.decide("mcp__github__create_issue", nil); got != decideAsk {
		t.Fatalf("still allowed: %q", got)
	}
}

// A workspace tool's level is changed from its access page; read only on a
// key that cannot be scoped says that Eriphe holds to it.
func TestWebChangeLevel(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	form := oauthToolForm(f)
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", form)
	code, state := f.authorize(authURL)
	callback(t, tw, "code="+code+"&state="+url.QueryEscape(state))
	waitStatus(t, c.pool.get("linear"), statusReady)
	code2, _, h := tw.post("/settings/tools/linear/access", url.Values{"access": {"read-only"}}, csrf)
	if code2 != 200 || h.Get("HX-Redirect") != "/settings/tools/linear/access?saved=1" {
		t.Fatalf("save: %d %v", code2, h)
	}
	if s := c.conf().Servers["linear"]; s.level() != accessReadOnly || s.authKind() != "oauth" {
		t.Fatalf("saved %+v", s)
	}
	_, body, _ := tw.do("GET", "/settings/tools/linear/access?saved=1", nil, nil)
	if b := html.UnescapeString(body); !strings.Contains(b, "Accès enregistré.") || !strings.Contains(b, "Le jeton permet plus : Eriphe s'en tient à la lecture.") ||
		!strings.Contains(b, `value="read-only" checked`) {
		t.Fatalf("page after save: %s", mainPart(t, body))
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "Lecture seule") {
		t.Fatal("the tile does not show the level")
	}
	if _, _, h := tw.post("/settings/tools/linear/access", url.Values{"access": {"all"}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("a bad level saved")
	}
}

// The OAuth scopes follow the level where the server advertises read and
// write; a narrower token than the level needs is seen, and so is one
// broader than read only.
func TestOAuthScopeFollowsLevel(t *testing.T) {
	f := newFakeAS(t)
	f.scopes = []string{"read", "write"}
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c, cancel := oauthCore(t, store, f)
	defer cancel()
	setLevelNoWait(c, "notion", accessReadOnly)
	waitStatus(t, c.pool.get("notion"), statusNeedsAuth)
	connectFlow(t, c, f, "http://127.0.0.1:8976/callback")
	if words := strings.Fields(f.lastScope); contains(words, "write") || !contains(words, "read") {
		t.Fatalf("read only asked %q", f.lastScope)
	}
	srv := c.conf().Servers["notion"]
	if got := store.get(tokenAccessKey("notion")); got != grantedRead {
		t.Fatalf("granted = %q", got)
	}
	if s := c.tokenScope("notion", srv); !s.Scoped || s.Granted != grantedRead || s.Reconnect || s.Shrink {
		t.Fatalf("read only, read token: %+v", s)
	}
	// Going up: the token cannot write, reconnect.
	setLevel(t, c, "notion", accessReads)
	if s := c.tokenScope("notion", c.conf().Servers["notion"]); !s.Reconnect {
		t.Fatalf("reads on a read token: %+v", s)
	}
	connectFlow(t, c, f, "http://127.0.0.1:8976/callback")
	if words := strings.Fields(f.lastScope); !contains(words, "write") || !contains(words, "read") {
		t.Fatalf("reads asked %q", f.lastScope)
	}
	if s := c.tokenScope("notion", c.conf().Servers["notion"]); s.Reconnect || s.Granted != grantedWrite {
		t.Fatalf("after reconnecting: %+v", s)
	}
	// Going down applies at once; reconnecting would shrink the token.
	setLevel(t, c, "notion", accessReadOnly)
	if s := c.tokenScope("notion", c.conf().Servers["notion"]); !s.Shrink || s.Reconnect {
		t.Fatalf("read only on a write token: %+v", s)
	}
	// A server with no read-only scope: not scoped, Eriphe holds to it.
	f.mu.Lock()
	f.scopes = []string{"read", "default"} // no write scope to tell apart
	f.mu.Unlock()
	connectFlow(t, c, f, "http://127.0.0.1:8976/callback")
	if s := c.tokenScope("notion", c.conf().Servers["notion"]); s.Scoped || s.Shrink {
		t.Fatalf("an unscoped server: %+v", s)
	}
}

func setLevelNoWait(c *core, name, level string) {
	servers := c.conf().Servers
	servers[name] = servers[name].withLevel(level)
	c.pool.sync(servers)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// The per-preset table and the generic fallback.
func TestScopesFor(t *testing.T) {
	cases := []struct {
		preset, level string
		found         []string
		fixed         bool
		want, granted string
	}{
		{"linear", accessReadOnly, nil, false, "read", grantedRead},
		{"linear", accessReads, nil, false, "read write", grantedWrite},
		{"", accessReadOnly, []string{"read", "write", "openid"}, false, "read openid", grantedRead},
		{"", accessFree, []string{"read", "write"}, false, "read write", grantedWrite},
		{"", accessReadOnly, []string{"default"}, false, "default", grantedNone},
		{"", accessReadOnly, []string{"read", "write"}, true, "read write", grantedWrite}, // the file's scopes, as they are
		{"linear", accessReads, []string{"read"}, true, "read", grantedRead},
	}
	for _, tc := range cases {
		got, granted := scopesFor(tc.preset, tc.level, tc.found, tc.fixed)
		if strings.Join(got, " ") != tc.want || granted != tc.granted {
			t.Errorf("scopesFor(%s, %s, %v, %v) = %v %s, want %s %s", tc.preset, tc.level, tc.found, tc.fixed, got, granted, tc.want, tc.granted)
		}
	}
}

// The GitHub tool on the app, read only: an installation token asked with
// read permissions only, cached apart from the full one.
func TestGitHubToolTokenReadOnly(t *testing.T) {
	c, _ := credCore(t, "")
	f := newFakeGitHub(t)
	withApp(t, c, f)
	if _, err := c.github.toolToken(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.mints()[0])
	if string(raw) != `{"permissions":{"checks":"read","contents":"read","issues":"read","metadata":"read","pull_requests":"read"}}` {
		t.Fatalf("read-only token asked with %s", raw)
	}
	if _, err := c.github.toolToken(context.Background(), false); err != nil || len(f.mints()) != 2 {
		t.Fatalf("the full token reused the read-only one: %v, %d mints", err, len(f.mints()))
	}
	if raw, _ := json.Marshal(f.mints()[1]); string(raw) != `{}` {
		t.Fatalf("full token asked with %s", raw)
	}
	if s := c.tokenScope("github", Server{URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{GitHubApp: true}, Access: accessReadOnly}); !s.Scoped || s.Granted != grantedRead {
		t.Fatalf("app scope: %+v", s)
	}
}

// A read-only tool lends only what is marked read-only, and only when the
// server's annotations are trusted.
func TestLends(t *testing.T) {
	ro := &mcp.Tool{Name: "r", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	rw := &mcp.Tool{Name: "w"}
	if !lends(Server{Access: accessReadOnly}, ro) || lends(Server{Access: accessReadOnly}, rw) || !lends(Server{Access: accessReads}, rw) {
		t.Fatal("lends")
	}
}
