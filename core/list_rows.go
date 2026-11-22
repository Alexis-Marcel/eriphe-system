package main

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The rows of a list (docs/node-format.md, « Lists of items »): a List
// bound as a whole to a source's items is told to the page with its rows,
// each with the card that waits on it (item_cards.go: code's, never the
// block's data). The page draws that card on its row, one Oui and ✕: the
// only way a row asks the user's yes. Nothing here knows a server or a
// tool, and nothing here runs one.

// listItem is a row of a list: its place in the source's list, its id,
// what it shows, and the card that waits on it (item_cards.go: code's,
// never the block's data), its Oui on its row.
type listItem struct {
	N      int    `json:"n"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
	Card   string `json:"card,omitempty"`
}

// pageList is a List of a block bound to a source's items: the List's
// component id, its source and its rows. Filtered: the List filters its
// source's items (list_sections.go), and shows only the rows given, never
// the others.
type pageList struct {
	Component string     `json:"component"`
	Source    string     `json:"source"`
	Filtered  bool       `json:"filtered,omitempty"`
	Open      bool       `json:"open,omitempty"` // its rows open in place (item_open.go)
	Items     []listItem `json:"items"`
}

// listedItem is an item a source lists: its id as the source gives it
// (a number stays a number for a tool), and the item itself.
type listedItem struct {
	ID   any
	Item any
}

// itemIDs are the ids of a source's items, those the filters keep (none: all), each with its value as the source gives
// it and its item, in the source's order (sourceItems' reading: an id
// given twice is its first item's).
func itemIDs(src nodefile.Source, value any, f itemFilters) (map[string]listedItem, []string) {
	if src.Items == nil || value == nil {
		return nil, nil
	}
	list, err := extract(value, src.Items.Path)
	items, ok := list.([]any)
	if err != nil || !ok {
		return nil, nil
	}
	ids := map[string]listedItem{}
	seen := map[string]bool{}
	var order []string
	for _, it := range items {
		v, err := extract(it, src.Items.ID)
		if err != nil || v == nil {
			continue
		}
		id := truncate(valueText(v), 200)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !f.keeps(it) {
			continue
		}
		ids[id] = listedItem{ID: v, Item: it}
		order = append(order, id)
	}
	return ids, order
}

// listPath is where a source's items are in a block's data, as a List's
// template names it: /<source>/<path>; "" when its path is no plain
// fields (a filter, *): no List is bound to it as a whole.
func listPath(name string, src nodefile.Source) string {
	if src.Items == nil {
		return ""
	}
	steps, err := parsePath(src.Items.Path)
	if err != nil {
		return ""
	}
	p := "/" + escapeToken(name)
	for _, st := range steps {
		if st.all || st.field != "" || st.key == "" {
			return ""
		}
		p += "/" + escapeToken(st.key)
	}
	return p
}

func escapeToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// sourceRows are a source's rows as a List shows them, those its filters
// keep (none: all), each with its place in the source's list and the card
// that waits on it (_card, as noteItems wrote it).
func sourceRows(name string, src nodefile.Source, value any, f itemFilters) []listItem {
	out := []listItem{}
	var raw []any
	if src.Items != nil && value != nil {
		list, _ := extract(value, src.Items.Path)
		raw, _ = list.([]any)
	}
	for _, it := range sourceItems(name, src, value, 0) {
		r := listItem{}
		r.N, _ = it["n"].(int)
		if r.N >= len(raw) || !f.keeps(raw[r.N]) {
			continue
		}
		r.ID, _ = it["id"].(string)
		if t, _ := it["title"].(string); t != r.ID {
			r.Title = t
		}
		r.Detail, _ = it["detail"].(string)
		if o, ok := raw[r.N].(map[string]any); ok {
			r.Card, _ = o[itemCardField].(string)
		}
		out = append(out, r)
	}
	return out
}

// foldedLists are the Lists a block's Folds name (Fold's list: a batch,
// its line says how many rows it has).
func foldedLists(comps []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, comp := range comps {
		if l, _ := comp["list"].(string); comp["component"] == "Fold" && l != "" {
			out[l] = true
		}
	}
	return out
}

// blockLists are the Lists of a block bound as a whole to a source's
// items that the page must know: those that filter them (it shows only
// their rows), those that open their rows, those a Fold names (a batch),
// and those a card waits on one of whose rows, each with its rows (from
// data, the block's data as the page gets it, the items' notes and cards
// on them). With only, those of these sources alone.
func (c *core) blockLists(b *nodefile.Block, data map[string]any, only ...string) []pageList {
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	folded := foldedLists(comps)
	var out []pageList
	for _, comp := range comps {
		if comp["component"] != "List" {
			continue
		}
		id, _ := comp["id"].(string)
		tpl, _ := comp["children"].(map[string]any)
		path, _ := tpl["path"].(string)
		if id == "" || path == "" {
			continue
		}
		name, f, err := listBinding(path, b.Sources)
		if err != nil && strings.Contains(path, "[") && len(only) == 0 {
			// A filter that does not read (a file written by hand): the
			// page shows none of the source's items, never all of them.
			out = append(out, pageList{Component: id, Filtered: true, Items: []listItem{}})
			continue
		}
		if err != nil || len(only) > 0 && !slices.Contains(only, name) {
			continue
		}
		src := b.Sources[name]
		rows := sourceRows(name, src, data[name], f)
		open := src.Items != nil && src.Items.Open != nil
		carded := slices.ContainsFunc(rows, func(r listItem) bool { return r.Card != "" })
		if f != nil || open || folded[id] || carded {
			out = append(out, pageList{Component: id, Source: name, Filtered: f != nil, Open: open, Items: rows})
		}
	}
	return out
}

// sourceNow is a block's source's value now: its last read, nil if none.
func (c *core) sourceNow(id string, b *nodefile.Block, name string) any {
	for _, r := range c.nodeReads(id) {
		if r.Block == b.Name && r.Name == name && r.Value != nil {
			return r.Value
		}
	}
	return nil
}
