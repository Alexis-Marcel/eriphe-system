package a2ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// surfaceOf writes a surface from components given as JSON objects.
func surfaceOf(components ...string) Surface {
	return Surface{ID: "s1", Components: json.RawMessage("[" + strings.Join(components, ",") + "]")}
}

// The examples the catalog gives a model are valid: those of its
// instructions, and each component's own.
func TestCatalogExamplesAreValid(t *testing.T) {
	c := Eriphe()
	instructions, _ := c.doc["instructions"].(string)
	blocks := regexp.MustCompile("(?s)```json\n(.*?)```").FindAllStringSubmatch(instructions, -1)
	if len(blocks) < 2 {
		t.Fatalf("%d examples in the instructions", len(blocks))
	}
	for i, b := range blocks {
		if errs := c.ValidateSurface(Surface{ID: "ex", Components: json.RawMessage(b[1])}); len(errs) > 0 {
			t.Errorf("instructions, example %d: %v", i+1, errs)
		}
	}
	comps, _ := c.doc["components"].(map[string]any)
	n := 0
	for _, name := range c.component {
		def, _ := comps[name].(map[string]any)
		examples, _ := def["examples"].([]any)
		for _, ex := range examples {
			m, _ := ex.(map[string]any)
			m = shallowCopy(m)
			m["id"] = "root"
			raw, _ := json.Marshal([]any{m})
			if errs := c.ValidateSurface(Surface{ID: "ex", Components: raw}); len(errs) > 0 {
				t.Errorf("%s's example: %v", name, errs)
			}
			n++
		}
	}
	if n < 5 {
		t.Errorf("only %d component examples", n)
	}
}

// A surface using every kind of value: literals, bindings, calls, a list
// template with relative paths, a nested template, @index in a template.
func TestValidSurface(t *testing.T) {
	s := surfaceOf(
		`{"id":"root","component":"Column","children":["title","sales","mails","tabs"]}`,
		`{"id":"title","component":"Text","variant":"title","text":{"@call":"formatString","args":{"value":"Ventes de ${/shop/name}"}}}`,
		`{"id":"sales","component":"Metric","label":"Total","value":{"@path":"/sales/total"},"unit":"€","delta":{"@path":"/sales/delta"}}`,
		`{"id":"mails","component":"List","children":{"path":"/threads","componentId":"thread"},"emptyText":"Rien"}`,
		`{"id":"thread","component":"Column","children":["head","replies"]}`,
		`{"id":"head","component":"Text","text":{"@call":"formatString","args":{"value":"${@index(offset: 1)}. ${subject}"}}}`,
		`{"id":"replies","component":"List","children":{"path":"messages","componentId":"reply"}}`,
		`{"id":"reply","component":"Message","from":{"@path":"from"},"date":{"@call":"formatRelative","args":{"value":{"@path":"date"}}},"subject":{"@path":"/shop/name"}}`,
		`{"id":"tabs","component":"Tabs","tabs":[{"title":"Courbe","child":"chart"},{"title":"Lignes","child":"table"}]}`,
		`{"id":"chart","component":"Chart","kind":"line","series":{"@path":"/sales/series"}}`,
		`{"id":"table","component":"Table","columns":[{"key":"name","label":"Nom"}],"rows":{"@path":"/sales/rows"}}`,
	)
	s.DataModel = json.RawMessage(`{"shop":{"name":"Sam"},"sales":{"total":12}}`)
	if errs := ValidateSurface(s); len(errs) > 0 {
		t.Fatal(errs)
	}
	// @index as a function call, in a template.
	s = surfaceOf(
		`{"id":"root","component":"List","children":{"path":"/items","componentId":"n"}}`,
		`{"id":"n","component":"Metric","label":{"@path":"label"},"value":{"@call":"@index","args":{"offset":1}}}`,
	)
	if errs := ValidateSurface(s); len(errs) > 0 {
		t.Fatal(errs)
	}
}

// What a model gets wrong comes back where it is, in a sentence it can act on.
func TestInvalidSurfaces(t *testing.T) {
	cases := []struct {
		name  string
		s     Surface
		code  string
		path  string
		words string
	}{
		{"wrong type", surfaceOf(`{"id":"root","component":"Text","text":42}`),
			CodeValidationFailed, "/components/0/text", "got number, want string or object"},
		{"unknown property", surfaceOf(`{"id":"root","component":"Text","text":"a","color":"red"}`),
			CodeValidationFailed, "/components/0/color", `property "color" is not allowed`},
		{"unknown component", surfaceOf(`{"id":"root","component":"Gauge","value":3}`),
			CodeValidationFailed, "/components/0/component", `unknown component "Gauge"; the catalog has Text, Markdown`},
		{"no component type", surfaceOf(`{"id":"root","text":"a"}`),
			CodeValidationFailed, "/components/0", `missing property "component"`},
		{"missing property", surfaceOf(`{"id":"root","component":"Metric","value":3}`),
			CodeValidationFailed, "/components/0", "missing property 'label'"},
		{"enum", surfaceOf(`{"id":"root","component":"Chart","kind":"pie","series":[]}`),
			CodeValidationFailed, "/components/0/kind", "'line', 'bar'"},
		{"nested additional property", surfaceOf(`{"id":"root","component":"Timeline","items":[{"label":"a","state":"done","color":"red"}]}`),
			CodeValidationFailed, "/components/0/items/0", "additional properties 'color' not allowed"},
		{"rows not a list", surfaceOf(`{"id":"root","component":"Table","columns":[{"key":"a","label":"A"}],"rows":"a"}`),
			CodeValidationFailed, "/components/0/rows", "got string, want array or object"},
		{"unknown function", surfaceOf(`{"id":"root","component":"Text","text":{"@call":"formatDat","args":{"value":"x"}}}`),
			CodeValidationFailed, "/components/0/text/@call", `must be one of "formatString", "formatNumber", "formatDate", "formatRelative", "@index"`},
		{"missing argument", surfaceOf(`{"id":"root","component":"Text","text":{"@call":"formatDate","args":{"value":"x"}}}`),
			CodeValidationFailed, "/components/0/text/args", "missing property 'format'"},
		{"binding with a wrong key", surfaceOf(`{"id":"root","component":"Text","text":{"path":"/a"}}`),
			CodeValidationFailed, "/components/0/text", "missing property '@path'"},
		{"no root", surfaceOf(`{"id":"top","component":"Text","text":"a"}`),
			CodeValidationFailed, "/components", `no component has the id "root"`},
		{"dangling child", surfaceOf(`{"id":"root","component":"Card","child":"body"}`),
			CodeValidationFailed, "/components/0/child", `no component has the id "body"`},
		{"dangling child in a list", surfaceOf(`{"id":"root","component":"Row","children":["a","b"]}`, `{"id":"a","component":"Divider"}`),
			CodeValidationFailed, "/components/0/children/1", `no component has the id "b"`},
		{"dangling tab", surfaceOf(`{"id":"root","component":"Tabs","tabs":[{"title":"A","child":"a"}]}`),
			CodeValidationFailed, "/components/0/tabs/0/child", `no component has the id "a"`},
		{"dangling template", surfaceOf(`{"id":"root","component":"List","children":{"path":"/x","componentId":"row"}}`),
			CodeValidationFailed, "/components/0/children/componentId", `no component has the id "row"`},
		{"cycle", surfaceOf(`{"id":"root","component":"Card","child":"a"}`, `{"id":"a","component":"Column","children":["root"]}`),
			CodeValidationFailed, "/components/1/children/0", "cycle: root → a → root"},
		{"self", surfaceOf(`{"id":"root","component":"Card","child":"root"}`),
			CodeValidationFailed, "/components/0/child", "cycle: root → root"},
		{"duplicate id", surfaceOf(`{"id":"root","component":"Divider"}`, `{"id":"root","component":"Divider"}`),
			CodeValidationFailed, "/components/1/id", `id "root" is used twice`},
		{"id not an identifier", surfaceOf(`{"id":"root","component":"Card","child":"user-card"}`, `{"id":"user-card","component":"Divider"}`),
			CodeValidationFailed, "/components/1/id", `id "user-card" is not an identifier`},
		{"relative path at the root", surfaceOf(`{"id":"root","component":"Text","text":{"@path":"name"}}`),
			CodeValidationFailed, "/components/0/text/@path", `relative path "name" outside a list template: start it with /`},
		{"relative path in a call at the root", surfaceOf(`{"id":"root","component":"Text","text":{"@call":"formatNumber","args":{"value":{"@path":"n"}}}}`),
			CodeValidationFailed, "/components/0/text/args/value/@path", `relative path "n" outside a list template`},
		{"relative template path at the root", surfaceOf(`{"id":"root","component":"List","children":{"path":"items","componentId":"i"}}`, `{"id":"i","component":"Divider"}`),
			CodeValidationFailed, "/components/0/children/path", `relative path "items" outside a list template`},
		{"@index at the root", surfaceOf(`{"id":"root","component":"Metric","label":"n","value":{"@call":"@index"}}`),
			CodeValidationFailed, "/components/0/value", "@index is only known inside a list template"},
		{"bad pointer", surfaceOf(`{"id":"root","component":"Text","text":{"@path":"/a~2b"}}`),
			CodeValidationFailed, "/components/0/text/@path", "~ must be followed by 0 or 1"},
		{"another catalog", surfaceOf(`{"id":"root","component":"Divider","@catalogId":"https://example.org/other"}`),
			CodeValidationFailed, "/components/0/@catalogId", `unknown catalog "https://example.org/other"`},
		{"no components", Surface{ID: "s1"}, CodeValidationFailed, "/components", "a surface needs components"},
		{"empty components", surfaceOf(), CodeValidationFailed, "/components", "minItems"},
		{"data not an object", Surface{ID: "s1", Components: json.RawMessage(`[{"id":"root","component":"Divider"}]`), DataModel: json.RawMessage(`[1]`)},
			CodeValidationFailed, "/dataModel", "got array, want object"},
		{"not JSON", Surface{ID: "s1", Components: json.RawMessage(`[{`)}, CodeValidationFailed, "/components", "not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateSurface(tc.s)
			assertA2UIErrors(t, errs)
			for _, e := range errs {
				if e.Code == tc.code && e.Path == tc.path && strings.Contains(e.Message, tc.words) && e.SurfaceID == tc.s.ID {
					return
				}
			}
			t.Errorf("want %s at %s with %q, got %v", tc.code, tc.path, tc.words, errs)
		})
	}
}

// One wrong value is one error, not one per form it might have taken.
func TestOneErrorPerMistake(t *testing.T) {
	errs := ValidateSurface(surfaceOf(
		`{"id":"root","component":"Column","children":["a","b"]}`,
		`{"id":"a","component":"Text","text":42}`,
		`{"id":"b","component":"Metric","label":"x","value":true,"better":"left"}`,
	))
	if len(errs) != 3 {
		t.Fatalf("want 3 errors, got %d: %v", len(errs), errs)
	}
	for i, path := range []string{"/components/1/text", "/components/2/better", "/components/2/value"} {
		if errs[i].Path != path {
			t.Errorf("error %d at %s, want %s", i, errs[i].Path, path)
		}
	}
}

// Validate takes messages, alone or in order, and follows each surface
// through them; paths point into what it was given.
func TestValidateMessages(t *testing.T) {
	create := `{"version":"1.0","createSurface":{"surfaceId":"s1","catalogId":"` + CatalogID + `"}}`
	update := func(sid string, comps ...string) string {
		return `{"version":"1.0","updateComponents":{"surfaceId":"` + sid + `","components":[` + strings.Join(comps, ",") + `]}}`
	}
	data := `{"version":"1.0","updateDataModel":{"surfaceId":"s1","path":"/sales","value":{"total":3}}}`
	del := `{"version":"1.0","deleteSurface":{"surfaceId":"s1"}}`

	ok := "[" + strings.Join([]string{create, update("s1", `{"id":"root","component":"Card","child":"m"}`), update("s1", `{"id":"m","component":"Metric","label":"a","value":{"@path":"/sales/total"}}`), data}, ",") + "]"
	if errs := Validate([]byte(ok)); len(errs) > 0 {
		t.Fatalf("valid stream: %v", errs)
	}
	if errs := Validate([]byte(del)); len(errs) > 0 {
		t.Fatalf("deleteSurface: %v", errs)
	}

	cases := []struct {
		name, raw, path, words string
	}{
		{"root in no message", "[" + create + "," + update("s1", `{"id":"m","component":"Divider"}`) + "]", "/1/updateComponents/components", `no component has the id "root"`},
		{"deleted then updated", "[" + create + "," + update("s1", `{"id":"root","component":"Card","child":"m"}`) + "," + del + "," + update("s1", `{"id":"m","component":"Divider"}`) + "]",
			"/3/updateComponents/components", `no component has the id "root"`},
		{"created twice", "[" + create + "," + create + "]", "/1/createSurface/surfaceId", "created twice"},
		{"another catalog", `{"version":"1.0","createSurface":{"surfaceId":"s1","catalogId":"https://example.org/c"}}`, "/createSurface/catalogId", `unknown catalog "https://example.org/c"`},
		{"no catalog", `{"version":"1.0","createSurface":{"surfaceId":"s1","components":[{"id":"root","component":"Divider"}]}}`, "/createSurface", "missing catalogId"},
		{"relative data path", `{"version":"1.0","updateDataModel":{"surfaceId":"s1","path":"sales","value":1}}`, "/updateDataModel/path", "must start with /"},
		{"two kinds", `{"version":"1.0","createSurface":{"surfaceId":"s1"},"deleteSurface":{"surfaceId":"s1"}}`, "", "exactly one of"},
		{"no kind", `{"version":"1.0"}`, "", "exactly one of"},
		{"no version", `{"deleteSurface":{"surfaceId":"s1"}}`, "", "missing property 'version'"},
		{"another version", `{"version":"0.9","deleteSurface":{"surfaceId":"s1"}}`, "/version", "must be '1.0'"},
		{"not an object", `[1]`, "/0", "a message is a JSON object"},
		{"not JSON", `{`, "", "not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Validate([]byte(tc.raw))
			assertA2UIErrors(t, errs)
			for _, e := range errs {
				if e.Path == tc.path && strings.Contains(e.Message, tc.words) {
					return
				}
			}
			t.Errorf("want %q at %q, got %v", tc.words, tc.path, errs)
		})
	}
}

// A catalog's composition constraints give UNALLOWED_PARENT and
// UNALLOWED_CHILD, the root's parent being the Surface.
func TestCompositionConstraints(t *testing.T) {
	c, err := LoadCatalog([]byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/menus.json",
  "protocolVersion": "1.0",
  "catalogId": "https://example.org/menus.json",
  "components": {
    "Layout": {"type": "object", "allowedParents": ["Surface"], "properties": {"component": {"const": "Layout"}, "children": {"$ref": "common_types.json#/$defs/ChildList"}}, "required": ["component", "children"]},
    "Menu": {"type": "object", "allowedChildren": ["Item"], "properties": {"component": {"const": "Menu"}, "children": {"$ref": "common_types.json#/$defs/ChildList"}}, "required": ["component", "children"]},
    "Item": {"type": "object", "allowedParents": ["Menu"], "properties": {"component": {"const": "Item"}, "label": {"type": "string"}}, "required": ["component"]}
  },
  "$defs": {
    "anyComponent": {"oneOf": [{"$ref": "#/components/Layout"}, {"$ref": "#/components/Menu"}, {"$ref": "#/components/Item"}]},
    "anyFunction": false
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	ok := Surface{ID: "m", Components: json.RawMessage(`[{"id":"root","component":"Layout","children":["menu"]},{"id":"menu","component":"Menu","children":["a"]},{"id":"a","component":"Item"}]`)}
	if errs := c.ValidateSurface(ok); len(errs) > 0 {
		t.Fatal(errs)
	}
	cases := []struct {
		name, comps, code, path string
	}{
		{"item outside a menu", `[{"id":"root","component":"Layout","children":["a"]},{"id":"a","component":"Item"}]`, CodeUnallowedParent, "/components/0/children/0"},
		{"menu holds a layout", `[{"id":"root","component":"Menu","children":["l"]},{"id":"l","component":"Layout","children":[]}]`, CodeUnallowedChild, "/components/0/children/0"},
		{"layout below the root", `[{"id":"root","component":"Layout","children":["l"]},{"id":"l","component":"Layout","children":[]}]`, CodeUnallowedParent, "/components/0/children/0"},
		{"item as the root", `[{"id":"root","component":"Item"}]`, CodeUnallowedParent, "/components/0/component"},
	}
	for _, tc := range cases {
		errs := c.ValidateSurface(Surface{ID: "m", Components: json.RawMessage(tc.comps)})
		found := false
		for _, e := range errs {
			found = found || (e.Code == tc.code && e.Path == tc.path)
		}
		if !found {
			t.Errorf("%s: want %s at %s, got %v", tc.name, tc.code, tc.path, errs)
		}
	}
}

// A validation error is a renderer-to-agent error message as A2UI defines it.
func TestValidationErrorIsAnA2UIError(t *testing.T) {
	errs := ValidateSurface(surfaceOf(`{"id":"root","component":"Text","text":42}`))
	if len(errs) != 1 {
		t.Fatal(errs)
	}
	raw, err := json.Marshal(errs[0].Event())
	if err != nil {
		t.Fatal(err)
	}
	if got := Eriphe().ValidateEvent(raw); len(got) > 0 {
		t.Errorf("%s: %v", raw, got)
	}
	want := `{"version":"1.0","error":{"code":"VALIDATION_FAILED","surfaceId":"s1","path":"/components/0/text","message":"got number, want string or object"}}`
	if string(raw) != want {
		t.Errorf("got  %s\nwant %s", raw, want)
	}
	if s := errs[0].Error(); !strings.Contains(s, "/components/0/text") {
		t.Errorf("Error(): %s", s)
	}
}

// assertA2UIErrors checks that each error, sent back as a renderer's error
// message, is one A2UI accepts.
func assertA2UIErrors(t *testing.T, errs []ValidationError) {
	t.Helper()
	for _, e := range errs {
		raw, err := json.Marshal(e.Event())
		if err != nil {
			t.Fatal(err)
		}
		if got := Eriphe().ValidateEvent(raw); len(got) > 0 {
			t.Errorf("%s is not an A2UI error message: %v", raw, got)
		}
	}
}

// An error at the root of what was given, with no surface, still has its
// path and surface fields; another error keeps a surface or a function
// call, not both.
func TestErrorShapes(t *testing.T) {
	errs := Validate([]byte(`{"version":"1.0"}`))
	if len(errs) != 1 || errs[0].Path != "" || errs[0].SurfaceID != "" {
		t.Fatal(errs)
	}
	assertA2UIErrors(t, errs)
	raw, _ := json.Marshal(errs[0].Event())
	if !strings.Contains(string(raw), `"surfaceId":"","path":""`) {
		t.Errorf("%s", raw)
	}
	for _, e := range []Error{
		{Code: "INVALID_FUNCTION_CALL", Message: "x", FunctionCallID: "c1"},
		{Code: "INTEGRITY_ERROR", Message: "x", SurfaceID: "s1"},
	} {
		raw, _ := json.Marshal(Event{Version: Version, Error: &e})
		if got := Eriphe().ValidateEvent(raw); len(got) > 0 {
			t.Errorf("%s: %v", raw, got)
		}
	}
}

func TestValidateEvent(t *testing.T) {
	c := Eriphe()
	action := `{"version":"1.0","action":{"name":"ask","surfaceId":"s1","sourceComponentId":"b","timestamp":"2026-11-12T08:00:00Z","context":{"text":"Relance"}}}`
	if errs := c.ValidateEvent([]byte(action)); len(errs) > 0 {
		t.Fatal(errs)
	}
	var e Event
	if err := json.Unmarshal([]byte(action), &e); err != nil || e.Action == nil || e.Action.Name != "ask" || string(e.Action.Context["text"]) != `"Relance"` {
		t.Fatalf("%v %+v", err, e)
	}
	for raw, path := range map[string]string{
		`{"version":"1.0","action":{"name":"ask","surfaceId":"s1","sourceComponentId":"b","context":{}}}`:                                       "/action",
		`{"version":"1.0","action":{"name":"ask","surfaceId":"s1","sourceComponentId":"b","timestamp":"hier","context":{}}}`:                    "/action/timestamp",
		`{"version":"1.0","error":{"code":"VALIDATION_FAILED","surfaceId":"s1","message":"x"}}`:                                                 "/error",
		`{"version":"1.0","action":{"name":"a","surfaceId":"s","sourceComponentId":"b","timestamp":"2026-11-12T08:00:00Z","context":{}},"x":1}`: "",
	} {
		errs := c.ValidateEvent([]byte(raw))
		if len(errs) == 0 || errs[0].Path != path {
			t.Errorf("%s: want an error at %q, got %v", raw, path, errs)
		}
	}
}

// The Go types read and write the messages as they are.
func TestMessageTypesRoundTrip(t *testing.T) {
	lines := []string{
		`{"version":"1.0","createSurface":{"surfaceId":"s1","catalogId":"` + CatalogID + `","sendDataModel":true,"components":[{"id":"root","component":"List","children":{"path":"/mails","componentId":"m"},"@accessibility":{"label":"Mails"}},{"id":"m","component":"Message","from":{"@path":"from"}}],"dataModel":{"mails":[{"from":"Sam"}]}}}`,
		`{"version":"1.0","updateComponents":{"surfaceId":"s1","components":[{"id":"root","component":"Row","children":["a","b"]}]}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"s1","path":"/mails/0","value":null}}`,
		`{"version":"1.0","deleteSurface":{"surfaceId":"s1"}}`,
	}
	for _, line := range lines {
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		json.Unmarshal([]byte(line), &a)
		json.Unmarshal(out, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("round trip:\n in  %s\n out %s", line, out)
		}
	}
	var m Message
	json.Unmarshal([]byte(lines[0]), &m)
	root := m.CreateSurface.Components[0]
	var children ChildList
	if err := json.Unmarshal(root.Props["children"], &children); err != nil || children.Template == nil || children.Template.ComponentID != "m" || children.Template.Path != "/mails" {
		t.Errorf("template: %v %+v", err, children)
	}
	var b Binding
	if err := json.Unmarshal(m.CreateSurface.Components[1].Props["from"], &b); err != nil || b.Path != "from" {
		t.Errorf("binding: %v %+v", err, b)
	}
	json.Unmarshal([]byte(lines[1]), &m)
	if err := json.Unmarshal(m.UpdateComponents.Components[0].Props["children"], &children); err != nil || children.Template != nil || fmt.Sprint(children.IDs) != "[a b]" {
		t.Errorf("ids: %v %+v", err, children)
	}
	for _, l := range []ChildList{{IDs: []string{"a"}}, {Template: &Template{Path: "/x", ComponentID: "t"}}, {}} {
		raw, _ := json.Marshal(l)
		var back ChildList
		if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back.Template, l.Template) || len(back.IDs) != len(l.IDs) {
			t.Errorf("%s: %+v", raw, back)
		}
	}
	var call FunctionCall
	if err := json.Unmarshal([]byte(`{"@call":"formatNumber","args":{"value":{"@path":"/n"}}}`), &call); err != nil || call.Call != "formatNumber" || string(call.Args["value"]) != `{"@path":"/n"}` {
		t.Errorf("call: %v %+v", err, call)
	}
}
