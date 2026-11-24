package nodefile

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// A block's component may name one of the block's sources instead of a
// binding (the catalog's "x-source": Sections' and Gallery's items,
// Table's rows): {"component": "Sections", "items": "mails"}. Code reads
// that source's rows there: the items its items path says, or the nodes
// node_find found. The file keeps what its author wrote; Bound is the
// block as code and the page read it, the name turned into its binding.

// NodeFindTool is node_find as a block's source: its value is
// {"nodes": [...]}.
const NodeFindTool = "eriphe-core/node_find"

// RowsPath is where a source's rows are in its block's data, as a JSON
// pointer: /<name>/<its items' path> for a source that lists items,
// /<name>/nodes for node_find's; "" for a source that lists none, or
// whose items' path is not plain fields (a filter, *).
func RowsPath(name string, s Source) string {
	if s.Items == nil {
		if s.Tool == NodeFindTool {
			return "/" + escapeToken(name) + "/nodes"
		}
		return ""
	}
	p := "/" + escapeToken(name)
	path := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s.Items.Path), "$"), ".")
	if path == "" {
		return p
	}
	for _, seg := range strings.Split(path, ".") {
		if seg == "" || seg == "*" || strings.ContainsAny(seg, "[]") {
			return ""
		}
		p += "/" + escapeToken(seg)
	}
	return p
}

func escapeToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// sourceNamed is a component's source-naming property, when it names a
// source as text: the property and the name.
func sourceNamed(comp map[string]any) (string, string, bool) {
	kind, _ := comp["component"].(string)
	prop := a2ui.SourceProp(kind)
	if prop == "" {
		return "", "", false
	}
	name, ok := comp[prop].(string)
	return prop, name, ok
}

// Bound is the block's components as code and the page read them: each
// source a component names turned into the binding to its rows
// ({"@path": RowsPath}). Unchanged when none names one.
func (b *Block) Bound() json.RawMessage {
	var comps []map[string]any
	if json.Unmarshal(b.Components, &comps) != nil {
		return b.Components
	}
	changed := false
	for _, comp := range comps {
		prop, name, ok := sourceNamed(comp)
		if !ok {
			continue
		}
		if p := RowsPath(name, b.Sources[name]); p != "" {
			comp[prop] = map[string]any{"@path": p}
			changed = true
		}
	}
	if !changed {
		return b.Components
	}
	out, err := marshal(comps)
	if err != nil {
		return b.Components
	}
	return out
}

// namedSources checks the sources a block's components name: each one of
// the block's, listing rows (a Sections' lists items, as its items say).
func (b *Block) namedSources() []string {
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	var out []string
	for i, comp := range comps {
		prop, name, ok := sourceNamed(comp)
		if !ok {
			continue
		}
		at := fmt.Sprintf("/components/%d/%s", i, prop)
		src, has := b.Sources[name]
		switch {
		case !has:
			out = append(out, fmt.Sprintf("%s: no source %q in this block: name one of its sources, or bind a list", at, name))
		case comp["component"] == "Sections" && src.Items == nil:
			out = append(out, fmt.Sprintf("%s: the source %s lists no items: Sections shows a source's items, as its items say (path, id, title)", at, name))
		case RowsPath(name, src) == "":
			out = append(out, fmt.Sprintf("%s: the source %s lists no rows: its items' path is fields joined by dots, or it is node_find", at, name))
		}
	}
	return out
}
