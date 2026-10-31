package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// A repository's skills load only when eriphe-core vouched for it; the
// brain's never do.
func TestRepoSkillsOff(t *testing.T) {
	for _, c := range []struct {
		spec TaskSpec
		off  bool
	}{
		{TaskSpec{}, false}, // no repository: nothing of anyone else's
		{TaskSpec{Repo: "someone/tool"}, true},
		{TaskSpec{Repo: "owner/repo", RepoSkills: true}, false},
		{TaskSpec{NoAgentMemory: true}, true},
		{TaskSpec{NoAgentMemory: true, Repo: "owner/repo", RepoSkills: true}, true},
	} {
		if got := repoSkillsOff(c.spec); got != c.off {
			t.Errorf("%+v: off %v, want %v", c.spec, got, c.off)
		}
	}
}

// Claude Code takes its permission rules from the task's managed settings
// only, which ignore a skill's allowed-tools; the spec's allowed tools are
// their allow rules. A repository not vouched for loads no skill.
func TestClaudeSkillGuards(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	for _, c := range []struct {
		spec TaskSpec
		off  bool
	}{
		{TaskSpec{Repo: "owner/repo", RepoSkills: true}, false},
		{TaskSpec{Repo: "someone/tool"}, true},
		{TaskSpec{Profile: "brain", NoAgentMemory: true, AllowedTools: []string{"mcp__eriphe-core", "mcp__notes__search"}}, true},
	} {
		task := &Task{ID: "t-1", Spec: c.spec}
		raw := claudeCode{}.files(n, task)[claudeManaged+"/managed-settings.json"]
		var m struct {
			Only        bool `json:"allowManagedPermissionRulesOnly"`
			NoShell     bool `json:"disableSkillShellExecution"`
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if !m.Only || m.NoShell != c.off || !reflect.DeepEqual(m.Permissions.Allow, c.spec.AllowedTools) || m.Permissions.Deny != nil {
			t.Errorf("%+v: managed settings %s", c.spec, raw)
		}
		args, _ := claudeCode{}.command(n, task)
		if got := slices.Contains(args, "--disable-slash-commands"); got != c.off {
			t.Errorf("%+v: --disable-slash-commands %v", c.spec, got)
		}
	}
}

func TestOpenCodeSkillGuards(t *testing.T) {
	env := opencodeSetup(&Task{Spec: TaskSpec{Repo: "owner/repo", RepoSkills: true}})
	if env["OPENCODE_DISABLE_EXTERNAL_SKILLS"] != "" || env["OPENCODE_DISABLE_PROJECT_CONFIG"] != "1" {
		t.Errorf("own repository: %v", env)
	}
	if strings.Contains(env["OPENCODE_CONFIG_CONTENT"], `"skill"`) {
		t.Errorf("a worker's skill tool is denied: %s", env["OPENCODE_CONFIG_CONTENT"])
	}
	if env := opencodeSetup(&Task{Spec: TaskSpec{Repo: "someone/tool"}}); env["OPENCODE_DISABLE_EXTERNAL_SKILLS"] != "1" {
		t.Errorf("someone else's repository: %v", env)
	}
	env = opencodeSetup(&Task{Spec: TaskSpec{NoAgentMemory: true}})
	var cfg struct {
		Permission map[string]string `json:"permission"`
	}
	json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &cfg)
	if env["OPENCODE_DISABLE_EXTERNAL_SKILLS"] != "1" || cfg.Permission["skill"] != "deny" {
		t.Errorf("the brain: %v", env)
	}
}

func TestCodexSkillGuards(t *testing.T) {
	for spec, off := range map[*TaskSpec]bool{
		{Repo: "owner/repo", RepoSkills: true}: false,
		{Repo: "someone/tool"}:                 true,
		{NoAgentMemory: true}:                  true,
	} {
		task := newTestTask()
		task.Spec = *spec
		args, _ := codexRuntime{}.command(newNode(options{}), task)
		if got := hasSeq(args, "-c", "skills.include_instructions=false"); got != off {
			t.Errorf("%+v: skills off %v: %v", *spec, got, args)
		}
	}
}
