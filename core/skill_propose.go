package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Skills get better with use (#769): a task that follows a skill (run_task's
// skill, or a skill's run) and had to correct its method to finish proposes
// a new version of it with skill_propose, a tool of its eriphe-nodes. Only
// of that skill, and only its instructions: the new version keeps the
// approved one's front matter byte for byte (its trigger, the tools,
// repositories and model it uses) and its other files, so a task never
// changes what its skill is lent nor when it runs. The version waits for
// the user's yes on its card exactly as one the brain writes
// (proposeSkillVersion, kind skill): nothing runs before it, the approved
// version stays until then and is kept after (skill_kept), and a tainted
// task's proposal is marked so. The card says why, in the task's words;
// the brain is told, as an event of the task's node, that a version waits.

// skillProposeInstruction is what a task that follows a skill is told of
// skill_propose.
const skillProposeInstruction = "You follow one of the user's skills: its method is in your prompt. If, to finish, you had to correct that method because it was wrong, or missed a step or a check it should have had, propose a better version of it with skill_propose, once, before your final message. Never for a detail of this node or of this one time."

// skillWhyMax is the most runes of why a task proposes a version.
const skillWhyMax = 300

type skillProposeIn struct {
	Skill        string `json:"skill" jsonschema:"the name of the skill you follow: only that one"`
	Instructions string `json:"instructions" jsonschema:"its whole instructions, rewritten: markdown, in the language of the skill; what the next task that follows it reads"`
	Why          string `json:"why" jsonschema:"what was wrong or missing in its method, one line in the user's language: the user reads it on the card"`
}

// skillBy is who proposed a version of a skill: the task that followed it,
// and why.
type skillBy struct {
	Task string `json:"task"`
	Why  string `json:"why"`
}

func taskSkillKey(task string) string    { return "task_skill:" + task }
func skillByKey(name, sha string) string { return "skill_by:" + name + ":" + sha }

// taskSkill is the skill task follows, "" for none.
func (c *core) taskSkill(task string) string { return c.store.get(taskSkillKey(task)) }

// skillProposedBy is the task that proposed a version of a skill, nil
// when none did (the brain, an upload).
func (c *core) skillProposedBy(name, sha string) *skillBy {
	raw := c.store.get(skillByKey(name, sha))
	if raw == "" {
		return nil
	}
	var b skillBy
	if json.Unmarshal([]byte(raw), &b) != nil {
		return nil
	}
	return &b
}

// proposeTaskSkill is a task's skill_propose: a new version of the skill
// it follows, its instructions only, waiting for the user's yes.
func (c *core) proposeTaskSkill(task string, in skillProposeIn) (map[string]any, error) {
	name := c.taskSkill(task)
	want := strings.TrimSpace(in.Skill)
	switch {
	case name == "":
		return nil, errors.New("you follow no skill: only a task started with a skill proposes a version of it")
	case want != name:
		return nil, fmt.Errorf("you follow the skill %q: you propose a version of it only, never of another", name)
	}
	body, why := strings.TrimSpace(in.Instructions), oneLine(in.Why)
	switch {
	case body == "":
		return nil, errors.New("instructions are needed: the skill's whole instructions, rewritten")
	case len(body) > skillBodyMax:
		return nil, fmt.Errorf("instructions of %d bytes: %d at most", len(body), skillBodyMax)
	case why == "":
		return nil, errors.New("why is needed: what was wrong or missing in its method, in one line")
	case runeLen(why) > skillWhyMax:
		return nil, fmt.Errorf("why is %d characters at most", skillWhyMax)
	}
	k, err := c.store.skill(name)
	if err != nil || !k.Active() {
		return nil, fmt.Errorf("the skill %q is no longer approved and on: nothing to propose", name)
	}
	if k.Pending != "" {
		// Never in place of a version the user has yet to read, theirs or
		// another's: they answer that one first.
		return nil, fmt.Errorf("a version of the skill %q already waits for the user's yes: say in your node what its method lacked", name)
	}
	v, err := c.store.skillVersion(k.Approved)
	if err != nil {
		return nil, fmt.Errorf("the skill %q: its approved version does not read", name)
	}
	files := make([]skillFile, 0, len(v.Files))
	for _, f := range v.Files {
		if f.Path == "SKILL.md" {
			data, err := withSkillBody(f.Data, body)
			if err != nil {
				return nil, err
			}
			f.Data = data
		}
		files = append(files, f)
	}
	nk, changed, err := c.proposeSkillVersion(files, c.taskTaint(task), nil)
	if err != nil {
		return nil, err
	}
	if !changed {
		return map[string]any{"result": "the same as the approved version: nothing to propose"}, nil
	}
	raw, _ := json.Marshal(skillBy{Task: task, Why: why})
	c.store.set(skillByKey(name, nk.Pending), string(raw))
	id := c.skillRowID(name)
	ref := proposalRef(cardSkill, id)
	c.cardNow(cardSkill, id, "", "task:"+task)
	// Code's words only: the task's are on the card.
	c.nodeEvent(c.taskNode(task), fmt.Sprintf("La tâche qui suit le skill « %s » en propose une nouvelle version (carte %s) : elle attend le oui de l'utilisateur sur sa carte, la version approuvée reste en place d'ici là.", name, ref), false)
	return map[string]any{"result": "proposed: it waits for the user's yes on its card; until then the approved version stays", "card": ref}, nil
}

// withSkillBody is a SKILL.md with its front matter as it is and body as
// its instructions.
func withSkillBody(raw []byte, body string) ([]byte, error) {
	text := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\xef\xbb\xbf"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return nil, errors.New("the skill's SKILL.md has no front matter")
	}
	front, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return nil, errors.New("the skill's SKILL.md has no front matter")
	}
	return []byte("---\n" + front + "\n---\n\n" + body + "\n"), nil
}
