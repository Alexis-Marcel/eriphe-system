package nodefile

import "testing"

// The variation is from its reference, in percent or in its units.
func TestGlanceVariation(t *testing.T) {
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
	for g, want := range map[Glance]float64{{}: 100, {Compare: GlanceDay}: 100, {Compare: GlancePrevious}: 102} {
		if ref := g.Reference(100, 102); ref != want {
			t.Errorf("%+v: reference %v, want %v", g, ref, want)
		}
	}
}
