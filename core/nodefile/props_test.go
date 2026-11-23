package nodefile

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// A node's properties are read from its front matter as written, typed by
// their YAML: an unquoted date and a quoted one are both dates.
func TestPropsRead(t *testing.T) {
	raw := "---\nid: a1\ntitle: Tarte\nlifetime: work\ncreated: 2026-11-17T09:00:00Z\n" +
		"tags: [cuisine/recette, \"#rapide\"]\ntemps: 15\nnote: 4.5\ncuit: 2026-10-01\nprevu: \"2026-12-24\"\n" +
		"chef: [[Sam]]\nami: \"[[Sam]]\"\nbio: true\nvide:\nreste: {a: 1}\nmots: [sucre, 3]\n---\nText.\n"
	n, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	kinds := map[string]string{}
	for _, p := range n.Meta.Props() {
		got[p.Key] = p.Any()
		kinds[p.Key] = p.Values[0].Kind
	}
	want := map[string]any{"tags": []any{"cuisine/recette", "#rapide"}, "temps": 15.0, "note": 4.5, "cuit": "2026-10-01", "prevu": "2026-12-24",
		"chef": "[[Sam]]", "ami": "[[Sam]]", "bio": "true", "mots": []any{"sucre", 3.0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("props %#v", got)
	}
	if kinds["temps"] != KindNumber || kinds["cuit"] != KindDate || kinds["prevu"] != KindDate || kinds["chef"] != KindText {
		t.Fatalf("kinds %v", kinds)
	}
	if tags := n.Meta.Tags(); !reflect.DeepEqual(tags, []string{"cuisine/recette", "rapide"}) {
		t.Fatalf("tags %v", tags)
	}
	// Read, it writes the same bytes.
	if out, _ := n.Bytes(); string(out) != raw {
		t.Fatalf("not lossless:\n%s", out)
	}
}

func TestSetProp(t *testing.T) {
	n, err := New(Meta{ID: "a1", Title: "Tarte", Lifetime: Work, Created: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}, "Text.\n")
	if err != nil {
		t.Fatal(err)
	}
	m := &n.Meta
	for k, v := range map[string]any{"temps": 15.0, "prix": 4.5, "cuit": "2026-10-01", "chef": "[[Sam]]", "tags": []any{"#cuisine/recette", "rapide"}, "bio": true} {
		if err := m.SetProp(k, v); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if err := m.SetProp("Temps", 20.0); err != nil { // the same name, without case
		t.Fatal(err)
	}
	if err := m.SetProp("prix", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := n.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(raw)
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	got := map[string]any{}
	for _, p := range back.Meta.Props() {
		got[p.Key] = p.Any()
		if p.Key == "cuit" && p.Values[0].Kind != KindDate {
			t.Errorf("cuit is %s", p.Values[0].Kind)
		}
	}
	want := map[string]any{"temps": 20.0, "cuit": "2026-10-01", "chef": "[[Sam]]", "tags": []any{"cuisine/recette", "rapide"}, "bio": "true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("props %#v\n%s", got, raw)
	}
	if !strings.Contains(string(raw), "temps: 20\n") || !strings.Contains(string(raw), "tags: [cuisine/recette, rapide]\n") {
		t.Fatalf("written:\n%s", raw)
	}
	for _, tc := range []struct {
		key string
		v   any
	}{
		{"title", "x"}, {"Links", "x"}, {"_note", "x"}, {"a:b", "x"}, {"", "x"},
		{"tags", []any{"deux mots"}}, {"tags", []any{"a//b"}}, {"x", map[string]any{"a": 1}},
		{"x", strings.Repeat("a", PropTextMax+1)}, {"x", "two\nlines"}, {"x", ""},
	} {
		if err := m.SetProp(tc.key, tc.v); err == nil {
			t.Errorf("%q = %v: not refused", tc.key, tc.v)
		}
	}
}
