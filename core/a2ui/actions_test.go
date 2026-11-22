package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

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
