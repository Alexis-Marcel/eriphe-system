package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Where a task writes (#768): where it works. Its node and every node
// under it; the node it was launched for, its node's parent, when its
// node is its own (the brain's launches; a skill's run works in its home
// node, a step in its step node, and writes those); and the nodes linked
// to or from these, one hop, read at each call. Elsewhere a write is
// refused with a hint: say it in its node, or ask for it.
//
// A link counts only when someone else wrote it: a task writes [[links]]
// in the nodes it may write, and a link of its own would open it any
// node. So a link from one of these nodes counts when the node's last
// version by another author (the user, the brain, another task; not
// code, whose versions carry what was there) has it too; a link to one
// of them counts when that node was not opened by the task's author
// itself, since a [[Name]] waiting anywhere resolves to a node the task
// opens with that name.
//
// What holds for its own node holds for every node it writes
// (task_nodes.go): the version expected, the owner's marks and code's
// values kept, its author, external when it is tainted, never an
// archived node; it never archives, approves or closes one.

// taskWriteAncestors is how far up a node's parents writable looks for
// the task's node.
const taskWriteAncestors = 64

// writable is the node a task may write: id, its own when id is empty;
// an error with a hint when it may not.
func (c *core) writable(w nodeWriter, id string) (string, error) {
	if c.nodes == nil {
		return "", errNoGraph
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = w.node
	}
	if id == "" {
		return "", errors.New("this task has no node")
	}
	sum, err := c.nodes.Get(id)
	if errors.Is(err, nodestore.ErrNotFound) {
		return "", fmt.Errorf("no node %s: node_find finds it", id)
	}
	if err != nil {
		return "", err
	}
	if !sum.Archived.IsZero() {
		return "", fmt.Errorf("node %s is archived: an archived node is never written; say what you found in your node", id)
	}
	if w.node == "" {
		return "", errors.New("this task has no node")
	}
	launch := c.launchNode(w)
	base := func(x string) bool { return x == launch || c.under(x, w.node) }
	if base(id) {
		return id, nil
	}
	// Linked from one of them, by someone else.
	if back, err := c.nodes.Backlinks(id); err == nil {
		for _, b := range back {
			if base(b.ID) && c.linkedByOther(b.ID, id, w.author) {
				return id, nil
			}
		}
	}
	// Linked to one of them, not opened by the task's author.
	for _, l := range sum.Links {
		if base(l) && (l == launch || c.firstAuthor(l) != w.author) {
			return id, nil
		}
	}
	where := "your node " + w.node + " and the nodes under it"
	if launch != "" {
		where += ", the node you work for " + launch
	}
	return "", fmt.Errorf("you write where you work: %s, and the nodes someone else linked to or from them; not %s. Say what you found in your node; what should change there, ask for it in your final message: Eriphe or the user writes it", where, id)
}

// launchNode is the node a task was launched for: its node's parent when
// its node is its own (opened for it by code, as its author); "" for a
// skill's run (its home node), a step (its step node), or a node at the
// top.
func (c *core) launchNode(w nodeWriter) string {
	if w.node == "" || c.runOfTask(w.task) != nil || c.firstAuthor(w.node) != "task:"+w.task {
		return ""
	}
	sum, err := c.nodes.Get(w.node)
	if err != nil {
		return ""
	}
	return sum.Parent
}

// under tells whether node id is top or below it.
func (c *core) under(id, top string) bool {
	if top == "" {
		return false
	}
	for range taskWriteAncestors {
		if id == top {
			return true
		}
		if id == "" {
			return false
		}
		sum, err := c.nodes.Get(id)
		if err != nil {
			return false
		}
		id = sum.Parent
	}
	return false
}

// firstAuthor is who made node id's first version; "" when not known.
func (c *core) firstAuthor(id string) string {
	vs, err := c.nodes.Versions(id)
	if err != nil || len(vs) == 0 {
		return ""
	}
	return vs[0].Author
}

// linkedByOther tells whether node from's link to node to was written by
// someone other than author: its last version by another author, code's
// left out, links to it too.
func (c *core) linkedByOther(from, to, author string) bool {
	vs, err := c.nodes.Versions(from)
	if err != nil {
		return false
	}
	for _, v := range slices.Backward(vs) {
		if v.Author == author || v.Author == nodeByCode {
			continue
		}
		n := v.N
		if n == vs[len(vs)-1].N {
			n = 0 // the last: its file, not the repository
		}
		d, err := c.nodes.Read(from, n)
		if err != nil {
			return false
		}
		if slices.Contains(d.Node.Meta.Links, to) {
			return true
		}
		var names []string
		for _, l := range d.Node.WikiLinks() {
			names = append(names, l.Name)
		}
		if len(names) == 0 {
			return false
		}
		found, err := c.nodes.Resolve(from, names)
		if err != nil {
			return false
		}
		for _, x := range found {
			if x.ID == to {
				return true
			}
		}
		return false
	}
	return false
}
