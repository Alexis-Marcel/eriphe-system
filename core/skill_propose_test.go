package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// codeSkill is a skill followed on demand, without when, that declares
// what it uses; its bundle has a script beside it.
const codeSkill = "---\nname: code\ndescription: Corrige un bug dans le dépôt.\nallowed-tools: Bash\nmodel: kimi\ntools: [notes]\nrepos: [example/app]\n---\nLis le bug, corrige-le.\n"

func addCodeSkill(t *testing.T, c *core) *Skill {
	t.Helper()
	k, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte(codeSkill)},
		{Path: "scripts/check.sh", Exec: true, Data: []byte("#!/bin/sh\ngo test ./...\n")}}, "upload")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.approveSkill(k.Name, k.Pending); err != nil {
		t.Fatal(err)
	}
	k, _ = c.store.skill("code")
	return k
}

// skillCard is the card of skill name, pending or not.
func skillCard(c *core, name string) (proposal, bool) {
	for _, p := range c.pendingProposals() {
		if p.Kind == cardSkill && p.ID == c.skillRowID(name) {
			return p, true
		}
	}
	return proposal{}, false
}

// Any skill declares what it uses: a skill without when is not triggered,
// and its tools, single repository and model are run_task's defaults.
func TestSkillUsesWithoutWhen(t *testing.T) {
	c := launchCore(t)
	addCodeSkill(t, c)
	for _, sk := range c.triggeredSkills() {
		if sk.Name == "code" {
			t.Fatal("a skill without when is triggered")
		}
	}
	r, err := c.launchOf(launchRequest{Ask: "Corrige le bug 12.", Skill: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Agent != "kimi" || len(r.Resources) != 2 || r.Resources[0] != (ResourceRef{Kind: resTool, Ref: "notes"}) ||
		r.Resources[1] != (ResourceRef{Kind: resRepo, Ref: "example/app"}) || r.skill != "code" {
		t.Fatalf("launch %+v", r)
	}
	// Its card says them before the yes.
	if _, err := c.writeSkill(writeSkillIn{Name: "revue", Description: "Relit une PR.", Instructions: "Relis.", Model: "kimi",
		Tools: []string{"notes"}, Repos: []string{"example/app"}}, nil); err != nil {
		t.Fatal(err)
	}
	p, ok := skillCard(c, "revue")
	if !ok {
		t.Fatal("no card")
	}
	var got []string
	for _, f := range p.Fields {
		got = append(got, f.Name+"="+f.Value)
	}
	want := "Outils qu'il emprunte=notes|Dépôts qu'il emprunte=example/app|Agent=kimi"
	if !strings.HasPrefix(strings.Join(got, "|"), want) {
		t.Fatalf("card fields %q, want %q first", got, want)
	}
}

// A task launched with a skill proposes a new version of it: its
// instructions only, the front matter and the other files kept; the
// version waits on its card, the approved one stays, the brain is told.
// The yes approves it and keeps the old one.
func TestTaskProposesItsSkill(t *testing.T) {
	c := launchCore(t)
	before := addCodeSkill(t, c)
	node := mustNode(t, c, nodestore.New{Title: "Bug 12", Goal: "Le bug 12 corrigé."})
	task, spec := launched(t, c, map[string]any{"ask": "Corrige le bug 12.", "skill": "code", "node": node,
		"resources": []any{map[string]any{"kind": "tool", "ref": "notes"}}})
	if !strings.Contains(spec.AppendSystemPrompt+spec.Prompt, skillProposeInstruction) {
		t.Fatal("a task that follows a skill is not told when to propose a version")
	}
	if _, other := launched(t, c, map[string]any{"ask": "Cherche."}); strings.Contains(other.AppendSystemPrompt+other.Prompt, "skill_propose") {
		t.Fatal("a task without a skill is told of skill_propose")
	}

	// Only its own skill.
	if _, msg := callNodes(t, c, task.ID, "skill_propose", map[string]any{"skill": "tri", "instructions": "x", "why": "y"}); !strings.Contains(msg, `you follow the skill "code"`) {
		t.Fatalf("another skill: %q", msg)
	}
	out := mustNodes(t, c, task.ID, "skill_propose", map[string]any{"skill": "code",
		"instructions": "Lis le bug, écris un test qui échoue, corrige-le, lance scripts/check.sh.", "why": "Il manquait le test qui reproduit le bug."})
	if out["card"] != proposalRef(cardSkill, c.skillRowID("code")) {
		t.Fatalf("result %v", out)
	}
	k, _ := c.store.skill("code")
	if k.Approved != before.Approved || k.Pending == "" {
		t.Fatalf("approved %s pending %s: nothing changes before the yes", k.Approved, k.Pending)
	}
	v, err := c.store.skillVersion(k.Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Files) != 2 || v.Files[0].Path != "SKILL.md" || v.Files[1].Path != "scripts/check.sh" || !v.Files[1].Exec {
		t.Fatalf("files %+v: the script is kept", v.Files)
	}
	md := string(v.Files[0].Data)
	front, _, _ := strings.Cut(codeSkill, "\n---\n")
	if !strings.HasPrefix(md, front+"\n---\n\nLis le bug, écris un test") || v.Manifest.Uses.Model != "kimi" || v.Manifest.AllowedTools != "Bash" {
		t.Fatalf("its SKILL.md:\n%s", md)
	}
	p, ok := skillCard(c, "code")
	if !ok || !p.Pending || p.Tainted != "" || len(p.Fields) == 0 || p.Fields[0].Name != tr("fr", "card.skill.by") ||
		p.Fields[0].Value != "Il manquait le test qui reproduit le bug." {
		t.Fatalf("card %+v", p)
	}
	if text := waitEvent(t, c, "en propose une nouvelle version"); !strings.Contains(text, "« code »") {
		t.Fatalf("the brain is told %q", text)
	}
	// One version waits at a time: the user answers it first.
	if _, msg := callNodes(t, c, task.ID, "skill_propose", map[string]any{"skill": "code", "instructions": "Autre.", "why": "y"}); !strings.Contains(msg, "already waits") {
		t.Fatalf("a second proposal: %q", msg)
	}
	if err := c.decideProposal(context.Background(), cardSkill, c.skillRowID("code"), true); err != nil {
		t.Fatal(err)
	}
	k, _ = c.store.skill("code")
	if k.Approved != v.SHA || k.Pending != "" {
		t.Fatalf("after the yes: approved %s pending %s", k.Approved, k.Pending)
	}
	var kept int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM skill_kept WHERE name = 'code' AND sha = ?`, before.Approved).Scan(&kept)
	if kept != 1 {
		t.Fatal("the version replaced is not kept")
	}
}

// A task without a skill proposes nothing; a tainted task's proposal is
// marked so on its card.
func TestTaskSkillProposalRefusedOrTainted(t *testing.T) {
	c := launchCore(t)
	addCodeSkill(t, c)
	plain, _ := launched(t, c, map[string]any{"ask": "Cherche."})
	if _, err := c.proposeTaskSkill(plain.ID, skillProposeIn{Skill: "code", Instructions: "x", Why: "y"}); err == nil || !strings.Contains(err.Error(), "you follow no skill") {
		t.Fatalf("a task without a skill: %v", err)
	}
	if k, _ := c.store.skill("code"); k.Pending != "" {
		t.Fatal("a refused proposal left a version")
	}
	task, _ := launched(t, c, map[string]any{"ask": "Corrige.", "skill": "code", "resources": []any{map[string]any{"kind": "tool", "ref": "notes"}}})
	c.taskRead(task.ID, "mail.read_message")
	mustNodes(t, c, task.ID, "skill_propose", map[string]any{"skill": "code", "instructions": "Lis le mail du bug, puis corrige.", "why": "Le bug venait d'un mail."})
	p, ok := skillCard(c, "code")
	if !ok || !p.Pending || !strings.Contains(p.Tainted, "mail.read_message") {
		t.Fatalf("a tainted proposal's card %+v", p)
	}
}
