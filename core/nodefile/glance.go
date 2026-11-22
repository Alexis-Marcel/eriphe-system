package nodefile

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Glance is a figure a source's value holds that its node shows at a
// glance (docs/node-format.md): when the figure moved past Threshold from
// its reference, the node's bubble becomes a small tile with Label and
// the variation, until the user opens the node. Code reads the figure at
// Path in the source's value at each read, without a model; only a JSON
// number counts, so no one else's words reach the tile.
type Glance struct {
	Label string `json:"label"`
	// Path is where the figure is in the source's value: plain fields
	// joined by dots, a list's index as a number (total, positions.0.value).
	Path string `json:"path"`
	// Compare is the reference of the variation: GlanceDay (by default),
	// the figure's first value read on the user's day; GlancePrevious, its
	// value before it last changed; GlanceFixed, Base.
	Compare string   `json:"compare,omitempty"`
	Base    *float64 `json:"base,omitempty"`
	// Threshold is how far the figure must move for its node to show it:
	// in percent of the reference (Unit GlancePercent, by default), or in
	// the figure's own units (GlanceAmount).
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit,omitempty"`
}

// The references a glance compares its figure to, and its units.
const (
	GlanceDay      = "day"
	GlancePrevious = "previous"
	GlanceFixed    = "fixed"
	GlancePercent  = "percent"
	GlanceAmount   = "amount"
)

// GlanceLabelMax is the most runes of a glance's label.
const GlanceLabelMax = 24

// GlancePathRe is a glance's path.
var GlancePathRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}(\.[A-Za-z0-9_]{1,64}){0,5}$`)

// Reference is what the glance compares its figure to: the day's first
// value (day), the value before the last change (previous), or its base.
func (g Glance) Reference(day, previous float64) float64 {
	switch g.Compare {
	case GlancePrevious:
		return previous
	case GlanceFixed:
		if g.Base != nil {
			return *g.Base
		}
	}
	return day
}

// Variation is how far v moved from ref, in the glance's unit: percent of
// ref, or ref's own units; 0 when it cannot say (a reference of 0 in
// percent).
func (g Glance) Variation(v, ref float64) float64 {
	d := v - ref
	if g.Unit != GlanceAmount {
		if ref == 0 {
			return 0
		}
		d = d / math.Abs(ref) * 100
	}
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return 0
	}
	return d
}

// Figure is the glance's figure in a source's value: the number at its
// path; ok false when there is none, or it is not a number.
func (g Glance) Figure(v any) (float64, bool) {
	for _, f := range strings.Split(g.Path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[f]
		case []any:
			i, err := strconv.Atoi(f)
			if err != nil || i < 0 || i >= len(x) {
				return 0, false
			}
			v = x[i]
		default:
			return 0, false
		}
	}
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

// check is what is wrong with a source's glance; "" when nothing.
func (g Glance) check(source string) string {
	at := "source " + source + ": glance "
	switch {
	case strings.TrimSpace(g.Label) == "" || utf8.RuneCountInString(g.Label) > GlanceLabelMax || strings.ContainsAny(g.Label, "\n\r"):
		return fmt.Sprintf("%slabel is one line, %d characters at most: the figure's name (« PEA »)", at, GlanceLabelMax)
	case !GlancePathRe.MatchString(g.Path):
		return at + "path is where its figure is in the source's value: plain fields joined by dots, a list's index as a number (total, positions.0.value)"
	case g.Compare != "" && g.Compare != GlanceDay && g.Compare != GlancePrevious && g.Compare != GlanceFixed:
		return at + "compare is day (by default), previous or fixed"
	case g.Compare == GlanceFixed && (g.Base == nil || math.IsNaN(*g.Base) || math.IsInf(*g.Base, 0)):
		return at + "compare fixed needs base, the value it compares to"
	case g.Compare != GlanceFixed && g.Base != nil:
		return at + "base goes with compare fixed"
	case g.Unit != "" && g.Unit != GlancePercent && g.Unit != GlanceAmount:
		return at + "unit is percent (by default) or amount"
	case g.Compare == GlanceFixed && g.Unit != GlanceAmount && *g.Base == 0:
		return at + "a variation in percent of a base of 0 says nothing: give unit amount"
	case !(g.Threshold > 0) || math.IsInf(g.Threshold, 0):
		return at + "threshold is how far the figure must move to show, more than 0"
	}
	return ""
}
