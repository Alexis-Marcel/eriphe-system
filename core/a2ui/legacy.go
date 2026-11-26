package a2ui

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Legacy tells whether a component's or a property's schema is marked
// "x-legacy": the catalog still validates and draws it, so that a node
// written with it keeps its page, but no model is told of it: another
// component does its job (List, Item and Fold: Sections; Row of Metric:
// Figures; Grid and Card: Gallery; Button: Asks; Progress: Timeline).
func Legacy(schema any) bool {
	m, _ := schema.(map[string]any)
	l, _ := m["x-legacy"].(bool)
	return l
}

// IsLegacy tells whether a component of Eriphe's catalog is legacy.
func IsLegacy(componentType string) bool { return Legacy(componentDoc(componentType)) }

// SourceProp is the property of a component that may name one of its
// block's sources ("x-source": Sections', Gallery's items, Table's rows):
// code reads that source's rows there (nodefile's Block.Bound). "" when
// it has none.
func SourceProp(componentType string) string {
	p, _ := componentDoc(componentType)["x-source"].(string)
	return p
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
