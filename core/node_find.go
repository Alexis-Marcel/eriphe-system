package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// node_find (#743): words, and filters on a node's tags and properties,
// a sort, the properties to give as columns, a limit. The brain calls it,
// and a block names it as its source ({"tool": "eriphe-core/node_find",
// "args": {…}}): code reads it like any source, so a page « Mes études en
// attente » stays up to date by itself, read again at once when a node
// changes.
//
// Trust is the same for both: a title written after an external read,
// and the properties and tags of a node that may hold someone else's
// words (an external one), are given only with include_external. The
// brain's call then taints its turn; a block whose source says it is
// external, and reading it taints (untrustedSources).

const (
	// nodeFindMax is how many nodes node_find gives by default; nodeFindMost
	// the most it gives.
	nodeFindMax  = 30
	nodeFindMost = 200
	// coreServer is eriphe-core's own name as a source's server: no
	// catalog server may take it (reserved).
	coreServer = "eriphe-core"
	// nodeFindSource is node_find as a block's source.
	nodeFindSource = coreServer + "/node_find"
	// nodeFindTaint is what node_find makes the turn when it quotes words
	// that may be someone else's.
	nodeFindTaint = "node_find.external"
)

type nodeFindIn struct {
	Query string `json:"query,omitempty" jsonschema:"words to look for; with neither words nor filters, the nodes at work or waiting, then the last changed"`
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
	IncludeExternal bool `json:"include_external,omitempty" jsonschema:"quote what may be someone else's words too (a title written after an external read, an external node's properties): this taints the turn; a block that reads it is external"`
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
var fixedFound = []string{"id", "title", "title_external", "status", "lifetime", "parent", "changed", "done", "archived", "external", "withheld"}

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
	for _, x := range xs {
		f := c.foundNode(x, in.IncludeExternal)
		quoted = quoted || f.TitleExternal && f.Title != ""
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
	found, quoted, err := c.findNodes(in)
	if err != nil {
		return nil, err
	}
	if quoted {
		c.turn.taint(nodeFindTaint) // someone else's words, quoted
	}
	return map[string]any{"nodes": found, "note": "A title written after reading external content (title_external), and an external node's properties (withheld), are quoted only with include_external, which taints the turn; node_read reads them too. Show them to the user as they are: the workspace marks them."}, nil
}

// nodeFindArgs reads a source's arguments as node_find's.
func nodeFindArgs(args map[string]any) (nodeFindIn, error) {
	var in nodeFindIn
	raw, _ := json.Marshal(args)
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("%s: its args are node_find's: %w", nodeFindSource, err)
	}
	return in, nil
}

// readNodeFind is a block's node_find source read: {"nodes": [...]}.
func (c *core) readNodeFind(args map[string]any) sourceRead {
	c.findSources.Store(true) // a node's change reads them again (nodesChanged)
	in, err := nodeFindArgs(args)
	if err != nil {
		return sourceRead{err: err.Error()}
	}
	found, _, err := c.findNodes(in)
	if err != nil {
		return sourceRead{err: truncate(err.Error(), 300)}
	}
	// As JSON, as any source's value is kept.
	raw, err := json.Marshal(map[string]any{"nodes": found})
	if err != nil {
		return sourceRead{err: err.Error()}
	}
	var v any
	json.Unmarshal(raw, &v)
	return sourceRead{value: v}
}

// nodeFindMedia makes each node found's media:<name> columns (a photo
// kept with node_set) the address of that node's own file on eriphe-core,
// as a Table of the page loads it: a thumbnail of each node. A node whose
// columns are withheld gives none.
func (c *core) nodeFindMedia(value any) {
	v, _ := value.(map[string]any)
	rows, _ := v["nodes"].([]any)
	for _, x := range rows {
		row, _ := x.(map[string]any)
		id, _ := row["id"].(string)
		if id == "" {
			continue
		}
		for k, col := range row {
			if slices.Contains(fixedFound, k) {
				continue
			}
			row[k] = swapMedia(col, func(s string) string {
				if src := c.nodeFileSrc(id, s); src != "" {
					return src
				}
				return s
			})
		}
	}
}

// swapMedia is a value, or each value of a list, through fn when a text.
func swapMedia(v any, fn func(string) string) any {
	switch x := v.(type) {
	case string:
		return fn(x)
	case []any:
		for i := range x {
			if s, ok := x[i].(string); ok {
				x[i] = fn(s)
			}
		}
	}
	return v
}

// nodeFindExternal tells a node_find source that quotes what may be
// someone else's words: its block is external.
func nodeFindExternal(src nodefile.Source) bool {
	if src.Tool != nodeFindSource {
		return false
	}
	in, err := nodeFindArgs(src.Args)
	return err == nil && in.IncludeExternal
}

// checkNodeFindSource refuses a node_find source whose arguments do not
// read.
func checkNodeFindSource(src nodefile.Source) error {
	if src.Tool != nodeFindSource {
		return nil
	}
	if _, err := nodeFindArgs(src.Args); err != nil {
		return err
	}
	return nil
}

// nodesChanged is a node that changed: the blocks that read node_find are
// read again soon (source_fresh.go), once a block reads it at all. It
// never reads the store: the store calls it with its lock held.
func (c *core) nodesChanged() {
	if c.findSources.Load() {
		c.readServerSoon(coreServer, "")
	}
}

// nodeFindOffer is node_find as sources_read lists it; full: its
// arguments and its result's shape.
func nodeFindOffer(full bool) readOffer {
	o := readOffer{Tool: nodeFindSource, Free: true, Shape: !full,
		Description: "The user's nodes, as node_find finds them: by words, tags and properties, sorted, with properties as columns. Its value: {nodes: [{id, title, status, changed, <columns>…}]}, read again at once when a node changes."}
	if full {
		if s, err := jsonschema.For[nodeFindIn](nil); err == nil {
			o.Args = shownSchema(s)
		}
		o.Output = json.RawMessage(`{"type": "object", "properties": {"nodes": {"type": "array", "items": {"type": "object", "properties": {"id": {"type": "string"}, "title": {"type": "string"}, "status": {"type": "string"}, "changed": {"type": "string"}}, "additionalProperties": true}}}}`)
	}
	return o
}
