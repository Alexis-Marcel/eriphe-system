package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// webSession is a workspace with a session whose plan the user approved:
// a task, then a choice and an approval.
func webSession(t *testing.T) (*testWeb, string, Session) {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	tw := newTestWebFor(t, newCore(&Config{User: "Sam", Listen: "127.0.0.1:8800", DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: secret}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p"}}}, store))
	csrf := tw.setup()
	tw.c.store.registerNode("nas", hashSecret("x"))
	tw.c.store.setNodeStatus("nas", true, []byte(`{"runtimes":["claude-code"],"cpus":8}`))
	s, err := tw.c.openSession(sessionIn{Title: "Brand name", Goal: "Find a name for the shop.", DoneWhen: "Sam picked a free name",
		Steps: []stepSpec{
			{Key: "names", Title: "Find names", Prompt: "Propose names."},
			{Key: "style", Title: "Pick a style", Kind: stepChoice, Question: "Which style?", Options: []string{"Calm", "Bold"}},
			{Key: "ok", Title: "Keep it", Kind: stepApproval, Question: "Keep the first free name?", After: []string{"names", "style"}},
		}}, nil, "brain")
	if err != nil {
		t.Fatal(err)
	}
	return tw, csrf, s
}

// A session's page draws its plan as stages, says how far each step's
// evidence goes, puts what waits for the user on top, and offers a line to
// its lead. In both languages.
func TestWebSessionPage(t *testing.T) {
	tw, _, s := webSession(t)
	// Before the plan's yes, its card is what waits.
	code, body, _ := tw.do("GET", "/sessions/1", nil, nil)
	if code != 200 || !strings.Contains(body, "Plan à approuver") || !strings.Contains(body, "Lancer la session « Brand name »") {
		t.Fatalf("proposed: %d", code)
	}
	tw.c.decideCard(context.Background(), sessionCard(t, tw.c, askPlan).ID, true, "web")
	for lang, words := range map[string][]string{
		"fr": {"En cours", "<h1>Brand name</h1>", "Find a name for the shop.", "Fini quand", "Sam picked a free name", "Menée par", "Chef de session 1",
			"Ce qui t&#39;attend", "Which style?", `value="Calm"`, `value="Bold"`, "Le plan", "Étape 1", "2 en parallèle", "Find names", "En cours",
			"Étape 2", "Keep it", "En attente", "affirmé par l&#39;agent", "Journal", "Plan approuvé.", "Écrire à Chef de session 1"},
		"en": {"Running", "<h1>Brand name</h1>", "Done when", "Led by", "Waiting for you", "The plan", "Step 1", "2 in parallel", "Step 2", "Waiting",
			"claimed by the agent", "Log", "Write to Chef de session 1"},
	} {
		code, body, _ := tw.do("GET", "/sessions/1", nil, map[string]string{"Cookie": "lang=" + lang})
		if code != 200 {
			t.Fatalf("%s: %d", lang, code)
		}
		at := 0
		for _, want := range words {
			i := strings.Index(body[at:], want)
			if i < 0 {
				t.Fatalf("%s: the page lacks %q after its place", lang, want)
			}
			at += i + len(want)
		}
		for _, want := range []string{`data-session-live="/sessions/1/events"`, `/static/sessions.css`, `/static/sessions.js`, `hx-target="#drawer"`, `id="drawer"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %s", lang, want)
			}
		}
	}
	// The list, and the tasks panel.
	if code, body, _ := tw.do("GET", "/sessions", nil, nil); code != 200 || !strings.Contains(body, `href="/sessions/1"`) || !strings.Contains(body, "Attend ta réponse") ||
		!strings.Contains(body, "0 sur 3 acceptées") {
		t.Fatalf("list: %d", code)
	}
	tasks, _, err := tw.w.tasksAndPerms()
	if err != nil || len(tasks.Sessions) != 1 || tasks.Sessions[0].ID != s.ID || !tasks.Sessions[0].Waits {
		t.Fatalf("panel %+v %v", tasks.Sessions, err)
	}
	html, err := tw.w.fragment("fr", "home", "tasks", tasks)
	if err != nil || !strings.Contains(html, "Sessions de travail") || !strings.Contains(html, `href="/sessions/1"`) || !strings.Contains(html, `class="sess-strip"`) {
		t.Errorf("panel html %v", err)
	}
	if code, _, _ := tw.do("GET", "/sessions/9", nil, nil); code != 404 {
		t.Errorf("no session 9: %d", code)
	}
}

// The user acts from the page: a choice, a word to the lead, pause,
// resume, stop.
func TestWebSessionActions(t *testing.T) {
	tw, csrf, s := webSession(t)
	ctx := context.Background()
	tw.c.decideCard(ctx, sessionCard(t, tw.c, askPlan).ID, true, "web")
	if code, body, _ := tw.post("/sessions/1/choose", url.Values{"step": {"style"}, "option": {"Bold"}}, csrf); code != 200 || !strings.Contains(body, "Acceptée") {
		t.Fatalf("choose: %d", code)
	}
	if st := stepOf(t, tw.c, s, "style"); st.Status != stepAccepted || st.Output != "Bold" {
		t.Errorf("style %+v", st)
	}
	if code, _, _ := tw.post("/sessions/1/lead", url.Values{"text": {"Short names, please."}}, csrf); code != 200 {
		t.Fatalf("lead: %d", code)
	}
	if msg := told(t, tw.c, s); !strings.Contains(msg, "De la part de Sam") || !strings.Contains(msg, "Short names, please.") {
		t.Errorf("the lead read %q", msg)
	}
	if code, _, h := tw.post("/sessions/1/lead", url.Values{"text": {"  "}}, csrf); code == 200 && !strings.Contains(h.Get("HX-Trigger"), "write") {
		t.Errorf("an empty word went: %d %v", code, h)
	}
	if code, body, _ := tw.post("/sessions/1/pause", nil, csrf); code != 200 || !strings.Contains(body, "En pause") {
		t.Fatalf("pause: %d", code)
	}
	if code, body, _ := tw.post("/sessions/1/resume", nil, csrf); code != 200 || !strings.Contains(body, "Mettre en pause") {
		t.Fatalf("resume: %d", code)
	}
	if code, body, _ := tw.post("/sessions/1/stop", nil, csrf); code != 200 || !strings.Contains(body, "Arrêtée") {
		t.Fatalf("stop: %d", code)
	}
	if code, _, _ := tw.post("/sessions/1/fly", nil, csrf); code != 404 {
		t.Errorf("unknown action: %d", code)
	}
}

// Steps are drawn one stage after the deepest step they need; the final
// check comes last.
func TestSessionStages(t *testing.T) {
	steps := []Step{
		{stepSpec: stepSpec{Key: "a"}}, {stepSpec: stepSpec{Key: "b"}},
		{stepSpec: stepSpec{Key: "c", After: []string{"a"}}},
		{stepSpec: stepSpec{Key: "d", After: []string{"c", "b"}}},
		{stepSpec: stepSpec{Key: finalStep}},
	}
	got := stages(steps)
	var keys []string
	for _, st := range got {
		var ks []string
		for _, x := range st.Steps {
			ks = append(ks, x.Key)
		}
		keys = append(keys, strings.Join(ks, "+"))
	}
	if strings.Join(keys, " | ") != "a+b | c | d | final-check" || !got[3].Final || got[2].Final {
		t.Errorf("stages %v", keys)
	}
}
