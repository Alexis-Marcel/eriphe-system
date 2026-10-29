package main

import (
	"net/http"
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

// The Memory page shows what the assistant knows about the user, the
// pinned first and marked, and does the interview again on demand.
func TestMemoryAboutAndInterview(t *testing.T) {
	keepName(t)
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	if err := c.editAssistant(brainAssistant, assistantEdit{Agent: "small", DisplayName: "Jarvis", SetName: true}); err != nil {
		t.Fatal(err)
	}
	m := c.mem
	pinned := mustAdd(t, m, &Memory{Kind: kindProfile, Subject: "owner", Text: "Sam est menuisier à son compte"})
	plain := mustAdd(t, m, &Memory{Kind: kindFact, Subject: "owner", Text: "Sam habite 1 rue de l'Exemple"})
	mustAdd(t, m, &Memory{Kind: kindPerson, Subject: "person:lea", Text: "Léa, une cliente de Sam"})
	mustAdd(t, m, &Memory{Kind: kindEpisode, Subject: "owner", Text: "Sam a fini un chantier"})

	about := func(body string) string {
		i := strings.Index(body, `id="mem-about"`)
		j := strings.Index(body, `id="mem-stats"`)
		if i < 0 || j < i {
			t.Fatalf("no section: %s", body)
		}
		return body[i:j]
	}
	_, body, _ := tw.do("GET", "/memory", nil, nil)
	sec := about(body)
	for _, want := range []string{"Ce que Jarvis sait de toi", "Faire l&#39;interview", `hx-post="/memory/interview"`,
		"Sam est menuisier à son compte", "Sam habite 1 rue de l&#39;Exemple", `hx-post="/memory/` + itoa(int(plain.ID)) + `/pin"`,
		`hx-post="/memory/` + itoa(int(pinned.ID)) + `/forget"`, `hx-get="/memory/` + itoa(int(plain.ID)) + `/edit" hx-target="closest .mem"`} {
		if !strings.Contains(sec, want) {
			t.Errorf("the section lacks %q", want)
		}
	}
	if strings.Count(sec, `class="pinned"`) != 1 || strings.Index(sec, "menuisier") > strings.Index(sec, "rue de l") {
		t.Error("the pinned one is not first, or not alone marked")
	}
	if strings.Contains(sec, "Léa") || strings.Contains(sec, "chantier") {
		t.Error("the section shows what is not about the user")
	}
	if _, body, _ := tw.do("GET", "/memory?view=profile", nil, nil); strings.Contains(body, `id="mem-about"`) {
		t.Error("the section on another view")
	}
	tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: "en"}})
	_, body, _ = tw.do("GET", "/memory", nil, nil)
	if sec := about(body); !strings.Contains(sec, "What Jarvis knows about you") || !strings.Contains(sec, "Do the interview") {
		t.Errorf("English: %s", sec)
	}

	// The button sends the first-run turn and opens the conversation.
	before := len(firstRunTurns(t, c))
	_, _, h := tw.post("/memory/interview", nil, csrf)
	if h.Get("HX-Redirect") != "/" || len(firstRunTurns(t, c)) != before+1 {
		t.Fatalf("interview: %q, %d turns", h.Get("HX-Redirect"), len(firstRunTurns(t, c)))
	}
}

// Nothing known yet: the section says so, and offers the interview.
func TestMemoryAboutEmpty(t *testing.T) {
	keepName(t)
	tw, _ := newMemoryWeb(t)
	_, body, _ := tw.do("GET", "/memory", nil, nil)
	if !strings.Contains(body, "Ce qu&#39;Eriphe sait de toi") || !strings.Contains(body, "Rien encore. Une courte interview") ||
		!strings.Contains(body, `hx-post="/memory/interview"`) {
		t.Fatalf("empty section: %s", body)
	}
}

// Without a brain, no interview to ask for.
func TestMemoryInterviewNoBrain(t *testing.T) {
	tw, _, csrf := catalogWeb(t, func(cfg *Config) { noAgents(cfg); cfg.Brain = &BrainConfig{} })
	tw.do("POST", "/welcome/skip", url.Values{"csrf": {csrf}}, nil)
	if code, _, _ := tw.post("/memory/interview", nil, csrf); code != http.StatusConflict {
		t.Fatalf("interview without an agent: %d", code)
	}
}

// The standing instructions hold the interview.
func TestBrainPromptFirstRun(t *testing.T) {
	raw, _ := os.ReadFile("brain-prompt.md")
	p := string(raw)
	for _, want := range []string{"## Premier lancement", "· premier lancement]", "refais-moi l'interview", "/settings/devices#connect",
		"Une question à la fois", "court résumé", "tu n'insistes pas", "À propos de moi", "update_memory pinned", "sujet `owner`"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
}
