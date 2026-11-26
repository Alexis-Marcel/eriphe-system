package main

import (
	"slices"
)

// There are only nodes. A skill may name a home node it maintains (its
// when's node:): its runs work in it, and it may still act on any other.
// Whether a node is maintained is computed from the skills, never stored
// in its file: the skills approved and on that name it as their home. A
// maintained node is not set done (node_tools.go), the graph draws its
// satellite, the brain sees « entretenu par <skill> » on its line, and its
// page's ⋯ says what keeps it alive (node_alive.go).

// homeSkills are the skills maintaining each node, by the node's id: the
// skills approved and on whose when names it. One pass over the skills,
// for a list of many nodes.
func (c *core) homeSkills() map[string][]string {
	out := map[string][]string{}
	for _, k := range c.triggeredSkills() {
		if k.Trigger != nil && k.Trigger.Node != "" {
			out[k.Trigger.Node] = append(out[k.Trigger.Node], k.Name)
		}
	}
	for id := range out {
		slices.Sort(out[id])
	}
	return out
}

// maintainers are the skills maintaining node id, by name; none for a
// node no skill names as its home.
func (c *core) maintainers(id string) []string { return c.homeSkills()[id] }
