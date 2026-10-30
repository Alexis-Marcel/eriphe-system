package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A role's powers hold together, and only a role that receives the
// conversation may have its trigger.
func TestAssistantRolePowers(t *testing.T) {
	for name, r := range assistantRoles {
		if err := r.check(); err != nil {
			t.Errorf("role %s: %v", name, err)
		}
	}
	brain := assistantRoles[assistantRoleBrain]
	for _, p := range assistantPowers {
		if !brain.can(p) {
			t.Errorf("the brain cannot %s", p)
		}
	}
	if assistantRoles[roleTest].can(powerLaunchesTasks) {
		t.Error("a role without powers launches tasks")
	}
	for _, bad := range []assistantRole{
		{Powers: []string{"flies"}},
		{Powers: []string{powerReceivesConversation}},
		{Powers: []string{powerLendsResources}},
		{Triggers: []string{triggerConversation}},
	} {
		if bad.check() == nil {
			t.Errorf("role %+v accepted", bad)
		}
	}

	c := brainCore(t, &BrainConfig{})
	if err := c.putAssistant("watch", Assistant{Role: roleTest, Triggers: []string{triggerConversation}}); err == nil ||
		!strings.Contains(err.Error(), "receives the conversation") {
		t.Fatalf("the conversation for a role without it: %v", err)
	}
	// At most one: a second brain, even under another name, is refused.
	if err := c.putAssistant("brain2", Assistant{Role: assistantRoleBrain}); err == nil {
		t.Fatal("two brains")
	}
}

// An assistant reports to the brain, and only to it.
func TestAssistantReportsTo(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	if err := c.putAssistant("watch", Assistant{Role: roleTest, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := c.conf().reportsTo(c.conf().Assistants["watch"]); got != brainAssistant {
		t.Fatalf("reports to %q by default", got)
	}
	if got := c.conf().reportsTo(c.conf().Assistants[brainAssistant]); got != "" {
		t.Fatalf("the brain reports to %q", got)
	}
	if err := c.putAssistant("watch", Assistant{Role: roleTest, Disabled: true, ReportsTo: brainAssistant}); err != nil {
		t.Fatalf("reports_to the brain: %v", err)
	}
	for _, to := range []string{"nobody", "watch2"} {
		c.putAssistant("watch2", Assistant{Role: roleTest, Disabled: true})
		if err := c.putAssistant("watch", Assistant{Role: roleTest, Disabled: true, ReportsTo: to}); err == nil {
			t.Fatalf("reports_to %s accepted", to)
		}
	}
	if err := c.putAssistant(brainAssistant, Assistant{ReportsTo: "watch"}); err == nil {
		t.Fatal("the brain reports to an assistant")
	}
	if a := c.conf().Assistants["watch"]; a.ReportsTo != brainAssistant {
		t.Fatalf("watch = %+v", a)
	}
}

// The page says how an assistant is doing in plain words.
func TestPlainStatus(t *testing.T) {
	view := func(agent string, disabled bool, on ...string) assistantView {
		v := assistantView{Spec: Assistant{Agent: agent, Disabled: disabled}}
		for _, typ := range []string{"Running", "Busy", "Ready"} {
			v.Status.Conditions = append(v.Status.Conditions, assistantCondition{Type: typ, Status: strings.Contains(strings.Join(on, ","), typ)})
		}
		return v
	}
	for _, c := range []struct {
		v    assistantView
		want string
	}{
		{view("", false, "Running"), "noai"},
		{view("sonnet", true, "Running"), "stopped"},
		{view("sonnet", false), "stopped"},
		{view("sonnet", false, "Running", "Busy"), "replying"},
		{view("sonnet", false, "Running", "Ready"), "available"},
		{view("sonnet", false, "Running"), "available"},
	} {
		if got := plainStatus(c.v); got != c.want {
			t.Errorf("%+v: %s, want %s", c.v.Status.Conditions, got, c.want)
		}
	}
}

// Settings → Agents opens on the brain: its name, how it is doing, the
// agent it thinks with, its tasks, and its details folded; its assistants
// below, never the brain among them.
func TestWebBrainCard(t *testing.T) {
	keepName(t)
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{}; cfg.Agents["opus"] = cfg.Agents["sonnet"] })
	n := len(c.conf().Agents)
	_, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	hasAll(t, "brain card", body, "Eriphe, ton cerveau", "Arrêté", `name="display_name" value="Eriphe"`, "Réfléchit avec",
		`hx-post="/settings/brain-agent"`, "Ses tâches", fmt.Sprintf("parmi tes %d agents", n),
		`<details class="advanced-opts brain-details">`, "Détails techniques", "Outils en plus", "les lectures libres du catalogue",
		"Les assistants d&#39;Eriphe", "Aucun pour l&#39;instant. Ce seront des aides")
	hasNone(t, "brain card", body, `hx-post="/settings/assistants/eriphe"`, `class="assistant"`, "Running", "assistants.")
	// After the agents it chooses from.
	if i, j := strings.Index(body, "ton cerveau"), strings.Index(body, `class="grid agents-grid"`); i < 0 || j < 0 || i < j {
		t.Fatal("the brain is not after the agents")
	}
	if strings.Count(body, `select id="brain-agent"`) != 1 || strings.Count(body, `hx-post="/settings/brain-agent"`) != 1 {
		t.Fatal("the brain's agent chosen in more than one place")
	}
	// Its failure shows.
	c.updateAssistant(brainAssistant, func(st *AssistantStatus) { st.LastError = "prompt: no such file" })
	if _, body, _ = tw.do("GET", "/settings/agents", nil, nil); !strings.Contains(body, "prompt: no such file") {
		t.Fatal("no error")
	}

	// One form, one Save: its name, its agent and its tools.
	_, _, h := tw.post("/settings/brain-agent", url.Values{"display_name": {"Jarvis"}, "agent": {c.conf().brain().Agent}, "tools": {"docs"}}, csrf)
	if h.Get("HX-Redirect") != "/settings/agents?brain=saved" {
		t.Fatalf("save: %v", h)
	}
	if b := c.conf().brain(); b.displayName() != "Jarvis" || strings.Join(b.Tools, ",") != "docs" {
		t.Fatalf("brain = %+v", b)
	}
	if _, _, h := tw.post("/settings/brain-agent", url.Values{"display_name": {"<b>"}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("a bad name saved")
	}

	// An assistant of the brain's: below, reporting to it.
	if err := c.putAssistant("watch", Assistant{Role: roleTest, DisplayName: "Vigie", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	_, body, _ = tw.do("GET", "/settings/agents?brain=saved", nil, nil)
	hasAll(t, "helpers", body, "Jarvis redémarre", "Jarvis, ton cerveau", "Les assistants de Jarvis", `id="assistant-watch"`, "Vigie",
		"Rend compte à Jarvis", `hx-post="/settings/assistants/watch"`)
	hasNone(t, "helpers", body, "Aucun pour l&#39;instant. Ce seront des aides")
	if strings.Count(body, `class="assistant"`) != 1 {
		t.Fatal("the brain listed among its assistants")
	}

	// In English.
	tw.do("POST", "/lang", url.Values{"csrf": {csrf}, "lang": {"en"}}, nil)
	_, body, _ = tw.do("GET", "/settings/agents", nil, nil)
	hasAll(t, "in English", body, "Jarvis, your brain", "Stopped", "Thinks with", "Its tasks", fmt.Sprintf("among your %d agents", n),
		"Technical details", "Extra tools", "Jarvis&#39;s assistants", "Reports to Jarvis")
}

// "Check now" asks the registry at once, a minute apart at most, and
// shows when it last did.
func TestWebCheckUpdatesNow(t *testing.T) {
	if updateEvery != time.Hour {
		t.Fatalf("checked every %s", updateEvery)
	}
	tw, h := newUpdatesWeb(t)
	_, body, _ := tw.do("GET", "/settings/devices", nil, nil)
	hasAll(t, "devices", body, `hx-post="/settings/updates/check"`, "Vérifier maintenant", "Vérifié à ")
	if code, _, _ := tw.do("POST", "/settings/updates/check", nil, map[string]string{"HX-Request": "true"}); code != 403 {
		t.Fatalf("without CSRF: %d", code)
	}
	before := tw.c.updates.checked()
	code, body, _ := tw.do("POST", "/settings/updates/check", nil, h)
	at := tw.c.updates.checked()
	if code != 200 || !at.After(before) {
		t.Fatalf("check: %d, %s then %s", code, before, at)
	}
	hasAll(t, "check", body, `id="updates"`, "Vérifié à "+at.Local().Format("15:04"))
	hasNone(t, "check", body, "<html")
	code, _, hdr := tw.do("POST", "/settings/updates/check", nil, h)
	if code != 429 || !strings.Contains(hdr.Get("HX-Trigger"), "moins d") || !tw.c.updates.checked().Equal(at) {
		t.Fatalf("again at once: %d %v", code, hdr)
	}
	// A minute later, a new release: the page reloads, its cards say it.
	tw.c.updates.mu.Lock()
	tw.c.updates.asked = time.Now().Add(-checkNowEvery)
	tw.c.updates.mu.Unlock()
	tw.c.updates.base = newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.2.0"}).srv.URL
	if _, _, hdr := tw.do("POST", "/settings/updates/check", nil, h); hdr.Get("HX-Refresh") != "true" || tw.c.updates.version("eriphe-core") != "0.2.0" {
		t.Fatalf("a new release: %v", hdr)
	}
}
