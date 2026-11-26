package main

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// node_find (#743): words, and filters on a node's tags and properties,
// a sort, the properties to give as columns, a limit. The brain and a
// task call it (task_nodes.go), and a block names it as its source
// ({"tool": "eriphe-core/node_find", "args": {…}}): code reads it like
// any source, so a page « Mes études en attente » stays up to date by
// itself, read again at once when a node changes.
//
// Trust is the same for all: a title written after an external read,
// and the properties and tags of a node that may hold someone else's
// words (an external one), are given only with include_external. The
// brain's call then taints its turn, a task's the task; a block whose
// source says it is external, and reading it taints (untrustedSources).

const (
	// nodeFindMax is how many nodes node_find gives by default; nodeFindMost
	// the most it gives.
	nodeFindMax  = 30
	nodeFindMost = 200
	// nodeGoalMax is how much of a node's goal node_find and the brain's
	// node list give.
	nodeGoalMax = 200
	// nodeFindTaint is what node_find makes the turn when it quotes words
	// that may be someone else's.
	nodeFindTaint = "node_find.external"
)

type nodeFindIn struct {
	Query string `json:"query,omitempty" jsonschema:"words to look for; with neither words nor filters, the nodes at work or waiting, the heaviest first, then the last changed"`
	// Tags and Where filter on the node's properties.
	Tags     []string     `json:"tags,omitempty" jsonschema:"tags it has, each one or one below it (cuisine finds cuisine/recette)"`
	Where    []nodeCondIn `json:"where,omitempty" jsonschema:"conditions on its properties, all must hold"`
	Sort     string       `json:"sort,omitempty" jsonschema:"a property, or title, created, changed; default: the best for the words, else the last changed"`
	Desc     bool         `json:"desc,omitempty" jsonschema:"sort from the largest, the latest"`
	Columns  []string     `json:"columns,omitempty" jsonschema:"properties to give with each node (tags too)"`
	Limit    int          `json:"limit,omitempty" jsonschema:"how many, 30 by default, 200 at most"`
	Archived bool         `json:"archived,omitempty" jsonschema:"the archived nodes too"`
	// IncludeExternal quotes the titles written after an external read,
	// as since does: it taints the turn.
	IncludeExternal bool `json:"include_external,omitempty" jsonschema:"quote what may be someone else's words too (a title written after an external read, an external node's properties): this taints the turn, or the task"`
}

// nodeCondIn is a condition on a property.
type nodeCondIn struct {
	Key   string `json:"key" jsonschema:"the property"`
	Op    string `json:"op,omitempty" jsonschema:"one of = != < <= > >= exists missing; = by default"`
	Value any    `json:"value,omitempty" jsonschema:"a text, a number or a date (2026-10-01), compared as such"`
}

// filtered tells a find with filters or a sort: one the index answers.
func (in nodeFindIn) filtered() bool {
	return len(in.Tags) > 0 || len(in.Where) > 0 || strings.TrimSpace(in.Sort) != ""
}

// MarshalJSON writes a node found with its columns beside its fields, as
// a block's Table reads them; a column named as a field (title, status,
// …) gives way to it.
func (f nodeFound) MarshalJSON() ([]byte, error) {
	type plain nodeFound
	raw, err := json.Marshal(plain(f))
	if err != nil || len(f.Props) == 0 {
		return raw, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range f.Props {
		if !slices.Contains(fixedFound, k) {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// fixedFound are the fields of a node found, which no column replaces.
var fixedFound = []string{"id", "title", "title_external", "goal", "status", "maintained", "parent", "changed", "done", "archived", "external", "withheld"}

// findNodes is node_find, for the brain and a block's source alike: it
// never taints; quoted says it gave words that may be someone else's.
func (c *core) findNodes(in nodeFindIn) (found []nodeFound, quoted bool, err error) {
	if c.nodes == nil {
		return nil, false, errNoGraph
	}
	limit := in.Limit
	if limit <= 0 {
		limit = nodeFindMax
	}
	limit = min(limit, nodeFindMost)
	var xs []statusNode
	switch q := strings.TrimSpace(in.Query); {
	case in.filtered():
		query := nodestore.Query{Words: q, Sort: in.Sort, Desc: in.Desc, Archived: in.Archived, Limit: limit}
		for _, t := range in.Tags {
			query.Where = append(query.Where, nodestore.Cond{Key: nodefile.TagsKey, Op: "=", Value: t})
		}
		for _, w := range in.Where {
			query.Where = append(query.Where, nodestore.Cond{Key: w.Key, Op: w.Op, Value: w.Value})
		}
		sel, err := c.nodes.Select(query)
		if err != nil {
			return nil, false, err
		}
		xs = c.withStatus(sel)
	case q != "":
		sel, err := c.nodes.Find(q, in.Archived, limit)
		if err != nil {
			return nil, false, err
		}
		xs = c.withStatus(sel)
	default:
		xs = c.activeNodes(limit)
		more, err := c.nodes.Recent(in.Archived, limit)
		if err != nil {
			return nil, false, err
		}
		for _, x := range c.withStatus(more) {
			if len(xs) < limit && !slices.ContainsFunc(xs, func(y statusNode) bool { return y.ID == x.ID }) {
				xs = append(xs, x)
			}
		}
	}
	var cols map[string]map[string]any
	if len(in.Columns) > 0 {
		ids := make([]string, 0, len(xs))
		for _, x := range xs {
			ids = append(ids, x.ID)
		}
		if cols, err = c.nodes.Props(ids, in.Columns); err != nil {
			return nil, false, err
		}
	}
	found = make([]nodeFound, 0, len(xs))
	homes := c.homeSkills()
	for _, x := range xs {
		f := c.foundNode(x, in.IncludeExternal, homes)
		quoted = quoted || f.TitleExternal && f.Title != "" || x.External && f.Goal != ""
		if len(cols[x.ID]) > 0 {
			if x.External && !in.IncludeExternal {
				f.Withheld = true // its properties may be someone else's words
			} else {
				f.Props = asked(in.Columns, cols[x.ID])
				quoted = quoted || x.External
			}
		}
		found = append(found, f)
	}
	return found, quoted, nil
}

// asked are a node's columns under the names asked (the index keeps
// them lowercase): a Table's column reads the key it was written with.
func asked(columns []string, vals map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range columns {
		if v, ok := vals[strings.ToLower(strings.TrimSpace(k))]; ok {
			out[k] = v
		}
	}
	return out
}

// brainNodeFind is node_find, as the brain calls it: what it quotes that
// may be someone else's words taints the turn.
func (c *core) brainNodeFind(in nodeFindIn) (map[string]any, error) {
	return c.nodeFindFor(in, c.turn.taint, "A title written after reading external content (title_external), and an external node's properties (withheld), are quoted only with include_external, which taints the turn; node_read reads them too. Show them to the user as they are: the workspace marks them.")
}

// nodeFindFor is node_find, for the brain and a task alike: what it
// quotes that may be someone else's words calls taint (nodeFindTaint);
// note goes with the nodes found.
func (c *core) nodeFindFor(in nodeFindIn, taint func(string), note string) (map[string]any, error) {
	found, quoted, err := c.findNodes(in)
	if err != nil {
		return nil, err
	}
	if quoted {
		taint(nodeFindTaint) // someone else's words, quoted
	}
	return map[string]any{"nodes": found, "note": note}, nil
}
