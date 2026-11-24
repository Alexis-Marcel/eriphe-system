package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
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

// nodeSourceOK is the check of what a node's block may read live, for
// any author: a catalog server's tool or resource (read as the brain's
// free reads, sourceWhy), a monitor the user approved, or node_find
// (node_find.go).
func (c *core) nodeSourceOK() func(nodefile.Source) error {
	return func(src nodefile.Source) error {
		switch {
		case src.Monitor != "":
			if m, err := c.monitorNamed(src.Monitor); err != nil || m.State != monitorOn {
				return fmt.Errorf("%s is not a monitor the user approved: read its tool or resource directly until it is", src.Monitor)
			}
		case src.Tool == nodeFindSource:
			return checkNodeFindSource(src)
		case src.Tool != "":
			server, _, _ := strings.Cut(src.Tool, "/")
			if _, ok := c.conf().Servers[server]; !ok {
				return fmt.Errorf("%s is not a tool of the catalog (list_tools)", src.Tool)
			}
		case src.Resource != "":
			if _, ok := c.conf().Servers[src.Resource]; !ok {
				return fmt.Errorf("%s is not a server of the catalog (list_tools)", src.Resource)
			}
		}
		return nil
	}
}

// sourceText is what a block reads, in a word.
func sourceText(s nodefile.Source) string {
	switch {
	case s.Monitor != "":
		return s.Monitor
	case s.Resource != "":
		return s.Resource + " " + s.URI
	}
	return s.Tool
}
