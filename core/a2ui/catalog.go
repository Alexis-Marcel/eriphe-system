package a2ui

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The published v1.0 schemas, unchanged (spec/NOTICE says from where).
//
//go:embed spec/v1_0/*.json
var specFS embed.FS

// Eriphe's catalog. Its descriptions and instructions are for the models
// that write surfaces, so they are in French, like the other prompts.
//
//go:embed catalog_v1.json
var catalogV1 []byte

// CatalogID is the id of Eriphe's catalog, v1: what a surface names in its
// catalogId. An identifier, not an address.
const CatalogID = "urn:eriphe:a2ui:workspace:v1"

const (
	// specBase is where the v1.0 schemas say they live; the envelope refers
	// to the catalog relatively, as catalog.json there.
	specBase     = "https://a2ui.org/specification/v1_0/"
	catalogAlias = specBase + "catalog.json"
	commonTypes  = "common_types.json#/$defs/"
)

// CatalogJSON returns Eriphe's catalog, the JSON Schema document the
// workspace's front reads.
func CatalogJSON() []byte { return bytes.Clone(catalogV1) }

// Catalog is a loaded catalog: it validates what an author writes against
// it and tells a model what it holds.
type Catalog struct {
	id        string
	doc       map[string]any
	component []string // in the order of $defs/anyComponent
	function  []string // in the order of $defs/anyFunction

	message *jsonschema.Schema            // agent_to_renderer.json
	kinds   map[string]*jsonschema.Schema // one message kind, by key
	shapes  map[string]*jsonschema.Schema // one component, with the envelope's common properties
	event   *jsonschema.Schema            // renderer_to_agent.json

	links    map[string][]link   // where a component names other components
	parents  map[string][]string // allowedParents, when set
	children map[string][]string // allowedChildren, when set
}

// link is a place in a component that names other components: a property
// path ("*" for any item of an array) holding one id or a child list.
type link struct {
	path []string
	list bool
}

// The message kinds an agent sends, and their schema in the envelope.
var messageKinds = map[string]string{
	"createSurface":         "CreateSurfaceMessage",
	"updateComponents":      "UpdateComponentsMessage",
	"updateDataModel":       "UpdateDataModelMessage",
	"deleteSurface":         "DeleteSurfaceMessage",
	"callRendererFunction":  "CallRendererFunctionMessage",
	"agentFunctionResponse": "AgentFunctionResponseMessage",
}

var eriphe = sync.OnceValue(func() *Catalog {
	c, err := LoadCatalog(catalogV1)
	if err != nil {
		panic("a2ui: Eriphe's catalog: " + err.Error())
	}
	return c
})

// Eriphe returns Eriphe's catalog, loaded once.
func Eriphe() *Catalog { return eriphe() }

// CatalogPrompt is the French section that tells a model which components
// and functions Eriphe's catalog offers.
func CatalogPrompt() string { return Eriphe().Prompt() }

// Validate checks agent messages against Eriphe's catalog (Catalog.Validate).
func Validate(raw []byte) []ValidationError { return Eriphe().Validate(raw) }

// ValidateSurface checks a surface against Eriphe's catalog
// (Catalog.ValidateSurface).
func ValidateSurface(s Surface) []ValidationError { return Eriphe().ValidateSurface(s) }

// ID returns the catalog's catalogId.
func (c *Catalog) ID() string { return c.id }

// Components lists the catalog's component names, in its order.
func (c *Catalog) Components() []string { return slices.Clone(c.component) }

// Functions lists the catalog's function names, in its order.
func (c *Catalog) Functions() []string { return slices.Clone(c.function) }

// LoadCatalog checks a catalog against A2UI v1.0's rules for catalogs and
// compiles it, with the embedded schemas, into a validator. It never
// fetches anything: a reference outside the embedded schemas is an error.
func LoadCatalog(raw []byte) (*Catalog, error) {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("catalog: not a JSON object")
	}
	if err := checkCatalog(doc); err != nil {
		return nil, err
	}
	c := &Catalog{doc: doc, kinds: map[string]*jsonschema.Schema{}, shapes: map[string]*jsonschema.Schema{},
		links: map[string][]link{}, parents: map[string][]string{}, children: map[string][]string{}}
	c.id, _ = doc["catalogId"].(string)
	c.component = refNames(doc, "anyComponent", "#/components/")
	c.function = refNames(doc, "anyFunction", "#/functions/")

	comp := newCompiler()
	if err := addSpec(comp); err != nil {
		return nil, err
	}
	// The envelope refers to "catalog.json" next to it: the catalog is
	// added there, as the A2UI conformance runner does.
	alias := shallowCopy(doc)
	alias["$id"] = catalogAlias
	if err := comp.AddResource(catalogAlias, alias); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	for _, name := range c.component {
		u := specBase + "eriphe/component/" + name + ".json"
		wrap := map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"allOf": []any{
				map[string]any{"$ref": specBase + commonTypes + "ComponentCommon"},
				map[string]any{"$ref": catalogAlias + "#/components/" + name},
			},
			"unevaluatedProperties": false,
		}
		if err := comp.AddResource(u, wrap); err != nil {
			return nil, err
		}
	}
	if c.message, err = comp.Compile(specBase + "agent_to_renderer.json"); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if c.event, err = comp.Compile(specBase + "renderer_to_agent.json"); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	for key, def := range messageKinds {
		if c.kinds[key], err = comp.Compile(specBase + "agent_to_renderer.json#/$defs/" + def); err != nil {
			return nil, fmt.Errorf("catalog: %w", err)
		}
	}
	comps, _ := doc["components"].(map[string]any)
	for _, name := range c.component {
		if c.shapes[name], err = comp.Compile(specBase + "eriphe/component/" + name + ".json"); err != nil {
			return nil, fmt.Errorf("catalog: component %s: %w", name, err)
		}
		def, _ := comps[name].(map[string]any)
		c.links[name] = findLinks(doc, def)
		if p, ok := stringList(def["allowedParents"]); ok {
			c.parents[name] = p
		}
		if ch, ok := stringList(def["allowedChildren"]); ok {
			c.children[name] = ch
		}
	}
	return c, nil
}

// newCompiler is a JSON Schema 2020-12 compiler that asserts formats (as
// the A2UI conformance runner does), knows Unicode identifiers in
// patterns, and loads nothing from anywhere.
func newCompiler() *jsonschema.Compiler {
	comp := jsonschema.NewCompiler()
	comp.DefaultDraft(jsonschema.Draft2020)
	comp.AssertFormat()
	comp.UseRegexpEngine(compileRegexp)
	comp.UseLoader(noLoader{})
	return comp
}

type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("%s is not one of the embedded A2UI schemas", url)
}

// addSpec adds the embedded v1.0 schemas, each at its $id.
func addSpec(comp *jsonschema.Compiler) error {
	entries, err := fs.ReadDir(specFS, "spec/v1_0")
	if err != nil {
		return err
	}
	for _, e := range entries {
		raw, err := specFS.ReadFile("spec/v1_0/" + e.Name())
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		id, _ := doc.(map[string]any)["$id"].(string)
		if id == "" {
			return fmt.Errorf("%s: no $id", e.Name())
		}
		if err := comp.AddResource(id, doc); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return nil
}

// compileRegexp compiles a schema's pattern with Go's regexp, which knows
// no XID_Start or XID_Continue: they become the general categories they
// are made of (UAX #31), close enough for an identifier.
func compileRegexp(s string) (jsonschema.Regexp, error) {
	s = strings.NewReplacer(
		`\p{XID_Start}`, `\p{L}\p{Nl}`,
		`\p{XID_Continue}`, `\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}`,
	).Replace(s)
	return regexp.Compile(s)
}

// isIdentifier tells whether s is a UAX #31 identifier, as A2UI wants of
// component ids and of the names in a catalog.
func isIdentifier(s string) bool {
	for i, r := range s {
		start := unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || r == '_'
		if i == 0 && !start {
			return false
		}
		if !start && !unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc) {
			return false
		}
	}
	return s != ""
}

// The schemas of common_types.json a catalog may refer to (rule 3 of the
// catalog rules in the A2UI protocol).
var allowedCommon = map[string]bool{
	"ComponentId": true, "Child": true, "ChildList": true, "DynamicString": true, "DynamicNumber": true,
	"DynamicBoolean": true, "DynamicStringList": true, "DynamicValue": true, "AccessibilityAttributes": true,
	"CheckRule": true, "Checkable": true, "Action": true, "DataBinding": true, "FunctionCall": true,
}

// Keys whose values are data or annotations, not schemas.
var notSchema = map[string]bool{"metadata": true, "examples": true, "const": true, "default": true, "enum": true, "description": true}

var catalogDefinition = sync.OnceValues(func() (*jsonschema.Schema, error) {
	comp := newCompiler()
	if err := addSpec(comp); err != nil {
		return nil, err
	}
	return comp.Compile(specBase + "catalog_definition.json")
})

// checkCatalog applies A2UI v1.0's rules for a catalog: its definition
// schema, names that are identifiers, a component's discriminator equal to
// its key, references only to the catalog itself and to the allowed common
// types, and anyComponent / anyFunction listing every entry.
func checkCatalog(doc map[string]any) error {
	def, err := catalogDefinition()
	if err != nil {
		return err
	}
	if err := def.Validate(doc); err != nil {
		return fmt.Errorf("catalog: %s", firstLines(err.Error(), 6))
	}
	if id, _ := doc["catalogId"].(string); id == "" {
		return errors.New("catalog: no catalogId")
	}
	comps, _ := doc["components"].(map[string]any)
	funcs, _ := doc["functions"].(map[string]any)
	defs, _ := doc["$defs"].(map[string]any)
	var problems []string
	for name, v := range comps {
		if !isIdentifier(name) {
			problems = append(problems, fmt.Sprintf("component name %q is not an identifier", name))
		}
		def, _ := v.(map[string]any)
		if !discriminated(def, name) {
			problems = append(problems, fmt.Sprintf("component %s: needs a required property component with const %q", name, name))
		}
		checkNames(def, &problems)
	}
	for name, v := range funcs {
		if !isIdentifier(strings.TrimPrefix(name, "@")) {
			problems = append(problems, fmt.Sprintf("function name %q is not an identifier", name))
		}
		checkNames(v, &problems)
	}
	for _, section := range []map[string]any{comps, funcs, defs} {
		for _, v := range section {
			for _, ref := range collectRefs(v, nil) {
				if msg := checkRef(ref, comps, funcs, defs); msg != "" {
					problems = append(problems, msg)
				}
			}
		}
	}
	for _, list := range []struct{ def, prefix string }{{"anyComponent", "#/components/"}, {"anyFunction", "#/functions/"}} {
		section := comps
		if list.def == "anyFunction" {
			section = funcs
		}
		listed := refNames(doc, list.def, list.prefix)
		for name := range section {
			if !slices.Contains(listed, name) {
				problems = append(problems, fmt.Sprintf("$defs/%s does not list %s", list.def, name))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("catalog: %s", strings.Join(problems, "; "))
	}
	return nil
}

// discriminated tells whether a component's schema requires a property
// component whose const is its name.
func discriminated(def map[string]any, name string) bool {
	if def == nil {
		return false
	}
	if p, _ := def["properties"].(map[string]any); p != nil {
		if c, _ := p["component"].(map[string]any); c != nil && c["const"] == name {
			req, _ := stringList(def["required"])
			return slices.Contains(req, "component")
		}
	}
	// A trait (Checkable) and the properties, under allOf.
	all, _ := def["allOf"].([]any)
	for _, sub := range all {
		if m, _ := sub.(map[string]any); m != nil && discriminated(m, name) {
			return true
		}
	}
	return false
}

// checkNames reports the property names of a schema that are not
// identifiers (@path and @call are the protocol's own).
func checkNames(v any, problems *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if props, ok := t["properties"].(map[string]any); ok {
			for name, sub := range props {
				if name != "@path" && name != "@call" && !isIdentifier(name) {
					*problems = append(*problems, fmt.Sprintf("property name %q is not an identifier", name))
				}
				checkNames(sub, problems)
			}
		}
		for k, sub := range t {
			if k != "properties" && !notSchema[k] {
				checkNames(sub, problems)
			}
		}
	case []any:
		for _, sub := range t {
			checkNames(sub, problems)
		}
	}
}

// collectRefs gathers the $ref values under a schema.
func collectRefs(v any, out []string) []string {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			out = append(out, ref)
		}
		if props, ok := t["properties"].(map[string]any); ok {
			for _, sub := range props {
				out = collectRefs(sub, out)
			}
		}
		for k, sub := range t {
			if k != "properties" && !notSchema[k] {
				out = collectRefs(sub, out)
			}
		}
	case []any:
		for _, sub := range t {
			out = collectRefs(sub, out)
		}
	}
	return out
}

func checkRef(ref string, comps, funcs, defs map[string]any) string {
	has := func(m map[string]any, k string) bool { _, ok := m[k]; return ok }
	switch {
	case strings.HasPrefix(ref, "#/components/"):
		if !has(comps, strings.TrimPrefix(ref, "#/components/")) {
			return fmt.Sprintf("unknown $ref %q", ref)
		}
	case strings.HasPrefix(ref, "#/functions/"):
		if !has(funcs, strings.TrimPrefix(ref, "#/functions/")) {
			return fmt.Sprintf("unknown $ref %q", ref)
		}
	case strings.HasPrefix(ref, "#/$defs/"):
		name := strings.TrimPrefix(ref, "#/$defs/")
		if name == "anyComponent" || name == "anyFunction" {
			return fmt.Sprintf("$ref %q is not allowed", ref)
		}
		if !has(defs, name) {
			return fmt.Sprintf("unknown $ref %q", ref)
		}
	case strings.HasPrefix(ref, commonTypes):
		if !allowedCommon[strings.TrimPrefix(ref, commonTypes)] {
			return fmt.Sprintf("$ref %q is not one of the allowed common types", ref)
		}
	default:
		return fmt.Sprintf("$ref %q is not allowed", ref)
	}
	return ""
}

// refNames lists, in order, the names a $defs union refers to.
func refNames(doc map[string]any, def, prefix string) []string {
	defs, _ := doc["$defs"].(map[string]any)
	union, _ := defs[def].(map[string]any)
	var out []string
	for _, key := range []string{"oneOf", "anyOf"} {
		list, _ := union[key].([]any)
		for _, v := range list {
			m, _ := v.(map[string]any)
			if ref, _ := m["$ref"].(string); strings.HasPrefix(ref, prefix) {
				out = append(out, strings.TrimPrefix(ref, prefix))
			}
		}
	}
	return out
}

// findLinks finds where a component's schema names other components: a
// $ref to ComponentId or Child (one id) or ChildList (ids or a template),
// under properties, items and combinators, through the catalog's $defs.
func findLinks(doc map[string]any, def map[string]any) []link {
	defs, _ := doc["$defs"].(map[string]any)
	var out []link
	var walk func(v any, path []string, seen map[string]bool)
	walk = func(v any, path []string, seen map[string]bool) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if ref, ok := m["$ref"].(string); ok {
			switch strings.TrimPrefix(ref, commonTypes) {
			case "ComponentId", "Child":
				out = append(out, link{path: slices.Clone(path)})
			case "ChildList":
				out = append(out, link{path: slices.Clone(path), list: true})
			}
			if name, ok := strings.CutPrefix(ref, "#/$defs/"); ok && !seen[name] {
				seen[name] = true
				walk(defs[name], path, seen)
				delete(seen, name)
			}
		}
		if props, ok := m["properties"].(map[string]any); ok {
			names := make([]string, 0, len(props))
			for k := range props {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				walk(props[k], append(slices.Clone(path), k), seen)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, append(slices.Clone(path), "*"), seen)
		}
		for _, key := range []string{"allOf", "anyOf", "oneOf"} {
			list, _ := m[key].([]any)
			for _, sub := range list {
				walk(sub, path, seen)
			}
		}
	}
	walk(def, nil, map[string]bool{})
	return out
}

func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

func shallowCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, " ")
}
