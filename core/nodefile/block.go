package nodefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// BlockRe is a block's name: lowercase letters, digits and _, 32 at most.
var BlockRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)

// SourceRe is a source's name, its place /<name> in the block's data.
var SourceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// MarksKey is where a block's components read the user's marks:
// /marks/<list>/<item> is "keep", "drop", "done" or nothing. No data key
// and no source is so named.
const MarksKey = "marks"

// SnapshotAt is the key of a snapshot that says when it was read; no
// source is so named.
const SnapshotAt = "at"

// The marks the user may set.
const (
	MarkKeep = "keep"
	MarkDrop = "drop"
	MarkDone = "done"
)

// fenceOpen starts a block's fence, its name after a space.
const fenceOpen = "```eriphe:block"

// CardRe is a card block's ref: a proposal's kind and its number
// (view:3, memory:12). What the card says is never in the block: code
// reads it from its records.
var CardRe = regexp.MustCompile(`^[a-z][a-z_]{0,31}:[1-9][0-9]{0,17}$`)

// Block is a named block of a node: components shown with their data and
// live sources, and code's fields (snapshot, marks, external). A card
// block holds only the ref of one of the node's cards (Card): where its
// author places it in the text; code draws the card from its records.
type Block struct {
	Name       string
	Card       string            // a card block: the card's ref, and nothing else
	Components json.RawMessage   // a flat list, one with id root
	Data       json.RawMessage   // an object, nil when it has none
	Sources    map[string]Source // live sources read by code, at /<name>
	Actions    map[string]Action // what the user may run on the items of a source, by name
	Snapshot   json.RawMessage   // code's: the sources' last good values, an object
	Marks      Marks             // code's: the user's marks
	External   bool              // code's: it may show someone else's words

	raw   []byte // the block as read, fences included; nil once completed
	canon []byte // its canonical form when read
	has   has    // the code's fields it said, as read
}

// has tells which of code's fields a block said, and whether it said a
// card.
type has struct{ snapshot, marks, external, completed, card bool }

// Marks are the user's marks on what a block shows: by list, then by
// item, "keep", "drop" or "done".
type Marks map[string]map[string]string

// Source is a live source, read by code: a tool, a resource or a monitor.
type Source struct {
	Tool     string         `json:"tool,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Resource string         `json:"resource,omitempty"`
	URI      string         `json:"uri,omitempty"`
	Monitor  string         `json:"monitor,omitempty"`
	Items    *Items         `json:"items,omitempty"`
	Every    int            `json:"every,omitempty"` // minutes between two reads, 1 to 1440; 0: the default
}

// Action is what the user may run on the items of a source they pick in
// a list: a tool with fixed args, the ids they picked in its Items
// parameter. The author names it (Label, a hint only); what its button
// says is code's, from the tool and its args. The click is the user's own
// gesture (the core's web_list_actions.go).
type Action struct {
	Label  string         `json:"label,omitempty"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args,omitempty"`
	Items  string         `json:"items"`
	Source string         `json:"source"`
}

// ActionRe is an action's name.
var ActionRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// ActionLabelMax is the most runes of an action's label.
const ActionLabelMax = 60

// Items says where a listing source's items are and what each shows.
type Items struct {
	Path   string `json:"path,omitempty"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// BlockError is what is wrong with a block, by its name.
type BlockError struct {
	Name     string
	Problems []string
	Err      error // ErrCodeField when a code's field was written
}

func (e *BlockError) Unwrap() error { return e.Err }

func (e *BlockError) Error() string {
	return "block " + e.Name + ": " + strings.Join(e.Problems, "; ")
}

func blockErr(name string, problems ...string) error {
	return &BlockError{Name: name, Problems: problems}
}

// wire is a block's JSON as read: code's fields as pointers, to tell
// those left out.
type wire struct {
	Card       string            `json:"card"`
	Components json.RawMessage   `json:"components"`
	Data       json.RawMessage   `json:"data"`
	Sources    map[string]Source `json:"sources"`
	Actions    map[string]Action `json:"actions"`
	Snapshot   json.RawMessage   `json:"snapshot"`
	Marks      *Marks            `json:"marks"`
	External   *bool             `json:"external"`
}

// decodeBlock reads a block's JSON; short: a single component, an object
// or a list of one without root, is completed with id root.
func decodeBlock(name string, raw []byte, short bool) (*Block, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return nil, blockErr(name, "not a block's JSON: "+err.Error())
	}
	if dec.More() {
		return nil, blockErr(name, "one JSON object only")
	}
	b := &Block{Name: name, Card: w.Card, Components: w.Components, Sources: w.Sources, Actions: w.Actions}
	if !isEmpty(w.Data) {
		b.Data = w.Data
	}
	if !isEmpty(w.Snapshot) {
		b.Snapshot = w.Snapshot
	}
	b.has = has{snapshot: w.Snapshot != nil, marks: w.Marks != nil, external: w.External != nil, card: w.Card != ""}
	if w.Marks != nil && len(*w.Marks) > 0 {
		b.Marks = *w.Marks
	}
	if w.External != nil {
		b.External = *w.External
	}
	if short && b.Card == "" {
		b.Components = completeShort(b.Components)
	}
	b.has.completed = !bytes.Equal(b.Components, w.Components)
	return b, nil
}

// isEmpty: nothing, null or {}.
func isEmpty(raw json.RawMessage) bool {
	s := string(bytes.TrimSpace(raw))
	if s == "" || s == "null" {
		return true
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && len(m) == 0
}

// completeShort completes the short form: one component, alone or in a
// list, with no root, becomes the root.
func completeShort(raw json.RawMessage) json.RawMessage {
	var one map[string]any
	if json.Unmarshal(raw, &one) == nil {
		if one == nil {
			return raw
		}
		one["id"] = "root"
		out, _ := marshal([]any{one})
		return out
	}
	var list []map[string]any
	if json.Unmarshal(raw, &list) != nil || len(list) != 1 || list[0] == nil || list[0]["id"] == "root" {
		return raw
	}
	list[0]["id"] = "root"
	out, _ := marshal(list)
	return out
}

// marshal is JSON without HTML escapes.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// compact is raw JSON on one line.
func compact(raw json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// bytes is the block as written: as read when unchanged, else in its
// canonical form.
func (b *Block) bytes() ([]byte, error) {
	canon, err := b.canonical()
	if err != nil {
		return nil, err
	}
	if b.raw != nil && bytes.Equal(canon, b.canon) {
		return b.raw, nil
	}
	return canon, nil
}

// canonical is the block's canonical form: its fence, then one field a
// line, each component on its own line, the fields empty left out.
func (b *Block) canonical() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(fenceOpen + " " + b.Name + "\n{")
	if b.Card != "" {
		ref, err := marshal(b.Card)
		if err != nil {
			return nil, blockErr(b.Name, "card: "+err.Error())
		}
		out.WriteString("\"card\": ")
		out.Write(ref)
		if b.External {
			out.WriteString(",\n \"external\": true")
		}
		out.WriteString("}\n```\n")
		return out.Bytes(), nil
	}
	var comps []json.RawMessage
	if err := json.Unmarshal(b.Components, &comps); err != nil {
		return nil, blockErr(b.Name, "components: a list of components")
	}
	out.WriteString("\"components\": [")
	for i, c := range comps {
		one, err := compact(c)
		if err != nil {
			return nil, blockErr(b.Name, "components: "+err.Error())
		}
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString("\n  ")
		out.Write(one)
	}
	if len(comps) > 0 {
		out.WriteString("\n")
	}
	out.WriteString("]")
	field := func(key string, v any) error {
		var one []byte
		var err error
		if raw, ok := v.(json.RawMessage); ok {
			one, err = compact(raw)
		} else {
			one, err = marshal(v)
		}
		if err != nil {
			return blockErr(b.Name, key+": "+err.Error())
		}
		out.WriteString(",\n \"" + key + "\": ")
		out.Write(one)
		return nil
	}
	if b.Data != nil {
		if err := field("data", b.Data); err != nil {
			return nil, err
		}
	}
	if len(b.Sources) > 0 {
		if err := field("sources", b.Sources); err != nil {
			return nil, err
		}
	}
	if len(b.Actions) > 0 {
		if err := field("actions", b.Actions); err != nil {
			return nil, err
		}
	}
	if b.Snapshot != nil {
		if err := field("snapshot", b.Snapshot); err != nil {
			return nil, err
		}
	}
	if len(b.Marks) > 0 {
		if err := field("marks", b.Marks); err != nil {
			return nil, err
		}
	}
	if b.External {
		out.WriteString(",\n \"external\": true")
	}
	out.WriteString("}\n```\n")
	return out.Bytes(), nil
}

// parseBody cuts a body into text and blocks. A fence ```eriphe:block
// <name> at the start of a line opens a block, the next line ``` closes
// it; one inside another fenced code block is text.
func parseBody(body []byte, short bool) ([]segment, error) {
	var segs []segment
	var text bytes.Buffer
	flush := func() {
		if text.Len() > 0 {
			segs = append(segs, segment{text: text.String()})
			text.Reset()
		}
	}
	seen := map[string]bool{}
	fence := "" // the ordinary fenced code block the line is in
	for at := 0; at < len(body); {
		line, _, _ := cutLine(body[at:])
		t := trimEOL(line)
		if fence != "" {
			if closesFence(t, fence) {
				fence = ""
			}
			text.Write(line)
			at += len(line)
			continue
		}
		if rest, ok := strings.CutPrefix(t, fenceOpen); ok && (rest == "" || rest[0] == ' ') {
			name := strings.TrimSpace(rest)
			if !BlockRe.MatchString(name) {
				return nil, blockErr(name, "a block's name is lowercase letters, digits and _, 32 at most, after "+fenceOpen)
			}
			if seen[name] {
				return nil, blockErr(name, "a node has one block so named")
			}
			seen[name] = true
			start := at + len(line)
			end := -1
			for j := start; j < len(body); {
				l, _, _ := cutLine(body[j:])
				if trimEOL(l) == "```" {
					end = j
					at = j + len(l)
					break
				}
				j += len(l)
			}
			if end < 0 {
				return nil, blockErr(name, "no closing ``` line")
			}
			b, err := decodeBlock(name, body[start:end], short)
			if err != nil {
				return nil, err
			}
			raw := body[start-len(line) : at]
			if b.canon, err = b.canonical(); err != nil {
				return nil, err
			}
			if !b.has.completed {
				b.raw = raw // completed, it is written in its full form
			}
			flush()
			segs = append(segs, segment{block: b})
			continue
		}
		if f := opensFence(t); f != "" {
			fence = f
		}
		text.Write(line)
		at += len(line)
	}
	flush()
	return segs, nil
}

// opensFence is the fence a line opens (``` or ~~~, 3 or more, after up to
// 3 spaces); "" when it opens none.
func opensFence(t string) string {
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

// closesFence tells whether a line closes the fence: the same character,
// as many or more, and nothing else.
func closesFence(t, fence string) bool {
	s := strings.TrimLeft(t, " ")
	return len(t)-len(s) <= 3 && len(s) >= len(fence) && strings.Trim(s, fence[:1]) == ""
}

// Validate checks a block alone: its name, its components against the
// catalog (core/a2ui), its data, its sources, its bindings against its
// own data, sources and marks, and its marks.
func (b *Block) Validate() error {
	if !BlockRe.MatchString(b.Name) {
		return blockErr(b.Name, "a block's name is lowercase letters, digits and _, 32 at most")
	}
	if b.Card != "" || b.has.card {
		return b.validateCard()
	}
	var problems []string
	fail := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	var data map[string]any
	if b.Data != nil && json.Unmarshal(b.Data, &data) != nil {
		fail("data: an object")
	}
	for _, k := range slices.Sorted(maps.Keys(data)) {
		if k == MarksKey {
			fail("data /%s is code's: the user's marks", k)
		}
		if _, ok := b.Sources[k]; ok {
			fail("data /%s is the place of the source %s: write your values under another key", k, k)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(b.Sources)) {
		if err := b.Sources[name].check(name); err != "" {
			fail("%s", err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(b.Actions)) {
		for _, err := range b.Actions[name].check(name, b.Sources) {
			fail("%s", err)
		}
	}
	if b.Snapshot != nil {
		var snap map[string]json.RawMessage
		if json.Unmarshal(b.Snapshot, &snap) != nil {
			fail("snapshot: an object")
		}
	}
	for _, list := range slices.Sorted(maps.Keys(b.Marks)) {
		for _, item := range slices.Sorted(maps.Keys(b.Marks[list])) {
			if err := checkMark(list, item, b.Marks[list][item]); err != "" {
				fail("%s", err)
			}
		}
	}
	if len(problems) > 0 {
		return blockErr(b.Name, problems...)
	}
	sid := "block:" + b.Name
	errs := a2ui.ValidateSurface(a2ui.Surface{ID: sid, Components: b.Components, DataModel: b.Data})
	if len(errs) == 0 {
		errs = a2ui.CheckLinks(sid, b.Components)
	}
	for _, e := range errs {
		fail("%s: %s", e.Path, e.Message)
	}
	if len(problems) > 0 {
		return blockErr(b.Name, problems...)
	}
	for _, bound := range a2ui.Eriphe().BoundPaths(b.Components) {
		at := tokens(bound.Path)
		if len(at) == 0 {
			continue
		}
		if at[0] == MarksKey {
			continue // a mark the user has not set reads empty
		}
		if _, ok := b.Sources[at[0]]; ok {
			continue // read by code: checked when it is read
		}
		if ok, hint := lookup(data, at, bound.List); !ok {
			fail("/components%s: nothing at %s in the block's data%s: write it in data, or read it from a source", bound.At, bound.Path, hint)
			continue
		}
		if bound.Link && !linksOK(valuesAt(data, at)) {
			fail("/components%s: a Link opens a field that is already an http(s) address as a whole, and %s holds something else", bound.At, bound.Path)
		}
	}
	if len(problems) > 0 {
		return blockErr(b.Name, problems...)
	}
	return nil
}

// validateCard checks a card block: the ref of a card, and nothing else
// (no components, data, sources nor marks: what the card says, what its
// yes does and whether it waits are code's, read from its records).
func (b *Block) validateCard() error {
	var problems []string
	if !CardRe.MatchString(b.Card) {
		problems = append(problems, "card: the ref a propose tool gave (view:3, memory:12)")
	}
	if !isEmpty(b.Components) && string(bytes.TrimSpace(b.Components)) != "[]" || b.Data != nil || len(b.Sources) > 0 || len(b.Actions) > 0 || b.Snapshot != nil || len(b.Marks) > 0 {
		problems = append(problems, "a card block holds its card's ref only: {\"card\": \"view:3\"}; what the card says comes from it")
	}
	if len(problems) > 0 {
		return blockErr(b.Name, problems...)
	}
	return nil
}

// check is what is wrong with a source; "" when nothing.
func (s Source) check(name string) string {
	kinds := 0
	for _, k := range []string{s.Tool, s.Resource, s.Monitor} {
		if k != "" {
			kinds++
		}
	}
	switch {
	case !SourceRe.MatchString(name):
		return fmt.Sprintf("source %q: a name is lowercase letters, digits, _ and -, 40 at most", name)
	case name == MarksKey:
		return "source /" + MarksKey + " is code's: the user's marks"
	case name == SnapshotAt:
		return "source " + SnapshotAt + ": the name is code's, when its snapshot was read"
	case kinds != 1:
		return "source " + name + ": one of tool, resource or monitor"
	case s.Resource != "" && s.URI == "":
		return "source " + name + ": a resource needs its uri"
	case s.Resource == "" && s.URI != "":
		return "source " + name + ": uri goes with a resource"
	case s.Monitor != "" && (len(s.Args) > 0 || s.Items != nil || s.Every != 0):
		return "source " + name + ": a monitor takes no args, items nor every"
	case s.Every < 0 || s.Every > 1440:
		return "source " + name + ": every is 1 to 1440 minutes"
	case s.Items != nil && s.Items.ID == "":
		return "source " + name + ": items needs id, each item's id"
	}
	return ""
}

// check is what is wrong with an action, its shape alone: what its tool
// takes is checked against the catalog when an author writes it (the
// core's nodeGuard).
func (a Action) check(name string, sources map[string]Source) []string {
	var out []string
	at := "action " + name + ": "
	if !ActionRe.MatchString(name) {
		out = append(out, fmt.Sprintf("action %q: a name is lowercase letters, digits, _ and -, 40 at most", name))
	}
	if server, tool, ok := strings.Cut(a.Tool, "/"); !ok || server == "" || tool == "" || strings.Contains(tool, "/") {
		out = append(out, at+"tool is <server>/<tool>, as a source's")
	}
	if strings.TrimSpace(a.Items) == "" {
		out = append(out, at+"items names the tool's parameter that takes the ids the user picks")
	} else if _, ok := a.Args[a.Items]; ok {
		out = append(out, at+"args has no "+a.Items+": the ids the user picks go there")
	}
	if src, ok := sources[a.Source]; !ok {
		out = append(out, at+"source names one of the block's sources, the list it acts on")
	} else if src.Items == nil {
		out = append(out, at+"its source "+a.Source+" says no items (path, id): the user picks items by their id")
	}
	if utf8.RuneCountInString(a.Label) > ActionLabelMax || strings.ContainsAny(a.Label, "\n\r") {
		out = append(out, fmt.Sprintf("%slabel is one line, %d characters at most", at, ActionLabelMax))
	}
	return out
}

// checkMark is what is wrong with a mark; "" when nothing.
func checkMark(list, item, mark string) string {
	switch {
	case !a2ui.MarkIDOK(list):
		return fmt.Sprintf("marks: %q is not a list's id", list)
	case !a2ui.MarkIDOK(item):
		return fmt.Sprintf("marks: %q is not an item's id", item)
	case mark != MarkKeep && mark != MarkDrop && mark != MarkDone:
		return fmt.Sprintf("marks: %s/%s is keep, drop or done, not %q", list, item, mark)
	}
	return ""
}

// tokens are a JSON pointer's tokens.
func tokens(ptr string) []string {
	if ptr == "" || ptr == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts
}

// lookup tells whether a path leads somewhere in v ("*": in an item of a
// list, an empty list telling nothing); if not, a hint of what is there.
func lookup(v any, toks []string, list bool) (bool, string) {
	if len(toks) == 0 {
		if _, ok := v.([]any); list && !ok {
			return false, " (not a list)"
		}
		return true, ""
	}
	t, rest := toks[0], toks[1:]
	switch x := v.(type) {
	case map[string]any:
		if t == "*" {
			return false, " (an object, not a list)"
		}
		next, ok := x[t]
		if !ok {
			if len(x) == 0 {
				return false, ""
			}
			return false, " (there: " + strings.Join(slices.Sorted(maps.Keys(x)), ", ") + ")"
		}
		return lookup(next, rest, list)
	case []any:
		if t == "*" {
			if len(x) == 0 {
				return true, ""
			}
			hint := ""
			for _, it := range x {
				ok, h := lookup(it, rest, list)
				if ok {
					return true, ""
				}
				if hint == "" {
					hint = h
				}
			}
			return false, hint
		}
		i, err := strconv.Atoi(t)
		if err != nil || i < 0 || i >= len(x) {
			return false, " (a list: bind its items through a template)"
		}
		return lookup(x[i], rest, list)
	}
	return false, ""
}

// valuesAt are the values a path leads to in v, "*" standing for each
// item of a list.
func valuesAt(v any, toks []string) []any {
	if len(toks) == 0 {
		return []any{v}
	}
	t, rest := toks[0], toks[1:]
	switch x := v.(type) {
	case map[string]any:
		if next, ok := x[t]; ok {
			return valuesAt(next, rest)
		}
	case []any:
		if t == "*" {
			var out []any
			for _, it := range x {
				out = append(out, valuesAt(it, rest)...)
			}
			return out
		}
		if i, err := strconv.Atoi(t); err == nil && i >= 0 && i < len(x) {
			return valuesAt(x[i], rest)
		}
	}
	return nil
}

// linksOK tells whether the values bound to a Link's url are addresses it
// may open (a2ui.LinkURLOK), or empty: no link.
func linksOK(vs []any) bool {
	for _, v := range vs {
		if u, ok := v.(string); (!ok && v != nil) || (ok && u != "" && !a2ui.LinkURLOK(u)) {
			return false
		}
	}
	return true
}

// inFence tells whether a text ends inside an open fenced code block: a
// block written after it would read as text.
func inFence(text string) bool {
	fence := ""
	for line := range strings.Lines(text) {
		t := trimEOL([]byte(line))
		switch {
		case fence != "":
			if closesFence(t, fence) {
				fence = ""
			}
		default:
			fence = opensFence(t)
		}
	}
	return fence != ""
}

// Code's API: what only code writes.

// SetMark sets the user's mark on an item of a list a block shows; ""
// takes it away.
func (n *Node) SetMark(block, list, item, mark string) error {
	b := n.Block(block)
	if b == nil {
		return fmt.Errorf("no block %s", block)
	}
	if mark == "" {
		delete(b.Marks[list], item)
		if len(b.Marks[list]) == 0 {
			delete(b.Marks, list)
		}
		return nil
	}
	if err := checkMark(list, item, mark); err != "" {
		return blockErr(block, err)
	}
	if b.Marks == nil {
		b.Marks = Marks{}
	}
	if b.Marks[list] == nil {
		b.Marks[list] = map[string]string{}
	}
	b.Marks[list][item] = mark
	return nil
}

// SetSnapshot keeps the last good values of a block's sources: an
// object, nil to have none.
func (n *Node) SetSnapshot(block string, snap json.RawMessage) error {
	b := n.Block(block)
	if b == nil {
		return fmt.Errorf("no block %s", block)
	}
	if isEmpty(snap) {
		b.Snapshot = nil
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(snap, &m) != nil {
		return blockErr(block, "snapshot: an object")
	}
	b.Snapshot = snap
	return nil
}

// SetBlockExternal says whether a block may show someone else's words.
func (n *Node) SetBlockExternal(block string, external bool) error {
	b := n.Block(block)
	if b == nil {
		return fmt.Errorf("no block %s", block)
	}
	b.External = external
	return nil
}

// sameField tells whether two values of a code's field are alike, empty
// ones alike.
func sameField(a, b any) bool {
	if ra, ok := a.(json.RawMessage); ok {
		rb := b.(json.RawMessage)
		if isEmpty(ra) || isEmpty(rb) {
			return isEmpty(ra) && isEmpty(rb)
		}
		var x, y any
		json.Unmarshal(ra, &x)
		json.Unmarshal(rb, &y)
		return reflect.DeepEqual(x, y)
	}
	if ma, ok := a.(Marks); ok {
		mb := b.(Marks)
		if len(ma) == 0 || len(mb) == 0 {
			return len(ma) == 0 && len(mb) == 0
		}
		return reflect.DeepEqual(ma, mb)
	}
	return a == b
}

// ErrCodeField is why an author's write that changes a code's field is
// refused.
var ErrCodeField = errors.New("written by code only")
