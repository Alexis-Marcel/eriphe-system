package main

import (
	"fmt"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A task's surroundings (#766): its node's place in the graph, which code adds
// to the first prompt of every worker task with a node (startTask), a
// skill's run in its home node too. Short and capped: its node (title,
// goal), the chain of its ancestors (title, goal), and an index of its
// children, links and backlinks (id, status, title, goal). Nothing else:
// nothing about the user, no memory; the task reads more with node_read
// and node_find when its work needs it (task_nodes.go).
//
// It quotes no one else's words: a title that may be (written after an
// external read) is named, not quoted, and an external node's goal is
// left out, as node_find gives them (foundNode). The task reads them with
// node_read, which taints it.

const (
	aroundHeading = "[Your node on Eriphe's graph: where your work sits. node_read reads any of these nodes, node_find finds others]"
	// aroundMax is the most runes of its surroundings: about 2,000 tokens.
	aroundMax = 6000
	// aroundAncestors is how many ancestors it gives, the nearest first.
	aroundAncestors = 8
	// aroundIndexMost is how many nodes of each index (children, links,
	// backlinks) it looks at, at most.
	aroundIndexMost = 100
	aroundTitleMax  = 120
	aroundGoalMax   = 160
)

// aroundSection is one of its indexes.
type aroundSection struct {
	title string
	nodes []nodestore.Summary
	more  int // past aroundIndexMost
}

// surroundings are node id's place in the graph, for a task working in
// it: "" for none.
func (c *core) surroundings(id string) string {
	if id == "" || c.nodes == nil {
		return ""
	}
	self, err := c.nodes.Get(id)
	if err != nil {
		return ""
	}
	// Its ancestors, the nearest first; a parent seen twice ends the walk.
	var chain []nodestore.Summary
	seen := map[string]bool{id: true}
	higher := false
	for p := self.Parent; p != "" && !seen[p]; {
		if len(chain) == aroundAncestors {
			higher = true
			break
		}
		seen[p] = true
		x, err := c.nodes.Get(p)
		if err != nil {
			break
		}
		chain = append(chain, x)
		p = x.Parent
	}
	live := func(xs []nodestore.Summary) aroundSection {
		var s aroundSection
		for _, x := range xs {
			if !x.Archived.IsZero() || x.ID == id {
				continue
			}
			if len(s.nodes) == aroundIndexMost {
				s.more++
				continue
			}
			s.nodes = append(s.nodes, x)
		}
		return s
	}
	kids, _ := c.nodes.Children(id)
	var linked []nodestore.Summary
	n := min(len(self.Links), aroundIndexMost)
	for _, l := range self.Links[:n] {
		if x, err := c.nodes.Get(l); err == nil {
			linked = append(linked, x)
		}
	}
	links := live(linked)
	links.more += len(self.Links) - n
	back, _ := c.nodes.Backlinks(id)
	sections := []aroundSection{live(kids), links, live(back)}
	sections[0].title, sections[1].title, sections[2].title = "Its children", "It links to", "Linked from"

	// Their status, read at once.
	all := append([]nodestore.Summary{self}, chain...)
	for _, s := range sections {
		all = append(all, s.nodes...)
	}
	status := map[string]string{}
	for _, x := range c.withStatus(all) {
		status[x.ID] = x.Status
	}

	var b strings.Builder
	b.WriteString(aroundHeading + "\n")
	b.WriteString("Your node: " + aroundLine(self, status[self.ID]) + "\n")
	if len(chain) > 0 {
		b.WriteString("Its ancestors, the nearest first:\n")
		for _, x := range chain {
			b.WriteString("- " + aroundLine(x, status[x.ID]) + "\n")
		}
		if higher {
			fmt.Fprintf(&b, "- … and higher: node_read %s gives its parent\n", chain[len(chain)-1].ID)
		}
	}
	// The indexes share what is left, each its part; what one leaves goes
	// to the next.
	left := aroundMax - runeLen(b.String())
	var full []aroundSection
	for _, s := range sections {
		if len(s.nodes) > 0 {
			full = append(full, s)
		}
	}
	for i, s := range full {
		share := left / (len(full) - i)
		head := fmt.Sprintf("%s (%d):\n", s.title, len(s.nodes)+s.more)
		used := runeLen(head)
		var lines []string
		for _, x := range s.nodes {
			line := "- " + aroundLine(x, status[x.ID]) + "\n"
			if used+runeLen(line)+aroundMoreRoom > share {
				break
			}
			lines, used = append(lines, line), used+runeLen(line)
		}
		if len(lines) == 0 && share < used+aroundMoreRoom {
			continue // no room for even its count
		}
		b.WriteString(head)
		for _, line := range lines {
			b.WriteString(line)
		}
		if n := len(s.nodes) + s.more - len(lines); n > 0 {
			more := fmt.Sprintf("- … %d more: node_read %s lists them\n", n, id)
			b.WriteString(more)
			used += runeLen(more)
		}
		left -= used
	}
	return strings.TrimRight(b.String(), "\n")
}

// aroundMoreRoom is what a section keeps for its « … n more » line.
const aroundMoreRoom = 80

// aroundLine is a node in a line: id, status, title and goal. A title
// that may be someone else's words is named, not quoted; an external
// node's goal is left out.
func aroundLine(x nodestore.Summary, status string) string {
	if status == "" {
		status = nodeIdle
	}
	if x.TitleExternal() {
		return fmt.Sprintf("%s [%s]: written after reading external content, its title is not quoted: node_read reads it, and that marks your work external", x.ID, status)
	}
	line := fmt.Sprintf("%s [%s] « %s »", x.ID, status, truncate(packQuote(x.Title), aroundTitleMax))
	switch {
	case x.External:
		line += " (it may hold someone else's words)"
	case strings.TrimSpace(x.Goal) != "":
		line += " — goal: " + truncate(packQuote(oneLine(x.Goal)), aroundGoalMax)
	}
	return line
}
