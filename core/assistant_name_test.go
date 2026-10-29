package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// keepName puts the default name back once a test is over: the words of the
// workspace read it from the process.
func keepName(t *testing.T) {
	t.Cleanup(func() { setAssistantName("") })
}

// An install that never named its assistant keeps Eriphe: in its
// instructions, in the workspace's words, and in its fingerprint, so that
// it is not restarted on the upgrade.
func TestAssistantNameDefault(t *testing.T) {
	keepName(t)
	c := brainCore(t, &BrainConfig{})
	b := c.conf().brain()
	if b.DisplayName != "" || b.displayName() != "Eriphe" || c.conf().assistantName() != "Eriphe" || assistantName() != "Eriphe" {
		t.Fatalf("name = %q / %q", b.DisplayName, c.conf().assistantName())
	}
	// The fingerprint as it was before the name.
	h := sha256.New()
	json.NewEncoder(h).Encode([]any{b.Agent, c.conf().Agents[b.Agent], b.Instructions, b.InstructionsFile, b.Node, b.Resources})
	if old := hex.EncodeToString(h.Sum(nil)); c.assistantSpecHash(*b) != old {
		t.Fatal("the unnamed brain's fingerprint changed: it would restart on the upgrade")
	}
	named := *b
	named.DisplayName = "Eriphe"
	if c.assistantSpecHash(named) != c.assistantSpecHash(*b) {
		t.Fatal("naming it Eriphe changes its fingerprint")
	}
	c.tendAssistants(context.Background(), "tok")
	spec, _ := c.store.taskSpec(brainStatus(c.core).Task)
	if !strings.Contains(spec.AppendSystemPrompt, "# Eriphe — orchestrateur") || !strings.Contains(spec.AppendSystemPrompt, "Tu es Eriphe, l'assistant de") ||
		strings.Contains(spec.AppendSystemPrompt, "{{name}}") {
		t.Fatalf("prompt = %q", truncate(spec.AppendSystemPrompt, 200))
	}
	if got := tr("fr", "devices.sees"); got != "Ce qu'Eriphe voit ici" {
		t.Fatalf("fr = %q", got)
	}
}

// Named through the API, the brain restarts with its name in its
// instructions, its workers speak for it, and the workspace calls it so.
func TestAssistantNameRename(t *testing.T) {
	keepName(t)
	c := brainCore(t, &BrainConfig{})
	ctx := context.Background()
	c.tendAssistants(ctx, "tok")
	id := brainStatus(c.core).Task
	c.store.updateTask(id, map[string]any{"state": stateIdle})

	mux := http.NewServeMux()
	c.assistantRoutes(mux, "secret")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var one assistantView
	if code := call(t, srv, "PUT", "/v1/assistants/eriphe", `{"display_name":"  Jarvis  "}`, &one); code != 200 || one.Spec.DisplayName != "Jarvis" {
		t.Fatalf("put: %d %+v", code, one)
	}
	if c.conf().assistantName() != "Jarvis" || assistantName() != "Jarvis" || c.conf().brain().Name != brainAssistant {
		t.Fatalf("name = %q, id %q", c.conf().assistantName(), c.conf().brain().Name)
	}
	if c.tendAssistants(ctx, "tok"); c.store.get("brain_stopped:"+id) == "" {
		t.Fatal("not restarted on its new name")
	}
	c.store.updateTask(id, map[string]any{"state": stateStopped})
	noWait(c.core, brainAssistant)
	c.tendAssistants(ctx, "tok")
	spec, _ := c.store.taskSpec(brainStatus(c.core).Task)
	if !strings.Contains(spec.AppendSystemPrompt, "# Jarvis — orchestrateur") || !strings.Contains(spec.AppendSystemPrompt, "Tu es Jarvis, l'assistant de") ||
		strings.Contains(spec.AppendSystemPrompt, "Tu es Eriphe") {
		t.Fatalf("prompt = %q", truncate(spec.AppendSystemPrompt, 200))
	}

	// Its workers' answers are relayed as its own.
	task, err := c.runTask(ctx, runRequest{Prompt: "count the lines", Agent: "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := c.store.taskSpec(task.ID)
	if !strings.HasPrefix(ws.AppendSystemPrompt, "Your final message is relayed to the user as Jarvis's.") {
		t.Fatalf("worker voice = %q", ws.AppendSystemPrompt)
	}

	// Kept across a restart.
	again := startDynamicCore(t, c.base, c.store)
	if again.conf().assistantName() != "Jarvis" {
		t.Fatalf("after a restart: %q", again.conf().assistantName())
	}
}

// A name is short and speakable; the file's is read-only.
func TestAssistantNameValidation(t *testing.T) {
	keepName(t)
	for _, ok := range []string{"Eriphe", "Jarvis", "R2-D2", "Mme Irma", "Élise", "O'Neil", "O’Neil", "HAL 9000", strings.Repeat("a", 32)} {
		if err := checkDisplayName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " ", "-Jarvis", "Jar<vis>", "{{user}}", "Jarvis!", "50%", strings.Repeat("a", 33), "a\nb"} {
		if err := checkDisplayName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := normalizeDisplayName("  Mme   Irma "); got != "Mme Irma" {
		t.Errorf("normalized = %q", got)
	}
	c := brainCore(t, &BrainConfig{})
	wantKey(t, c.putAssistant(brainAssistant, Assistant{DisplayName: "Jar<vis>"}), "assistants.badname")
	if c.conf().assistantName() != "Eriphe" {
		t.Fatal("a bad name was kept")
	}
	f := brainCore(t, &BrainConfig{DisplayName: "Alfred"})
	if f.conf().assistantName() != "Alfred" {
		t.Fatalf("the file's name = %q", f.conf().assistantName())
	}
	wantKey(t, f.putAssistant(brainAssistant, Assistant{DisplayName: "Jarvis"}), "catalog.readonly")
	wantKey(t, f.editAssistant(brainAssistant, assistantEdit{DisplayName: "Jarvis", SetName: true}), "catalog.readonly")
}

// The workspace's words name the assistant as the user calls it, with
// French elision; the software's name stays.
func TestAssistantNameWords(t *testing.T) {
	keepName(t)
	setAssistantName("Jarvis")
	for _, c := range []struct{ lang, key, want string }{
		{"fr", "devices.sees", "Ce que Jarvis voit ici"},
		{"fr", "devices.can", "Ce que Jarvis peut faire ici"},
		{"fr", "ha.drive", "Jarvis pilote la maison"},
		{"fr", "tasks.label", "Tâches de Jarvis"},
		{"fr", "chat.placeholder", "Écris à Jarvis…"},
		{"en", "devices.sees", "What Jarvis sees here"},
		{"en", "tasks.label", "Jarvis's tasks"},
		{"en", "ha.drive", "Jarvis drives the house"},
	} {
		if got := tr(c.lang, c.key); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.lang, c.key, got, c.want)
		}
	}
	if got := tr("fr", "drive.noapp"); !strings.Contains(got, "L'application Eriphe pour Google") {
		t.Errorf("the Google app is renamed: %q", got)
	}
	if got := tr("fr", "folders.access.droplead"); !strings.HasPrefix(got, "Jarvis lit") || !strings.Contains(got, "« Eriphe »") {
		t.Errorf("the results folder keeps its name: %q", got)
	}
	setAssistantName("Alfred")
	if got := tr("fr", "tasks.label"); got != "Tâches d'Alfred" {
		t.Errorf("elided = %q", got)
	}
	if got := withName("fr", "Que {{name}} ou l'aide {{name}}", "Ulysse"); got != "Qu'Ulysse ou l'aide Ulysse" {
		t.Errorf("elision = %q", got)
	}
}

// Settings → Agents names the brain, and the pages follow.
func TestWebAssistantName(t *testing.T) {
	keepName(t)
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{} })
	if _, body, _ := tw.do("GET", "/settings/agents", nil, nil); !strings.Contains(body, `name="display_name" value="Eriphe"`) {
		t.Fatal("no name field")
	}
	if _, _, h := tw.post("/settings/assistants/eriphe", url.Values{"display_name": {"Jarvis"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %v", h)
	}
	if c.conf().assistantName() != "Jarvis" {
		t.Fatalf("name = %q", c.conf().assistantName())
	}
	if _, _, h := tw.post("/settings/assistants/eriphe", url.Values{"display_name": {"<b>"}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("a bad name saved")
	}
	_, body, _ := tw.do("GET", "/", nil, nil)
	for _, want := range []string{"Écris à Jarvis…", "<title>Conversation · Jarvis</title>", `content="Jarvis"`, "<span>Jarvis</span>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("conversation lacks %q: %s", want, truncate(body, 400))
		}
	}
	// The installed app is called so too.
	code, raw, h := tw.do("GET", "/manifest.webmanifest", nil, nil)
	var m map[string]any
	if code != 200 || json.Unmarshal([]byte(raw), &m) != nil || m["name"] != "Jarvis" || m["short_name"] != "Jarvis" ||
		m["start_url"] != "/" || !strings.Contains(h.Get("Content-Type"), "manifest+json") {
		t.Fatalf("manifest: %d %s", code, raw)
	}
	tw.do("POST", "/lang", url.Values{"csrf": {csrf}, "lang": {"en"}}, nil)
	if _, body, _ = tw.do("GET", "/", nil, nil); !strings.Contains(body, "Write to Jarvis…") {
		t.Fatalf("conversation in English: %s", truncate(body, 400))
	}
}

// Home Assistant reads the assistant's name with its voice token, for its
// conversation agent.
func TestVoiceName(t *testing.T) {
	keepName(t)
	c, tok := voiceCore(t)
	srv := httptest.NewServer(http.HandlerFunc(c.handleVoiceInfo))
	defer srv.Close()
	get := func(token string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var v struct{ Name string }
		json.NewDecoder(resp.Body).Decode(&v)
		return resp.StatusCode, v.Name
	}
	if code, _ := get(c.token); code != http.StatusUnauthorized {
		t.Fatalf("another token: %d", code)
	}
	if code, name := get(tok); code != 200 || name != "Eriphe" {
		t.Fatalf("default: %d %q", code, name)
	}
	setBrain(c, &BrainConfig{Agent: "small", DisplayName: "Jarvis"})
	if code, name := get(tok); code != 200 || name != "Jarvis" {
		t.Fatalf("named: %d %q", code, name)
	}
}
