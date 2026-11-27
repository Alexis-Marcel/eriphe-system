package main

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

// firstRunTurns are the first-run turns in the conversation.
func firstRunTurns(t *testing.T, c *core) []Message {
	t.Helper()
	msgs, err := c.store.messagesAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []Message
	for _, m := range msgs {
		if m.Via == firstRunVia {
			out = append(out, m)
		}
	}
	return out
}

// A new install: once the wizard is over, opening the conversation gives
// the brain its first-run turn, once; the conversation does not show it.
func TestFirstRunOnceAfterWizard(t *testing.T) {
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	if got := c.store.get(firstRunKey); got != firstRunDue {
		t.Fatalf("a new install: %q", got)
	}
	// Still in the wizard: nothing yet.
	tw.do("GET", "/", nil, nil)
	if n := len(firstRunTurns(t, c.core)); n != 0 {
		t.Fatalf("during the wizard: %d turns", n)
	}
	_, _, h := tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "next": {"welcome"},
		"key": {"sk-ant-oat01-" + strings.Repeat("x", 40)}, "preset": {"sonnet"}}, csrf)
	if h.Get("HX-Redirect") != "/welcome" || c.conf().brain().Agent == "" {
		t.Fatalf("the AI account: %q", h.Get("HX-Redirect"))
	}
	code, body, _ := tw.do("GET", "/", nil, nil)
	turns := firstRunTurns(t, c.core)
	if code != 200 || len(turns) != 1 || turns[0].Role != roleUser || turns[0].Text != firstRunText {
		t.Fatalf("after the wizard: %d %+v", code, turns)
	}
	if strings.Contains(body, firstRunText) || strings.Contains(body, `id="m`+itoa(turns[0].Seq)+`"`) {
		t.Fatal("the conversation shows the first-run turn")
	}
	if un, _ := c.store.undelivered(); len(un) == 0 || un[len(un)-1].Seq != turns[0].Seq {
		t.Fatalf("not waiting for the brain: %+v", un)
	}
	// Never twice, even after a restart.
	tw.do("GET", "/", nil, nil)
	c.noteFirstRun(c.conf())
	c.firstRun()
	if n := len(firstRunTurns(t, c.core)); n != 1 || c.store.get(firstRunKey) != firstRunDone {
		t.Fatalf("fired %d times, %q", n, c.store.get(firstRunKey))
	}
}

// The wizard skipped without an AI account: no brain to talk to, nothing
// sent, and still due for when it gets one.
func TestFirstRunWaitsForAnAgent(t *testing.T) {
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	tw.do("POST", "/welcome/skip", url.Values{"csrf": {csrf}}, nil)
	if code, _, _ := tw.do("GET", "/", nil, nil); code != 200 || len(firstRunTurns(t, c.core)) != 0 || c.store.get(firstRunKey) != firstRunDue {
		t.Fatalf("skipped: %d, %q", code, c.store.get(firstRunKey))
	}
}

// An install from before this release is never interviewed: one whose
// brain has an agent already, one with a conversation, one whose brain
// has spoken.
func TestFirstRunNotOnExistingInstall(t *testing.T) {
	// Its brain has an agent at the first start of this release.
	tw, c, _ := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{Agent: "sonnet"} })
	tw.do("GET", "/", nil, nil)
	if got := c.store.get(firstRunKey); got != firstRunDone || len(firstRunTurns(t, c.core)) != 0 {
		t.Fatalf("with an agent: %q", got)
	}

	// A conversation already, the brain's agent not set yet.
	_, c, _ = catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, firstRunKey) // as before this release
	c.store.addMessage(roleUser, "Bonjour", true)
	c.noteFirstRun(c.conf())
	if got := c.store.get(firstRunKey); got != firstRunDone {
		t.Fatalf("with a conversation: %q", got)
	}

	// Due, but the brain has written already: it is not sent.
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	tw.post("/settings/providers", url.Values{"kind": {"claude-subscription"}, "next": {"welcome"},
		"key": {"sk-ant-oat01-" + strings.Repeat("x", 40)}, "preset": {"sonnet"}}, csrf)
	c.store.addMessage(roleBrain, "Salut", true)
	tw.do("GET", "/", nil, nil)
	if got := c.store.get(firstRunKey); got != firstRunDone || len(firstRunTurns(t, c.core)) != 0 {
		t.Fatalf("the brain has spoken: %q", got)
	}
}

// The standing instructions hold the interview.
func TestBrainPromptFirstRun(t *testing.T) {
	raw, _ := os.ReadFile("brain-prompt.md")
	p := string(raw)
	for _, want := range []string{"## Premier lancement", "· premier lancement]", "refais-moi l'interview", "/settings/devices#connect",
		"Une question à la fois", "court résumé", "tu n'insistes pas", "À propos de moi", "« Faire connaissance »", "dans le nœud de leur sujet"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
}
