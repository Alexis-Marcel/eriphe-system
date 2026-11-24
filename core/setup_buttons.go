package main

import "slices"

// Setup buttons: an ask Button that sets something up says what (its
// until: a tool to link, a monitor to turn on, a skill to approve), and
// code hides it once that holds. Its author need not remember to take it
// away: the page says what is true now, read from code's own records.

// setUp tells whether what a Button's until names holds now: a tool's
// server connected (one Eriphe provides, offering something), a monitor
// that runs, a skill approved and on. An until that does not read holds
// nothing: the button stays.
func (c *core) setUp(until map[string]any) bool {
	if name, _ := until["tool"].(string); name != "" {
		if c.pool == nil {
			return false
		}
		u := c.pool.get(name)
		if u == nil || u.provided != nil && u.provided.offered != nil && !u.provided.offered() {
			return false
		}
		status, _, _ := u.snapshot()
		return status == statusReady
	}
	if name, _ := until["monitor"].(string); name != "" {
		m, err := c.monitorNamed(name)
		return err == nil && m.State == monitorOn
	}
	if name, _ := until["skill"].(string); name != "" {
		sk, err := c.store.skill(name)
		return err == nil && sk.Active()
	}
	return false
}

// setUpButtons are the ids of a block's Buttons whose until holds.
func (c *core) setUpButtons(comps []map[string]any) []string {
	var out []string
	for _, comp := range comps {
		until, _ := comp["until"].(map[string]any)
		if id, _ := comp["id"].(string); comp["component"] == "Button" && until != nil && c.setUp(until) {
			out = append(out, id)
		}
	}
	return out
}

// withoutComponents is a block's components without those named, nor
// what only held them: a parent whose one child (child) goes goes too, a
// Row or a Column left with no children goes, a tab whose child goes
// goes. The root, if it goes, becomes an empty Column: the block shows
// nothing.
func withoutComponents(comps []map[string]any, gone []string) []map[string]any {
	gone = slices.Clone(gone)
	for changed := true; changed; {
		changed = false
		for _, comp := range comps {
			id, _ := comp["id"].(string)
			if slices.Contains(gone, id) {
				continue
			}
			if child, _ := comp["child"].(string); child != "" && slices.Contains(gone, child) {
				gone, changed = append(gone, id), true
				continue
			}
			if aside, _ := comp["aside"].(string); aside != "" && slices.Contains(gone, aside) {
				delete(comp, "aside")
			}
			if kids, ok := comp["children"].([]any); ok && len(kids) > 0 {
				kept := slices.DeleteFunc(slices.Clone(kids), func(k any) bool { s, _ := k.(string); return slices.Contains(gone, s) })
				if len(kept) < len(kids) {
					comp["children"] = kept
					if len(kept) == 0 && (comp["component"] == "Row" || comp["component"] == "Column") {
						gone, changed = append(gone, id), true
					}
				}
			}
			if tabs, ok := comp["tabs"].([]any); ok {
				kept := slices.DeleteFunc(slices.Clone(tabs), func(t any) bool {
					m, _ := t.(map[string]any)
					s, _ := m["child"].(string)
					return slices.Contains(gone, s)
				})
				if len(kept) < len(tabs) {
					comp["tabs"] = kept
					if len(kept) == 0 {
						gone, changed = append(gone, id), true
					}
				}
			}
		}
	}
	out := make([]map[string]any, 0, len(comps))
	for _, comp := range comps {
		id, _ := comp["id"].(string)
		switch {
		case id == "root" && slices.Contains(gone, id):
			out = append(out, map[string]any{"id": "root", "component": "Column", "children": []any{}})
		case !slices.Contains(gone, id):
			out = append(out, comp)
		}
	}
	return out
}
