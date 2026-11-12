package a2ui

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

// Prompt is the section that tells a model what the catalog holds, for any
// assistant that writes surfaces: the catalog's instructions, then each
// component with its properties and each function with its arguments,
// written from the catalog alone. It is in French, like Eriphe's prompts,
// and the same for the same catalog.
func (c *Catalog) Prompt() string {
	var b strings.Builder
	b.WriteString("# Écrire une surface (A2UI " + Version + ")\n\n")
	if s, _ := c.doc["instructions"].(string); s != "" {
		b.WriteString(strings.TrimSpace(s))
		b.WriteString("\n\n")
	}
	comps, _ := c.doc["components"].(map[string]any)
	b.WriteString("## Composants\n")
	b.WriteString("`*` : obligatoire. « fixe » : une valeur écrite telle quelle, sans liaison.\n\n")
	for _, name := range c.component {
		def, _ := comps[name].(map[string]any)
		b.WriteString("- **" + name + "**")
		if d, _ := def["description"].(string); d != "" {
			b.WriteString(" : " + d)
		}
		b.WriteString("\n")
		if props := c.describeProps(def, "component"); props != "" {
			b.WriteString("  " + props + "\n")
		}
		if ex, ok := def["examples"].([]any); ok && len(ex) > 0 {
			b.WriteString("  Exemple : `" + compactJSON(ex[0]) + "`\n")
		}
	}
	funcs, _ := c.doc["functions"].(map[string]any)
	if len(c.function) > 0 {
		b.WriteString("\n## Fonctions\n")
		b.WriteString("Un appel s'écrit `{\"@call\": \"nom\", \"args\": {…}}`, là où une valeur est attendue.\n\n")
		for _, name := range c.function {
			def, _ := funcs[name].(map[string]any)
			props, _ := def["properties"].(map[string]any)
			args, _ := props["args"].(map[string]any)
			b.WriteString("- **" + name + "**(" + c.describeProps(args, "") + ")")
			if r, _ := def["returnType"].(string); r != "" && returnTypes[r] != "" {
				b.WriteString(" → " + returnTypes[r])
			}
			if d, _ := def["description"].(string); d != "" {
				b.WriteString(" : " + d)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

var returnTypes = map[string]string{
	"string": "texte", "number": "nombre", "boolean": "booléen", "array": "liste", "object": "objet",
	"validationResult": "vérification", "any": "valeur",
}

// The common types, as a model reads them.
var commonNames = map[string]string{
	"DynamicString": "texte", "DynamicNumber": "nombre", "DynamicBoolean": "booléen",
	"DynamicStringList": "liste de textes", "DynamicValue": "valeur",
	"ComponentId": "id d'un composant", "Child": "id d'un composant",
	"ChildList":   "enfants ([ids] ou modèle {path, componentId})",
	"Action":      `action ({"event": {"name", "context"}} ou {"functionCall": appel})`,
	"DataBinding": "liaison", "FunctionCall": "appel", "AccessibilityAttributes": "accessibilité",
	"CheckRule": "vérification",
}

// describeProps writes an object schema's properties, the required ones
// first in their order, then the others by name: "name* type (description)".
func (c *Catalog) describeProps(def map[string]any, skip string) string {
	props := map[string]any{}
	var required []string
	var gather func(m map[string]any)
	gather = func(m map[string]any) {
		if p, ok := m["properties"].(map[string]any); ok {
			for k, v := range p {
				props[k] = v
			}
		}
		if r, ok := stringList(m["required"]); ok {
			required = append(required, r...)
		}
		all, _ := m["allOf"].([]any)
		for _, sub := range all {
			if s, ok := sub.(map[string]any); ok {
				gather(s)
			}
		}
	}
	gather(def)
	var names []string
	for _, r := range required {
		if _, ok := props[r]; ok && r != skip && !slices.Contains(names, r) {
			names = append(names, r)
		}
	}
	var rest []string
	for k := range props {
		if k != skip && !slices.Contains(names, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	names = append(names, rest...)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		p, _ := props[k].(map[string]any)
		s := k
		if slices.Contains(required, k) {
			s += "*"
		}
		s += " " + c.typeOf(p, 0)
		if d, _ := p["description"].(string); d != "" {
			s += " (" + strings.TrimSuffix(d, ".") + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// typeOf says what a schema accepts, in a few words.
func (c *Catalog) typeOf(s map[string]any, depth int) string {
	if s == nil || depth > 4 {
		return "valeur"
	}
	if ref, ok := s["$ref"].(string); ok {
		if name, ok := strings.CutPrefix(ref, commonTypes); ok {
			if n := commonNames[name]; n != "" {
				return n
			}
			return name
		}
		if name, ok := strings.CutPrefix(ref, "#/$defs/"); ok {
			defs, _ := c.doc["$defs"].(map[string]any)
			def, _ := defs[name].(map[string]any)
			return c.typeOf(def, depth+1)
		}
		return "valeur"
	}
	if enum, ok := s["enum"].([]any); ok {
		parts := make([]string, 0, len(enum))
		for _, v := range enum {
			parts = append(parts, compactJSON(v))
		}
		return strings.ReplaceAll(strings.Join(parts, "|"), `"`, "")
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if list, ok := s[key].([]any); ok {
			var parts []string
			for _, v := range list {
				m, _ := v.(map[string]any)
				if t := c.typeOf(m, depth+1); !slices.Contains(parts, t) {
					parts = append(parts, t)
				}
			}
			return strings.Join(parts, " ou ")
		}
	}
	switch t := s["type"].(type) {
	case []any:
		var parts []string
		for _, x := range t {
			parts = append(parts, c.typeOf(map[string]any{"type": x}, depth+1))
		}
		return strings.Join(parts, " ou ")
	case string:
		switch t {
		case "string":
			return "texte fixe"
		case "number", "integer":
			return "nombre fixe"
		case "boolean":
			return "booléen fixe"
		case "array":
			items, _ := s["items"].(map[string]any)
			return "[" + c.typeOf(items, depth+1) + ", …]"
		case "object":
			if _, ok := s["properties"]; ok {
				return "{" + c.describeProps(s, "") + "}"
			}
			return "objet"
		}
	}
	return "valeur"
}

// compactJSON writes a value on one line, a component's id and type first,
// then its keys in order, with no escaping of < > &.
func compactJSON(v any) string {
	var b bytes.Buffer
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		rank := func(k string) int {
			switch k {
			case "id":
				return 0
			case "component":
				return 1
			}
			return 2
		}
		sort.Slice(keys, func(i, j int) bool {
			if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
				return ri < rj
			}
			return keys[i] < keys[j]
		})
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			writeJSON(b, k)
			b.WriteString(": ")
			writeJSON(b, t[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			writeJSON(b, x)
		}
		b.WriteByte(']')
	default:
		enc := json.NewEncoder(b)
		enc.SetEscapeHTML(false)
		enc.Encode(t)
		b.Truncate(b.Len() - 1) // the encoder's newline
	}
}
