package main

import (
	"net/http"
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
	// First its name: Eriphe unless the user picks another.
	_, body, _ := tw.do("GET", "/welcome", nil, nil)
	for _, want := range []string{"Bienvenue dans Eriphe", "Comment veux-tu appeler ton assistant ?", `action="/welcome/name"`,
		`name="name" value="Eriphe"`, "/welcome/skip"} {
		if !strings.Contains(body, want) {
			t.Errorf("the name step lacks %q", want)
		}
	}
	if strings.Contains(body, `name="key"`) {
		t.Error("the AI account comes before the name")
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

	// Then the AI account.
	_, body, _ = tw.do("GET", "/welcome", nil, nil)
	for _, want := range []string{"Relie l&#39;IA de Jarvis", "Avec quelle IA Jarvis réfléchit-il ?", "claude setup-token", `name="key"`,
		`name="next" value="welcome"`, `name="kind" value="claude-subscription"`, "/welcome?kind=chatgpt-subscription", "/welcome/skip"} {
		if !strings.Contains(body, want) {
			t.Errorf("the wizard lacks %q", want)
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

	// No machine yet, then one, then the brain at work.
	_, body, _ = tw.do("GET", "/welcome", nil, nil)
	if !strings.Contains(body, "Jarvis réfléchit avec Claude Sonnet") || !strings.Contains(body, "attend un appareil") ||
		!strings.Contains(body, `hx-get="/welcome/status"`) || strings.Contains(body, `name="key"`) {
		t.Fatalf("waiting for a machine: %s", body)
	}
	c.store.registerNode("pc", hashSecret("s"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	if _, body, _ = tw.do("GET", "/welcome/status", nil, nil); !strings.Contains(body, "Jarvis démarre sur ton appareil") {
		t.Fatalf("starting: %s", body)
	}
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Node: "pc", Agent: "sonnet", Runtime: "claude-code", State: stateIdle})
	setBrainTask(c.core, "b-1")
	_, body, _ = tw.do("GET", "/welcome/status", nil, nil)
	if !strings.Contains(body, "Jarvis est prêt") || !strings.Contains(body, `href="/"`) || strings.Contains(body, "hx-trigger") {
		t.Fatalf("ready: %s", body)
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
	if _, body, _ := tw.do("GET", "/welcome", nil, nil); !strings.Contains(body, `name="key"`) || !strings.Contains(body, "Relie l&#39;IA d&#39;Eriphe") {
		t.Fatal("the name step again")
	}
}

// The wizard can wait: the conversation opens after "Later".
func TestWebWelcomeSkip(t *testing.T) {
	tw, _, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	if code, _, h := tw.do("POST", "/welcome/skip", url.Values{"csrf": {csrf}}, nil); code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("skip: %d %s", code, h.Get("Location"))
	}
	if code, _, _ := tw.do("GET", "/", nil, nil); code != http.StatusOK {
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
	if !strings.Contains(body, "-core http://127.0.0.1:8800") || !strings.Contains(body, "ne marche que sur cet ordinateur") {
		t.Fatalf("join on the loopback: %s", body)
	}
	tw.c.conf().PublicURL = "http://core.example.org:8800"
	_, body, _ = tw.post("/settings/machines/join", url.Values{"name": {"desk2"}}, csrf)
	if !strings.Contains(body, "-core http://core.example.org:8800") || strings.Contains(body, "ne marche que sur cet ordinateur") {
		t.Fatalf("join with public_url: %s", body)
	}
}
