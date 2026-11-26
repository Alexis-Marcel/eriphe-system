package nodefile

import (
	"fmt"
	"math"
	"regexp"
	"slices"
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

// GlanceOpt is the callout option, and the property, that shows a
// page's figure at a glance: glance 3% (in percent of the day's first
// value), glance 500 (in the figure's units), previous after it to
// compare with the value before the last change.
const GlanceOpt = "glance"

// ParseGlance reads a glance's threshold as the option or the property
// writes it (3%, 500, 3% previous): ok false when it does not read.
func ParseGlance(s string) (Glance, bool) {
	f := strings.Fields(strings.ToLower(s))
	if len(f) == 0 || len(f) > 2 {
		return Glance{}, false
	}
	g := Glance{Unit: GlanceAmount}
	num := f[0]
	if t, ok := strings.CutSuffix(num, "%"); ok {
		num, g.Unit = t, GlancePercent
	}
	v, err := strconv.ParseFloat(strings.Replace(num, ",", ".", 1), 64)
	if err != nil || !(v > 0) || math.IsInf(v, 0) {
		return Glance{}, false
	}
	g.Threshold = v
	if len(f) == 2 {
		switch f[1] {
		case GlanceDay, GlancePrevious:
			g.Compare = f[1]
		default:
			return Glance{}, false
		}
	}
	return g, true
}

// LineGlance is the figure a page shows at a glance: the first number
// written by a line that follows events (on:) in the callout marked
// glance <threshold>, else in the page when its property glance says the
// threshold.
type LineGlance struct {
	Glance   Glance // its label (the line's words before its value), threshold, unit, compare
	Value    float64
	Target   string // the line's on: target
	External bool   // its callout is marked external, or the node is
}

// LineGlance is the page's glance; ok false when it declares none, or
// its line writes no number.
func (n *Node) LineGlance() (LineGlance, bool) {
	p, err := n.Page()
	if err != nil {
		return LineGlance{}, false
	}
	body, _ := n.Body()
	lines, _ := splitBody(body)
	pick := func(from, to int, g Glance, ext bool) (LineGlance, bool) {
		for _, l := range p.Listens() {
			if l.Line < from || l.Line > to {
				continue
			}
			v, ok := NumberIn(l.Label)
			if !ok {
				continue
			}
			g.Label = glanceLabel(lines[l.Line-1], l)
			if g.Label == "" {
				g.Label = n.Meta.Title
			}
			if utf8.RuneCountInString(g.Label) > GlanceLabelMax {
				g.Label = strings.TrimSpace(string([]rune(g.Label)[:GlanceLabelMax-1])) + "…"
			}
			e := ext || n.Meta.External
			if b := calloutAt(p.Blocks, l.Callout); !e && b != nil {
				e = slices.Contains(b.Opts, OptExternal)
			}
			return LineGlance{Glance: g, Value: v, Target: l.Target, External: e}, true
		}
		return LineGlance{}, false
	}
	// A callout marked glance first.
	var found *PageBlock
	var g Glance
	var walk func(bs []PageBlock)
	walk = func(bs []PageBlock) {
		for i := range bs {
			b := &bs[i]
			if found != nil {
				return
			}
			if b.Kind == KindCallout {
				for _, o := range b.Opts {
					if t, ok := strings.CutPrefix(o, GlanceOpt+" "); ok {
						if g, ok = ParseGlance(t); ok {
							found = b
							return
						}
					}
				}
			}
			walk(b.Body)
			for _, it := range b.Items {
				walk(it.Detail)
			}
		}
	}
	walk(p.Blocks)
	if found != nil {
		return pick(found.Line, found.End, g, slices.Contains(found.Opts, OptExternal))
	}
	for _, pr := range n.Meta.Props() {
		if !strings.EqualFold(pr.Key, GlanceOpt) || pr.List || len(pr.Values) != 1 {
			continue
		}
		if g, ok := ParseGlance(pr.Values[0].Text); ok {
			return pick(1, len(lines), g, false)
		}
	}
	return LineGlance{}, false
}

// calloutAt is the callout whose first line is ln.
func calloutAt(bs []PageBlock, ln int) *PageBlock {
	for i := range bs {
		b := &bs[i]
		if b.Kind == KindCallout && b.Line == ln {
			return b
		}
		if c := calloutAt(b.Body, ln); c != nil {
			return c
		}
		for _, it := range b.Items {
			if c := calloutAt(it.Detail, ln); c != nil {
				return c
			}
		}
	}
	return nil
}

// glanceLabelTrim are what a label's words end with before their value.
const glanceLabelTrim = " \t:–—-·*_"

// glanceLabel is the words a line writes before its on: link: its >,
// list marker and box off (« - PEA : [18 385 €](on:…) » gives PEA).
func glanceLabel(t string, l PageLink) string {
	for _, k := range inlineTokens(t) {
		if k.kind == LinkOn && k.target == l.Target {
			t = t[:k.start]
			break
		}
	}
	t = lineCore(t)
	if m := pageItemRe.FindStringSubmatch(t); m != nil {
		t = m[3]
	}
	if m := pageTaskRe.FindStringSubmatch(t); m != nil {
		t = m[2]
	}
	return strings.Trim(t, glanceLabelTrim)
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
