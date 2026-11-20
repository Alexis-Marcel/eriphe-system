package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A view in sections (docs/node-format.md, « Sections »): the items of a
// source carry their notes, and a List shows those whose field has a
// value. Nothing here knows a server, a tool or a group's name: the
// author (Eriphe, a skill) chooses the groups when it notes the items
// (item_note's group), and writes one List per section, its template's
// path the source's items with a filter:
//
//	/mails/messages[_group=À supprimer]
//	/mails/messages[_group!=À supprimer]
//
// Code evaluates the filter, never the page: the page gets the List bound
// to the source's items as a whole, and the rows it shows (pageList,
// filtered), in the source's order. The click on an action of such a list
// is checked against the filter as it stands (web_list_actions.go).

// The fields code adds to each item of a source that lists items: its
// note, its state, its group (empty when it has none), and, when the note
// may carry someone else's words, _external.
const (
	itemNoteField     = "_note"
	itemStateField    = "_state"
	itemGroupField    = "_group"
	itemExternalField = "_external"
)

// itemFilter is a List's filter on its source's items: the items whose
// field has the value, or, Not, those whose field has another.
type itemFilter struct {
	Field, Value string
	Not          bool
}

// keeps tells whether an item passes the filter; nil keeps all. A field
// the item has not reads as empty.
func (f *itemFilter) keeps(it any) bool {
	if f == nil {
		return true
	}
	o, _ := it.(map[string]any)
	got := ""
	if v, ok := o[f.Field]; ok && v != nil {
		got = valueText(v)
	}
	return (got == f.Value) != f.Not
}

var errNotItems = errors.New("not the items of a source")

// listBinding is what a List's template path binds among a block's
// sources: the source whose items it shows as a whole (listPath), and
// the filter after them, if any. errNotItems when the path is not a
// source's items; another error for a filter that does not read.
func listBinding(path string, sources map[string]nodefile.Source) (string, *itemFilter, error) {
	base, filter, _ := strings.Cut(path, "[")
	name := ""
	for _, n := range slices.Sorted(maps.Keys(sources)) {
		if p := listPath(n, sources[n]); p != "" && p == base {
			name = n
			break
		}
	}
	if name == "" {
		return "", nil, errNotItems
	}
	if !strings.Contains(path, "[") {
		return name, nil, nil
	}
	steps, err := parsePath("[" + filter)
	if err != nil || len(steps) != 1 || steps[0].field == "" || !strings.HasSuffix(filter, "]") || strings.Count(filter, "[") > 0 {
		return "", nil, fmt.Errorf("%s: a filter is one [field=value] or [field!=value], after the source's items (%s[_group=À supprimer])", path, base)
	}
	st := steps[0]
	return name, &itemFilter{Field: st.field, Value: st.equals, Not: st.not}, nil
}

// noteItems adds to each item of a source's value its note's fields
// (_note, _state, _group, and _external when the note may carry someone
// else's words), the items' notes given by id; every item gets the
// first three, empty when it has none, so that a filter reads them.
// value is the block's own copy (read for the page, never kept): it is
// changed in place. A source whose items' path is not plain fields is
// left as it is.
func noteItems(src nodefile.Source, value any, notes map[string]nodestore.Item) {
	if src.Items == nil || value == nil || listPath("x", src) == "" {
		return
	}
	list, err := extract(value, src.Items.Path)
	items, ok := list.([]any)
	if err != nil || !ok {
		return
	}
	for _, it := range items {
		o, ok := it.(map[string]any)
		if !ok {
			continue
		}
		n := notes[itemID(src, it)]
		o[itemNoteField], o[itemStateField], o[itemGroupField] = n.Note, n.State, n.Group
		delete(o, itemExternalField)
		if n.Tainted != "" {
			o[itemExternalField] = true
		}
	}
}

// itemID is an item's id as the user picks it ("" when it has none).
func itemID(src nodefile.Source, it any) string {
	v, err := extract(it, src.Items.ID)
	if err != nil || v == nil {
		return ""
	}
	return truncate(valueText(v), 200)
}

// itemNotes are a node's notes on the items of a source, by item.
func (c *core) itemNotes(id, source string) map[string]nodestore.Item {
	out := map[string]nodestore.Item{}
	if c.nodes == nil {
		return out
	}
	items, _ := c.nodes.Items(id)
	for _, it := range items {
		if it.Source == source {
			out[it.Item] = it
		}
	}
	return out
}

// noteSources adds their notes to the items of a block's sources in
// data, the block's data as the page gets it.
func (c *core) noteSources(id string, b *nodefile.Block, data map[string]any) {
	for name, src := range b.Sources {
		if src.Items != nil && data[name] != nil {
			noteItems(src, data[name], c.itemNotes(id, name))
		}
	}
}

// unfiltered are a path's tokens with no filter: messages[_group=x] is
// messages, the whole list (a template's items are its next token, *).
func unfiltered(toks []string) []string {
	out := make([]string, len(toks))
	for i, t := range toks {
		out[i], _, _ = strings.Cut(t, "[")
	}
	return out
}

// listTemplate is the template's path of a block's List, by its
// component id; "" when it has no such List.
func listTemplate(b *nodefile.Block, id string) string {
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	for _, comp := range comps {
		if comp["id"] == id && comp["component"] == "List" {
			tpl, _ := comp["children"].(map[string]any)
			path, _ := tpl["path"].(string)
			return path
		}
	}
	return ""
}

// at0 is a path's first token, "" for none.
func at0(toks []string) string {
	if len(toks) == 0 {
		return ""
	}
	return toks[0]
}

// unfilterLists sets, in a block's components as the page gets them, each
// List's filtered path to its source's items as a whole: the page shows
// the rows code filtered (pageList).
func unfilterLists(comps []map[string]any) {
	for _, comp := range comps {
		tpl, _ := comp["children"].(map[string]any)
		if path, _ := tpl["path"].(string); comp["component"] == "List" && strings.Contains(path, "[") {
			base, _, _ := strings.Cut(path, "[")
			tpl["path"] = base
		}
	}
}

// listsOK checks a block's Lists that filter, and its actions' lists:
// each filter reads (listBinding), on a List; an action's list is a List
// of the block bound to the action's source.
func listsOK(b *nodefile.Block) []string {
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	var out []string
	lists := map[string]string{} // List id: the source it shows
	for _, comp := range comps {
		id, _ := comp["id"].(string)
		tpl, _ := comp["children"].(map[string]any)
		path, _ := tpl["path"].(string)
		if path == "" {
			continue
		}
		name, _, err := listBinding(path, b.Sources)
		switch {
		case err == nil && comp["component"] == "List":
			lists[id] = name
		case !strings.Contains(path, "["):
		case comp["component"] != "List":
			out = append(out, fmt.Sprintf("component %s: only a List filters a source's items", id))
		case errors.Is(err, errNotItems):
			out = append(out, fmt.Sprintf("component %s: %s: a filter follows a source's items, as its items' path says them (/<source>/<path>[field=value])", id, path))
		default:
			out = append(out, fmt.Sprintf("component %s: %v", id, err))
		}
	}
	for _, an := range slices.Sorted(maps.Keys(b.Actions)) {
		a := b.Actions[an]
		if a.List != "" && lists[a.List] != a.Source {
			out = append(out, fmt.Sprintf("action %s: list %s is not a List of the source %s's items", an, a.List, a.Source))
		}
	}
	return out
}
