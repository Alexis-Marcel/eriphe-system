package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct horse battery"

type testWeb struct {
	t   *testing.T
	w   *web
	c   *core
	srv *httptest.Server
	hc  *http.Client
}

// newTestWeb serves the workspace over TLS on a temporary store, with
// cheap password hashing.
func newTestWeb(t *testing.T) *testWeb {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{User: "Sam", Listen: "127.0.0.1:8800", DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: "/nonexistent"}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p", Use: "quick things"}}}
	return newTestWebFor(t, newCore(cfg, s))
}

func newTestWebFor(t *testing.T, c *core) *testWeb {
	t.Helper()
	argonTime, argonMemory, argonThreads = 1, 1024, 1
	w, err := newWeb(c, WebConfig{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(w.routes())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Transport: srv.Client().Transport, Jar: jar}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &testWeb{t: t, w: w, c: c, srv: srv, hc: hc}
}

func (tw *testWeb) do(method, path string, form url.Values, header map[string]string) (int, string, http.Header) {
	tw.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, tw.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := tw.hc.Do(req)
	if err != nil {
		tw.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
var csrfMeta = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

// formToken loads a login or setup page and returns its CSRF token.
func (tw *testWeb) formToken(path string) string {
	tw.t.Helper()
	code, body, _ := tw.do("GET", path, nil, nil)
	m := csrfField.FindStringSubmatch(body)
	if code != 200 || m == nil {
		tw.t.Fatalf("GET %s: %d, no CSRF field", path, code)
	}
	return m[1]
}

// setup creates the password and returns the session's CSRF token.
func (tw *testWeb) setup() string {
	tw.t.Helper()
	tok := tw.formToken("/setup")
	code, _, h := tw.do("POST", "/setup", url.Values{"csrf": {tok}, "code": {tw.w.setup},
		"password": {testPassword}, "confirm": {testPassword}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/" {
		tw.t.Fatalf("setup: %d %s", code, h.Get("Location"))
	}
	return tw.sessionToken()
}

// sessionToken reads the session's CSRF token from a page that is always
// there (the conversation may lead to the first-run wizard).
func (tw *testWeb) sessionToken() string {
	tw.t.Helper()
	code, body, _ := tw.do("GET", "/settings/account", nil, nil)
	m := csrfMeta.FindStringSubmatch(body)
	if code != 200 || m == nil || m[1] == "" {
		tw.t.Fatalf("GET /settings/account: %d, no CSRF token", code)
	}
	return m[1]
}

func (tw *testWeb) post(path string, form url.Values, csrf string) (int, string, http.Header) {
	return tw.do("POST", path, form, map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true"})
}

func TestWebSetupLoginLogout(t *testing.T) {
	tw := newTestWeb(t)
	if code, _, h := tw.do("GET", "/", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/setup" {
		t.Fatalf("before setup: %d %s", code, h.Get("Location"))
	}
	tok := tw.formToken("/setup")
	if code, _, _ := tw.do("POST", "/setup", url.Values{"csrf": {tok}, "code": {"AAAA-AAAA"},
		"password": {testPassword}, "confirm": {testPassword}}, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong setup code: %d", code)
	}
	if code, _, _ := tw.do("POST", "/setup", url.Values{"csrf": {tok}, "code": {tw.w.setup},
		"password": {"short"}, "confirm": {"short"}}, nil); code != http.StatusBadRequest {
		t.Fatalf("short password: %d", code)
	}
	if code, _, _ := tw.do("POST", "/setup", url.Values{"csrf": {"forged"}, "code": {tw.w.setup},
		"password": {testPassword}, "confirm": {testPassword}}, nil); code != http.StatusForbidden {
		t.Fatalf("setup without CSRF: %d", code)
	}
	csrf := tw.setup()
	if tw.w.setup != "" {
		t.Fatal("the setup code still works after setup")
	}
	if hash := tw.w.ownerHash(); !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, testPassword) {
		t.Fatalf("stored password: %q", hash)
	}
	if code, _, h := tw.do("GET", "/setup", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Fatalf("setup again: %d", code)
	}

	if code, _, h := tw.post("/logout", nil, csrf); code != http.StatusOK || h.Get("HX-Redirect") != "/login" {
		t.Fatalf("logout: %d %v", code, h)
	}
	if code, _, h := tw.do("GET", "/", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Fatalf("after logout: %d %s", code, h.Get("Location"))
	}

	tok = tw.formToken("/login")
	if code, body, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {"not the password"}}, nil); code != http.StatusUnauthorized || !strings.Contains(body, "incorrect") {
		t.Fatalf("bad password: %d", code)
	}
	if code, _, h := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, nil); code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("login: %d", code)
	}
	tw.sessionToken()

	// The session cookie is HttpOnly, Secure and SameSite=Strict.
	u, _ := url.Parse(tw.srv.URL)
	for _, c := range tw.hc.Jar.Cookies(u) {
		if c.Name == sessionCookie && c.Value != "" {
			return
		}
	}
	t.Fatal("no session cookie")
}

func TestWebSessionCookieFlags(t *testing.T) {
	tw := newTestWeb(t)
	tok := tw.formToken("/setup")
	_, _, h := tw.do("POST", "/setup", url.Values{"csrf": {tok}, "code": {tw.w.setup},
		"password": {testPassword}, "confirm": {testPassword}}, nil)
	var set string
	for _, c := range h.Values("Set-Cookie") {
		if strings.HasPrefix(c, sessionCookie+"=") {
			set = c
		}
	}
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(set, want) {
			t.Errorf("session cookie %q lacks %s", set, want)
		}
	}
}

func TestWebLoginRateLimit(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.hc.Jar, _ = cookiejar.New(nil)
	tok := tw.formToken("/login")
	for i := 0; i < 5; i++ {
		if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {"guess"}}, nil); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	// Locked out, even with the right password.
	if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures: %d", code)
	}
	tw.w.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	if code, _, _ := tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, nil); code != http.StatusSeeOther {
		t.Fatalf("after the window: %d", code)
	}
}

func TestWebCSRF(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	if code, _, _ := tw.do("POST", "/messages", url.Values{"text": {"hello"}}, nil); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
	if code, _, _ := tw.post("/messages", url.Values{"text": {"hello"}}, "wrong"); code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _, _ := tw.do("POST", "/messages", url.Values{"text": {"hello"}},
		map[string]string{"X-CSRF-Token": csrf, "Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("other origin: %d", code)
	}
	if msgs, _ := tw.c.store.messagesAfter(0, 10); len(msgs) != 0 {
		t.Fatalf("a refused POST added %v", msgs)
	}
	if code, _, _ := tw.post("/messages", url.Values{"text": {"hello"}}, csrf); code != http.StatusNoContent {
		t.Fatalf("with the token: %d", code)
	}
	msgs, _ := tw.c.store.messagesAfter(0, 10)
	if len(msgs) != 1 || msgs[0].Role != roleUser || msgs[0].Text != "hello" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestWebSessionExpiry(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	// A week without use ends the session.
	tw.w.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if code, _, h := tw.do("GET", "/", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Fatalf("idle session: %d %s", code, h.Get("Location"))
	}
	// So does its end, even in use.
	tw.w.now = time.Now
	tok := tw.formToken("/login")
	tw.do("POST", "/login", url.Values{"csrf": {tok}, "password": {testPassword}}, nil)
	tw.sessionToken()
	tw.w.idle = 365 * 24 * time.Hour
	tw.w.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if code, _, _ := tw.do("GET", "/", nil, nil); code != http.StatusSeeOther {
		t.Fatalf("expired session: %d", code)
	}
	var n int
	tw.c.store.db.QueryRow(`SELECT COUNT(*) FROM web_sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions left", n)
	}
}

func TestWebSecurityHeaders(t *testing.T) {
	tw := newTestWeb(t)
	_, _, h := tw.do("GET", "/login", nil, nil)
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "default-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %s", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe") {
		t.Errorf("CSP allows unsafe sources: %q", csp)
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers: %v", h)
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS on a self-signed certificate")
	}
}

// fixtures fills the store with what the pages show.
func fixtures(t *testing.T, c *core) {
	t.Helper()
	s := c.store
	s.registerNode("box", "h")
	s.setNodeStatus("box", true, []byte(`{"cpus":8,"memory_bytes":17179869184,"runtimes":["claude-code"],"labels":["gpu"],"version":"0.1.0-dev+7654321"}`))
	s.registerNode("old", "h2")
	for _, task := range []*TaskRow{
		{ID: "t-run", Node: "box", Agent: "small", Runtime: "claude-code", Dir: "acme/site@fix-form", Prompt: "Fix the form", State: stateRunning},
		{ID: "t-perm", Node: "box", Agent: "small", Runtime: "claude-code", Dir: "acme/pkg", Prompt: "Publish the package", State: stateWaitingPermission},
		{ID: "t-idle", Node: "box", Agent: "small", Runtime: "claude-code", Dir: "acme/notes", Prompt: "Write the summary", State: stateIdle},
		{ID: "t-done", Node: "box", Agent: "small", Runtime: "claude-code", Dir: "acme/site", Prompt: "Old work", State: stateStopped},
	} {
		if err := s.insertTask(task); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	s.addEvents("t-run", []EventRow{{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "PR: https://github.com/acme/site/pull/7."}},
		{Seq: 2, Time: now, Type: "tool_use", Data: map[string]any{"name": "Bash", "input": map[string]any{"command": "go test ./..."}}}})
	s.addEvents("t-perm", []EventRow{{Seq: 1, Time: now, Type: "permission", Data: map[string]any{"id": "p-1", "tool_name": "Bash",
		"input": map[string]any{"command": "npm publish"}}}})
	s.markUserOnly("t-perm", "p-1", "publishes a package")
	s.addEvents("t-idle", []EventRow{{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "Done. Shall I send it?"}}})
	s.addMessage(roleUser, "Fix the <b>form</b>", true)
	s.addMessage(roleBrain, "On it: **small** agent.", true)
	s.recordDecision("t-run", "p-0", "Bash", map[string]any{"command": "git push origin main"}, decideDeny, byPolicy, "no pushing to main")
}

func TestWebPagesRender(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	fixtures(t, tw.c)
	dir := t.TempDir()
	key, _ := vaultKey(filepath.Join(dir, "vault.key"))
	v, err := openVault(tw.c.store, key)
	if err != nil {
		t.Fatal(err)
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	v.set("github", "secret-value-123")

	pages := map[string][]string{
		"/": {"Fix the &lt;b&gt;form&lt;/b&gt;", "<strong>small</strong>", "Cette décision te revient", "npm publish",
			`name="permission" value="p-1"`, "PR #7", "acme/site", "fix-form", "Attend ta réponse", "Terminées récemment"},
		"/tasks/t-run":       {"go test ./...", "Arrêter la tâche", "PR #7"},
		"/tasks/t-idle":      {"Répondre à l&#39;agent", "Shall I send it?"},
		"/tasks/t-perm":      {"d-perm-t-perm-p-1"},
		"/settings/machines": {"box", "En ligne", "Hors ligne", "16 Go", "gpu", `eriphe-node 0.1.0-dev <small class="mono">7654321</small>`},
		"/settings/agents":   {"small", "quick things", "Par défaut", "Prêt"},
		"/settings/tools":    {"github", "providers.p.secret", "Secrets avancés", "Home Assistant", "/settings/tools/new?preset=mail"},
		"/settings/tools/new?preset=home-assistant": {"/api/mcp", `name="token"`, `value="bearer" checked`},
		"/settings/tools/new?preset=mail":           {"provider=gmail", "provider=free", "provider=other", "Bientôt"},
		"/settings/tools/new?preset=mail&provider=gmail": {`name="address"`, `name="password"`, "myaccount.google.com/apppasswords",
			`name="mail_provider" value="gmail"`, `name="access" value="read" checked`},
		"/settings/tools/new?preset=calendar":                 {"provider=icloud", "provider=nextcloud", "Google Agenda"},
		"/settings/tools/new?preset=calendar&provider=icloud": {`name="cal_user"`, "Europe/Paris", "Mots de passe pour apps"},
		"/settings/tools/new?preset=local":                    {`name="image"`, `name="env_name_0"`},
		"/settings/agents/new":                                {`name="runtime"`, `name="provider"`},
		"/settings/providers/new":                             {"kind=claude-subscription", "kind=chatgpt-subscription"},
		"/settings/providers/new?kind=claude-subscription":    {`name="key"`, "claude setup-token", `name="preset"`},
		"/settings/journal":                                   {"git push origin main", "Règle", "Refusé"},
		"/settings/account":                                   {"Français", "English", "eriphe-core " + version},
	}
	for path, wants := range pages {
		code, body, _ := tw.do("GET", path, nil, nil)
		if code != 200 {
			t.Errorf("GET %s: %d %s", path, code, body)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %q", path, want)
			}
		}
		if strings.Contains(body, "secret-value-123") {
			t.Errorf("GET %s shows a secret's value", path)
		}
	}

	// English.
	u, _ := url.Parse(tw.srv.URL)
	tw.hc.Jar.SetCookies(u, []*http.Cookie{{Name: "lang", Value: "en", Path: "/"}})
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, "This decision is yours") || !strings.Contains(body, `lang="en"`) {
		t.Error("the English page is not in English")
	}
}

func TestWebAddMachine(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	code, body, _ := tw.post("/settings/machines/join", url.Values{"name": {"desk"}, "labels": {"gpu"}}, csrf)
	m := regexp.MustCompile(`-join-token ([0-9a-f]{16}\.[0-9a-f]{48}) -name desk -labels gpu`).FindStringSubmatch(body)
	if code != 200 || m == nil {
		t.Fatalf("join command: %d %s", code, body)
	}
	// The one-liners first, from the latest GitHub release; compose, the program alone and
	// the downloaded script under the advanced options.
	for _, want := range []string{
		"curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core http://",
		" --join-token " + m[1] + " --name desk --labels gpu</pre>",
		"& ([scriptblock]::Create((irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1))) -Core 'http://",
		"-JoinToken " + m[1] + " -Name desk -Labels 'gpu'</pre>",
		"<summary>Options avancées</summary>",
		"eriphe-node -core http://", "install.ps1 -Core 'http://", "ERIPHE_JOIN_TOKEN: " + m[1],
	} {
		if !strings.Contains(body, want) {
			t.Errorf("join commands lack %q", want)
		}
	}
	if strings.Index(body, "releases/latest/download/install.sh") > strings.Index(body, "Options avancées") {
		t.Error("the one-liners are not first")
	}
	if strings.Contains(body, "-Rejoin") {
		t.Error("a new machine's command asks to rejoin")
	}
	if _, err := tw.c.store.joinNode(hashSecret(m[1]), "other", hashSecret("s")); !errors.Is(err, errJoinName) {
		t.Fatalf("the token joins another name: %v", err)
	}
	if _, err := tw.c.store.joinNode(hashSecret(m[1]), "desk", hashSecret("s")); err != nil {
		t.Fatalf("the token shown does not join: %v", err)
	}
	if _, body, _ := tw.post("/settings/machines/join", url.Values{"name": {"Bad Name"}}, csrf); strings.Contains(body, "-join-token") {
		t.Fatal("a bad name got a token")
	}
	// A taken name gets no token; the machine's own action gives a rejoin one.
	if _, body, _ := tw.post("/settings/machines/join", url.Values{"name": {"desk"}}, csrf); strings.Contains(body, "-join-token") {
		t.Fatal("a taken name got a join token")
	}
	_, body, _ = tw.post("/settings/machines/desk/rejoin", url.Values{}, csrf)
	m = regexp.MustCompile(`-join-token ([0-9a-f]{16}\.[0-9a-f]{48}) -name desk`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "-Rejoin") {
		t.Fatalf("rejoin command: %s", body)
	}
	if rejoined, err := tw.c.store.joinNode(hashSecret(m[1]), "desk", hashSecret("s2")); err != nil || !rejoined {
		t.Fatalf("the rejoin token does not rejoin: %v %v", rejoined, err)
	}
	if !tw.c.store.checkNode("desk", hashSecret("s2")) {
		t.Fatal("the new secret is not the node's")
	}
	if _, body, _ := tw.post("/settings/machines/ghost/rejoin", url.Values{}, csrf); strings.Contains(body, "-join-token") {
		t.Fatal("an unknown machine got a rejoin token")
	}
}

func TestWebAnswerAsUser(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	fixtures(t, tw.c)
	// A stale card: the permission shown is not the pending one.
	if _, _, h := tw.post("/tasks/t-perm/answer", url.Values{"permission": {"p-0"}, "allow": {"yes"}}, csrf); !strings.Contains(h.Get("HX-Trigger"), "error") {
		t.Fatalf("stale permission: %v", h)
	}
	if tw.c.store.answered("t-perm", "p-1") {
		t.Fatal("a stale answer went through")
	}
	code, _, h := tw.post("/tasks/t-perm/answer", url.Values{"permission": {"p-1"}, "allow": {"no"}, "reason": {"not yet"}}, csrf)
	if code != 200 || !strings.Contains(h.Get("HX-Trigger"), "ok") {
		t.Fatalf("answer: %d %v", code, h)
	}
	items, _ := tw.c.store.inbox("t-perm")
	if len(items) != 1 || items[0].Data["by"] != byUser || items[0].Data["allow"] != false || items[0].Data["message"] != "not yet" {
		t.Fatalf("inbox = %+v", items)
	}
	rows, _ := tw.c.store.decisions(0, 10)
	if len(rows) == 0 || rows[0].By != byUser || rows[0].Decision != decideDeny || rows[0].Subject != "npm publish" {
		t.Fatalf("decisions = %+v", rows)
	}
	if _, body, _ := tw.do("GET", "/", nil, nil); strings.Contains(body, `value="p-1"`) {
		t.Fatal("an answered permission is still shown")
	}
	// The brain could not have answered it: only the user may.
	if _, err := tw.c.answer(context.Background(), "t-perm", "", "", new(bool), byBrain); err == nil {
		t.Fatal("answered twice")
	}

	// Replying to an idle task is a new turn, from me.
	if code, _, h := tw.post("/tasks/t-idle/reply", url.Values{"text": {"Yes, send it"}}, csrf); code != 200 || !strings.Contains(h.Get("HX-Trigger"), "ok") {
		t.Fatalf("reply: %d %v", code, h)
	}
	items, _ = tw.c.store.inbox("t-idle")
	if len(items) != 1 || items[0].Kind != "message" || items[0].Data["text"] != "Yes, send it" {
		t.Fatalf("idle inbox = %+v", items)
	}
	if code, _, _ := tw.post("/tasks/t-run/stop", nil, csrf); code != 200 {
		t.Fatalf("stop: %d", code)
	}
}

func TestWebEventsStream(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	if code, _, _ := (&testWeb{t: t, srv: tw.srv, hc: &http.Client{Transport: tw.hc.Transport}}).do("GET", "/events", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("events without a session: %d", code)
	}
	tw.c.store.addMessage(roleUser, "before", true)
	last := tw.c.store.lastMessageSeq()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events?after="+strconv.Itoa(last), nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		tw.c.store.addMessage(roleBrain, "a new **answer**", true)
		tw.w.notify()
	}()
	sc := bufio.NewScanner(resp.Body)
	event, sawTasks := "", false
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
			sawTasks = sawTasks || v == "tasks"
		}
		if event == "message" && strings.HasPrefix(line, "data: ") {
			if strings.Contains(line, "before") {
				t.Fatal("the stream sent a message seen already")
			}
			if strings.Contains(line, "<strong>answer</strong>") {
				if !sawTasks {
					t.Error("no tasks panel before the message")
				}
				return
			}
		}
	}
	t.Fatalf("no message event: %v", sc.Err())
}

func TestMarkdownEscapes(t *testing.T) {
	got := string(markdown("Hi <script>alert(1)</script> **bold** `<code>` [x](javascript:alert(1)) https://example.com/a_b.\n\n- one\n- two"))
	for _, bad := range []string{"<script>", `href="javascript`} {
		if strings.Contains(got, bad) {
			t.Errorf("markdown kept %q: %s", bad, got)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "<strong>bold</strong>", "<code>&lt;code&gt;</code>",
		`<a href="https://example.com/a_b"`, "</a>.", "<ul><li>one</li><li>two</li></ul>"} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown lacks %q: %s", want, got)
		}
	}
}

func TestSelfSignedCertificate(t *testing.T) {
	dir := t.TempDir()
	names := certNames([]string{"eriphe.home"}, "http://10.1.2.3:8800")
	cert, err := selfSigned(filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem"), names, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !certCovers(cert, []string{"eriphe.home", "10.1.2.3", "localhost"}, time.Now()) {
		t.Fatal("certificate misses a name")
	}
	if certCovers(cert, []string{"other.home"}, time.Now()) {
		t.Fatal("certificate covers an unknown name")
	}
	if certCovers(cert, names, time.Now().Add(380*24*time.Hour)) {
		t.Fatal("a certificate near its end is kept")
	}
	if st, _ := os.Stat(filepath.Join(dir, "k.pem")); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode())
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if leaf.PublicKeyAlgorithm != x509.ECDSA || len(fingerprint(cert)) != 95 {
		t.Fatalf("algorithm %v, fingerprint %q", leaf.PublicKeyAlgorithm, fingerprint(cert))
	}
}

// Connect from the workspace: the browser goes to the authorization page
// and comes back to the workspace's own HTTPS callback, bound to the
// browser that started it.
func TestWebConnectServer(t *testing.T) {
	f := newFakeAS(t)
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c, cancel := oauthCore(t, store, f)
	defer cancel()
	waitStatus(t, c.pool.get("notion"), statusNeedsAuth)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "notion") ||
		!strings.Contains(body, "À connecter") || !strings.Contains(body, "/settings/servers/notion/connect") {
		t.Fatalf("tools page: %s", body)
	}
	if code, _, _ := tw.post("/settings/servers/notion/connect", nil, "forged"); code != http.StatusForbidden {
		t.Fatalf("connect without CSRF: %d", code)
	}
	code, _, h := tw.post("/settings/servers/notion/connect", nil, csrf)
	authURL := h.Get("HX-Redirect")
	if code != 200 || authURL == "" {
		t.Fatalf("connect: %d %v", code, h)
	}
	callback := tw.srv.URL + "/oauth/callback"
	u, _ := url.Parse(authURL)
	if got := u.Query().Get("redirect_uri"); got != callback {
		t.Fatalf("redirect_uri = %q, want %q", got, callback)
	}
	if got := f.registered(); len(got) != 1 || got[0] != callback {
		t.Fatalf("registered redirects = %v", got)
	}
	authCode, state := f.authorize(authURL)

	// Another browser, or a forged state, cannot finish it.
	other := &testWeb{t: t, srv: tw.srv, hc: &http.Client{Transport: tw.hc.Transport}}
	if code, body, _ := other.do("GET", "/oauth/callback?code="+authCode+"&state="+url.QueryEscape(state), nil, nil); code != http.StatusBadRequest ||
		!strings.Contains(body, "plus valable") {
		t.Fatalf("callback from another browser: %d", code)
	}
	if code, _, _ := tw.do("GET", "/oauth/callback?code="+authCode+"&state=forged", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("callback with a forged state: %d", code)
	}
	code, body, h := tw.do("GET", "/oauth/callback?code="+authCode+"&state="+url.QueryEscape(state), nil, nil)
	if code != 200 || !strings.Contains(body, "« notion » est connecté") || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("callback: %d %s", code, body)
	}
	if strings.Contains(body, authCode) {
		t.Fatal("the page shows the code")
	}
	waitStatus(t, c.pool.get("notion"), statusReady)
	if _, body, _ := tw.do("GET", "/settings/tools?connected=notion", nil, nil); !strings.Contains(body, "srv-ready") || !strings.Contains(body, "est connecté") {
		t.Fatal("the server is not shown connected")
	}
	// The same answer again is refused: the flow is over.
	if code, _, _ := tw.do("GET", "/oauth/callback?code="+authCode+"&state="+url.QueryEscape(state), nil, nil); code != http.StatusBadRequest {
		t.Fatalf("replayed callback: %d", code)
	}
	// The CLI's hint stays a loopback one.
	if s := c.listServers("notion", false); s[0].RedirectURI != "" {
		t.Fatalf("hint = %q", s[0].RedirectURI)
	}
}

// A pre-registered client that knows only a loopback redirect keeps the
// paste step.
func TestWebConnectPaste(t *testing.T) {
	f := newFakeAS(t)
	store, _ := openStore(filepath.Join(t.TempDir(), "core.db"))
	loopback := "http://127.0.0.1:8976/callback"
	cfg := &Config{Servers: map[string]Server{"notion": {URL: f.srv.URL + "/mcp",
		Auth: &ServerAuth{OAuth: &OAuthConfig{RedirectURI: loopback}}, Use: "notes"}}}
	c := newCore(cfg, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.pool.run(ctx)
	waitStatus(t, c.pool.get("notion"), statusNeedsAuth)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	_, body, _ := tw.post("/settings/servers/notion/connect", nil, csrf)
	m := regexp.MustCompile(`href="([^"]+)" target="_blank"`).FindStringSubmatch(body)
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil || flow == nil {
		t.Fatalf("connect: %s", body)
	}
	authURL := html.UnescapeString(m[1])
	code, state := f.authorize(authURL)
	if u, _ := url.Parse(authURL); u.Query().Get("redirect_uri") != loopback {
		t.Fatalf("redirect_uri = %q", u.Query().Get("redirect_uri"))
	}
	_, body, _ = tw.post("/settings/servers/notion/callback", url.Values{"flow": {flow[1]}, "url": {"not a redirect"}}, csrf)
	if !strings.Contains(body, "code=") {
		t.Fatalf("a bad URL: %s", body)
	}
	// The workspace's callback does not take a CLI-style flow.
	if code, _, _ := tw.do("GET", "/oauth/callback?code="+code+"&state="+url.QueryEscape(state), nil, nil); code != http.StatusBadRequest {
		t.Fatalf("web callback for a paste flow: %d", code)
	}
	_, body, _ = tw.post("/settings/servers/notion/callback", url.Values{"flow": {flow[1]},
		"url": {loopback + "?code=" + code + "&state=" + url.QueryEscape(state)}}, csrf)
	if !strings.Contains(body, "Connecté : 1 outils") {
		t.Fatalf("callback: %s", body)
	}
}
