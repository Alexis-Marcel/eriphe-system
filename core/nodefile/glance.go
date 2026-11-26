package nodefile

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Glance is a figure of a page its node shows at a glance
// (docs/node-format.md): when the figure moved past Threshold from its
// reference, the node's bubble becomes a small tile with Label and the
// variation, until the user opens the node. The figure is a line that
// follows events (LineGlance); code reads it at each change of the file,
// without a model.
type Glance struct {
	Label string `json:"label"`
	// Compare is the reference of the variation: GlanceDay (by default),
	// the figure's first value read on the user's day; GlancePrevious, its
	// value before it last changed.
	Compare string `json:"compare,omitempty"`
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
	GlancePercent  = "percent"
	GlanceAmount   = "amount"
)

// GlanceLabelMax is the most runes of a glance's label.
const GlanceLabelMax = 24

// Reference is what the glance compares its figure to: the day's first
// value (day), or the value before the last change (previous).
func (g Glance) Reference(day, previous float64) float64 {
	if g.Compare == GlancePrevious {
		return previous
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
