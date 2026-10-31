package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The first run: the account, then the assistant's name, then the AI
// account it thinks with, then it starts once a machine is there.
func TestWebWelcomeFlow(t *testing.T) {
	keepName(t)
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	if code, _, h := tw.do("GET", "/", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
		t.Fatalf("the conversation without an agent: %d %s", code, h.Get("Location"))
	}
	// First its name, which a new install chooses: the field starts empty.
	_, body, _ := tw.do("GET", "/welcome", nil, nil)
	for _, want := range []string{"Bienvenue", "Comment veux-tu appeler ton assistant ?", `action="/welcome/name"`,
		`name="name" value="" required`, "Jusqu&#39;à 32 caractères.", "/welcome/skip"} {
		if !strings.Contains(body, want) {
			t.Errorf("the name step lacks %q", want)
		}
	}
	if strings.Contains(body, `name="key"`) || strings.Contains(body, "placeholder") {
		t.Error("the AI account comes before the name, or a name is suggested")
	}
	if _, _, h := tw.post("/welcome/name", url.Values{"name": {""}}, csrf); h.Get("Location") != "" {
		t.Fatal("no name went on")
	}
	if _, body, h := tw.post("/welcome/name", url.Values{"name": {"Jar<vis>"}}, csrf); h.Get("Location") != "" ||
		!strings.Contains(body, "Ce nom ne convient pas") || !strings.Contains(body, `value="Jar&lt;vis&gt;"`) {
		t.Fatalf("a bad name: %s %s", h.Get("Location"), body)
	}
	if c.conf().assistantName() != "Eriphe" {
		t.Fatal("a bad name was kept")
	}
	if code, _, h := tw.post("/welcome/name", url.Values{"name": {" Jarvis "}}, csrf); code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
		t.Fatalf("named: %d %s", code, h.Get("Location"))
	}
	if b := c.conf().brain(); b.Name != brainAssistant || b.DisplayName != "Jarvis" {
		t.Fatalf("brain = %+v", b)
	}

	// Then the AI account: every kind, as equal choices, none picked;
	// subscriptions first, then the API keys, the custom one last.
	_, body, _ = tw.do("GET", "/welcome", nil, nil)
	for _, want := range []string{"Son IA", "Avec quelle IA Jarvis réfléchit-il ?", "Un abonnement", "Une clé API", "/welcome/skip"} {
		if !strings.Contains(body, want) {
			t.Errorf("the wizard lacks %q", want)
		}
	}
	last := -1
	for _, k := range []string{"claude-subscription", "chatgpt-subscription", "anthropic-api", "openrouter-api", "custom-api"} {
		i := strings.Index(body, `href="/welcome?kind=`+k+`"`)
		if i < 0 || i < last {
			t.Errorf("the kind %s is missing or out of order", k)
		}
		last = i
	}
	if strings.Contains(body, `name="key"`) || strings.Contains(body, "<details") {
		t.Error("a kind is picked in advance, or some are hidden")
	}
	// Picked, its form; the others one click away.
	_, body, _ = tw.do("GET", "/welcome?kind=claude-subscription", nil, nil)
	for _, want := range []string{"claude setup-token", `name="key"`, `name="next" value="welcome"`, `name="kind" value="claude-subscription"`, `href="/welcome"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form lacks %q", want)
		}
	}
	if strings.Contains(body, `href="/settings/agents">Annuler`) {
		t.Error("the wizard's form leads away to Settings")
	}
	if _, body, _ := tw.do("GET", "/welcome?kind=openrouter-api", nil, nil); !strings.Contains(body, `name="kind" value="openrouter-api"`) {
		t.Error("another kind is not offered")
	}

	// A bad token comes back in the wizard's form; a good one goes on.
	_, body, h := tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "next": {"welcome"}}, csrf)
	if h.Get("HX-Redirect") != "" || !strings.Contains(body, `name="next" value="welcome"`) {
		t.Fatalf("no key: %s %s", h.Get("HX-Redirect"), body)
	}
	_, body, h = tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "next": {"welcome"},
		"key": {"sk-ant-oat01-" + strings.Repeat("x", 40)}, "preset": {"opus", "sonnet", "haiku"}}, csrf)
	if h.Get("HX-Redirect") != "/welcome" {
		t.Fatalf("saved: %q %s", h.Get("HX-Redirect"), body)
	}
	if b := c.conf().brain(); b.Agent != "sonnet" || len(c.conf().Agents) != 3 {
		t.Fatalf("brain agent %q, agents %v", b.Agent, c.conf().Agents)
	}
	if code, _, _ := tw.do("GET", "/", nil, nil); code != http.StatusOK {
		t.Fatalf("the conversation once the brain has an agent: %d", code)
	}

	// No device at all, then this machine's own (an install with its core
	// has one): it starts here, offline or not, without a device to connect.
	_, body, _ = tw.do("GET", "/welcome", nil, nil)
	if !strings.Contains(body, "Jarvis réfléchit avec Claude Sonnet") || !strings.Contains(body, "attend un appareil") ||
		!strings.Contains(body, `hx-get="/welcome/status"`) || strings.Contains(body, `name="key"`) {
		t.Fatalf("waiting for a machine: %s", body)
	}
	c.store.registerNode("pc", hashSecret("s"))
	if _, body, _ = tw.do("GET", "/welcome/status", nil, nil); !strings.Contains(body, "Jarvis démarre sur cette machine") ||
		strings.Contains(body, "/settings/devices") {
		t.Fatalf("registered, not online yet: %s", body)
	}
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	if _, body, _ = tw.do("GET", "/welcome/status", nil, nil); !strings.Contains(body, "Jarvis démarre sur cette machine") {
		t.Fatalf("starting: %s", body)
	}
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "pc", Agent: "sonnet", Runtime: "claude-code", State: stateIdle})
	setBrainTask(c.core, "b-1")
	_, body, _ = tw.do("GET", "/welcome/status", nil, nil)
	if !strings.Contains(body, "Jarvis est prêt") || !strings.Contains(body, `href="/"`) || strings.Contains(body, "hx-trigger") {
		t.Fatalf("ready: %s", body)
	}
}

// A key the provider refuses stays in the wizard's AI step, with the error
// and the field, "Finish later" still there: nothing is saved and the
// assistant is not "ready". A provider that cannot be reached does not
// hold it back.
func TestWebWelcomeRefusedKey(t *testing.T) {
	keepName(t)
	status := http.StatusUnauthorized
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer fake.Close()
	old := checkEndpoints["anthropic-api"]
	checkEndpoints["anthropic-api"] = fake.URL
	t.Cleanup(func() { checkEndpoints["anthropic-api"] = old })

	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	tw.post("/welcome/name", url.Values{"name": {"Sam"}}, csrf)
	form := url.Values{"kind": {"anthropic-api"}, "next": {"welcome"}, "key": {"sk-ant-api03-" + strings.Repeat("x", 40)}, "preset": {"sonnet"}}
	_, body, h := tw.post("/settings/providers", form, csrf)
	if h.Get("HX-Redirect") != "" {
		t.Fatalf("a refused key went on to %q", h.Get("HX-Redirect"))
	}
	for _, want := range []string{"Clé refusée", `name="key"`, `name="next" value="welcome"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused form lacks %q: %s", want, body)
		}
	}
	if b := c.conf().brain(); b.Agent != "" || len(c.conf().Providers) != 0 || len(c.conf().Agents) != 0 {
		t.Fatalf("a refused key was saved: brain %+v, providers %v", b, c.conf().Providers)
	}
	if _, body, _ := tw.do("GET", "/welcome", nil, nil); strings.Contains(body, "est prêt") || !strings.Contains(body, "/welcome/skip") {
		t.Fatalf("the wizard after a refused key: %s", body)
	}

	// Not reachable: saved, the account says it could not be checked.
	checkEndpoints["anthropic-api"] = "http://127.0.0.1:1/"
	if _, body, h := tw.post("/settings/providers", form, csrf); h.Get("HX-Redirect") != "/welcome" {
		t.Fatalf("an unchecked key: %q %s", h.Get("HX-Redirect"), body)
	}
	if h := c.providerHealth("anthropic"); h == nil || h.Cause != causeNetwork {
		t.Fatalf("health = %+v", h)
	}
}

// Keeping Eriphe leaves the default: nothing set, nothing restarted.
func TestWebWelcomeKeepsEriphe(t *testing.T) {
	keepName(t)
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	if code, _, h := tw.post("/welcome/name", url.Values{"name": {"Eriphe"}}, csrf); code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
		t.Fatalf("named: %d %s", code, h.Get("Location"))
	}
	if b := c.conf().brain(); b.DisplayName != "" || c.conf().assistantName() != "Eriphe" {
		t.Fatalf("brain = %+v", b)
	}
	if _, body, _ := tw.do("GET", "/welcome", nil, nil); !strings.Contains(body, "/welcome?kind=claude-subscription") || !strings.Contains(body, "Avec quelle IA Eriphe") {
		t.Fatal("the name step again")
	}
}

// The wizard can wait: the conversation opens after "Later".
func TestWebWelcomeSkip(t *testing.T) {
	tw, _, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	if code, _, h := tw.do("POST", "/welcome/skip", url.Values{"csrf": {csrf}}, nil); code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("skip: %d %s", code, h.Get("Location"))
	}
	// It says the assistant has no AI yet, and leads back to the wizard.
	if code, body, _ := tw.do("GET", "/", nil, nil); code != http.StatusOK || !strings.Contains(body, "pas encore d&#39;IA") || !strings.Contains(body, `href="/welcome"`) {
		t.Fatalf("after skip: %d", code)
	}
	if code, _, _ := tw.do("GET", "/welcome", nil, nil); code != http.StatusOK {
		t.Fatalf("the wizard after skip: %d", code)
	}
}

// Without a brain, or with its agent in the file, there is no wizard.
func TestWebNoWelcome(t *testing.T) {
	tw, _, _ := catalogWeb(t, nil)
	if code, _, _ := tw.do("GET", "/", nil, nil); code != http.StatusOK {
		t.Fatalf("no brain: %d", code)
	}
	if code, _, h := tw.do("GET", "/welcome", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("the wizard without a brain: %d", code)
	}
	tw, _, _ = catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} })
	if code, _, _ := tw.do("GET", "/", nil, nil); code != http.StatusOK {
		t.Fatalf("brain agent in the file: %d", code)
	}
}

// A join command for this computer's own address warns that another
// machine cannot use it.
func TestWebJoinLocalAddress(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	_, body, _ := tw.post("/settings/machines/join", url.Values{"name": {"desk"}}, csrf)
	if !strings.Contains(body, "-core http://127.0.0.1:8800") || !strings.Contains(body, "ne marche que sur cet ordinateur") ||
		!strings.Contains(body, `href="/settings/account#device-fallback"`) || !strings.Contains(body, "tunnel SSH") {
		t.Fatalf("join on the loopback: %s", body)
	}
	if _, acc, _ := tw.do("GET", "/settings/account", nil, nil); !strings.Contains(acc, `id="device-fallback"`) {
		t.Error("the account page lacks the devices' second address")
	}
	// With the devices' second address, the command gives it: another device uses it.
	tw.w.c.store.set(deviceFallbackKey, "http://100.64.0.2:8800")
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"desk1"}}, csrf)
	if !strings.Contains(body, "-core http://127.0.0.1:8800,http://100.64.0.2:8800") || !strings.Contains(body, "seconde adresse que la commande donne, http://100.64.0.2:8800") ||
		strings.Contains(body, "#device-fallback") {
		t.Fatalf("join on the loopback with a second address: %s", body)
	}
	tw.w.c.store.set(deviceFallbackKey, "")
	tw.c.conf().PublicURL = "http://core.example.org:8800"
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"desk2"}}, csrf)
	if !strings.Contains(body, "-core http://core.example.org:8800") || strings.Contains(body, "ne marche que sur cet ordinateur") {
		t.Fatalf("join with public_url: %s", body)
	}
}
