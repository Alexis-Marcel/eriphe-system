package main

import (
	"net/http"
	"slices"
	"strings"
)

// The user's skills in worker tasks. Every worker task gets the skills
// active when it starts, by name and hash (spec.skills); its node fetches
// each bundle with its own secret, checks the hash, and mounts the skills
// read-only at /run/eriphe/skills/.claude/skills/<name>, where it points
// the agent (node/skills.go). Scripts run in the task's container, under
// its permissions, like any other command.
//
//	GET /v1/node/tasks/{id}/skills/{sha256}   (node secret) → the bundle (a tar)

// nodeSkill is a skill as a task's spec names it.
type nodeSkill struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// skillsDir is where a task finds the user's skills.
const skillsDir = "/run/eriphe/skills/.claude/skills"

// skillsNative tells whether the task's agent loads the mounted skills by
// itself. Claude Code and Codex keep every skill off in a task on a
// repository that is not the user's (node/skills.go): theirs are then
// listed in the task's instructions, as for an ACP agent, which loads none.
func skillsNative(runtime string, repoSkillsOff bool) bool {
	switch runtime {
	case "opencode":
		return true
	case "claude-code", "", "codex":
		return !repoSkillsOff
	}
	return false
}

// skillsIndex tells a task's agent where the user's skills are, when it
// does not load them itself.
func skillsIndex(skills []Skill) string {
	var b strings.Builder
	b.WriteString("The user's skills are mounted read-only under " + skillsDir + ", one folder each. " +
		"Before a task one of them covers, read its SKILL.md and follow it; run its scripts from that folder:\n")
	for _, k := range skills {
		b.WriteString("- " + k.Name + ": " + strings.Join(strings.Fields(k.Description), " ") + " (" + skillsDir + "/" + k.Name + "/SKILL.md)\n")
	}
	return strings.TrimSpace(b.String())
}

// lendSkills gives a worker task the active skills, and tells its agent of
// them when it does not load them itself.
func (c *core) lendSkills(spec *nodeTaskSpec) {
	skills := c.activeSkills()
	if len(skills) == 0 {
		return
	}
	for _, k := range skills {
		spec.Skills = append(spec.Skills, nodeSkill{Name: k.Name, SHA256: k.Approved})
	}
	if !skillsNative(spec.Runtime, spec.Repo != "" && !spec.RepoSkills) {
		spec.AppendSystemPrompt = strings.TrimSpace(spec.AppendSystemPrompt + "\n\n" + skillsIndex(skills))
	}
}

// handleTaskSkill serves a node one of its task's skills.
func (c *core) handleTaskSkill(w http.ResponseWriter, r *http.Request) {
	node, ok := c.nodeFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, err := c.store.task(r.PathValue("id"))
	if err != nil || t.Node != node || finished(t.State) {
		http.Error(w, "no such task on this node", http.StatusNotFound)
		return
	}
	spec, err := c.store.taskSpec(t.ID)
	sha := r.PathValue("sha")
	if err != nil || !slices.ContainsFunc(spec.Skills, func(k nodeSkill) bool { return k.SHA256 == sha }) {
		http.Error(w, "no such skill in this task", http.StatusNotFound)
		return
	}
	data, err := c.store.skillBundle(sha)
	if err != nil {
		http.Error(w, "this version of the skill is gone", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Write(data)
}
