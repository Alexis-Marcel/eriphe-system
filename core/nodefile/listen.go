package nodefile

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// A line that follows the event journal: [18 385 €](on:monitor.changed/pea)
// is rewritten by code at each event of that kind and key, without a
// model (docs/node-format.md, "Lines that follow events").
//
//   - The target is <kind>/<key>#<field>: the event's kind, its key (the
//     events of any key when there is none), and where the value is in
//     its payload (plain fields joined by dots; value by default). A key
//     holds no space nor ), as a Markdown link's target does not.
//   - The number of the label is replaced, written as the author wrote
//     it (its decimals, its separators), what surrounds it kept (the
//     unit). A text value replaces the whole label: one line, cut short,
//     without what Markdown reads as a link or code. A list or an object
//     is no value of a line: a skill writes those.
//   - The date that follows the link (« · relu le 26/11 à 10 h 05 ») is
//     written again, added when there is none.
//   - An event written by someone else (external) marks the callout that
//     holds the line: its option external, code's only (AuthorBody refuses
//     an author's write that changes it), never taken off by an event.
//     Outside a callout, nothing could say whose words they are: only a
//     number is written there.

// EventKindRe is an event's kind: lowercase words joined by dots, two to
// four of them.
var EventKindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,3}$`)

// OnField is the payload's field a line follows when its target names
// none.
const OnField = "value"

// OptExternal marks a callout whose values someone else's event wrote:
// code's only.
const OptExternal = "external"

// OnTextMax is the most runes of a text value a line is given.
const OnTextMax = 80

var onFieldRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}(\.[A-Za-z0-9_]{1,64}){0,5}$`)

// On is what an on: link follows.
type On struct {
	Kind  string
	Key   string // "" for the events of any key
	Field string // where its value is in the payload
}

// ParseOn reads an on: link's target (what follows on:); ok is false when
// it does not name an event's kind, or its field is not a path.
func ParseOn(target string) (On, bool) {
	rest, field, hasField := strings.Cut(target, "#")
	kind, key, _ := strings.Cut(rest, "/")
	o := On{Kind: kind, Key: key, Field: OnField}
	if hasField {
		o.Field = field
	}
	if !EventKindRe.MatchString(o.Kind) || !onFieldRe.MatchString(o.Field) {
		return On{}, false
	}
	return o, true
}

// Hears tells whether the line follows an event of this kind and key.
func (o On) Hears(kind, key string) bool {
	return o.Kind == kind && (o.Key == "" || o.Key == key)
}

// Listens are the page's on: links that name an event, in order.
func (p *Page) Listens() []PageLink {
	var out []PageLink
	for _, l := range p.Links {
		if l.Kind != LinkOn {
			continue
		}
		if _, ok := ParseOn(l.Target); ok {
			out = append(out, l)
		}
	}
	return out
}

// Ons are what the node's page follows, each once.
func (n *Node) Ons() []On {
	p, err := n.Page()
	if err != nil {
		return nil
	}
	var out []On
	for _, l := range p.Listens() {
		o, _ := ParseOn(l.Target)
		o.Field = ""
		if !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// Heard is an event of the journal, as a page's lines follow it.
type Heard struct {
	Kind, Key string
	Payload   map[string]any
	External  bool   // written by someone else
	Stamp     string // the date written after a line (ReadStamp)
}

// Hear rewrites the lines of the node's page that follow ev: their value,
// their date, and the external mark of their callout. It gives how many
// links it rewrote; 0 when none follows ev, or none had a value in it.
func (n *Node) Hear(ev Heard) (int, error) {
	body, err := n.Body()
	if err != nil {
		return 0, err
	}
	out, count := hear(body, ev)
	if count == 0 {
		return 0, nil
	}
	return count, n.CodeBody(out)
}

// hear is Hear on a body.
func hear(body string, ev Heard) (string, int) {
	p := ReadPage(body)
	lines, join := splitBody(body)
	byLine := map[int][]PageLink{}
	var order []int
	for _, l := range p.Listens() {
		o, _ := ParseOn(l.Target)
		if !o.Hears(ev.Kind, ev.Key) {
			continue
		}
		if _, ok := byLine[l.Line]; !ok {
			order = append(order, l.Line)
		}
		byLine[l.Line] = append(byLine[l.Line], l)
	}
	count := 0
	fed := map[int]bool{} // the callouts a value was written in
	for _, ln := range order {
		ls := byLine[ln]
		t := lines[ln-1]
		toks := inlineTokens(t)
		// Right to left: a token's place holds while those after it change.
		for i := len(toks) - 1; i >= 0; i-- {
			k := toks[i]
			if k.kind != LinkOn {
				continue
			}
			o, ok := ParseOn(k.target)
			if !ok || !o.Hears(ev.Kind, ev.Key) {
				continue
			}
			v, ok := fieldOf(ev.Payload, o.Field)
			if !ok {
				continue
			}
			callout := ls[0].Callout
			label, ok := relabel(k.text, v, ev.External && callout == 0)
			if !ok {
				continue
			}
			rest := restamp(t[k.end:], ev.Stamp)
			t = t[:k.start] + "[" + label + "](" + LinkOn + ":" + k.target + ")" + rest
			count++
			if callout != 0 {
				fed[callout] = true
			}
		}
		lines[ln-1] = t
	}
	// The callouts fed by someone else's event are marked; no event takes
	// the mark off (words around a number, a line it did not rewrite, may
	// still be someone else's).
	if ev.External {
		for c := range fed {
			lines[c-1] = calloutMark(lines[c-1], true)
		}
	}
	return join(lines), count
}

// calloutHeadRe cuts a callout's first line: its >, [!type, its options,
// and the rest.
var calloutHeadRe = regexp.MustCompile(`^((?:[ \t]*>[ ]?)*\s*\[![\w-]+)((?:\|[^\]|]*)*)(\].*)$`)

// calloutMark sets or takes off the external option of a callout's first
// line.
func calloutMark(t string, on bool) string {
	m := calloutHeadRe.FindStringSubmatch(t)
	if m == nil {
		return t
	}
	var opts []string
	has := false
	for o := range strings.SplitSeq(m[2], "|") {
		if strings.TrimSpace(o) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(o), OptExternal) {
			has = true
			if !on {
				continue
			}
		}
		opts = append(opts, o)
	}
	if on && !has {
		opts = append(opts, OptExternal)
	}
	out := m[1]
	for _, o := range opts {
		out += "|" + o
	}
	return out + m[3]
}

// fieldOf is the value at a path of a payload: plain fields joined by
// dots, a list's index as a number.
func fieldOf(p map[string]any, path string) (any, bool) {
	var v any = p
	for f := range strings.SplitSeq(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = x[f]; !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(f)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, v != nil
}

// numInRe finds a number in a label, as a page writes it: 18 385, 1,8,
// −2, 3.5.
var numInRe = regexp.MustCompile(`[+−-]?\d(?:[ \x{00a0}\x{202f}]?\d)*(?:[,.]\d+)?`)

// numStyle is how a label writes its number.
type numStyle struct {
	group    string // between groups of three digits; "" for none
	from     int    // the fewest digits grouped
	decimal  string // , or .
	decimals int
	minus    string // - or −
	plus     bool   // a number over 0 is written with its +
}

// NumberIn is the first number a label writes; ok false when none.
func NumberIn(label string) (float64, bool) {
	loc := numInRe.FindStringIndex(label)
	if loc == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.NewReplacer(" ", "", " ", "", " ", "", ",", ".", "−", "-", "+", "").Replace(label[loc[0]:loc[1]]), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// styleOf is how a number is written; a number of four digits or more
// written in one piece is not grouped, one shorter is grouped with a
// space from five digits (as a page writes 18 385), unless its decimals
// follow a point.
func styleOf(s string) numStyle {
	st := numStyle{decimal: ",", minus: "-", from: 4, plus: strings.HasPrefix(s, "+")}
	if strings.HasPrefix(s, "−") {
		st.minus = "−"
	}
	digits := strings.TrimLeft(s, "+−-")
	intPart := digits
	if i := strings.IndexAny(digits, ",."); i >= 0 {
		st.decimal = digits[i : i+1]
		st.decimals = len(digits) - i - 1
		intPart = digits[:i]
	}
	for _, r := range intPart {
		if !unicode.IsDigit(r) {
			st.group = string(r)
			break
		}
	}
	if st.group == "" && len(intPart) < 4 && st.decimal != "." {
		st.group, st.from = " ", 5
	}
	return st
}

// format writes v in a style.
func (st numStyle) format(v float64) string {
	p := math.Pow(10, float64(st.decimals))
	s := strconv.FormatFloat(math.Round(math.Abs(v)*p)/p, 'f', st.decimals, 64) // half away from 0
	intPart, frac, _ := strings.Cut(s, ".")
	if st.group != "" && len(intPart) >= st.from {
		var b strings.Builder
		for i, r := range intPart {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				b.WriteString(st.group)
			}
			b.WriteRune(r)
		}
		intPart = b.String()
	}
	if frac != "" {
		intPart += st.decimal + frac
	}
	zero := strings.Trim(intPart, "0,.   ") == ""
	switch {
	case v < 0 && !zero:
		intPart = st.minus + intPart
	case v > 0 && !zero && st.plus:
		intPart = "+" + intPart
	}
	return intPart
}

// relabel is a label with its value v: its number replaced when v is one
// and the label writes one, else the whole label replaced by v's text.
// numbersOnly: a text is not written. ok is false when v is no value of
// a line (a list, an object, a text made of nothing).
func relabel(label string, v any, numbersOnly bool) (string, bool) {
	num, isNum := v.(float64)
	if s, ok := v.(string); ok && Number(s) {
		num, isNum = NumberIn(s)
	}
	switch x := v.(type) {
	case int:
		num, isNum = float64(x), true
	case int64:
		num, isNum = float64(x), true
	}
	if isNum {
		if math.IsNaN(num) || math.IsInf(num, 0) {
			return "", false
		}
		if loc := numInRe.FindStringIndex(label); loc != nil {
			return label[:loc[0]] + styleOf(label[loc[0]:loc[1]]).format(num) + label[loc[1]:], true
		}
		return numStyle{group: " ", from: 5, decimal: ",", decimals: decimalsOf(num), minus: "-"}.format(num), true
	}
	s, ok := v.(string)
	if !ok || numbersOnly {
		return "", false
	}
	s = inertText(s)
	if s == "" {
		return "", false
	}
	return s, true
}

// decimalsOf is how many decimals v needs, two at most.
func decimalsOf(v float64) int {
	for d := 0; d < 2; d++ {
		p := math.Pow(10, float64(d))
		if math.Abs(math.Round(v*p)-v*p) < 1e-9 {
			return d
		}
	}
	return 2
}

// inertText is a text as a line's label holds it: one line, OnTextMax
// runes at most, without what Markdown reads as a link, an image, code
// or HTML, nor a table's bar or an item's date.
func inertText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), unicode.IsSpace(r):
			return ' '
		case strings.ContainsRune("[]()`<>|\\📅", r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > OnTextMax {
		s = strings.TrimSpace(string([]rune(s)[:OnTextMax-1])) + "…"
	}
	return s
}

// stampRe is the date after a line's link, as ReadStamp writes it (« ·
// relu le 26/11 à 10 h 05 », « · read on Nov 26 at 10:05 »), its hour
// left out or short (« à 10 h ») when an author wrote it so; nothing
// after it.
var stampRe = regexp.MustCompile(`^[ \t]*·[ \t]*(?:relu le \d{1,2}/\d{1,2}(?:/\d{2,4})?(?: à \d{1,2} h(?: \d{2})?)?|read on [A-Z][a-z]{2} \d{1,2}(?: at \d{1,2}:\d{2})?)`)

// restamp is what follows a link, its date written again (or added).
func restamp(rest, stamp string) string {
	if stamp == "" {
		return rest
	}
	if loc := stampRe.FindStringIndex(rest); loc != nil {
		return " · " + stamp + rest[loc[1]:]
	}
	return " · " + stamp + rest
}

// ReadStamp is the date written after a line its event rewrote, in the
// user's language (fr or en) and zone: « relu le 26/11 à 10 h 05 ».
func ReadStamp(t time.Time, lang string) string {
	if lang == "en" {
		return fmt.Sprintf("read on %s at %s", t.Format("Jan 2"), t.Format("15:04"))
	}
	return fmt.Sprintf("relu le %s à %d h %02d", t.Format("02/01"), t.Hour(), t.Minute())
}

// onSpan is an on: link of a body and the date after it, as written.
type onSpan struct {
	target string
	text   string
}

// onSpans are a body's on: links with their dates, by target and
// occurrence ("<target> <n>").
func onSpans(body string) map[string]string {
	out := map[string]string{}
	p := ReadPage(body)
	lines, _ := splitBody(body)
	seen := map[string]int{}
	done := map[int]bool{}
	for _, l := range p.Links {
		if l.Kind != LinkOn || done[l.Line] {
			continue
		}
		done[l.Line] = true
		for _, s := range lineSpans(lines[l.Line-1]) {
			k := fmt.Sprintf("%s %d", s.target, seen[s.target])
			seen[s.target]++
			out[k] = s.text
		}
	}
	return out
}

// lineSpans are a line's on: links with their dates, in order.
func lineSpans(t string) []onSpan {
	var out []onSpan
	for _, k := range inlineTokens(t) {
		if k.kind != LinkOn {
			continue
		}
		end := k.end
		if loc := stampRe.FindStringIndex(t[end:]); loc != nil {
			end += loc[1]
		}
		out = append(out, onSpan{target: k.target, text: t[k.start:end]})
	}
	return out
}

// KeepHeard keeps in an author's body (written) the values code wrote
// since the version the author read (was): a line whose value and date
// the author left as that version had them gets those of the file now
// (now). A value the author changed is the author's.
func KeepHeard(was, now, written string) string {
	before, after := onSpans(was), onSpans(now)
	lines, join := splitBody(written)
	p := ReadPage(written)
	seen := map[string]int{}
	done := map[int]bool{}
	changed := false
	for _, l := range p.Links {
		if l.Kind != LinkOn || done[l.Line] {
			continue
		}
		done[l.Line] = true
		t := lines[l.Line-1]
		spans := lineSpans(t)
		type swap struct{ from, to string }
		var swaps []swap
		for _, s := range spans {
			k := fmt.Sprintf("%s %d", s.target, seen[s.target])
			seen[s.target]++
			if b, ok := before[k]; ok && b == s.text {
				if a, ok := after[k]; ok && a != b {
					swaps = append(swaps, swap{s.text, a})
				}
			}
		}
		at := 0
		for _, sw := range swaps {
			i := strings.Index(t[at:], sw.from)
			if i < 0 {
				continue
			}
			i += at
			t = t[:i] + sw.to + t[i+len(sw.from):]
			at = i + len(sw.to)
			changed = true
		}
		lines[l.Line-1] = t
	}
	if !changed {
		return written
	}
	return join(lines)
}

// CalloutExternal tells a page that holds a callout marked external:
// someone else's words may be in it, and whoever reads it is tainted.
func (n *Node) CalloutExternal() bool {
	p, err := n.Page()
	return err == nil && len(externalTargets(p)) > 0
}

// withoutValues is a page's text as its authors wrote it: the values and
// dates code writes on its on: lines blanked, its callouts' external
// marks taken off. Code's lines aside, two texts alike there are the same
// authors' words.
func withoutValues(text string) string {
	p := ReadPage(text)
	lines, join := splitBody(text)
	for i, t := range lines {
		if i >= len(p.info) || p.info[i].code {
			continue
		}
		t = calloutMark(t, false)
		spans := lineSpans(t)
		for j := len(spans) - 1; j >= 0; j-- {
			s := spans[j]
			at := strings.LastIndex(t, s.text)
			t = t[:at] + "[](" + LinkOn + ":" + s.target + ")" + t[at+len(s.text):]
		}
		lines[i] = t
	}
	return join(lines)
}

// externalTargets are the on: targets of each callout marked external,
// one set a callout.
func externalTargets(p *Page) []map[string]bool {
	var out []map[string]bool
	var walk func(bs []PageBlock)
	walk = func(bs []PageBlock) {
		for _, b := range bs {
			if b.Kind == KindCallout && slices.Contains(b.Opts, OptExternal) {
				ts := map[string]bool{}
				for _, l := range p.Links {
					if l.Kind == LinkOn && l.Line >= b.Line && l.Line <= b.End {
						ts[l.Target] = true
					}
				}
				out = append(out, ts)
			}
			walk(b.Body)
			for _, it := range b.Items {
				walk(it.Detail)
			}
		}
	}
	walk(p.Blocks)
	return out
}

// keepMarks refuses an author's page that changes code's external marks:
// a value someone else's event wrote that stays in the page out of a
// marked callout, or a callout marked that was not (no callout marked
// before held any of its lines).
func keepMarks(was, now *Page) error {
	before, after := externalTargets(was), externalTargets(now)
	marked := map[string]bool{}
	for _, ts := range after {
		for t := range ts {
			marked[t] = true
		}
	}
	for _, l := range now.Links {
		if l.Kind != LinkOn || marked[l.Target] {
			continue
		}
		for _, ts := range before {
			if ts[l.Target] {
				return fmt.Errorf("the callout that holds on:%s is marked %s by code: keep its |%s (%w)", l.Target, OptExternal, OptExternal, ErrCodeField)
			}
		}
	}
	for _, ts := range after {
		if !slices.ContainsFunc(before, func(b map[string]bool) bool { return overlap(b, ts) }) {
			return fmt.Errorf("a callout's |%s is %w: leave it out", OptExternal, ErrCodeField)
		}
	}
	return nil
}

// overlap tells two callouts' targets that share one, or are both none.
func overlap(a, b map[string]bool) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	for t := range a {
		if b[t] {
			return true
		}
	}
	return false
}
