// Package nodefile reads and writes a node of the graph as a file: YAML
// front matter, a markdown body, and typed blocks in fences
// ("```eriphe:block <name>"). The format is written down in
// docs/node-format.md.
//
// Parsing is lossless: a node parsed then written gives the same bytes.
// What changes through this package's API is written again in its
// canonical form; everything else keeps its bytes (front matter, text,
// each block).
//
// Some fields are code's only: a block's marks, snapshot and external,
// and the node's external. Code sets them with the methods of Node
// (SetMark, SetSnapshot, SetBlockExternal, Meta.External); an author's
// write goes through ParseAuthor, AuthorBody or PutAuthorBlock, which
// keep them when the author leaves them out and refuse a write that
// changes them.
package nodefile

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Lifetime says how long a node lives: work ends when it is done, a
// standing node (the day, the mails) stays.
type Lifetime string

const (
	Work     Lifetime = "work"
	Standing Lifetime = "standing"
)

// TitleMax is the most runes of a node's title.
const TitleMax = 120

// idRe is a node's id: lowercase letters and digits, no dash, so that a
// file name <slug>-<id>.md splits at its last dash.
var idRe = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

// IDOK tells whether s is a node's id.
func IDOK(s string) bool { return idRe.MatchString(s) }

// Meta is a node's front matter.
type Meta struct {
	ID       string
	Title    string
	Parent   string   // the parent's id, "" for a node at the top
	Links    []string // ids of the nodes it links to
	Lifetime Lifetime
	Goal     string // what the node is for, never shown to the user
	Created  time.Time
	Done     time.Time // zero while not done
	Archived time.Time // zero while not archived
	External bool      // code's: the node holds someone else's words
	// Extra are the fields this package does not know, kept in their
	// order and written back as they were.
	Extra []Field
}

// Field is a front-matter field this package does not know.
type Field struct {
	Key   string
	Value *yaml.Node
}

// known are the front matter's fields, in the order they are written.
var known = []string{"id", "title", "parent", "links", "lifetime", "goal", "created", "done", "archived", "external"}

// Node is a node's file, parsed.
type Node struct {
	Meta Meta

	segs []segment // the body: text and blocks, in order

	rawFront   []byte // the front matter as read, fences included
	canonFront []byte // its canonical form when read
	hasExt     bool   // the front matter said external (author writes)
}

// segment is a piece of the body: text as it is, or a block.
type segment struct {
	text  string
	block *Block
}

// Blocks are the node's blocks, in order.
func (n *Node) Blocks() []*Block {
	var out []*Block
	for _, s := range n.segs {
		if s.block != nil {
			out = append(out, s.block)
		}
	}
	return out
}

// Block is the block so named; nil when the node has none.
func (n *Node) Block(name string) *Block {
	for _, s := range n.segs {
		if s.block != nil && s.block.Name == name {
			return s.block
		}
	}
	return nil
}

// Text is the node's body without its blocks: its markdown text.
func (n *Node) Text() string {
	var b strings.Builder
	for _, s := range n.segs {
		b.WriteString(s.text)
	}
	return b.String()
}

// Parse reads a node's file and checks it: its front matter and each of
// its blocks. The file holds blocks in their full form.
func Parse(raw []byte) (*Node, error) {
	n, err := parse(raw, false)
	if err != nil {
		return nil, err
	}
	return n, n.Validate()
}

// parse reads a file; short: a block may be in its short form (an
// author's write), completed.
func parse(raw []byte, short bool) (*Node, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("a node is UTF-8 text")
	}
	front, yml, body, err := splitFront(raw)
	if err != nil {
		return nil, err
	}
	n := &Node{rawFront: front}
	if n.Meta, n.hasExt, err = decodeMeta(yml); err != nil {
		return nil, err
	}
	if n.canonFront, err = encodeMeta(n.Meta); err != nil {
		return nil, err
	}
	if n.segs, err = parseBody(body, short); err != nil {
		return nil, err
	}
	return n, nil
}

// Bytes writes the node: what was read as it was, what changed in its
// canonical form.
func (n *Node) Bytes() ([]byte, error) {
	var b bytes.Buffer
	front, err := encodeMeta(n.Meta)
	if err != nil {
		return nil, err
	}
	if n.rawFront != nil && bytes.Equal(front, n.canonFront) {
		b.Write(n.rawFront)
	} else {
		b.WriteString("---\n")
		b.Write(front)
		b.WriteString("---\n")
	}
	for _, s := range n.segs {
		if s.block == nil {
			b.WriteString(s.text)
			continue
		}
		out, err := s.block.bytes()
		if err != nil {
			return nil, err
		}
		b.Write(out)
	}
	return b.Bytes(), nil
}

// New is a new node with its front matter and a markdown body (blocks
// included, in their full form, with no code's field set).
func New(m Meta, body string) (*Node, error) {
	segs, err := parseBody([]byte(body), false)
	if err != nil {
		return nil, err
	}
	n := &Node{Meta: m, segs: segs}
	return n, n.Validate()
}

// Validate checks the front matter and each block.
func (n *Node) Validate() error {
	if err := n.Meta.validate(); err != nil {
		return err
	}
	for _, b := range n.Blocks() {
		if err := b.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (m Meta) validate() error {
	switch {
	case !IDOK(m.ID):
		return fmt.Errorf("front matter: id %q is lowercase letters and digits, 32 at most", m.ID)
	case strings.TrimSpace(m.Title) == "":
		return errors.New("front matter: title is needed")
	case strings.ContainsAny(m.Title, "\r\n") || utf8.RuneCountInString(m.Title) > TitleMax:
		return fmt.Errorf("front matter: title is one line of %d characters at most", TitleMax)
	case m.Parent != "" && !IDOK(m.Parent):
		return fmt.Errorf("front matter: parent %q is a node's id", m.Parent)
	case m.Parent == m.ID:
		return errors.New("front matter: a node is not its own parent")
	case m.Lifetime != Work && m.Lifetime != Standing:
		return fmt.Errorf("front matter: lifetime is %s or %s, not %q", Work, Standing, m.Lifetime)
	case m.Created.IsZero():
		return errors.New("front matter: created is needed")
	}
	for i, l := range m.Links {
		switch {
		case !IDOK(l):
			return fmt.Errorf("front matter: links: %q is not a node's id", l)
		case l == m.ID:
			return errors.New("front matter: links: a node does not link to itself")
		case slices.Contains(m.Links[:i], l):
			return fmt.Errorf("front matter: links: %s is there twice", l)
		}
	}
	return nil
}

// splitFront cuts a file into its front matter (fences included), the
// YAML inside, and its body.
func splitFront(raw []byte) (front, yml, body []byte, err error) {
	first, _, ok := cutLine(raw)
	if !ok || trimEOL(first) != "---" {
		return nil, nil, nil, errors.New("a node starts with its front matter, between two --- lines")
	}
	for at := len(first); at < len(raw); {
		line, _, _ := cutLine(raw[at:])
		if trimEOL(line) == "---" {
			end := at + len(line)
			return raw[:end], raw[len(first):at], raw[end:], nil
		}
		at += len(line)
	}
	return nil, nil, nil, errors.New("front matter: no closing --- line")
}

// cutLine cuts the first line, its \n included; ok false when b has no
// \n (line is then all of b).
func cutLine(b []byte) (line, rest []byte, ok bool) {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return b, nil, false
	}
	return b[:i+1], b[i+1:], true
}

// trimEOL is a line without its end and trailing blanks.
func trimEOL(line []byte) string { return strings.TrimRight(string(line), " \t\r\n") }

// decodeMeta reads the front matter's YAML; ext tells whether it says
// external.
func decodeMeta(yml []byte) (m Meta, ext bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(yml, &doc); err != nil {
		return m, false, fmt.Errorf("front matter: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return m, false, errors.New("front matter: a mapping of fields (id, title, …)")
	}
	top := doc.Content[0]
	seen := map[string]bool{}
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], top.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return m, false, fmt.Errorf("front matter: line %d: a field's name is plain text", k.Line)
		}
		if seen[k.Value] {
			return m, false, fmt.Errorf("front matter: %s is there twice", k.Value)
		}
		seen[k.Value] = true
		var err error
		switch k.Value {
		case "id":
			err = v.Decode(&m.ID)
		case "title":
			err = v.Decode(&m.Title)
		case "parent":
			err = v.Decode(&m.Parent)
		case "links":
			err = v.Decode(&m.Links)
		case "lifetime":
			err = v.Decode(&m.Lifetime)
		case "goal":
			err = v.Decode(&m.Goal)
		case "created":
			err = v.Decode(&m.Created)
		case "done":
			err = v.Decode(&m.Done)
		case "archived":
			err = v.Decode(&m.Archived)
		case "external":
			ext = true
			err = v.Decode(&m.External)
		default:
			m.Extra = append(m.Extra, Field{Key: k.Value, Value: v})
		}
		if err != nil {
			return m, false, fmt.Errorf("front matter: %s: %w", k.Value, err)
		}
	}
	return m, ext, nil
}

// encodeMeta is the front matter's canonical YAML: the known fields in
// their order, those empty left out, then the others as they were.
func encodeMeta(m Meta) ([]byte, error) {
	top := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, v any) error {
		var val yaml.Node
		if err := val.Encode(v); err != nil {
			return fmt.Errorf("front matter: %s: %w", key, err)
		}
		if key == "links" {
			val.Style = yaml.FlowStyle
		}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &val)
		return nil
	}
	for _, key := range known {
		var v any
		switch key {
		case "id":
			v = m.ID
		case "title":
			v = m.Title
		case "parent":
			v = nonEmpty(m.Parent)
		case "links":
			if len(m.Links) > 0 {
				v = m.Links
			}
		case "lifetime":
			v = string(m.Lifetime)
		case "goal":
			v = nonEmpty(m.Goal)
		case "created":
			v = m.Created
		case "done":
			v = nonZero(m.Done)
		case "archived":
			v = nonZero(m.Archived)
		case "external":
			if m.External {
				v = true
			}
		}
		if v == nil {
			continue
		}
		if err := add(key, v); err != nil {
			return nil, err
		}
	}
	for _, f := range m.Extra {
		if slices.Contains(known, f.Key) {
			return nil, fmt.Errorf("front matter: %s is a known field, not an extra one", f.Key)
		}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: f.Key}, f.Value)
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(top); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	enc.Close()
	return b.Bytes(), nil
}

func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nonZero(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
