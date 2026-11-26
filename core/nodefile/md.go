package nodefile

import (
	"regexp"
	"strings"
)

// A node's body is a Markdown page (docs/node-format.md, "The page"):
// what it shows is written in it, with Obsidian's conventions where they
// exist (- [ ] tasks, 📅 dates, > [!type] callouts, ![[embeds]]) and
// Eriphe's own only as valid Markdown links: a reference [text][m1] with
// its target [m1]: mail:<id> at the bottom, and the links do:, card:,
// ask: and on:. ReadPage reads a body into a Page; Check says what is
// wrong in it (check.go); lines.go changes one line of it.
//
// Go's reading is authoritative (references, a do: link's scope, where a
// card goes); the workspace reads the same pages the same way to draw
// them. Both are held to testdata/pages: each page and its tree as JSON.
//
// Lines are counted from 1, in the body (what follows the front matter,
// as node_read gives it).

// Page is a body read as a Markdown page.
type Page struct {
	// Written is the « Écrit le … » line, when the page's first line of
	// text says when it was written; it is not one of the blocks.
	Written *Written    `json:"written,omitempty"`
	Blocks  []PageBlock `json:"blocks"`
	Refs    []RefDef    `json:"refs"`  // the reference targets ([m1]: mail:<id>), in order
	Uses    []RefUse    `json:"uses"`  // the references ([text][m1]) of the text, in order
	Links   []PageLink  `json:"links"` // the do:, card:, ask: and on: links, in order

	lines []string   // the body's lines, without their ends
	info  []lineInfo // what holds each line (index: line-1)
	open  []int      // the lines of code fences never closed
}

// Written is the line that says when the page was written.
type Written struct {
	Text string `json:"text"` // as written, without its final dot
	Line int    `json:"line"`
}

// Block kinds.
const (
	KindHeading   = "heading"
	KindParagraph = "paragraph"
	KindList      = "list"
	KindTable     = "table"
	KindCode      = "code"
	KindCallout   = "callout"
	KindQuote     = "quote"
	KindEmbed     = "embed"
	KindRule      = "rule"
)

// PageBlock is a block of a page: Kind says which, and which fields it
// has. Line and End are its first and last lines.
type PageBlock struct {
	Kind string `json:"kind"`
	Line int    `json:"line"`
	End  int    `json:"end"`
	// Heading: its level (1-6); heading, paragraph, code and quote: its
	// text (a paragraph's lines trimmed, joined by \n; a code block's as
	// written).
	Level int    `json:"level,omitempty"`
	Text  string `json:"text,omitempty"`
	// List.
	Ordered bool       `json:"ordered,omitempty"`
	Items   []PageItem `json:"items,omitempty"`
	// Table: its header's cells, then its rows', as many as the header.
	Head []string   `json:"head,omitempty"`
	Rows [][]string `json:"rows,omitempty"`
	// Code: the first word after its fence (diff, json, eriphe:block…).
	Lang string `json:"lang,omitempty"`
	// Callout (> [!type|opt|opt]- Title): its type and options lowercased,
	// its fold (- folded, + open, "" none), its title and what it holds.
	Type  string      `json:"type,omitempty"`
	Opts  []string    `json:"opts,omitempty"`
	Fold  string      `json:"fold,omitempty"`
	Title string      `json:"title,omitempty"`
	Body  []PageBlock `json:"body,omitempty"`
	// Quote: who said it and when, from its last line « — Sam, 24/11 ».
	From string `json:"from,omitempty"`
	When string `json:"when,omitempty"`
	// Embed (![[Name]]): the node it names.
	Name string `json:"name,omitempty"`
}

// Task states, written between the brackets of - [ ].
const (
	TaskOpen    = " "
	TaskDone    = "x"
	TaskDoing   = "/"
	TaskDropped = "-"
)

// PageItem is an item of a list: its first line, and its detail, the
// lines indented under it read as blocks.
type PageItem struct {
	Line   int         `json:"line"`
	End    int         `json:"end"`            // its last line, its detail's included
	Task   string      `json:"task,omitempty"` // a task's state (TaskOpen…); "" for no task
	Text   string      `json:"text"`           // its first line, after its marker and box
	Due    string      `json:"due,omitempty"`  // the date after 📅 (YYYY-MM-DD), as written
	Detail []PageBlock `json:"detail,omitempty"`
}

// RefDef is a reference's target, [label]: target, on its own line.
type RefDef struct {
	Label  string `json:"label"`  // lowercased, its spaces one each
	Target string `json:"target"` // the first word after the colon, as written
	// Scheme and ID split the target at its first colon (mail and the
	// id, byte for byte); Scheme is "" for a target without one.
	Scheme string `json:"scheme,omitempty"`
	ID     string `json:"id,omitempty"`
	Line   int    `json:"line"`
}

// RefUse is a reference of the text: [text][label], or [label][].
type RefUse struct {
	Label string `json:"label"` // lowercased, as RefDef's
	Text  string `json:"text"`  // the link's words, never what the target is
	Line  int    `json:"line"`
	// Item is the first line of the item whose line or detail holds it
	// (0 for none); Task that item's state.
	Item int    `json:"item,omitempty"`
	Task string `json:"task,omitempty"`
}

// Link kinds: the schemes of Eriphe's own links.
const (
	LinkDo   = "do"   // an action on the references of its heading
	LinkCard = "card" // a card already proposed, placed here
	LinkAsk  = "ask"  // a probable reply, sent to Eriphe as a message
	LinkOn   = "on"   // a value an event rewrites
)

// PageLink is a [label](do:…), card:, ask: or on: link.
type PageLink struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Target string `json:"target"` // what follows the scheme's colon
	Line   int    `json:"line"`
	// Alone: its line holds only links of these kinds, separated by
	// spaces or « · ».
	Alone bool `json:"alone,omitempty"`
	// Item is the first line of the item whose line or detail holds it;
	// Callout the first line of the callout that holds it; Heading the
	// line of the heading whose section holds it. 0 for none.
	Item    int `json:"item,omitempty"`
	Callout int `json:"callout,omitempty"`
	Heading int `json:"heading,omitempty"`
}

// lineInfo is what holds a line of the body.
type lineInfo struct {
	code    bool // in a fenced code block (its fences included)
	def     bool // a reference's target
	item    int  // the innermost item holding it, 0 for none
	task    string
	callout int // the innermost callout holding it, 0 for none
	heading int // the section's heading (a heading of the page's, not a callout's)
}

// line is a line of the body read in a block: its text (a callout's or
// an item's prefix taken off) and its number.
type line struct {
	t  string
	ln int
}

var (
	pageFenceRe = regexp.MustCompile("^\\s*(`{3,}|~{3,})\\s*(\\S*)")
	pageItemRe  = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	pageDefRe   = regexp.MustCompile(`^ {0,3}\[([^\]^][^\]]*)\]:\s*(\S+)`)
	calloutRe   = regexp.MustCompile(`^\[!([\w-]+)((?:\|[^\]|]*)*)\]([-+]?)\s*(.*)$`)
	pageTaskRe  = regexp.MustCompile(`^\[([ xX/-])\](?:\s+(.*))?$`)
	pageHeadRe  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	pageEmbedRe = regexp.MustCompile(`^!\[\[([^\]]+)\]\]$`)
	pageRuleRe  = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	writtenRe   = regexp.MustCompile(`(?i)^(Écrit|Ecrit|Mis à jour|Written|Updated)( le| on)?\b`)
	dueRe       = regexp.MustCompile(`📅\s*(\d{4}-\d{2}-\d{2})`)
	quoteByRe   = regexp.MustCompile(`^\s*[—–]\s*(.+?)(?:,\s*([^,]+))?\s*$`)
)

// ReadPage reads a body as a Markdown page. It never fails: what it
// cannot read is text, and Check says what is wrong.
func ReadPage(body string) *Page {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	raw := strings.Split(body, "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1] // the last line's end ends no line
	}
	p := &Page{lines: raw, info: make([]lineInfo, len(raw)),
		Blocks: []PageBlock{}, Refs: []RefDef{}, Uses: []RefUse{}, Links: []PageLink{}}

	// Reference targets, outside code fences, are not blocks.
	var body2 []line
	fence := ""
	for i, t := range raw {
		ln := i + 1
		if fence != "" {
			p.info[i].code = true
			if pageCloses(t, fence) {
				fence = ""
			}
			body2 = append(body2, line{t, ln})
			continue
		}
		if m := pageFenceRe.FindStringSubmatch(t); m != nil {
			fence = m[1]
			p.info[i].code = true
			body2 = append(body2, line{t, ln})
			continue
		}
		if m := pageDefRe.FindStringSubmatch(t); m != nil {
			d := RefDef{Label: refLabel(m[1]), Target: m[2], Line: ln}
			if s, id, ok := strings.Cut(d.Target, ":"); ok && schemeRe.MatchString(s) {
				d.Scheme, d.ID = s, id
			}
			p.Refs = append(p.Refs, d)
			p.info[i].def = true
			continue
		}
		body2 = append(body2, line{t, ln})
	}
	// « Écrit le … » on the first line of text.
	for k, l := range body2 {
		if blank(l.t) {
			continue
		}
		if s := strings.TrimSpace(l.t); writtenRe.MatchString(s) && !p.info[l.ln-1].code {
			p.Written = &Written{Text: strings.TrimSuffix(s, "."), Line: l.ln}
			body2 = append(body2[:k:k], body2[k+1:]...)
		}
		break
	}
	p.Blocks = p.blocks(body2, 0)
	p.sections()
	p.inline()
	return p
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)

// refLabel is a label as references match it: without case, its spaces
// one each.
func refLabel(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// indent is a line's indent: a tab counts four.
func indent(s string) int {
	n := 0
	for _, c := range s {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// dedent takes n columns of indent off s, a tab counting four.
func dedent(s string, n int) string {
	col := 0
	for i, c := range s {
		if col >= n {
			return s[i:]
		}
		switch c {
		case ' ':
			col++
		case '\t':
			col += 4
			if col > n {
				return strings.Repeat(" ", col-n) + s[i+1:]
			}
		default:
			return s[i:]
		}
	}
	return ""
}

// pageCloses tells whether t closes a fence opened by f: the same
// character, at least as many, nothing after.
func pageCloses(t, f string) bool {
	s := strings.TrimSpace(t)
	if !strings.HasPrefix(s, strings.Repeat(f[:1], len(f))) {
		return false
	}
	return strings.Trim(s, f[:1]) == ""
}

// pageStartsBlock tells a line that ends a paragraph.
func pageStartsBlock(t string) bool {
	s := strings.TrimSpace(t)
	return pageFenceRe.MatchString(t) || pageHeadRe.MatchString(s) || strings.HasPrefix(s, ">") ||
		pageItemRe.MatchString(t) || pageEmbedRe.MatchString(s) || pageRuleRe.MatchString(s)
}

// tableDelim tells a table's delimiter row: | --- | :-: |.
func tableDelim(t string) bool {
	s := strings.TrimSpace(t)
	if !strings.Contains(s, "-") || !strings.Contains(s, "|") || !delimRe.MatchString(s) {
		return false
	}
	for _, c := range TableCells(s) {
		if !alignRe.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

// cells are a table row's cells, trimmed.
func cells(t string) []string {
	out := TableCells(t)
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

// blocks reads lines into blocks; callout is the callout holding them.
func (p *Page) blocks(ls []line, callout int) []PageBlock {
	out := []PageBlock{}
	for i := 0; i < len(ls); {
		l := ls[i]
		s := strings.TrimSpace(l.t)
		switch {
		case blank(l.t):
			i++
		case pageFenceRe.MatchString(l.t):
			m := pageFenceRe.FindStringSubmatch(l.t)
			var code []string
			j := i + 1
			for ; j < len(ls) && !pageCloses(ls[j].t, m[1]); j++ {
				code = append(code, ls[j].t)
			}
			end := l.ln
			if j < len(ls) {
				end = ls[j].ln
			} else {
				p.open = append(p.open, l.ln)
				if len(ls) > i+1 {
					end = ls[len(ls)-1].ln
				}
			}
			for ln := l.ln; ln <= end; ln++ {
				p.info[ln-1].code = true
			}
			out = append(out, PageBlock{Kind: KindCode, Line: l.ln, End: end, Lang: m[2], Text: strings.Join(code, "\n")})
			i = j + 1
		case pageHeadRe.MatchString(s):
			m := pageHeadRe.FindStringSubmatch(s)
			out = append(out, PageBlock{Kind: KindHeading, Line: l.ln, End: l.ln, Level: len(m[1]), Text: trimClosingHashes(m[2])})
			i++
		case pageEmbedRe.MatchString(s):
			name, _, _ := strings.Cut(pageEmbedRe.FindStringSubmatch(s)[1], "|")
			out = append(out, PageBlock{Kind: KindEmbed, Line: l.ln, End: l.ln, Name: strings.TrimSpace(name)})
			i++
		case pageRuleRe.MatchString(s):
			out = append(out, PageBlock{Kind: KindRule, Line: l.ln, End: l.ln})
			i++
		case strings.HasPrefix(s, ">"):
			var inner []line
			for ; i < len(ls) && strings.HasPrefix(strings.TrimSpace(ls[i].t), ">"); i++ {
				inner = append(inner, line{quoteStrip(ls[i].t), ls[i].ln})
			}
			out = append(out, p.quote(inner, callout))
		case pageItemRe.MatchString(l.t):
			var b PageBlock
			b, i = p.list(ls, i, callout)
			out = append(out, b)
		case strings.Contains(s, "|") && i+1 < len(ls) && tableDelim(ls[i+1].t):
			b := PageBlock{Kind: KindTable, Line: l.ln, Head: cells(s), Rows: [][]string{}}
			j := i + 2
			for ; j < len(ls) && strings.Contains(ls[j].t, "|") && !blank(ls[j].t) && !pageStartsBlock(ls[j].t); j++ {
				row := cells(ls[j].t)
				fit := make([]string, len(b.Head))
				copy(fit, row)
				b.Rows = append(b.Rows, fit)
			}
			b.End = ls[j-1].ln
			out = append(out, b)
			i = j
		default:
			para := []string{s}
			j := i + 1
			for ; j < len(ls) && !blank(ls[j].t) && !pageStartsBlock(ls[j].t); j++ {
				para = append(para, strings.TrimSpace(ls[j].t))
			}
			out = append(out, PageBlock{Kind: KindParagraph, Line: l.ln, End: ls[j-1].ln, Text: strings.Join(para, "\n")})
			i = j
		}
	}
	return out
}

// trimClosingHashes takes off a heading's closing #s.
func trimClosingHashes(s string) string {
	t := strings.TrimRight(strings.TrimSpace(s), "#")
	if t != strings.TrimSpace(s) && (t == "" || strings.HasSuffix(t, " ")) {
		return strings.TrimSpace(t)
	}
	return strings.TrimSpace(s)
}

// quoteStrip takes a line's first > off, and the space after it.
func quoteStrip(t string) string {
	s := strings.TrimLeft(t, " \t")
	s = strings.TrimPrefix(s, ">")
	return strings.TrimPrefix(s, " ")
}

// quote reads a > block: a callout, or a quote whose last line « — Sam,
// 24/11 » says who said it.
func (p *Page) quote(inner []line, callout int) PageBlock {
	first, last := inner[0].ln, inner[len(inner)-1].ln
	if m := calloutRe.FindStringSubmatch(strings.TrimSpace(inner[0].t)); m != nil {
		b := PageBlock{Kind: KindCallout, Line: first, End: last, Type: strings.ToLower(m[1]), Fold: m[3], Title: strings.TrimSpace(m[4])}
		for o := range strings.SplitSeq(m[2], "|") {
			if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
				b.Opts = append(b.Opts, o)
			}
		}
		for ln := first; ln <= last; ln++ {
			p.info[ln-1].callout = first
		}
		b.Body = p.blocks(inner[1:], first)
		return b
	}
	text := make([]string, 0, len(inner))
	for _, l := range inner {
		text = append(text, l.t)
	}
	for len(text) > 0 && blank(text[len(text)-1]) {
		text = text[:len(text)-1]
	}
	b := PageBlock{Kind: KindQuote, Line: first, End: last}
	if n := len(text); n > 0 {
		if m := quoteByRe.FindStringSubmatch(text[n-1]); m != nil {
			b.Text, b.From, b.When = strings.TrimSpace(strings.Join(text[:n-1], "\n")), m[1], strings.TrimSpace(m[2])
			return b
		}
	}
	b.Text = strings.TrimSpace(strings.Join(text, "\n"))
	return b
}

// list reads the items that start at i, at that line's indent: an item's
// lines indented further are its detail, read again as blocks. It gives
// where the list ends.
func (p *Page) list(ls []line, i int, callout int) (PageBlock, int) {
	m := pageItemRe.FindStringSubmatch(ls[i].t)
	base := indent(ls[i].t)
	b := PageBlock{Kind: KindList, Line: ls[i].ln, Ordered: m[2][0] >= '0' && m[2][0] <= '9'}
	for i < len(ls) {
		m := pageItemRe.FindStringSubmatch(ls[i].t)
		if m == nil || indent(ls[i].t) != base {
			break
		}
		it := PageItem{Line: ls[i].ln, Text: m[3]}
		if t := pageTaskRe.FindStringSubmatch(m[3]); t != nil {
			it.Task, it.Text = strings.ToLower(t[1]), t[2]
		}
		if d := dueRe.FindStringSubmatch(it.Text); d != nil {
			it.Due = d[1]
		}
		var kids []line
		j := i + 1
		for ; j < len(ls); j++ {
			c := ls[j].t
			if blank(c) {
				// A blank line keeps the item open only if an indented
				// line follows.
				k := j + 1
				for k < len(ls) && blank(ls[k].t) {
					k++
				}
				if k < len(ls) && indent(ls[k].t) > base {
					kids = append(kids, ls[j])
					continue
				}
				break
			}
			if indent(c) <= base {
				break
			}
			kids = append(kids, ls[j])
		}
		it.End = it.Line
		if len(kids) > 0 {
			it.End = kids[len(kids)-1].ln
		}
		for ln := it.Line; ln <= it.End; ln++ {
			p.info[ln-1].item, p.info[ln-1].task = it.Line, it.Task
		}
		cut := -1
		for _, k := range kids {
			if !blank(k.t) && (cut < 0 || indent(k.t) < cut) {
				cut = indent(k.t)
			}
		}
		ded := make([]line, len(kids))
		for k, l := range kids {
			ded[k] = line{dedent(l.t, max(cut, 0)), l.ln}
		}
		if d := p.blocks(ded, callout); len(d) > 0 {
			it.Detail = d
		}
		b.Items = append(b.Items, it)
		b.End = it.End
		i = j
		// Blank lines between two items of the list.
		k := i
		for k < len(ls) && blank(ls[k].t) {
			k++
		}
		if k > i && k < len(ls) && pageItemRe.MatchString(ls[k].t) && indent(ls[k].t) == base {
			i = k
		}
	}
	return b, i
}

// sections says for each line the heading of its section: the last
// heading of the page's above it (a callout's headings are its own).
func (p *Page) sections() {
	heads := map[int]bool{}
	for _, b := range p.Blocks {
		if b.Kind == KindHeading {
			heads[b.Line] = true
		}
	}
	cur := 0
	for i := range p.info {
		if heads[i+1] {
			cur = i + 1
		}
		p.info[i].heading = cur
	}
}

// Heading is the top-level heading at line ln; nil when there is none.
func (p *Page) Heading(ln int) *PageBlock {
	for i := range p.Blocks {
		if b := &p.Blocks[i]; b.Kind == KindHeading && b.Line == ln {
			return b
		}
	}
	return nil
}

// inline reads each line's references and links, outside code.
func (p *Page) inline() {
	for i, t := range p.lines {
		in := p.info[i]
		if in.code || in.def {
			continue
		}
		ln := i + 1
		toks := inlineTokens(t)
		alone := onlyLinks(t, toks)
		for _, k := range toks {
			switch {
			case k.ref:
				p.Uses = append(p.Uses, RefUse{Label: k.label, Text: k.text, Line: ln, Item: in.item, Task: in.task})
			case k.kind != "":
				p.Links = append(p.Links, PageLink{Kind: k.kind, Label: k.text, Target: k.target, Line: ln,
					Alone: alone, Item: in.item, Callout: in.callout, Heading: in.heading})
			}
		}
	}
}

// token is a reference or a link of a line: start and end its bytes.
type token struct {
	start, end   int
	text         string
	ref          bool   // [text][label]
	label        string // its label, lowercased
	kind, target string // a link of Eriphe's: its kind and target
}

var linkRe = regexp.MustCompile(`^\[([^\]]+)\](?:\[([^\]]*)\]|\(([^)\s]*)\))`)

// inlineTokens are a line's references and Eriphe's links, in order. A
// code span is read whole: nothing in it is a link; nor is an image, a
// [[wikilink]] or an escaped bracket.
func inlineTokens(s string) []token {
	var out []token
	for i := 0; i < len(s); {
		c := s[i]
		if c == '`' {
			n := 0
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			if close := strings.Index(s[i+n:], strings.Repeat("`", n)); close >= 0 {
				i += n + close + n
			} else {
				i += n
			}
			continue
		}
		if c == '\\' {
			i += 2
			continue
		}
		if c == '[' && (i+1 >= len(s) || s[i+1] != '[') && (i == 0 || (s[i-1] != '!' && s[i-1] != '[')) {
			if m := linkRe.FindStringSubmatchIndex(s[i:]); m != nil {
				text := s[i+m[2] : i+m[3]]
				t := token{start: i, end: i + m[1], text: text}
				switch {
				case m[4] >= 0: // [text][label] or [text][]
					label := s[i+m[4] : i+m[5]]
					if strings.TrimSpace(label) == "" {
						label = text
					}
					t.ref, t.label = true, refLabel(label)
				case m[6] >= 0:
					kind, target, ok := strings.Cut(s[i+m[6]:i+m[7]], ":")
					if !ok || !isLinkKind(kind) {
						i += m[1]
						continue
					}
					t.kind, t.target = kind, target
				}
				out = append(out, t)
				i += m[1]
				continue
			}
		}
		i++
	}
	return out
}

func isLinkKind(k string) bool {
	return k == LinkDo || k == LinkCard || k == LinkAsk || k == LinkOn
}

// onlyLinks tells whether a line holds only links of Eriphe's,
// separated by spaces, « · », | or commas (a callout's > before them).
func onlyLinks(s string, toks []token) bool {
	if len(toks) == 0 {
		return false
	}
	at := 0
	gap := func(g string) bool {
		return strings.Trim(g, " \t·|,>") == ""
	}
	for _, t := range toks {
		if t.kind == "" || !gap(s[at:t.start]) {
			return false
		}
		at = t.end
	}
	return gap(s[at:])
}

// Body is the node's body as its file holds it: its text, and its
// blocks in their fences.
func (n *Node) Body() string {
	var b strings.Builder
	for _, s := range n.segs {
		if s.block == nil {
			b.WriteString(s.text)
			continue
		}
		if out, err := s.block.bytes(); err == nil {
			b.Write(out)
		}
	}
	return b.String()
}

// Page reads the node's body as a page: a block in its fence is code.
func (n *Node) Page() *Page { return ReadPage(n.Body()) }
