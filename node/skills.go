package main

import "encoding/json"

// Agent skills (agentskills.io): a folder with a SKILL.md, instructions
// and often scripts, that the agents load by themselves from their
// configuration and from the repository they work in.
//
// Two rules hold for every runtime:
//
//   - A repository's own skills (.claude/skills, .agents/skills,
//     .opencode/skills) load only when eriphe-core says so (repo_skills:
//     the user's own repository, or one the user trusts). The brain loads
//     none of its own.
//   - A skill never approves a tool by itself: Claude Code's allowed-tools
//     frontmatter would run the tools it lists without asking, so its
//     permission rules come from a managed settings file only, which
//     ignores skills' allowed-tools (claudeManagedSettings). OpenCode and
//     Codex do not read allowed-tools: every call still goes through
//     their permission requests, which the node relays.
//
// What each runtime does:
//
//	Claude Code  --disable-slash-commands (no skill, no Skill tool) and
//	             disableSkillShellExecution
//	OpenCode     OPENCODE_DISABLE_EXTERNAL_SKILLS (the .claude and .agents
//	             skills); its .opencode ones are off anyway with the
//	             project's configuration (OPENCODE_DISABLE_PROJECT_CONFIG);
//	             the brain also denies the skill tool
//	Codex        skills.include_instructions=false: no skill catalog in
//	             the model's context
//	ACP          nothing to load: a custom agent's own business

// repoSkillsOff tells whether the skills the task's repository carries
// must stay off: a repository eriphe-core did not vouch for. The brain's
// are always off (it has no repository, but its profile is shared).
func repoSkillsOff(spec TaskSpec) bool {
	return spec.NoAgentMemory || (spec.Repo != "" && !spec.RepoSkills)
}

// claudeManaged is the run subdirectory mounted at Claude Code's managed
// settings directory (/etc/claude-code on Linux), read-only: a task cannot
// change it.
const (
	claudeManaged      = "claude-managed"
	claudeManagedMount = "/etc/claude-code"
)

// claudeManagedSettings is the task's managed-settings.json. With
// allowManagedPermissionRulesOnly, Claude Code takes permission rules from
// managed settings only: it ignores those of the user's, the project's and
// --settings files, --allowedTools (the spec's allowed tools are its allow
// rules here instead), and the allowed-tools of skills and commands from a
// project, the user's skills and an --add-dir directory (Claude Code
// 2.1.282 or later). What is not allowed here asks, through the node's
// permission tool.
func claudeManagedSettings(spec TaskSpec) []byte {
	m := map[string]any{"allowManagedPermissionRulesOnly": true}
	if len(spec.AllowedTools) > 0 {
		m["permissions"] = map[string]any{"allow": spec.AllowedTools}
	}
	if repoSkillsOff(spec) {
		// Belt and braces: no skill loads anyway (--disable-slash-commands).
		m["disableSkillShellExecution"] = true
	}
	raw, _ := json.Marshal(m)
	return raw
}
