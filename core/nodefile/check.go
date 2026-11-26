package nodefile

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The page check: what a page's author got wrong, a convention misused
// that would change what the page shows without a word. A write is never
// refused for it (nothing is lost): its author is told and fixes it.

// Problem is a thing wrong in a page: its line, a stable code, and what
// it is, in words for its author.
type Problem struct {
	Line int    `json:"line"`
	Code string `json:"code"`
	What string `json:"what"`
}

// Problem codes.
const (
	ProblemRefUndefined   = "ref-undefined"   // [text][m1] with no [m1]: target
	ProblemRefUnused      = "ref-unused"      // [m1]: target no reference uses
	ProblemRefDuplicate   = "ref-duplicate"   // [m1]: defined twice
	ProblemCalloutType    = "callout-type"    // > [!type] Eriphe does not know
	ProblemCalloutOption  = "callout-option"  // > [!type|opt] an option it does not know
	ProblemDateImpossible = "date-impossible" // 📅 2026-02-30
	ProblemDateMalformed  = "date-malformed"  // 📅 27/11
	ProblemDoPlace        = "do-place"        // do: not alone right under a heading
	ProblemDoTarget       = "do-target"       // do: not server/tool
	ProblemDoTool         = "do-tool"         // do: a tool its author does not have
	ProblemLinkTarget     = "link-target"     // card: or on: with nothing after it
	ProblemChart          = "chart"           // [!chart] whose table is not one it draws
	ProblemFenceOpen      = "fence-open"      // a code fence never closed
)

// Callout types Eriphe draws, and the options each takes besides aside.
var calloutOpts = map[string][]string{
	"figures": nil,
	"chart":   {"line", "bar", "stack"},
	"steps":   nil,
	"agenda":  nil,
	"person":  nil,
	"warning": nil,
	"info":    nil,
	"success": nil,
	"note":    nil,
}

// CalloutTypes are the callout types Eriphe draws.
func CalloutTypes() []string {
	out := make([]string, 0, len(calloutOpts))
	for t := range calloutOpts {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// OptAside puts a callout in the page's side column.
const OptAside = "aside"

var (
	dateMarkRe = regexp.MustCompile(`📅`)
	isoDateRe  = regexp.MustCompile(`^\s*(\d{4}-\d{2}-\d{2})`)
	numberRe   = regexp.MustCompile(`^[+−-]?\s*\d[\d \x{00a0}\x{202f}]*(?:[,.]\d+)?$`)
)

// Check is what is wrong in the page. hasTool tells whether the page's
// author has a tool (server/tool) a do: link names; nil checks none.
func (p *Page) Check(hasTool func(tool string) bool) []Problem {
	out := []Problem{}
	add := func(ln int, code, what string, args ...any) {
		out = append(out, Problem{Line: ln, Code: code, What: fmt.Sprintf(what, args...)})
	}
	// References.
	defined := map[string]bool{}
	used := map[string]bool{}
	for _, u := range p.Uses {
		used[u.Label] = true
	}
	for _, d := range p.Refs {
		if defined[d.Label] {
			add(d.Line, ProblemRefDuplicate, "the label [%s] is defined twice: each reference has a label of its own", d.Label)
			continue
		}
		defined[d.Label] = true
		if !used[d.Label] {
			add(d.Line, ProblemRefUnused, "no reference uses [%s]: remove its target with its line", d.Label)
		}
	}
	for _, u := range p.Uses {
		if !defined[u.Label] {
			add(u.Line, ProblemRefUndefined, "[%s][%s] has no target: write [%s]: <scheme>:<id> at the bottom, the id as the tool gave it", u.Text, u.Label, u.Label)
		}
	}
	// Dates, outside code.
	for i, t := range p.lines {
		if p.info[i].code || p.info[i].def {
			continue
		}
		t = blankCodeSpans(t)
		for _, at := range dateMarkRe.FindAllStringIndex(t, -1) {
			m := isoDateRe.FindStringSubmatch(t[at[1]:])
			switch {
			case m == nil:
				add(i+1, ProblemDateMalformed, "a date after 📅 is written YYYY-MM-DD (📅 2026-11-27)")
			case !realDate(m[1]):
				add(i+1, ProblemDateImpossible, "%s is not a date", m[1])
			}
		}
	}
	// Callouts and charts.
	var walk func(bs []PageBlock)
	walk = func(bs []PageBlock) {
		for _, b := range bs {
			switch b.Kind {
			case KindCallout:
				opts, known := calloutOpts[b.Type]
				if !known {
					add(b.Line, ProblemCalloutType, "[!%s] is not a callout Eriphe draws (%s): it is drawn as a note", b.Type, strings.Join(CalloutTypes(), ", "))
				}
				for _, o := range b.Opts {
					if o != OptAside && !slices.Contains(opts, o) {
						add(b.Line, ProblemCalloutOption, "[!%s] takes no option %q", b.Type, o)
					}
				}
				if b.Type == "chart" {
					p.checkChart(b, add)
				}
				walk(b.Body)
			case KindList:
				for _, it := range b.Items {
					walk(it.Detail)
				}
			}
		}
	}
	walk(p.Blocks)
	// Links.
	for _, l := range p.Links {
		switch l.Kind {
		case LinkDo:
			if _, ok := p.Scope(l); !ok {
				add(l.Line, ProblemDoPlace, "[%s](do:%s) acts on the references of a heading: on its own line, right under the heading", l.Label, l.Target)
			}
			server, tool, _ := strings.Cut(l.Target, "/")
			switch {
			case server == "" || tool == "":
				add(l.Line, ProblemDoTarget, "do: names a tool as server/tool (do:mail/trash), not %q", l.Target)
			case hasTool != nil && !hasTool(l.Target):
				add(l.Line, ProblemDoTool, "%s is not a tool you have: its card could not be composed", l.Target)
			}
		case LinkCard, LinkOn:
			if strings.TrimSpace(l.Target) == "" {
				add(l.Line, ProblemLinkTarget, "%s: names what it places (%s:<ref>)", l.Kind, l.Kind)
			}
		}
	}
	for _, ln := range p.open {
		add(ln, ProblemFenceOpen, "this code block is never closed: the rest of the page is read as code")
	}
	slices.SortStableFunc(out, func(a, b Problem) int { return a.Line - b.Line })
	return out
}

// blankCodeSpans is a line whose code spans are spaces: nothing in them
// is read.
func blankCodeSpans(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(b) && b[i+n] == '`' {
			n++
		}
		close := strings.Index(string(b[i+n:]), strings.Repeat("`", n))
		if close < 0 {
			i += n
			continue
		}
		end := i + n + close + n
		for k := i; k < end; k++ {
			b[k] = ' '
		}
		i = end
	}
	return string(b)
}

// realDate tells whether YYYY-MM-DD is a day of the calendar.
func realDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// checkChart checks a [!chart]'s table: first column the x, a column per
// series, every series cell a number (or empty).
func (p *Page) checkChart(b PageBlock, add func(int, string, string, ...any)) {
	var table *PageBlock
	for i := range b.Body {
		if b.Body[i].Kind == KindTable {
			table = &b.Body[i]
			break
		}
	}
	switch {
	case table == nil:
		add(b.Line, ProblemChart, "[!chart] draws a table: | x | Series (unit) |, a row per point")
		return
	case len(table.Head) < 2:
		add(table.Line, ProblemChart, "a chart's table has its x first, then a column per series")
		return
	case len(table.Rows) == 0:
		add(table.Line, ProblemChart, "a chart's table has a row per point")
		return
	}
	for r, row := range table.Rows {
		for _, c := range row[1:] {
			if c != "" && !Number(c) {
				add(table.Line+2+r, ProblemChart, "%q is not a number: a series' cells are numbers, the unit in the header", c)
			}
		}
	}
}

// Number tells whether s is a number as a page writes it: 18 385, 1,8,
// −2, 3.5.
func Number(s string) bool {
	s = strings.TrimSpace(s)
	if !numberRe.MatchString(s) {
		return false
	}
	_, err := strconv.ParseFloat(strings.NewReplacer(" ", "", " ", "", " ", "", ",", ".", "−", "-").Replace(s), 64)
	return err == nil
}

// Section is the part of a page a heading starts: to the next heading of
// its level or a higher one.
type Section struct {
	Heading int // the heading's line
	Level   int
	End     int // its last line
	// Uses are the references written in it, with the state of the item
	// each is on.
	Uses []RefUse
}

// Scope is the section a do: link acts on, when it is placed right: on
// its own line, the first one written under a heading of the page (not
// in a list nor a callout). ok is false otherwise: then it acts on
// nothing.
func (p *Page) Scope(l PageLink) (Section, bool) {
	if l.Kind != LinkDo || !l.Alone || l.Heading == 0 || l.Item != 0 || l.Callout != 0 {
		return Section{}, false
	}
	for ln := l.Heading + 1; ln < l.Line; ln++ {
		if !blank(p.lines[ln-1]) {
			return Section{}, false
		}
	}
	h := p.Heading(l.Heading)
	if h == nil {
		return Section{}, false
	}
	s := Section{Heading: h.Line, Level: h.Level, End: len(p.lines)}
	for _, b := range p.Blocks {
		if b.Kind == KindHeading && b.Line > h.Line && b.Level <= h.Level {
			s.End = b.Line - 1
			break
		}
	}
	for _, u := range p.Uses {
		if u.Line > h.Line && u.Line <= s.End {
			s.Uses = append(s.Uses, u)
		}
	}
	return s, true
}
