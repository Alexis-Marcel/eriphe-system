package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// A task the brain launches (run_task, #767): a node, a skill if one fits,
// and a short ask. The brain does not write the task's prompt: code builds
// it from the ask, the skill's method (its SKILL.md body, as a skill's run
// gets it), the node's surroundings (task_surroundings.go) and the standing
// worker instructions (workerVoice). The context is the graph, the method
// the skill: the brain never restates the node.
//
// A skill's declared tools, repository and model (a triggered skill's
// front matter: skill_triggers.go) are the launch's defaults: resources
// named by the call replace them, and an agent named wins over its model.
// Rights do not move: what the task is lent goes through its start card,
// as for any launch of the brain (task_start.go).

// launchRequest is run_task's input.
type launchRequest struct {
	Title     string        `json:"title,omitempty" jsonschema:"what the user reads, a few words in their language"`
	Ask       string        `json:"ask" jsonschema:"one or two sentences: the user's request as said, or « move this node forward ». Never the node's content: the task starts with its surroundings and reads the graph"`
	Skill     string        `json:"skill,omitempty" jsonschema:"an approved skill that fits (list_skills): its instructions are the task's method, its tools, repository and model the defaults"`
	Node      string        `json:"node,omitempty" jsonschema:"the node it works for, under which it gets its own; empty for this turn's node"`
	Resources []ResourceRef `json:"resources,omitempty" jsonschema:"what it is lent, as node_open's steps' resources: only what it needs, the least access; given, they replace the skill's"`
	Agent     string        `json:"agent,omitempty" jsonschema:"from list_agents; empty for the skill's model, else the default"`
	Device    string        `json:"device,omitempty" jsonschema:"empty: the scheduler chooses"`
	// Contract: what must be true at the end, for a task that changes
	// something (contract.go); none for a search or a read.
	Contract *taskContract `json:"contract,omitempty" jsonschema:"for a task that changes something, as node_open's steps' contract; none for a read. Approval is never in it"`
}

// launchAskMax is the most runes of an ask: a sentence or two, not a prompt.
const launchAskMax = 1000

// launchOf is the task a launch asks for: its prompt the ask, its method
// the skill's, its resources and agent the call's, else the skill's.
func (c *core) launchOf(in launchRequest) (runRequest, error) {
	ask := strings.TrimSpace(in.Ask)
	switch {
	case ask == "":
		return runRequest{}, errors.New("ask is required: the user's request in a sentence or two, or « move this node forward »")
	case runeLen(ask) > launchAskMax:
		return runRequest{}, fmt.Errorf("an ask of %d characters: %d at most. Say what to do in a sentence or two; the task reads its node and the graph, and a skill gives the method", runeLen(ask), launchAskMax)
	}
	r := runRequest{Title: in.Title, Prompt: ask, Resources: in.Resources, Agent: in.Agent, Device: in.Device,
		Node: in.Node, Contract: in.Contract}
	name := strings.TrimSpace(in.Skill)
	if name == "" {
		return r, nil
	}
	m, err := c.launchSkill(name)
	if err != nil {
		return runRequest{}, err
	}
	r.method = "[Your method: the user's skill « " + name + " », approved by them. Follow it for what you are asked]\n" + skillMethod(name, m)
	if t := m.Trigger; t != nil {
		if len(r.Resources) == 0 {
			for _, tool := range t.Tools {
				r.Resources = append(r.Resources, ResourceRef{Kind: resTool, Ref: tool})
			}
			// A task takes one repository: a skill that declares several
			// lets the call name the one.
			if len(t.Repos) == 1 {
				r.Resources = append(r.Resources, ResourceRef{Kind: resRepo, Ref: t.Repos[0]})
			}
		}
		if strings.TrimSpace(r.Agent) == "" {
			r.Agent = t.Model
		}
	}
	return r, nil
}

// launchSkill is the approved version of the user's skill name, active
// (approved and on); an error listing the active ones otherwise.
func (c *core) launchSkill(name string) (skillManifest, error) {
	var names []string
	for _, k := range c.activeSkills() {
		if k.Name != name {
			names = append(names, k.Name)
			continue
		}
		if m, ok := c.skillManifestOf(k.Approved); ok {
			return m, nil
		}
		return skillManifest{}, fmt.Errorf("skill %q: its approved version does not read", name)
	}
	if len(names) == 0 {
		return skillManifest{}, fmt.Errorf("skill %q is not one of the user's approved skills, and none is: launch without a skill", name)
	}
	slices.Sort(names)
	return skillManifest{}, fmt.Errorf("skill %q is not one of the user's approved skills: %s", name, strings.Join(names, ", "))
}

// skillMethod is a skill as a task follows it: its name, what it is for,
// and its instructions.
func skillMethod(name string, m skillManifest) string {
	return fmt.Sprintf("Skill « %s » : %s\n\n%s", name, m.Description, m.Body)
}
