package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// The tests never reach a real AI provider: a key saved in a test is
// checked against a port nothing listens on, unless the test serves one.
func TestMain(m *testing.M) {
	for kind := range checkEndpoints {
		checkEndpoints[kind] = "http://127.0.0.1:1/"
	}
	os.Exit(m.Run())
}

// Raw errors become a sentence with the likely cause, in both languages,
// the raw text kept for Details.
func TestExplainErrors(t *testing.T) {
	keepName(t)
	setAssistantName("Atlas")
	for _, c := range []struct {
		lang, where, raw, cause, want string
		soft, action                  bool
	}{
		{"fr", whereTool, `calling "initialize": sending "initialize": Unauthorized`, causeAuth, "Le service a refusé la connexion : reconnecte-le", false, false},
		{"en", whereTool, `calling "initialize": sending "initialize": Unauthorized`, causeAuth, "The service refused the connection: reconnect it", false, false},
		{"fr", whereTool, "service mail is pending on pc-bureau", causePending, "Il démarre sur son appareil", true, false},
		{"fr", whereTask, "the agent runs model openrouter/google/gemini-3-pro-image-preview, not openrouter/moonshotai/kimi-k3.1-pro: is the key of its provider there?",
			causeModel, "L'agent n'a pas pu utiliser son modèle", false, true},
		{"en", whereTask, "the agent runs model a, not b: is the key of its provider there?", causeModel, "The agent could not use its model", false, true},
		{"fr", whereBrain, "API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)", causeNetwork,
			"injoignable depuis l'appareil où tourne Atlas", false, true},
		{"fr", whereBrain, "Failed to authenticate. API Error: 401 invalid x-api-key", causeAuth, "Le compte IA d'Atlas a refusé sa clé", false, true},
		{"en", whereBrain, "API Error: 429 rate_limit_error", causeQuota, "Atlas's AI account reached its limit", false, true},
		{"fr", whereBrain, "something odd", causeUnknown, "Atlas a rencontré une erreur inattendue", false, true},
	} {
		p := explain(c.lang, c.where, c.raw)
		if p.Cause != c.cause || !strings.Contains(p.Text, c.want) || p.Detail != c.raw || p.Soft != c.soft || (p.Href != "") != c.action {
			t.Errorf("%s %s %q = %+v", c.lang, c.where, c.raw, p)
		}
	}
	if p := explain("fr", whereBrain, "API Error: 401"); p.Action != "Vérifier le compte IA" || p.Href != accountsHref {
		t.Errorf("brain action = %+v", p)
	}
	if p := explain("en", whereBrain, "API Error: 401"); p.Action != "Check the AI account" {
		t.Errorf("brain action = %+v", p)
	}
}

// The brain's failure is an error card with its name, the cause, "Check the
// AI account" and "Try again", the raw error folded in Details; never its
// raw error as its message.
func TestBrainErrorCard(t *testing.T) {
	keepName(t)
	tw := newTestWeb(t)
	setAssistantName("Atlas") // after the core, which sets its own
	raw := "API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)"
	m := Message{Seq: 7, Time: time.Now(), Role: roleError, Via: "turn", Text: raw}
	for lang, wants := range map[string][]string{
		"fr": {"Atlas n&#39;a pas pu répondre", "injoignable depuis l&#39;appareil où tourne Atlas", "Vérifier le compte IA",
			`href="/settings/agents#prov-title"`, "Réessayer", `hx-post="/brain/retry"`, "<summary>Détails</summary>", "ECONNREFUSED"},
		"en": {"Atlas could not answer", "cannot be reached from the device Atlas runs on", "Check the AI account", "Try again", "<summary>Details</summary>"},
	} {
		html, err := tw.w.fragment(lang, "home", "message", m)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(html, want) {
				t.Errorf("%s card lacks %q: %s", lang, want, html)
			}
		}
		if strings.Contains(html, `class="bubble"`) {
			t.Errorf("%s: the error is shown as a message: %s", lang, html)
		}
	}
	m.Via, m.Text = "start", "exit status 1"
	if html, _ := tw.w.fragment("fr", "home", "message", m); !strings.Contains(html, "Atlas n&#39;arrive pas à démarrer") ||
		!strings.Contains(html, "Son agent s&#39;arrête dès le démarrage") {
		t.Errorf("start card: %s", html)
	}
}

// A turn that fails leaves one error card and no message of the brain; the
// conversation stops showing it as thinking, until "Try again" gives it
// the turn once more.
func TestBrainFailedTurn(t *testing.T) {
	keepName(t)
	c := brainCore(t, &BrainConfig{})
	c.tendAssistants(context.Background(), "tok")
	id := brainStatus(c.core).Task
	c.updateAssistant(brainAssistant, func(st *AssistantStatus) { st.Quiet = false })
	tw := newTestWebFor(t, c.core)

	c.store.addMessage(roleUser, "Trie mes factures", true)
	c.store.updateTask(id, map[string]any{"state": stateRunning})
	if got := tw.w.brainState(); got != "thinking" {
		t.Fatalf("state = %q", got)
	}
	raw := "API Error: Connection refused (ECONNREFUSED)"
	c.store.addEvents(id, []EventRow{
		{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": raw}},
		{Seq: 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success", "result": raw}},
	})
	bt, _ := c.store.task(id)
	c.relayBrainOutput(theBrain(c.core), bt)
	msgs, _ := c.store.messagesAfter(0, 50)
	var errs int
	for _, m := range msgs {
		if m.Role == roleBrain {
			t.Errorf("raw error relayed as the brain's message: %q", m.Text)
		}
		if m.Role == roleError {
			errs++
			if m.Text != raw || m.Via != "turn" {
				t.Errorf("error message = %+v", m)
			}
		}
	}
	if errs != 1 {
		t.Fatalf("%d error cards", errs)
	}
	// Still running (its harness retries, or an event woke it): no endless dots.
	if got := tw.w.brainState(); got != "" {
		t.Errorf("state after the failure = %q", got)
	}
	// Another failure before I say anything: still one card.
	c.store.addEvents(id, []EventRow{{Seq: 3, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success", "is_error": true, "result": raw}}})
	c.relayBrainOutput(theBrain(c.core), bt)
	if n := countRole(t, c.core, roleError); n != 1 {
		t.Errorf("%d error cards after a second failure", n)
	}
	// The brain never reads its own failures.
	if und, _ := c.store.undelivered(); len(und) != 0 {
		t.Errorf("undelivered = %+v", und)
	}

	csrf := tw.setup()
	if code, _, h := tw.post("/brain/retry", url.Values{}, csrf); code != 200 || h.Get("HX-Trigger") == "" {
		t.Fatalf("retry: %d %v", code, h)
	}
	und, _ := c.store.undelivered()
	if len(und) != 1 || und[0].Text != "Trie mes factures" {
		t.Errorf("after retry, undelivered = %+v", und)
	}
	if got := tw.w.brainState(); got != "thinking" {
		t.Errorf("state after retry = %q", got)
	}
	// A new failure after the retry gets its card.
	c.store.addEvents(id, []EventRow{{Seq: 4, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success", "auth_error": true, "result": "Invalid API key"}}})
	c.relayBrainOutput(theBrain(c.core), bt)
	if n := countRole(t, c.core, roleError); n != 2 {
		t.Errorf("%d error cards after a failed retry", n)
	}
	// Its account is marked, and the brain's card says so too.
	c.noteAgentHealth(bt, []EventRow{{Type: "turn_end", Data: map[string]any{"subtype": "success", "auth_error": true, "result": "Invalid API key"}}})
	_, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	i, j := strings.Index(body, "brain-card"), strings.Index(body, "brain-form")
	if i < 0 || j < i || !strings.Contains(body[i:j], "Clé refusée") {
		t.Error("the brain's card does not say its key was refused")
	}
}

// What a node reports of a failed turn, old nodes included (no error mark).
func TestTurnFailure(t *testing.T) {
	for _, c := range []struct {
		e   EventRow
		bad bool
	}{
		{EventRow{Type: "text", Data: map[string]any{"text": "Failed to authenticate. API Error: 401 OAuth access token is invalid."}}, true},
		{EventRow{Type: "text", Data: map[string]any{"text": "API Error: Connection refused"}}, true},
		{EventRow{Type: "text", Data: map[string]any{"text": "Overloaded, try later", "error": true}}, true},
		{EventRow{Type: "text", Data: map[string]any{"text": "Voici le résumé de tes mails."}}, false},
		{EventRow{Type: "turn_end", Data: map[string]any{"subtype": "success", "result": "Fait."}}, false},
		{EventRow{Type: "turn_end", Data: map[string]any{"subtype": "error_during_execution", "result": "401 Unauthorized"}}, true},
		{EventRow{Type: "turn_end", Data: map[string]any{"subtype": "error_max_turns", "result": "J'en étais à…"}}, true},
	} {
		raw, bad := turnFailure(c.e)
		if bad != c.bad {
			t.Errorf("%+v: %v", c.e, bad)
		}
		if c.e.Data["subtype"] == "error_max_turns" && raw != "error_max_turns" {
			t.Errorf("a turn cut by a limit reads as %q", raw)
		}
	}
}

func countRole(t *testing.T, c *core, role string) int {
	t.Helper()
	msgs, _ := c.store.messagesAfter(0, 200)
	n := 0
	for _, m := range msgs {
		if m.Role == role {
			n++
		}
	}
	return n
}

// An account whose key was refused when an agent used it says so, and so
// do its agents, instead of "Ready"; a turn that goes through clears it.
func TestProviderHealthFromUse(t *testing.T) {
	tw, c, _ := catalogWeb(t, nil)
	c.store.registerNode("pc", hashSecret("s"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	task := &TaskRow{Agent: "sonnet"}
	c.noteAgentHealth(task, []EventRow{{Type: "turn_end", Data: map[string]any{"subtype": "success", "auth_error": true,
		"result": "Failed to authenticate. API Error: 401 OAuth token has expired"}}})
	if h := c.providerHealth("anthropic"); h == nil || h.Cause != causeAuth || h.label() != "refused" {
		t.Fatalf("health = %+v", h)
	}
	_, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	for _, want := range []string{"health-refused", "Clé refusée", "La clé a été refusée", "OAuth token has expired"} {
		if !strings.Contains(body, want) {
			t.Errorf("agents page lacks %q", want)
		}
	}
	if strings.Contains(body, `<span class="status on"`) {
		t.Error("an agent of the refused account still shows as ready")
	}
	// A network failure is not the account's.
	c.clearProviderHealth("anthropic")
	c.noteAgentHealth(task, []EventRow{{Type: "text", Data: map[string]any{"text": "API Error: Connection refused (ECONNREFUSED)", "error": true}}})
	if h := c.providerHealth("anthropic"); h != nil {
		t.Errorf("network failure marked the account: %+v", h)
	}
	c.noteAgentHealth(task, []EventRow{{Type: "turn_end", Data: map[string]any{"subtype": "success", "is_error": true, "result": "API Error: 429 rate_limit_error"}}})
	if h := c.providerHealth("anthropic"); h == nil || h.label() != "down" {
		t.Errorf("quota = %+v", h)
	}
	c.noteAgentHealth(task, []EventRow{{Type: "turn_end", Data: map[string]any{"subtype": "success", "result": "Done."}}})
	if h := c.providerHealth("anthropic"); h != nil {
		t.Errorf("not cleared by a turn that went through: %+v", h)
	}
	if _, body, _ := tw.do("GET", "/settings/agents", nil, nil); strings.Contains(body, "health-") || !strings.Contains(body, `<span class="status on"`) {
		t.Error("the agents page still shows the old failure")
	}
}

// A key the provider refuses when the account is saved is not saved: the
// form says so and stays, new account or not. One that could not be tried
// is saved, with a warning. A test of the stored key that fails makes the
// account and its agents say "Key refused"; one that passes clears it.
func TestProviderSavedWithRefusedKey(t *testing.T) {
	status := http.StatusUnauthorized
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Write([]byte(`{"data":{}}`))
	}))
	defer fake.Close()
	old := checkEndpoints["openrouter-api"]
	checkEndpoints["openrouter-api"] = fake.URL
	t.Cleanup(func() { checkEndpoints["openrouter-api"] = old })

	tw, c, csrf := catalogWeb(t, noAgents)
	_, body, h := tw.post("/settings/providers", url.Values{"kind": {"openrouter-api"}, "key": {"sk-or-v1-bad"}, "preset": {"kimi"}}, csrf)
	if h.Get("HX-Redirect") != "" || !strings.Contains(body, "Clé refusée") || !strings.Contains(body, `name="key"`) {
		t.Fatalf("a refused key went on: %v %s", h, body)
	}
	if _, ok := c.conf().Providers["openrouter"]; ok || len(c.conf().Agents) != 0 || c.providerHealth("openrouter") != nil {
		t.Fatalf("a refused key was saved: %v %v", c.conf().Providers, c.conf().Agents)
	}

	// The provider's own error is not the key's: saved, and it says so.
	status = http.StatusInternalServerError
	_, body, h = tw.post("/settings/providers", url.Values{"kind": {"openrouter-api"}, "key": {"sk-or-v1-good"}, "preset": {"kimi"}}, csrf)
	if h.Get("HX-Redirect") != "/settings/agents?created=openrouter" {
		t.Fatalf("save: %v %s", h, body)
	}
	if h := c.providerHealth("openrouter"); h == nil || !h.Tested || h.Cause != causeServer {
		t.Fatalf("health = %+v", h)
	}
	_, body, _ = tw.do("GET", "/settings/agents?created=openrouter", nil, nil)
	for _, want := range []string{"n&#39;a pas passé le test", "Non vérifiée"} {
		if !strings.Contains(body, want) {
			t.Errorf("agents page lacks %q", want)
		}
	}
	secret := c.conf().Providers["openrouter"].Secret

	// A refused key does not replace the stored one either.
	status = http.StatusUnauthorized
	_, body, h = tw.post("/settings/providers/openrouter", url.Values{"kind": {"openrouter-api"}, "key": {"sk-or-v1-bad"}}, csrf)
	if h.Get("HX-Redirect") != "" || !strings.Contains(body, "Clé refusée") {
		t.Fatalf("a refused key replaced the stored one: %v %s", h, body)
	}
	if c.conf().Providers["openrouter"].Secret != secret {
		t.Fatal("the stored key changed")
	}

	if _, b, _ := tw.post("/settings/provider-test/openrouter", url.Values{}, csrf); !strings.Contains(b, "Clé refusée") {
		t.Fatalf("test: %s", b)
	}
	if h := c.providerHealth("openrouter"); h == nil || !h.Tested || h.Cause != causeAuth {
		t.Fatalf("health = %+v", h)
	}
	_, body, _ = tw.do("GET", "/settings/agents", nil, nil)
	for _, want := range []string{"La clé a été refusée", "health-refused", "Clé refusée"} {
		if !strings.Contains(body, want) {
			t.Errorf("agents page lacks %q", want)
		}
	}
	if strings.Count(body, "Clé refusée") < 2 {
		t.Error("the account and its agent should both say so")
	}
	status = http.StatusOK
	if _, b, _ := tw.post("/settings/provider-test/openrouter", url.Values{}, csrf); !strings.Contains(b, "La clé fonctionne") {
		t.Fatalf("test: %s", b)
	}
	if h := c.providerHealth("openrouter"); h != nil {
		t.Errorf("a passing test of the stored key did not clear it: %+v", h)
	}
	// Removing the account forgets its state.
	c.testedHealth("openrouter", testView{State: "err", Cause: causeAuth})
	c.deleteAgent("kimi")
	tw.post("/settings/providers/openrouter/delete", url.Values{}, csrf)
	if c.store.get(healthKey("openrouter")) != "" {
		t.Error("a removed account keeps its state")
	}
}

// The brain's notes do not show in the conversation: the catalog's, a
// task's turn, a permission. A task that failed is one short line in
// words, which opens its drawer.
func TestConversationHidesNotes(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.insertTask(&TaskRow{ID: "t-ac14385a", Node: "pc", Agent: "small", Runtime: "claude-code",
		Prompt: "Trie les factures\net fais un tableau", State: stateFailed})
	for _, text := range []string{
		"Catalogue : le compte « claude » a été ajouté depuis l'espace de travail, avec les agents haiku, opus. list_agents les montre.",
		"La tâche t-ac14385a (nœud pc) a fini son tour. Son dernier message : fini",
		"La tâche t-ac14385a (nœud pc) demande une permission (permission_id 1) : Bash {}",
		"La tâche t-ac14385a (nœud pc) est maintenant failed.",
	} {
		tw.c.store.addMessage(roleEvent, text, false)
	}
	_, body, _ := tw.do("GET", "/", nil, nil)
	for _, bad := range []string{"Catalogue", "list_agents", "est maintenant", "fini son tour", "permission_id"} {
		if strings.Contains(body, bad) {
			t.Errorf("the conversation shows %q", bad)
		}
	}
	for _, want := range []string{"La tâche « Trie les factures » a échoué.", `hx-get="/tasks/t-ac14385a"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the conversation lacks %q", want)
		}
	}
	// The stream says the same.
	last := tw.c.store.lastMessageSeq()
	msgs, _ := tw.c.store.messagesAfter(last-1, 1)
	tw.w.dress(msgs)
	if html, _ := tw.w.fragment("en", "home", "message", msgs[0]); !strings.Contains(html, "The task “Trie les factures” failed.") {
		t.Errorf("en notice: %s", html)
	}
	// The brain still reads them all.
	if und, _ := tw.c.store.undelivered(); len(und) != 4 {
		t.Errorf("undelivered = %d", len(und))
	}
}

// A tool's raw error is a sentence with Details, and a tool that is
// starting is not shown as an error.
func TestToolRowErrors(t *testing.T) {
	keepName(t)
	tw := newTestWeb(t)
	tw.setup()
	for _, c := range []struct {
		tv   toolView
		want []string
		not  []string
	}{
		{toolView{serverView: serverView{serverInfo: serverInfo{Name: "github", Status: statusError, Error: `calling "initialize": sending "initialize": Unauthorized`}}},
			[]string{"Le service a refusé la connexion : reconnecte-le", "<summary>Détails</summary>", "Unauthorized"}, nil},
		{toolView{serverView: serverView{serverInfo: serverInfo{Name: "mail", Kind: "local", Status: statusError, Error: "service mail is pending on pc-bureau"}, Node: "pc-bureau", Service: "pending"},
			Pending: true, Placed: true},
			[]string{"Il démarre sur son appareil"}, []string{"is pending on", "problem-text"}},
	} {
		html, err := tw.w.fragment("fr", "settings", "tools", page{Data: toolsPage{Tools: []toolView{c.tv}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range c.want {
			if !strings.Contains(html, want) {
				t.Errorf("%s lacks %q", c.tv.Name, want)
			}
		}
		for _, bad := range c.not {
			if strings.Contains(html, bad) {
				t.Errorf("%s shows %q", c.tv.Name, bad)
			}
		}
	}
}

// The task's drawer words its failure, the raw text in Details.
func TestTaskDrawerError(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "opencode", Prompt: "Écris un script", State: stateFailed})
	raw := "the agent runs model openrouter/a, not openrouter/b: is the key of its provider there?"
	tw.c.store.addEvents("t-1", []EventRow{{Seq: 1, Time: time.Now(), Type: "error", Data: map[string]any{"message": raw}}})
	_, body, _ := tw.do("GET", "/tasks/t-1", nil, map[string]string{"HX-Request": "true"})
	for _, want := range []string{"L&#39;agent n&#39;a pas pu utiliser son modèle", "Vérifier le compte IA", "<summary>Détails</summary>", "is the key of its provider there?"} {
		if !strings.Contains(body, want) {
			t.Errorf("drawer lacks %q", want)
		}
	}
}

// Where the pages speak of the assistant, they use its name; Eriphe is the
// software's.
func TestAssistantNameOnPages(t *testing.T) {
	keepName(t)
	setAssistantName("Atlas")
	for _, c := range []struct{ lang, key, want string }{
		{"fr", "remote.lead", "Parle à Atlas depuis ton téléphone"},
		{"fr", "remote.off.title", "Parle à Atlas de partout"},
		{"fr", "remote.off.lead", "Atlas ne répond que sur ton réseau local"},
		{"en", "remote.lead", "Talk to Atlas from your phone"},
		{"fr", "github.create", "Créer l'app GitHub d'Atlas"},
		{"fr", "github.createlead", "la description qu'Atlas lui envoie"},
		{"fr", "github.created", "les dépôts où Atlas peut travailler"},
		{"en", "github.create", "Create Atlas's GitHub App"},
		{"fr", "folders.access.droplead", "sous-dossier « Atlas »"},
		{"fr", "drive.lead", "Atlas ne voit que les dossiers"},
	} {
		if got := tr(c.lang, c.key); !strings.Contains(got, c.want) || strings.Contains(got, "{{name}}") {
			t.Errorf("%s %s = %q", c.lang, c.key, got)
		}
	}
	for _, key := range []string{"remote.lead", "remote.off.title", "remote.off.lead", "github.lead", "github.create", "github.forgethint", "github.noapp"} {
		for _, lang := range []string{"fr", "en"} {
			if strings.Contains(tr(lang, key), "Eriphe") {
				t.Errorf("%s %s still names Eriphe: %q", lang, key, tr(lang, key))
			}
		}
	}
	tw, _, _ := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{DisplayName: "Atlas"} })
	if _, body, _ := tw.do("GET", "/settings/github", nil, nil); !strings.Contains(body, "Créer l&#39;app GitHub d&#39;Atlas") {
		t.Error("the GitHub page does not name Atlas")
	}
}
