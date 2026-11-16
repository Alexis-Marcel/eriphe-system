package main

import "strconv"

// A thread's default composition (#611): what its surface shows under
// code's frame while its holder has composed no block. The same for every
// thread, drawn from the thread's data only, by the paths a holder's block
// binds alike: its steps (/thread/steps) when it has some, then each
// source's items (/thread/items/<source>), in the order the thread added
// them. It goes away as soon as the holder writes a block, and comes back
// once the last block is removed. Its component ids are code's (thread_).

// threadDefault adds the default composition to a surface's components,
// from its data: steps is /thread/steps, items /thread/items by source,
// order the sources' names as the thread added them.
func threadDefault(out *threadComps, steps map[string]any, items map[string]any, order []string, lang string) {
	defaultSteps(out, steps)
	for i, name := range order {
		v, _ := items[name].(map[string]any)
		defaultSource(out, v, "/thread/items/"+name, "thread_items_"+strconv.Itoa(i), lang)
	}
}

// defaultSteps draws a thread's steps: those done folded in one line
// (summary), opened to see them as a Timeline; the others as a Timeline;
// the cards decided on them, each one line (compact).
func defaultSteps(out *threadComps, steps map[string]any) {
	type comp = map[string]any
	bound := func(k string) map[string]any { return map[string]any{"@path": "/thread/steps/" + k} }
	if has(steps["done"]) {
		out.inner(comp{"id": "thread_steps_done", "component": "Timeline", "items": bound("done")})
		out.add(comp{"id": "thread_steps_fold", "component": "Fold", "label": bound("summary"), "child": "thread_steps_done"})
	}
	if has(steps["open"]) {
		out.add(comp{"id": "thread_steps_open", "component": "Timeline", "items": bound("open")})
	}
	if has(steps["cards"]) {
		out.inner(comp{"id": "thread_steps_card", "component": "Proposal", "proposalId": map[string]any{"@path": "ref"}, "compact": true})
		out.add(comp{"id": "thread_steps_cards", "component": "List", "children": map[string]any{"path": "/thread/steps/cards", "componentId": "thread_steps_card"}})
	}
}

// defaultSource draws one source's data (at): the word on how it reads;
// a monitor's number, its last ones and its events; the items of one that
// lists things, grouped by their notes' state, each group a Fold counted
// on its line with the actions its rows share aside, each row an Item with
// its note, its mark, the cards decided on it and the actions it offers
// (Offers: a click makes a card, never more, offerOf).
func defaultSource(out *threadComps, v map[string]any, at, id, lang string) {
	type comp = map[string]any
	bound := func(k string) map[string]any { return map[string]any{"@path": at + "/" + k} }
	field := func(k string) map[string]any { return map[string]any{"@path": k} }
	if caption, _ := v["caption"].(string); caption != "" {
		out.add(comp{"id": id + "_caption", "component": "Text", "variant": "caption", "text": bound("caption")})
	}
	if v["value"] != nil {
		name, _ := v["name"].(string)
		out.add(comp{"id": id + "_value", "component": "Metric", "label": name, "value": bound("value")})
	}
	if has(v["series"]) {
		out.add(comp{"id": id + "_chart", "component": "Chart", "kind": "line", "series": bound("series")})
	}
	if has(v["events"]) {
		out.add(comp{"id": id + "_events", "component": "Timeline", "items": bound("events")})
	}
	if _, listed := v["groups"].([]any); !listed {
		return
	}
	out.add(comp{"id": id + "_cards", "component": "List", "children": map[string]any{"path": at + "/cards", "componentId": id + "_card"}})
	out.add(comp{"id": id + "_groups", "component": "List", "children": map[string]any{"path": at + "/groups", "componentId": id + "_group"}, "emptyText": tr(lang, "thread.item.none")})
	out.inner(
		comp{"id": id + "_group", "component": "Fold", "label": field("label"), "open": field("open"), "child": id + "_rows", "aside": id + "_group_offers"},
		comp{"id": id + "_group_offers", "component": "Offers", "offers": field("offers")},
		comp{"id": id + "_rows", "component": "List", "children": map[string]any{"path": "items", "componentId": id + "_row"}},
		comp{"id": id + "_row", "component": "Column", "children": []string{id + "_item", id + "_row_cards", id + "_row_offers"}},
		comp{"id": id + "_item", "component": "Item", "title": field("title"), "detail": field("detail"),
			"note": field("note"), "due": field("due"), "mark": field("mark"), "level": field("level")},
		comp{"id": id + "_row_cards", "component": "List", "children": map[string]any{"path": "cards", "componentId": id + "_card"}},
		comp{"id": id + "_row_offers", "component": "Offers", "offers": field("offers")},
		// One template for the cards decided on a row and those on several
		// of its items.
		comp{"id": id + "_card", "component": "Proposal", "proposalId": field("ref"), "compact": true},
	)
}

// has tells whether a value of the data is a list with something in it.
func has(v any) bool {
	list, _ := v.([]any)
	return len(list) > 0
}
