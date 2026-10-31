package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's side of the user's skills. The brain never loads a skill by
// itself (node/skills.go): it lists them with list_skills and reads one
// with read_skill, as text. Nothing of a skill runs in the brain's
// container: its scripts run in tasks, which all have the skills. A tool
// rather than a mount keeps code and the user's files out of the brain's
// privileged, long-lived container, and works whatever its runtime. A
// skill is the user's own, approved by them: reading it is not external
// content.

// maxSkillRead is what read_skill returns of a file at most.
const maxSkillRead = 64 << 10

func registerSkillTools(s *mcp.Server, c *core) {
	tool(s, "list_skills", "The user's skills: instructions (and maybe scripts) the user approved in Settings → Skills. Each has a name, a description saying when it applies, its files, and whether it has scripts. Read one with read_skill before following it. Every worker task has them too, at "+skillsDir+"/<name>: to run a skill's scripts, start a task.",
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			type entry struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				HasScripts  bool     `json:"has_scripts"`
				Files       []string `json:"files"`
			}
			out := []entry{}
			for _, k := range c.activeSkills() {
				v, err := c.store.skillVersion(k.Approved)
				if err != nil {
					continue
				}
				e := entry{Name: k.Name, Description: v.Manifest.Description, HasScripts: len(v.Scripts()) > 0}
				for _, f := range v.Files {
					e.Files = append(e.Files, f.Path)
				}
				out = append(out, e)
			}
			return map[string]any{"skills": out}, nil
		})

	type readIn struct {
		Name string `json:"name" jsonschema:"the skill's name (list_skills)"`
		Path string `json:"path,omitempty" jsonschema:"a file of the skill, such as references/guide.md; empty for its SKILL.md"`
	}
	tool(s, "read_skill", "Read one of the user's skills: its SKILL.md (the instructions to follow), or another of its text files. Scripts are not read here: they run in tasks.",
		func(ctx context.Context, in readIn) (map[string]any, error) {
			k, err := c.store.skill(strings.TrimSpace(in.Name))
			if err != nil || !k.Active() {
				return nil, fmt.Errorf("no skill %q (see list_skills)", in.Name)
			}
			v, err := c.store.skillVersion(k.Approved)
			if err != nil {
				return nil, err
			}
			p := strings.TrimPrefix(strings.TrimSpace(in.Path), "./")
			if p == "" {
				p = "SKILL.md"
			}
			for _, f := range v.Files {
				if f.Path != p {
					continue
				}
				if isScript(f) {
					return nil, fmt.Errorf("%s is a script: it runs in a task, which has the skill at %s/%s", p, skillsDir, k.Name)
				}
				text := string(f.Data)
				out := map[string]any{"name": k.Name, "path": p}
				if len(text) > maxSkillRead {
					text, out["truncated"] = text[:maxSkillRead], true
				}
				out["content"] = text
				return out, nil
			}
			return nil, fmt.Errorf("skill %s has no file %q", k.Name, p)
		})
}
