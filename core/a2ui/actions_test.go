package a2ui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// An Asks says what each ask asks: a bare « Oui » is refused (a yes is a
// card's); its asks are read again by their place, « root#1 », and a
// place that is not an ask, or a « # » on another component, is none.
func TestAsks(t *testing.T) {
	comps := json.RawMessage(`[{"id":"root","component":"Asks","items":["Fais comme tu proposes",{"text":"Branche l'agenda","until":{"tool":"calendar"}},""]},{"id":"b","component":"Button","label":"x","action":{"event":{"name":"ask","context":{"text":"Vas-y"}}}}]`)
	if errs := CheckActions("block:x", comps); len(errs) > 0 {
		t.Errorf("asks that say what they ask: %+v", errs)
	}
	for _, bare := range []string{"Oui", " oui ", "OK", "D'accord", "Yes", "no"} {
		errs := CheckActions("block:x", json.RawMessage(`[{"id":"root","component":"Asks","items":["Fais-le",`+strconv.Quote(bare)+`]}]`))
		if len(errs) != 1 || errs[0].Path != "/components/0/items/1" || !strings.Contains(errs[0].Message, "asks nothing") {
			t.Errorf("%q: %+v", bare, errs)
		}
	}
	for id, want := range map[string]string{"root#0": "Fais comme tu proposes", "root#1": "Branche l'agenda", "b": "Vas-y"} {
		if got, ok := AskText(comps, id); !ok || got != want {
			t.Errorf("AskText(%q) = %q, %v; want %q", id, got, ok, want)
		}
	}
	for _, id := range []string{"root", "root#2", "root#3", "root#-1", "root#x", "b#0", "nope#0"} {
		if got, ok := AskText(comps, id); ok {
			t.Errorf("AskText(%q) = %q: an ask", id, got)
		}
	}
}

// A button only asks: any other name, marks and answers included, is
// refused, and so are the Mark and Proposal components.
func TestCheckActionsAskOnly(t *testing.T) {
	for _, name := range []string{"mark", "answer", "expand", "tab", "offer", "open"} {
		button := json.RawMessage(`[{"id":"root","component":"Button","label":"x","action":{"event":{"name":"` + name + `","context":{"text":"x"}}}}]`)
		errs := CheckActions("block:x", button)
		if len(errs) != 1 || errs[0].Path != "/components/0/action/event/name" || !strings.Contains(errs[0].Message, "only asks") {
			t.Errorf("%s on a Button: %+v", name, errs)
		}
	}
	ask := json.RawMessage(`[{"id":"root","component":"Button","label":"x","action":{"event":{"name":"ask","context":{"text":"Fais comme tu proposes"}}}}]`)
	if errs := CheckActions("block:x", ask); len(errs) > 0 {
		t.Errorf("an ask: %+v", errs)
	}
	for _, comp := range []string{`{"id":"root","component":"Mark","list":"options","item":"a","marks":["keep"]}`, `{"id":"root","component":"Proposal","proposalId":"view:3"}`} {
		if errs := ValidateSurface(surfaceOf(comp)); len(errs) == 0 {
			t.Errorf("%s: accepted", comp)
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
