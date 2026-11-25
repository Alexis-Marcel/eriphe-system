package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// triSkill is a triggered skill that declares a tool and a model: what a
// launch with it gets by default.
const triSkill = "---\nname: tri\ndescription: Trie les notes.\nwhen: time weekdays 08:00\nmodel: kimi\ntools: [notes]\n---\nLis chaque note et range-la.\n"

// launchCore is a core with a graph, the catalog's notes tool, two agents
// (sonnet by default, kimi) and the approved skill tri.
func launchCore(t *testing.T) *core {
	t.Helper()
	c := newCatalogCore(t, withGraph(t))
	c.store.registerDevice("pc", hashSecret("pc-secret"))
	c.store.setDeviceStatus("pc", true, []byte(`{"runtimes":["claude-code","opencode"]}`))
	addTriggered(t, c, triSkill)
	return c
}

// launched is the task a run_task call started, its device spec and its
// node's parent.
func launched(t *testing.T, c *core, args map[string]any) (*TaskRow, deviceTaskSpec) {
	t.Helper()
	out, msg := brainNodes(t, c, "run_task", args)
	if msg != "" {
		t.Fatalf("run_task %v: %s", args, msg)
	}
	task, err := c.store.task(fmt.Sprint(out["task"]))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := c.store.taskSpec(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task, spec
}

// A launch with a skill: the ask is kept, the skill's instructions are the
// task's method after it, then its surroundings; its tools are lent and
// its model is the agent.
func TestLaunchWithSkill(t *testing.T) {
	c := launchCore(t)
	node := mustNode(t, c, nodestore.New{Title: "Notes", Goal: "Mes notes rangées."})
	task, spec := launched(t, c, map[string]any{"title": "Ranger", "ask": "Range mes notes.", "skill": "tri", "node": node})
	if task.Prompt != "Range mes notes." {
		t.Errorf("kept prompt %q: the ask only", task.Prompt)
	}
	if task.Agent != "kimi" {
		t.Errorf("agent %q, want the skill's model kimi", task.Agent)
	}
	if len(task.Resources) != 1 || task.Resources[0].Kind != resTool || task.Resources[0].Ref != "notes" {
		t.Errorf("lent %v, want the skill's tool notes", task.Resources)
	}
	ask := strings.Index(spec.Prompt, "Range mes notes.")
	method := strings.Index(spec.Prompt, "[Your method: the user's skill « tri »")
	body := strings.Index(spec.Prompt, "Skill « tri » : Trie les notes.\n\nLis chaque note et range-la.")
	around := strings.Index(spec.Prompt, aroundHeading)
	if ask != 0 || method < ask || body < method || around < body {
		t.Fatalf("its prompt: ask %d, method %d, body %d, surroundings %d:\n%s", ask, method, body, around, spec.Prompt)
	}
	if !strings.Contains(spec.Prompt, "« Notes » — goal: Mes notes rangées.") {
		t.Errorf("the node it works for is not in its surroundings:\n%s", spec.Prompt)
	}
	if sum, _ := c.nodes.Get(c.taskNode(task.ID)); sum.Goal != "Range mes notes." {
		t.Errorf("its node's goal %q", sum.Goal)
	}
	t.Logf("a launch with a skill, %d runes:\n%s", runeLen(spec.Prompt), spec.Prompt)

	// The result says what the skill lent.
	out, msg := brainNodes(t, c, "run_task", map[string]any{"ask": "Range.", "skill": "tri"})
	if msg != "" || !strings.HasPrefix(fmt.Sprint(out["lent"]), "[tool notes") {
		t.Errorf("result %v %s, want the tool notes lent", out, msg)
	}
}

// Under a hub of 300 children, the node it works for's children share
// the same cap, and their « more » line names the hub.
func TestLaunchBesideCapped(t *testing.T) {
	c := launchCore(t)
	hub := mustNode(t, c, nodestore.New{Title: "Recettes", Goal: "Les recettes de Sam."})
	for i := range 300 {
		mustNode(t, c, nodestore.New{Title: fmt.Sprintf("Recette %d", i), Goal: strings.Repeat("Une recette longue à décrire. ", 10), Parent: hub})
	}
	task, _ := launched(t, c, map[string]any{"ask": "Fais le menu.", "node": hub})
	b := c.surroundings(c.taskNode(task.ID), true)
	if n := runeLen(b); n > aroundMax {
		t.Fatalf("%d runes, over %d", n, aroundMax)
	}
	if !strings.Contains(b, "Beside it, under "+hub+", the node it works for (300):") || !strings.Contains(b, "more: node_read "+hub+" lists them") {
		t.Fatalf("beside a hub:\n%s", b)
	}
	t.Logf("beside a hub: %d runes", runeLen(b))
}

// Without a skill: the ask, no method, the default agent, nothing lent.
func TestLaunchWithoutSkill(t *testing.T) {
	c := launchCore(t)
	task, spec := launched(t, c, map[string]any{"ask": "Cherche qui détient la marque."})
	if strings.Contains(spec.Prompt, "[Your method") || !strings.HasPrefix(spec.Prompt, "Cherche qui détient la marque.") {
		t.Errorf("its prompt:\n%s", spec.Prompt)
	}
	if task.Agent != "sonnet" || len(task.Resources) != 0 {
		t.Errorf("agent %q, lent %v", task.Agent, task.Resources)
	}
}

// The call narrows what the skill declares: resources named replace its
// tools, an agent named its model.
func TestLaunchNarrowed(t *testing.T) {
	c := launchCore(t)
	task, spec := launched(t, c, map[string]any{"ask": "Range.", "skill": "tri", "agent": "sonnet",
		"resources": []any{map[string]any{"kind": "tool", "ref": "notes.read"}}})
	if task.Agent != "sonnet" {
		t.Errorf("agent %q, want the one named", task.Agent)
	}
	if len(task.Resources) != 1 || task.Resources[0].Ref != "notes.read" {
		t.Errorf("lent %v, want notes.read only", task.Resources)
	}
	if !strings.Contains(spec.Prompt, "Lis chaque note et range-la.") {
		t.Errorf("the method is gone:\n%s", spec.Prompt)
	}
}

// A skill that is not approved, unknown or off is refused, with the
// approved ones; the old prompt and an empty or long ask too. Nothing
// starts.
func TestLaunchRefused(t *testing.T) {
	c := launchCore(t)
	if _, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte("---\nname: brouillon\ndescription: pas encore lu\n---\nFais tout.\n")}}, "upload"); err != nil {
		t.Fatal(err)
	}
	addTriggered(t, c, "---\nname: eteint\ndescription: coupé\n---\nRien.\n")
	if err := c.store.enableSkill("eteint", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"ask": "x", "skill": "inconnu"}, `skill "inconnu" is not one of the user's approved skills: tri`},
		{map[string]any{"ask": "x", "skill": "brouillon"}, `skill "brouillon" is not one of the user's approved skills: tri`},
		{map[string]any{"ask": "x", "skill": "eteint"}, `skill "eteint" is not one of the user's approved skills: tri`},
		{map[string]any{"prompt": "Fais ceci."}, "prompt"},
		{map[string]any{"ask": " "}, "ask is required"},
		{map[string]any{"ask": strings.Repeat("é", launchAskMax+1)}, "at most"},
	} {
		if _, msg := brainNodes(t, c, "run_task", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%v: %q, want %q", tc.args, msg, tc.want)
		}
	}
	if tasks, _ := c.store.tasks(false, 10); len(tasks) != 0 {
		t.Fatalf("a refused launch started %d task(s)", len(tasks))
	}
}

// Rights do not move: on a tainted turn, what a skill lends by default
// goes through the same start as the same resources named, and the task
// is tainted as any other.
func TestLaunchTaintedStart(t *testing.T) {
	c := launchCore(t)
	c.turn.taint("mail.search_messages")
	withSkill, _ := launched(t, c, map[string]any{"ask": "Range.", "skill": "tri"})
	named, _ := launched(t, c, map[string]any{"ask": "Range.", "agent": "kimi",
		"resources": []any{map[string]any{"kind": "tool", "ref": "notes"}}})
	a, b := c.startState(withSkill), c.startState(named)
	if a["start"] != "waits_for_card" || a["start"] != b["start"] || fmt.Sprint(a["asks"]) != fmt.Sprint(b["asks"]) {
		t.Fatalf("a skill's lend starts as %v, the same named as %v", a, b)
	}
	if len(c.taskTaint(withSkill.ID)) == 0 {
		t.Fatal("a launch on a tainted turn is not tainted")
	}
	t.Logf("start: %v", a)
}

// A task the brain launches works in a node of its own, under the node it
// works for: its surroundings give that node's other children and links,
// and its start card does not repeat that node's line.
func TestLaunchSurroundingsBeside(t *testing.T) {
	c := launchCore(t)
	guide := mustNode(t, c, nodestore.New{Title: "Guide de style", Goal: "Le ton des lettres."})
	sub := mustNode(t, c, nodestore.New{Title: "Substack", Goal: "Une lettre par semaine.", Links: []string{guide}})
	sibling := mustNode(t, c, nodestore.New{Title: "Lettre 12", Goal: "La douzième.", Parent: sub})
	task, spec := launched(t, c, map[string]any{"ask": "Écris la lettre 13.", "node": sub})
	for _, want := range []string{
		"Beside it, under " + sub + ", the node it works for (1):\n- " + sibling,
		sub + " links to (1):\n- " + guide, "« Guide de style » — goal: Le ton des lettres.",
	} {
		if !strings.Contains(spec.Prompt, want) {
			t.Errorf("its surroundings lack %q:\n%s", want, spec.Prompt)
		}
	}
	if n := strings.Count(spec.Prompt, "Substack"); n != 1 {
		t.Errorf("the node it works for is said %d times:\n%s", n, spec.Prompt)
	}
	if strings.Contains(spec.Prompt, "- "+c.taskNode(task.ID)) {
		t.Errorf("its own node listed beside it:\n%s", spec.Prompt)
	}
	// A skill's run in its home has no such section: it is not beside.
	if b := c.surroundings(sibling, false); strings.Contains(b, "Beside it") {
		t.Errorf("not beside:\n%s", b)
	}
}

// A task's start card leaves out the line of a node its surroundings give
// (its own or an ancestor), and keeps the rest.
func TestStartCardWithoutAroundLine(t *testing.T) {
	c, _ := testMemoryCore(t)
	id := siteNode(t, c)
	child := mustNode(t, c, nodestore.New{Title: "Certificat", Parent: id})
	key := nodeRef(id)
	full, _ := c.aboutCard(key, aboutCardChars)
	short, _ := c.aboutCardOf(key, aboutCardChars, false)
	if !strings.Contains(full, "Site en ligne") || strings.Contains(short, "Site en ligne") || !strings.Contains(short, "Décision 4 du site") {
		t.Fatalf("full:\n%s\nwithout its line:\n%s", full, short)
	}
	if !c.aroundHolds(child, key) || !c.aroundHolds(id, key) || c.aroundHolds(child, "person:lina") || c.aroundHolds(id, nodeRef(child)) {
		t.Fatal("aroundHolds: an ancestor or itself only")
	}
}
