package a2ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// Surface is what an author writes of a surface: its flat list of
// components and, when it is composed once, its data.
type Surface struct {
	ID         string          `json:"surfaceId"`
	Components json.RawMessage `json:"components"`
	DataModel  json.RawMessage `json:"dataModel,omitempty"`
}

// Validate checks agent-to-renderer messages: one message object, or an
// array of them in the order they would be sent. The messages are taken as
// the whole life of their surfaces: each surface that gets components must
// have a root, and every reference must resolve among them.
//
// An error's Path is a JSON pointer into raw ("/createSurface/components/2/text",
// or "/1/updateComponents/…" in an array). No error means valid.
func (c *Catalog) Validate(raw []byte) []ValidationError {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []ValidationError{{Code: CodeValidationFailed, Path: "", Message: "not JSON: " + err.Error()}}
	}
	st := &stream{c: c, surfaces: map[string]*surfaceState{}}
	if list, ok := v.([]any); ok {
		for i, m := range list {
			st.message(m, []string{strconv.Itoa(i)})
		}
	} else {
		st.message(v, nil)
	}
	st.finish()
	return st.errs
}

// ValidateSurface checks a surface written by an author, as the
// createSurface message that would carry it on Eriphe's catalog. An error's
// Path is a JSON pointer into the surface ("/components/0/text").
func (c *Catalog) ValidateSurface(s Surface) []ValidationError {
	body := map[string]any{"surfaceId": s.ID, "catalogId": c.id}
	for key, raw := range map[string]json.RawMessage{"components": s.Components, "dataModel": s.DataModel} {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return []ValidationError{{Code: CodeValidationFailed, SurfaceID: s.ID, Path: "/" + key, Message: "not JSON: " + err.Error()}}
		}
		body[key] = v
	}
	if _, ok := body["components"]; !ok {
		return []ValidationError{{Code: CodeValidationFailed, SurfaceID: s.ID, Path: "/components", Message: "a surface needs components"}}
	}
	st := &stream{c: c, surfaces: map[string]*surfaceState{}, strip: 1}
	st.message(map[string]any{"version": Version, "createSurface": body}, nil)
	st.finish()
	return st.errs
}

// ValidateEvent checks a renderer-to-agent message (an action, an error)
// against the schema. An error's Path is a JSON pointer into raw.
func (c *Catalog) ValidateEvent(raw []byte) []ValidationError {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []ValidationError{{Code: CodeValidationFailed, Message: "not JSON: " + err.Error()}}
	}
	sid := ""
	if m, ok := v.(map[string]any); ok {
		for _, body := range m {
			if b, ok := body.(map[string]any); ok {
				if s, ok := b["surfaceId"].(string); ok {
					sid = s
				}
			}
		}
	}
	var out []ValidationError
	found := dropUnevaluated(explain(c.event.Validate(v)))
	slices.SortStableFunc(found, func(a, b leaf) int { return compareLoc(a.loc, b.loc) })
	for _, l := range found {
		out = append(out, ValidationError{Code: CodeValidationFailed, SurfaceID: sid, Path: pointer(l.loc), Message: l.msg})
	}
	return out
}

// stream follows the surfaces through a list of messages.
type stream struct {
	c        *Catalog
	surfaces map[string]*surfaceState
	order    []string
	errs     []ValidationError
	strip    int // leading tokens left out of the paths (ValidateSurface)
}

type surfaceState struct {
	created    bool
	broken     bool               // a message was invalid: its tree is not checked
	components map[string]*placed // by id
	last       []string           // where the last components list was
}

// placed is a component and where it was written.
type placed struct {
	value map[string]any
	loc   []string
}

func (st *stream) fail(code, sid string, loc []string, msg string) {
	if len(loc) >= st.strip {
		loc = loc[st.strip:]
	}
	st.errs = append(st.errs, ValidationError{Code: code, SurfaceID: sid, Path: pointer(loc), Message: msg})
}

func (st *stream) surface(sid string) *surfaceState {
	s := st.surfaces[sid]
	if s == nil {
		s = &surfaceState{components: map[string]*placed{}}
		st.surfaces[sid] = s
		st.order = append(st.order, sid)
	}
	return s
}

// message checks one message and records its components.
func (st *stream) message(v any, at []string) {
	m, ok := v.(map[string]any)
	if !ok {
		st.fail(CodeValidationFailed, "", at, "a message is a JSON object")
		return
	}
	var key string
	var keys []string
	for k := range messageKinds {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 1 {
		key = keys[0]
	}
	body, _ := m[key].(map[string]any)
	sid, _ := body["surfaceId"].(string)
	if key == "" {
		st.fail(CodeValidationFailed, "", at, "a message has exactly one of createSurface, updateComponents, updateDataModel, deleteSurface, callRendererFunction, agentFunctionResponse")
		return
	}
	if err := st.c.message.Validate(m); err != nil {
		st.schemaErrors(key, m, body, sid, at)
		if sid != "" {
			st.surface(sid).broken = true
		}
		return
	}
	switch key {
	case "createSurface":
		s := st.surface(sid)
		if s.created {
			st.fail(CodeValidationFailed, sid, append(at, key, "surfaceId"), fmt.Sprintf("surface %q is created twice without deleteSurface", sid))
		}
		s.created = true
		catalog, has := body["catalogId"].(string)
		if has && catalog != st.c.id {
			st.fail(CodeValidationFailed, sid, append(at, key, "catalogId"), fmt.Sprintf("unknown catalog %q: use %q", catalog, st.c.id))
		} else if !has && body["components"] != nil {
			st.fail(CodeValidationFailed, sid, append(at, key), fmt.Sprintf("missing catalogId: use %q", st.c.id))
		}
		if list, ok := body["components"].([]any); ok {
			st.components(sid, list, append(slices.Clone(at), key, "components"))
		}
	case "updateComponents":
		list, _ := body["components"].([]any)
		st.components(sid, list, append(slices.Clone(at), key, "components"))
	case "updateDataModel":
		if p, ok := body["path"].(string); ok && p != "" && p != "/" {
			if msg := checkPointer(p, true); msg != "" {
				st.fail(CodeValidationFailed, sid, append(at, key, "path"), msg)
			}
		}
	case "deleteSurface":
		delete(st.surfaces, sid)
		if i := slices.Index(st.order, sid); i >= 0 {
			st.order = slices.Delete(st.order, i, i+1)
		}
	}
}

// components records a list of components, checking what does not need
// the rest of the tree.
func (st *stream) components(sid string, list []any, at []string) {
	s := st.surface(sid)
	s.last = at
	seen := map[string]bool{}
	for i, v := range list {
		comp, _ := v.(map[string]any)
		loc := append(slices.Clone(at), strconv.Itoa(i))
		id, _ := comp["id"].(string)
		if !isIdentifier(id) {
			st.fail(CodeValidationFailed, sid, append(loc, "id"), fmt.Sprintf("id %q is not an identifier: letters, digits and _, not starting with a digit", id))
		}
		if seen[id] {
			st.fail(CodeValidationFailed, sid, append(loc, "id"), fmt.Sprintf("id %q is used twice in this list", id))
		}
		seen[id] = true
		if cat, ok := comp["@catalogId"].(string); ok && cat != st.c.id {
			st.fail(CodeValidationFailed, sid, append(loc, "@catalogId"), fmt.Sprintf("unknown catalog %q", cat))
		}
		s.components[id] = &placed{value: comp, loc: loc}
	}
}

// schemaErrors explains why a message failed its schema: the message's own
// schema for the envelope, and each component against its own shape, so
// that the errors say what is wrong rather than which of many shapes the
// value is not.
func (st *stream) schemaErrors(key string, m, body map[string]any, sid string, at []string) {
	sch := st.c.message
	if key != "" {
		sch = st.c.kinds[key]
	}
	var found []leaf
	for _, l := range explain(sch.Validate(m)) {
		if len(l.loc) >= 3 && l.loc[1] == "components" {
			continue // a component's own errors come below
		}
		found = append(found, l)
	}
	list, _ := body["components"].([]any)
	for i, v := range list {
		loc := []string{key, "components", strconv.Itoa(i)}
		comp, ok := v.(map[string]any)
		if !ok {
			continue // the envelope's schema said so
		}
		name, ok := comp["component"].(string)
		shape := st.c.shapes[name]
		switch {
		case !ok:
			found = append(found, leaf{loc: loc, msg: `missing property "component"`})
		case shape == nil:
			found = append(found, leaf{loc: append(loc, "component"), msg: fmt.Sprintf("unknown component %q; the catalog has %s", name, strings.Join(st.c.component, ", "))})
		default:
			for _, l := range explain(shape.Validate(comp)) {
				l.loc = append(slices.Clone(loc), l.loc...)
				found = append(found, l)
			}
		}
	}
	found = dropUnevaluated(found)
	slices.SortStableFunc(found, func(a, b leaf) int { return compareLoc(a.loc, b.loc) })
	if len(found) == 0 {
		found = []leaf{{msg: "does not match the A2UI v1.0 schema"}}
	}
	for _, l := range found {
		st.fail(CodeValidationFailed, sid, append(slices.Clone(at), l.loc...), l.msg)
	}
}

// finish checks each surface's tree once every message is in.
func (st *stream) finish() {
	for _, sid := range st.order {
		s := st.surfaces[sid]
		if s == nil || s.broken || len(s.components) == 0 {
			continue
		}
		st.tree(sid, s)
	}
}

// edge is a reference from a component to another.
type edge struct {
	from, to string
	loc      []string // where the reference is written
	template string   // the data path, for a template
	tmpl     bool     // the reference is a list template
}

// tree checks a surface's components as a tree: a root, references that
// resolve, allowed parents and children, no cycle, and relative paths
// only where a list template gives them an item to be read in.
func (st *stream) tree(sid string, s *surfaceState) {
	ids := make([]string, 0, len(s.components))
	for id := range s.components {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int { return compareLoc(s.components[a].loc, s.components[b].loc) })

	edges := map[string][]edge{}
	for _, id := range ids {
		p := s.components[id]
		name, _ := p.value["component"].(string)
		for _, e := range st.c.edges(id, name, p.value, p.loc) {
			if _, ok := s.components[e.to]; !ok {
				st.fail(CodeValidationFailed, sid, e.loc, fmt.Sprintf("no component has the id %q", e.to))
				continue
			}
			edges[id] = append(edges[id], e)
		}
	}
	root := s.components["root"]
	if root == nil {
		st.fail(CodeValidationFailed, sid, s.last, `no component has the id "root"`)
	}

	// Allowed parents and children; the root's parent is the Surface.
	kindOf := func(id string) string { k, _ := s.components[id].value["component"].(string); return k }
	if root != nil {
		if allowed, ok := st.c.parents[kindOf("root")]; ok && !slices.Contains(allowed, "Surface") {
			st.fail(CodeUnallowedParent, sid, append(slices.Clone(root.loc), "component"),
				fmt.Sprintf("%s cannot be the root; its parents may be %s", kindOf("root"), strings.Join(allowed, ", ")))
		}
	}
	for _, id := range ids {
		for _, e := range edges[id] {
			parent, child := kindOf(e.from), kindOf(e.to)
			if allowed, ok := st.c.children[parent]; ok && !slices.Contains(allowed, child) {
				st.fail(CodeUnallowedChild, sid, e.loc, fmt.Sprintf("%s cannot hold a %s; it holds %s", parent, child, strings.Join(allowed, ", ")))
			}
			if allowed, ok := st.c.parents[child]; ok && !slices.Contains(allowed, parent) {
				st.fail(CodeUnallowedParent, sid, e.loc, fmt.Sprintf("a %s cannot be inside a %s; its parents may be %s", child, parent, strings.Join(allowed, ", ")))
			}
		}
	}

	// Cycles.
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	cyclic := false
	var visit func(id string)
	visit = func(id string) {
		color[id] = grey
		stack = append(stack, id)
		for _, e := range edges[id] {
			switch color[e.to] {
			case grey:
				i := slices.Index(stack, e.to)
				cycle := append(slices.Clone(stack[i:]), e.to)
				st.fail(CodeValidationFailed, sid, e.loc, "cycle: "+strings.Join(cycle, " → "))
				cyclic = true
			case white:
				visit(e.to)
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}
	for _, id := range ids {
		if color[id] == white {
			visit(id)
		}
	}

	// Paths: well formed everywhere, relative only inside a template.
	scopes := map[string][]string{}
	if root != nil && !cyclic {
		scopes = st.c.scopes(edges)
	}
	for _, id := range ids {
		p := s.components[id]
		for _, b := range bindingsIn(p.value, p.loc) {
			if b.call == "@index" {
				if slices.Contains(scopes[id], "") {
					st.fail(CodeValidationFailed, sid, b.loc, "@index is only known inside a list template")
				}
				continue
			}
			if b.call != "" {
				continue
			}
			if msg := checkPointer(b.path, false); msg != "" {
				st.fail(CodeValidationFailed, sid, b.loc, msg)
				continue
			}
			if !strings.HasPrefix(b.path, "/") && slices.Contains(scopes[id], "") {
				st.fail(CodeValidationFailed, sid, b.loc, fmt.Sprintf("relative path %q outside a list template: start it with /", b.path))
			}
		}
		for _, e := range edges[id] {
			if !e.tmpl {
				continue
			}
			if msg := checkPointer(e.template, false); msg != "" {
				st.fail(CodeValidationFailed, sid, templatePathLoc(e.loc), msg)
			} else if !strings.HasPrefix(e.template, "/") && slices.Contains(scopes[id], "") {
				st.fail(CodeValidationFailed, sid, templatePathLoc(e.loc), fmt.Sprintf("relative path %q outside a list template: start it with /", e.template))
			}
		}
	}
}

func templatePathLoc(loc []string) []string {
	return append(slices.Clone(loc[:len(loc)-1]), "path")
}

// edges lists where a component names others, through the catalog's links.
func (c *Catalog) edges(id, name string, comp map[string]any, loc []string) []edge {
	var out []edge
	for _, l := range c.links[name] {
		var walk func(v any, rest []string, at []string)
		walk = func(v any, rest []string, at []string) {
			if len(rest) == 0 {
				switch t := v.(type) {
				case string:
					out = append(out, edge{from: id, to: t, loc: at})
				case []any:
					if l.list {
						for i, x := range t {
							if s, ok := x.(string); ok {
								out = append(out, edge{from: id, to: s, loc: append(slices.Clone(at), strconv.Itoa(i))})
							}
						}
					}
				case map[string]any:
					if cid, ok := t["componentId"].(string); ok && l.list {
						path, _ := t["path"].(string)
						out = append(out, edge{from: id, to: cid, loc: append(slices.Clone(at), "componentId"), template: path, tmpl: true})
					}
				}
				return
			}
			switch t := v.(type) {
			case map[string]any:
				if rest[0] != "*" {
					if sub, ok := t[rest[0]]; ok {
						walk(sub, rest[1:], append(slices.Clone(at), rest[0]))
					}
				}
			case []any:
				if rest[0] == "*" {
					for i, sub := range t {
						walk(sub, rest[1:], append(slices.Clone(at), strconv.Itoa(i)))
					}
				}
			}
		}
		walk(comp, l.path, loc)
	}
	return out
}

// maxScopes bounds the data scopes kept for one component.
const maxScopes = 8

// scopes gives, for each component reachable from the root, the data
// scopes it is rendered in: "" for the root of the data, else the list
// its template repeats over ("/mails/*").
func (c *Catalog) scopes(edges map[string][]edge) map[string][]string {
	out := map[string][]string{}
	type visit struct{ id, scope string }
	queue := []visit{{"root", ""}}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if slices.Contains(out[v.id], v.scope) || len(out[v.id]) >= maxScopes {
			continue
		}
		out[v.id] = append(out[v.id], v.scope)
		for _, e := range edges[v.id] {
			scope := v.scope
			if e.tmpl {
				scope = resolvePath(e.template, v.scope) + "/*"
			}
			queue = append(queue, visit{e.to, scope})
		}
	}
	return out
}

// resolvePath reads a path in a scope: absolute as it is, relative under
// the scope.
func resolvePath(path, scope string) string {
	if strings.HasPrefix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	if path == "" {
		return scope
	}
	return scope + "/" + path
}

// boundAt is a binding or a function call found in a component.
type boundAt struct {
	path string // the @path, for a binding
	call string // the @call, for a function call
	loc  []string
}

// bindingsIn finds the bindings and function calls in a component's
// properties, the arguments of calls included.
func bindingsIn(comp map[string]any, loc []string) []boundAt {
	var out []boundAt
	var walk func(v any, at []string)
	walk = func(v any, at []string) {
		switch t := v.(type) {
		case map[string]any:
			if p, ok := t["@path"].(string); ok {
				out = append(out, boundAt{path: p, loc: append(slices.Clone(at), "@path")})
				return
			}
			if call, ok := t["@call"].(string); ok {
				out = append(out, boundAt{call: call, loc: at})
			}
			for k, sub := range t {
				walk(sub, append(slices.Clone(at), k))
			}
		case []any:
			for i, sub := range t {
				walk(sub, append(slices.Clone(at), strconv.Itoa(i)))
			}
		}
	}
	for k, v := range comp {
		if k != "id" && k != "component" {
			walk(v, append(slices.Clone(loc), k))
		}
	}
	slices.SortFunc(out, func(a, b boundAt) int { return compareLoc(a.loc, b.loc) })
	return out
}

// checkPointer checks a JSON pointer (RFC 6901): absolute, or relative
// when it may be; "" when it is well formed.
func checkPointer(p string, absolute bool) string {
	if absolute && !strings.HasPrefix(p, "/") {
		return fmt.Sprintf("path %q must start with /", p)
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' && (i+1 == len(p) || (p[i+1] != '0' && p[i+1] != '1')) {
			return fmt.Sprintf("path %q: ~ must be followed by 0 or 1 (~0 is ~, ~1 is /)", p)
		}
	}
	return ""
}

// leaf is one thing wrong at one place.
type leaf struct {
	loc         []string
	msg         string
	unevaluated bool       // a property no schema allowed, maybe because its own schema failed
	typ         *kind.Type // a wrong type, to merge with the other forms'
	want        []any      // the values allowed (const, enum), to merge likewise
	excluded    bool       // the value is one a "not" excludes
	rejects     []string   // the properties refused here
}

var printer = message.NewPrinter(language.English)

// explain turns a schema validation error into the leaves an author can
// act on (leaves tells how).
func explain(err error) []leaf {
	var ve *jsonschema.ValidationError
	if err == nil || !errors.As(err, &ve) {
		if err != nil {
			return []leaf{{msg: err.Error()}}
		}
		return nil
	}
	return leaves(ve)
}

// leaves walks a validation error down to what is wrong. Where a value may
// take several forms (oneOf, anyOf), it keeps the form the author meant:
//   - a form whose discriminator fails (a property of the value with a
//     constant, an enum or a "not", such as "@call") is another kind of
//     object; so is a form of another type, or one that refuses the
//     discriminator property;
//   - when a discriminator is in play, the kept form that went deepest is
//     the one meant; when none is kept, the value names a kind that does
//     not exist, and one leaf says which ones do;
//   - else the form that went deepest into the value, if one got in;
//     when none did, one leaf says what the value may be.
func leaves(e *jsonschema.ValidationError) []leaf {
	at := e.InstanceLocation
	switch k := e.ErrorKind.(type) {
	case *kind.OneOf, *kind.AnyOf:
		if one, ok := k.(*kind.OneOf); ok && len(one.Subschemas) > 1 {
			return []leaf{{loc: at, msg: "matches more than one of the allowed forms"}}
		}
		type form struct {
			ls         []leaf
			disc, kind bool
		}
		var forms []form
		discs := map[string]bool{}
		var all []leaf
		for _, cause := range e.Causes {
			f := form{ls: leaves(cause)}
			for _, l := range f.ls {
				if len(l.loc) == len(at)+1 && (l.want != nil || l.excluded) {
					f.disc = true
					discs[l.loc[len(at)]] = true
				}
				if l.typ != nil && len(l.loc) == len(at) {
					f.kind = true
				}
			}
			forms = append(forms, f)
			all = append(all, f.ls...)
		}
		var best []leaf
		bestDepth := -1
		for _, f := range forms {
			if f.disc || f.kind || rejectsAny(f.ls, len(at), discs) {
				continue
			}
			if d := depth(f.ls); d > bestDepth {
				best, bestDepth = f.ls, d
			}
		}
		if len(discs) > 0 {
			if best != nil {
				return best
			}
			var named []leaf
			for _, f := range forms {
				if f.disc {
					named = append(named, f.ls...)
				}
			}
			return summarize(at, named, true)
		}
		if best != nil && bestDepth > len(at) {
			return best
		}
		return summarize(at, all, false)
	}
	if len(e.Causes) == 0 {
		l := leaf{loc: at, msg: e.ErrorKind.LocalizedString(printer)}
		switch k := e.ErrorKind.(type) {
		case *kind.FalseSchema:
			if strings.HasSuffix(e.SchemaURL, "/unevaluatedProperties") && len(at) > 0 {
				l.unevaluated = true
				l.msg = fmt.Sprintf("property %q is not allowed here", at[len(at)-1])
			}
		case *kind.Type:
			l.typ = k
		case *kind.Const:
			l.want = []any{k.Want}
		case *kind.Enum:
			l.want = k.Want
		case *kind.Not:
			l.excluded = true
		case *kind.AdditionalProperties:
			l.rejects = k.Properties
		}
		return []leaf{l}
	}
	var out []leaf
	for _, cause := range e.Causes {
		out = append(out, leaves(cause)...)
	}
	return out
}

// rejectsAny tells whether a form refuses one of the discriminator
// properties the value has.
func rejectsAny(ls []leaf, at int, discs map[string]bool) bool {
	for _, l := range ls {
		if len(l.loc) == at {
			for _, p := range l.rejects {
				if discs[p] {
					return true
				}
			}
		}
		if l.unevaluated && len(l.loc) == at+1 && discs[l.loc[at]] {
			return true
		}
	}
	return false
}

// depth is how far into the value the deepest leaf is.
func depth(ls []leaf) int {
	d := 0
	for _, l := range ls {
		if !l.unevaluated && len(l.loc) > d {
			d = len(l.loc)
		}
	}
	return d
}

// summarize says what a value may be, from the forms it fails: the types
// they want, merged; the values a property may take, merged; the rest.
// With named, only the values of the discriminators count.
func summarize(at []string, all []leaf, named bool) []leaf {
	if named {
		all = slices.DeleteFunc(slices.Clone(all), func(l leaf) bool { return l.want == nil || len(l.loc) != len(at)+1 })
	}
	var typ *kind.Type
	wants := map[string]*leaf{}
	var order []string
	var other []string
	for _, l := range all {
		switch {
		case l.unevaluated:
		case l.typ != nil && len(l.loc) == len(at):
			if typ == nil {
				typ = &kind.Type{Got: l.typ.Got}
			}
			for _, w := range l.typ.Want {
				if !slices.Contains(typ.Want, w) {
					typ.Want = append(typ.Want, w)
				}
			}
		case l.want != nil:
			p := pointer(l.loc)
			if wants[p] == nil {
				wants[p] = &leaf{loc: l.loc}
				order = append(order, p)
			}
			for _, w := range l.want {
				if !slices.Contains(wants[p].want, w) {
					wants[p].want = append(wants[p].want, w)
				}
			}
		default:
			if !slices.Contains(other, l.msg) {
				other = append(other, l.msg)
			}
		}
	}
	// Only constants of one property: say which values it may take.
	if typ == nil && len(other) == 0 && len(order) == 1 {
		w := wants[order[0]]
		w.msg = mustBe(w.want)
		return []leaf{*w}
	}
	var parts []string
	if typ != nil {
		parts = append(parts, typ.LocalizedString(printer))
	}
	for _, p := range order {
		parts = append(parts, strings.TrimPrefix(p, pointer(at)+"/")+" "+mustBe(wants[p].want))
	}
	parts = append(parts, other...)
	if len(parts) == 0 {
		parts = []string{"matches none of the allowed forms"}
	}
	l := leaf{loc: at, msg: strings.Join(parts, "; or ")}
	if typ != nil && len(parts) == 1 {
		l.typ = typ
	}
	return []leaf{l}
}

func mustBe(want []any) string {
	if len(want) == 1 {
		return "must be " + compactJSON(want[0])
	}
	parts := make([]string, len(want))
	for i, w := range want {
		parts[i] = compactJSON(w)
	}
	return "must be one of " + strings.Join(parts, ", ")
}

// dropUnevaluated leaves out "not allowed here" when something else is
// wrong: a property whose schema failed is also reported as unevaluated.
func dropUnevaluated(ls []leaf) []leaf {
	other := false
	for _, l := range ls {
		if !l.unevaluated {
			other = true
		}
	}
	if !other {
		return ls
	}
	return slices.DeleteFunc(ls, func(l leaf) bool { return l.unevaluated })
}

// pointer writes a location as a JSON pointer.
func pointer(loc []string) string {
	var b strings.Builder
	for _, t := range loc {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))
	}
	return b.String()
}

// compareLoc orders locations as they are written: numbers as numbers.
func compareLoc(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		x, errX := strconv.Atoi(a[i])
		y, errY := strconv.Atoi(b[i])
		if errX == nil && errY == nil {
			return x - y
		}
		return strings.Compare(a[i], b[i])
	}
	return len(a) - len(b)
}
