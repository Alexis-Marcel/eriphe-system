package a2ui

import (
	"encoding/json"
	"strconv"
)

// Bound is a place of the data model a surface's components read: a
// binding ({"@path": …}) or the list a template repeats over.
type Bound struct {
	// Path is absolute, a JSON pointer in which "*" stands for each item
	// of a list a template repeats over: "/mails/*/subject".
	Path string
	// List: the path is a template's list, read as an array.
	List bool
	// Component is the id of the component that reads it, Type its type
	// in the catalog and Prop the property ("value", "url").
	Component, Type, Prop string
	// At is where it is written, a JSON pointer into the components list
	// ("/2/value/@path").
	At string
	// Media: the value is the address of an image or a video the page
	// would load (Image's url).
	Media bool
}

// mediaProps are the properties whose value is an address the page loads,
// by component type.
var mediaProps = map[string]string{"Image": "url"}

// IsMedia tells whether a component's property is an address the page
// loads (an image's): core serves those itself, never the page directly.
func IsMedia(componentType, prop string) bool {
	p, ok := mediaProps[componentType]
	return ok && p == prop
}

// BoundPaths lists what a valid list of components reads from its data
// model, each path made absolute through the templates it is rendered in.
// A component that no path from the root reaches reads as from the root.
// Components that are not a list of objects give nothing.
func (c *Catalog) BoundPaths(components json.RawMessage) []Bound {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return nil
	}
	comps := map[string]*placed{}
	var ids []string
	for i, v := range list {
		id, _ := v["id"].(string)
		if id == "" || comps[id] != nil {
			continue
		}
		comps[id] = &placed{value: v, loc: []string{strconv.Itoa(i)}}
		ids = append(ids, id)
	}
	edges := map[string][]edge{}
	for _, id := range ids {
		p := comps[id]
		name, _ := p.value["component"].(string)
		for _, e := range c.edges(id, name, p.value, p.loc) {
			if comps[e.to] != nil {
				edges[id] = append(edges[id], e)
			}
		}
	}
	scopes := c.scopes(edges)
	var out []Bound
	for _, id := range ids {
		p := comps[id]
		name, _ := p.value["component"].(string)
		in := scopes[id]
		if len(in) == 0 {
			in = []string{""}
		}
		for _, scope := range in {
			for _, b := range bindingsIn(p.value, p.loc) {
				if b.call != "" || b.path == "" && scope == "" {
					continue
				}
				prop := ""
				if len(b.loc) > 1 {
					prop = b.loc[1]
				}
				out = append(out, Bound{Path: resolvePath(b.path, scope), Component: id, Type: name, Prop: prop,
					At: pointer(b.loc), Media: IsMedia(name, prop) && len(b.loc) == 3})
			}
			for _, e := range edges[id] {
				if e.tmpl {
					out = append(out, Bound{Path: resolvePath(e.template, scope), List: true, Component: id, Type: name,
						Prop: e.loc[1], At: pointer(templatePathLoc(e.loc))})
				}
			}
		}
	}
	return out
}
