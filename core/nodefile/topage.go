package nodefile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Format 2 → 3: a node's page is its Markdown. Each block of format 2 (a
// fence « ```eriphe:block <name> » holding A2UI components, their data and
// the sources code read live) is written as the page's own Markdown, at
// its place; the text around it keeps its bytes.
//
//   - Components with their data: List, Item and Fold become list items,
//     their detail folded under them; Column, Row, Card and Tabs are
//     flattened in order; Text a paragraph (a title a heading); Metric,
//     Figures and Progress lines of a [!figures] callout; Timeline a
//     [!steps] callout; Table a table; Chart a [!chart] callout; Agenda an
//     [!agenda] callout; Person, Quote, Alert, Message and Code theirs;
//     Button and Asks ask: links; Link a link, its address as written,
//     never built; a card block a card: link.
//   - A block that read sources: what code last read of them (Snapshot,
//     kept outside the file until now) is written as lines under « Écrit
//     le … », the time of that read. An item of a source whose server and
//     id are known is a reference ([Devis][m1] … [m1]: mail:<id>), and its
//     note (item_note) the line's comment, its group a heading, its state
//     the line's box. A Metric bound to a monitor's value follows it (an
//     on:monitor.changed/<name> line).
//   - What it cannot write (a function call, a field, an input, a gallery,
//     an unreadable block) is kept whole in a ```json fence: readable,
//     nothing lost.
//
// Words from the data, the reads and the notes are someone else's: they
// are escaped, so that none reads as a link, a heading or a cell. A block
// that may show someone else's words (its external, or a note so marked)
// makes the node external, its title still trusted. Code has no tool: no
// do: line is ever written.

// Snapshot is what code kept outside a format-2 file of one of its
// blocks: its sources' last values and its items' notes. The conversion
// writes them as lines.
type Snapshot struct {
	Read   time.Time                  // when the values were last read; zero: never
	Values map[string]json.RawMessage // a source's last value, by its name
	Notes  map[string][]ItemNote      // the notes of a source's items, by its name
}

// ItemNote is an author's note of one item of a source.
type ItemNote struct {
	Item    string `json:"item"` // the item's id
	Note    string `json:"note,omitempty"`
	State   string `json:"state,omitempty"`   // act, look, info, none or done
	Group   string `json:"group,omitempty"`   // the section it shows in
	Tainted bool   `json:"tainted,omitempty"` // it may carry someone else's words
}

// Snapshots give a block's snapshot (nil when there is none): code's,
// read from core.db at start.
type Snapshots func(node, block string) *Snapshot

// ConversionsWith are the format's conversions, the blocks' snapshots
// taken from snap (nil: none, as for a version of the history).
func ConversionsWith(snap Snapshots) []Conversion {
	return []Conversion{
		dropLifetime, // 1 → 2: no more lifetime: only nodes, none standing
		func(raw []byte) ([]byte, error) { return toPage(raw, snap) }, // 2 → 3: blocks become Markdown
		foldFrontBlock, // 3 → 4: a front-matter block written in a page becomes the node's properties
	}
}

const blockFence = "```eriphe:block"

// toPage turns format 2 into format 3.
func toPage(raw []byte, snap Snapshots) ([]byte, error) {
	front, yml, body, err := splitFront(raw)
	if err != nil {
		return nil, err
	}
	var meta struct {
		ID       string `yaml:"id"`
		External bool   `yaml:"external"`
	}
	if err := yaml.Unmarshal(yml, &meta); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	segs, err := cutBlocks(body)
	if err != nil {
		return nil, err
	}
	if len(segs) == 1 && segs[0].name == "" {
		return raw, nil // no block: nothing to write
	}
	book := newRefBook(ReadPage(string(body)))
	var out strings.Builder
	external := false
	for i, s := range segs {
		if s.name == "" {
			out.WriteString(s.text)
			continue
		}
		var sn *Snapshot
		if snap != nil {
			sn = snap(meta.ID, s.name)
		}
		md, ext := convertBlock(s.name, s.inner, sn, book)
		external = external || ext
		// A blank line between it and the text around it: a list or a
		// callout never runs into a paragraph.
		if t := out.String(); t != "" && !strings.HasSuffix(t, "\n\n") {
			if !strings.HasSuffix(t, "\n") {
				out.WriteString("\n")
			}
			out.WriteString("\n")
		}
		out.WriteString(md)
		if i+1 < len(segs) && strings.TrimSpace(segs[i+1].text) != "" && !strings.HasPrefix(segs[i+1].text, "\n") {
			out.WriteString("\n")
		}
	}
	page := out.String()
	if defs := book.defs; len(defs) > 0 {
		page = strings.TrimRight(page, "\n") + "\n\n" + strings.Join(defs, "\n") + "\n"
	}
	fm := front
	if external && !meta.External {
		fm = markExternal(front)
	}
	return append(append([]byte{}, fm...), page...), nil
}

// markExternal writes external (and title_trusted: its title was not the
// block's) in a front matter, before its closing line.
func markExternal(front []byte) []byte {
	s := string(front)
	eol := "\n"
	if strings.Contains(s, "\r\n") {
		eol = "\r\n"
	}
	end := strings.LastIndex(strings.TrimRight(s, "\r\n"), eol) + len(eol)
	add := "external: true" + eol
	if !strings.Contains(s, eol+"title_trusted:") {
		add += "title_trusted: true" + eol
	}
	return []byte(s[:end] + add + s[end:])
}

// blockSeg is a piece of a format-2 body: text, or a block (its name and
// its JSON).
type blockSeg struct {
	text  string
	name  string
	inner []byte
}

// cutBlocks cuts a body into text and blocks, as format 2 read them: a
// fence « ```eriphe:block <name> » at the start of a line, outside a code
// fence, to the next line « ``` ».
func cutBlocks(body []byte) ([]blockSeg, error) {
	var segs []blockSeg
	var text strings.Builder
	fence := ""
	lines := strings.SplitAfter(string(body), "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimRight(l, " \t\r\n")
		if fence != "" {
			if fenceCloses(t, fence) {
				fence = ""
			}
			text.WriteString(l)
			continue
		}
		if rest, ok := strings.CutPrefix(t, blockFence); ok && (rest == "" || rest[0] == ' ') {
			name := strings.TrimSpace(rest)
			var inner strings.Builder
			j := i + 1
			for ; j < len(lines) && strings.TrimRight(lines[j], " \t\r\n") != "```"; j++ {
				inner.WriteString(lines[j])
			}
			if j >= len(lines) {
				return nil, fmt.Errorf("block %s: no closing ``` line", name)
			}
			if text.Len() > 0 {
				segs = append(segs, blockSeg{text: text.String()})
				text.Reset()
			}
			segs = append(segs, blockSeg{name: name, inner: []byte(inner.String())})
			i = j
			continue
		}
		if f := fenceOpens(t); f != "" {
			fence = f
		}
		text.WriteString(l)
	}
	if text.Len() > 0 || len(segs) == 0 {
		segs = append(segs, blockSeg{text: text.String()})
	}
	return segs, nil
}

// fenceOpens is the fence a line opens (``` or ~~~, 3 or more, after up
// to 3 spaces); "" when none.
func fenceOpens(t string) string {
	s := strings.TrimLeft(t, " ")
	if len(t)-len(s) > 3 || len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return ""
	}
	n := len(s) - len(strings.TrimLeft(s, s[:1]))
	if n < 3 {
		return ""
	}
	return s[:n]
}

// fenceCloses tells whether a line closes a fence.
func fenceCloses(t, fence string) bool {
	s := strings.TrimLeft(t, " ")
	return len(t)-len(s) <= 3 && len(s) >= len(fence) && strings.Trim(s, fence[:1]) == ""
}

// ---- JSON, its objects' keys in order ----

// jobj is a JSON object, its keys in the order written.
type jobj struct {
	keys []string
	vals map[string]any
}

func (o *jobj) get(k string) any {
	if o == nil {
		return nil
	}
	return o.vals[k]
}

func (o *jobj) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// decodeJSON reads one JSON value: objects as *jobj, numbers as
// json.Number (written back as they were).
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := readJSON(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("one JSON value only")
	}
	return v, nil
}

func readJSON(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch d {
	case '{':
		o := &jobj{vals: map[string]any{}}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}
			o.set(k.(string), v)
		}
		_, err = dec.Token()
		return o, err
	case '[':
		l := []any{}
		for dec.More() {
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}
			l = append(l, v)
		}
		_, err = dec.Token()
		return l, err
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

// ---- References ----

// refBook is the page's references: the labels taken (the text's own
// too), a target's label, the targets to write at the page's foot.
type refBook struct {
	taken    map[string]bool
	byTarget map[string]string
	defs     []string
}

func newRefBook(p *Page) *refBook {
	b := &refBook{taken: map[string]bool{}, byTarget: map[string]string{}}
	for _, d := range p.Refs {
		b.taken[d.Label] = true
		if _, ok := b.byTarget[d.Target]; !ok {
			b.byTarget[d.Target] = d.Label
		}
	}
	for _, u := range p.Uses {
		b.taken[u.Label] = true
	}
	return b
}

// label is the reference of an item of server: one already in the page,
// or a new one; "" when its server or its id cannot be a target.
func (b *refBook) label(server, id string) string {
	if !schemeRe.MatchString(server) || id == "" || strings.ContainsAny(id, " \t\r\n<>") || utf8.RuneCountInString(id) > 300 {
		return ""
	}
	target := server + ":" + id
	if l, ok := b.byTarget[target]; ok {
		return l
	}
	prefix := strings.ToLower(server[:1])
	for n := 1; ; n++ {
		l := prefix + strconv.Itoa(n)
		if !b.taken[l] {
			b.taken[l] = true
			b.byTarget[target] = l
			b.defs = append(b.defs, "["+l+"]: "+target)
			return l
		}
	}
}

// ---- A block ----

// v2source is what the conversion reads of a block's source.
type v2source struct {
	Tool     string `json:"tool"`
	Resource string `json:"resource"`
	Monitor  string `json:"monitor"`
	Items    *struct {
		Path   string `json:"path"`
		ID     string `json:"id"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"items"`
}

// server is the catalog server a source reads; "" for a monitor.
func (s v2source) server() string {
	if s.Resource != "" {
		return s.Resource
	}
	server, _, _ := strings.Cut(s.Tool, "/")
	return server
}

// itemOf is an item of a source: its reference and its note.
type itemOf struct {
	ref  string // its reference's label; "" when it has none
	note *ItemNote
	src  v2source
}

// conv converts one block.
type conv struct {
	root     *jobj            // the data, and each source's value at /<name>
	comps    map[string]*jobj // components by id
	sources  map[string]v2source
	items    map[*jobj]*itemOf
	book     *refBook
	failed   string // why it cannot be written; "" while it can
	external bool
}

// mdBlock is a piece of Markdown: its lines; kind ask and figure merge
// with their like next to them.
type mdBlock struct {
	kind  string // "", "ask", "figure"
	lines []string
}

// convertBlock writes a block as Markdown; ext: it may show someone
// else's words.
func convertBlock(name string, inner []byte, sn *Snapshot, book *refBook) (md string, ext bool) {
	v, err := decodeJSON(inner)
	top, ok := v.(*jobj)
	if err != nil || !ok {
		return keepJSON(inner, sn), false
	}
	ext = top.get("external") == true
	if card, ok := top.get("card").(string); ok && card != "" {
		return "[" + cardWords + "](card:" + card + ")\n", ext
	}
	c := &conv{root: &jobj{vals: map[string]any{}}, comps: map[string]*jobj{}, sources: map[string]v2source{},
		items: map[*jobj]*itemOf{}, book: book}
	if d, ok := top.get("data").(*jobj); ok {
		for _, k := range d.keys {
			c.root.set(k, d.vals[k])
		}
	}
	list, _ := top.get("components").([]any)
	if len(list) == 0 {
		if one, ok := top.get("components").(*jobj); ok { // the short form
			one.set("id", "root")
			list = []any{one}
		}
	}
	if len(list) == 1 {
		if one, ok := list[0].(*jobj); ok && one.get("id") == nil {
			one.set("id", "root")
		}
	}
	for _, x := range list {
		if o, ok := x.(*jobj); ok {
			if id, ok := o.get("id").(string); ok {
				c.comps[id] = o
			}
		}
	}
	if s, ok := top.get("sources").(*jobj); ok {
		raw, _ := json.Marshal(jsonOf(s))
		var srcs map[string]v2source
		json.Unmarshal(raw, &srcs)
		for _, k := range s.keys {
			c.sources[k] = srcs[k]
		}
	}
	read := c.fill(sn)
	c.external = c.external || ext
	blocks := c.render("root", nil, 0)
	if c.failed != "" || c.comps["root"] == nil {
		return keepJSON(inner, sn), c.external
	}
	var b strings.Builder
	if len(c.sources) > 0 && !read.IsZero() {
		b.WriteString(writtenOn(read) + "\n\n")
	}
	b.WriteString(joinBlocks(merge(blocks)))
	if b.Len() == 0 {
		return "", c.external
	}
	return b.String(), c.external
}

// cardWords are a card: link's words: alone on its line, the whole card
// shows, never them.
const cardWords = "Carte"

// writtenOn is the line that says when a snapshot was read.
func writtenOn(t time.Time) string {
	t = t.In(time.Local)
	return fmt.Sprintf("Écrit le %02d/%02d/%d à %d h %02d.", t.Day(), int(t.Month()), t.Year(), t.Hour(), t.Minute())
}

// keepJSON is a block it cannot write: its JSON in a fence, and what code
// had read for it in another.
func keepJSON(inner []byte, sn *Snapshot) string {
	out := fenced(strings.TrimRight(string(inner), "\r\n") + "\n")
	if sn != nil && (len(sn.Values) > 0 || len(sn.Notes) > 0) {
		kept := map[string]any{}
		if len(sn.Values) > 0 {
			kept["values"] = sn.Values
		}
		if len(sn.Notes) > 0 {
			kept["notes"] = sn.Notes
		}
		if !sn.Read.IsZero() {
			kept["read"] = sn.Read.In(time.Local).Format(time.RFC3339)
		}
		raw, _ := json.MarshalIndent(kept, "", "  ")
		out += "\n" + fenced(string(raw)+"\n")
	}
	return out
}

// fenced is JSON in a fence longer than any run of backticks in it.
func fenced(text string) string {
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	return fence + "json\n" + text + fence + "\n"
}

// longestRun is the longest run of c in s.
func longestRun(s string, c byte) int {
	most, n := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			n++
			most = max(most, n)
		} else {
			n = 0
		}
	}
	return most
}

// jsonOf is a value as encoding/json takes it.
func jsonOf(v any) any {
	switch x := v.(type) {
	case *jobj:
		m := map[string]any{}
		for _, k := range x.keys {
			m[k] = jsonOf(x.vals[k])
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, it := range x {
			out[i] = jsonOf(it)
		}
		return out
	}
	return v
}

// fill puts each source's last value at /<name>, its items' notes on its
// items; it gives when the values were read.
func (c *conv) fill(sn *Snapshot) time.Time {
	if sn == nil {
		return time.Time{}
	}
	for _, name := range c.sourceNames() {
		raw, ok := sn.Values[name]
		if !ok {
			continue
		}
		v, err := decodeJSON(raw)
		if err != nil {
			continue
		}
		c.root.set(name, v)
		src := c.sources[name]
		if src.Items == nil {
			continue
		}
		notes := map[string]*ItemNote{}
		for i, n := range sn.Notes[name] {
			notes[n.Item] = &sn.Notes[name][i]
		}
		for _, it := range c.sourceItems(name) {
			o, ok := it.(*jobj)
			if !ok {
				continue
			}
			id := textOf(dotted(o, src.Items.ID))
			info := &itemOf{src: src}
			if id != "" {
				info.ref = c.book.label(src.server(), id)
			}
			if n := notes[id]; n != nil && id != "" {
				info.note = n
				o.set("_note", n.Note)
				o.set("_state", n.State)
				o.set("_group", n.Group)
				if n.Tainted {
					c.external = true
				}
			}
			c.items[o] = info
		}
	}
	return sn.Read
}

func (c *conv) sourceNames() []string {
	var out []string
	for k := range c.sources {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// sourceItems are the items a source lists, from its last value.
func (c *conv) sourceItems(name string) []any {
	src := c.sources[name]
	v := c.root.get(name)
	if src.Items != nil {
		v = dotted(v, src.Items.Path)
	}
	l, _ := v.([]any)
	return l
}

// dotted reads a dotted path (a source's items' fields: "from.name").
func dotted(v any, path string) any {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	if path == "" {
		return v
	}
	for k := range strings.SplitSeq(path, ".") {
		switch x := v.(type) {
		case *jobj:
			v = x.get(k)
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

// fail says the block cannot be written; the first reason stays.
func (c *conv) fail(why string) {
	if c.failed == "" {
		c.failed = why
	}
}

// ---- Values ----

// lookup reads a binding's path: from the data's root when it starts
// with /, else from the item of a list it is drawn for. A token may end
// with filters on a list's items: [field=value], [field!=value].
func (c *conv) lookup(scope any, path string) any {
	cur := scope
	if strings.HasPrefix(path, "/") {
		cur, path = c.root, path[1:]
	}
	if path == "" {
		return cur
	}
	for tok := range strings.SplitSeq(path, "/") {
		base, filters, _ := strings.Cut(tok, "[")
		base = strings.ReplaceAll(strings.ReplaceAll(base, "~1", "/"), "~0", "~")
		if base != "" {
			switch x := cur.(type) {
			case *jobj:
				cur = x.get(base)
			case []any:
				i, err := strconv.Atoi(base)
				if err != nil || i < 0 || i >= len(x) {
					return nil
				}
				cur = x[i]
			default:
				return nil
			}
		}
		if filters != "" {
			cur = c.filter(cur, "["+filters)
		}
	}
	return cur
}

// filter keeps a list's items whose fields hold the filters.
func (c *conv) filter(v any, filters string) any {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []any{}
	for _, it := range l {
		keep := true
		for f := range strings.SplitSeq(strings.Trim(filters, "[]"), "][") {
			field, value, ok := strings.Cut(f, "=")
			if !ok {
				c.fail("a filter " + f)
				return nil
			}
			field, not := strings.CutSuffix(strings.TrimSpace(field), "!")
			got := ""
			if o, ok := it.(*jobj); ok {
				got = textOf(o.get(strings.TrimSpace(field)))
			}
			if (got == strings.TrimSpace(value)) == not {
				keep = false
			}
		}
		if keep {
			out = append(out, it)
		}
	}
	return out
}

// value is a property's value: a literal, or what its binding reads. A
// function call cannot be written.
func (c *conv) value(scope, prop any) any {
	o, ok := prop.(*jobj)
	if !ok {
		return prop
	}
	if p, ok := o.get("@path").(string); ok && len(o.keys) == 1 {
		return c.lookup(scope, p)
	}
	if p, ok := o.get("path").(string); ok && len(o.keys) == 1 {
		return c.lookup(scope, p)
	}
	c.fail("a computed value")
	return nil
}

// text is a property as words, escaped.
func (c *conv) text(scope, prop any) string { return esc(textOf(c.value(scope, prop))) }

// textOf is a value as words: a list or an object is none.
func textOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "oui"
		}
		return "non"
	}
	return ""
}

var escaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`,
	"|", `\|`, "~", `\~`, "📅", "", "\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

// esc is words as a line's text: nothing in them reads as a link, code,
// HTML, a cell or a date, nor as a heading or an item when they start a
// line; one line.
func esc(s string) string {
	s = strings.TrimSpace(escaper.Replace(s))
	if m := leadRe.FindStringSubmatchIndex(s); m != nil {
		s = s[:m[3]-1] + `\` + s[m[3]-1:]
	}
	return s
}

// leadRe is the start of a line that reads as a heading or an item: its
// last mark is escaped.
var leadRe = regexp.MustCompile(`^(#{1,6}|[-+]|\d+[.)])(?:\s|$)`)

// Words are someone else's words as a line's text of a page, escaped as
// the conversion writes them (esc).
func Words(s string) string { return esc(s) }

// LinkWords are someone else's words as a link's text: Words, no bracket.
func LinkWords(s string) string { return label(esc(s)) }

// linkIn tells whether words hold a link: a bracket not escaped.
func linkIn(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[', ']':
			return true
		}
	}
	return false
}

// label is words a link's brackets hold: no bracket in them.
func label(s string) string {
	return strings.NewReplacer(`\[`, "(", `\]`, ")", "[", "(", "]", ")").Replace(s)
}

// ---- Components ----

// render writes a component, for the item of a list it is drawn for
// (scope), as blocks of Markdown.
func (c *conv) render(id string, scope any, depth int) []mdBlock {
	comp := c.comps[id]
	if comp == nil || depth > 40 {
		c.fail("no component " + id)
		return nil
	}
	kind, _ := comp.get("component").(string)
	para := func(s string) []mdBlock {
		if s == "" {
			return nil
		}
		return []mdBlock{{lines: []string{s}}}
	}
	switch kind {
	case "Text":
		t := c.text(scope, comp.get("text"))
		switch comp.get("variant") {
		case "title":
			if t != "" {
				return para("## " + t)
			}
		case "subtitle":
			if t != "" {
				return para("### " + t)
			}
		}
		return para(t)
	case "Markdown":
		prop := comp.get("text")
		if _, bound := prop.(*jobj); bound {
			return c.paragraphs(textOf(c.value(scope, prop)))
		}
		md := strings.TrimSpace(textOf(prop))
		if md == "" {
			return nil
		}
		return []mdBlock{{lines: defang(strings.Split(md, "\n"))}}
	case "Image":
		// An address as written, or one of the node's own files (a page a
		// task captured: media:med_….png), kept by its name.
		u := textOf(c.value(scope, comp.get("url")))
		if !httpURL(u) && !mediaFile.MatchString(u) {
			return nil
		}
		return para("![" + label(c.text(scope, comp.get("alt"))) + "](" + u + ")")
	case "Icon", "Placeholder", "Divider":
		if kind == "Divider" {
			return para("---")
		}
		return nil
	case "Link":
		return para(c.link(comp, scope))
	case "Code":
		text := textOf(c.value(scope, comp.get("text")))
		lang, _ := comp.get("language").(string)
		if comp.get("mode") == "diff" {
			lang = "diff"
		}
		if f := strings.Fields(lang); len(f) > 0 {
			lang = f[0]
		}
		return []mdBlock{codeBlock(lang, text)}
	case "Row", "Column", "List", "Grid":
		return c.children(comp, scope, depth, kind)
	case "Card":
		var out []mdBlock
		if t := c.text(scope, comp.get("title")); t != "" {
			out = append(out, mdBlock{lines: []string{"### " + t}})
		}
		if child, ok := comp.get("child").(string); ok {
			out = append(out, c.render(child, scope, depth+1)...)
		}
		return out
	case "Tabs":
		var out []mdBlock
		tabs, _ := comp.get("tabs").([]any)
		for _, x := range tabs {
			tab, _ := x.(*jobj)
			if t := c.text(scope, tab.get("title")); t != "" {
				out = append(out, mdBlock{lines: []string{"### " + t}})
			}
			if child, ok := tab.get("child").(string); ok {
				out = append(out, c.render(child, scope, depth+1)...)
			}
		}
		return out
	case "Fold":
		line, detail := c.item(id, scope, depth)
		return []mdBlock{listItem("", line, detail)}
	case "Item":
		line, detail := c.item(id, scope, depth)
		return []mdBlock{listItem(c.box(scope), line, detail)}
	case "Button":
		if t := c.askText(comp); t != "" {
			return []mdBlock{{kind: "ask", lines: []string{"[" + label(t) + "](ask:)"}}}
		}
		return nil
	case "Asks":
		var asks []string
		items, _ := comp.get("items").([]any)
		for _, x := range items {
			t := textOf(x)
			if o, ok := x.(*jobj); ok {
				t = textOf(o.get("text"))
			}
			if t = oneLine(t); t != "" {
				asks = append(asks, "["+label(t)+"](ask:)")
			}
		}
		if len(asks) == 0 {
			return nil
		}
		return []mdBlock{{kind: "ask", lines: []string{strings.Join(asks, " · ")}}}
	case "Metric":
		return []mdBlock{{kind: "figure", lines: []string{c.metric(comp, scope)}}}
	case "Figures":
		var lines []string
		for _, f := range c.listOf(comp.get("items"), scope) {
			lines = append(lines, c.metric(f, f))
		}
		return []mdBlock{callout("figures", c.text(scope, comp.get("title")), lines)}
	case "Progress":
		return []mdBlock{{kind: "figure", lines: []string{c.progress(comp, scope)}}}
	case "Timeline":
		return []mdBlock{c.timeline(comp, scope)}
	case "Table":
		return c.table(comp, scope)
	case "Chart":
		return c.chart(comp, scope)
	case "Agenda":
		return []mdBlock{c.agenda(comp, scope)}
	case "Sections":
		return c.sections(comp, scope)
	case "Person":
		return []mdBlock{c.person(comp, scope)}
	case "Quote":
		return []mdBlock{c.quote(c.text(scope, comp.get("text")), c.text(scope, comp.get("from")), textOf(c.value(scope, comp.get("date"))), c.link(comp, scope))}
	case "Message":
		out := para(joinNonEmpty(" · ", bold(c.text(scope, comp.get("subject"))), c.text(scope, comp.get("from")), dayText(textOf(c.value(scope, comp.get("date"))))))
		body := c.text(scope, comp.get("body"))
		if body == "" {
			body = c.text(scope, comp.get("excerpt"))
		}
		if body != "" {
			out = append(out, mdBlock{lines: []string{"> " + body}})
		}
		return out
	case "Alert":
		typ := "info"
		switch comp.get("level") {
		case "success":
			typ = "success"
		case "warning", "error":
			typ = "warning"
		}
		var body []string
		if t := c.text(scope, comp.get("text")); t != "" {
			body = []string{t}
		}
		return []mdBlock{callout(typ, c.text(scope, comp.get("title")), body)}
	}
	c.fail("a component " + kind)
	return nil
}

// paragraphs are someone else's text, escaped, a paragraph a line.
func (c *conv) paragraphs(s string) []mdBlock {
	var out []mdBlock
	for l := range strings.SplitSeq(s, "\n") {
		if l = esc(l); l != "" {
			out = append(out, mdBlock{lines: []string{l}})
		}
	}
	return out
}

// defang keeps an author's Markdown, but for Eriphe's links in it: those
// are written by an author, never by a conversion.
func defang(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		toks := inlineTokens(l)
		for k := len(toks) - 1; k >= 0; k-- {
			if toks[k].kind != "" {
				l = l[:toks[k].start] + `\` + l[toks[k].start:]
			}
		}
		out[i] = l
	}
	return out
}

// mediaFile is one of the node's own files, by its name (nodestore's).
var mediaFile = regexp.MustCompile(`^media:med_[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

func httpURL(u string) bool {
	return (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) && !strings.ContainsAny(u, " \t\r\n<>()[]")
}

// link is a Link: its address as written (or a field that is one), never
// built.
func (c *conv) link(comp *jobj, scope any) string {
	u := textOf(c.value(scope, comp.get("url")))
	words := c.text(scope, comp.get("label"))
	if !httpURL(u) {
		return words
	}
	if words == "" {
		words = esc(u)
	}
	return "[" + label(words) + "](" + u + ")"
}

// askText is what a Button asks: the words its ask sends.
func (c *conv) askText(comp *jobj) string {
	a, _ := comp.get("action").(*jobj)
	ev, _ := a.get("event").(*jobj)
	ctx, _ := ev.get("context").(*jobj)
	if t := oneLine(textOf(ctx.get("text"))); t != "" && ev.get("name") == "ask" {
		return t
	}
	return oneLine(textOf(comp.get("label")))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// children writes a container's children in order; a List's are items.
func (c *conv) children(comp *jobj, scope any, depth int, kind string) []mdBlock {
	ids, elems, tmpl := c.childList(comp.get("children"), scope)
	var out []mdBlock
	if tmpl != "" {
		var items []mdBlock
		for _, el := range elems {
			line, detail := c.item(tmpl, el, depth+1)
			items = append(items, listItem(c.box(el), line, detail))
		}
		if len(items) == 0 {
			return c.paragraphs(textOf(c.value(scope, comp.get("emptyText"))))
		}
		return []mdBlock{joinItems(items)}
	}
	if kind == "List" {
		var items []mdBlock
		for _, id := range ids {
			line, detail := c.item(id, scope, depth+1)
			items = append(items, listItem("", line, detail))
		}
		if len(items) > 0 {
			return []mdBlock{joinItems(items)}
		}
		return nil
	}
	if kind == "Row" && c.inlineAll(ids) {
		var parts []string
		for _, id := range ids {
			if s := c.inline(id, scope, depth+1); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 && !c.allKind(ids, "Metric", "Button") {
			return []mdBlock{{lines: []string{strings.Join(parts, " · ")}}}
		}
	}
	for _, id := range ids {
		out = append(out, c.render(id, scope, depth+1)...)
	}
	return out
}

// childList reads a ChildList: ids, or a template drawn for each item of
// a list (its component's id, and the items).
func (c *conv) childList(v, scope any) (ids []string, elems []any, tmpl string) {
	switch x := v.(type) {
	case []any:
		for _, it := range x {
			if s, ok := it.(string); ok {
				ids = append(ids, s)
			}
		}
	case *jobj:
		tmpl, _ = x.get("componentId").(string)
		path, _ := x.get("path").(string)
		if tmpl == "" {
			c.fail("a template without its component")
			return
		}
		elems, _ = c.lookup(scope, path).([]any)
	}
	return
}

// listOf is a list property: written, or bound.
func (c *conv) listOf(prop, scope any) []*jobj {
	v := c.value(scope, prop)
	if s, ok := v.(string); ok { // a source's name: its items
		if _, ok := c.sources[s]; ok {
			v = c.sourceItems(s)
		}
	}
	l, _ := v.([]any)
	var out []*jobj
	for _, it := range l {
		if o, ok := it.(*jobj); ok {
			out = append(out, o)
		}
	}
	return out
}

// inlineAll: every one of these components is words on a line.
func (c *conv) inlineAll(ids []string) bool {
	for _, id := range ids {
		switch c.comps[id].get("component") {
		case "Text", "Link", "Icon", "Metric", "Button":
		default:
			return false
		}
	}
	return len(ids) > 0
}

// allKind: every one of these components is of one of the kinds.
func (c *conv) allKind(ids []string, kinds ...string) bool {
	for _, id := range ids {
		if k, _ := c.comps[id].get("component").(string); !slices.Contains(kinds, k) {
			return false
		}
	}
	return true
}

// inline is a component as words on a line ("" for none).
func (c *conv) inline(id string, scope any, depth int) string {
	comp := c.comps[id]
	switch comp.get("component") {
	case "Text":
		return c.text(scope, comp.get("text"))
	case "Link":
		return c.link(comp, scope)
	case "Metric":
		return strings.TrimPrefix(c.metric(comp, scope), "- ")
	case "Button":
		if t := c.askText(comp); t != "" {
			return "[" + label(t) + "](ask:)"
		}
		return ""
	case "Icon":
		return ""
	}
	bs := c.render(id, scope, depth)
	if len(bs) == 1 && len(bs[0].lines) == 1 {
		return bs[0].lines[0]
	}
	return ""
}

// box is a list item's box, from its note's state: to do, or done.
func (c *conv) box(scope any) string {
	o, _ := scope.(*jobj)
	info := c.items[o]
	if info == nil || info.note == nil {
		return ""
	}
	switch info.note.State {
	case "act":
		return "[ ] "
	case "done":
		return "[x] "
	}
	return ""
}

// item writes a component as a list item: its line, and its detail.
func (c *conv) item(id string, scope any, depth int) (string, []mdBlock) {
	o, _ := scope.(*jobj)
	info := c.items[o]
	comp := c.comps[id]
	if comp == nil && (id != "" || info == nil) {
		c.fail("no component " + id)
		return "", nil
	}
	var title, rest []string // the line's words: what it is, then what is said of it
	var detail []mdBlock
	switch comp.get("component") {
	case nil: // a source's item, as code drew it: its title and detail
		title = append(title, esc(textOf(dotted(o, itemField(info, "title")))), esc(textOf(dotted(o, itemField(info, "detail")))))
	case "Item":
		title = append(title, c.text(scope, comp.get("title")), c.text(scope, comp.get("detail")))
		if n := c.text(scope, comp.get("note")); n != "" {
			rest = append(rest, n)
		}
		if due := textOf(c.value(scope, comp.get("due"))); len(due) >= 10 && realDate(due[:10]) {
			rest = append(rest, "📅 "+due[:10])
		} else if due != "" {
			rest = append(rest, esc(due))
		}
		if m := c.text(scope, comp.get("mark")); m != "" {
			rest = append(rest, m)
		}
	case "Fold":
		label := c.text(scope, comp.get("label"))
		if list, ok := comp.get("list").(string); ok {
			label = strings.ReplaceAll(label, "{n}", strconv.Itoa(c.rows(list, scope)))
		}
		title = append(title, label)
		if child, ok := comp.get("child").(string); ok {
			detail = append(detail, c.render(child, scope, depth+1)...)
		}
		if aside, ok := comp.get("aside").(string); ok {
			detail = append(detail, c.render(aside, scope, depth+1)...)
		}
	case "Card":
		title = append(title, c.text(scope, comp.get("title")))
		if child, ok := comp.get("child").(string); ok {
			detail = append(detail, c.render(child, scope, depth+1)...)
		}
	case "Row", "Column":
		ids, _, _ := c.childList(comp.get("children"), scope)
		for k, cid := range ids {
			if s := c.inline(cid, scope, depth+1); s != "" && (comp.get("component") == "Row" || k == 0) && c.inlineAll([]string{cid}) {
				title = append(title, s)
				continue
			}
			detail = append(detail, c.render(cid, scope, depth+1)...)
		}
	default:
		if s := c.inline(id, scope, depth); s != "" {
			title = append(title, s)
		} else {
			detail = c.render(id, scope, depth)
		}
	}
	words := joinNonEmpty(" · ", title...)
	if words == "" && info != nil {
		// A source's item its template gave no words: its own.
		words = joinNonEmpty(" · ", esc(textOf(dotted(o, itemField(info, "title")))), esc(textOf(dotted(o, itemField(info, "detail")))))
	}
	if info != nil && info.ref != "" {
		if words == "" || linkIn(words) {
			words = joinNonEmpty(" · ", words, "["+label(esc(info.ref))+"]["+info.ref+"]")
		} else {
			words = "[" + label(words) + "][" + info.ref + "]"
		}
	}
	if info != nil && info.note != nil {
		if n := esc(info.note.Note); n != "" && !slices.Contains(rest, n) {
			rest = append([]string{n}, rest...)
		}
	}
	line := words
	if len(rest) > 0 {
		if line == "" {
			line = strings.Join(rest, " · ")
		} else {
			line += " : " + strings.Join(rest, " · ")
		}
	}
	if line == "" && len(detail) > 0 && len(detail[0].lines) == 1 && detail[0].kind == "" {
		line, detail = detail[0].lines[0], detail[1:]
	}
	if line == "" {
		line = "…"
	}
	return line, detail
}

// itemField is the field a source says its items' title or detail is in.
func itemField(info *itemOf, which string) string {
	if info.src.Items == nil {
		return ""
	}
	if which == "title" {
		return info.src.Items.Title
	}
	return info.src.Items.Detail
}

// rows counts the items a List shows: those of its template's list.
func (c *conv) rows(list string, scope any) int {
	comp := c.comps[list]
	_, elems, tmpl := c.childList(comp.get("children"), scope)
	if tmpl == "" {
		ids, _, _ := c.childList(comp.get("children"), scope)
		return len(ids)
	}
	return len(elems)
}

// listItem is an item: « - » and its box, its line, its detail indented.
func listItem(box, line string, detail []mdBlock) mdBlock {
	b := mdBlock{lines: []string{"- " + box + line}}
	for i, d := range merge(detail) {
		if i > 0 {
			b.lines = append(b.lines, "")
		}
		for _, l := range d.lines {
			if l == "" {
				b.lines = append(b.lines, "")
				continue
			}
			b.lines = append(b.lines, "  "+l)
		}
	}
	return b
}

// joinItems makes one list of items.
func joinItems(items []mdBlock) mdBlock {
	var b mdBlock
	for _, it := range items {
		b.lines = append(b.lines, it.lines...)
	}
	return b
}

// metric is a figure's line: « Label : 18385 € (▲ 1.8 cette semaine) ».
func (c *conv) metric(comp *jobj, scope any) string {
	lbl := c.text(scope, comp.get("label"))
	unit := c.text(scope, comp.get("unit"))
	var value string
	if list, ok := comp.get("list").(string); ok {
		value = strconv.Itoa(c.rows(list, scope))
	} else {
		value = joinNonEmpty(" ", c.text(scope, comp.get("value")), unit)
		if on := c.follows(comp.get("value")); on != "" && value != "" {
			value = "[" + label(value) + "](" + on + ")"
		}
	}
	if d := textOf(c.value(scope, comp.get("delta"))); d != "" {
		arrow := "▲"
		if strings.HasPrefix(d, "-") {
			arrow, d = "▼", d[1:]
		}
		value = joinNonEmpty(" ", value, "("+joinNonEmpty(" ", arrow, d, c.text(scope, comp.get("deltaLabel")))+")")
	}
	if value == "" {
		value = "—"
	}
	if lbl == "" {
		return "- " + value
	}
	return "- " + lbl + " : " + value
}

// follows is the on: target of a value bound to a monitor's: the line
// follows the monitor's changes. "" for any other value.
func (c *conv) follows(prop any) string {
	o, ok := prop.(*jobj)
	if !ok {
		return ""
	}
	p, _ := o.get("@path").(string)
	name, field, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	src, ok := c.sources[name]
	if !ok || src.Monitor == "" || field != "value" || strings.ContainsAny(src.Monitor, " \t)(") {
		return ""
	}
	return "on:monitor.changed/" + src.Monitor
}

// progress is a work's state as a figure: its word, done on the total,
// its current step.
func (c *conv) progress(comp *jobj, scope any) string {
	lbl := c.text(scope, comp.get("label"))
	var v string
	if total := textOf(c.value(scope, comp.get("total"))); total != "" {
		v = cmpOr(textOf(c.value(scope, comp.get("value"))), "0") + "/" + total
	}
	v = joinNonEmpty(" · ", v, c.text(scope, comp.get("step")))
	if v == "" {
		return "- " + lbl
	}
	return "- " + cmpOr(lbl, "—") + " : " + v
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// timeline is a [!steps] callout, a step a task line.
func (c *conv) timeline(comp *jobj, scope any) mdBlock {
	var lines []string
	for _, it := range c.listOf(comp.get("items"), scope) {
		box := " "
		switch textOf(it.get("state")) {
		case "done":
			box = "x"
		case "current":
			box = "/"
		case "failed":
			box = "-"
		}
		l := "- [" + box + "] " + cmpOr(esc(textOf(it.get("label"))), "…")
		if d := textOf(it.get("date")); len(d) >= 10 && realDate(d[:10]) {
			l += " 📅 " + d[:10]
		}
		lines = append(lines, l)
		if d := esc(textOf(it.get("detail"))); d != "" {
			lines = append(lines, "  - "+d)
		}
	}
	return callout("steps", c.text(scope, comp.get("title")), lines)
}

// table is a Table: its title, then its rows in a Markdown table.
func (c *conv) table(comp *jobj, scope any) []mdBlock {
	rows := c.listOf(comp.get("rows"), scope)
	type col struct{ key, head string }
	var cols []col
	units, _ := comp.get("units").(*jobj)
	if cs, ok := comp.get("columns").([]any); ok {
		for _, x := range cs {
			o, _ := x.(*jobj)
			cols = append(cols, col{textOf(o.get("key")), textOf(o.get("label"))})
		}
	} else if len(rows) > 0 {
		for _, k := range rows[0].keys {
			if !strings.HasPrefix(k, "_") {
				cols = append(cols, col{k, k})
			}
		}
	}
	var out []mdBlock
	if t := c.text(scope, comp.get("title")); t != "" {
		out = append(out, mdBlock{lines: []string{bold(t)}})
	}
	if len(cols) == 0 || len(rows) == 0 {
		return append(out, c.paragraphs(textOf(c.value(scope, comp.get("emptyText"))))...)
	}
	head, delim := "|", "|"
	for _, k := range cols {
		h := esc(k.head)
		if u := textOf(units.get(k.key)); u != "" {
			h += " (" + esc(u) + ")"
		}
		head += " " + cmpOr(h, "—") + " |"
		delim += "---|"
	}
	t := mdBlock{lines: []string{head, delim}}
	for _, r := range rows {
		line := "|"
		for _, k := range cols {
			cell := esc(textOf(r.get(k.key)))
			if info := c.items[r]; info != nil && info.ref != "" && k == cols[0] && cell != "" {
				cell = "[" + label(cell) + "][" + info.ref + "]"
			}
			line += " " + cell + " |"
		}
		t.lines = append(t.lines, line)
	}
	return append(out, t)
}

// chart is a [!chart] callout: a table, its x first, a column a series.
func (c *conv) chart(comp *jobj, scope any) []mdBlock {
	series := c.listOf(comp.get("series"), scope)
	if len(series) == 0 {
		return nil
	}
	unit := c.text(scope, comp.get("unit"))
	var xs []string
	cells := map[string][]string{}
	head, delim := "| x |", "|---|"
	for i, s := range series {
		h := cmpOr(esc(textOf(s.get("name"))), "—")
		if unit != "" {
			h += " (" + unit + ")"
		}
		head += " " + h + " |"
		delim += "---|"
		pts, _ := s.get("points").([]any)
		for _, p := range pts {
			o, _ := p.(*jobj)
			x := esc(textOf(o.get("x")))
			if _, ok := cells[x]; !ok {
				xs = append(xs, x)
				cells[x] = make([]string, len(series))
			}
			cells[x][i] = textOf(o.get("y"))
		}
	}
	lines := []string{head, delim}
	for _, x := range xs {
		lines = append(lines, "| "+x+" | "+strings.Join(cells[x], " | ")+" |")
	}
	kind, _ := comp.get("kind").(string)
	typ := "chart"
	if kind == "bar" || kind == "stack" || kind == "line" {
		typ += "|" + kind
	}
	return []mdBlock{callout(typ, c.text(scope, comp.get("title")), lines)}
}

var (
	frDays   = []string{"Dimanche", "Lundi", "Mardi", "Mercredi", "Jeudi", "Vendredi", "Samedi"}
	frMonths = []string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
)

// agenda is an [!agenda] callout: a heading a day, an event a line.
func (c *conv) agenda(comp *jobj, scope any) mdBlock {
	type ev struct {
		day, line string
		sort      string
	}
	var evs []ev
	for _, e := range c.listOf(comp.get("events"), scope) {
		start := textOf(e.get("start"))
		var day, at string
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			day, at = t.Format(time.DateOnly), t.Format("15:04")
			if t2, err := time.Parse(time.RFC3339, textOf(e.get("end"))); err == nil && t2.Format(time.DateOnly) == day {
				at += "–" + t2.Format("15:04")
			}
		} else if len(start) >= 10 && realDate(start[:10]) {
			day = start[:10]
		} else {
			continue
		}
		if e.get("allDay") == true {
			at = ""
		}
		title := cmpOr(esc(textOf(e.get("title"))), "…")
		if info := c.items[e]; info != nil && info.ref != "" {
			title = "[" + label(title) + "][" + info.ref + "]"
		}
		l := joinNonEmpty(" ", at, title)
		if p := esc(textOf(e.get("place"))); p != "" {
			l += " · " + p
		}
		switch strings.ToLower(textOf(e.get("status"))) {
		case "needs-action", "needsaction":
			l += " (à répondre)"
		case "tentative":
			l += " (provisoire)"
		case "declined", "cancelled", "canceled":
			l += " (annulé)"
		}
		evs = append(evs, ev{day, l, day + " " + at})
	}
	slices.SortStableFunc(evs, func(a, b ev) int { return strings.Compare(a.sort, b.sort) })
	var lines []string
	last := ""
	for _, e := range evs {
		if e.day != last {
			if last != "" {
				lines = append(lines, "")
			}
			t, _ := time.Parse(time.DateOnly, e.day)
			lines = append(lines, fmt.Sprintf("### %s %d %s %d", frDays[t.Weekday()], t.Day(), frMonths[t.Month()-1], t.Year()))
			last = e.day
		}
		lines = append(lines, "- "+e.line)
	}
	return callout("agenda", "", lines)
}

// sections writes a source's items by the groups of their notes: the
// groups with something to do first, an item a line.
func (c *conv) sections(comp *jobj, scope any) []mdBlock {
	var out []mdBlock
	if t := c.text(scope, comp.get("title")); t != "" {
		out = append(out, mdBlock{lines: []string{"## " + t}})
	}
	var groups []string
	byGroup := map[string][]mdBlock{}
	act := map[string]bool{}
	for _, it := range c.listOf(comp.get("items"), scope) {
		info := c.items[it]
		g := ""
		if info != nil && info.note != nil {
			g = info.note.Group
			act[g] = act[g] || info.note.State == "act"
		}
		if _, ok := byGroup[g]; !ok {
			groups = append(groups, g)
		}
		line, _ := c.item("", it, 0)
		byGroup[g] = append(byGroup[g], listItem(c.box(it), line, nil))
	}
	slices.SortStableFunc(groups, func(a, b string) int {
		switch {
		case a == "" || b == "":
			return boolCmp(a == "", b == "")
		default:
			return boolCmp(!act[a], !act[b])
		}
	})
	for _, g := range groups {
		if g != "" {
			out = append(out, mdBlock{lines: []string{"### " + esc(g)}})
		} else if len(groups) > 1 {
			out = append(out, mdBlock{lines: []string{"### Autres"}})
		}
		out = append(out, joinItems(byGroup[g]))
	}
	return out
}

func boolCmp(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// person is a [!person] callout.
func (c *conv) person(comp *jobj, scope any) mdBlock {
	var lines []string
	if d := c.text(scope, comp.get("detail")); d != "" {
		lines = append(lines, "- Rôle : "+d)
	}
	for _, f := range c.listOf(comp.get("facts"), scope) {
		lines = append(lines, "- "+joinNonEmpty(" : ", esc(textOf(f.get("label"))), esc(textOf(f.get("value")))))
	}
	if n := c.text(scope, comp.get("note")); n != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, n)
	}
	return callout("person", c.text(scope, comp.get("name")), lines)
}

// quote is someone else's words, and who said them, when.
func (c *conv) quote(text, from, date, link string) mdBlock {
	b := mdBlock{lines: []string{"> " + cmpOr(text, "…")}}
	if link != "" && strings.Contains(link, "](") {
		b.lines = append(b.lines, ">", "> "+link)
	}
	if by := joinNonEmpty(", ", from, dayText(date)); by != "" {
		b.lines = append(b.lines, "> — "+by)
	}
	return b
}

// dayText is an ISO date as people write it (26/11/2026); words as they
// are otherwise.
func dayText(s string) string {
	if len(s) >= 10 && realDate(s[:10]) {
		t, _ := time.Parse(time.DateOnly, s[:10])
		return t.Format("02/01/2006")
	}
	return esc(s)
}

// callout is a callout: its head, then each line behind « > ».
func callout(typ, title string, lines []string) mdBlock {
	b := mdBlock{lines: []string{strings.TrimRight("> [!"+typ+"] "+title, " ")}}
	for _, l := range lines {
		if l == "" {
			b.lines = append(b.lines, ">")
			continue
		}
		b.lines = append(b.lines, "> "+l)
	}
	return b
}

// codeBlock is a fenced code block, its fence longer than any run of
// backticks in it.
func codeBlock(lang, text string) mdBlock {
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	b := mdBlock{lines: []string{fence + lang}}
	b.lines = append(b.lines, strings.Split(strings.TrimRight(text, "\n"), "\n")...)
	return mdBlock{lines: append(b.lines, fence)}
}

func bold(s string) string {
	if s == "" {
		return ""
	}
	return "**" + s + "**"
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// merge joins asks side by side on one line, and figures one after the
// other in one [!figures] callout.
func merge(bs []mdBlock) []mdBlock {
	var out []mdBlock
	for _, b := range bs {
		if len(b.lines) == 0 {
			continue
		}
		n := len(out)
		switch {
		case b.kind == "ask" && n > 0 && out[n-1].kind == "ask":
			out[n-1].lines[0] += " · " + b.lines[0]
		case b.kind == "figure" && n > 0 && out[n-1].kind == "figure":
			out[n-1].lines = append(out[n-1].lines, "> "+b.lines[0])
		case b.kind == "figure":
			out = append(out, mdBlock{kind: "figure", lines: []string{"> [!figures]", "> " + b.lines[0]}})
		default:
			out = append(out, mdBlock{kind: b.kind, lines: slices.Clone(b.lines)})
		}
	}
	return out
}

// joinBlocks writes blocks, a blank line between two.
func joinBlocks(bs []mdBlock) string {
	var b strings.Builder
	for i, x := range bs {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, l := range x.lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}
