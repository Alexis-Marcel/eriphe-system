package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// noAgents is a configuration with no provider or agent of its own.
func noAgents(cfg *Config) { cfg.Providers, cfg.Agents, cfg.DefaultAgent = nil, nil, "" }

func TestWebAgentsOnboarding(t *testing.T) {
	tw, _, _ := catalogWeb(t, noAgents)
	_, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	for _, want := range []string{"Pour commencer", "kind=claude-subscription", "kind=chatgpt-subscription", "kind=openrouter-api",
		"Un abonnement", "Une clé API"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty agents page lacks %q", want)
		}
	}
	if strings.Contains(body, `href="/settings/agents/new"`) {
		t.Error("offers to add an agent before an account")
	}
	_, body, _ = tw.do("GET", "/settings/agents/new", nil, nil)
	if !strings.Contains(body, "Ajoute d&#39;abord ton abonnement") {
		t.Error("the agent form does not send to the accounts first")
	}
	for kind, wants := range map[string][]string{
		"claude-subscription":  {"claude setup-token", "environ un an", "Ne colle pas ce jeton dans une conversation avec une IA", "sk-ant-oat", `value="opus"`, `value="sonnet"`, `value="haiku"`, "Abonnement"},
		"anthropic-api":        {"platform.claude.com", "sk-ant-api", "bill-usage"},
		"openrouter-api":       {"openrouter.ai/settings/keys", `value="kimi"`, "Kimi K3.1 Pro"},
		"chatgpt-subscription": {`hx-post="/settings/chatgpt-login" hx-target="#login-box" hx-swap="innerHTML"`, "code d&#39;appareil", `value="gpt"`},
	} {
		_, body, _ := tw.do("GET", "/settings/providers/new?kind="+kind, nil, nil)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s form lacks %q", kind, want)
			}
		}
	}
}

func TestWebProviderWithPresets(t *testing.T) {
	tw, c, csrf := catalogWeb(t, noAgents)
	// A Claude API key in the subscription card is caught.
	_, body, _ := tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "key": {"sk-ant-api03-xxxxxxxx"}}, csrf)
	if !strings.Contains(body, "Clé API Anthropic") {
		t.Fatalf("misplaced key accepted: %s", body)
	}
	_, body, h := tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "key": {"sk-ant-oat01-" + strings.Repeat("x", 40)},
		"preset": {"sonnet", "haiku"}}, csrf)
	if h.Get("HX-Redirect") != "/settings/agents?created=claude" {
		t.Fatalf("provider refused: %s", body)
	}
	m := c.conf()
	if m.Providers["claude"].Secret != "vault:provider.claude.key" || len(m.Agents) != 2 || m.DefaultAgent != "sonnet" {
		t.Fatalf("providers %v, agents %v, default %q", m.Providers, m.Agents, m.DefaultAgent)
	}
	if a := m.Agents["haiku"]; a.Runtime != "claude-code" || a.Model != "haiku" || !strings.Contains(a.Use, "petites tâches") {
		t.Fatalf("haiku = %+v", a)
	}
	_, body, _ = tw.do("GET", "/settings/agents?created=claude", nil, nil)
	for _, want := range []string{"Compte « claude » ajouté. 2 agents créés", "Claude Sonnet", "Agents conseillés", `name="preset" value="opus"`,
		"Eriphe, ton cerveau", "Comptes IA"} {
		if !strings.Contains(body, want) {
			t.Errorf("agents page lacks %q", want)
		}
	}
	if strings.Contains(body, "sk-ant-oat01") {
		t.Fatal("token shown")
	}
	// The suggestion, one click.
	if _, _, h := tw.post("/settings/agents/suggested", url.Values{"provider": {"claude"}, "preset": {"opus"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatal("suggestion refused")
	}
	if a := c.conf().Agents["opus"]; a.Model != "opus" {
		t.Fatalf("opus = %+v", a)
	}
	if _, body, _ := tw.do("GET", "/settings/agents", nil, nil); strings.Contains(body, "Agents conseillés") {
		t.Error("still suggests what exists")
	}
	// A second Claude account: its presets get its name.
	tw.post("/settings/providers", url.Values{"kind": {"anthropic-api"}, "key": {"sk-ant-api03-yyyy"}, "preset": {"sonnet"}}, csrf)
	if a, ok := c.conf().Agents["sonnet-anthropic"]; !ok || a.Provider != "anthropic" || c.conf().DefaultAgent != "sonnet" {
		t.Fatalf("agents %v, default %q", c.conf().Agents, c.conf().DefaultAgent)
	}
	// The kind of a provider does not change on an edit.
	tw.post("/settings/providers/anthropic", url.Values{"kind": {"openrouter-api"}}, csrf)
	if k := c.conf().Providers["anthropic"].Kind; k != "anthropic-api" {
		t.Fatalf("kind changed to %s", k)
	}
}

func TestWebAgentForm(t *testing.T) {
	tw, c, csrf := catalogWeb(t, noAgents)
	tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "key": {"sk-ant-oat01-" + strings.Repeat("x", 40)}}, csrf)
	tw.post("/settings/providers", url.Values{"kind": {"openrouter-api"}, "key": {"sk-or-v1-zzz"}}, csrf)
	if len(c.conf().Agents) != 0 {
		t.Fatal("presets created without being asked")
	}
	// Changing the account offers its models and its program.
	_, body, _ := tw.do("GET", "/settings/agents/new?refresh=1&provider=openrouter&model=sonnet&runtime=claude-code", nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(body, `value="openrouter/moonshotai/kimi-k3.1-pro" selected`) || !strings.Contains(body, `name="runtime" value="opencode"`) {
		t.Fatalf("refresh: %s", body)
	}
	// Only a runtime that picks its own model offers to name none.
	if strings.Contains(body, "Le choix de Claude Code") {
		t.Error("opencode offers its own choice of model")
	}
	if _, body, _ := tw.do("GET", "/settings/agents/new?refresh=1&provider=claude", nil, map[string]string{"HX-Request": "true"}); !strings.Contains(body, "Le choix de Claude Code") {
		t.Error("claude-code does not offer its own choice of model")
	}
	// No name: the model's. Another model: the provider's prefix is added.
	_, body, h := tw.post("/settings/agents", url.Values{"provider": {"openrouter"}, "model": {"other"}, "model_other": {"qwen/qwen4-coder"},
		"use": {"code for less"}, "default": {"1"}}, csrf)
	if h.Get("HX-Redirect") == "" {
		t.Fatalf("agent refused: %s", body)
	}
	a, ok := c.conf().Agents["qwen4-coder"]
	if !ok || a.Model != "openrouter/qwen/qwen4-coder" || a.Runtime != "opencode" || c.conf().DefaultAgent != "qwen4-coder" {
		t.Fatalf("agents %v, default %q", c.conf().Agents, c.conf().DefaultAgent)
	}
	_, body, _ = tw.do("GET", "/settings/agents/qwen4-coder/edit", nil, nil)
	if !strings.Contains(body, `value="other" selected`) || !strings.Contains(body, `value="openrouter/qwen/qwen4-coder"`) {
		t.Fatalf("edit form: %s", body)
	}
	if _, body, h := tw.post("/settings/agents", url.Values{"provider": {"claude"}, "model": {"sonnet"}}, csrf); h.Get("HX-Redirect") != "" || !strings.Contains(body, `class="alert"`) {
		t.Fatalf("an agent without a use: %s", body)
	}
}

func TestWebProviderTest(t *testing.T) {
	var auth string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization") + r.Header.Get("x-api-key")
		switch {
		case strings.HasSuffix(auth, "good"):
			w.Write([]byte(`{"data":{"limit":10,"limit_remaining":7.5}}`))
		case strings.HasSuffix(auth, "broke"):
			w.WriteHeader(http.StatusPaymentRequired)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer fake.Close()
	old := checkEndpoints
	checkEndpoints = map[string]string{"openrouter-api": fake.URL, "anthropic-api": fake.URL}
	t.Cleanup(func() { checkEndpoints = old })

	tw, c, csrf := catalogWeb(t, noAgents)
	try := func(path string, form url.Values) string {
		_, body, _ := tw.post(path, form, csrf)
		return body
	}
	if b := try("/settings/provider-test", url.Values{"kind": {"openrouter-api"}, "key": {"sk-or-good"}}); !strings.Contains(b, "La clé fonctionne") || !strings.Contains(b, "7.50") {
		t.Errorf("good key: %s", b)
	}
	if auth != "Bearer sk-or-good" {
		t.Errorf("sent %q", auth)
	}
	if b := try("/settings/provider-test", url.Values{"kind": {"openrouter-api"}, "key": {"sk-or-bad"}}); !strings.Contains(b, "Clé refusée") {
		t.Errorf("bad key: %s", b)
	}
	if b := try("/settings/provider-test", url.Values{"kind": {"anthropic-api"}, "key": {"sk-ant-api-broke"}}); !strings.Contains(b, "plus de crédit") {
		t.Errorf("no credit: %s", b)
	}
	if auth != "sk-ant-api-broke" {
		t.Errorf("anthropic sent %q", auth)
	}
	if b := try("/settings/provider-test", url.Values{"kind": {"claude-subscription"}, "key": {"sk-ant-oat01-" + strings.Repeat("y", 40)}}); !strings.Contains(b, "première tâche") {
		t.Errorf("subscription token: %s", b)
	}
	if b := try("/settings/provider-test", url.Values{"kind": {"openrouter-api"}, "key": {"sk-ant-oat01-zzzz"}}); !strings.Contains(b, "Abonnement Claude") {
		t.Errorf("misplaced: %s", b)
	}
	// The stored key, when none is typed.
	c.putProvider(putCreate, "or", Provider{Kind: "openrouter-api"}, "sk-or-good")
	if b := try("/settings/provider-test/or", url.Values{}); !strings.Contains(b, "La clé fonctionne") {
		t.Errorf("stored key: %s", b)
	}
}

// A self-hosted model server: its card, "Test the server" listing its
// models from a fake /v1/models, one agent per model on creation, and the
// agent form offering them.
func TestWebSelfHosted(t *testing.T) {
	var auth, path string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		if auth != "" && auth != "Bearer sk-local" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"object":"list","data":[{"id":"qwen3:8b","object":"model"},{"id":"llama3.1:8b","object":"model"}]}`))
	}))
	defer fake.Close()
	// The form's address is on the LAN; the fake answers on the loopback.
	var dialed string
	selfHostedDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		var d net.Dialer
		return d.DialContext(ctx, network, fake.Listener.Addr().String())
	}
	t.Cleanup(func() { selfHostedDial = nil })

	tw, c, csrf := catalogWeb(t, noAgents)
	_, body, _ := tw.do("GET", "/settings/providers/new", nil, nil)
	if !strings.Contains(body, "kind=openai-compatible") || !strings.Contains(body, "Ton propre serveur") {
		t.Error("the gallery lacks the self-hosted card")
	}
	_, body, _ = tw.do("GET", "/settings/providers/new?kind=openai-compatible", nil, nil)
	for _, want := range []string{`name="base_url"`, `name="models"`, "Clé (facultative)", "Tester le serveur", "bill-selfhosted", "ollama.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %q", want)
		}
	}
	base := "http://192.0.2.10:11434/v1"
	try := func(form url.Values) string {
		form.Set("kind", selfHostedKind)
		_, body, _ := tw.post("/settings/provider-test", form, csrf)
		return body
	}
	if b := try(url.Values{"base_url": {base + "/"}}); !strings.Contains(b, "2 modèle(s)") || !strings.Contains(b, "<code>qwen3:8b</code>") ||
		auth != "" || path != "/v1/models" || dialed != "192.0.2.10:11434" {
		t.Errorf("no key: %s (auth %q, path %q, dialed %q)", b, auth, path, dialed)
	}
	if b := try(url.Values{"base_url": {base}, "key": {"sk-local"}}); !strings.Contains(b, "2 modèle(s)") || auth != "Bearer sk-local" {
		t.Errorf("key: %s (auth %q)", b, auth)
	}
	if b := try(url.Values{"base_url": {base}, "key": {"sk-wrong"}}); !strings.Contains(b, "Clé refusée") {
		t.Errorf("wrong key: %s", b)
	}
	dialed = ""
	for _, bad := range []string{"http://localhost:11434/v1", "http://127.0.0.1:11434/v1", "http://169.254.169.254/v1"} {
		if b := try(url.Values{"base_url": {bad}}); !strings.Contains(b, "Cette adresse ne convient pas") || dialed != "" {
			t.Errorf("%s: %s (dialed %q)", bad, b, dialed)
		}
	}

	// Saved: no key, its models, one agent each, the first the default.
	save := url.Values{"kind": {selfHostedKind}, "base_url": {base}, "models": {"qwen3:8b\nllama3.1:8b\n"}}
	if _, body, _ := tw.post("/settings/providers", url.Values{"kind": {selfHostedKind}, "base_url": {"http://127.0.0.1:11434/v1"}}, csrf); !strings.Contains(body, "Cette adresse ne convient pas") {
		t.Fatalf("loopback saved: %s", body)
	}
	if _, body, _ := tw.post("/settings/providers", url.Values{"kind": {selfHostedKind}, "base_url": {base}, "key": {`a"b`}}, csrf); !strings.Contains(body, "guillemet") {
		t.Fatalf("a key with a quote saved: %s", body)
	}
	_, body, h := tw.post("/settings/providers", save, csrf)
	if h.Get("HX-Redirect") != "/settings/agents?created=selfhosted" {
		t.Fatalf("refused: %s", body)
	}
	m := c.conf()
	p := m.Providers["selfhosted"]
	if p.Secret != "" || p.BaseURL != base || !slices.Equal(p.Models, []string{"qwen3:8b", "llama3.1:8b"}) {
		t.Fatalf("provider %+v", p)
	}
	if a := m.Agents["qwen3-8b"]; a.Model != "selfhosted/qwen3:8b" || a.Runtime != "opencode" || m.DefaultAgent != "qwen3-8b" ||
		m.Agents["llama3-1-8b"].Model != "selfhosted/llama3.1:8b" {
		t.Fatalf("agents %+v, default %q", m.Agents, m.DefaultAgent)
	}
	_, body, _ = tw.do("GET", "/settings/agents", nil, nil)
	if !strings.Contains(body, "Auto-hébergé") || !strings.Contains(body, "qwen3:8b") {
		t.Error("agents page lacks the self-hosted account")
	}
	// The stored address is tested when the form leaves it.
	auth = "x"
	if _, b, _ := tw.post("/settings/provider-test/selfhosted", url.Values{}, csrf); !strings.Contains(b, "2 modèle(s)") || auth != "" {
		t.Errorf("stored: %s", b)
	}
	// The agent form offers its models.
	_, body, _ = tw.do("GET", "/settings/agents/new?refresh=1&provider=selfhosted", nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(body, `value="selfhosted/qwen3:8b"`) || !strings.Contains(body, `value="selfhosted/llama3.1:8b"`) {
		t.Errorf("agent form: %s", body)
	}
	// An edit keeps the kind and changes the models.
	if _, body, h := tw.post("/settings/providers/selfhosted", url.Values{"base_url": {base}, "models": {"qwen3:8b"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatalf("edit: %s", body)
	}
	if p := c.conf().Providers["selfhosted"]; !slices.Equal(p.Models, []string{"qwen3:8b"}) || p.Kind != selfHostedKind {
		t.Fatalf("edited %+v", p)
	}
}

var loginID = regexp.MustCompile(`/settings/chatgpt-login/([0-9a-f]+)`)

func TestWebChatGPTSignIn(t *testing.T) {
	f := newFakeOpenAI(t)
	tw, c, csrf := catalogWeb(t, noAgents)
	_, body, _ := tw.post("/settings/chatgpt-login", nil, csrf)
	m := loginID.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "ABCD-1234") || !strings.Contains(body, f.URL+"/codex/device") || !strings.Contains(body, `hx-trigger="every 3s"`) {
		t.Fatalf("login box: %s", body)
	}
	// Saving before the sign-in is refused.
	if _, body, _ := tw.post("/settings/providers", url.Values{"kind": {"chatgpt-subscription"}, "login": {m[1]}}, csrf); !strings.Contains(body, "Connecte-toi d&#39;abord") {
		t.Fatalf("saved without a sign-in: %s", body)
	}
	f.mu.Lock()
	f.approved = true
	f.mu.Unlock()
	chatgptLogins.mu.Lock()
	sid := chatgptLogins.logins[m[1]].session
	chatgptLogins.mu.Unlock()
	waitLogin(t, m[1], sid, loginDone)
	_, body, _ = tw.do("GET", "/settings/chatgpt-login/"+m[1], nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(body, "sam@example.com") || !strings.Contains(body, "Plus") || !strings.Contains(body, `name="login" value="`+m[1]+`"`) {
		t.Fatalf("done box: %s", body)
	}
	for _, secret := range []string{"rt-", "access_token", "eyJ"} {
		if strings.Contains(body, secret) {
			t.Fatalf("the page shows %q", secret)
		}
	}
	if _, body, h := tw.post("/settings/providers", url.Values{"kind": {"chatgpt-subscription"}, "login": {m[1]}, "preset": {"gpt"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatalf("not saved: %s", body)
	}
	if a := c.conf().Agents["gpt"]; a.Runtime != "codex" || a.Model != "gpt-6.1-sol" || c.conf().DefaultAgent != "gpt" {
		t.Fatalf("gpt = %+v", a)
	}
	if v := chatgptLogins.status(m[1], sid); v.State != loginUnknown {
		t.Fatal("the sign-in outlives its save")
	}
	_, body, _ = tw.post("/settings/provider-test/chatgpt", nil, csrf)
	if !strings.Contains(body, "abonnement Plus") {
		t.Fatalf("test: %s", body)
	}
}

// The page's words do not replace others of the workspace.
func TestAgentWordsAreTheirOwn(t *testing.T) {
	for lang, words := range agentStrings {
		for _, other := range []map[string]map[string]string{catalogStrings} {
			for k := range words {
				if _, dup := other[lang][k]; dup {
					t.Errorf("%s: %s is also the catalog's", lang, k)
				}
			}
		}
	}
	if tr("fr", "login.title") != "Connexion" {
		t.Errorf("login.title = %q", tr("fr", "login.title"))
	}
}

// Another ACP agent comes with a provider that names its key's variable, and its command, one argument a
// line, goes into the tasks it runs.
func TestWebACPAgents(t *testing.T) {
	tw, c, csrf := catalogWeb(t, noAgents)
	_, body, _ := tw.do("GET", "/settings/providers/new", nil, nil)
	if !strings.Contains(body, "kind=custom-api") || !strings.Contains(body, "Autre agent ACP") {
		t.Fatalf("gallery: %s", body)
	}
	_, body, _ = tw.do("GET", "/settings/providers/new?kind=custom-api", nil, nil)
	if !strings.Contains(body, `name="env"`) || strings.Contains(body, "/settings/provider-test") || !strings.Contains(body, "agentclientprotocol.com") {
		t.Fatalf("custom form: %s", body)
	}
	// A system variable is refused; a key that looks like another kind's is not.
	if _, body, h := tw.post("/settings/providers", url.Values{"kind": {"custom-api"}, "env": {"PATH"}, "key": {"k"}}, csrf); h.Get("HX-Redirect") != "" ||
		!strings.Contains(body, "Ce nom de variable ne convient pas") {
		t.Fatalf("PATH accepted: %s", body)
	}
	if _, body, h := tw.post("/settings/providers", url.Values{"kind": {"custom-api"}, "env": {"OPENAI_API_KEY"}, "key": {"sk-proj-abc"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatalf("custom provider refused: %s", body)
	}
	if p := c.conf().Providers["custom"]; p.Kind != "custom-api" || p.Env != "OPENAI_API_KEY" {
		t.Fatalf("provider = %+v", p)
	}
	_, body, _ = tw.do("GET", "/settings/providers/custom/edit", nil, nil)
	if !strings.Contains(body, `value="OPENAI_API_KEY"`) {
		t.Fatalf("edit keeps no variable: %s", body)
	}

	// The agent form asks for a command instead of a model.
	_, body, _ = tw.do("GET", "/settings/agents/new?provider=custom", nil, nil)
	if !strings.Contains(body, `name="command"`) || strings.Contains(body, `name="model"`) {
		t.Fatalf("acp form: %s", body)
	}
	if _, body, h := tw.post("/settings/agents", url.Values{"provider": {"custom"}, "use": {"x"}}, csrf); h.Get("HX-Redirect") != "" ||
		!strings.Contains(body, "Saisis la commande") {
		t.Fatalf("no command accepted: %s", body)
	}
	if _, body, h := tw.post("/settings/agents", url.Values{"provider": {"custom"}, "command": {"sh\r\n-c\r\nqwen"}, "use": {"x"}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatalf("a shell accepted: %s", body)
	}
	_, body, h := tw.post("/settings/agents", url.Values{"provider": {"custom"}, "command": {"goose\r\n\r\nacp \r\n"}, "use": {"small chores"}, "default": {"1"}}, csrf)
	if h.Get("HX-Redirect") == "" {
		t.Fatalf("acp agent refused: %s", body)
	}
	a, ok := c.conf().Agents["goose"]
	if !ok || a.Runtime != "acp" || a.Model != "" || !slices.Equal(a.Command, []string{"goose", "acp"}) {
		t.Fatalf("agents %+v", c.conf().Agents)
	}
	_, body, _ = tw.do("GET", "/settings/agents/goose/edit", nil, nil)
	if !strings.Contains(body, "goose\nacp</textarea>") {
		t.Fatalf("edit form: %s", body)
	}

	// A task of the ACP agent carries its command; its instructions come first.
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["acp"]}`))
	task, err := c.runTask(context.Background(), runRequest{Prompt: "tidy up", Agent: "goose"})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	c.store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&raw)
	var spec nodeTaskSpec
	json.Unmarshal([]byte(raw), &spec)
	if spec.Runtime != "acp" || !slices.Equal(spec.Command, []string{"goose", "acp"}) || spec.AppendSystemPrompt != "" ||
		!strings.HasSuffix(spec.Prompt, "\n\ntidy up") {
		t.Fatalf("spec = %+v", spec)
	}
	env, err := c.lend("worker", "goose", "")
	if err != nil || env["OPENAI_API_KEY"] != "sk-proj-abc" {
		t.Fatalf("lent %v, %v", env, err)
	}
}

// Settings → Agents picks the brain's agent among those that can be it.
func TestWebBrainAgent(t *testing.T) {
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{Node: "nas"} })
	tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "key": {"sk-ant-oat01-" + strings.Repeat("x", 40)},
		"preset": {"opus", "sonnet"}}, csrf)
	if len(c.conf().Agents) == 0 {
		t.Fatal("no agents to choose from")
	}
	_, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	if !strings.Contains(body, `hx-post="/settings/brain-agent"`) || strings.Contains(body, "brain.agent") {
		t.Fatalf("no brain menu: %s", body)
	}
	other := "sonnet"
	if c.conf().brain().Agent == other {
		other = "opus"
	}
	if _, body, h := tw.post("/settings/brain-agent", url.Values{"agent": {other}}, csrf); h.Get("HX-Redirect") != "/settings/agents?brain=1" {
		t.Fatalf("brain agent refused: %s", body)
	}
	if c.conf().brain().Agent != other {
		t.Fatalf("brain agent = %q", c.conf().brain().Agent)
	}
	if _, body, _ := tw.do("GET", "/settings/agents?brain=1", nil, nil); !strings.Contains(body, "Eriphe réfléchit maintenant avec l&#39;agent « "+other+" »") {
		t.Fatalf("no flash: %s", body)
	}
	if _, _, h := tw.post("/settings/brain-agent", url.Values{"agent": {"nobody"}}, csrf); h.Get("HX-Redirect") != "" || c.conf().brain().Agent != other {
		t.Fatalf("an unknown agent taken: %q", c.conf().brain().Agent)
	}
}
