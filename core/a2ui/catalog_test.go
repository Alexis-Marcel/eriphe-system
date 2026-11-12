package a2ui

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The catalog holds the shapes agreed for the workspace (#416), in this
// order: a new one is a decision, not a side effect.
func TestCatalogComponents(t *testing.T) {
	want := []string{"Text", "Markdown", "Image", "Icon", "Row", "Column", "List", "Card", "Tabs", "Divider",
		"Button", "Choice", "TextField", "DateField", "Metric", "Chart", "Table", "Timeline", "Message", "Day",
		"Proposal", "Alert", "Placeholder"}
	c := Eriphe()
	if got := c.Components(); !slices.Equal(got, want) {
		t.Errorf("components:\n got %v\nwant %v", got, want)
	}
	if got := c.Functions(); !slices.Equal(got, []string{"formatString", "formatNumber", "formatDate", "formatRelative"}) {
		t.Errorf("functions: %v", got)
	}
	if c.ID() != CatalogID {
		t.Errorf("id %q", c.ID())
	}
}

// The catalog is the document served to the front, and says what it is.
func TestCatalogJSON(t *testing.T) {
	raw := CatalogJSON()
	if !bytes.Equal(raw, catalogV1) {
		t.Fatal("CatalogJSON differs from the embedded catalog")
	}
	raw[0] = 'x' // a copy: the catalog stays as it is
	if catalogV1[0] != '{' {
		t.Fatal("CatalogJSON gave the embedded bytes")
	}
	var doc map[string]any
	if err := json.Unmarshal(catalogV1, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["catalogId"] != CatalogID || doc["$id"] != CatalogID || doc["protocolVersion"] != Version {
		t.Errorf("catalogId %v, $id %v, protocolVersion %v", doc["catalogId"], doc["$id"], doc["protocolVersion"])
	}
	if s, _ := doc["instructions"].(string); !strings.Contains(s, "jamais des outils") {
		t.Error("the instructions do not say that components are shapes")
	}
}

// Every component describes itself and its data-bound properties take a
// binding: a model learns what a shape is for from the catalog alone.
func TestCatalogDescribesItsComponents(t *testing.T) {
	c := Eriphe()
	comps, _ := c.doc["components"].(map[string]any)
	for _, name := range c.component {
		def, _ := comps[name].(map[string]any)
		if d, _ := def["description"].(string); d == "" {
			t.Errorf("%s has no description", name)
		}
	}
	// The properties that carry data accept a binding.
	for _, p := range []struct{ comp, prop string }{
		{"Metric", "value"}, {"Metric", "delta"}, {"Chart", "series"}, {"Table", "rows"}, {"Timeline", "items"},
		{"Day", "events"}, {"Day", "date"}, {"Message", "from"}, {"Message", "body"}, {"Proposal", "proposalId"},
	} {
		comp := `{"id":"root","component":"` + p.comp + `"`
		for _, req := range requiredOf(comps[p.comp]) {
			if req != "component" && req != p.prop {
				comp += `,"` + req + `":` + sampleFor(p.comp, req)
			}
		}
		comp += `,"` + p.prop + `":{"@path":"/x"}}`
		if errs := ValidateSurface(surfaceOf(comp)); len(errs) > 0 {
			t.Errorf("%s.%s does not take a binding: %v", p.comp, p.prop, errs)
		}
	}
}

func requiredOf(def any) []string {
	m, _ := def.(map[string]any)
	r, _ := stringList(m["required"])
	return r
}

func sampleFor(comp, prop string) string {
	switch comp + "." + prop {
	case "Chart.kind":
		return `"line"`
	case "Table.columns":
		return `[{"key":"a","label":"A"}]`
	}
	return `{"@path":"/y"}`
}

// The structure of each component (where it names others) comes from the
// catalog's schemas, through its $defs.
func TestCatalogLinks(t *testing.T) {
	c := Eriphe()
	got := map[string]string{}
	for name, ls := range c.links {
		var parts []string
		for _, l := range ls {
			s := strings.Join(l.path, "/")
			if l.list {
				s += "[]"
			}
			parts = append(parts, s)
		}
		if len(parts) > 0 {
			got[name] = strings.Join(parts, " ")
		}
	}
	want := map[string]string{"Row": "children[]", "Column": "children[]", "List": "children[]", "Card": "child", "Tabs": "tabs/*/child"}
	if len(got) != len(want) {
		t.Errorf("links: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: links %q, want %q", k, got[k], v)
		}
	}
}

// A catalog that breaks A2UI's rules for catalogs does not load.
func TestLoadCatalogRefuses(t *testing.T) {
	base := func(components, defs string) string {
		return `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.org/c.json","protocolVersion":"1.0","catalogId":"https://example.org/c.json",
"components":{` + components + `},"$defs":{"anyComponent":{"oneOf":[{"$ref":"#/components/A"}]},"anyFunction":false` + defs + `}}`
	}
	good := `"A":{"type":"object","properties":{"component":{"const":"A"},"text":{"$ref":"common_types.json#/$defs/DynamicString"}},"required":["component"]}`
	if _, err := LoadCatalog([]byte(base(good, ""))); err != nil {
		t.Fatalf("a good catalog: %v", err)
	}
	for name, tc := range map[string]struct{ raw, words string }{
		"not JSON":              {`{`, "catalog"},
		"not an object":         {`[]`, "not a JSON object"},
		"no catalogId":          {`{"$defs":{"anyComponent":true,"anyFunction":true}}`, "catalogId"},
		"no discriminator":      {base(`"A":{"type":"object","properties":{"text":{"type":"string"}}}`, ""), "needs a required property component"},
		"wrong discriminator":   {base(`"A":{"type":"object","properties":{"component":{"const":"B"}},"required":["component"]}`, ""), "needs a required property component"},
		"name not identifier":   {base(good+`,"my-comp":{"type":"object","properties":{"component":{"const":"my-comp"}},"required":["component"]}`, ""), `component name "my-comp"`},
		"property name":         {base(`"A":{"type":"object","properties":{"component":{"const":"A"},"font-size":{"type":"number"}},"required":["component"]}`, ""), `property name "font-size"`},
		"not listed":            {base(good+`,"B":{"type":"object","properties":{"component":{"const":"B"}},"required":["component"]}`, ""), "anyComponent does not list B"},
		"reserved Surface":      {base(good+`,"Surface":{"type":"object","properties":{"component":{"const":"Surface"}},"required":["component"]}`, ""), "catalog"},
		"external ref":          {base(`"A":{"type":"object","properties":{"component":{"const":"A"},"x":{"$ref":"https://example.org/other.json#/x"}},"required":["component"]}`, ""), "is not allowed"},
		"common type refused":   {base(`"A":{"type":"object","properties":{"component":{"const":"A"},"x":{"$ref":"common_types.json#/$defs/Surface"}},"required":["component"]}`, ""), "not one of the allowed common types"},
		"unknown local ref":     {base(`"A":{"type":"object","properties":{"component":{"const":"A"},"x":{"$ref":"#/$defs/Nope"}},"required":["component"]}`, ""), `unknown $ref "#/$defs/Nope"`},
		"ref to anyComponent":   {base(`"A":{"type":"object","properties":{"component":{"const":"A"},"x":{"$ref":"#/$defs/anyComponent"}},"required":["component"]}`, ""), "is not allowed"},
		"component in $defs":    {base(good, `,"Hidden":{"type":"object","properties":{"component":{"const":"Hidden"}}}`), "catalog"},
		"unknown top-level key": {strings.Replace(base(good, ""), `"protocolVersion"`, `"theme":{},"protocolVersion"`, 1), "catalog"},
	} {
		if _, err := LoadCatalog([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.words) {
			t.Errorf("%s: %v, want an error with %q", name, err, tc.words)
		}
	}
}

func TestIsIdentifier(t *testing.T) {
	for _, s := range []string{"root", "user_card", "userCard", "item_id_1", "_internal", "étape", "Δx"} {
		if !isIdentifier(s) {
			t.Errorf("%q is an identifier", s)
		}
	}
	for _, s := range []string{"", "user-card", "1st", "User Card", "a#b", "calc$val", "@path"} {
		if isIdentifier(s) {
			t.Errorf("%q is not an identifier", s)
		}
	}
	// The same rule as the schemas' pattern, through the regexp engine.
	re, err := compileRegexp(`^[\p{XID_Start}_][\p{XID_Continue}]*$`)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"user_card", "étape", "user-card", "1st"} {
		if re.MatchString(s) != isIdentifier(s) {
			t.Errorf("%q: pattern and isIdentifier differ", s)
		}
	}
}
