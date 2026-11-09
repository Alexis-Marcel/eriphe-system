package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The real cases of #244 step 8, end to end with fakes: GetOut production
// (a poll rule, an incident fact, the brain), the mail triage (an
// assistant and its facts) and the tech watch (a schedule rule and an
// assistant that reads the web).

func factValue(c *core, id string, now time.Time) (any, bool) {
	for _, f := range c.allFacts(now) {
		if f.id() == id && f.known(now) {
			return f.Value, true
		}
	}
	return nil, false
}

// GetOut production, with no assistant: an outil rule polls the service,
// opens one incident per outage (deduplicated by code) as a closed fact,
// wakes the brain on the transition, and another rule follows the fact.
func TestRealCaseProdIncident(t *testing.T) {
	c, mon, now := pollCore(t)
	w := pollWatch(t, c, &pollCond{Kind: condCompare, Path: "data.state", Op: "eq", Value: "down"})
	id := "rules." + ruleFactID(w.ID)
	ctx := context.Background()
	step := func(d time.Duration) {
		*now = now.Add(d)
		c.ruleTick(ctx)
	}
	if _, ok := factValue(c, id+".incident", *now); ok {
		t.Fatal("an incident fact before the first look")
	}
	step(0)
	if v, _ := factValue(c, id+".incident", *now); v != incidentClosed {
		t.Fatalf("after a look, up: %v", v)
	}
	// A rule on the incident fact, as the brain would write it.
	follow := mustWatch(t, c, &rule{Name: "Incident prod", Source: sourceFact, Spec: ruleWhen{Fact: id + ".incident", Becomes: incidentOpen},
		Act: actNotify, ActText: "La prod est en incident.", ActLevel: levelNow})
	step(0) // its first look only notes the fact

	mon.set("down", "2.0.1")
	step(time.Minute)
	step(pollConfirm)
	if v, _ := factValue(c, id+".incident", *now); v != incidentOpen {
		t.Fatalf("down, confirmed: %v", v)
	}
	if r, got := c.pendingRun(); r == nil || got.ID != w.ID {
		t.Fatal("the brain is not woken for the outage")
	}
	step(0)
	if runs := runsOf(t, c, follow.ID); len(runs) != 1 || runs[0].Outcome != runNotified {
		t.Fatalf("the rule on the fact: %+v", runs)
	}
	// Still down: one incident, not two.
	step(time.Minute)
	step(time.Minute)
	if n, _ := factValue(c, id+".incidents_7d", *now); n != float64(1) {
		t.Fatalf("deduplicated: %v", n)
	}
	mon.set("up", "2.0.1")
	step(time.Minute)
	step(pollConfirm)
	if v, _ := factValue(c, id+".incident", *now); v != incidentClosed {
		t.Fatalf("recovered: %v", v)
	}
	if s, _ := factValue(c, id+".since", *now); s == nil {
		t.Fatal("no since")
	}
	// A condition on it is checked: open or closed only, an outil rule only.
	if err := c.checkRule(&rule{Name: "x", Source: sourceSchedule, Spec: ruleWhen{Schedule: "daily 07:00"}, Prompt: "y",
		If: id + ".incident = open and " + id + ".incidents_7d > 2"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{id + ".incident = down", "rules.r999.incident = open", id + ".mood = open"} {
		if err := c.checkRule(&rule{Name: "x", Source: sourceSchedule, Spec: ruleWhen{Schedule: "daily 07:00"}, Prompt: "y", If: bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// And the brain reads it among the facts, closed.
	out, _ := c.brainFacts(factsIn{Owner: ownerRules}, *now)
	if views := out["facts"].([]factView); len(views) != 3 || views[0].Detail["rule"] != "Le site" {
		t.Fatalf("the brain's facts: %+v", views)
	}
}

// The mail triage: an assistant made from its template writes one row per
// mail; its views give the urgent ones and those waiting for a reply; the
// brain's line counts them.
func TestRealCaseMailTriage(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	out, err := c.proposeAssistant(proposeAssistantIn{Template: "tri-mails", Tools: []string{"mail"},
		Why: "Il lit les mails à ta place : leur contenu ne passe jamais par moi."})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.decideProposal(context.Background(), cardNewAssistant, out["proposal"].(int64), true); err != nil {
		t.Fatal(err)
	}
	a := c.conf().Assistants["tri-mails"]
	now := c.notifyTime()
	rows := []any{
		map[string]any{"id": "m1", "de": "banque@example.org", "sujet": "Échéance", "categorie": "facture", "urgence": "haute",
			"attend_reponse": "non", "echeance": "2026-11-12", "recu": "2026-11-09T08:00"},
		map[string]any{"id": "m2", "de": "sam@example.org", "sujet": "Dîner ?", "categorie": "perso", "urgence": "normale",
			"attend_reponse": "oui", "recu": "2026-11-09T09:00"},
		map[string]any{"id": "m3", "de": "news@example.org", "sujet": "Lettre", "categorie": "newsletter", "urgence": "basse",
			"attend_reponse": "non", "recu": "2026-11-09T07:00"},
	}
	if _, err := c.setState(a, setStateIn{Values: map[string]any{"mails": rows}}, []string{"mcp__mail__read"}, now); err != nil {
		t.Fatal(err)
	}
	count := func(view string) int {
		res, err := c.runView(a, view, 0, now)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := res.Value.([]any)
		return len(got)
	}
	if count("urgents") != 1 || count("attend_reponse") != 1 || count("derniers") != 3 {
		t.Fatalf("views: %d %d %d", count("urgents"), count("attend_reponse"), count("derniers"))
	}
	// A rule may count its rows: « s'il y a des mails, … ».
	if err := c.checkRule(&rule{Name: "x", Source: sourceSchedule, Spec: ruleWhen{Schedule: "daily 07:00"}, Prompt: "y", If: "tri-mails.mails > 0"}); err != nil {
		t.Fatal(err)
	}
}

// The tech watch: a schedule rule wakes an assistant that reads the web
// itself, lent nothing to send; its reads go by the web's value.
func TestRealCaseTechWatch(t *testing.T) {
	c, _ := watchCore(t, at(t, "2026-11-09", "10:00"))
	in := proposeAssistantIn{Template: "veille-techno", Why: "Il lit le web à ta place, chaque matin."}
	if _, err := c.proposeAssistant(in); err == nil || !strings.Contains(err.Error(), "<les sujets>") {
		t.Fatalf("a mission with its hole: %v", err)
	}
	in.Mission = "Tu fais la veille technique de {{user}} sur ces sujets : Go, SQLite et les agents. Écris tes nouveautés et une phrase pour le brief."
	if _, err := c.proposeAssistant(proposeAssistantIn{Template: in.Template, Why: in.Why, Mission: in.Mission, Tools: []string{"mail"}}); err == nil {
		t.Fatal("an assistant that reads the web, lent a tool")
	}
	out, err := c.proposeAssistant(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.decideProposal(context.Background(), cardNewAssistant, out["proposal"].(int64), true); err != nil {
		t.Fatal(err)
	}
	a := c.conf().Assistants["veille-techno"]
	if tools := assistantHarnessTools(a, assistantRoles[assistantRoleAssistant]); !slicesContain(tools, webFetchTool) || !slicesContain(tools, webSearchTool) {
		t.Fatalf("its harness tools: %v", tools)
	}
	if ws := c.wakeRules("veille-techno"); len(ws) != 1 || ws[0].Source != sourceSchedule {
		t.Fatalf("its rule: %+v", ws)
	}
	for _, x := range []struct {
		tool  string
		input map[string]any
		value string
		yes   bool
	}{
		{webSearchTool, map[string]any{"query": "Go 1.27 release"}, gridFree, true},
		{webFetchTool, map[string]any{"url": "https://go.dev/blog"}, gridFree, true},
		{webFetchTool, map[string]any{"url": "http://192.168.1.10/admin"}, gridFree, false},
		{webFetchTool, map[string]any{"url": "https://go.dev/blog"}, gridApproval, false},
		{webSearchTool, map[string]any{"query": "x"}, gridNever, false},
		{"Bash", map[string]any{"command": "curl example.org"}, gridFree, false},
	} {
		c.setWebValue(x.value)
		if yes, why := c.assistantWebDecision(a, x.tool, x.input); yes != x.yes {
			t.Errorf("%s %v (%s): %v, %s", x.tool, x.input, x.value, yes, why)
		}
	}
	// Without the power, no web.
	c.setWebValue(gridFree)
	if yes, _ := c.assistantWebDecision(Assistant{Name: "tri", Role: assistantRoleAssistant}, webSearchTool, map[string]any{"query": "x"}); yes {
		t.Fatal("the web without its power")
	}
}
