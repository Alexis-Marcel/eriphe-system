package nodefile

import (
	"strings"
	"testing"
)

// A source's glance is declared beside it, checked when the block is read:
// a label, where its figure is, a reference, a threshold, a unit.
func TestGlanceDeclared(t *testing.T) {
	block := func(glance string) []byte {
		return []byte(head("Le PEA.\n\n```eriphe:block pea\n" +
			`{"components": [{"id": "root", "component": "Text", "text": {"@path": "/pea/label"}}], "sources": {"pea": {"tool": "bourse/portefeuille", "glance": ` + glance + `}}}` +
			"\n```\n"))
	}
	n, err := Parse(block(`{"label": "PEA", "path": "total", "threshold": 2}`))
	if err != nil {
		t.Fatal(err)
	}
	g := n.Block("pea").Sources["pea"].Glance
	if g == nil || g.Label != "PEA" || g.Path != "total" || g.Threshold != 2 || g.Compare != "" || g.Unit != "" {
		t.Fatalf("glance %+v", g)
	}
	if raw, _ := n.Bytes(); !strings.Contains(string(raw), `"glance": {"label": "PEA", "path": "total", "threshold": 2}`) {
		t.Errorf("not kept as written:\n%s", raw)
	}
	if _, err := Parse(block(`{"label": "Erreurs", "path": "rates.0.v", "compare": "fixed", "base": 10, "threshold": 5, "unit": "amount"}`)); err != nil {
		t.Errorf("a fixed base in amount: %v", err)
	}
	for _, c := range []struct{ glance, want string }{
		{`{"path": "total", "threshold": 2}`, "label is one line"},
		{`{"label": "Un nom bien trop long pour une tuile", "path": "total", "threshold": 2}`, "label is one line"},
		{`{"label": "PEA", "path": "/total", "threshold": 2}`, "path is where its figure is"},
		{`{"label": "PEA", "path": "a.b.c.d.e.f.g", "threshold": 2}`, "path is where its figure is"},
		{`{"label": "PEA", "path": "total", "threshold": 0}`, "threshold is how far"},
		{`{"label": "PEA", "path": "total", "threshold": -1}`, "threshold is how far"},
		{`{"label": "PEA", "path": "total", "threshold": 2, "compare": "week"}`, "compare is day"},
		{`{"label": "PEA", "path": "total", "threshold": 2, "compare": "fixed"}`, "compare fixed needs base"},
		{`{"label": "PEA", "path": "total", "threshold": 2, "base": 3}`, "base goes with compare fixed"},
		{`{"label": "PEA", "path": "total", "threshold": 2, "compare": "fixed", "base": 0}`, "a base of 0"},
		{`{"label": "PEA", "path": "total", "threshold": 2, "unit": "euros"}`, "unit is percent"},
		{`{"label": "PEA", "path": "total", "threshold": 2, "good": "up"}`, "unknown field"},
	} {
		if _, err := Parse(block(c.glance)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.glance, err, c.want)
		}
	}
}

// Its figure is a number at its path, nothing else: no words reach the
// tile. The variation is from its reference, in percent or in its units.
func TestGlanceFigure(t *testing.T) {
	value := map[string]any{"total": 103.5, "label": "103,5 €", "rates": []any{map[string]any{"v": 4.0}}, "bad": "12"}
	for path, want := range map[string]float64{"total": 103.5, "rates.0.v": 4} {
		if v, ok := (Glance{Path: path}).Figure(value); !ok || v != want {
			t.Errorf("%s: %v %v", path, v, ok)
		}
	}
	for _, path := range []string{"label", "bad", "rates.1.v", "rates.x", "missing", "total.more"} {
		if v, ok := (Glance{Path: path}).Figure(value); ok {
			t.Errorf("%s: a figure %v", path, v)
		}
	}
	pct, amount := Glance{Threshold: 2}, Glance{Threshold: 5, Unit: GlanceAmount}
	if d := pct.Variation(103, 100); d < 2.999 || d > 3.001 {
		t.Errorf("percent: %v", d)
	}
	if d := pct.Variation(-110, -100); d < -10.001 || d > -9.999 {
		t.Errorf("percent of a negative reference: %v", d)
	}
	if d := pct.Variation(5, 0); d != 0 {
		t.Errorf("percent of 0: %v", d)
	}
	if d := amount.Variation(12, 4); d != 8 {
		t.Errorf("amount: %v", d)
	}
	base := 50.0
	for g, want := range map[Glance]float64{{}: 100, {Compare: GlanceDay}: 100, {Compare: GlancePrevious}: 102, {Compare: GlanceFixed, Base: &base}: 50} {
		if ref := g.Reference(100, 102); ref != want {
			t.Errorf("%+v: reference %v, want %v", g, ref, want)
		}
	}
}
