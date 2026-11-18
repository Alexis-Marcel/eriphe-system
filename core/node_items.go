package main

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A node's items (#631): what a block's source lists (mails, events,
// pull requests), read by code from the source's last good value: each
// item by its id (the source's items.id), with what its row says (items'
// title and detail, else the item's common fields). An author notes an
// item (item_note, nodestore's node_items); a call kept on one is that
// item's card (item_cards.go).

const itemTextMax = 160 // runes of an item's title or detail

// sourceItems are the items of what a source read: each with its id, in
// the order the source gives them, as many as most (0: all of them).
func sourceItems(name string, src nodefile.Source, value any, most int) []map[string]any {
	if src.Items == nil || value == nil {
		return nil
	}
	list, err := extract(value, src.Items.Path)
	items, ok := list.([]any)
	if err != nil || !ok {
		return nil
	}
	var out []map[string]any
	seen := map[string]bool{}
	for i, it := range items {
		v, err := extract(it, src.Items.ID)
		if err != nil || v == nil {
			continue
		}
		id := truncate(valueText(v), 200)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		row := map[string]any{"source": name, "id": id, "n": i}
		title, titleKey := itemField(it, src.Items.Title, itemTitleFields, false)
		detail, detailKey := itemField(it, src.Items.Detail, itemDetailFields, true)
		if title == "" {
			title, titleKey = itemAnyText(it, id, src.Items.ID, detailKey)
		}
		switch {
		case title != "":
			row["title"] = title
		case detail != "":
			row["title"], detail = detail, "" // its only words
		default:
			row["title"] = id // nothing else says what it is
		}
		if detail != "" && detailKey != titleKey {
			row["detail"] = detail
		}
		out = append(out, row)
		if len(out) == most {
			break
		}
	}
	return out
}

// What an item's row says when its source names no path for it: the first
// of these fields it has, at its top level, whatever the source (a mail's
// subject and sender, an event's title and organizer, an issue's title and
// author). Never its id while some words say what it is.
var (
	itemTitleFields  = []string{"subject", "title", "name", "summary", "label", "snippet", "text", "description"}
	itemDetailFields = []string{"from", "sender", "author", "organizer", "owner", "creator", "user", "assignee"}
)

// itemField is the text at path in an item, else at the first of fields
// it has; and the field it came from ("" for a path given).
func itemField(it any, path string, fields []string, person bool) (string, string) {
	if path != "" {
		v, err := extract(it, path)
		if err != nil {
			return "", ""
		}
		return itemText(v, person), path
	}
	m, ok := it.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range fields {
		if text := itemText(m[k], person); text != "" {
			return text, k
		}
	}
	return "", ""
}

// itemAnyText is, for an item none of the common fields names, the first
// short text it has at its top level that is not its id: a row says words
// before an id.
func itemAnyText(it any, id, idPath, skip string) (string, string) {
	m, ok := it.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		lk := strings.ToLower(k)
		if k == idPath || k == skip || lk == "id" || strings.HasSuffix(lk, "_id") || strings.HasSuffix(k, "Id") || strings.HasSuffix(lk, "uid") {
			continue
		}
		s, ok := m[k].(string)
		if !ok || s == id || strings.Contains(s, "://") {
			continue
		}
		if text := itemText(s, false); text != "" && utf8.RuneCountInString(s) <= itemTextMax {
			return text, k
		}
	}
	return "", ""
}

// itemText is a value as a row says it, one line: an object by its name;
// for a person (a detail), "Sam <sam@example.org>" by their name too.
func itemText(v any, person bool) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.TrimSpace(x)
		if name, _, ok := strings.Cut(s, " <"); ok && person && strings.HasSuffix(s, ">") {
			if name = strings.Trim(strings.TrimSpace(name), `"`); name != "" {
				s = name
			}
		}
		return truncate(oneLine(s), itemTextMax)
	case map[string]any:
		for _, k := range []string{"name", "displayName", "display_name", "login", "email", "title"} {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return itemText(s, person)
			}
		}
		return ""
	case []any:
		var parts []string
		for _, e := range x {
			if s := itemText(e, person); s != "" {
				parts = append(parts, s)
			}
		}
		return truncate(strings.Join(parts, ", "), itemTextMax)
	}
	return truncate(oneLine(valueText(v)), itemTextMax)
}
