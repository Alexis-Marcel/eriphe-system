package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

// An author never writes mark nor offer on a Button: the Mark and Offers
// components send them.
func TestCheckActionsRefusesThreadActions(t *testing.T) {
	for name, want := range map[string]string{"mark": "write a Mark", "offer": "write an Offers"} {
		button := json.RawMessage(`[{"id":"root","component":"Button","label":"x","action":{"event":{"name":"` + name + `","context":{"list":"options","item":"a","mark":"keep"}}}}]`)
		errs := CheckActions("thread-12", 12, button)
		if len(errs) != 1 || errs[0].Path != "/components/0/action/event/name" || !strings.Contains(errs[0].Message, want) {
			t.Errorf("%s on a Button: %+v", name, errs)
		}
	}
}

// Mark and Offers are on a thread's surface only.
func TestCheckActionsThreadOnly(t *testing.T) {
	comps := json.RawMessage(`[{"id":"root","component":"Column","children":["m","o"]},
		{"id":"m","component":"Mark","list":"options","item":"a","marks":["keep"]},
		{"id":"o","component":"Offers","offers":{"@path":"/thread/items/mails/groups/0/offers"}}]`)
	if errs := CheckActions("thread-12", 12, comps); len(errs) > 0 {
		t.Errorf("on a thread's surface: %+v", errs)
	}
	errs := CheckActions("q-x", 0, comps)
	if len(errs) != 2 || errs[0].Path != "/components/1" || errs[1].Path != "/components/2" || !strings.Contains(errs[0].Message, "thread's surface only") {
		t.Errorf("on no thread's surface: %+v", errs)
	}
}

// Mark's shape: list and item each an id or a binding, never text nor a
// function call; the marks it offers, one to three of keep, drop, done.
func TestMarkShape(t *testing.T) {
	mark := func(props string) Surface {
		return surfaceOf(`{"id":"root","component":"Mark",` + props + `}`)
	}
	for _, ok := range []string{
		`"list":"options","item":"a","marks":["keep","drop"]`,
		`"list":"options","item":{"@path":"/id"},"marks":["done"]`,
		`"list":{"@path":"/list"},"item":"msg-18c3:1@x","marks":["keep","drop","done"]`,
	} {
		if errs := ValidateSurface(mark(ok)); len(errs) > 0 {
			t.Errorf("%s: %v", ok, errs)
		}
	}
	for name, bad := range map[string]string{
		"no marks":        `"list":"options","item":"a"`,
		"no mark":         `"list":"options","item":"a","marks":[]`,
		"another mark":    `"list":"options","item":"a","marks":["star"]`,
		"twice":           `"list":"options","item":"a","marks":["keep","keep"]`,
		"text":            `"list":"options","item":"garde tout","marks":["keep"]`,
		"a path":          `"list":"a/b","item":"a","marks":["keep"]`,
		"too long":        `"list":"options","item":"` + strings.Repeat("a", 129) + `","marks":["keep"]`,
		"a function call": `"list":"options","item":{"@call":"formatString","args":{"value":"x"}},"marks":["keep"]`,
		"no item":         `"list":"options","marks":["keep"]`,
		"an action":       `"list":"options","item":"a","marks":["keep"],"action":{"event":{"name":"ask","context":{"text":"x"}}}`,
		"a thread":        `"thread":12,"list":"options","item":"a","marks":["keep"]`,
	} {
		if errs := ValidateSurface(mark(bad)); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Offers binds code's offers, never a list written by its author.
func TestOffersShape(t *testing.T) {
	if errs := ValidateSurface(surfaceOf(`{"id":"root","component":"Offers","offers":{"@path":"/thread/items/mails/groups/0/offers"}}`)); len(errs) > 0 {
		t.Errorf("bound: %v", errs)
	}
	for name, bad := range map[string]string{
		"a literal list":  `"offers":[{"label":"Archiver","thread":1,"source":"mails","item":"a","offer":"0123456789ab"}]`,
		"a function call": `"offers":{"@call":"formatString","args":{"value":"x"}}`,
		"nothing":         ``,
	} {
		props := `"id":"root","component":"Offers"`
		if bad != "" {
			props += "," + bad
		}
		if errs := ValidateSurface(surfaceOf("{" + props + "}")); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Grid lays out children, a list or a template; Chart stacks its series.
func TestGridAndStack(t *testing.T) {
	for _, ok := range []Surface{
		surfaceOf(`{"id":"root","component":"Grid","children":["a","b"],"size":"small"}`, `{"id":"a","component":"Text","text":"A"}`, `{"id":"b","component":"Text","text":"B"}`),
		surfaceOf(`{"id":"root","component":"Grid","children":{"path":"/pieces","componentId":"piece"},"emptyText":"Rien"}`,
			`{"id":"piece","component":"Card","title":{"@path":"title"},"child":"img"}`, `{"id":"img","component":"Image","url":{"@path":"image"}}`),
		surfaceOf(`{"id":"root","component":"Chart","kind":"stack","unit":"%","series":[{"name":"Actions","points":[{"x":"Aujourd'hui","y":60},{"x":"Cible","y":50}]}]}`),
		surfaceOf(`{"id":"root","component":"Chart","kind":"stack","series":{"@path":"/allocation"}}`),
	} {
		if errs := ValidateSurface(ok); len(errs) > 0 {
			t.Errorf("%s: %v", ok.Components, errs)
		}
	}
	for name, bad := range map[string]Surface{
		"a grid with no children": surfaceOf(`{"id":"root","component":"Grid"}`),
		"a grid of another size":  surfaceOf(`{"id":"root","component":"Grid","children":[],"size":"huge"}`),
		"a grid's missing child":  surfaceOf(`{"id":"root","component":"Grid","children":["nope"]}`),
		"another kind":            surfaceOf(`{"id":"root","component":"Chart","kind":"pie","series":[{"name":"A","points":[{"x":"a","y":1}]}]}`),
		"a stack with no points":  surfaceOf(`{"id":"root","component":"Chart","kind":"stack","series":[{"name":"A"}]}`),
	} {
		if errs := ValidateSurface(bad); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}
