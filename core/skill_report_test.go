package main

import (
	"strings"
	"testing"
	"time"
)

// A triggered skill's page lists its runs (events, task, the card it made
// and the answer, whether it told) and its totals over 30 days; it reloads
// while a run is going; the list says its trigger; the brain reads the
// same with skill_runs, without what the runs read.
func TestSkillRunsPage(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: prod\ndescription: la prod\nwhen: hook.received\ntools: [monitor]\n---\nEnquête.\n")
	r := startedRun(t, c, now, "prod")
	ev := []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": "p1", "tool_name": "mcp__monitor__rollback", "input": map[string]any{"version": "2.0.0"}}}}
	c.store.addEvents(r.Task, ev)
	c.applyPolicy(r.Task, ev)

	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/skills/prod", nil, nil)
	hasAll(t, "a run going", body, `id="skill-runs"`, `hx-trigger="every 5s"`, "en cours", "hook.received", "« 5xx »", r.Task,
		"1 exécution(s)")
	endTurn(t, c, r.Task, "GetOut renvoie des 5xx : je propose de remettre la 2.0.0.")
	_, body, _ = tw.do("GET", "/settings/skills/prod/runs", nil, nil)
	hasAll(t, "its fragment", body, "fait", "je propose de remettre la 2.0.0", "1 action(s) proposée(s)", "en attente de ta réponse",
		"t&#39;a prévenu", "cartes : 0 acceptée(s), 0 refusée(s), 1 en attente", "contenu extérieur")
	hasNone(t, "its fragment", body, `hx-trigger="every 5s"`, "<html")
	// Its action, answered on its own: yes runs it as kept.
	ask := c.runAsks(r.ID)[0]
	if code, _, _ := tw.post("/settings/skills/other/calls/"+idText(ask.ID)+"/approve", nil, csrf); code != 404 {
		t.Fatalf("another skill's page answered it: %d", code)
	}
	_, body, _ = tw.post("/settings/skills/prod/calls/"+idText(ask.ID)+"/approve", nil, csrf)
	hasAll(t, "answered", body, "acceptées", "cartes : 1 acceptée(s)")
	waitNote(t, c, r.ID, "rollback")
	_, body, _ = tw.do("GET", "/settings/skills", nil, nil)
	hasAll(t, "the list", body, "à chaque événement hook.received", `class="small skill-trigger"`)

	out := c.brainSkillRuns(skillRunsIn{})
	text := mustJSON(out)
	if !strings.Contains(text, `"skill":"prod"`) || !strings.Contains(text, `"calls":1`) || !strings.Contains(text, `"Runs":1`) ||
		strings.Contains(text, "2.0.0") || strings.Contains(text, "5xx") {
		t.Fatalf("skill_runs %s", text)
	}
}
