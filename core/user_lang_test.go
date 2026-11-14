package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"
)

// taskSpec is a task's spec as eriphe-core sends it to its node.
func taskSpec(t *testing.T, c *core, id string) nodeTaskSpec {
	t.Helper()
	var raw string
	c.store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, id).Scan(&raw)
	var spec nodeTaskSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// The user's language is one: what sets it (a signed-in page) is read by
// the queue, the channels and every prompt that writes for the user.
func TestUserLang(t *testing.T) {
	tw := newTestWeb(t)
	c := tw.c
	if c.userLang() != "fr" {
		t.Fatalf("default %q", c.userLang())
	}
	csrf := tw.setup()
	if code, _, _ := tw.post("/lang", url.Values{"lang": {"en"}}, csrf); code != 200 {
		t.Fatalf("POST /lang: %d", code)
	}
	if c.userLang() != "en" {
		t.Fatalf("after the switch %q", c.userLang())
	}
	b := &botChannel{c: c}
	if b.lang() != "en" {
		t.Fatalf("channel %q", b.lang())
	}
	c.noteUserLang("de") // not a language of Eriphe's: kept as it was
	if c.userLang() != "en" {
		t.Fatalf("after de %q", c.userLang())
	}

	// The brain's (and the assistants') turn, and a session lead's first.
	if h := c.turnHead(time.Now()); !strings.Contains(h, "[langue de l'utilisateur : écris en anglais") {
		t.Fatalf("turn head:\n%s", h)
	}
	if p := assistantRoles[assistantRoleLead].firstPrompt(c, Assistant{}, false); !strings.Contains(p, "en anglais") {
		t.Fatalf("lead's first turn:\n%s", p)
	}
	if s := delegateSurfaceText("s-1", c.userLang()); !strings.Contains(s, "en anglais") {
		t.Fatalf("task surface: %s", s)
	}
	c.noteUserLang("fr")
	if h := c.turnHead(time.Now()); !strings.Contains(h, "en français, sa langue, en le tutoyant") {
		t.Fatalf("turn head:\n%s", h)
	}
}

// A task's and a skill run's voice: the user's language, the code's rules
// kept whatever it is.
func TestVoicesInUserLang(t *testing.T) {
	en, fr := skillVoice("en"), skillVoice("fr")
	if !strings.Contains(en, "in the user's language, English") || strings.Contains(en, "French") ||
		!strings.Contains(en, "no entry, and the user is told nothing") || strings.Contains(en, "[brief]") {
		t.Fatalf("en:\n%s", en)
	}
	if !strings.Contains(fr, "French, with tutoiement") {
		t.Fatalf("fr:\n%s", fr)
	}
	if w := workerVoice("Iris", "en"); !strings.Contains(w, "in the user's language, English") || strings.Contains(w, "French") {
		t.Fatalf("worker en:\n%s", w)
	}
	if w := workerVoice("Iris", "fr"); !strings.Contains(w, "French, with tutoiement") {
		t.Fatalf("worker fr:\n%s", w)
	}
}

// The prompts sent with a task and a skill run carry the user's language.
func TestTaskPromptsInUserLang(t *testing.T) {
	c := newCatalogCore(t)
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code","opencode"]}`))
	c.noteUserLang("en")
	task, err := c.runTask(context.Background(), runRequest{Prompt: "count the lines"})
	if err != nil {
		t.Fatal(err)
	}
	if s := taskSpec(t, c, task.ID); !strings.Contains(s.AppendSystemPrompt+s.Prompt, "in the user's language, English") {
		t.Fatalf("task spec %+v", s)
	}

	sc, now := skillCore(t)
	sc.noteUserLang("en")
	addTriggered(t, sc, "---\nname: mails-rdv\ndescription: les rendez-vous des mails\nwhen: monitor.new\nevery: 15m\ntools: [monitor]\n---\nRepère les rendez-vous.\n")
	sc.emit(journalEvent{Kind: "monitor.new", Key: "perso", Source: "monitor:mails", At: *now, Payload: map[string]any{"subject": "RDV"}})
	sc.tickSkills(context.Background(), now.Add(15*time.Minute))
	rs := runsOfSkill(t, sc, "mails-rdv")
	if len(rs) != 1 || rs[0].Task == "" {
		t.Fatalf("runs %+v", rs)
	}
	if s := taskSpec(t, sc, rs[0].Task); !strings.Contains(s.AppendSystemPrompt+s.Prompt, "in the user's language, English") ||
		!strings.Contains(s.AppendSystemPrompt+s.Prompt, "attention list") {
		t.Fatalf("skill run spec %+v", s)
	}
}

// The arrival skill writes its surfaces in the user's language.
func TestArrivalSkillLanguage(t *testing.T) {
	body, err := fs.ReadFile(defaultSkillFS, path.Join(defaultSkillsDir, "arrivee", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "langue de l'utilisateur") {
		t.Fatal("the arrival skill does not name the user's language")
	}
}

// A learned entry's reason, when code wrote it, is in the page's language.
func TestLearnedReasonInLang(t *testing.T) {
	reason := templateFuncs("fr")["reason"].(func(string) string)
	if got := reason(defaultSkillReason); got != "installé par défaut" {
		t.Fatalf("fr %q", got)
	}
	if got := reason("Sam asked for it"); got != "Sam asked for it" {
		t.Fatalf("free text %q", got)
	}
}
